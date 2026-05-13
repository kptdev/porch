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
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	infoCalls [][]interface{}
}

func (m *mockLogger) Info(msg string, keysAndValues ...interface{}) {
	m.infoCalls = append(m.infoCalls, append([]interface{}{msg}, keysAndValues...))
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
