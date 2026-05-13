// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package contentcache

import (
	"context"
	"fmt"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo"
	"github.com/kptdev/porch/pkg/repository"
)

var _ repository.ContentCache = &contentCache{}

type contentCache struct {
	cache cachetypes.Cache
}

// NewContentCache creates a ContentCache backed by the given Cache.
func NewContentCache(cache cachetypes.Cache) repository.ContentCache {
	return &contentCache{cache: cache}
}

func (c *contentCache) GetPackageContent(ctx context.Context, repoKey repository.RepositoryKey, pkg, workspace string) (repository.PackageContent, error) {
	pkgRev, err := c.findPackageRevision(ctx, repoKey, pkg, workspace)
	if err != nil {
		return nil, err
	}
	return &packageContentWrapper{inner: pkgRev}, nil
}

func (c *contentCache) CreateNewDraft(ctx context.Context, repoKey repository.RepositoryKey, pkgName, workspace, lifecycle string) (repository.PackageRevisionDraftSlim, error) {
	repo, err := c.getRepository(repoKey)
	if err != nil {
		return nil, err
	}

	obj := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    pkgName,
			WorkspaceName:  workspace,
			RepositoryName: repoKey.Name,
			Lifecycle:      porchapi.PackageRevisionLifecycle(lifecycle),
		},
	}

	draft, err := repo.CreatePackageRevisionDraft(ctx, obj)
	if err != nil {
		return nil, err
	}
	return &draftSlimWrapper{inner: draft}, nil
}

func (c *contentCache) CreateDraftFromExisting(ctx context.Context, repoKey repository.RepositoryKey, pkgName, workspace string) (repository.PackageRevisionDraftSlim, error) {
	repo, err := c.getRepository(repoKey)
	if err != nil {
		return nil, err
	}

	pkgRev, err := c.findPackageRevision(ctx, repoKey, pkgName, workspace)
	if err != nil {
		return nil, err
	}

	draft, err := repo.UpdatePackageRevision(ctx, pkgRev)
	if err != nil {
		return nil, err
	}
	return &draftSlimWrapper{inner: draft}, nil
}

func (c *contentCache) CloseDraft(ctx context.Context, repoKey repository.RepositoryKey, draft repository.PackageRevisionDraftSlim, version int) error {
	repo, err := c.getRepository(repoKey)
	if err != nil {
		return err
	}

	wrapper, ok := draft.(*draftSlimWrapper)
	if !ok {
		return fmt.Errorf("draft is not a contentCache draft")
	}

	_, err = repo.ClosePackageRevisionDraft(ctx, wrapper.inner, version)
	return err
}

func (c *contentCache) UpdateLifecycle(ctx context.Context, repoKey repository.RepositoryKey, pkg, workspace, desired string) (repository.PackageContent, error) {
	pkgRev, err := c.findPackageRevision(ctx, repoKey, pkg, workspace)
	if err != nil {
		return nil, err
	}

	desiredLC := porchv1alpha2.PackageRevisionLifecycle(desired)
	currentLC := porchv1alpha2.PackageRevisionLifecycle(pkgRev.Lifecycle(ctx))

	if !isKnownLifecycle(currentLC) {
		return nil, fmt.Errorf("invalid current lifecycle value: %q", currentLC)
	}
	if !isKnownLifecycle(desiredLC) {
		return nil, fmt.Errorf("invalid desired lifecycle value: %q", desiredLC)
	}

	// Published ↔ DeletionProposed: direct update, no draft needed.
	if porchv1alpha2.LifecycleIsPublished(currentLC) {
		if err := pkgRev.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycle(desiredLC)); err != nil {
			return nil, err
		}
		return &packageContentWrapper{inner: pkgRev}, nil
	}

	// Proposed → Published: use UpdateLifecycle which handles revision increment and git push.
	if currentLC == porchv1alpha2.PackageRevisionLifecycleProposed && desiredLC == porchv1alpha2.PackageRevisionLifecyclePublished {
		if err := pkgRev.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycle(desiredLC)); err != nil {
			return nil, err
		}
		return &packageContentWrapper{inner: pkgRev}, nil
	}

	// Draft ↔ Proposed: draft/close cycle.
	repo, err := c.getRepository(repoKey)
	if err != nil {
		return nil, err
	}

	draft, err := repo.UpdatePackageRevision(ctx, pkgRev)
	if err != nil {
		return nil, err
	}

	if err := draft.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycle(desiredLC)); err != nil {
		return nil, err
	}

	// version 0 for non-publish; cache calculates the real version on publish.
	closed, err := repo.ClosePackageRevisionDraft(ctx, draft, 0)
	if err != nil {
		return nil, err
	}
	return &packageContentWrapper{inner: closed}, nil
}

func (c *contentCache) DeletePackage(ctx context.Context, repoKey repository.RepositoryKey, pkg, workspace string) error {
	repo, err := c.getRepository(repoKey)
	if err != nil {
		return err
	}

	pkgRev, err := c.findPackageRevision(ctx, repoKey, pkg, workspace)
	if err != nil {
		return err
	}

	return repo.DeletePackageRevision(ctx, pkgRev)
}

func isKnownLifecycle(lc porchv1alpha2.PackageRevisionLifecycle) bool {
	switch lc {
	case porchv1alpha2.PackageRevisionLifecycleDraft,
		porchv1alpha2.PackageRevisionLifecycleProposed,
		porchv1alpha2.PackageRevisionLifecyclePublished,
		porchv1alpha2.PackageRevisionLifecycleDeletionProposed:
		return true
	default:
		return false
	}
}

// getRepository resolves a partial RepositoryKey (namespace+name only) to the
// full cache key by scanning GetRepositories(), then looks up the repo.
func (c *contentCache) getRepository(repoKey repository.RepositoryKey) (repository.Repository, error) {
	for _, spec := range c.cache.GetRepositories() {
		if spec.Namespace == repoKey.Namespace && spec.Name == repoKey.Name {
			fullKey, err := externalrepo.RepositoryKey(spec)
			if err != nil {
				return nil, fmt.Errorf("repository %s: %w", repoKey, err)
			}
			if repo := c.cache.GetRepository(fullKey); repo != nil {
				return repo, nil
			}
		}
	}
	return nil, fmt.Errorf("repository %s not found", repoKey)
}

func (c *contentCache) findPackageRevision(ctx context.Context, repoKey repository.RepositoryKey, pkg, workspace string) (repository.PackageRevision, error) {
	repo, err := c.getRepository(repoKey)
	if err != nil {
		return nil, err
	}

	// Split the package name into path and leaf name for matching.
	// e.g. "sub/folder/pkg" -> path="sub/folder", leafName="pkg"
	pkgPath, leafName := repository.SplitPackagePathName(pkg)

	revisions, err := repo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey:        repository.PackageKey{Path: pkgPath, Package: leafName},
			WorkspaceName: workspace,
		},
	})
	if err != nil {
		return nil, err
	}

	for _, rev := range revisions {
		key := rev.Key()
		if key.WorkspaceName == workspace && key.PkgKey.Package == leafName && key.PkgKey.Path == pkgPath {
			return rev, nil
		}
	}
	return nil, fmt.Errorf("package revision %s/%s not found in repository %s", pkg, workspace, repoKey)
}
