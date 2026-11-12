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
	"testing"

	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	mockcachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	mockprrstore "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/prrstore"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- NewAccessor ---

func TestNewAccessor(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	cc := NewAccessor(mockCache, nil)
	assert.NotNil(t, cc)
}

// --- GetPackageContent ---

func TestGetPackageContent(t *testing.T) {
	cc, _, _, _ := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)

	content, err := cc.GetPackageContent(context.Background(), testRepoKey, testPkg, testWS)
	require.NoError(t, err)
	assert.Equal(t, "Draft", content.Lifecycle(context.Background()))
	assert.Equal(t, testPkg, content.Key().PkgKey.Package)
}

func TestGetPackageContent_RepoNotFound(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockGetRepoNotFound(mockCache)
	cc := &accessor{cache: mockCache}

	_, err := cc.GetPackageContent(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "not found")
}

func TestGetPackageContent_PkgNotFound(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).
		Return([]repository.PackageRevision{}, nil)

	cc := &accessor{cache: mockCache}
	_, err := cc.GetPackageContent(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "not found")
}

func TestGetPackageContent_ListError(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("list failed"))

	cc := &accessor{cache: mockCache}
	_, err := cc.GetPackageContent(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "list failed")
}

// --- UpdateLifecycle ---

func TestUpdateLifecycle_PublishedToDeletionProposed_NoGitWrite(t *testing.T) {
	// No UpdateLifecycle expectation on the package revision: the mock fails if it is called.
	cc, _, _, _ := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecyclePublished)

	content, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "DeletionProposed")
	assert.NoError(t, err)
	assert.NotNil(t, content)
}

func TestUpdateLifecycle_DeletionProposedToPublished_NoGitWrite(t *testing.T) {
	cc, _, _, _ := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDeletionProposed)

	content, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Published")
	assert.NoError(t, err)
	assert.NotNil(t, content)
}

func TestUpdateLifecycle_DraftCycle_DraftToProposed(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)

	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockPkgRev).Return(mockDraft, nil)
	mockDraft.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecycleProposed).Return(nil)
	mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mockDraft, 0).Return(mockPkgRev, nil)

	content, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Proposed")
	assert.NoError(t, err)
	assert.NotNil(t, content)
}

func TestUpdateLifecycle_DraftCycle_ProposedToDraft(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleProposed)

	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockPkgRev).Return(mockDraft, nil)
	mockDraft.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecycleDraft).Return(nil)
	mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mockDraft, 0).Return(mockPkgRev, nil)

	content, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Draft")
	assert.NoError(t, err)
	assert.NotNil(t, content)
}

func TestUpdateLifecycle_DraftCycle_ProposedToPublished(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleProposed)

	// Proposed → Published goes through the draft/close cycle. The backend's own
	// UpdateLifecycle only supports Published ↔ DeletionProposed, so publishing
	// must open a draft and close it with the next revision number. With no
	// existing published revisions, the next revision is 1.
	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockPkgRev).Return(mockDraft, nil)
	mockDraft.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecyclePublished).Return(nil)
	mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mockDraft, 1).Return(mockPkgRev, nil)

	content, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Published")
	assert.NoError(t, err)
	assert.NotNil(t, content)
}

// seederRepo wraps a MockRepository and implements the draftSeeder interface so
// the publish path exercises StageDraftFromCache.
type seederRepo struct {
	*mockrepository.MockRepository
	staged map[*mockrepository.MockPackageRevisionDraft]bool
}

func (s *seederRepo) StageDraftFromCache(_ context.Context, draft repository.PackageRevisionDraft) error {
	if s.staged == nil {
		s.staged = map[*mockrepository.MockPackageRevisionDraft]bool{}
	}
	s.staged[draft.(*mockrepository.MockPackageRevisionDraft)] = true
	return nil
}

func TestUpdateLifecycle_ProposedToPublished_SeedsDraftFromCache(t *testing.T) {
	// Publishing must seed the draft from the resource store (PRRStore) before
	// close, so the committed content reflects pushed/rendered resources.
	mockCache := mockcachetypes.NewMockCache(t)
	base := mockrepository.NewMockRepository(t)
	repo := &seederRepo{MockRepository: base}
	mockPkgRev := mockrepository.NewMockPackageRevision(t)

	mockCache.EXPECT().GetRepositories().Return([]*configapi.Repository{testRepoSpec}).Maybe()
	mockCache.EXPECT().GetRepository(testFullRepoKey).Return(repo).Maybe()
	base.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).
		Return([]repository.PackageRevision{mockPkgRev}, nil).Maybe()
	mockPkgRev.EXPECT().Key().Return(key.PackageRevisionKey{
		PkgKey:        key.PackageKey{Package: testPkg},
		WorkspaceName: testWS,
	}).Maybe()
	mockPkgRev.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecycleProposed).Maybe()

	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	base.EXPECT().UpdatePackageRevision(mock.Anything, mockPkgRev).Return(mockDraft, nil)
	mockDraft.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecyclePublished).Return(nil)
	base.EXPECT().ClosePackageRevisionDraft(mock.Anything, mockDraft, 1).Return(mockPkgRev, nil)

	cc := &accessor{cache: mockCache}
	content, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Published")
	assert.NoError(t, err)
	assert.NotNil(t, content)
	assert.True(t, repo.staged[mockDraft], "draft should be seeded from cache before close on publish")
}

func TestUpdateLifecycle_DraftCycle_ProposedToPublished_IncrementsRevision(t *testing.T) {
	// A published v1 already exists; publishing the Proposed revision must
	// assign revision 2 (highest published + 1).
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)

	// The revision being published (Proposed, workspace ws-1).
	target := mockrepository.NewMockPackageRevision(t)
	target.EXPECT().Key().Return(key.PackageRevisionKey{
		PkgKey:        key.PackageKey{Package: testPkg},
		WorkspaceName: testWS,
	}).Maybe()
	target.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecycleProposed).Maybe()

	// An existing published v1 of the same package.
	published := mockrepository.NewMockPackageRevision(t)
	published.EXPECT().Key().Return(key.PackageRevisionKey{
		PkgKey:        key.PackageKey{Package: testPkg},
		WorkspaceName: "ws-0",
		Revision:      1,
	}).Maybe()
	published.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Maybe()

	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).
		Return([]repository.PackageRevision{target, published}, nil).Maybe()

	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, target).Return(mockDraft, nil)
	mockDraft.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecyclePublished).Return(nil)
	mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mockDraft, 2).Return(target, nil)

	cc := &accessor{cache: mockCache}
	content, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Published")
	assert.NoError(t, err)
	assert.NotNil(t, content)
}

func TestUpdateLifecycle_InvalidCurrentLifecycle(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockPkgRev := mockrepository.NewMockPackageRevision(t)

	mockGetRepo(mockCache, mockRepo)
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).
		Return([]repository.PackageRevision{mockPkgRev}, nil)
	mockPkgRev.EXPECT().Key().Return(key.PackageRevisionKey{
		PkgKey:        key.PackageKey{Package: testPkg},
		WorkspaceName: testWS,
	})
	mockPkgRev.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecycle("Bogus"))

	cc := &accessor{cache: mockCache}
	_, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Draft")
	assert.ErrorContains(t, err, "invalid current lifecycle")
}

func TestUpdateLifecycle_InvalidDesiredLifecycle(t *testing.T) {
	cc, _, _, _ := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)

	_, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Bogus")
	assert.ErrorContains(t, err, "invalid desired lifecycle")
}

func TestUpdateLifecycle_RepoNotFound(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockGetRepoNotFound(mockCache)
	cc := &accessor{cache: mockCache}

	_, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Proposed")
	assert.ErrorContains(t, err, "not found")
}

func TestUpdateLifecycle_DraftCycle_RepoNotFoundForDraft(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockGetRepoNotFound(mockCache)
	cc := &accessor{cache: mockCache}

	_, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Proposed")
	assert.ErrorContains(t, err, "not found")
}

func TestUpdateLifecycle_DraftCycle_UpdatePackageRevisionError(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)
	mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockPkgRev).Return(nil, fmt.Errorf("conflict"))

	_, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Proposed")
	assert.ErrorContains(t, err, "conflict")
}

func TestUpdateLifecycle_DraftCycle_UpdateLifecycleOnDraftError(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)

	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockPkgRev).Return(mockDraft, nil)
	mockDraft.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecycleProposed).Return(fmt.Errorf("permission denied"))

	_, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Proposed")
	assert.ErrorContains(t, err, "permission denied")
}

func TestUpdateLifecycle_DraftCycle_CloseError(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)

	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockPkgRev).Return(mockDraft, nil)
	mockDraft.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecycleProposed).Return(nil)
	mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mockDraft, 0).Return(nil, fmt.Errorf("git push failed"))

	_, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Proposed")
	assert.ErrorContains(t, err, "git push failed")
}

// --- CreateDraftFromExisting ---

func TestCreateDraftFromExisting(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)

	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockPkgRev).Return(mockDraft, nil)
	mockDraft.EXPECT().Key().Return(key.PackageRevisionKey{
		PkgKey:        key.PackageKey{Package: testPkg},
		WorkspaceName: testWS,
	})

	draft, err := cc.CreateDraftFromExisting(context.Background(), testRepoKey, testPkg, testWS)
	require.NoError(t, err)
	assert.Equal(t, testPkg, draft.Key().PkgKey.Package)
}

func TestCreateDraftFromExisting_RepoNotFound(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockGetRepoNotFound(mockCache)
	cc := &accessor{cache: mockCache}

	_, err := cc.CreateDraftFromExisting(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "not found")
}

func TestCreateDraftFromExisting_UpdatePackageRevisionError(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)
	mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockPkgRev).Return(nil, fmt.Errorf("conflict"))

	_, err := cc.CreateDraftFromExisting(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "conflict")
}

func TestCreateDraftFromExisting_FindPkgRevError(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("list failed"))

	cc := &accessor{cache: mockCache}
	_, err := cc.CreateDraftFromExisting(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "list failed")
}

// --- CreateNewDraft ---

func TestCreateNewDraft(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	cc := &accessor{cache: mockCache}

	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.MatchedBy(func(obj *porchapi.PackageRevision) bool {
		return obj.Spec.PackageName == testPkg &&
			obj.Spec.WorkspaceName == testWS &&
			obj.Spec.RepositoryName == testRepoKey.Name &&
			obj.Spec.Lifecycle == porchapi.PackageRevisionLifecycleDraft
	})).Return(mockDraft, nil)
	mockDraft.EXPECT().Key().Return(key.PackageRevisionKey{
		PkgKey:        key.PackageKey{Package: testPkg},
		WorkspaceName: testWS,
	})

	draft, err := cc.CreateNewDraft(context.Background(), testRepoKey, testPkg, testWS, "Draft")
	require.NoError(t, err)
	assert.Equal(t, testPkg, draft.Key().PkgKey.Package)
	assert.Equal(t, testWS, draft.Key().WorkspaceName)
}

func TestCreateNewDraft_RepoNotFound(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockGetRepoNotFound(mockCache)
	cc := &accessor{cache: mockCache}

	_, err := cc.CreateNewDraft(context.Background(), testRepoKey, testPkg, testWS, "Draft")
	assert.ErrorContains(t, err, "not found")
}

func TestCreateNewDraft_CreateError(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	cc := &accessor{cache: mockCache}

	mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("already exists"))

	_, err := cc.CreateNewDraft(context.Background(), testRepoKey, testPkg, testWS, "Draft")
	assert.ErrorContains(t, err, "already exists")
}

// --- CloseDraft ---

func TestCloseDraft(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)

	innerDraft := mockrepository.NewMockPackageRevisionDraft(t)
	draft := &draftSlimWrapper{inner: innerDraft}
	mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, innerDraft, 0).Return(mockPkgRev, nil)

	err := cc.CloseDraft(context.Background(), testRepoKey, draft, 0)
	assert.NoError(t, err)
}

func TestCloseDraft_RepoNotFound(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockGetRepoNotFound(mockCache)
	cc := &accessor{cache: mockCache}

	draft := &draftSlimWrapper{inner: mockrepository.NewMockPackageRevisionDraft(t)}
	err := cc.CloseDraft(context.Background(), testRepoKey, draft, 0)
	assert.ErrorContains(t, err, "not found")
}

func TestCloseDraft_WrongDraftType(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	cc := &accessor{cache: mockCache}

	fakeDraft := &fakeDraftSlim{}
	err := cc.CloseDraft(context.Background(), testRepoKey, fakeDraft, 0)
	assert.ErrorContains(t, err, "not a contentCache draft")
}

type fakeDraftSlim struct{}

func (f *fakeDraftSlim) Key() key.PackageRevisionKey { return key.PackageRevisionKey{} }
func (f *fakeDraftSlim) UpdateResources(_ context.Context, _ map[string]string, _ string) error {
	return nil
}
func (f *fakeDraftSlim) UpdateLifecycle(_ context.Context, _ string) error { return nil }

// --- DeletePackage ---

func TestDeletePackage(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)
	mockRepo.EXPECT().DeletePackageRevision(mock.Anything, mockPkgRev).Return(nil)

	err := cc.DeletePackage(context.Background(), testRepoKey, testPkg, testWS)
	assert.NoError(t, err)
}

func TestDeletePackage_RepoNotFound(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockGetRepoNotFound(mockCache)
	cc := &accessor{cache: mockCache}

	err := cc.DeletePackage(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "not found")
}

func TestDeletePackage_DeleteError(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)
	mockRepo.EXPECT().DeletePackageRevision(mock.Anything, mockPkgRev).Return(fmt.Errorf("permission denied"))

	err := cc.DeletePackage(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "permission denied")
}

func TestDeletePackage_FindPkgRevError(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("list failed"))

	cc := &accessor{cache: mockCache}
	err := cc.DeletePackage(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "list failed")
}

// --- DeletePackage: store cleanup when git no longer has the revision ---

func expectedStoreKey() key.PackageRevisionKey {
	path, leaf := key.SplitPackagePathName(testPkg)
	return key.PackageRevisionKey{
		PkgKey:        key.PackageKey{RepoKey: testRepoKey, Path: path, Package: leaf},
		WorkspaceName: testWS,
	}
}

func TestNewAccessor_OnlyAStoreThatCanDeleteIsUsedForCleanup(t *testing.T) {
	cache := mockcachetypes.NewMockCache(t)

	withStore, ok := NewAccessor(cache, mockprrstore.NewMockPRRStore(t)).(*accessor)
	require.True(t, ok)
	assert.NotNil(t, withStore.deleter)

	readOnly, ok := NewAccessor(cache, mockprrstore.NewMockPRRReader(t)).(*accessor)
	require.True(t, ok)
	assert.Nil(t, readOnly.deleter, "a read-only store cannot delete")

	noStore, ok := NewAccessor(cache, nil).(*accessor)
	require.True(t, ok)
	assert.Nil(t, noStore.deleter)
}

// The package is gone from git but its content may still be in the store: remove it before
// the caller drops the finalizer. The caller still sees "not found" and treats it as done.
func TestDeletePackage_RevisionGoneFromGitRemovesStoreContent(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, nil)
	store := mockprrstore.NewMockPRRStore(t)
	store.EXPECT().DeleteResources(mock.Anything, expectedStoreKey()).Return(nil)

	cc := &accessor{cache: mockCache, deleter: store}
	err := cc.DeletePackage(context.Background(), testRepoKey, testPkg, testWS)

	require.Error(t, err)
	assert.True(t, repository.IsNotFoundError(err), "the not-found result is preserved")
}

func TestDeletePackage_RepositoryNotCachedRemovesStoreContent(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockGetRepoNotFound(mockCache)
	store := mockprrstore.NewMockPRRStore(t)
	store.EXPECT().DeleteResources(mock.Anything, expectedStoreKey()).Return(nil)

	cc := &accessor{cache: mockCache, deleter: store}
	err := cc.DeletePackage(context.Background(), testRepoKey, testPkg, testWS)

	assert.ErrorContains(t, err, "not found")
}

// If the store cannot be cleaned the error must not look like "not found", or the
// controller would treat the delete as done and drop the finalizer with content left behind.
func TestDeletePackage_StoreCleanupFailureIsNotTreatedAsNotFound(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockGetRepoNotFound(mockCache)
	store := mockprrstore.NewMockPRRStore(t)
	store.EXPECT().DeleteResources(mock.Anything, expectedStoreKey()).Return(fmt.Errorf("db down"))

	cc := &accessor{cache: mockCache, deleter: store}
	err := cc.DeletePackage(context.Background(), testRepoKey, testPkg, testWS)

	require.Error(t, err)
	assert.ErrorContains(t, err, "db down")
	assert.False(t, repository.IsNotFoundError(err))
}

// Errors that are not "not found" are returned as they are, and the store is not touched.
func TestDeletePackage_OtherErrorsLeaveTheStoreAlone(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("list failed"))
	store := mockprrstore.NewMockPRRStore(t) // no DeleteResources expectation

	cc := &accessor{cache: mockCache, deleter: store}
	err := cc.DeletePackage(context.Background(), testRepoKey, testPkg, testWS)

	assert.ErrorContains(t, err, "list failed")
}

// --- isKnownLifecycle ---

func TestIsKnownLifecycle(t *testing.T) {
	assert.True(t, isKnownLifecycle("Draft"))
	assert.True(t, isKnownLifecycle("Proposed"))
	assert.True(t, isKnownLifecycle("Published"))
	assert.True(t, isKnownLifecycle("DeletionProposed"))
	assert.False(t, isKnownLifecycle(""))
	assert.False(t, isKnownLifecycle("Bogus"))
}
