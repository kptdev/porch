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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// fakeManager is a minimal manager.Manager for unit testing Init().
// Only GetClient(), GetAPIReader(), and GetWebhookServer() are implemented; all other methods will panic if called.
type fakeManager struct {
	manager.Manager
	client client.Client
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
			mgr := &fakeManager{client: fake.NewClientBuilder().Build()}

			err := reconciler.Init(mgr)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
