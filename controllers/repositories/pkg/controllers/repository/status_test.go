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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
)

// Test helper functions
func setupTest() (*runtime.Scheme, context.Context) {
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)
	return scheme, context.Background()
}

func TestSetCondition(t *testing.T) {
	repo := createTestRepo("test-repo", "test-ns")
	repo.Generation = 5

	reconciler := &RepositoryReconciler{}
	reconciler.setCondition(repo, configapi.RepositoryReady, metav1.ConditionTrue, configapi.ReasonReady, "Repository is ready")

	require.Len(t, repo.Status.Conditions, 1)

	condition := repo.Status.Conditions[0]
	assert.Equal(t, configapi.RepositoryReady, condition.Type)
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
	assert.Equal(t, configapi.ReasonReady, condition.Reason)
	assert.Equal(t, int64(5), condition.ObservedGeneration)
}

func TestUpdateRepoStatusWithBackoff(t *testing.T) {
	_, ctx := setupTest()

	tests := []struct {
		name        string
		patchError  error
		expectError bool
	}{
		{
			name:        "successful patch",
			patchError:  nil,
			expectError: false,
		},
		{
			name:        "conflict error retries",
			patchError:  apierrors.NewConflict(schema.GroupResource{}, "test", errors.New("conflict")),
			expectError: false,
		},
		{
			name:        "non-conflict error fails",
			patchError:  errors.New("network error"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := createTestRepo("test-repo", "test-ns")

			mockClient := mockclient.NewMockClient(t)
			mockStatusWriter := mockclient.NewMockSubResourceWriter(t)

			mockClient.EXPECT().Status().Return(mockStatusWriter)
			if tt.name == "conflict error retries" {
				// First call returns conflict, second call succeeds
				mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(tt.patchError).Once()
				mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
			} else {
				mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(tt.patchError)
			}

			reconciler := &RepositoryReconciler{Client: mockClient}
			err := reconciler.updateRepoStatusWithBackoff(ctx, repo, RepositoryStatusReady, nil, nil)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHasSpecChanged(t *testing.T) {
	tests := []struct {
		name     string
		repo     *configapi.Repository
		expected bool
	}{
		{
			name:     "no conditions - new repo",
			repo:     createTestRepo("test-repo", "test-ns"),
			expected: true, // New repos have Generation=1, ObservedGeneration=0, so spec changed
		},
		{
			name: "controller restart - status ObservedGeneration present",
			repo: func() *configapi.Repository {
				repo := createTestRepo("test-repo", "test-ns")
				repo.Generation = 5
				repo.Status.ObservedGeneration = 5 // Matches current generation
				repo.Status.Conditions = []metav1.Condition{{
					Type:               configapi.RepositoryReady,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: 5,
				}}
				return repo
			}(),
			expected: false, // No spec change
		},
		{
			name: "controller restart during spec change - generation mismatch",
			repo: func() *configapi.Repository {
				repo := createTestRepo("test-repo", "test-ns")
				repo.Generation = 6                // Spec was changed
				repo.Status.ObservedGeneration = 5 // Controller crashed before updating status
				repo.Status.Conditions = []metav1.Condition{{
					Type:               configapi.RepositoryReady,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: 5,
				}}
				return repo
			}(),
			expected: true, // Spec changed - controller must reconcile the change
		},
		{
			name: "observed generation matches - no change",
			repo: func() *configapi.Repository {
				repo := createTestRepo("test-repo", "test-ns")
				repo.Generation = 5
				repo.Status.ObservedGeneration = 5
				repo.Status.Conditions = []metav1.Condition{{
					Type:               configapi.RepositoryReady,
					ObservedGeneration: 5,
				}}
				return repo
			}(),
			expected: false,
		},
		{
			name: "observed generation differs - spec changed",
			repo: func() *configapi.Repository {
				repo := createTestRepo("test-repo", "test-ns")
				repo.Generation = 6
				repo.Status.ObservedGeneration = 5
				repo.Status.Conditions = []metav1.Condition{{
					Type:               configapi.RepositoryReady,
					ObservedGeneration: 5,
				}}
				return repo
			}(),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &RepositoryReconciler{}
			result := reconciler.hasSpecChanged(tt.repo)

			assert.Equal(t, tt.expected, result)
		})
	}
}
func TestUpdateRepoStatusWithBackoffExtended(t *testing.T) {
	_, ctx := setupTest()

	tests := []struct {
		name         string
		status       RepositoryStatus
		syncError    error
		nextSyncTime *time.Time
		expectError  bool
	}{
		{
			name:         "status ready with next sync time",
			status:       RepositoryStatusReady,
			syncError:    nil,
			nextSyncTime: func() *time.Time { t := time.Now().Add(time.Hour); return &t }(),
			expectError:  false,
		},
		{
			name:         "status error with sync error",
			status:       RepositoryStatusError,
			syncError:    errors.New("sync failed"),
			nextSyncTime: nil,
			expectError:  false,
		},
		{
			name:         "status sync in progress",
			status:       RepositoryStatusSyncInProgress,
			syncError:    nil,
			nextSyncTime: nil,
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := createTestRepo("test-repo", "test-ns")

			mockClient := mockclient.NewMockClient(t)
			mockStatusWriter := mockclient.NewMockSubResourceWriter(t)

			mockClient.EXPECT().Status().Return(mockStatusWriter)
			mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

			reconciler := &RepositoryReconciler{Client: mockClient}
			err := reconciler.updateRepoStatusWithBackoff(ctx, repo, tt.status, tt.syncError, tt.nextSyncTime)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHasSpecChangedEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		repo     *configapi.Repository
		expected bool
	}{
		{
			name: "multiple conditions - uses status ObservedGeneration",
			repo: func() *configapi.Repository {
				repo := createTestRepo("test-repo", "test-ns")
				repo.Generation = 5
				repo.Status.ObservedGeneration = 5
				repo.Status.Conditions = []metav1.Condition{
					{
						Type:               "SomeOtherCondition",
						ObservedGeneration: 3,
					},
					{
						Type:               configapi.RepositoryReady,
						ObservedGeneration: 5,
					},
				}
				return repo
			}(),
			expected: false,
		},
		{
			name: "zero generation values",
			repo: func() *configapi.Repository {
				repo := createTestRepo("test-repo", "test-ns")
				repo.Generation = 0
				repo.Status.ObservedGeneration = 0
				repo.Status.Conditions = []metav1.Condition{{
					Type:               configapi.RepositoryReady,
					ObservedGeneration: 0,
				}}
				return repo
			}(),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &RepositoryReconciler{}
			result := reconciler.hasSpecChanged(tt.repo)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildRepositoryCondition(t *testing.T) {
	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-repo",
			Namespace:  "default",
			Generation: 1,
		},
	}

	tests := []struct {
		name      string
		status    RepositoryStatus
		errorMsg  string
		expected  metav1.ConditionStatus
		reason    string
		message   string
		expectErr bool
	}{
		{
			name:     "SyncInProgress",
			status:   RepositoryStatusSyncInProgress,
			errorMsg: "",
			expected: metav1.ConditionFalse,
			reason:   configapi.ReasonReconciling,
			message:  "Repository reconciliation in progress",
		},
		{
			name:     "Ready",
			status:   RepositoryStatusReady,
			errorMsg: "",
			expected: metav1.ConditionTrue,
			reason:   configapi.ReasonReady,
			message:  "Repository Ready",
		},
		{
			name:     "ErrorWithMessage",
			status:   RepositoryStatusError,
			errorMsg: "some error",
			expected: metav1.ConditionFalse,
			reason:   configapi.ReasonError,
			message:  "some error",
		},
		{
			name:     "ErrorWithoutMessage",
			status:   RepositoryStatusError,
			errorMsg: "",
			expected: metav1.ConditionFalse,
			reason:   configapi.ReasonError,
			message:  "unknown error",
		},
		{
			name:      "UnknownStatus",
			status:    "unknown",
			errorMsg:  "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond, err := buildRepositoryCondition(repo, tt.status, tt.errorMsg, nil)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, cond.Status)
				assert.Equal(t, tt.reason, cond.Reason)
				assert.Equal(t, tt.message, cond.Message)
				assert.Equal(t, repo.Generation, cond.ObservedGeneration)
				assert.False(t, cond.LastTransitionTime.IsZero())
			}
		})
	}
}
