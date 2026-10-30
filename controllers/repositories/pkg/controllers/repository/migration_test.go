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
	"strings"
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

			validDrafts := filterValidDraftsLogic(drafts)

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

// TestOrchestrateMigrationSuccess tests the pure orchestration logic with no DB discovery error
func TestOrchestrateMigrationSuccess(t *testing.T) {

	// Create mock discovered packages
	discovered := make([]repository.PackageRevision, 3)
	for i := 0; i < 3; i++ {
		mock := repositorymocks.NewMockPackageRevision(t)
		mock.On("Key").Return(repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				Package: fmt.Sprintf("discovered-%d", i),
			},
			WorkspaceName: "discovered-ws",
		})
		discovered[i] = mock
	}

	// Create mock DB-only packages with mix of valid and invalid workspaces
	var dbOnly []repository.PackageRevision
	validWSNames := []string{"ws1", "ws-2"}
	invalidWSNames := []string{"ws_invalid", "WS.bad"}

	for i, ws := range validWSNames {
		mock := repositorymocks.NewMockPackageRevision(t)
		mock.On("Key").Return(repository.PackageRevisionKey{
			WorkspaceName: ws,
			PkgKey: repository.PackageKey{
				Package: fmt.Sprintf("valid-%d", i),
			},
		})
		dbOnly = append(dbOnly, mock)
	}

	for i, ws := range invalidWSNames {
		mock := repositorymocks.NewMockPackageRevision(t)
		mock.On("Key").Return(repository.PackageRevisionKey{
			WorkspaceName: ws,
			PkgKey: repository.PackageKey{
				Package: fmt.Sprintf("invalid-%d", i),
			},
		})
		dbOnly = append(dbOnly, mock)
	}

	// Call orchestrateMigration with no error
	result := orchestrateMigration(discovered, dbOnly, nil)

	// Verify: discovered (3) + valid DB-only (2) = 5 total
	require.Equal(t, 5, len(result.AllPackageRevisions), "should combine discovered + valid DB-only")
	require.Equal(t, 3, result.DiscoveredCount, "should report discovered count")
	require.Equal(t, 2, result.ValidDBOnlyCount, "should report valid DB-only count")
	require.Equal(t, 2, result.SkippedCount, "should report skipped (invalid) count")
	require.NoError(t, result.DBDiscoveryErr, "no error expected")
}

// TestOrchestrateMigrationDBDiscoveryError tests orchestration when DB discovery fails.
// On error, migration aborts DB-only merge and returns filtered discovered packages.
// This prevents accidental deletion of existing v1alpha2 CRDs if DB is temporarily unavailable
// while still validating workspace names to avoid invalid CRD creation.
func TestOrchestrateMigrationDBDiscoveryError(t *testing.T) {

	// Create discovered packages with mix of valid and invalid workspaces
	var discovered []repository.PackageRevision
	validWSNames := []string{"ws-valid"}
	invalidWSNames := []string{"WS.invalid", strings.Repeat("x", 64)} // exceeds 63 chars

	for i, ws := range validWSNames {
		mock := repositorymocks.NewMockPackageRevision(t)
		mock.On("Key").Return(repository.PackageRevisionKey{
			WorkspaceName: ws,
			PkgKey: repository.PackageKey{
				Package: fmt.Sprintf("valid-%d", i),
			},
		})
		discovered = append(discovered, mock)
	}

	for i, ws := range invalidWSNames {
		mock := repositorymocks.NewMockPackageRevision(t)
		mock.On("Key").Return(repository.PackageRevisionKey{
			WorkspaceName: ws,
			PkgKey: repository.PackageKey{
				Package: fmt.Sprintf("invalid-%d", i),
			},
		})
		discovered = append(discovered, mock)
	}

	// Simulate DB discovery error
	dbErr := fmt.Errorf("database connection failed")

	// Call orchestrateMigration with error
	result := orchestrateMigration(discovered, []repository.PackageRevision{}, dbErr)

	// Verify: on error, returns only filtered discovered packages (valid ones)
	// - 1 valid discovered package
	// - 0 DB-only (error means DB-only not attempted)
	// - 2 invalid packages skipped
	require.Equal(t, 1, len(result.AllPackageRevisions), "should return only valid discovered packages on error")
	require.Equal(t, 1, result.DiscoveredCount, "discovered count reflects filtering")
	require.Equal(t, 0, result.ValidDBOnlyCount, "no DB-only packages on error path")
	require.Equal(t, 2, result.SkippedCount, "2 invalid discovered packages skipped")
	require.Equal(t, dbErr, result.DBDiscoveryErr, "error should be captured in result")
}

// TestOrchestrateMigrationAllDBOnlyInvalid tests when all DB-only packages are invalid
func TestOrchestrateMigrationAllDBOnlyInvalid(t *testing.T) {

	// Create discovered packages
	discovered := make([]repository.PackageRevision, 1)
	mock := repositorymocks.NewMockPackageRevision(t)
	mock.On("Key").Return(repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			Package: "discovered-pkg",
		},
		WorkspaceName: "discovered-ws",
	})
	discovered[0] = mock

	// Create DB-only packages with invalid workspace names
	var dbOnly []repository.PackageRevision
	for i, ws := range []string{"invalid_1", "INVALID.2", "bad-@-name"} {
		mock := repositorymocks.NewMockPackageRevision(t)
		mock.On("Key").Return(repository.PackageRevisionKey{
			WorkspaceName: ws,
			PkgKey: repository.PackageKey{
				Package: fmt.Sprintf("invalid-%d", i),
			},
		})
		dbOnly = append(dbOnly, mock)
	}

	result := orchestrateMigration(discovered, dbOnly, nil)

	// Verify: only discovered packages, all DB-only filtered out
	require.Equal(t, 1, len(result.AllPackageRevisions), "should only have discovered packages")
	require.Equal(t, 1, result.DiscoveredCount, "discovered count correct")
	require.Equal(t, 0, result.ValidDBOnlyCount, "no valid DB-only packages")
	require.Equal(t, 3, result.SkippedCount, "all 3 DB-only packages skipped")
	require.NoError(t, result.DBDiscoveryErr, "no error in orchestration itself")
}

// TestOrchestrateMigrationEmptyDiscovered tests with no discovered packages
func TestOrchestrateMigrationEmptyDiscovered(t *testing.T) {

	// Create DB-only packages
	var dbOnly []repository.PackageRevision
	for i, ws := range []string{"ws1", "ws-2"} {
		mock := repositorymocks.NewMockPackageRevision(t)
		mock.On("Key").Return(repository.PackageRevisionKey{
			WorkspaceName: ws,
			PkgKey: repository.PackageKey{
				Package: fmt.Sprintf("db-pkg-%d", i),
			},
		})
		dbOnly = append(dbOnly, mock)
	}

	result := orchestrateMigration([]repository.PackageRevision{}, dbOnly, nil)

	// Verify: only valid DB-only packages
	require.Equal(t, 2, len(result.AllPackageRevisions), "should have DB-only packages")
	require.Equal(t, 0, result.DiscoveredCount, "no discovered packages")
	require.Equal(t, 2, result.ValidDBOnlyCount, "valid DB-only count correct")
	require.Equal(t, 0, result.SkippedCount, "no skipped packages")
}

// TestOrchestrateMigrationDeduplication tests deduplication when same package in both sources
func TestOrchestrateMigrationDeduplication(t *testing.T) {

	// Create shared package key
	sharedKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{Name: "repo", Namespace: "ns"},
			Package: "shared-pkg",
			Path:    "path",
		},
		WorkspaceName: "ws1",
		Revision:      1,
	}

	// Git-discovered package (Draft pushed to git)
	gitDiscovered := repositorymocks.NewMockPackageRevision(t)
	gitDiscovered.On("Key").Return(sharedKey)

	// DB-only package (same package still in DB)
	dbOnly := repositorymocks.NewMockPackageRevision(t)
	dbOnly.On("Key").Return(sharedKey)

	result := orchestrateMigration([]repository.PackageRevision{gitDiscovered}, []repository.PackageRevision{dbOnly}, nil)

	// Verify: only 1 entry (duplicate deduplicated)
	require.Equal(t, 1, len(result.AllPackageRevisions), "should deduplicate same package from both sources")
	require.Equal(t, 1, result.DiscoveredCount, "discovered count correct")
	require.Equal(t, 1, result.ValidDBOnlyCount, "DB-only package was valid (before dedup)")
	require.Equal(t, 0, result.SkippedCount, "no skipped packages")
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

			validDrafts := filterValidDraftsLogic(drafts)

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

			validProposed := filterValidDraftsLogic(proposed)

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

			validPkgs := filterValidDraftsLogic(allPkgs)

			require.Equal(t, tt.expectedValidCount, len(validPkgs), "valid package count mismatch")
			require.Equal(t, tt.expectedFilteredOut, len(allPkgs)-len(validPkgs), "filtered package count mismatch")
		})
	}
}

// TestCombinePackages tests the combining logic with deduplication
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
		{"both non-overlapping", 2, 3, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock packages with distinct keys
			var discovered []repository.PackageRevision
			var validDBOnly []repository.PackageRevision

			// Create discovered packages with distinct keys
			for i := 0; i < tt.discoveredLen; i++ {
				mock := repositorymocks.NewMockPackageRevision(t)
				key := repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						RepoKey: repository.RepositoryKey{Name: "repo", Namespace: "ns"},
						Package: fmt.Sprintf("discovered-%d", i),
						Path:    "path",
					},
					WorkspaceName: "ws",
				}
				mock.On("Key").Return(key)
				discovered = append(discovered, mock)
			}

			// Create DB-only packages with distinct keys (no overlap)
			for i := 0; i < tt.validDBOnlyLen; i++ {
				mock := repositorymocks.NewMockPackageRevision(t)
				key := repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						RepoKey: repository.RepositoryKey{Name: "repo", Namespace: "ns"},
						Package: fmt.Sprintf("dbonly-%d", i),
						Path:    "path",
					},
					WorkspaceName: "ws",
				}
				mock.On("Key").Return(key)
				validDBOnly = append(validDBOnly, mock)
			}

			result := combinePackages(discovered, validDBOnly)
			require.Equal(t, tt.expectTotal, len(result), "combined count mismatch")
		})
	}
}

func TestCombinePackagesDetectsDuplicatesWhenPushDraftsToGitEnabled(t *testing.T) {
	// Create a package key representing a Draft pushed to git
	draftKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Name:      "test-repo",
				Namespace: "default",
			},
			Package: "my-pkg",
			Path:    "packages/my-pkg",
		},
		WorkspaceName: "dev",
		Revision:      1,
	}

	// Mock: Git-backed draft (from ListPackageRevisions with no filter when pushDraftsToGit=true)
	gitBackedDraft := repositorymocks.NewMockPackageRevision(t)
	gitBackedDraft.On("Key").Return(draftKey)

	// Mock: DB-only draft (from discoverDBOnlyPackages - queries DB for all Draft/Proposed)
	dbOnlyDraft := repositorymocks.NewMockPackageRevision(t)
	dbOnlyDraft.On("Key").Return(draftKey)

	// Simulate migration flow when pushDraftsToGit=true
	discovered := []repository.PackageRevision{gitBackedDraft} // Git-backed, includes drafts
	validDBOnly := []repository.PackageRevision{dbOnlyDraft}   // DB discovery

	combined := combinePackages(discovered, validDBOnly)

	// ISSUE PROOF: combined list contains duplicates
	require.Equal(t, 1, len(combined),
		"After fix: combinePackages should deduplicate packages with same Key")

	// Verify the remaining entry is from discovered list
	require.Equal(t, combined[0].Key(), draftKey,
		"Combined list should contain the package")

	t.Logf("FIX VERIFIED: combinePackages correctly deduplicates")
	t.Logf("  - Input discovered: 1 package")
	t.Logf("  - Input validDBOnly: 1 package (same Key as discovered)")
	t.Logf("  - Output combined: 1 package (duplicate filtered)")
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

// TestOrchestrateMigrationFiltersDiscoveredPackages tests that discovered packages are also filtered
// for valid workspace names (fix for r4262318055: validation bypass).
func TestOrchestrateMigrationFiltersDiscoveredPackages(t *testing.T) {
	// Create discovered packages with mix of valid and invalid workspaces
	var discovered []repository.PackageRevision
	validDiscoveredWSNames := []string{"ws-valid", "prod"}
	invalidDiscoveredWSNames := []string{"ws_invalid", "WS.bad", strings.Repeat("x", 64)} // exceeds 63 chars

	for i, ws := range validDiscoveredWSNames {
		mock := repositorymocks.NewMockPackageRevision(t)
		mock.On("Key").Return(repository.PackageRevisionKey{
			WorkspaceName: ws,
			PkgKey: repository.PackageKey{
				Package: fmt.Sprintf("valid-discovered-%d", i),
			},
		})
		discovered = append(discovered, mock)
	}

	for i, ws := range invalidDiscoveredWSNames {
		mock := repositorymocks.NewMockPackageRevision(t)
		mock.On("Key").Return(repository.PackageRevisionKey{
			WorkspaceName: ws,
			PkgKey: repository.PackageKey{
				Package: fmt.Sprintf("invalid-discovered-%d", i),
			},
		})
		discovered = append(discovered, mock)
	}

	// Create valid DB-only packages
	var dbOnly []repository.PackageRevision
	mock := repositorymocks.NewMockPackageRevision(t)
	mock.On("Key").Return(repository.PackageRevisionKey{
		WorkspaceName: "db-valid",
		PkgKey: repository.PackageKey{
			Package: "db-pkg",
		},
	})
	dbOnly = append(dbOnly, mock)

	// Call orchestrateMigration
	result := orchestrateMigration(discovered, dbOnly, nil)

	// Verify: 2 valid discovered + 1 valid DB-only = 3 total
	// 3 invalid discovered should be skipped
	require.Equal(t, 3, len(result.AllPackageRevisions), "should combine valid discovered + valid DB-only")
	require.Equal(t, 2, result.DiscoveredCount, "should report valid discovered count (2, not 5)")
	require.Equal(t, 1, result.ValidDBOnlyCount, "should report valid DB-only count")
	require.Equal(t, 3, result.SkippedCount, "should report 3 invalid discovered packages skipped")
	require.NoError(t, result.DBDiscoveryErr, "no error expected")
}
