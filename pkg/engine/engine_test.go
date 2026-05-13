// Copyright 2025-2026 The kpt Authors
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
	"fmt"
	"testing"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/builtins/builtintypes"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/task"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Test fixtures for common test objects
type testFixture struct {
	mockRepo        *mockrepo.MockRepository
	mockCache       *mockCache
	mockTaskHandler *mockTaskHandler
	repositoryObj   *configapi.Repository
	packageRevision *porchapi.PackageRevision
	engine          *cadEngine
}

func newTestFixture(t *testing.T) *testFixture {
	mockRepo := &mockrepo.MockRepository{}
	mockCache := &mockCache{}
	mockTaskHandler := &mockTaskHandler{}

	repositoryObj := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "default",
		},
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Directory: "test-dir",
			},
		},
	}

	packageRevision := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pkg",
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    "test-package",
			WorkspaceName:  "test-workspace",
			RepositoryName: "test-repo",
		},
	}

	// Setup common mock expectations
	mockCache.On("OpenRepository", mock.Anything, mock.MatchedBy(func(repo *configapi.Repository) bool {
		return repo.Name == "test-repo" && repo.Namespace == "default"
	})).Return(mockRepo, nil).Once()

	engine := &cadEngine{
		cache:       mockCache,
		taskHandler: mockTaskHandler,
	}

	return &testFixture{
		mockRepo:        mockRepo,
		mockCache:       mockCache,
		mockTaskHandler: mockTaskHandler,
		repositoryObj:   repositoryObj,
		packageRevision: packageRevision,
		engine:          engine,
	}
}

func setupMockPackageRevision(t *testing.T) *mockrepo.MockPackageRevision {
	mockPkgRev := &mockrepo.MockPackageRevision{}
	// Setup common mock package revision expectations
	mockPkgRev.On("Key").Return(repository.PackageRevisionKey{})
	mockPkgRev.On("GetMeta").Return(metav1.ObjectMeta{})
	mockPkgRev.On("GetKptfile", mock.Anything).Return(kptfilev1.KptFile{}, nil)
	mockPkgRev.On("KubeObjectName").Return("test-pkg")
	mockPkgRev.On("KubeObjectNamespace").Return("default")
	mockPkgRev.On("UID").Return(types.UID("test-uid"))
	mockPkgRev.On("Lifecycle", mock.Anything).Return(porchapi.PackageRevisionLifecycleDraft)
	mockPkgRev.On("GetPackageRevision", mock.Anything).Return(&porchapi.PackageRevision{}, nil)
	mockPkgRev.On("GetResources", mock.Anything).Return(&porchapi.PackageRevisionResources{}, nil)
	mockPkgRev.On("GetUpstreamLock", mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)
	mockPkgRev.On("GetLock", mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)
	mockPkgRev.On("ResourceVersion").Return("1")
	mockPkgRev.On("ToMainPackageRevision", mock.Anything).Return(mockPkgRev)
	mockPkgRev.On("SetMeta", mock.Anything, mock.Anything).Return(nil)
	return mockPkgRev
}

func TestCreatePackageRevisionRollback(t *testing.T) {
	tests := []struct {
		name          string
		setupTest     func(*testFixture, *mockrepo.MockPackageRevision, *mockrepo.MockPackageRevisionDraft)
		expectedError bool
		errorContains string
	}{
		{
			name: "rollback on task application failure",
			setupTest: func(f *testFixture, mockPkgRev *mockrepo.MockPackageRevision, mockDraft *mockrepo.MockPackageRevisionDraft) {
				mockDraft.On("UpdateResources", mock.Anything, mock.Anything, mock.Anything).Return(nil)
				mockDraft.On("UpdateLifecycle", mock.Anything, mock.Anything).Return(nil)

				f.mockRepo.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{}, nil)
				f.mockRepo.On("CreatePackageRevisionDraft", mock.Anything, mock.Anything).Return(mockDraft, nil)
				f.mockRepo.On("ClosePackageRevisionDraft", mock.Anything, mock.Anything, mock.Anything).Return(mockPkgRev, nil)
				f.mockRepo.On("DeletePackageRevision", mock.Anything, mock.Anything).Return(nil)
				f.mockRepo.On("Close", mock.Anything).Return(nil)
				f.mockRepo.On("Key", mock.Anything).Return(repository.RepositoryKey{})

				f.mockTaskHandler.On("ApplyTask", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("task application failed"))
			},
			expectedError: true,
			errorContains: "task application failed",
		},
		{
			name: "rollback on lifecycle update failure",
			setupTest: func(f *testFixture, mockPkgRev *mockrepo.MockPackageRevision, mockDraft *mockrepo.MockPackageRevisionDraft) {
				mockDraft.On("UpdateResources", mock.Anything, mock.Anything, mock.Anything).Return(nil)
				mockDraft.On("UpdateLifecycle", mock.Anything, mock.Anything).Return(fmt.Errorf("lifecycle update failed"))

				f.mockRepo.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{}, nil)
				f.mockRepo.On("CreatePackageRevisionDraft", mock.Anything, mock.Anything).Return(mockDraft, nil)
				f.mockRepo.On("ClosePackageRevisionDraft", mock.Anything, mock.Anything, mock.Anything).Return(mockPkgRev, nil)
				f.mockRepo.On("DeletePackageRevision", mock.Anything, mock.Anything).Return(nil)
				f.mockRepo.On("Close", mock.Anything).Return(nil)
				f.mockRepo.On("Key", mock.Anything).Return(repository.RepositoryKey{})

				f.mockTaskHandler.On("ApplyTask", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
			},
			expectedError: true,
			errorContains: "lifecycle update failed",
		},
		{
			name: "rollback on close draft failure",
			setupTest: func(f *testFixture, mockPkgRev *mockrepo.MockPackageRevision, mockDraft *mockrepo.MockPackageRevisionDraft) {
				mockDraft.On("UpdateResources", mock.Anything, mock.Anything, mock.Anything).Return(nil)
				mockDraft.On("UpdateLifecycle", mock.Anything, mock.Anything).Return(nil)

				f.mockRepo.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{}, nil)
				f.mockRepo.On("CreatePackageRevisionDraft", mock.Anything, mock.Anything).Return(mockDraft, nil)
				f.mockRepo.On("ClosePackageRevisionDraft", mock.Anything, mock.Anything, mock.Anything).Return(mockPkgRev, fmt.Errorf("close failed"))
				f.mockRepo.On("Close", mock.Anything).Return(nil)
				f.mockRepo.On("Key", mock.Anything).Return(repository.RepositoryKey{})

				f.mockTaskHandler.On("ApplyTask", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
			},
			expectedError: true,
			errorContains: "close failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestFixture(t)
			mockPkgRev := setupMockPackageRevision(t)
			mockDraft := &mockrepo.MockPackageRevisionDraft{}
			tt.setupTest(f, mockPkgRev, mockDraft)

			_, err := f.engine.CreatePackageRevision(context.Background(), f.repositoryObj, f.packageRevision, nil)
			if tt.expectedError {
				assert.Error(t, err)
				assert.ErrorContains(t, err, tt.errorContains)
			} else {
				assert.NoError(t, err)
			}

			f.mockRepo.Close(context.Background())

			f.mockRepo.AssertExpectations(t)
			f.mockTaskHandler.AssertExpectations(t)
			f.mockCache.AssertExpectations(t)
		})
	}
}

// Mock implementations
type mockTaskHandler struct {
	mock.Mock
}

func (m *mockTaskHandler) ApplyTask(ctx context.Context, draft repository.PackageRevisionDraft, repositoryObj *configapi.Repository, obj *porchapi.PackageRevision, packageConfig *builtintypes.PackageConfig) error {
	args := m.Called(ctx, draft, repositoryObj, obj, packageConfig)
	return args.Error(0)
}

func (m *mockTaskHandler) DoPRMutations(ctx context.Context, repoPr repository.PackageRevision, oldObj *porchapi.PackageRevision, newObj *porchapi.PackageRevision, draft repository.PackageRevisionDraft) error {
	args := m.Called(ctx, repoPr, oldObj, newObj, draft)
	return args.Error(0)
}

func (m *mockTaskHandler) DoPRResourceMutations(ctx context.Context, pr2Update repository.PackageRevision, draft repository.PackageRevisionDraft, oldRes *porchapi.PackageRevisionResources, newRes *porchapi.PackageRevisionResources) (*porchapi.RenderStatus, error) {
	args := m.Called(ctx, pr2Update, draft, oldRes, newRes)
	return args.Get(0).(*porchapi.RenderStatus), args.Error(1)
}

func (m *mockTaskHandler) GetRuntime() fn.FunctionRuntime {
	args := m.Called()
	return args.Get(0).(fn.FunctionRuntime)
}

func (m *mockTaskHandler) SetCredentialResolver(resolver repository.CredentialResolver) {
	m.Called(resolver)
}

func (m *mockTaskHandler) SetReferenceResolver(resolver repository.ReferenceResolver) {
	m.Called(resolver)
}

func (m *mockTaskHandler) SetRepoOpener(opener repository.RepositoryOpener) {
	m.Called(opener)
}

func (m *mockTaskHandler) SetRunnerOptionsResolver(resolver func(namespace string) runneroptions.RunnerOptions) {
	m.Called(resolver)
}

func (m *mockTaskHandler) SetRuntime(runtime fn.FunctionRuntime) {
	m.Called(runtime)
}

func (m *mockTaskHandler) SetRepoOperationRetryAttempts(retryAttempts int) {
	m.Called(retryAttempts)
}

type mockCache struct {
	mock.Mock
}

var _ cachetypes.Cache = &mockCache{}

func (m *mockCache) OpenRepository(ctx context.Context, repositoryObj *configapi.Repository) (repository.Repository, error) {
	args := m.Called(ctx, repositoryObj)
	return args.Get(0).(repository.Repository), args.Error(1)
}

func (m *mockCache) CloseRepository(ctx context.Context, repositoryObj *configapi.Repository, repositories []configapi.Repository) error {
	args := m.Called(ctx, repositoryObj, repositories)
	return args.Error(0)
}

func (m *mockCache) GetRepositories() []*configapi.Repository {
	args := m.Called()
	return args.Get(0).([]*configapi.Repository)
}

func (m *mockCache) GetRepository(repoKey repository.RepositoryKey) repository.Repository {
	args := m.Called()
	return args.Get(0).(repository.Repository)
}

func (m *mockCache) UpdateRepository(ctx context.Context, repositoryObj *configapi.Repository) error {
	args := m.Called(ctx, repositoryObj)
	return args.Error(0)
}

func (m *mockCache) CheckRepositoryConnectivity(ctx context.Context, repositorySpec *configapi.Repository) error {
	args := m.Called(ctx, repositorySpec)
	return args.Error(0)
}

func (m *mockCache) FindAllUpstreamReferencesInRepositories(ctx context.Context, namespace, prName string) (string, error) {
	args := m.Called(ctx, namespace, prName)
	return args.String(0), args.Error(1)
}

func (m *mockCache) ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	args := m.Called(ctx, filter)
	return args.Get(0).([]repository.PackageRevision), args.Error(1)
}

func TestCreatePRWith2Tasks(t *testing.T) {
	pr := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeInit,
					Init: &porchapi.PackageInitTaskSpec{},
				},
				{
					Type: porchapi.TaskTypeEdit,
					Edit: &porchapi.PackageEditTaskSpec{
						Source: &porchapi.PackageRevisionRef{
							Name: "test",
						},
					},
				},
			},
		},
	}

	engine := &cadEngine{}

	_, err := engine.CreatePackageRevision(context.TODO(), nil, pr, nil)
	assert.ErrorContains(t, err, "must not contain more than one")
}

func TestCreatePRInitIsAdded(t *testing.T) {
	pr := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			// short-circuit the method
			Lifecycle: "test",
		},
	}

	engine := &cadEngine{}

	_, err := engine.CreatePackageRevision(context.TODO(), nil, pr, nil)

	require.ErrorContains(t, err, "unsupported lifecycle value")
	require.Len(t, pr.Spec.Tasks, 1)
	require.Equal(t, pr.Spec.Tasks[0].Type, porchapi.TaskTypeInit)
}

func TestValidateUpgradeTask(t *testing.T) {
	oldUpstream := &fake.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: "blueprint",
				},
				Package: "test-package",
			},
			WorkspaceName: "v1",
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
	}

	newUpstream := oldUpstream
	newUpstream.PrKey.WorkspaceName = "v2"

	t.Run("Successful", func(t *testing.T) {
		local := &fake.FakePackageRevision{
			PrKey: repository.PackageRevisionKey{
				PkgKey: repository.PackageKey{
					RepoKey: repository.RepositoryKey{
						Name: "deployment",
					},
					Package: "test-package",
				},
				WorkspaceName: "v1",
			},
			PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		}

		revs := []repository.PackageRevision{oldUpstream, newUpstream, local}
		spec := &porchapi.PackageUpgradeTaskSpec{
			OldUpstream: porchapi.PackageRevisionRef{
				Name: oldUpstream.KubeObjectName(),
			},
			NewUpstream: porchapi.PackageRevisionRef{
				Name: newUpstream.KubeObjectName(),
			},
			LocalPackageRevisionRef: porchapi.PackageRevisionRef{
				Name: local.KubeObjectName(),
			},
		}

		err := validateUpgradeTask(context.TODO(), revs, spec)
		assert.NoError(t, err)
	})

	t.Run("Failure", func(t *testing.T) {
		local := &fake.FakePackageRevision{
			PrKey: repository.PackageRevisionKey{
				PkgKey: repository.PackageKey{
					RepoKey: repository.RepositoryKey{
						Name: "deployment",
					},
					Package: "test-package",
				},
				WorkspaceName: "v1",
			},
			PackageLifecycle: porchapi.PackageRevisionLifecycleDraft,
		}

		revs := []repository.PackageRevision{oldUpstream, newUpstream, local}
		spec := &porchapi.PackageUpgradeTaskSpec{
			OldUpstream: porchapi.PackageRevisionRef{
				Name: oldUpstream.KubeObjectName(),
			},
			NewUpstream: porchapi.PackageRevisionRef{
				Name: newUpstream.KubeObjectName(),
			},
			LocalPackageRevisionRef: porchapi.PackageRevisionRef{
				Name: local.KubeObjectName(),
			},
		}

		err := validateUpgradeTask(context.TODO(), revs, spec)
		assert.ErrorContains(t, err, "must be published")
		assert.ErrorContains(t, err, local.KubeObjectName())
	})
}

func TestCreateCloneTaskValidation(t *testing.T) {
	tests := []struct {
		name          string
		existingRevs  []repository.PackageRevision
		expectedError bool
		errorContains string
	}{
		{
			name:          "success - no existing revisions",
			existingRevs:  []repository.PackageRevision{},
			expectedError: false,
		},
		{
			name: "success - existing revision in different repo",
			existingRevs: []repository.PackageRevision{
				&fake.FakePackageRevision{
					PrKey: repository.PackageRevisionKey{
						PkgKey: repository.PackageKey{
							RepoKey: repository.RepositoryKey{
								Name: "different-repo",
							},
							Package: "test-package",
						},
						WorkspaceName: "v1",
					},
				},
			},
			expectedError: false,
		},
		{
			name: "failure - existing revision with same package and repo",
			existingRevs: []repository.PackageRevision{
				&fake.FakePackageRevision{
					PrKey: repository.PackageRevisionKey{
						PkgKey: repository.PackageKey{
							RepoKey: repository.RepositoryKey{
								Name: "test-repo",
							},
							Package: "test-package",
						},
						WorkspaceName: "v1",
					},
				},
			},
			expectedError: true,
			errorContains: "`clone` cannot create a new revision for package \"test-package\" that already exists in repo \"test-repo\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestFixture(t)
			mockPkgRev := setupMockPackageRevision(t)
			mockDraft := &mockrepo.MockPackageRevisionDraft{}

			// Create a package revision with CLONE task
			f.packageRevision.Spec.Tasks = []porchapi.Task{
				{
					Type: porchapi.TaskTypeClone,
					Clone: &porchapi.PackageCloneTaskSpec{
						Upstream: porchapi.UpstreamPackage{
							Type: porchapi.RepositoryTypeGit,
							Git: &porchapi.GitPackage{
								Repo:      "https://example.com/repo",
								Ref:       "main",
								Directory: "/",
							},
						},
					},
				},
			}

			// Setup mocks
			mockDraft.On("UpdateResources", mock.Anything, mock.Anything, mock.Anything).Return(nil)
			mockDraft.On("UpdateLifecycle", mock.Anything, mock.Anything).Return(nil)

			f.mockRepo.On("ListPackageRevisions", mock.Anything, mock.Anything).Return(tt.existingRevs, nil)
			f.mockRepo.On("CreatePackageRevisionDraft", mock.Anything, mock.Anything).Return(mockDraft, nil).Maybe()
			f.mockRepo.On("ClosePackageRevisionDraft", mock.Anything, mock.Anything, mock.Anything).Return(mockPkgRev, nil).Maybe()
			f.mockRepo.On("Close", mock.Anything).Return(nil).Maybe()
			f.mockRepo.On("Key", mock.Anything).Return(repository.RepositoryKey{}).Maybe()

			f.mockTaskHandler.On("ApplyTask", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

			_, err := f.engine.CreatePackageRevision(context.Background(), f.repositoryObj, f.packageRevision, nil)

			if tt.expectedError {
				assert.Error(t, err)
				assert.ErrorContains(t, err, tt.errorContains)
			} else {
				assert.NoError(t, err)
			}

			f.mockRepo.Close(context.Background())
			f.mockRepo.AssertExpectations(t)
		})
	}
}

func TestPathsOverlap(t *testing.T) {
	tests := []struct {
		name     string
		path1    string
		path2    string
		overlaps bool
	}{
		{
			name:     "identical paths",
			path1:    "pkg",
			path2:    "pkg",
			overlaps: false,
		},
		{
			name:     "path2 is child of path1",
			path1:    "parent",
			path2:    "parent/child",
			overlaps: true,
		},
		{
			name:     "path1 is child of path2",
			path1:    "parent/child",
			path2:    "parent",
			overlaps: true,
		},
		{
			name:     "sibling paths",
			path1:    "pkg1",
			path2:    "pkg2",
			overlaps: false,
		},
		{
			name:     "similar prefix no overlap",
			path1:    "test",
			path2:    "test-package",
			overlaps: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := repository.PathsOverlap(tt.path1, tt.path2)
			assert.Equal(t, tt.overlaps, result)
		})
	}
}

func TestValidatePackagePathOverlap(t *testing.T) {
	tests := []struct {
		name          string
		newPr         *porchapi.PackageRevision
		existingRevs  []repository.PackageRevision
		expectError   bool
		errorContains string
	}{
		{
			name: "no conflict - empty list",
			newPr: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					PackageName:    "pkg1",
					RepositoryName: "repo1",
				},
			},
			existingRevs: []repository.PackageRevision{},
			expectError:  false,
		},
		{
			name: "no conflict - sibling paths",
			newPr: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					PackageName:    "pkg1",
					RepositoryName: "repo1",
				},
			},
			existingRevs: []repository.PackageRevision{
				&fake.FakePackageRevision{
					PrKey: repository.PackageRevisionKey{
						PkgKey: repository.PackageKey{
							RepoKey: repository.RepositoryKey{Name: "repo1"},
							Package: "pkg2",
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "conflict - nested path",
			newPr: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					PackageName:    "parent/child",
					RepositoryName: "repo1",
				},
			},
			existingRevs: []repository.PackageRevision{
				&fake.FakePackageRevision{
					PrKey: repository.PackageRevisionKey{
						PkgKey: repository.PackageKey{
							RepoKey: repository.RepositoryKey{Name: "repo1"},
							Package: "parent",
						},
					},
				},
			},
			expectError:   true,
			errorContains: "conflicts with existing package",
		},
		{
			name: "error - duplicate package",
			newPr: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					PackageName:    "pkg1",
					RepositoryName: "repo1",
				},
			},
			existingRevs: []repository.PackageRevision{
				&fake.FakePackageRevision{
					PrKey: repository.PackageRevisionKey{
						PkgKey: repository.PackageKey{
							RepoKey: repository.RepositoryKey{Name: "repo1"},
							Package: "pkg1",
						},
					},
				},
			},
			expectError:   true,
			errorContains: "already exists",
		},
		{
			name: "no conflict - different repository",
			newPr: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					PackageName:    "pkg1",
					RepositoryName: "repo1",
				},
			},
			existingRevs: []repository.PackageRevision{
				&fake.FakePackageRevision{
					PrKey: repository.PackageRevisionKey{
						PkgKey: repository.PackageKey{
							RepoKey: repository.RepositoryKey{Name: "repo2"},
							Package: "pkg1",
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repository.ValidatePackagePathOverlap(tt.newPr, tt.existingRevs)
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.ErrorContains(t, err, tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFindUpstreamReference(t *testing.T) {
	tests := []struct {
		name          string
		namespace     string
		prName        string
		expected      string
		expectedError bool
		errorContains string
	}{
		{
			name:          "success - finds downstream",
			namespace:     "default",
			prName:        "upstream-pkg",
			expected:      "downstream-pkg",
			expectedError: false,
		},
		{
			name:          "success - no downstream found",
			namespace:     "test",
			prName:        "upstream-pkg",
			expected:      "",
			expectedError: false,
		},
		{
			name:          "error - cache query fails",
			namespace:     "default",
			prName:        "upstream-pkg",
			expectedError: true,
			errorContains: "cache error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCache := &mockCache{}
			if tt.expectedError {
				mockCache.On("FindAllUpstreamReferencesInRepositories", mock.Anything, tt.namespace, tt.prName).Return("", fmt.Errorf("cache error"))
			} else {
				mockCache.On("FindAllUpstreamReferencesInRepositories", mock.Anything, tt.namespace, tt.prName).Return(tt.expected, nil)
			}

			engine := &cadEngine{cache: mockCache}
			result, err := engine.FindAllUpstreamReferencesInRepositories(context.Background(), tt.namespace, tt.prName)

			if tt.expectedError {
				assert.Error(t, err)
				assert.ErrorContains(t, err, tt.errorContains)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}

			mockCache.AssertExpectations(t)
		})
	}
}

func TestUpdatePackageResourcesRenderFailure(t *testing.T) {
	tests := []struct {
		name                  string
		renderErr             error
		closeErr              error
		prAnnotations         map[string]string
		expectPackageReturned bool
		expectError           bool
		expectErrContains     []string
		expectClose           bool
	}{
		{
			name:                  "success - no render error",
			renderErr:             nil,
			expectPackageReturned: true,
			expectError:           false,
			expectClose:           true,
		},
		{
			name:                  "push on render failure - annotation enabled",
			renderErr:             &task.RenderError{Err: fmt.Errorf("render failed")},
			prAnnotations:         map[string]string{porchapi.PushOnFnRenderFailureKey: "true"},
			expectPackageReturned: false,
			expectError:           true,
			expectClose:           true,
		},
		{
			name:                  "no push on render failure - no annotation",
			renderErr:             &task.RenderError{Err: fmt.Errorf("render failed")},
			expectPackageReturned: false,
			expectError:           true,
			expectClose:           false,
		},
		{
			name:                  "push on render failure - close draft also fails",
			renderErr:             &task.RenderError{Err: fmt.Errorf("render failed")},
			closeErr:              fmt.Errorf("git push failed"),
			prAnnotations:         map[string]string{porchapi.PushOnFnRenderFailureKey: "true"},
			expectPackageReturned: false,
			expectError:           true,
			expectErrContains:     []string{"git push failed", "render failed"},
			expectClose:           true,
		},
		{
			name:                  "persistence failure - no push even with annotation",
			renderErr:             &task.RenderPersistError{RenderErr: fmt.Errorf("render failed"), PersistErr: fmt.Errorf("draft update failed")},
			prAnnotations:         map[string]string{porchapi.PushOnFnRenderFailureKey: "true"},
			expectPackageReturned: false,
			expectError:           true,
			expectErrContains:     []string{"draft update failed", "render failed"},
			expectClose:           false,
		},
		{
			name:                  "generic persistence error - no push even with annotation",
			renderErr:             fmt.Errorf("draft update failed"),
			prAnnotations:         map[string]string{porchapi.PushOnFnRenderFailureKey: "true"},
			expectPackageReturned: false,
			expectError:           true,
			expectErrContains:     []string{"draft update failed"},
			expectClose:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := &mockrepo.MockRepository{}
			mockCache := &mockCache{}
			mockTaskHandler := &mockTaskHandler{}
			mockPkgRev := &mockrepo.MockPackageRevision{}
			mockDraft := &mockrepo.MockPackageRevisionDraft{}

			repositoryObj := &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "default",
				},
			}

			oldRes := &porchapi.PackageRevisionResources{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-pkg",
					ResourceVersion: "1",
				},
			}
			newRes := &porchapi.PackageRevisionResources{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-pkg",
					ResourceVersion: "1",
				},
			}

			mockPkgRev.On("GetPackageRevision", mock.Anything).Return(&porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.prAnnotations,
				},
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleDraft,
				},
			}, nil)

			mockPkgRev.On("Key").Return(repository.PackageRevisionKey{}).Maybe()

			mockCache.On("OpenRepository", mock.Anything, repositoryObj).Return(mockRepo, nil)
			mockRepo.On("UpdatePackageRevision", mock.Anything, mockPkgRev).Return(mockDraft, nil)

			if tt.expectClose {
				closeRet := mockPkgRev
				if tt.closeErr != nil {
					closeRet = nil
				}
				mockRepo.On("ClosePackageRevisionDraft", mock.Anything, mockDraft, 0).Return(closeRet, tt.closeErr).Once()
			}

			mockTaskHandler.On("DoPRResourceMutations", mock.Anything, mockPkgRev, mockDraft, oldRes, newRes).Return(&porchapi.RenderStatus{}, tt.renderErr)

			engine := &cadEngine{
				cache:       mockCache,
				taskHandler: mockTaskHandler,
			}

			pkgRev, renderStatus, err := engine.UpdatePackageResources(context.Background(), repositoryObj, mockPkgRev, oldRes, newRes)

			if tt.expectError {
				assert.Error(t, err)
				for _, s := range tt.expectErrContains {
					assert.Contains(t, err.Error(), s)
				}
			} else {
				assert.NoError(t, err)
			}

			if tt.expectPackageReturned {
				assert.NotNil(t, pkgRev)
			} else {
				assert.Nil(t, pkgRev)
			}

			assert.NotNil(t, renderStatus)

			if !tt.expectClose {
				mockRepo.AssertNotCalled(t, "ClosePackageRevisionDraft", mock.Anything, mockDraft, 0)
			}

			mockRepo.AssertExpectations(t)
			mockTaskHandler.AssertExpectations(t)
			mockCache.AssertExpectations(t)
		})
	}
}

func TestUpdatePackageResourcesWithoutRender(t *testing.T) {
	tests := []struct {
		name          string
		lifecycle     porchapi.PackageRevisionLifecycle
		oldRV         string
		newRV         string
		closeErr      error
		expectError   bool
		errorContains string
	}{
		{
			name:      "success - draft lifecycle",
			lifecycle: porchapi.PackageRevisionLifecycleDraft,
			oldRV:     "1",
			newRV:     "1",
		},
		{
			name:          "failure - published lifecycle rejected",
			lifecycle:     porchapi.PackageRevisionLifecyclePublished,
			oldRV:         "1",
			newRV:         "1",
			expectError:   true,
			errorContains: "cannot update a package revision with lifecycle value",
		},
		{
			name:          "failure - empty resource version",
			lifecycle:     porchapi.PackageRevisionLifecycleDraft,
			oldRV:         "1",
			newRV:         "",
			expectError:   true,
			errorContains: "resourceVersion must be specified",
		},
		{
			name:          "failure - resource version conflict",
			lifecycle:     porchapi.PackageRevisionLifecycleDraft,
			oldRV:         "1",
			newRV:         "2",
			expectError:   true,
			errorContains: OptimisticLockErrorMsg,
		},
		{
			name:          "failure - close draft error",
			lifecycle:     porchapi.PackageRevisionLifecycleDraft,
			oldRV:         "1",
			newRV:         "1",
			closeErr:      fmt.Errorf("git push failed"),
			expectError:   true,
			errorContains: "git push failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := &mockrepo.MockRepository{}
			mockCache := &mockCache{}
			mockPkgRev := &mockrepo.MockPackageRevision{}
			mockDraft := &mockrepo.MockPackageRevisionDraft{}

			repositoryObj := &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "default",
				},
			}

			oldRes := &porchapi.PackageRevisionResources{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-pkg",
					ResourceVersion: tt.oldRV,
				},
			}
			newRes := &porchapi.PackageRevisionResources{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-pkg",
					ResourceVersion: tt.newRV,
				},
				Spec: porchapi.PackageRevisionResourcesSpec{
					Resources: map[string]string{"Kptfile": "test"},
				},
			}

			mockPkgRev.On("Lifecycle", mock.Anything).Return(tt.lifecycle).Maybe()
			mockPkgRev.On("Key").Return(repository.PackageRevisionKey{}).Maybe()

			// Only expect repo open + draft flow when we pass validation
			needsDraft := tt.newRV != "" && tt.oldRV == tt.newRV && tt.lifecycle == porchapi.PackageRevisionLifecycleDraft
			if needsDraft {
				mockCache.On("OpenRepository", mock.Anything, repositoryObj).Return(mockRepo, nil)
				mockRepo.On("UpdatePackageRevision", mock.Anything, mockPkgRev).Return(mockDraft, nil)
				mockDraft.On("UpdateResources", mock.Anything, mock.Anything, mock.Anything).Return(nil)

				closeRet := mockrepo.MockPackageRevision{}
				if tt.closeErr != nil {
					mockRepo.On("ClosePackageRevisionDraft", mock.Anything, mockDraft, 0).Return(nil, tt.closeErr)
				} else {
					mockRepo.On("ClosePackageRevisionDraft", mock.Anything, mockDraft, 0).Return(&closeRet, nil)
				}
			}

			engine := &cadEngine{cache: mockCache}

			result, err := engine.UpdatePackageResourcesWithoutRender(context.Background(), repositoryObj, mockPkgRev, oldRes, newRes)

			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorContains(t, err, tt.errorContains)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}

			mockRepo.AssertExpectations(t)
			mockCache.AssertExpectations(t)
			mockPkgRev.AssertExpectations(t)
		})
	}
}
