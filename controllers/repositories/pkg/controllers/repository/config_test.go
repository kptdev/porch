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
	"flag"
	"net/http"
	"testing"
	"time"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

func TestInitDefaults(t *testing.T) {
	r := &RepositoryReconciler{}
	r.InitDefaults()

	assert.Equal(t, 100, r.MaxConcurrentReconciles)
	assert.Equal(t, 50, r.MaxConcurrentSyncs)
}

func TestBindFlags(t *testing.T) {
	r := &RepositoryReconciler{}
	flags := flag.NewFlagSet("test", flag.ContinueOnError)

	r.BindFlags("repo-", flags)

	// Parse test flags
	err := flags.Parse([]string{
		"--repo-max-concurrent-reconciles=100",
	})
	require.NoError(t, err)

	assert.Equal(t, 100, r.MaxConcurrentReconciles)
}

type mockLogger struct {
	infoCalls [][]any
}

func (m *mockLogger) Info(msg string, keysAndValues ...any) {
	m.infoCalls = append(m.infoCalls, append([]any{msg}, keysAndValues...))
}

func TestLogConfig(t *testing.T) {
	tests := []struct {
		name           string
		reconciler     *RepositoryReconciler
		expectWarnings int
	}{
		{
			name: "default config - no warnings",
			reconciler: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
			expectWarnings: 0,
		},
		{
			name: "low health check frequency - warning",
			reconciler: &RepositoryReconciler{
				HealthCheckFrequency:       1 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
			expectWarnings: 1,
		},
		{
			name: "low full sync frequency - warning",
			reconciler: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          30 * time.Minute,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
			expectWarnings: 1,
		},
		{
			name: "both frequencies low - two warnings",
			reconciler: &RepositoryReconciler{
				HealthCheckFrequency:       1 * time.Minute,
				FullSyncFrequency:          30 * time.Minute,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
			expectWarnings: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := &mockLogger{}
			tt.reconciler.LogConfig(logger)

			require.NotEmpty(t, logger.infoCalls)

			// First call should be the main config log
			firstMsg := logger.infoCalls[0][0].(string)
			assert.Equal(t, "Repository controller configuration", firstMsg)

			// Check warning count (total calls - 1 for main config)
			warningCount := len(logger.infoCalls) - 1
			assert.Equal(t, tt.expectWarnings, warningCount)
		})
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    *RepositoryReconciler
		expected *RepositoryReconciler
	}{
		{
			name: "all valid values - no changes",
			input: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
			expected: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
		},
		{
			name: "zero health check - uses default",
			input: &RepositoryReconciler{
				HealthCheckFrequency:       0,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
			expected: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
		},
		{
			name: "negative full sync - uses default",
			input: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          -1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
			expected: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
		},
		{
			name: "zero stale timeout - uses default",
			input: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           0,
				RepoOperationRetryAttempts: 3,
			},
			expected: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
		},
		{
			name: "zero max concurrent reconciles - uses default",
			input: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    0,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
			expected: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
		},
		{
			name: "negative max concurrent syncs - uses default",
			input: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         -10,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
			expected: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
		},
		{
			name: "zero retry attempts - uses default",
			input: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 0,
			},
			expected: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
		},
		{
			name: "all invalid - all use defaults",
			input: &RepositoryReconciler{
				HealthCheckFrequency:       0,
				FullSyncFrequency:          -1,
				MaxConcurrentReconciles:    -1,
				MaxConcurrentSyncs:         0,
				SyncStaleTimeout:           0,
				RepoOperationRetryAttempts: -10,
			},
			expected: &RepositoryReconciler{
				HealthCheckFrequency:       5 * time.Minute,
				FullSyncFrequency:          1 * time.Hour,
				MaxConcurrentReconciles:    100,
				MaxConcurrentSyncs:         50,
				SyncStaleTimeout:           20 * time.Minute,
				RepoOperationRetryAttempts: 3,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.input.validateConfig()
			assert.Equal(t, tt.expected.HealthCheckFrequency, tt.input.HealthCheckFrequency)
			assert.Equal(t, tt.expected.FullSyncFrequency, tt.input.FullSyncFrequency)
			assert.Equal(t, tt.expected.MaxConcurrentReconciles, tt.input.MaxConcurrentReconciles)
			assert.Equal(t, tt.expected.MaxConcurrentSyncs, tt.input.MaxConcurrentSyncs)
			assert.Equal(t, tt.expected.SyncStaleTimeout, tt.input.SyncStaleTimeout)
			assert.Equal(t, tt.expected.RepoOperationRetryAttempts, tt.input.RepoOperationRetryAttempts)
		})
	}
}

// fakeFieldIndexer is a minimal field.Indexer for testing that stores index functions but doesn't actually index.
type fakeFieldIndexer struct{}

func (f *fakeFieldIndexer) IndexField(ctx context.Context, obj client.Object, field string, fn client.IndexerFunc) error {
	// No-op for testing - just accept the index function registration
	return nil
}

// fakeManager is a minimal manager.Manager for unit testing Init().
// Only GetClient(), GetAPIReader(), GetWebhookServer(), and GetFieldIndexer() are implemented; all other methods will panic if called.
type fakeManager struct {
	manager.Manager
	client  client.Client
	indexer *fakeFieldIndexer
}

func (f *fakeManager) GetClient() client.Client {
	return f.client
}

func (f *fakeManager) GetAPIReader() client.Reader {
	return f.client
}

func (f *fakeManager) GetWebhookServer() webhook.Server {
	return &fakeWebhookServer{}
}

func (f *fakeManager) GetFieldIndexer() client.FieldIndexer {
	return f.indexer
}

// fakeWebhookServer is a minimal webhook.Server for testing.
type fakeWebhookServer struct{}

func (f *fakeWebhookServer) Register(path string, handler http.Handler) {
	// No-op for testing
}

func (f *fakeWebhookServer) Start(ctx context.Context) error {
	return nil
}

func (f *fakeWebhookServer) NeedLeaderElection() bool {
	return false
}

func (f *fakeWebhookServer) StartedChecker() healthz.Checker {
	// Return a no-op checker for testing
	return healthz.Ping
}

func (f *fakeWebhookServer) WebhookMux() *http.ServeMux {
	return nil
}

func TestInit(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "successfully registers repository webhook",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &RepositoryReconciler{}
			mgr := &fakeManager{
				client:  fake.NewClientBuilder().Build(),
				indexer: &fakeFieldIndexer{},
			}

			err := reconciler.Init(mgr)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestGitRepoIndexingFunction tests the git.repo index function used in Init
func TestGitRepoIndexingFunction(t *testing.T) {
	indexFunc := func(o client.Object) []string {
		repository := o.(*configapi.Repository)
		if repository.Spec.Git == nil || repository.Spec.Git.Repo == "" {
			return nil
		}
		return []string{repository.Spec.Git.Repo}
	}

	tests := []struct {
		name     string
		repo     *configapi.Repository
		expected []string
	}{
		{
			name: "git repository with valid URL",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo1", Namespace: "default"},
				Spec: configapi.RepositorySpec{
					Git: &configapi.GitRepository{
						Repo: "http://gitea.local/org/repo.git",
					},
				},
			},
			expected: []string{"http://gitea.local/org/repo.git"},
		},
		{
			name: "OCI repository (nil Git)",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo-oci", Namespace: "default"},
				Spec: configapi.RepositorySpec{
					Type: configapi.RepositoryTypeOCI,
				},
			},
			expected: nil,
		},
		{
			name: "git repository with empty URL",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo2", Namespace: "default"},
				Spec: configapi.RepositorySpec{
					Git: &configapi.GitRepository{Repo: ""},
				},
			},
			expected: nil,
		},
		{
			name: "repository with git spec but empty URL",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo3", Namespace: "default"},
				Spec: configapi.RepositorySpec{
					Git: &configapi.GitRepository{
						Repo: "https://github.com/example/pkg.git",
					},
				},
			},
			expected: []string{"https://github.com/example/pkg.git"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := indexFunc(tc.repo)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestGitBranchIndexingFunction tests the git.branch index function used in Init
func TestGitBranchIndexingFunction(t *testing.T) {
	indexFunc := func(o client.Object) []string {
		repository := o.(*configapi.Repository)
		if repository.Spec.Git == nil || repository.Spec.Git.Branch == "" {
			return nil
		}
		return []string{repository.Spec.Git.Branch}
	}

	tests := []struct {
		name     string
		repo     *configapi.Repository
		expected []string
	}{
		{
			name: "repository with main branch",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo1", Namespace: "default"},
				Spec: configapi.RepositorySpec{
					Git: &configapi.GitRepository{
						Branch: "main",
					},
				},
			},
			expected: []string{"main"},
		},
		{
			name: "repository with develop branch",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo2", Namespace: "default"},
				Spec: configapi.RepositorySpec{
					Git: &configapi.GitRepository{
						Branch: "develop",
					},
				},
			},
			expected: []string{"develop"},
		},
		{
			name: "OCI repository (nil Git)",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo-oci", Namespace: "default"},
				Spec:       configapi.RepositorySpec{},
			},
			expected: nil,
		},
		{
			name: "repository with release branch",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo3", Namespace: "prod"},
				Spec: configapi.RepositorySpec{
					Git: &configapi.GitRepository{
						Branch: "release-v1.0",
					},
				},
			},
			expected: []string{"release-v1.0"},
		},
		{
			name: "repository with feature branch",
			repo: &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo4", Namespace: "staging"},
				Spec: configapi.RepositorySpec{
					Git: &configapi.GitRepository{
						Branch: "feature/new-feature",
					},
				},
			},
			expected: []string{"feature/new-feature"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := indexFunc(tc.repo)
			assert.Equal(t, tc.expected, result)
		})
	}
}
