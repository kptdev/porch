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
	"github.com/kptdev/porch/pkg/prrstore"
	"github.com/kptdev/porch/pkg/repository"
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
	r := newTestPRRRepository(ext, nil)

	want := []repository.PackageRevision{}
	ext.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(want, nil)

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
	require.Equal(t, draft, got)
}

func TestPRRRepository_DeletePackageRevision_Delegates(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	pr := mockrepo.NewMockPackageRevision(t)
	pr.EXPECT().Key().Return(testExtPRKey("pkg")).Maybe()
	r := newTestPRRRepository(ext, nil)

	ext.EXPECT().DeletePackageRevision(mock.Anything, pr).Return(nil)

	require.NoError(t, r.DeletePackageRevision(context.Background(), pr))
}

func TestPRRRepository_UpdatePackageRevision_Delegates(t *testing.T) {
	ext := mockrepo.NewMockRepository(t)
	pr := mockrepo.NewMockPackageRevision(t)
	pr.EXPECT().Key().Return(testExtPRKey("pkg")).Maybe()
	draft := mockrepo.NewMockPackageRevisionDraft(t)
	r := newTestPRRRepository(ext, nil)

	ext.EXPECT().UpdatePackageRevision(mock.Anything, pr).Return(draft, nil)

	got, err := r.UpdatePackageRevision(context.Background(), pr)
	require.NoError(t, err)
	require.Equal(t, draft, got)
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
	cache.EXPECT().WriteResources(mock.Anything, pr2Key, map[string]string{"Kptfile": "y"}).Return("v1", nil)

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
	cache.EXPECT().WriteResources(mock.Anything, closedKey, map[string]string{"Kptfile": "c"}).Return("v1", nil)

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
	cache.EXPECT().WriteResources(mock.Anything, closedKey, mock.Anything).Return("", fmt.Errorf("write boom"))

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
