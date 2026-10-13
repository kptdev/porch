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
	"regexp"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// dns1123Regex matches Kubernetes DNS-1123 subdomain format.
var dns1123Regex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// isValidDNS1123Subdomain checks if name is valid for Kubernetes DNS-1123 labels (max 63 chars).
func isValidDNS1123Subdomain(name string) bool {
	if len(name) > 63 {
		return false
	}
	if len(name) == 0 {
		return false
	}
	return dns1123Regex.MatchString(name)
}

// discoverDBOnlyPackages queries for Draft/Proposed packages from the v1alpha1 DB.
func discoverDBOnlyPackages(ctx context.Context, repoHandle repository.Repository) ([]repository.PackageRevision, error) {
	filter := repository.ListPackageRevisionFilter{
		Lifecycles: []porchapi.PackageRevisionLifecycle{
			porchapi.PackageRevisionLifecycleDraft,
			porchapi.PackageRevisionLifecycleProposed,
		},
	}
	return repoHandle.ListPackageRevisions(ctx, filter)
}

// combinePackages merges discovered and DB-only packages, deduplicating by PackageRevisionKey.
func combinePackages(discovered []repository.PackageRevision, validDBOnly []repository.PackageRevision) []repository.PackageRevision {
	// Build a set of discovered keys to avoid duplicates
	discoveredKeys := make(map[string]bool, len(discovered))
	for _, pr := range discovered {
		if pr != nil {
			discoveredKeys[pr.Key().String()] = true
		}
	}

	// Start with all discovered packages
	result := discovered

	// Add DB-only packages only if not already discovered
	for _, pr := range validDBOnly {
		if pr != nil && !discoveredKeys[pr.Key().String()] {
			result = append(result, pr)
		}
	}

	return result
}

// MigrationOrchestrationResult holds orchestration outputs for unit testing.
type MigrationOrchestrationResult struct {
	AllPackageRevisions []repository.PackageRevision
	DiscoveredCount     int
	ValidDBOnlyCount    int
	SkippedCount        int
	DBDiscoveryErr      error
}

// orchestrateMigration handles migration logic without side effects (pure function).
func orchestrateMigration(discoveredPkgRevs, dbOnlyPkgRevs []repository.PackageRevision, dbDiscoveryErr error) MigrationOrchestrationResult {
	// If DB discovery failed, start with empty DB-only list (non-fatal).
	if dbDiscoveryErr != nil {
		dbOnlyPkgRevs = []repository.PackageRevision{}
	}

	// Filter to only those with valid workspace names.
	// Invalid workspace names prevent CRD creation (DNS-1123 constraint on CRD names).
	validDBOnly := filterValidDraftsLogic(dbOnlyPkgRevs)
	skippedCount := len(dbOnlyPkgRevs) - len(validDBOnly)

	// Combine discovered packages (from git) with valid DB-only packages (from v1alpha1 DB).
	allPkgRevs := combinePackages(discoveredPkgRevs, validDBOnly)

	return MigrationOrchestrationResult{
		AllPackageRevisions: allPkgRevs,
		DiscoveredCount:     len(discoveredPkgRevs),
		ValidDBOnlyCount:    len(validDBOnly),
		SkippedCount:        skippedCount,
		DBDiscoveryErr:      dbDiscoveryErr,
	}
}

// filterValidDraftsLogic filters packages by DNS-1123 workspace name validity.
func filterValidDraftsLogic(drafts []repository.PackageRevision) []repository.PackageRevision {
	var valid []repository.PackageRevision
	for _, draft := range drafts {
		key := draft.Key()
		if isValidDNS1123Subdomain(key.WorkspaceName) {
			valid = append(valid, draft)
		}
	}
	return valid
}

// syncV1Alpha2Migration discovers and migrates v1alpha1 packages to v1alpha2 CRDs.
// All errors are non-fatal and logged; migration failures do not block sync.
func (r *RepositoryReconciler) syncV1Alpha2Migration(ctx context.Context, repo *configapi.Repository, repoHandle repository.Repository, discoveredPkgRevs []repository.PackageRevision) {
	log := log.FromContext(ctx)

	// Discover drafts and proposed packages from v1alpha1 DB (migration-only).
	dbOnlyPkgRevs, err := discoverDBOnlyPackages(ctx, repoHandle)

	// Orchestrate the migration (pure logic).
	result := orchestrateMigration(discoveredPkgRevs, dbOnlyPkgRevs, err)

	// Log results.
	if result.DBDiscoveryErr != nil {
		log.Error(result.DBDiscoveryErr, "Failed to discover draft/proposed packages during migration", "repo", repo.Name)
	}
	if result.SkippedCount > 0 {
		log.Info("Migration skipped draft/proposed packages with invalid workspace names",
			"repo", repo.Name, "skippedCount", result.SkippedCount)
	}

	// Sync packages to v1alpha2 CRDs (non-fatal).
	if err := r.syncPackageRevisions(ctx, repo, result.AllPackageRevisions); err != nil {
		log.Error(err, "Failed to sync v1alpha2 PackageRevisions during migration", "repo", repo.Name)
	}
}
