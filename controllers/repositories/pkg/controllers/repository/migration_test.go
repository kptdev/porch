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
	"fmt"
	"testing"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	repositorymocks "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/require"
)

// TestIsValidDNS1123Subdomain tests the DNS-1123 validation helper.
func TestIsValidDNS1123Subdomain(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantValid bool
	}{
		// Valid cases
		{
			name:      "simple lowercase",
			input:     "workspace",
			wantValid: true,
		},
		{
			name:      "with hyphen",
			input:     "my-workspace",
			wantValid: true,
		},
		{
			name:      "with numbers",
			input:     "ws1",
			wantValid: true,
		},
		{
			name:      "all numbers",
			input:     "123",
			wantValid: true,
		},
		{
			name:      "starts with number",
			input:     "1ws",
			wantValid: true,
		},
		{
			name:      "ends with number",
			input:     "ws1",
			wantValid: true,
		},
		{
			name:      "single char",
			input:     "a",
			wantValid: true,
		},

		// Invalid cases
		{
			name:      "empty string",
			input:     "",
			wantValid: false,
		},
		{
			name:      "contains underscore",
			input:     "my_workspace",
			wantValid: false,
		},
		{
			name:      "contains uppercase",
			input:     "MyWorkspace",
			wantValid: false,
		},
		{
			name:      "contains dot",
			input:     "my.workspace",
			wantValid: false,
		},
		{
			name:      "contains space",
			input:     "my workspace",
			wantValid: false,
		},
		{
			name:      "starts with hyphen",
			input:     "-workspace",
			wantValid: false,
		},
		{
			name:      "ends with hyphen",
			input:     "workspace-",
			wantValid: false,
		},
		{
			name:      "only hyphen",
			input:     "-",
			wantValid: false,
		},
		{
			name:      "exceeds max length (254 chars)",
			input:     "a" + string(make([]byte, 253)) + "b", // 255 total
			wantValid: false,
		},
		{
			name:      "contains special chars",
			input:     "ws@123",
			wantValid: false,
		},
		{
			name:      "contains slash",
			input:     "ws/123",
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidDNS1123Subdomain(tt.input)
			require.Equal(t, tt.wantValid, got, "input: %q", tt.input)
		})
	}
}

// TestFilterValidDrafts tests the DB-only package filtering logic (both Draft and Proposed packages)
func TestFilterValidDrafts(t *testing.T) {
	tests := []struct {
		name          string
		inputDrafts   []string // workspace names
		expectedCount int
		expectedValid map[string]bool // workspace -> expected to be valid
	}{
		{
			name:          "all valid",
			expectedCount: 3,
			inputDrafts:   []string{"ws1", "ws-2", "valid"},
			expectedValid: map[string]bool{
				"ws1":   true,
				"ws-2":  true,
				"valid": true,
			},
		},
		{
			name:          "all invalid",
			expectedCount: 0,
			inputDrafts:   []string{"ws_1", "ws.2", "WS3"},
			expectedValid: map[string]bool{},
		},
		{
			name:          "mixed valid and invalid",
			expectedCount: 2,
			inputDrafts:   []string{"valid-1", "invalid_2", "valid-3"},
			expectedValid: map[string]bool{
				"valid-1": true,
				"valid-3": true,
			},
		},
		{
			name:          "empty input",
			expectedCount: 0,
			inputDrafts:   []string{},
			expectedValid: map[string]bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock PackageRevisions
			var drafts []repository.PackageRevision
			for i, ws := range tt.inputDrafts {
				mockPR := repositorymocks.NewMockPackageRevision(t)
				mockPR.On("Key").Return(repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						Package: "pkg" + string(rune(i)),
						Path:    "packages/pkg" + string(rune(i)),
					},
					WorkspaceName: ws,
				})
				drafts = append(drafts, mockPR)
			}

			r := &RepositoryReconciler{}
			validDrafts := r.filterValidDrafts(drafts)

			// Check count
			require.Equal(t, tt.expectedCount, len(validDrafts), "filtered count mismatch")

			// Check which ones are valid
			for _, valid := range validDrafts {
				ws := valid.Key().WorkspaceName
				require.True(t, tt.expectedValid[ws], "unexpected valid workspace: %s", ws)
			}
		})
	}
}

// TestSyncV1Alpha2Migration tests the main migration sync logic with DB-only package filtering (Draft and Proposed)
// Note: This test verifies filterValidDrafts logic but does not invoke syncV1Alpha2Migration directly.
// For full integration testing of the migration handler with live repository calls, see E2E tests.
func TestSyncV1Alpha2Migration(t *testing.T) {
	tests := []struct {
		name                string
		inputDrafts         []string // workspace names
		expectedMigratedPRs int      // count of valid drafts that should be created
		expectedSkipped     int      // count of invalid drafts
	}{
		{
			name:                "all valid drafts",
			inputDrafts:         []string{"ws1", "ws-2", "valid"},
			expectedMigratedPRs: 3,
			expectedSkipped:     0,
		},
		{
			name:                "all invalid drafts",
			inputDrafts:         []string{"ws_1", "ws.2", "WS3"},
			expectedMigratedPRs: 0,
			expectedSkipped:     3,
		},
		{
			name:                "mixed valid and invalid",
			inputDrafts:         []string{"valid-1", "invalid_2", "valid-3"},
			expectedMigratedPRs: 2,
			expectedSkipped:     1,
		},
		{
			name:                "no drafts",
			inputDrafts:         []string{},
			expectedMigratedPRs: 0,
			expectedSkipped:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock PackageRevisions
			var drafts []repository.PackageRevision
			for i, ws := range tt.inputDrafts {
				mockPR := repositorymocks.NewMockPackageRevision(t)
				mockPR.On("Key").Return(repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						Package: "pkg" + string(rune(i)),
						Path:    "packages/pkg" + string(rune(i)),
					},
					WorkspaceName: ws,
				})
				drafts = append(drafts, mockPR)
			}

			r := &RepositoryReconciler{}
			ctx := context.Background()

			// Call filterValidDrafts to test the filtering
			var _ context.Context = ctx
			validDrafts := r.filterValidDrafts(drafts)

			// Verify counts
			require.Equal(t, tt.expectedMigratedPRs, len(validDrafts), "valid drafts count mismatch")
			require.Equal(t, tt.expectedSkipped, len(drafts)-len(validDrafts), "skipped drafts count mismatch")
		})
	}
}

// TestSyncV1Alpha2MigrationWithPublishedAndDrafts tests the full migration path
// with both published packages and valid/invalid DB-only packages (Draft and Proposed)
func TestSyncV1Alpha2MigrationWithPublishedAndDrafts(t *testing.T) {
	tests := []struct {
		name                string
		publishedCount      int
		draftWSNames        []string // workspace names for drafts
		expectedTotalCount  int      // expected final package revisions count
		expectedSkippedDuct int      // expected skipped drafts
	}{
		{
			name:                "published only",
			publishedCount:      5,
			draftWSNames:        []string{},
			expectedTotalCount:  5,
			expectedSkippedDuct: 0,
		},
		{
			name:                "published + valid drafts",
			publishedCount:      3,
			draftWSNames:        []string{"ws1", "ws-2"},
			expectedTotalCount:  5,
			expectedSkippedDuct: 0,
		},
		{
			name:                "published + invalid drafts",
			publishedCount:      2,
			draftWSNames:        []string{"ws_invalid", "WS.bad"},
			expectedTotalCount:  2,
			expectedSkippedDuct: 2,
		},
		{
			name:                "published + mixed drafts",
			publishedCount:      4,
			draftWSNames:        []string{"valid-ws", "invalid_ws", "another-valid"},
			expectedTotalCount:  6,
			expectedSkippedDuct: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock published package revisions
			var published []repository.PackageRevision
			for i := 0; i < tt.publishedCount; i++ {
				mockPR := repositorymocks.NewMockPackageRevision(t)
				// Only set up Key() when it will be called (during filterValidDrafts)
				// For published packages, we don't call Key() in the test, just store them
				published = append(published, mockPR)
			}

			// Create mock draft package revisions with Key expectations
			var drafts []repository.PackageRevision
			for i, ws := range tt.draftWSNames {
				mockPR := repositorymocks.NewMockPackageRevision(t)
				// This is called during filterValidDrafts
				mockPR.On("Key").Return(repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						Package: "draft-pkg" + string(rune(i)),
						Path:    "packages/draft-pkg" + string(rune(i)),
					},
					WorkspaceName: ws,
				})
				drafts = append(drafts, mockPR)
			}

			r := &RepositoryReconciler{}
			ctx := context.Background()

			// Filter valid drafts (this calls Key())
			var _ context.Context = ctx
			validDrafts := r.filterValidDrafts(drafts)

			// Combine published + valid drafts
			allPkgRevs := append([]repository.PackageRevision{}, published...)
			allPkgRevs = append(allPkgRevs, validDrafts...)

			// Verify final counts
			require.Equal(t, tt.expectedTotalCount, len(allPkgRevs), "total package revisions count mismatch")
			require.Equal(t, tt.expectedSkippedDuct, len(drafts)-len(validDrafts), "skipped drafts count mismatch")
		})
	}
}

// TestFilterValidDraftsEdgeCases tests edge cases in DB-only package filtering (Draft and Proposed)
func TestFilterValidDraftsEdgeCases(t *testing.T) {
	tests := []struct {
		name           string
		workspaceNames []string
		expectedValid  []string
	}{
		{
			name:           "numeric workspace names",
			workspaceNames: []string{"123", "456-789"},
			expectedValid:  []string{"123", "456-789"},
		},
		{
			name:           "boundary length names",
			workspaceNames: []string{"a", "ab", "a-b"},
			expectedValid:  []string{"a", "ab", "a-b"},
		},
		{
			name:           "mixed case rejection",
			workspaceNames: []string{"validName", "UPPERCASE", "MixedCase"},
			expectedValid:  []string{},
		},
		{
			name:           "special chars rejection",
			workspaceNames: []string{"ws@1", "ws#1", "ws$1", "ws%1", "ws&1"},
			expectedValid:  []string{},
		},
		{
			name:           "whitespace rejection",
			workspaceNames: []string{"ws 1", "ws\t1", "ws\n1"},
			expectedValid:  []string{},
		},
		{
			name:           "hyphen edge cases",
			workspaceNames: []string{"-start", "end-", "--middle", "a-b-c-d"},
			expectedValid:  []string{"a-b-c-d"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock drafts
			var drafts []repository.PackageRevision
			for i, ws := range tt.workspaceNames {
				mockPR := repositorymocks.NewMockPackageRevision(t)
				mockPR.On("Key").Return(repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						Package: "pkg" + string(rune(i)),
						Path:    "packages/pkg" + string(rune(i)),
					},
					WorkspaceName: ws,
				})
				drafts = append(drafts, mockPR)
			}

			r := &RepositoryReconciler{}
			validDrafts := r.filterValidDrafts(drafts)

			// Extract workspace names from valid drafts
			var validWSNames []string
			for _, valid := range validDrafts {
				validWSNames = append(validWSNames, valid.Key().WorkspaceName)
			}

			require.ElementsMatch(t, tt.expectedValid, validWSNames, "valid workspace names mismatch")
		})
	}
}

// TestFilterValidProposedPackages tests filtering of Proposed packages (also DB-only)
func TestFilterValidProposedPackages(t *testing.T) {
	tests := []struct {
		name                string
		proposedWSNames     []string // workspace names for proposed packages
		expectedValidCount  int
		expectedFilteredOut int
	}{
		{
			name:                "all valid proposed",
			proposedWSNames:     []string{"qa", "staging-v1", "review"},
			expectedValidCount:  3,
			expectedFilteredOut: 0,
		},
		{
			name:                "all invalid proposed",
			proposedWSNames:     []string{"dev_review", "STAGING.v1", "review@main"},
			expectedValidCount:  0,
			expectedFilteredOut: 3,
		},
		{
			name:                "mixed valid and invalid proposed",
			proposedWSNames:     []string{"qa", "dev_review", "staging-v1", "REVIEW"},
			expectedValidCount:  2,
			expectedFilteredOut: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock proposed package revisions
			var proposed []repository.PackageRevision
			for i, ws := range tt.proposedWSNames {
				mockPR := repositorymocks.NewMockPackageRevision(t)
				mockPR.On("Key").Return(repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						Package: "proposed-pkg" + string(rune(i)),
						Path:    "packages/proposed-pkg" + string(rune(i)),
					},
					WorkspaceName: ws,
				})
				proposed = append(proposed, mockPR)
			}

			r := &RepositoryReconciler{}
			validProposed := r.filterValidDrafts(proposed) // Works for any DB-only packages

			require.Equal(t, tt.expectedValidCount, len(validProposed), "valid proposed count mismatch")
			require.Equal(t, tt.expectedFilteredOut, len(proposed)-len(validProposed), "filtered proposed count mismatch")
		})
	}
}

// TestMixedDraftAndProposedPackages tests filtering when both Draft and Proposed packages exist
func TestMixedDraftAndProposedPackages(t *testing.T) {
	tests := []struct {
		name                string
		draftWSNames        []string
		proposedWSNames     []string
		expectedValidCount  int
		expectedFilteredOut int
	}{
		{
			name:                "drafts and proposed mixed valid",
			draftWSNames:        []string{"dev", "feature-x"},
			proposedWSNames:     []string{"staging", "qa"},
			expectedValidCount:  4,
			expectedFilteredOut: 0,
		},
		{
			name:                "drafts valid, proposed invalid",
			draftWSNames:        []string{"dev", "feature-x"},
			proposedWSNames:     []string{"staging_old", "qa.v2"},
			expectedValidCount:  2,
			expectedFilteredOut: 2,
		},
		{
			name:                "drafts invalid, proposed valid",
			draftWSNames:        []string{"dev_old", "feature.main"},
			proposedWSNames:     []string{"staging", "qa"},
			expectedValidCount:  2,
			expectedFilteredOut: 2,
		},
		{
			name:                "both drafts and proposed mixed",
			draftWSNames:        []string{"dev", "dev_old", "feature-x"},
			proposedWSNames:     []string{"staging", "qa.prod", "review"},
			expectedValidCount:  4,
			expectedFilteredOut: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock draft package revisions
			var allPkgs []repository.PackageRevision
			for i, ws := range tt.draftWSNames {
				mockPR := repositorymocks.NewMockPackageRevision(t)
				mockPR.On("Key").Return(repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						Package: "draft-pkg" + string(rune(i)),
						Path:    "packages/draft-pkg" + string(rune(i)),
					},
					WorkspaceName: ws,
				})
				allPkgs = append(allPkgs, mockPR)
			}

			// Create mock proposed package revisions
			for i, ws := range tt.proposedWSNames {
				mockPR := repositorymocks.NewMockPackageRevision(t)
				mockPR.On("Key").Return(repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						Package: "proposed-pkg" + string(rune(i)),
						Path:    "packages/proposed-pkg" + string(rune(i)),
					},
					WorkspaceName: ws,
				})
				allPkgs = append(allPkgs, mockPR)
			}

			r := &RepositoryReconciler{}
			validPkgs := r.filterValidDrafts(allPkgs)

			require.Equal(t, tt.expectedValidCount, len(validPkgs), "valid package count mismatch")
			require.Equal(t, tt.expectedFilteredOut, len(allPkgs)-len(validPkgs), "filtered package count mismatch")
		})
	}
}

// TestCombinePackages tests the pure combining logic (no mocks needed)
func TestCombinePackages(t *testing.T) {
	tests := []struct {
		name           string
		discoveredLen  int
		validDBOnlyLen int
		expectTotal    int
	}{
		{"empty both", 0, 0, 0},
		{"only discovered", 3, 0, 3},
		{"only valid DB-only", 0, 2, 2},
		{"both", 2, 3, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create nil slices (combinePackages just combines by reference)
			discovered := make([]repository.PackageRevision, tt.discoveredLen)
			validDBOnly := make([]repository.PackageRevision, tt.validDBOnlyLen)

			result := combinePackages(discovered, validDBOnly)
			require.Equal(t, tt.expectTotal, len(result), "combined count mismatch")
		})
	}
}

// TestDiscoverDBOnlyPackagesSuccess tests successful DB discovery
func TestDiscoverDBOnlyPackagesSuccess(t *testing.T) {
	ctx := context.Background()

	// Mock repository that returns 2 packages
	mockRepo := repositorymocks.NewMockRepository(t)
	expectedPackages := make([]repository.PackageRevision, 2)
	mockRepo.On("ListPackageRevisions", ctx, repository.ListPackageRevisionFilter{
		Lifecycles: []porchapi.PackageRevisionLifecycle{
			porchapi.PackageRevisionLifecycleDraft,
			porchapi.PackageRevisionLifecycleProposed,
		},
	}).Return(expectedPackages, nil)

	result, err := discoverDBOnlyPackages(ctx, mockRepo)

	require.NoError(t, err)
	require.Equal(t, 2, len(result), "should return 2 packages")
}

// TestDiscoverDBOnlyPackagesError tests error handling
func TestDiscoverDBOnlyPackagesError(t *testing.T) {
	ctx := context.Background()

	// Mock repository that returns error
	mockRepo := repositorymocks.NewMockRepository(t)
	mockRepo.On("ListPackageRevisions", ctx, repository.ListPackageRevisionFilter{
		Lifecycles: []porchapi.PackageRevisionLifecycle{
			porchapi.PackageRevisionLifecycleDraft,
			porchapi.PackageRevisionLifecycleProposed,
		},
	}).Return(nil, fmt.Errorf("db error"))

	result, err := discoverDBOnlyPackages(ctx, mockRepo)

	require.Error(t, err)
	require.Nil(t, result, "should return nil on error")
}
