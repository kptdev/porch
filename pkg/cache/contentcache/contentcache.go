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

	"github.com/kptdev/porch/api/porch/key"
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

func (c *contentCache) GetPackageContent(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace string) (repository.PackageContent, error) {
	pkgRev, err := c.findPackageRevision(ctx, repoKey, pkg, workspace)
	if err != nil {
		return nil, err
	}
	return &packageContentWrapper{inner: pkgRev}, nil
}

func (c *contentCache) GetMainPackageContent(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace string) (repository.PackageContent, error) {
	pkgRev, err := c.findPackageRevision(ctx, repoKey, pkg, workspace)
	if err != nil {
		return nil, err
	}
	// Derive the main-branch HEAD placeholder (Revision -1) from the published
	// revision; no git ops, caller must have published the content first.
	mainPkgRev := pkgRev.ToMainPackageRevision(ctx)
	if mainPkgRev == nil {
		return nil, fmt.Errorf("could not derive main package revision for %s/%s in repository %s", pkg, workspace, repoKey)
	}
	return &packageContentWrapper{inner: mainPkgRev}, nil
}

func (c *contentCache) CreateNewDraft(ctx context.Context, repoKey key.RepositoryKey, pkgName, workspace, lifecycle string) (repository.PackageRevisionDraftSlim, error) {
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

func (c *contentCache) CreateDraftFromExisting(ctx context.Context, repoKey key.RepositoryKey, pkgName, workspace string) (repository.PackageRevisionDraftSlim, error) {
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

func (c *contentCache) CloseDraft(ctx context.Context, repoKey key.RepositoryKey, draft repository.PackageRevisionDraftSlim, version int) error {
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

func (c *contentCache) UpdateLifecycle(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace, desired string) (repository.PackageContent, error) {
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

	// Published ↔ DeletionProposed: direct update on PackageRevision (Git allows this only for published revisions).
	if porchv1alpha2.LifecycleIsPublished(currentLC) {
		if err := pkgRev.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycle(desiredLC)); err != nil {
			return nil, err
		}
		return &packageContentWrapper{inner: pkgRev}, nil
	}

	// Approve (Proposed or Draft → Published): push to the external repo and bump revision.
	// This must use PackageRevision.UpdateLifecycle (db publishPR / git tag), not the draft/close
	// cycle — ClosePackageRevisionDraft only persists DB state and does not publish to git.
	if desiredLC == porchv1alpha2.PackageRevisionLifecyclePublished &&
		(currentLC == porchv1alpha2.PackageRevisionLifecycleProposed ||
			currentLC == porchv1alpha2.PackageRevisionLifecycleDraft) {
		if err := pkgRev.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycle(desiredLC)); err != nil {
			return nil, err
		}
		return &packageContentWrapper{inner: pkgRev}, nil
	}

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

func (c *contentCache) DeletePackage(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace string) error {
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
func (c *contentCache) getRepository(repoKey key.RepositoryKey) (repository.Repository, error) {
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

func (c *contentCache) findPackageRevision(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace string) (repository.PackageRevision, error) {
	repo, err := c.getRepository(repoKey)
	if err != nil {
		return nil, err
	}

	// Split the package name into path and leaf name for matching.
	// e.g. "sub/folder/pkg" -> path="sub/folder", leafName="pkg"
	pkgPath, leafName := key.SplitPackagePathName(pkg)

	revisions, err := repo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
		Key: key.PackageRevisionKey{
			PkgKey:        key.PackageKey{Path: pkgPath, Package: leafName},
			WorkspaceName: workspace,
		},
	})
	if err != nil {
		return nil, err
	}

	var matches []repository.PackageRevision
	for _, rev := range revisions {
		k := rev.Key()
		if k.WorkspaceName == workspace && k.PkgKey.Package == leafName && k.PkgKey.Path == pkgPath {
			matches = append(matches, rev)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("package revision %s/%s not found in repository %s", pkg, workspace, repoKey)
	}
	return selectPackageRevisionForWorkspace(matches), nil
}

// selectPackageRevisionForWorkspace picks the package revision row that represents the
// workspace's current content. After publish, both a Published revision and a new Draft
// placeholder (revision 0) exist; prefer the Published revision so lifecycle reconciliation
// observes the published state instead of re-entering publish on the placeholder draft.
func selectPackageRevisionForWorkspace(revisions []repository.PackageRevision) repository.PackageRevision {
	var published repository.PackageRevision
	highestPublishedRev := -1
	for _, rev := range revisions {
		if !porchapi.LifecycleIsPublished(rev.Lifecycle(context.Background())) {
			continue
		}
		if rev.Key().Revision > highestPublishedRev {
			highestPublishedRev = rev.Key().Revision
			published = rev
		}
	}
	if published != nil {
		return published
	}
	for _, rev := range revisions {
		if rev.Key().Revision == 0 {
			return rev
		}
	}
	return revisions[0]
}
