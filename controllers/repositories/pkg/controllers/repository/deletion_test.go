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

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	cachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
)

func TestGetAllRepositories(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		allRepos    []configapi.Repository
		listError   error
		expectError bool
	}{
		{
			name: "successful list",
			allRepos: []configapi.Repository{
				*createTestRepo("repo1", "test-ns"),
				*createTestRepo("repo2", "test-ns"),
			},
		},
		{
			name:        "list error",
			listError:   errors.New("list failed"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mockclient.NewMockClient(t)

			if tt.listError != nil {
				mockClient.EXPECT().List(ctx, &configapi.RepositoryList{}).Return(tt.listError)
			} else {
				mockClient.EXPECT().List(ctx, &configapi.RepositoryList{}).Run(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) {
					repoList := list.(*configapi.RepositoryList)
					repoList.Items = tt.allRepos
				}).Return(nil)
			}

			reconciler := &RepositoryReconciler{Client: mockClient}
			repos, err := reconciler.getAllRepositories(ctx)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, repos, len(tt.allRepos))
			}
		})
	}
}

func TestCleanupRepositoryCache(t *testing.T) {
	ctx := context.Background()
	repo := createTestRepo("test-repo", "test-ns")
	allRepos := []configapi.Repository{*createTestRepo("other-repo", "test-ns")}

	tests := []struct {
		name       string
		cacheError error
	}{
		{
			name: "successful cleanup",
		},
		{
			name:       "cache error returns error",
			cacheError: errors.New("cache close failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCache := cachetypes.NewMockCache(t)

			if tt.cacheError != nil {
				mockCache.EXPECT().CloseRepository(ctx, repo, allRepos).Return(tt.cacheError)
			} else {
				mockCache.EXPECT().CloseRepository(ctx, repo, allRepos).Return(nil)
			}

			reconciler := &RepositoryReconciler{Cache: mockCache}
			// Should not panic or return error - just logs
			reconciler.cleanupRepositoryCache(ctx, repo, allRepos)
		})
	}
}

func TestRemoveFinalizer(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		updateError error
		expectError bool
	}{
		{
			name: "successful update",
		},
		{
			name:        "update error",
			updateError: errors.New("update failed"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := createTestRepoWithFinalizer("test-repo", "test-ns")
			mockClient := mockclient.NewMockClient(t)

			if tt.updateError != nil {
				mockClient.EXPECT().Update(ctx, repo).Return(tt.updateError)
			} else {
				mockClient.EXPECT().Update(ctx, repo).Return(nil)
			}

			reconciler := &RepositoryReconciler{Client: mockClient}
			err := reconciler.removeFinalizer(ctx, repo)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Finalizer should always be removed regardless of update result
			assert.False(t, controllerutil.ContainsFinalizer(repo, RepositoryFinalizer))
		})
	}
}

func TestHandleDeletion(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name            string
		repo            *configapi.Repository
		allRepos        []configapi.Repository
		cacheError      error
		listError       error
		updateError     error
		expectRequeue   bool
		expectError     bool
		expectFinalizer bool
	}{
		{
			name: "successful deletion",
			repo: createTestRepoWithFinalizer("test-repo", "test-ns"),
			allRepos: []configapi.Repository{
				*createTestRepo("other-repo", "test-ns"),
			},
			expectFinalizer: false,
		},
		{
			name:            "successful deletion with empty repo list",
			repo:            createTestRepoWithFinalizer("test-repo", "test-ns"),
			allRepos:        []configapi.Repository{},
			expectFinalizer: false,
		},
		{
			name:            "list repositories error",
			repo:            createTestRepoWithFinalizer("test-repo", "test-ns"),
			listError:       errors.New("list failed"),
			expectError:     true,
			expectFinalizer: true,
		},
		{
			name: "cache close error prevents finalizer removal",
			repo: createTestRepoWithFinalizer("test-repo", "test-ns"),
			allRepos: []configapi.Repository{
				*createTestRepo("other-repo", "test-ns"),
			},
			cacheError:      errors.New("cache close failed"),
			expectRequeue:   true,
			expectFinalizer: true,
		},
		{
			name:        "update error after finalizer removal",
			repo:        createTestRepoWithFinalizer("test-repo", "test-ns"),
			allRepos:    []configapi.Repository{},
			updateError: errors.New("update failed"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock client
			mockClient := mockclient.NewMockClient(t)

			// Setup List expectation
			if tt.listError != nil {
				mockClient.EXPECT().List(mock.Anything, &configapi.RepositoryList{}).Return(tt.listError)
			} else {
				mockClient.EXPECT().List(mock.Anything, &configapi.RepositoryList{}).Run(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) {
					repoList := list.(*configapi.RepositoryList)
					repoList.Items = tt.allRepos
				}).Return(nil)
			}

			// Setup Update expectation (only if no list error and no cache error)
			if tt.listError == nil && tt.cacheError == nil {
				if tt.updateError != nil {
					mockClient.EXPECT().Update(mock.Anything, tt.repo).Return(tt.updateError)
				} else {
					mockClient.EXPECT().Update(mock.Anything, tt.repo).Return(nil)
				}
			}

			mockCache := cachetypes.NewMockCache(t)
			// Only expect CloseRepository call if no list error
			if tt.listError == nil {
				if tt.cacheError != nil {
					mockCache.EXPECT().CloseRepository(mock.Anything, tt.repo, tt.allRepos).Return(tt.cacheError)
				} else {
					mockCache.EXPECT().CloseRepository(mock.Anything, tt.repo, tt.allRepos).Return(nil)
				}
			}

			reconciler := &RepositoryReconciler{
				Client: mockClient,
				Cache:  mockCache,
			}

			result, err := reconciler.handleDeletion(ctx, tt.repo)

			// Check error expectation
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Check requeue expectation
			if tt.expectRequeue {
				assert.NotZero(t, result.RequeueAfter)
			} else {
				assert.Zero(t, result.RequeueAfter)
			}

			// Check finalizer expectation
			hasFinalizer := controllerutil.ContainsFinalizer(tt.repo, RepositoryFinalizer)
			assert.Equal(t, tt.expectFinalizer, hasFinalizer)
		})
	}
}

func TestHandleDeletionTimeout(t *testing.T) {
	// Create context that will timeout quickly
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Wait for context to timeout
	time.Sleep(10 * time.Millisecond)

	repo := createTestRepoWithFinalizer("test-repo", "test-ns")

	fakeClient := mockclient.NewMockClient(t)
	fakeClient.EXPECT().List(mock.Anything, &configapi.RepositoryList{}).Return(context.DeadlineExceeded)

	reconciler := &RepositoryReconciler{
		Client: fakeClient,
		Cache:  cachetypes.NewMockCache(t),
	}

	result, err := reconciler.handleDeletion(ctx, repo)

	// Should requeue on timeout, not return error
	assert.NoError(t, err)
	assert.NotZero(t, result.RequeueAfter)
}

// Helper functions
func createTestRepoWithFinalizer(name, namespace string) *configapi.Repository {
	repo := createTestRepo(name, namespace)
	controllerutil.AddFinalizer(repo, RepositoryFinalizer)
	return repo
}
