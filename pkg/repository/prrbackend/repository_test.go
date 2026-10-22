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

package prrbackend

import (
	"context"
	"fmt"
	"testing"

	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/prrstore"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util/selector"
	mockprrstore "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/prrstore"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// prrRepoTestKey builds a repository key for tests.
func prrRepoTestKey() key.RepositoryKey {
	return key.RepositoryKey{Namespace: "default", Name: "test-repo"}
}

// newTestPRRRepository builds a PRRRepository with injected mocks. Because the test is
// in-package, it can set the unexported fields directly, bypassing NewRepository
// (which would otherwise clone a real git repo).
func newTestPRRRepository(ext repository.Repository, store prrstore.PRRStore) *Repository {
	return &Repository{
		repoKey:      prrRepoTestKey(),
		externalRepo: ext,
		meta: configapi.Repository{
			ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "default"},
		},
		store: store,
	}
}

func testExtPRKey(pkg string) key.PackageRevisionKey {
	return key.PackageRevisionKey{
		PkgKey: key.PackageKey{
			RepoKey: prrRepoTestKey(),
			Package: pkg,
		},
		WorkspaceName: "v1",
		Revision:      1,
	}
}

func prrResources(res map[string]string) *porchapi.PackageRevisionResources {
	return &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{Resources: res},
	}
}

// ===== Accessors =====

func TestPRRRepository_Accessors(t *testing.T) {
	r := newTestPRRRepository(nil, nil)

	require.Equal(t, "test-repo", r.KubeObjectName())
	require.Equal(t, "default", r.KubeObjectNamespace())
	require.Equal(t, prrRepoTestKey(), r.Key())
	require.Equal(t, "test-repo", r.Meta().Name)
	require.Equal(t, "default", r.Meta().Namespace)
}

// ===== Pure delegation methods =====

func TestPRRRepository_ListPackageRevisions_Delegates(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(ext, store)

	want := []repository.PackageRevision{}
	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(want, nil)
	// Nothing is store-only, so git's result is returned unchanged.
	store.EXPECT().ListStoreOnly(mock.Anything, r.Key()).Return(nil, nil)

	got, err := r.ListPackageRevisions(context.Background(), repository.ListPackageRevisionFilter{})
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestPRRRepository_BranchCommitHash_Delegates(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	r := newTestPRRRepository(ext, nil)

	ext.EXPECT().BranchCommitHash(mock.Anything).Return("abc123", nil)

	got, err := r.BranchCommitHash(context.Background())
	require.NoError(t, err)
	require.Equal(t, "abc123", got)
}

func TestPRRRepository_Version_Delegates(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	r := newTestPRRRepository(ext, nil)

	ext.EXPECT().Version(mock.Anything).Return("v-42", nil)

	got, err := r.Version(context.Background())
	require.NoError(t, err)
	require.Equal(t, "v-42", got)
}

func TestPRRRepository_ListPackages_Delegates(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	r := newTestPRRRepository(ext, nil)

	want := []repository.Package{}
	ext.EXPECT().ListPackages(mock.Anything, mock.Anything).Return(want, nil)

	got, err := r.ListPackages(context.Background(), repository.ListPackageFilter{})
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestPRRRepository_CreatePackageRevisionDraft_Delegates(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	draft := mockrepo.NewMockPackageRevisionDraft(t)
	r := newTestPRRRepository(ext, nil)

	obj := &porchapi.PackageRevision{Spec: porchapi.PackageRevisionSpec{PackageName: "pkg"}}
	ext.EXPECT().CreatePackageRevisionDraft(mock.Anything, obj).Return(draft, nil)

	got, err := r.CreatePackageRevisionDraft(context.Background(), obj)
	require.NoError(t, err)
	inner, lifecycle := unwrapPRRDraft(got)
	require.Equal(t, draft, inner, "the git draft is wrapped, not replaced")
	require.Equal(t, porchapi.PackageRevisionLifecycleDraft, lifecycle, "a new package starts as a Draft")
}

func TestPRRRepository_DeletePackageRevision_Delegates(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	pr := mockrepo.NewMockPackageRevision(t)
	pr.EXPECT().Key().Return(testExtPRKey("pkg")).Maybe()
	store := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(ext, store)

	ext.EXPECT().DeletePackageRevision(mock.Anything, pr).Return(nil)
	store.EXPECT().DeleteResources(mock.Anything, testExtPRKey("pkg")).Return(nil)

	require.NoError(t, r.DeletePackageRevision(context.Background(), pr))
}

func TestPRRRepository_UpdatePackageRevision_Delegates(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	pr := mockrepo.NewMockPackageRevision(t)
	pr.EXPECT().Key().Return(testExtPRKey("pkg")).Maybe()
	pr.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecycleProposed)
	draft := mockrepo.NewMockPackageRevisionDraft(t)
	r := newTestPRRRepository(ext, nil)

	ext.EXPECT().UpdatePackageRevision(mock.Anything, pr).Return(draft, nil)

	got, err := r.UpdatePackageRevision(context.Background(), pr)
	require.NoError(t, err)
	inner, lifecycle := unwrapPRRDraft(got)
	require.Equal(t, draft, inner)
	require.Equal(t, porchapi.PackageRevisionLifecycleProposed, lifecycle, "draft keeps the lifecycle it was opened with")
}

func TestPRRRepository_Close_Delegates(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	r := newTestPRRRepository(ext, nil)

	ext.EXPECT().Close(mock.Anything).Return(nil)

	require.NoError(t, r.Close(context.Background()))
}

// ===== sanitizeResources =====

func TestPRRRepository_SanitizeResources(t *testing.T) {
	r := newTestPRRRepository(nil, nil)
	prKey := testExtPRKey("pkg")

	t.Run("nil input", func(t *testing.T) {
		res, size := r.sanitizeResources(prKey, nil)
		require.NotNil(t, res)
		require.Empty(t, res)
		require.Equal(t, int64(0), size)
	})

	t.Run("nil resources map", func(t *testing.T) {
		res, size := r.sanitizeResources(prKey, &porchapi.PackageRevisionResources{})
		require.Empty(t, res)
		require.Equal(t, int64(0), size)
	})

	t.Run("valid resources kept with size", func(t *testing.T) {
		in := prrResources(map[string]string{"Kptfile": "abc", "f.yaml": "de"})
		res, size := r.sanitizeResources(prKey, in)
		require.Equal(t, map[string]string{"Kptfile": "abc", "f.yaml": "de"}, res)
		require.Equal(t, int64(5), size) // len("abc")=3 + len("de")=2
	})

	t.Run("drops NUL byte in value", func(t *testing.T) {
		in := prrResources(map[string]string{"good": "ok", "bad": "a\x00b"})
		res, _ := r.sanitizeResources(prKey, in)
		require.Equal(t, map[string]string{"good": "ok"}, res)
	})

	t.Run("drops NUL byte in key", func(t *testing.T) {
		in := prrResources(map[string]string{"good": "ok", "ba\x00d": "value"})
		res, _ := r.sanitizeResources(prKey, in)
		require.Equal(t, map[string]string{"good": "ok"}, res)
	})

	t.Run("drops invalid UTF-8 in value", func(t *testing.T) {
		in := prrResources(map[string]string{"good": "ok", "bad": string([]byte{0xff, 0xfe})})
		res, _ := r.sanitizeResources(prKey, in)
		require.Equal(t, map[string]string{"good": "ok"}, res)
	})
}

// ===== Refresh =====

func TestPRRRepository_Refresh_Success(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	cache := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(ext, cache)

	ext.EXPECT().Refresh(mock.Anything).Return(nil)
	// cacheExternalPackageResources lists then writes.
	pr := mockrepo.NewMockPackageRevision(t)
	prKey := testExtPRKey("pkg")
	pr.EXPECT().Key().Return(prKey)
	pr.EXPECT().GetResources(mock.Anything).Return(prrResources(map[string]string{"Kptfile": "x"}), nil)
	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{pr}, nil)
	cache.EXPECT().GetResourceVersion(mock.Anything, prKey).Return("", nil)
	cache.EXPECT().WriteResources(mock.Anything, prKey, map[string]string{"Kptfile": "x"}).Return("v1", nil)

	require.NoError(t, r.Refresh(context.Background()))
}

func TestPRRRepository_Refresh_ExternalRefreshFails(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	r := newTestPRRRepository(ext, nil)

	ext.EXPECT().Refresh(mock.Anything).Return(fmt.Errorf("git boom"))

	err := r.Refresh(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "refresh failed")
}

func TestPRRRepository_Refresh_CacheErrorIsNonFatal(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	cache := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(ext, cache)

	ext.EXPECT().Refresh(mock.Anything).Return(nil)
	// Listing fails inside cacheExternalPackageResources; Refresh logs and still returns nil.
	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("list boom"))

	require.NoError(t, r.Refresh(context.Background()), "cache failure must not fail Refresh")
}

// ===== cacheExternalPackageResources =====

func TestPRRRepository_CacheExternal_SkipsGetResourcesError(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	cache := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(ext, cache)

	// PR1 fails GetResources (skipped), PR2 succeeds and is written.
	pr1 := mockrepo.NewMockPackageRevision(t)
	pr1.EXPECT().Key().Return(testExtPRKey("pkg1"))
	pr1.EXPECT().GetResources(mock.Anything).Return(nil, fmt.Errorf("resource boom"))

	pr2 := mockrepo.NewMockPackageRevision(t)
	pr2Key := testExtPRKey("pkg2")
	pr2.EXPECT().Key().Return(pr2Key)
	pr2.EXPECT().GetResources(mock.Anything).Return(prrResources(map[string]string{"Kptfile": "y"}), nil)

	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{pr1, pr2}, nil)
	// Neither PR is in the store yet, so both are hydrated from git discovery.
	cache.EXPECT().GetResourceVersion(mock.Anything, mock.Anything).Return("", nil)
	cache.EXPECT().WriteResources(mock.Anything, pr2Key, map[string]string{"Kptfile": "y"}).Return("v1", nil)

	require.NoError(t, r.cacheExternalPackageResources(context.Background()))
}

// TestPRRRepository_CacheExternal_SkipsWhenAlreadyInStore verifies discovery does not
// overwrite a PR the PRRStore already holds: the store is authoritative, so a git sync
// must not clobber newer pushed/rendered content with older git content.
func TestPRRRepository_CacheExternal_SkipsWhenAlreadyInStore(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	cache := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(ext, cache)

	pr := mockrepo.NewMockPackageRevision(t)
	prKey := testExtPRKey("pkg")
	pr.EXPECT().Key().Return(prKey)
	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{pr}, nil)
	// Already in the store: no GetResources, no WriteResources.
	cache.EXPECT().GetResourceVersion(mock.Anything, prKey).Return("existing-version", nil)

	require.NoError(t, r.cacheExternalPackageResources(context.Background()))
}

func TestPRRRepository_CacheExternal_ContinuesOnWriteError(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	cache := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(ext, cache)

	pr := mockrepo.NewMockPackageRevision(t)
	prKey := testExtPRKey("pkg")
	pr.EXPECT().Key().Return(prKey)
	pr.EXPECT().GetResources(mock.Anything).Return(prrResources(map[string]string{"Kptfile": "z"}), nil)
	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{pr}, nil)
	cache.EXPECT().GetResourceVersion(mock.Anything, prKey).Return("", nil)
	cache.EXPECT().WriteResources(mock.Anything, prKey, mock.Anything).Return("", fmt.Errorf("write boom"))

	// Write error is logged and skipped; the method still returns nil.
	require.NoError(t, r.cacheExternalPackageResources(context.Background()))
}

func TestPRRRepository_CacheExternal_ListError(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	cache := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(ext, cache)

	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("list boom"))

	err := r.cacheExternalPackageResources(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "list external package revisions")
}

// ===== ClosePackageRevisionDraft =====

func TestPRRRepository_CloseDraft_Success_CachesResources(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	cache := mockprrstore.NewMockPRRStore(t)
	draft := mockrepo.NewMockPackageRevisionDraft(t)
	r := newTestPRRRepository(ext, cache)

	closed := mockrepo.NewMockPackageRevision(t)
	closedKey := testExtPRKey("pkg")
	closed.EXPECT().Key().Return(closedKey)
	closed.EXPECT().GetResources(mock.Anything).Return(prrResources(map[string]string{"Kptfile": "c"}), nil)

	ext.EXPECT().ClosePackageRevisionDraft(mock.Anything, draft, 3).Return(closed, nil)
	cache.EXPECT().WriteResourcesAs(mock.Anything, closedKey, map[string]string{"Kptfile": "c"}, "").Return("v1", nil)

	got, err := r.ClosePackageRevisionDraft(context.Background(), draft, 3)
	require.NoError(t, err)
	require.Equal(t, closed, got)
}

func TestPRRRepository_CloseDraft_ExternalError(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	draft := mockrepo.NewMockPackageRevisionDraft(t)
	r := newTestPRRRepository(ext, nil)

	ext.EXPECT().ClosePackageRevisionDraft(mock.Anything, draft, 0).Return(nil, fmt.Errorf("close boom"))

	got, err := r.ClosePackageRevisionDraft(context.Background(), draft, 0)
	require.Error(t, err)
	require.Nil(t, got)
	require.Contains(t, err.Error(), "close package revision draft")
}

func TestPRRRepository_CloseDraft_GetResourcesErrorIsNonFatal(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	cache := mockprrstore.NewMockPRRStore(t)
	draft := mockrepo.NewMockPackageRevisionDraft(t)
	r := newTestPRRRepository(ext, cache)

	closed := mockrepo.NewMockPackageRevision(t)
	closed.EXPECT().Key().Return(testExtPRKey("pkg"))
	closed.EXPECT().GetResources(mock.Anything).Return(nil, fmt.Errorf("resource boom"))
	ext.EXPECT().ClosePackageRevisionDraft(mock.Anything, draft, 1).Return(closed, nil)
	// No WriteResources expected because GetResources failed.

	got, err := r.ClosePackageRevisionDraft(context.Background(), draft, 1)
	require.NoError(t, err, "GetResources failure must not fail the close")
	require.Equal(t, closed, got)
}

func TestPRRRepository_CloseDraft_WriteErrorIsNonFatal(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	cache := mockprrstore.NewMockPRRStore(t)
	draft := mockrepo.NewMockPackageRevisionDraft(t)
	r := newTestPRRRepository(ext, cache)

	closed := mockrepo.NewMockPackageRevision(t)
	closedKey := testExtPRKey("pkg")
	closed.EXPECT().Key().Return(closedKey)
	closed.EXPECT().GetResources(mock.Anything).Return(prrResources(map[string]string{"Kptfile": "c"}), nil)
	ext.EXPECT().ClosePackageRevisionDraft(mock.Anything, draft, 2).Return(closed, nil)
	cache.EXPECT().WriteResourcesAs(mock.Anything, closedKey, mock.Anything, "").Return("", fmt.Errorf("write boom"))

	got, err := r.ClosePackageRevisionDraft(context.Background(), draft, 2)
	require.NoError(t, err, "cache write failure must not fail the close")
	require.Equal(t, closed, got)
}

// ===== StageDraftFromCache (publish/lifecycle write-through seed) =====

// draftSeeder mirrors the unexported interface the content accessor asserts against,
// so this test fails to compile if *Repository ever stops satisfying it — the exact
// regression that silently dropped pushed content on publish.
type draftSeeder interface {
	StageDraftFromCache(ctx context.Context, draft repository.PackageRevisionDraft) error
}

var _ draftSeeder = (*Repository)(nil)

func TestPRRRepository_StageDraftFromCache_WritesCachedContentOntoDraft(t *testing.T) {
	cache := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(nil, cache)

	draft := mockrepo.NewMockPackageRevisionDraft(t)
	draftKey := testExtPRKey("pkg")
	draft.EXPECT().Key().Return(draftKey)

	// PRRStore holds the pushed content that the stale git draft lacks.
	pushed := map[string]string{"Kptfile": "k", "data.yaml": "pushed-content"}
	cache.EXPECT().GetResources(mock.Anything, draftKey).Return(pushed, nil)

	// It must be written back onto the git draft so the close commit reflects it.
	draft.EXPECT().UpdateResources(mock.Anything, mock.MatchedBy(func(prr *porchapi.PackageRevisionResources) bool {
		return prr != nil && prr.Spec.Resources["data.yaml"] == "pushed-content"
	}), mock.Anything).Return(nil)

	require.NoError(t, r.StageDraftFromCache(context.Background(), draft))
}

func TestPRRRepository_StageDraftFromCache_EmptyCacheIsNoOp(t *testing.T) {
	cache := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(nil, cache)

	draft := mockrepo.NewMockPackageRevisionDraft(t)
	draftKey := testExtPRKey("pkg")
	draft.EXPECT().Key().Return(draftKey)
	cache.EXPECT().GetResources(mock.Anything, draftKey).Return(map[string]string{}, nil)

	// No cached content → must NOT call UpdateResources (leave the git draft as-is).
	require.NoError(t, r.StageDraftFromCache(context.Background(), draft))
}

func TestPRRRepository_StageDraftFromCache_StoreErrorPropagates(t *testing.T) {
	cache := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(nil, cache)

	draft := mockrepo.NewMockPackageRevisionDraft(t)
	draftKey := testExtPRKey("pkg")
	draft.EXPECT().Key().Return(draftKey)
	cache.EXPECT().GetResources(mock.Anything, draftKey).Return(nil, fmt.Errorf("store boom"))

	err := r.StageDraftFromCache(context.Background(), draft)
	require.Error(t, err)
	require.Contains(t, err.Error(), "staging draft from PRRStore")
}

// ===== Store-only (unpushed) draft support =====

// newSkipPushRepository builds a repository that skips draft pushes, as when
// push-drafts-to-git is disabled.
func newSkipPushRepository(ext repository.Repository, store prrstore.PRRStore) *Repository {
	r := newTestPRRRepository(ext, store)
	r.skipDraftPush = true
	return r
}

// storeOnlyRev builds a store-only row for the test repository.
func storeOnlyRev(pkg, workspace, lifecycle string) prrstore.StoreOnlyRevision {
	return prrstore.StoreOnlyRevision{
		Key: key.PackageRevisionKey{
			PkgKey:        key.PackageKey{RepoKey: prrRepoTestKey(), Package: pkg},
			WorkspaceName: workspace,
		},
		Lifecycle: lifecycle,
	}
}

// gitRev builds a mock git revision with the given key and lifecycle.
func gitRev(t *testing.T, pkg, workspace string, revision int, lc porchapi.PackageRevisionLifecycle) *mockrepo.MockPackageRevision {
	t.Helper()
	k := key.PackageRevisionKey{
		PkgKey:        key.PackageKey{RepoKey: prrRepoTestKey(), Package: pkg},
		WorkspaceName: workspace,
		Revision:      revision,
	}
	m := mockrepo.NewMockPackageRevision(t)
	m.EXPECT().Key().Return(k).Maybe()
	m.EXPECT().KubeObjectName().Return(key.ComposePkgRevObjName(k)).Maybe()
	m.EXPECT().Lifecycle(mock.Anything).Return(lc).Maybe()
	return m
}

func TestPRRRepository_List_AddsStoreOnlyDrafts(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	published := gitRev(t, "pub", "v1", 1, porchapi.PackageRevisionLifecyclePublished)
	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{published}, nil)
	store.EXPECT().ListStoreOnly(mock.Anything, r.Key()).Return([]prrstore.StoreOnlyRevision{
		storeOnlyRev("zdraft", "ws", prrstore.StoreOnlyDraft),
		storeOnlyRev("aprop", "ws", prrstore.StoreOnlyProposed),
	}, nil)

	got, err := r.ListPackageRevisions(context.Background(), repository.ListPackageRevisionFilter{})
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Same(t, published, got[0], "git revisions come first, unchanged")
	// Store-only revisions follow in name order, with the lifecycle recorded in the store.
	require.Equal(t, "aprop", got[1].Key().PkgKey.Package)
	require.Equal(t, porchapi.PackageRevisionLifecycleProposed, got[1].Lifecycle(context.Background()))
	require.Equal(t, "zdraft", got[2].Key().PkgKey.Package)
	require.Equal(t, porchapi.PackageRevisionLifecycleDraft, got[2].Lifecycle(context.Background()))
}

// The key must carry the repository's own RepositoryKey. A partial key (no path or
// placeholder workspace) compares unequal to git's keys and breaks revision assignment.
func TestPRRRepository_List_StoreOnlyKeyCarriesRepositoryKey(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)
	r.repoKey = key.RepositoryKey{Namespace: "default", Name: "test-repo", Path: "some/dir", PlaceholderWSname: "main"}

	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, nil)
	row := storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft)
	row.Key.PkgKey.RepoKey = r.repoKey
	store.EXPECT().ListStoreOnly(mock.Anything, r.repoKey).Return([]prrstore.StoreOnlyRevision{row}, nil)

	got, err := r.ListPackageRevisions(context.Background(), repository.ListPackageRevisionFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, r.repoKey, got[0].Key().RKey())
}

func TestPRRRepository_List_StoreOverridesGitDraft(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	// Git still has an older pushed Draft (for example from before the flag was turned
	// off) while the store says the revision has since moved to Proposed.
	staleGit := gitRev(t, "pkg", "ws", 0, porchapi.PackageRevisionLifecycleDraft)
	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{staleGit}, nil)
	store.EXPECT().ListStoreOnly(mock.Anything, r.Key()).Return([]prrstore.StoreOnlyRevision{
		storeOnlyRev("pkg", "ws", prrstore.StoreOnlyProposed),
	}, nil)

	got, err := r.ListPackageRevisions(context.Background(), repository.ListPackageRevisionFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1, "one revision per name")
	require.Equal(t, porchapi.PackageRevisionLifecycleProposed, got[0].Lifecycle(context.Background()),
		"the store is the authority for an unpushed draft")
}

func TestPRRRepository_List_GitWinsForPublished(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	// A leftover marker on a revision that git now holds as Published must not shadow it.
	published := gitRev(t, "pkg", "ws", 1, porchapi.PackageRevisionLifecyclePublished)
	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{published}, nil)
	store.EXPECT().ListStoreOnly(mock.Anything, r.Key()).Return([]prrstore.StoreOnlyRevision{
		storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft),
	}, nil)

	got, err := r.ListPackageRevisions(context.Background(), repository.ListPackageRevisionFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Same(t, published, got[0])
}

func TestPRRRepository_List_AppliesFilterToStoreOnly(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, nil)
	store.EXPECT().ListStoreOnly(mock.Anything, r.Key()).Return([]prrstore.StoreOnlyRevision{
		storeOnlyRev("one", "ws", prrstore.StoreOnlyDraft),
		storeOnlyRev("two", "ws", prrstore.StoreOnlyDraft),
	}, nil)

	filter := repository.ListPackageRevisionFilter{Key: key.PackageRevisionKey{
		PkgKey:        key.PackageKey{Package: "two"},
		WorkspaceName: "ws",
	}}
	got, err := r.ListPackageRevisions(context.Background(), filter)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "two", got[0].Key().PkgKey.Package)
}

// Published-only lookups (clone upstream, for example) cannot match a draft, so the
// store must not be consulted at all.
func TestPRRRepository_List_SkipsStoreForPublishedOnlyFilter(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t) // no ListStoreOnly expectation: a call fails the test
	r := newSkipPushRepository(ext, store)

	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, nil)

	_, err := r.ListPackageRevisions(context.Background(), repository.ListPackageRevisionFilter{
		Lifecycles: []porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecyclePublished},
	})
	require.NoError(t, err)
}

func TestPRRRepository_List_StoreErrorFails(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, nil)
	store.EXPECT().ListStoreOnly(mock.Anything, r.Key()).Return(nil, fmt.Errorf("db down"))

	// Dropping drafts silently would look like "not found" and send callers down wrong paths.
	_, err := r.ListPackageRevisions(context.Background(), repository.ListPackageRevisionFilter{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "db down")
}

func TestPRRRepository_Close_SkipPushMarksDraft(t *testing.T) {
	for _, tc := range []struct {
		lifecycle porchapi.PackageRevisionLifecycle
		marker    string
	}{
		{porchapi.PackageRevisionLifecycleDraft, prrstore.StoreOnlyDraft},
		{porchapi.PackageRevisionLifecycleProposed, prrstore.StoreOnlyProposed},
	} {
		t.Run(string(tc.lifecycle), func(t *testing.T) {
			ext := mockrepo.NewMockRepository(t)
			store := mockprrstore.NewMockPRRStore(t)
			r := newSkipPushRepository(ext, store)

			inner := mockrepo.NewMockPackageRevisionDraft(t)
			closed := mockrepo.NewMockPackageRevision(t)
			closedKey := testExtPRKey("pkg")
			closed.EXPECT().Key().Return(closedKey)
			closed.EXPECT().GetResources(mock.Anything).Return(prrResources(map[string]string{"Kptfile": "c"}), nil)
			ext.EXPECT().ClosePackageRevisionDraft(mock.Anything, inner, 0).Return(closed, nil)
			store.EXPECT().WriteResourcesAs(mock.Anything, closedKey, map[string]string{"Kptfile": "c"}, tc.marker).Return("v1", nil)

			got, err := r.ClosePackageRevisionDraft(context.Background(), newPRRDraft(inner, tc.lifecycle), 0)
			require.NoError(t, err)
			require.Equal(t, closed, got, "the revision git built is still returned")
		})
	}
}

// Publishing must hand the revision back to git: the marker is cleared.
func TestPRRRepository_Close_PublishClearsMarker(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	inner := mockrepo.NewMockPackageRevisionDraft(t)
	closed := mockrepo.NewMockPackageRevision(t)
	closedKey := testExtPRKey("pkg")
	closed.EXPECT().Key().Return(closedKey)
	closed.EXPECT().GetResources(mock.Anything).Return(prrResources(map[string]string{"Kptfile": "c"}), nil)
	ext.EXPECT().ClosePackageRevisionDraft(mock.Anything, inner, 2).Return(closed, nil)
	store.EXPECT().WriteResourcesAs(mock.Anything, closedKey, mock.Anything, "").Return("v1", nil)

	inner.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecyclePublished).Return(nil)
	d := newPRRDraft(inner, porchapi.PackageRevisionLifecycleProposed)
	require.NoError(t, d.UpdateLifecycle(context.Background(), porchapi.PackageRevisionLifecyclePublished))
	_, err := r.ClosePackageRevisionDraft(context.Background(), d, 2)
	require.NoError(t, err)
}

// With pushes enabled git holds the draft, so nothing is store-only.
func TestPRRRepository_Close_PushEnabledNeverMarks(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(ext, store) // skipDraftPush is false

	inner := mockrepo.NewMockPackageRevisionDraft(t)
	closed := mockrepo.NewMockPackageRevision(t)
	closedKey := testExtPRKey("pkg")
	closed.EXPECT().Key().Return(closedKey)
	closed.EXPECT().GetResources(mock.Anything).Return(prrResources(map[string]string{"Kptfile": "c"}), nil)
	ext.EXPECT().ClosePackageRevisionDraft(mock.Anything, inner, 0).Return(closed, nil)
	store.EXPECT().WriteResourcesAs(mock.Anything, closedKey, mock.Anything, "").Return("v1", nil)

	_, err := r.ClosePackageRevisionDraft(context.Background(), newPRRDraft(inner, porchapi.PackageRevisionLifecycleDraft), 0)
	require.NoError(t, err)
}

// For an unpushed draft the store holds the only copy, so a failed write must fail the close.
func TestPRRRepository_Close_StoreOnlyWriteFailureIsFatal(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	inner := mockrepo.NewMockPackageRevisionDraft(t)
	closed := mockrepo.NewMockPackageRevision(t)
	closedKey := testExtPRKey("pkg")
	closed.EXPECT().Key().Return(closedKey)
	closed.EXPECT().GetResources(mock.Anything).Return(prrResources(map[string]string{"Kptfile": "c"}), nil)
	ext.EXPECT().ClosePackageRevisionDraft(mock.Anything, inner, 0).Return(closed, nil)
	store.EXPECT().WriteResourcesAs(mock.Anything, closedKey, mock.Anything, prrstore.StoreOnlyDraft).Return("", fmt.Errorf("write boom"))

	got, err := r.ClosePackageRevisionDraft(context.Background(), newPRRDraft(inner, porchapi.PackageRevisionLifecycleDraft), 0)
	require.Error(t, err)
	require.Nil(t, got)
}

func TestPRRRepository_Close_StoreOnlyReadFailureIsFatal(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	inner := mockrepo.NewMockPackageRevisionDraft(t)
	closed := mockrepo.NewMockPackageRevision(t)
	closed.EXPECT().Key().Return(testExtPRKey("pkg"))
	closed.EXPECT().GetResources(mock.Anything).Return(nil, fmt.Errorf("read boom"))
	ext.EXPECT().ClosePackageRevisionDraft(mock.Anything, inner, 0).Return(closed, nil)

	_, err := r.ClosePackageRevisionDraft(context.Background(), newPRRDraft(inner, porchapi.PackageRevisionLifecycleDraft), 0)
	require.Error(t, err, "no write happened, so the draft would be lost")
}

func TestPRRRepository_UpdatePackageRevision_OpensStoreOnlyDraftSeededFromStore(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyProposed), store)
	// GetPackageRevision reads the Kptfile from the store.
	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(map[string]string{"Kptfile": ""}, nil)

	inner := mockrepo.NewMockPackageRevisionDraft(t)
	inner.EXPECT().Key().Return(sd.Key())
	ext.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.MatchedBy(func(obj *porchapi.PackageRevision) bool {
		return obj.Spec.PackageName == "pkg" && obj.Spec.WorkspaceName == "ws"
	})).Return(inner, nil)
	// Seeded straight away, so a close with no further writes reproduces the store.
	inner.EXPECT().UpdateResources(mock.Anything, mock.MatchedBy(func(prr *porchapi.PackageRevisionResources) bool {
		return prr.Spec.Resources["Kptfile"] == ""
	}), mock.Anything).Return(nil)

	got, err := r.UpdatePackageRevision(context.Background(), sd)
	require.NoError(t, err)
	gotInner, lifecycle := unwrapPRRDraft(got)
	require.Equal(t, inner, gotInner)
	require.Equal(t, porchapi.PackageRevisionLifecycleProposed, lifecycle, "keeps the store's lifecycle")
}

func TestPRRRepository_Delete_StoreOnlyRemovesStoreRowOnly(t *testing.T) {
	ext := mockrepo.NewMockRepository(t) // no DeletePackageRevision expectation: git must not be touched
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)
	store.EXPECT().DeleteResources(mock.Anything, sd.Key()).Return(nil)

	require.NoError(t, r.DeletePackageRevision(context.Background(), sd))
}

func TestPRRRepository_Delete_StoreOnlyFailureIsFatal(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)
	store.EXPECT().DeleteResources(mock.Anything, sd.Key()).Return(fmt.Errorf("db boom"))

	require.Error(t, r.DeletePackageRevision(context.Background(), sd),
		"the row is all there is, so leaving it would resurrect the draft")
}

func TestPRRRepository_Delete_GitBackedStoreFailureIsNonFatal(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newTestPRRRepository(ext, store)

	pr := mockrepo.NewMockPackageRevision(t)
	pr.EXPECT().Key().Return(testExtPRKey("pkg")).Maybe()
	ext.EXPECT().DeletePackageRevision(mock.Anything, pr).Return(nil)
	store.EXPECT().DeleteResources(mock.Anything, testExtPRKey("pkg")).Return(fmt.Errorf("db boom"))

	require.NoError(t, r.DeletePackageRevision(context.Background(), pr), "git is deleted; the copy is best-effort")
}

func TestPRRRepository_Delete_GitFailureLeavesStoreAlone(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t) // no DeleteResources expectation
	r := newTestPRRRepository(ext, store)

	pr := mockrepo.NewMockPackageRevision(t)
	pr.EXPECT().Key().Return(testExtPRKey("pkg")).Maybe()
	ext.EXPECT().DeletePackageRevision(mock.Anything, pr).Return(fmt.Errorf("git boom"))

	require.Error(t, r.DeletePackageRevision(context.Background(), pr))
}

// ===== storeDraftRevision =====

func TestStoreDraftRevision_UpstreamLockComesFromKptfile(t *testing.T) {
	store := mockprrstore.NewMockPRRStore(t)
	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)

	kptfile := `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: pkg
upstream:
  type: git
  git:
    repo: https://example.com/r.git
    directory: /up
    ref: v1
upstreamLock:
  type: git
  git:
    repo: https://example.com/r.git
    directory: /up
    ref: v1
    commit: abc123
`
	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(map[string]string{"Kptfile": kptfile}, nil)

	up, lock, err := sd.GetUpstreamLock(context.Background())
	require.NoError(t, err)
	require.Equal(t, "v1", up.Git.Ref)
	require.Equal(t, "abc123", lock.Git.Commit, "a cloned draft must keep its upstream lock or the controller blanks the status")
}

func TestStoreDraftRevision_NoUpstreamIsEmptyNotError(t *testing.T) {
	store := mockprrstore.NewMockPRRStore(t)
	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)
	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: pkg\n",
	}, nil)

	up, lock, err := sd.GetUpstreamLock(context.Background())
	require.NoError(t, err)
	require.Nil(t, up.Git)
	require.Nil(t, lock.Git)
}

func TestStoreDraftRevision_UnpushedDraftHasNoGitIdentity(t *testing.T) {
	store := mockprrstore.NewMockPRRStore(t)
	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)
	ctx := context.Background()

	_, lock, err := sd.GetLock(ctx)
	require.NoError(t, err)
	require.Nil(t, lock.Git, "no git location exists until it is published")
	require.Equal(t, "", sd.ResourceVersion())
	require.Nil(t, sd.ToMainPackageRevision(ctx))
	require.False(t, sd.IsLatestRevision())
	ts, by := sd.GetCommitInfo()
	require.True(t, ts.IsZero())
	require.Empty(t, by)
	require.Error(t, sd.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished),
		"lifecycle changes go through the draft cycle")
}

func TestStoreDraftRevision_FilteredResources(t *testing.T) {
	store := mockprrstore.NewMockPRRStore(t)
	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)
	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(map[string]string{"Kptfile": "k", "a.yaml": "a"}, nil)

	got, err := sd.GetFilteredResources(context.Background(), selector.PRRGet{FilePaths: []string{"a.yaml"}})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"a.yaml": "a"}, got.Spec.Resources)

	_, err = sd.GetFilteredResources(context.Background(), selector.PRRGet{FilePaths: []string{"missing"}})
	require.Error(t, err, "same as git: a requested file that does not exist is an error")
}

// ===== NewRepository =====

// setExternalRepoUnitTestMode makes externalrepo hand back a fake instead of cloning git.
func setExternalRepoUnitTestMode(t *testing.T, on bool) {
	t.Helper()
	prev := externalrepo.ExternalRepoInUnitTestMode
	externalrepo.ExternalRepoInUnitTestMode = on
	t.Cleanup(func() { externalrepo.ExternalRepoInUnitTestMode = prev })
}

func prrTestSpec() *configapi.Repository {
	return &configapi.Repository{ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "default"}}
}

func TestNewRepository_ExternalRepoFailure(t *testing.T) {
	setExternalRepoUnitTestMode(t, false) // an unsupported repository type is then an error

	got, err := NewRepository(context.Background(), prrRepoTestKey(), prrTestSpec(),
		mockprrstore.NewMockPRRStore(t), externalrepotypes.ExternalRepoOptions{})
	require.Error(t, err)
	require.Nil(t, got)
	require.Contains(t, err.Error(), "failed to create external repository")
}

func TestNewRepository_RequiresStore(t *testing.T) {
	setExternalRepoUnitTestMode(t, true)

	got, err := NewRepository(context.Background(), prrRepoTestKey(), prrTestSpec(), nil, externalrepotypes.ExternalRepoOptions{})
	require.Error(t, err)
	require.Nil(t, got)
	require.Contains(t, err.Error(), "PRRStore not configured")
}

func TestNewRepository_CarriesSkipDraftPush(t *testing.T) {
	setExternalRepoUnitTestMode(t, true)
	store := mockprrstore.NewMockPRRStore(t)

	for _, skip := range []bool{false, true} {
		got, err := NewRepository(context.Background(), prrRepoTestKey(), prrTestSpec(), store,
			externalrepotypes.ExternalRepoOptions{SkipDraftPush: skip})
		require.NoError(t, err)

		r, ok := got.(*Repository)
		require.True(t, ok)
		require.Equal(t, skip, r.skipDraftPush, "the git option and the store-only marking must agree")
		require.Equal(t, prrRepoTestKey(), r.Key())
	}
}

// ===== Listing: remaining paths =====

func TestPRRRepository_List_GitErrorFails(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t) // the store is not consulted when git fails
	r := newTestPRRRepository(ext, store)

	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("git boom"))

	got, err := r.ListPackageRevisions(context.Background(), repository.ListPackageRevisionFilter{})
	require.Error(t, err)
	require.Nil(t, got)
}

func TestFilterMayMatchStoreOnly(t *testing.T) {
	for name, tc := range map[string]struct {
		lifecycles []porchapi.PackageRevisionLifecycle
		want       bool
	}{
		"no lifecycle filter":  {nil, true},
		"draft":                {[]porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecycleDraft}, true},
		"proposed":             {[]porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecycleProposed}, true},
		"published and draft":  {[]porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecyclePublished, porchapi.PackageRevisionLifecycleDraft}, true},
		"published only":       {[]porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecyclePublished}, false},
		"deletion proposed":    {[]porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecycleDeletionProposed}, false},
		"published and delete": {[]porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecyclePublished, porchapi.PackageRevisionLifecycleDeletionProposed}, false},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, filterMayMatchStoreOnly(repository.ListPackageRevisionFilter{Lifecycles: tc.lifecycles}))
		})
	}
}

// ===== Drafts: remaining paths =====

func TestPRRRepository_CreatePackageRevisionDraft_ExternalError(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	r := newTestPRRRepository(ext, nil)

	ext.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("git boom"))

	got, err := r.CreatePackageRevisionDraft(context.Background(), &porchapi.PackageRevision{})
	require.Error(t, err)
	require.Nil(t, got, "a failed create must not return a wrapper around nothing")
}

// A nil revision from git means nothing was produced: there is nothing to store.
func TestPRRRepository_CloseDraft_NilRevisionWritesNothing(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t) // no write expected
	r := newSkipPushRepository(ext, store)

	inner := mockrepo.NewMockPackageRevisionDraft(t)
	ext.EXPECT().ClosePackageRevisionDraft(mock.Anything, inner, 0).Return(nil, nil)

	got, err := r.ClosePackageRevisionDraft(context.Background(), newPRRDraft(inner, porchapi.PackageRevisionLifecycleDraft), 0)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestPRRRepository_UpdatePackageRevision_GitErrorFails(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	r := newTestPRRRepository(ext, nil)

	pr := mockrepo.NewMockPackageRevision(t)
	pr.EXPECT().Key().Return(testExtPRKey("pkg")).Maybe()
	ext.EXPECT().UpdatePackageRevision(mock.Anything, pr).Return(nil, fmt.Errorf("git boom"))

	got, err := r.UpdatePackageRevision(context.Background(), pr)
	require.Error(t, err)
	require.Nil(t, got)
}

func TestPRRRepository_UpdatePackageRevision_StoreOnlyGitDraftFailure(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)
	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(map[string]string{"Kptfile": ""}, nil)
	ext.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("git boom"))

	got, err := r.UpdatePackageRevision(context.Background(), sd)
	require.Error(t, err)
	require.Nil(t, got)
	require.Contains(t, err.Error(), "open git draft for store-only")
}

// If the draft cannot be seeded from the store, opening must fail: a draft left empty
// would replace the store's content with nothing on close.
func TestPRRRepository_UpdatePackageRevision_StoreOnlySeedFailure(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	store := mockprrstore.NewMockPRRStore(t)
	r := newSkipPushRepository(ext, store)

	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)
	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(map[string]string{"Kptfile": ""}, nil)
	inner := mockrepo.NewMockPackageRevisionDraft(t)
	inner.EXPECT().Key().Return(sd.Key())
	ext.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(inner, nil)
	inner.EXPECT().UpdateResources(mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("stage boom"))

	got, err := r.UpdatePackageRevision(context.Background(), sd)
	require.Error(t, err)
	require.Nil(t, got)
}

// ===== prrDraft =====

func TestPRRDraft_ForwardsToInnerDraft(t *testing.T) {
	inner := mockrepo.NewMockPackageRevisionDraft(t)
	d := newPRRDraft(inner, porchapi.PackageRevisionLifecycleDraft)
	ctx := context.Background()

	inner.EXPECT().Key().Return(testExtPRKey("pkg"))
	require.Equal(t, testExtPRKey("pkg"), d.Key())

	meta := metav1.ObjectMeta{Name: "n", Namespace: "ns"}
	inner.EXPECT().GetMeta().Return(meta)
	require.Equal(t, meta, d.GetMeta())

	res := prrResources(map[string]string{"a": "b"})
	task := &porchapi.Task{Type: porchapi.TaskTypeRender}
	inner.EXPECT().UpdateResources(mock.Anything, res, task).Return(nil).Once()
	require.NoError(t, d.UpdateResources(ctx, res, task))

	inner.EXPECT().UpdateResources(mock.Anything, res, task).Return(fmt.Errorf("boom")).Once()
	require.Error(t, d.UpdateResources(ctx, res, task))
}

// The recorded lifecycle drives the store-only marker, so it must only change when git
// accepted the change.
func TestPRRDraft_FailedLifecycleChangeIsNotRecorded(t *testing.T) {
	inner := mockrepo.NewMockPackageRevisionDraft(t)
	d := newPRRDraft(inner, porchapi.PackageRevisionLifecycleDraft)

	inner.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecyclePublished).Return(fmt.Errorf("boom"))
	require.Error(t, d.UpdateLifecycle(context.Background(), porchapi.PackageRevisionLifecyclePublished))

	_, lifecycle := unwrapPRRDraft(d)
	require.Equal(t, porchapi.PackageRevisionLifecycleDraft, lifecycle)
}

// An unexpected draft type is passed through with no lifecycle, so it is never marked.
func TestUnwrapPRRDraft_ForeignDraftHasNoLifecycle(t *testing.T) {
	foreign := mockrepo.NewMockPackageRevisionDraft(t)

	got, lifecycle := unwrapPRRDraft(foreign)
	require.Same(t, foreign, got)
	require.Empty(t, lifecycle)
}

// ===== storeDraftRevision: remaining paths =====

func TestStoreDraftRevision_GetResources(t *testing.T) {
	store := mockprrstore.NewMockPRRStore(t)
	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)
	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(map[string]string{"Kptfile": "k"}, nil).Once()

	got, err := sd.GetResources(context.Background())
	require.NoError(t, err)
	require.Equal(t, map[string]string{"Kptfile": "k"}, got.Spec.Resources)
	require.Equal(t, sd.KubeObjectName(), got.Name)
	require.Equal(t, "pkg", got.Spec.PackageName)
	require.Equal(t, "ws", got.Spec.WorkspaceName)

	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(nil, fmt.Errorf("db boom")).Once()
	_, err = sd.GetResources(context.Background())
	require.Error(t, err)
}

func TestStoreDraftRevision_FilteredResourcesStoreError(t *testing.T) {
	store := mockprrstore.NewMockPRRStore(t)
	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)
	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(nil, fmt.Errorf("db boom"))

	_, err := sd.GetFilteredResources(context.Background(), selector.PRRGet{})
	require.Error(t, err)
}

func TestStoreDraftRevision_FilteredResourcesPathOnly(t *testing.T) {
	store := mockprrstore.NewMockPRRStore(t)
	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)
	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(map[string]string{"Kptfile": "k", "a.yaml": "a"}, nil)

	got, err := sd.GetFilteredResources(context.Background(), selector.PRRGet{PathOnly: true})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"Kptfile": "", "a.yaml": ""}, got.Spec.Resources, "paths without content")
}

func TestStoreDraftRevision_KptfileErrors(t *testing.T) {
	store := mockprrstore.NewMockPRRStore(t)
	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)
	ctx := context.Background()

	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(nil, fmt.Errorf("db boom")).Once()
	_, err := sd.GetKptfile(ctx)
	require.Error(t, err)

	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(map[string]string{"Kptfile": "{{{ not yaml"}, nil).Once()
	_, err = sd.GetKptfile(ctx)
	require.Error(t, err, "an undecodable Kptfile is an error, not an empty one")

	// The upstream lock is read from the Kptfile, so the same failure surfaces there.
	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(nil, fmt.Errorf("db boom")).Once()
	_, _, err = sd.GetUpstreamLock(ctx)
	require.Error(t, err)
}

func TestStoreDraftRevision_MetaRoundTripsIntoPackageRevision(t *testing.T) {
	store := mockprrstore.NewMockPRRStore(t)
	sd := newStoreDraftRevision(storeOnlyRev("pkg", "ws", prrstore.StoreOnlyDraft), store)
	ctx := context.Background()
	store.EXPECT().GetResources(mock.Anything, sd.Key()).Return(map[string]string{"Kptfile": ""}, nil)

	created := metav1.Now()
	meta := metav1.ObjectMeta{Name: "n", CreationTimestamp: created, Labels: map[string]string{"l": "v"}}
	require.NoError(t, sd.SetMeta(ctx, meta))
	require.Equal(t, meta, sd.GetMeta())

	pr, err := sd.GetPackageRevision(ctx)
	require.NoError(t, err)
	require.Equal(t, created, pr.CreationTimestamp, "creation time comes from the metadata set on the revision")
	require.Equal(t, porchapi.PackageRevisionLifecycleDraft, pr.Spec.Lifecycle)

	prr, err := sd.GetResources(ctx)
	require.NoError(t, err)
	require.Equal(t, created, prr.CreationTimestamp)
}
