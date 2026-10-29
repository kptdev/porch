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

package content

import (
	"context"
	"fmt"

	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo"
	"github.com/kptdev/porch/pkg/prrstore"
	"github.com/kptdev/porch/pkg/repository"
)

var _ repository.PackageContentAccess = &accessor{}

type accessor struct {
	cache cachetypes.Cache
	// prrStore is the authoritative content store and is mandatory for v1alpha2
	// (CRD) repositories; content reads go to it exclusively, never falling back
	// to git. It is nil only in v1alpha1 / CR-cache deployment mode, where reads
	// come from the git-backed revision instead.
	prrStore prrstore.PRRReader
	// deleter removes a revision's store row. It is set when prrStore can delete.
	deleter storeDeleter
}

// storeDeleter is the part of the PRRStore needed to remove a revision's content.
type storeDeleter interface {
	DeleteResources(ctx context.Context, prKey key.PackageRevisionKey) error
}

// draftSeeder is implemented by repositories that back draft content with a
// separate resource store (PRRStore) and can stage that content onto a git
// draft before it is closed.
type draftSeeder interface {
	StageDraftFromCache(ctx context.Context, draft repository.PackageRevisionDraft) error
}

// NewAccessor creates a PackageContentAccess backed by the given Cache. prrStore is
// the authoritative content store and is required for v1alpha2 (CRD) repositories;
// pass nil only in v1alpha1 / CR-cache mode, where content is read from git instead.
func NewAccessor(cache cachetypes.Cache, prrStore prrstore.PRRReader) repository.PackageContentAccess {
	a := &accessor{cache: cache, prrStore: prrStore}
	if d, ok := prrStore.(storeDeleter); ok {
		a.deleter = d
	}
	return a
}

func (c *accessor) GetPackageContent(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace string) (repository.PackageContent, error) {
	pkgRev, err := c.findPackageRevision(ctx, repoKey, pkg, workspace)
	if err != nil {
		return nil, err
	}
	return &packageContentWrapper{inner: pkgRev, prrStore: c.prrStore}, nil
}

func (c *accessor) GetMainPackageContent(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace string) (repository.PackageContent, error) {
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
	return &packageContentWrapper{inner: mainPkgRev, prrStore: c.prrStore}, nil
}

func (c *accessor) CreateNewDraft(ctx context.Context, repoKey key.RepositoryKey, pkgName, workspace, lifecycle string) (repository.PackageRevisionDraftSlim, error) {
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

func (c *accessor) CreateDraftFromExisting(ctx context.Context, repoKey key.RepositoryKey, pkgName, workspace string) (repository.PackageRevisionDraftSlim, error) {
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

func (c *accessor) CloseDraft(ctx context.Context, repoKey key.RepositoryKey, draft repository.PackageRevisionDraftSlim, version int) error {
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

func (c *accessor) UpdateLifecycle(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace, desired string) (repository.PackageContent, error) {
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

	// Published and DeletionProposed are not written to git.
	if porchv1alpha2.LifecycleIsPublished(currentLC) {
		return &packageContentWrapper{inner: pkgRev, prrStore: c.prrStore}, nil
	}

	// Draft ↔ Proposed and Proposed → Published go through the draft/close cycle;
	// ClosePackageRevisionDraft assigns the revision and writes the published ref.
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

	// A lifecycle-only transition opens the draft from git and stages no
	// resources, so the git draft lags behind PRRStore (the source of truth for
	// pushed/rendered content). Seed the draft from PRRStore before close so the
	// committed git content matches, for every transition, not just publish.
	if seeder, ok := repo.(draftSeeder); ok {
		if err := seeder.StageDraftFromCache(ctx, draft); err != nil {
			return nil, err
		}
	}

	// On publish, assign the next revision number (highest published revision of
	// this package + 1). Non-publish transitions use 0; the revision only becomes
	// meaningful once the package is published.
	version := 0
	if desiredLC == porchv1alpha2.PackageRevisionLifecyclePublished {
		version, err = c.nextRevision(ctx, repo, pkgRev.Key())
		if err != nil {
			return nil, err
		}
	}

	closed, err := repo.ClosePackageRevisionDraft(ctx, draft, version)
	if err != nil {
		return nil, err
	}
	return &packageContentWrapper{inner: closed, prrStore: c.prrStore}, nil
}

// nextRevision returns one more than the highest published revision number of
// the package identified by prKey -- i.e. the revision to assign when publishing.
func (c *accessor) nextRevision(ctx context.Context, repo repository.Repository, prKey key.PackageRevisionKey) (int, error) {
	revisions, err := repo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
		Key: key.PackageRevisionKey{PkgKey: prKey.PkgKey},
	})
	if err != nil {
		return 0, err
	}

	highest := 0
	for _, rev := range revisions {
		if rev.Key().PkgKey != prKey.PkgKey {
			continue
		}
		if porchv1alpha2.LifecycleIsPublished(porchv1alpha2.PackageRevisionLifecycle(rev.Lifecycle(ctx))) &&
			rev.Key().Revision > highest {
			highest = rev.Key().Revision
		}
	}
	return highest + 1, nil
}

func (c *accessor) DeletePackage(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace string) error {
	repo, err := c.getRepository(repoKey)
	if err != nil {
		return c.deleteStoreContentIfGone(ctx, repoKey, pkg, workspace, err)
	}

	pkgRev, err := c.findPackageRevision(ctx, repoKey, pkg, workspace)
	if err != nil {
		return c.deleteStoreContentIfGone(ctx, repoKey, pkg, workspace, err)
	}

	return repo.DeletePackageRevision(ctx, pkgRev)
}

// deleteStoreContentIfGone handles a delete for a revision git no longer has, or whose
// repository is no longer cached: the caller treats that as nothing to clean up, but the
// store may still hold its content, so remove it before the finalizer goes. Any other
// error is returned unchanged. The not-found error is returned too, for the caller.
func (c *accessor) deleteStoreContentIfGone(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace string, notFound error) error {
	if c.deleter == nil || !repository.IsNotFoundError(notFound) {
		return notFound
	}
	pkgPath, leafName := key.SplitPackagePathName(pkg)
	prKey := key.PackageRevisionKey{
		PkgKey:        key.PackageKey{RepoKey: repoKey, Path: pkgPath, Package: leafName},
		WorkspaceName: workspace,
	}
	if err := c.deleter.DeleteResources(ctx, prKey); err != nil {
		return fmt.Errorf("delete PRRStore content of %s: %w", prKey, err)
	}
	return notFound
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
func (c *accessor) getRepository(repoKey key.RepositoryKey) (repository.Repository, error) {
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

func (c *accessor) findPackageRevision(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace string) (repository.PackageRevision, error) {
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

	for _, rev := range revisions {
		key := rev.Key()
		if key.WorkspaceName == workspace && key.PkgKey.Package == leafName && key.PkgKey.Path == pkgPath {
			return rev, nil
		}
	}
	return nil, fmt.Errorf("package revision %s/%s not found in repository %s", pkg, workspace, repoKey)
}
