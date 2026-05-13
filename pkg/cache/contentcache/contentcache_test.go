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
	"testing"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	mockcachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- NewContentCache ---

func TestNewContentCache(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	cc := NewContentCache(mockCache)
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
	cc := &contentCache{cache: mockCache}

	_, err := cc.GetPackageContent(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "not found")
}

func TestGetPackageContent_PkgNotFound(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).
		Return([]repository.PackageRevision{}, nil)

	cc := &contentCache{cache: mockCache}
	_, err := cc.GetPackageContent(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "not found")
}

func TestGetPackageContent_ListError(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("list failed"))

	cc := &contentCache{cache: mockCache}
	_, err := cc.GetPackageContent(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "list failed")
}

// --- UpdateLifecycle ---

func TestUpdateLifecycle_DirectUpdate_PublishedToDeletionProposed(t *testing.T) {
	cc, _, _, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecyclePublished)
	mockPkgRev.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecycleDeletionProposed).Return(nil)

	content, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "DeletionProposed")
	assert.NoError(t, err)
	assert.NotNil(t, content)
}

func TestUpdateLifecycle_DirectUpdate_DeletionProposedToPublished(t *testing.T) {
	cc, _, _, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDeletionProposed)
	mockPkgRev.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecyclePublished).Return(nil)

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
	cc, _, _, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleProposed)

	// Proposed → Published uses direct UpdateLifecycle (no draft cycle).
	mockPkgRev.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecyclePublished).Return(nil)

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
	mockPkgRev.EXPECT().Key().Return(repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{Package: testPkg},
		WorkspaceName: testWS,
	})
	mockPkgRev.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecycle("Bogus"))

	cc := &contentCache{cache: mockCache}
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
	cc := &contentCache{cache: mockCache}

	_, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Proposed")
	assert.ErrorContains(t, err, "not found")
}

func TestUpdateLifecycle_DraftCycle_RepoNotFoundForDraft(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockPkgRev := mockrepository.NewMockPackageRevision(t)

	mockCache.EXPECT().GetRepositories().Return([]*configapi.Repository{testRepoSpec}).Maybe()
	mockCache.EXPECT().GetRepository(testFullRepoKey).Return(mockRepo).Once()
	mockCache.EXPECT().GetRepository(testFullRepoKey).Return(nil).Once()
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).
		Return([]repository.PackageRevision{mockPkgRev}, nil)
	mockPkgRev.EXPECT().Key().Return(repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{Package: testPkg},
		WorkspaceName: testWS,
	})
	mockPkgRev.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecycleDraft)

	cc := &contentCache{cache: mockCache}
	_, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Proposed")
	assert.ErrorContains(t, err, "not found")
}

func TestUpdateLifecycle_DraftCycle_UpdatePackageRevisionError(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)
	mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockPkgRev).Return(nil, fmt.Errorf("lock conflict"))

	_, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Proposed")
	assert.ErrorContains(t, err, "lock conflict")
}

func TestUpdateLifecycle_DraftCycle_UpdateLifecycleOnDraftError(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)

	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockPkgRev).Return(mockDraft, nil)
	mockDraft.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecycleProposed).Return(fmt.Errorf("invalid transition"))

	_, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "Proposed")
	assert.ErrorContains(t, err, "invalid transition")
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

func TestUpdateLifecycle_DirectUpdate_Error(t *testing.T) {
	cc, _, _, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecyclePublished)
	mockPkgRev.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecycleDeletionProposed).Return(fmt.Errorf("branch protected"))

	_, err := cc.UpdateLifecycle(context.Background(), testRepoKey, testPkg, testWS, "DeletionProposed")
	assert.ErrorContains(t, err, "branch protected")
}

// --- CreateDraftFromExisting ---

func TestCreateDraftFromExisting(t *testing.T) {
	cc, _, mockRepo, mockPkgRev := setupCacheWithPkgRev(t, porchapi.PackageRevisionLifecycleDraft)

	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockPkgRev).Return(mockDraft, nil)
	mockDraft.EXPECT().Key().Return(repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{Package: testPkg},
		WorkspaceName: testWS,
	})

	draft, err := cc.CreateDraftFromExisting(context.Background(), testRepoKey, testPkg, testWS)
	require.NoError(t, err)
	assert.Equal(t, testPkg, draft.Key().PkgKey.Package)
}

func TestCreateDraftFromExisting_RepoNotFound(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockGetRepoNotFound(mockCache)
	cc := &contentCache{cache: mockCache}

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

	cc := &contentCache{cache: mockCache}
	_, err := cc.CreateDraftFromExisting(context.Background(), testRepoKey, testPkg, testWS)
	assert.ErrorContains(t, err, "list failed")
}

// --- CreateNewDraft ---

func TestCreateNewDraft(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	cc := &contentCache{cache: mockCache}

	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.MatchedBy(func(obj *porchapi.PackageRevision) bool {
		return obj.Spec.PackageName == testPkg &&
			obj.Spec.WorkspaceName == testWS &&
			obj.Spec.RepositoryName == testRepoKey.Name &&
			obj.Spec.Lifecycle == porchapi.PackageRevisionLifecycleDraft
	})).Return(mockDraft, nil)
	mockDraft.EXPECT().Key().Return(repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{Package: testPkg},
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
	cc := &contentCache{cache: mockCache}

	_, err := cc.CreateNewDraft(context.Background(), testRepoKey, testPkg, testWS, "Draft")
	assert.ErrorContains(t, err, "not found")
}

func TestCreateNewDraft_CreateError(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	cc := &contentCache{cache: mockCache}

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
	cc := &contentCache{cache: mockCache}

	draft := &draftSlimWrapper{inner: mockrepository.NewMockPackageRevisionDraft(t)}
	err := cc.CloseDraft(context.Background(), testRepoKey, draft, 0)
	assert.ErrorContains(t, err, "not found")
}

func TestCloseDraft_WrongDraftType(t *testing.T) {
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockGetRepo(mockCache, mockRepo)
	cc := &contentCache{cache: mockCache}

	fakeDraft := &fakeDraftSlim{}
	err := cc.CloseDraft(context.Background(), testRepoKey, fakeDraft, 0)
	assert.ErrorContains(t, err, "not a contentCache draft")
}

type fakeDraftSlim struct{}

func (f *fakeDraftSlim) Key() repository.PackageRevisionKey { return repository.PackageRevisionKey{} }
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
	cc := &contentCache{cache: mockCache}

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

	cc := &contentCache{cache: mockCache}
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
