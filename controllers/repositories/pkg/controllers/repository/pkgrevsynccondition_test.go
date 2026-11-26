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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
)

func TestBuildPkgRevSyncCondition(t *testing.T) {
	repo := createTestRepo("test-repo", "test-ns")
	repo.Generation = 3

	tests := []struct {
		name     string
		build    func() metav1.Condition
		expected metav1.ConditionStatus
		reason   string
		message  string
	}{
		{
			name:     "SyncComplete",
			build:    func() metav1.Condition { return pkgRevSyncResultCondition(repo, packageRevisionSyncResult{Total: 18}) },
			expected: metav1.ConditionTrue,
			reason:   configapi.ReasonSyncComplete,
			message:  "18 of 18 PackageRevisions synced",
		},
		{
			name:     "SyncCompleteEmptyRepository",
			build:    func() metav1.Condition { return pkgRevSyncResultCondition(repo, packageRevisionSyncResult{}) },
			expected: metav1.ConditionTrue,
			reason:   configapi.ReasonSyncComplete,
			message:  "0 of 0 PackageRevisions synced",
		},
		{
			name: "PartialSyncFailure",
			build: func() metav1.Condition {
				return pkgRevSyncResultCondition(repo, packageRevisionSyncResult{
					Total:    5,
					Failures: []packageRevisionSyncFailure{{Name: "b", Err: errors.New("quota")}, {Name: "a", Err: errors.New("conflict")}},
				})
			},
			expected: metav1.ConditionFalse,
			reason:   configapi.ReasonPartialSyncFailure,
			message:  "2 of 5 PackageRevisions failed to sync: a: conflict; b: quota",
		},
		{
			name:     "SyncFailed",
			build:    func() metav1.Condition { return pkgRevSyncFailedCondition(repo, errors.New("list failed")) },
			expected: metav1.ConditionFalse,
			reason:   configapi.ReasonSyncFailed,
			message:  "PackageRevision sync did not complete: list failed",
		},
		{
			name:     "V1Alpha2SyncDisabled",
			build:    func() metav1.Condition { return pkgRevSyncDisabledCondition(repo) },
			expected: metav1.ConditionFalse,
			reason:   configapi.ReasonV1Alpha2SyncDisabled,
			message: "repository is annotated with porch.kpt.dev/v1alpha2-migration=true but the controller is running with " +
				"--repositories.create-v1alpha2-rpkg=false; PackageRevisions are not being synced",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := tt.build()

			assert.Equal(t, configapi.RepositoryPackageRevisionSyncComplete, cond.Type)
			assert.Equal(t, tt.expected, cond.Status)
			assert.Equal(t, tt.reason, cond.Reason)
			assert.Equal(t, tt.message, cond.Message)
			assert.Equal(t, repo.Generation, cond.ObservedGeneration)
			assert.False(t, cond.LastTransitionTime.IsZero())
		})
	}
}

func TestFormatPkgRevSyncFailures(t *testing.T) {
	failure := func(name, err string) packageRevisionSyncFailure {
		return packageRevisionSyncFailure{Name: name, Err: errors.New(err)}
	}
	longErr := strings.Repeat("x", 1000)

	tests := []struct {
		name     string
		result   packageRevisionSyncResult
		expected string
	}{
		{
			name: "failures are sorted by name",
			result: packageRevisionSyncResult{Total: 3, Failures: []packageRevisionSyncFailure{
				failure("c", "err-c"), failure("a", "err-a"),
			}},
			expected: "2 of 3 PackageRevisions failed to sync: a: err-a; c: err-c",
		},
		{
			name: "failures beyond the cap are summarised as a count",
			result: packageRevisionSyncResult{Total: 10, Failures: []packageRevisionSyncFailure{
				failure("e", "err-e"), failure("c", "err-c"), failure("a", "err-a"), failure("d", "err-d"), failure("b", "err-b"),
			}},
			expected: "5 of 10 PackageRevisions failed to sync: a: err-a; b: err-b; c: err-c; and 2 more",
		},
		{
			name:     "long errors are truncated",
			result:   packageRevisionSyncResult{Total: 1, Failures: []packageRevisionSyncFailure{failure("a", longErr)}},
			expected: "1 of 1 PackageRevisions failed to sync: a: " + longErr[:maxPkgRevSyncErrorLen],
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatPkgRevSyncFailures(tt.result))
		})
	}
}

func TestPkgRevSyncConditionUpdateResolve(t *testing.T) {
	repo := createTestRepo("test-repo", "test-ns")
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	ready := metav1.Condition{Type: configapi.RepositoryReady, Status: metav1.ConditionTrue, Reason: configapi.ReasonReady}
	existing := metav1.Condition{
		Type:               configapi.RepositoryPackageRevisionSyncComplete,
		Status:             metav1.ConditionFalse,
		Reason:             configapi.ReasonPartialSyncFailure,
		Message:            "1 of 2 failed",
		LastTransitionTime: metav1.NewTime(past),
	}

	tests := []struct {
		name          string
		existing      []metav1.Condition
		update        pkgRevSyncConditionUpdate
		expectNil     bool
		expected      metav1.ConditionStatus
		message       string
		keepsLastTime bool
	}{
		{
			name:          "zero value keeps the existing condition",
			existing:      []metav1.Condition{ready, existing},
			update:        pkgRevSyncConditionUpdate{},
			expected:      metav1.ConditionFalse,
			message:       existing.Message,
			keepsLastTime: true,
		},
		{
			name:      "zero value without an existing condition writes nothing",
			existing:  []metav1.Condition{ready},
			update:    pkgRevSyncConditionUpdate{},
			expectNil: true,
		},
		{
			name:      "remove drops the existing condition",
			existing:  []metav1.Condition{ready, existing},
			update:    removePkgRevSyncCondition(),
			expectNil: true,
		},
		{
			name:     "set without an existing condition adds it",
			existing: []metav1.Condition{ready},
			update:   setPkgRevSyncCondition(pkgRevSyncResultCondition(repo, packageRevisionSyncResult{Total: 2})),
			expected: metav1.ConditionTrue,
			message:  "2 of 2 PackageRevisions synced",
		},
		{
			name:     "set with the same status updates the message but keeps LastTransitionTime",
			existing: []metav1.Condition{ready, existing},
			update: setPkgRevSyncCondition(newPkgRevSyncCondition(repo, metav1.ConditionFalse,
				configapi.ReasonPartialSyncFailure, "2 of 2 failed")),
			expected:      metav1.ConditionFalse,
			message:       "2 of 2 failed",
			keepsLastTime: true,
		},
		{
			name:     "set with a different status moves LastTransitionTime",
			existing: []metav1.Condition{ready, existing},
			update:   setPkgRevSyncCondition(pkgRevSyncResultCondition(repo, packageRevisionSyncResult{Total: 2})),
			expected: metav1.ConditionTrue,
			message:  "2 of 2 PackageRevisions synced",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := tt.update.resolve(tt.existing)

			if tt.expectNil {
				assert.Nil(t, cond)
				return
			}
			require.NotNil(t, cond)
			assert.Equal(t, tt.expected, cond.Status)
			assert.Equal(t, tt.message, cond.Message)
			assert.Equal(t, tt.keepsLastTime, cond.LastTransitionTime.Time.Equal(past))
		})
	}
}
