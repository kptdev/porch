// Copyright 2025 The kpt Authors
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

package engine

import (
	"context"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestPushPublishedPackageRevision_PushDraftsDisabled(t *testing.T) {
	ctx := context.TODO()

	tests := []struct {
		name        string
		setupMocks  func(*mockrepo.MockRepository, *mockrepo.MockPackageRevision, *mockrepo.MockPackageRevisionDraft)
		expectError bool
	}{
		{
			name: "Lifecycle is not in Published state",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecycleDraft).Once()
			},
			expectError: true,
		},
		{
			name: "GetPackageRevision fails",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(nil, assert.AnError).Once()
			},
			expectError: true,
		},
		{
			name: "GetResources fails",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(nil, assert.AnError).Once()
			},
			expectError: true,
		},
		{
			name: "CreatePackageRevisionDraft fails",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil).Once()
				mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()
			},
			expectError: true,
		},
		{
			name: "UpdateResources fails",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil).Once()
				mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(mockPRD, nil).Once()
				mockPRD.EXPECT().UpdateResources(mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError).Once()
			},
			expectError: true,
		},
		{
			name: "UpdateLifecycle fails",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil).Once()
				mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(mockPRD, nil).Once()
				mockPRD.EXPECT().UpdateResources(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				mockPRD.EXPECT().UpdateLifecycle(mock.Anything, mock.Anything).Return(assert.AnError).Once()
			},
			expectError: true,
		},
		{
			name: "ClosePackageRevisionDraft fails",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil).Once()
				mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(mockPRD, nil).Once()
				mockPRD.EXPECT().UpdateResources(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				mockPRD.EXPECT().UpdateLifecycle(mock.Anything, mock.Anything).Return(nil).Once()
				mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()
			},
			expectError: true,
		},
		{
			name: "GetLock fails",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil).Once()
				mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(mockPRD, nil).Once()
				mockPRD.EXPECT().UpdateResources(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				mockPRD.EXPECT().UpdateLifecycle(mock.Anything, mock.Anything).Return(nil).Once()
				mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mock.Anything, mock.Anything).Return(mockPR, nil).Once()
				mockPR.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, assert.AnError).Once()
			},
			expectError: true,
		},
		{
			name: "Push Published Package Revision succeeds",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil).Once()
				mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(mockPRD, nil).Once()
				mockPRD.EXPECT().UpdateResources(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				mockPRD.EXPECT().UpdateLifecycle(mock.Anything, mock.Anything).Return(nil).Once()
				mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mock.Anything, mock.Anything).Return(mockPR, nil).Once()
				mockPR.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Once()
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := mockrepo.NewMockRepository(t)
			mockPR := mockrepo.NewMockPackageRevision(t)
			mockPRD := mockrepo.NewMockPackageRevisionDraft(t)

			mockRepo.EXPECT().Key().Return(repository.RepositoryKey{}).Maybe()
			mockPR.EXPECT().Key().Return(repository.PackageRevisionKey{}).Maybe()

			tt.setupMocks(mockRepo, mockPR, mockPRD)

			_, err := PushPackageRevision(ctx, mockRepo, mockPR, false, nil)
			if tt.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestPushPublishedPackageRevision_PushDraftsEnabled(t *testing.T) {
	ctx := context.TODO()

	tests := []struct {
		name        string
		setupMocks  func(*mockrepo.MockRepository, *mockrepo.MockPackageRevision, *mockrepo.MockPackageRevision, *mockrepo.MockPackageRevisionDraft)
		gitPR       bool
		expectError bool
	}{
		{
			name:  "Update existing PR",
			gitPR: true,
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil).Once()
				mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockGitPR).Return(mockPRD, nil).Once()
				mockPRD.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecyclePublished).Return(nil).Once()
				mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mockPRD, mock.Anything).Return(mockPR, nil).Once()
				mockPR.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Once()
			},
			expectError: false,
		},
		{
			name:  "Existing PR found via list",
			gitPR: false,
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil).Once()
				mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{mockGitPR}, nil).Once()
				mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockGitPR).Return(mockPRD, nil).Once()
				mockPRD.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecyclePublished).Return(nil).Once()
				mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mockPRD, mock.Anything).Return(mockPR, nil).Once()
				mockPR.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Once()
			},
			expectError: false,
		},
		{
			name:  "UpdatePackageRevision fails when gitPR provided",
			gitPR: true,
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil).Once()
				mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockGitPR).Return(nil, assert.AnError).Once()
			},
			expectError: true,
		},
		{
			name:  "UpdatePackageRevision fails when gitPR found via list",
			gitPR: false,
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil).Once()
				mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{mockGitPR}, nil).Once()
				mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockGitPR).Return(nil, assert.AnError).Once()
			},
			expectError: true,
		},
		{
			name:  "ListPackageRevisions fails and falls back to creating package revision draft",
			gitPR: false,
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{Spec: porchapi.PackageRevisionSpec{Tasks: []porchapi.Task{{Type: porchapi.TaskTypePush}}}}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil).Once()
				mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()
				mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(mockPRD, nil).Once()
				mockPRD.EXPECT().UpdateResources(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				mockPRD.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecyclePublished).Return(nil).Once()
				mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mockPRD, mock.Anything).Return(mockPR, nil).Once()
				mockPR.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Once()
			},
			expectError: false,
		},
		{
			name:  "ListPackageRevisions returns empty and falls back to creating package revision draft",
			gitPR: false,
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockPR.EXPECT().Lifecycle(mock.Anything).Return(porchapi.PackageRevisionLifecyclePublished).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil).Once()
				mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{}, nil).Once()
				mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(mockPRD, nil).Once()
				mockPRD.EXPECT().UpdateResources(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				mockPRD.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecyclePublished).Return(nil).Once()
				mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mockPRD, mock.Anything).Return(mockPR, nil).Once()
				mockPR.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Once()
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := mockrepo.NewMockRepository(t)
			mockPR := mockrepo.NewMockPackageRevision(t)
			mockGitPR := mockrepo.NewMockPackageRevision(t)
			mockPRD := mockrepo.NewMockPackageRevisionDraft(t)

			mockRepo.EXPECT().Key().Return(repository.RepositoryKey{}).Maybe()
			mockPR.EXPECT().Key().Return(repository.PackageRevisionKey{}).Maybe()

			var gitPR repository.PackageRevision
			if tt.gitPR {
				gitPR = mockGitPR
			}

			tt.setupMocks(mockRepo, mockPR, mockGitPR, mockPRD)

			_, err := PushPackageRevision(ctx, mockRepo, mockPR, true, gitPR)
			if tt.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestGetOrCreateGitDraft(t *testing.T) {
	ctx := context.TODO()

	tests := []struct {
		name               string
		setupMocks         func(*mockrepo.MockRepository, *mockrepo.MockPackageRevision, *mockrepo.MockPackageRevision, *mockrepo.MockPackageRevisionDraft)
		gitPR              bool
		expectError        bool
		expectUpdatedGitPR bool
	}{
		{
			name: "UpdatePackageRevision succeeds",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockGitPR).Return(mockPRD, nil).Once()
			},
			gitPR:              true,
			expectError:        false,
			expectUpdatedGitPR: true,
		},
		{
			name: "UpdatePackageRevision fails",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockGitPR).Return(nil, assert.AnError).Once()
			},
			gitPR:              true,
			expectError:        true,
			expectUpdatedGitPR: false,
		},
		{
			name: "Existing PRs found",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{mockGitPR}, nil).Once()
				mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockGitPR).Return(mockPRD, nil).Once()
			},
			gitPR:              false,
			expectError:        false,
			expectUpdatedGitPR: true,
		},
		{
			name: "UpdatePackageRevision fails with existing PRs found",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{mockGitPR}, nil).Once()
				mockRepo.EXPECT().UpdatePackageRevision(mock.Anything, mockGitPR).Return(nil, assert.AnError).Once()
			},
			gitPR:              false,
			expectError:        true,
			expectUpdatedGitPR: false,
		},
		{
			name: "ListPackageRevisions fails and new draft package revision is created",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(mockPRD, nil).Once()
			},
			gitPR:              false,
			expectError:        false,
			expectUpdatedGitPR: false,
		},
		{
			name: "CreatePackageRevisionDraft succeeds",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(mockPRD, nil).Once()
			},
			gitPR:              false,
			expectError:        false,
			expectUpdatedGitPR: false,
		},
		{
			name: "GetPackageRevision fails",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(nil, assert.AnError).Once()
			},
			gitPR:              false,
			expectError:        true,
			expectUpdatedGitPR: false,
		},
		{
			name: "CreatePackageRevisionDraft fails",
			setupMocks: func(mockRepo *mockrepo.MockRepository, mockPR *mockrepo.MockPackageRevision, mockGitPR *mockrepo.MockPackageRevision, mockPRD *mockrepo.MockPackageRevisionDraft) {
				mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return([]repository.PackageRevision{}, nil).Once()
				mockPR.EXPECT().GetPackageRevision(mock.Anything).Return(&porchapi.PackageRevision{}, nil).Once()
				mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()
			},
			gitPR:              false,
			expectError:        true,
			expectUpdatedGitPR: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := mockrepo.NewMockRepository(t)
			mockPR := mockrepo.NewMockPackageRevision(t)
			mockGitPR := mockrepo.NewMockPackageRevision(t)
			mockPRD := mockrepo.NewMockPackageRevisionDraft(t)

			mockPR.EXPECT().Key().Return(repository.PackageRevisionKey{}).Maybe()

			var gitPR repository.PackageRevision
			if tt.gitPR {
				gitPR = mockGitPR
			}

			tt.setupMocks(mockRepo, mockPR, mockGitPR, mockPRD)

			draft, updatedGitPR, err := GetOrCreateGitDraft(ctx, mockRepo, mockPR, gitPR)

			if tt.expectError {
				assert.NotNil(t, err)
				assert.Nil(t, draft)
				assert.Nil(t, updatedGitPR)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, mockPRD, draft)
				if tt.expectUpdatedGitPR {
					assert.Equal(t, mockGitPR, updatedGitPR)
				} else {
					assert.Nil(t, updatedGitPR)
				}
			}
		})
	}
}
