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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	cachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	mockRepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
)

func TestIsSyncInProgress(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		repo     *api.Repository
		expected bool
	}{
		{
			name: "sync in progress",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						Reason:             api.ReasonReconciling,
						LastTransitionTime: metav1.NewTime(now.Add(-5 * time.Minute)),
					}},
				},
			},
			expected: true,
		},
		{
			name: "sync stale - over 20 minutes",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "stale-repo"},
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						Reason:             api.ReasonReconciling,
						LastTransitionTime: metav1.NewTime(now.Add(-25 * time.Minute)),
					}},
				},
			},
			expected: false, // Stale sync is treated as not in progress
		},
		{
			name: "sync not in progress - ready",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:   api.RepositoryReady,
						Status: metav1.ConditionTrue,
					}},
				},
			},
			expected: false,
		},
		{
			name: "sync not in progress - failed",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:   api.RepositoryReady,
						Status: metav1.ConditionFalse,
						Reason: "Failed",
					}},
				},
			},
			expected: false,
		},
		{
			name: "no conditions",
			repo: &api.Repository{
				Status: api.RepositoryStatus{},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{}
			result := r.isSyncInProgress(context.Background(), tt.repo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateNextSyncInterval(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		repo     *api.Repository
		expected time.Duration
	}{
		{
			name: "returns health check interval when sooner",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute)),
					}},
				},
			},
			expected: 3 * time.Minute, // 5m health check - 2m elapsed
		},
		{
			name: "returns full sync interval when sooner",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-55 * time.Minute)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-1 * time.Minute)),
					}},
				},
			},
			expected: 4 * time.Minute, // Health check in 4m, full sync in 5m
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				HealthCheckFrequency: 5 * time.Minute,
				FullSyncFrequency:    1 * time.Hour,
			}
			result := r.calculateNextSyncInterval(tt.repo)
			assert.InDelta(t, tt.expected.Seconds(), result.Seconds(), 2.0)
		})
	}
}

func TestIsFullSyncDue(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		repo     *api.Repository
		expected bool
	}{
		{
			name: "never synced - no annotation",
			repo: &api.Repository{
				Status: api.RepositoryStatus{},
			},
			expected: true,
		},
		{
			name: "full sync due - old sync time",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-2 * time.Hour)},
				},
			},
			expected: true,
		},
		{
			name: "full sync not due - recent sync",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-30 * time.Minute)},
				},
			},
			expected: false,
		},
		{
			name: "blocked by error condition",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-2 * time.Hour)},
					Conditions: []metav1.Condition{{
						Type:   api.RepositoryReady,
						Status: metav1.ConditionFalse,
						Reason: api.ReasonError,
					}},
				},
			},
			expected: false,
		},
		{
			name: "blocked by pending RunOnceAt",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(time.Hour)},
					},
				},
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-2 * time.Hour)},
				},
			},
			expected: false,
		},
		{
			name: "custom schedule - sync due",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						Schedule: "*/1 * * * *", // Every minute
					},
				},
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-2 * time.Minute)},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				FullSyncFrequency: 1 * time.Hour,
			}
			result := r.isFullSyncDue(tt.repo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsOneTimeSyncDue(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		repo     *api.Repository
		expected bool
	}{
		{
			name: "one-time sync due",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(-time.Hour)},
					},
				},
			},
			expected: true,
		},
		{
			name: "one-time sync not due",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(time.Hour)},
					},
				},
			},
			expected: false,
		},
		{
			name: "no one-time sync",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{},
				},
			},
			expected: false,
		},
		{
			name: "nil sync spec",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: nil,
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{}
			result := r.isOneTimeSyncDue(tt.repo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDetermineRetryInterval(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected time.Duration
	}{
		{
			name:     "network error - no such host",
			err:      errors.New("no such host"),
			expected: 30 * time.Second,
		},
		{
			name:     "network error - connection refused",
			err:      errors.New("connection refused"),
			expected: 30 * time.Second,
		},
		{
			name:     "auth error - authentication",
			err:      errors.New("authentication failed"),
			expected: 10 * time.Minute,
		},
		{
			name:     "auth error - permission denied",
			err:      errors.New("permission denied"),
			expected: 10 * time.Minute,
		},
		{
			name:     "auth error - failed to resolve credentials",
			err:      errors.New("failed to resolve credentials"),
			expected: 10 * time.Minute,
		},
		{
			name:     "auth error - resolved credentials are invalid",
			err:      errors.New("resolved credentials are invalid"),
			expected: 10 * time.Minute,
		},
		{
			name:     "repo error - not found",
			err:      errors.New("repository not found"),
			expected: 2 * time.Minute,
		},
		{
			name:     "repo error - invalid",
			err:      errors.New("invalid repository"),
			expected: 2 * time.Minute,
		},
		{
			name:     "repo error - branch",
			err:      errors.New("branch not found"),
			expected: 2 * time.Minute,
		},
		{
			name:     "timeout error",
			err:      errors.New("timeout occurred"),
			expected: 1 * time.Minute,
		},
		{
			name:     "deadline error",
			err:      errors.New("deadline exceeded"),
			expected: 1 * time.Minute,
		},
		{
			name:     "tls error - certificate",
			err:      errors.New("certificate error"),
			expected: 5 * time.Minute,
		},
		{
			name:     "tls error - tls",
			err:      errors.New("tls handshake failed"),
			expected: 5 * time.Minute,
		},
		{
			name:     "tls error - ssl",
			err:      errors.New("ssl error"),
			expected: 5 * time.Minute,
		},
		{
			name:     "rate limit error",
			err:      errors.New("rate limit exceeded"),
			expected: 5 * time.Minute,
		},
		{
			name:     "rate limit error - too many requests",
			err:      errors.New("too many requests"),
			expected: 5 * time.Minute,
		},
		{
			name:     "unknown error",
			err:      errors.New("unknown error"),
			expected: 30 * time.Second, // Default retry interval
		},
		{
			name:     "case insensitive matching",
			err:      errors.New("NO SUCH HOST"),
			expected: 30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{}
			result := r.determineRetryInterval(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClearOneTimeSyncFlag(t *testing.T) {
	ctx := context.Background()
	now := metav1.Now()

	tests := []struct {
		name          string
		repo          *api.Repository
		patchError    error
		expectedError string
	}{
		{
			name: "clears one-time sync flag",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &now,
					},
				},
			},
		},
		{
			name: "no one-time sync flag",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{},
				},
			},
		},
		{
			name: "nil sync spec",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
				Spec: api.RepositorySpec{
					Sync: nil,
				},
			},
		},
		{
			name: "patch fails",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &now,
					},
				},
			},
			patchError:    errors.New("patch failed"),
			expectedError: "patch failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mockclient.NewMockClient(t)

			if tt.repo.Spec.Sync != nil && tt.repo.Spec.Sync.RunOnceAt != nil {
				mockClient.EXPECT().Patch(ctx, mock.Anything, mock.Anything).Return(tt.patchError)
			}

			r := &RepositoryReconciler{
				Client: mockClient,
			}

			err := r.clearOneTimeSyncFlag(ctx, tt.repo)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSyncRepository(t *testing.T) {
	ctx := context.Background()
	repo := &api.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-ns",
		},
	}

	tests := []struct {
		name          string
		openError     error
		refreshError  error
		listError     error
		expectedError string
	}{
		{
			name: "successful sync",
		},
		{
			name:          "open repository fails",
			openError:     errors.New("failed to open repository"),
			expectedError: "failed to open",
		},
		{
			name:          "refresh fails",
			refreshError:  errors.New("refresh failed"),
			expectedError: "refresh",
		},
		{
			name:          "list packages fails",
			listError:     errors.New("list failed"),
			expectedError: "list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCache := cachetypes.NewMockCache(t)
			mockRepository := mockRepo.NewMockRepository(t)

			if tt.openError != nil {
				mockCache.EXPECT().OpenRepository(ctx, repo).Return(nil, tt.openError)
			} else {
				mockCache.EXPECT().OpenRepository(ctx, repo).Return(mockRepository, nil)

				if tt.refreshError != nil {
					mockRepository.EXPECT().Refresh(ctx).Return(tt.refreshError)
				} else {
					mockRepository.EXPECT().Refresh(ctx).Return(nil)

					if tt.listError != nil {
						mockRepository.EXPECT().ListPackageRevisions(ctx, mock.Anything).Return(nil, tt.listError)
					} else {
						mockRepository.EXPECT().ListPackageRevisions(ctx, mock.Anything).Return(nil, nil)
						mockRepository.EXPECT().BranchCommitHash(ctx).Return("abc123", nil)
					}
				}
			}

			r := &RepositoryReconciler{
				Cache: mockCache,
			}

			_, _, err := r.syncRepository(ctx, repo)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsHealthCheckDue(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		repo     *api.Repository
		expected bool
	}{
		{
			name: "never checked",
			repo: &api.Repository{
				Status: api.RepositoryStatus{},
			},
			expected: true,
		},
		{
			name: "health check due",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-6 * time.Minute)),
					}},
				},
			},
			expected: true,
		},
		{
			name: "health check not due",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute)),
					}},
				},
			},
			expected: false,
		},
		{
			name: "blocked by error condition",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						Reason:             api.ReasonError,
						LastTransitionTime: metav1.NewTime(now.Add(-10 * time.Minute)),
					}},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				HealthCheckFrequency: 5 * time.Minute,
			}
			result := r.isHealthCheckDue(tt.repo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetLastFullSyncTime(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		repo     *api.Repository
		expected time.Time
	}{
		{
			name: "has LastFullSyncTime",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now},
				},
			},
			expected: now,
		},
		{
			name: "no LastFullSyncTime",
			repo: &api.Repository{
				Status: api.RepositoryStatus{},
			},
			expected: time.Time{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{}
			result := r.getLastFullSyncTime(tt.repo)
			assert.Equal(t, tt.expected.Unix(), result.Unix())
		})
	}
}

func TestGetLastStatusUpdateTime(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		repo     *api.Repository
		expected time.Time
	}{
		{
			name: "has ready condition",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now),
					}},
				},
			},
			expected: now,
		},
		{
			name: "has ready condition false",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						LastTransitionTime: metav1.NewTime(now),
					}},
				},
			},
			expected: now,
		},
		{
			name: "no conditions",
			repo: &api.Repository{
				Status: api.RepositoryStatus{},
			},
			expected: time.Time{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{}
			result := r.getLastStatusUpdateTime(tt.repo)
			assert.Equal(t, tt.expected.Unix(), result.Unix())
		})
	}
}

func TestDetermineSyncDecision(t *testing.T) {
	now := time.Now()
	ctx := context.Background()

	tests := []struct {
		name         string
		repo         *api.Repository
		expectedType OperationType
		expectedNeed bool
	}{
		{
			name: "sync in progress",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						Reason:             api.ReasonReconciling,
						LastTransitionTime: metav1.NewTime(now.Add(-5 * time.Minute)),
					}},
				},
			},
			expectedType: OperationHealthCheck,
			expectedNeed: false,
		},
		{
			name: "RunOnceAt due",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(-time.Hour)},
					},
				},
			},
			expectedType: OperationFullSync,
			expectedNeed: true,
		},
		{
			name: "spec changed",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 2,
				},
				Status: api.RepositoryStatus{
					ObservedGeneration: 1,
					Conditions: []metav1.Condition{{
						Type:   api.RepositoryReady,
						Status: metav1.ConditionTrue,
					}},
				},
			},
			expectedType: OperationFullSync,
			expectedNeed: true,
		},
		{
			name: "error retry due",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						Reason:             api.ReasonError,
						Message:            "error",
						LastTransitionTime: metav1.NewTime(now.Add(-10 * time.Minute)),
					}},
				},
			},
			expectedType: OperationHealthCheck,
			expectedNeed: true,
		},
		{
			name: "full sync due",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-2 * time.Hour)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute)),
					}},
				},
			},
			expectedType: OperationFullSync,
			expectedNeed: true,
		},
		{
			name: "health check due",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-30 * time.Minute)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-6 * time.Minute)),
					}},
				},
			},
			expectedType: OperationHealthCheck,
			expectedNeed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				HealthCheckFrequency: 5 * time.Minute,
				FullSyncFrequency:    1 * time.Hour,
			}
			r.coldStartRepos.Store(tt.repo.Namespace+"/"+tt.repo.Name, true)
			decision := r.determineSyncDecision(ctx, tt.repo)
			assert.Equal(t, tt.expectedType, decision.Type)
			assert.Equal(t, tt.expectedNeed, decision.SyncNecessary)
		})
	}
}

func TestIsErrorRetryDue(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		repo     *api.Repository
		expected bool
	}{
		{
			name: "retry time in message - due",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						Reason:             api.ReasonError,
						Message:            "error",
						LastTransitionTime: metav1.NewTime(now.Add(-10 * time.Minute)),
					}},
				},
			},
			expected: true,
		},
		{
			name: "error message without retry time - due",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						Reason:             api.ReasonError,
						Message:            "connection refused",
						LastTransitionTime: metav1.NewTime(now.Add(-time.Minute)),
					}},
				},
			},
			expected: true,
		},
		{
			name: "error message without retry time - not due",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						Reason:             api.ReasonError,
						Message:            "connection refused",
						LastTransitionTime: metav1.NewTime(now.Add(-10 * time.Second)),
					}},
				},
			},
			expected: false,
		},
		{
			name: "no error condition",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:   api.RepositoryReady,
						Status: metav1.ConditionTrue,
					}},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{}
			result := r.isErrorRetryDue(tt.repo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetRequeueInterval(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		repo     *api.Repository
		expected time.Duration
	}{
		{
			name: "error with generic message",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:    api.RepositoryReady,
						Status:  metav1.ConditionFalse,
						Reason:  api.ReasonError,
						Message: "error",
					}},
				},
			},
			expected: 30 * time.Second,
		},
		{
			name: "error with message - no retry time",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:    api.RepositoryReady,
						Status:  metav1.ConditionFalse,
						Reason:  api.ReasonError,
						Message: "connection refused",
					}},
				},
			},
			expected: 30 * time.Second,
		},
		{
			name: "error with empty message",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:    api.RepositoryReady,
						Status:  metav1.ConditionFalse,
						Reason:  api.ReasonError,
						Message: "",
					}},
				},
			},
			expected: 30 * time.Second,
		},
		{
			name: "no error - calculate next sync",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-30 * time.Minute)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute)),
					}},
				},
			},
			expected: 3 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				HealthCheckFrequency: 5 * time.Minute,
				FullSyncFrequency:    1 * time.Hour,
			}
			result := r.getRequeueInterval(tt.repo)
			assert.InDelta(t, tt.expected.Seconds(), result.Seconds(), 2.0)
		})
	}
}

func TestCalculateNextSyncIntervalExtended(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		repo     *api.Repository
		expected time.Duration
	}{
		{
			name: "RunOnceAt in future",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(10 * time.Minute)},
					},
				},
			},
			expected: 10 * time.Minute,
		},
		{
			name: "RunOnceAt in past - no status",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(-time.Hour)},
					},
				},
			},
			// When no status exists, use health check frequency to avoid tight loops
			// In production, determineSyncDecision would detect this as "sync needed" and never call this function
			expected: 5 * time.Minute,
		},
		{
			name: "custom schedule - next sync in future",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						Schedule: "0 0 * * *", // Daily at midnight
					},
				},
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(time.Date(2024, 1, 1, 10, 28, 0, 0, time.UTC)),
					}},
				},
			},
			// Health check is sooner than next cron run
			expected: 0, // Will be between 0 and 5 minutes
		},
		{
			name: "invalid schedule - fallback to default",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						Schedule: "invalid",
					},
				},
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-30 * time.Minute)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute)),
					}},
				},
			},
			expected: 3 * time.Minute,
		},
		{
			name: "no last sync time with schedule",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						Schedule: "0 * * * *", // Every hour at :00
					},
				},
				Status: api.RepositoryStatus{
					// No LastFullSyncTime - will use time.Now() as baseline
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(time.Date(2024, 1, 1, 10, 28, 0, 0, time.UTC)),
					}},
				},
			},
			expected: 0, // Will be between 0 and 5 minutes
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				HealthCheckFrequency: 5 * time.Minute,
				FullSyncFrequency:    1 * time.Hour,
			}
			result := r.calculateNextSyncInterval(tt.repo)
			if tt.expected == 0 {
				assert.True(t, result >= 0 && result <= 5*time.Minute, "expected 0-5 minutes, got %v", result)
			} else {
				assert.InDelta(t, tt.expected.Seconds(), result.Seconds(), 2.0)
			}
		})
	}
}

func TestDetermineSyncDecisionExtended(t *testing.T) {
	now := time.Now()
	ctx := context.Background()

	tests := []struct {
		name         string
		repo         *api.Repository
		expectedType OperationType
		expectedNeed bool
	}{
		{
			name: "spec changed with future RunOnceAt",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 2,
				},
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(time.Hour)},
					},
				},
				Status: api.RepositoryStatus{
					ObservedGeneration: 1,
					Conditions: []metav1.Condition{{
						Type:   api.RepositoryReady,
						Status: metav1.ConditionTrue,
					}},
				},
			},
			expectedType: OperationHealthCheck,
			expectedNeed: false,
		},
		{
			name: "nothing needed",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-30 * time.Minute)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute)),
					}},
				},
			},
			expectedType: OperationHealthCheck,
			expectedNeed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				HealthCheckFrequency: 5 * time.Minute,
				FullSyncFrequency:    1 * time.Hour,
			}
			r.coldStartRepos.Store(tt.repo.Namespace+"/"+tt.repo.Name, true)
			decision := r.determineSyncDecision(ctx, tt.repo)
			assert.Equal(t, tt.expectedType, decision.Type)
			assert.Equal(t, tt.expectedNeed, decision.SyncNecessary)
		})
	}
}

func TestIsFullSyncDue_CronScheduleEdgeCases(t *testing.T) {
	// Use a fixed time to avoid flakiness from minute boundaries
	// Set to 10:02:30 - safely in the middle of a 5-minute interval
	now := time.Date(2024, 1, 1, 10, 2, 30, 0, time.UTC)
	tests := []struct {
		name     string
		repo     *api.Repository
		expected bool
	}{
		{
			name: "cron faster than frequency - cron due",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						Schedule: "*/5 * * * *", // Every 5 minutes
					},
				},
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-6 * time.Minute)},
				},
			},
			expected: true, // Cron overrides frequency
		},
		{
			name: "invalid cron - fallback to frequency - due",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						Schedule: "invalid cron",
					},
				},
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-2 * time.Hour)},
				},
			},
			expected: true, // Falls back to frequency
		},
		{
			name: "no cron - uses frequency - due",
			repo: &api.Repository{
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-2 * time.Hour)},
				},
			},
			expected: true, // Uses default frequency
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				FullSyncFrequency: 1 * time.Hour,
			}
			result := r.isFullSyncDue(tt.repo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDetermineSyncDecision_RunOnceAtInteractions(t *testing.T) {
	now := time.Now()
	ctx := context.Background()

	tests := []struct {
		name         string
		repo         *api.Repository
		expectedType OperationType
		expectedNeed bool
	}{
		{
			name: "RunOnceAt due + cron not due - RunOnceAt wins",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(-time.Minute)}, // Due
						Schedule:  "0 */6 * * *",                             // Not due for hours
					},
				},
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-2 * time.Minute)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute)),
					}},
				},
			},
			expectedType: OperationFullSync,
			expectedNeed: true,
		},
		{
			name: "RunOnceAt future + cron due - waits for RunOnceAt",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 2,
				},
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(time.Hour)}, // Future
						Schedule:  "*/1 * * * *",                          // Due now
					},
				},
				Status: api.RepositoryStatus{
					ObservedGeneration: 1,
					LastFullSyncTime:   &metav1.Time{Time: now.Add(-2 * time.Hour)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Hour)),
					}},
				},
			},
			expectedType: OperationHealthCheck,
			expectedNeed: false, // Waits for RunOnceAt despite spec change
		},
		{
			name: "RunOnceAt due + spec changed - RunOnceAt wins",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 2,
				},
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(-time.Minute)},
					},
				},
				Status: api.RepositoryStatus{
					ObservedGeneration: 1,
					Conditions: []metav1.Condition{{
						Type:   api.RepositoryReady,
						Status: metav1.ConditionTrue,
					}},
				},
			},
			expectedType: OperationFullSync,
			expectedNeed: true,
		},
		{
			name: "RunOnceAt not due + spec changed - waits",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 2,
				},
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(10 * time.Minute)},
					},
				},
				Status: api.RepositoryStatus{
					ObservedGeneration: 1,
					Conditions: []metav1.Condition{{
						Type:   api.RepositoryReady,
						Status: metav1.ConditionTrue,
					}},
				},
			},
			expectedType: OperationHealthCheck,
			expectedNeed: false,
		},
		{
			name: "RunOnceAt not due + error retry due - waits for RunOnceAt",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(time.Hour)},
					},
				},
				Status: api.RepositoryStatus{
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						Reason:             api.ReasonError,
						Message:            "error",
						LastTransitionTime: metav1.NewTime(now.Add(-10 * time.Minute)),
					}},
				},
			},
			expectedType: OperationHealthCheck,
			expectedNeed: true, // Error retry does health check, not full sync
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				HealthCheckFrequency: 5 * time.Minute,
				FullSyncFrequency:    1 * time.Hour,
			}
			r.coldStartRepos.Store(tt.repo.Namespace+"/"+tt.repo.Name, true)
			decision := r.determineSyncDecision(ctx, tt.repo)
			assert.Equal(t, tt.expectedType, decision.Type)
			assert.Equal(t, tt.expectedNeed, decision.SyncNecessary)
		})
	}
}

func TestGetSyncStaleTimeout(t *testing.T) {
	tests := []struct {
		name             string
		syncStaleTimeout time.Duration
		expected         time.Duration
	}{
		{
			name:             "custom timeout set",
			syncStaleTimeout: 30 * time.Minute,
			expected:         30 * time.Minute,
		},
		{
			name:             "default timeout",
			syncStaleTimeout: 0,
			expected:         20 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				SyncStaleTimeout: tt.syncStaleTimeout,
			}
			result := r.getSyncStaleTimeout()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateNextFullSyncTime(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		repo     *api.Repository
		validate func(t *testing.T, result time.Time, now time.Time)
	}{
		{
			name: "with cron schedule and last sync",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						Schedule: "0 */1 * * *", // Every hour at :00
					},
				},
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-30 * time.Minute)},
				},
			},
			validate: func(t *testing.T, result time.Time, now time.Time) {
				// Just verify it returns a valid time (not zero)
				assert.False(t, result.IsZero(), "Should return a valid time")
			},
		},
		{
			name: "with invalid cron - uses default frequency",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						Schedule: "invalid",
					},
				},
			},
			validate: func(t *testing.T, result time.Time, now time.Time) {
				expected := now.Add(1 * time.Hour)
				assert.InDelta(t, expected.Unix(), result.Unix(), 2.0)
			},
		},
		{
			name: "no schedule - uses default frequency",
			repo: &api.Repository{
				Spec: api.RepositorySpec{},
			},
			validate: func(t *testing.T, result time.Time, now time.Time) {
				expected := now.Add(1 * time.Hour)
				assert.InDelta(t, expected.Unix(), result.Unix(), 2.0)
			},
		},
		{
			name: "cron with no last sync time",
			repo: &api.Repository{
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						Schedule: "0 */1 * * *", // Every hour at :00
					},
				},
				Status: api.RepositoryStatus{},
			},
			validate: func(t *testing.T, result time.Time, now time.Time) {
				// Should be in the future
				assert.True(t, result.After(now), "Next sync should be in future")
				// Should be within 1 hour
				assert.True(t, result.Before(now.Add(61*time.Minute)), "Next sync should be within 61 minutes")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				FullSyncFrequency: 1 * time.Hour,
			}
			result := r.calculateNextFullSyncTime(tt.repo)
			tt.validate(t, result, now)
		})
	}
}

func TestDetermineSyncDecision_PriorityOrder(t *testing.T) {
	now := time.Now()
	ctx := context.Background()

	tests := []struct {
		name         string
		repo         *api.Repository
		expectedType OperationType
		expectedNeed bool
		description  string
	}{
		{
			name: "priority 0: sync in progress blocks everything",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 2,
				},
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(-time.Minute)},
					},
				},
				Status: api.RepositoryStatus{
					ObservedGeneration: 1,
					LastFullSyncTime:   &metav1.Time{Time: now.Add(-2 * time.Hour)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						Reason:             api.ReasonReconciling,
						LastTransitionTime: metav1.NewTime(now.Add(-5 * time.Minute)),
					}},
				},
			},
			expectedType: OperationHealthCheck,
			expectedNeed: false,
			description:  "Sync in progress blocks RunOnceAt, spec change, and full sync",
		},
		{
			name: "priority 1: RunOnceAt beats spec change",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 2,
				},
				Spec: api.RepositorySpec{
					Sync: &api.RepositorySync{
						RunOnceAt: &metav1.Time{Time: now.Add(-time.Minute)},
					},
				},
				Status: api.RepositoryStatus{
					ObservedGeneration: 1,
					Conditions: []metav1.Condition{{
						Type:   api.RepositoryReady,
						Status: metav1.ConditionTrue,
					}},
				},
			},
			expectedType: OperationFullSync,
			expectedNeed: true,
			description:  "RunOnceAt due takes priority over spec change",
		},
		{
			name: "priority 2: spec change beats error retry",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 2,
				},
				Status: api.RepositoryStatus{
					ObservedGeneration: 1,
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						Reason:             api.ReasonError,
						Message:            "error",
						LastTransitionTime: metav1.NewTime(now.Add(-10 * time.Minute)),
					}},
				},
			},
			expectedType: OperationFullSync,
			expectedNeed: true,
			description:  "Spec change takes priority over error retry",
		},
		{
			name: "priority 3: error retry beats full sync",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{},
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-2 * time.Hour)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionFalse,
						Reason:             api.ReasonError,
						Message:            "connection refused",
						LastTransitionTime: metav1.NewTime(now.Add(-time.Minute)),
					}},
				},
			},
			expectedType: OperationHealthCheck,
			expectedNeed: true,
			description:  "Error retry takes priority over scheduled full sync",
		},
		{
			name: "priority 4: full sync beats health check",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{},
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-2 * time.Hour)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-10 * time.Minute)),
					}},
				},
			},
			expectedType: OperationFullSync,
			expectedNeed: true,
			description:  "Full sync takes priority over health check",
		},
		{
			name: "priority 5: health check when nothing else due",
			repo: &api.Repository{
				ObjectMeta: metav1.ObjectMeta{},
				Status: api.RepositoryStatus{
					LastFullSyncTime: &metav1.Time{Time: now.Add(-30 * time.Minute)},
					Conditions: []metav1.Condition{{
						Type:               api.RepositoryReady,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(now.Add(-6 * time.Minute)),
					}},
				},
			},
			expectedType: OperationHealthCheck,
			expectedNeed: true,
			description:  "Health check runs when nothing else is due",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				HealthCheckFrequency: 5 * time.Minute,
				FullSyncFrequency:    1 * time.Hour,
			}
			r.coldStartRepos.Store(tt.repo.Namespace+"/"+tt.repo.Name, true)
			decision := r.determineSyncDecision(ctx, tt.repo)
			assert.Equal(t, tt.expectedType, decision.Type, tt.description)
			assert.Equal(t, tt.expectedNeed, decision.SyncNecessary, tt.description)
		})
	}
}

func TestInitializeSyncLimiter(t *testing.T) {
	tests := []struct {
		name               string
		maxConcurrentSyncs int
		expectedCapacity   int
	}{
		{
			name:               "uses custom value",
			maxConcurrentSyncs: 50,
			expectedCapacity:   50,
		},
		{
			name:               "uses default when zero",
			maxConcurrentSyncs: 0,
			expectedCapacity:   100,
		},
		{
			name:               "uses default when negative",
			maxConcurrentSyncs: -1,
			expectedCapacity:   100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{
				MaxConcurrentSyncs: tt.maxConcurrentSyncs,
			}
			r.InitializeSyncLimiter()

			assert.Equal(t, tt.expectedCapacity, r.MaxConcurrentSyncs)
			assert.Equal(t, tt.expectedCapacity, cap(r.syncLimiter))
		})
	}
}
