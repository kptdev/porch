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
	discoveredKeys := make(map[string]bool, len(discovered))
	for _, pr := range discovered {
		if pr != nil {
			discoveredKeys[pr.Key().String()] = true
		}
	}

	result := discovered
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
	validDiscovered := filterValidDraftsLogic(discoveredPkgRevs)
	discoveredSkipped := len(discoveredPkgRevs) - len(validDiscovered)

	result := MigrationOrchestrationResult{
		DiscoveredCount: len(validDiscovered),
		SkippedCount:    discoveredSkipped,
		DBDiscoveryErr:  dbDiscoveryErr,
	}

	if dbDiscoveryErr != nil {
		// On DB discovery error, return filtered discovered packages without DB-only merge.
		// This prevents syncPackageRevisions from deleting existing v1alpha2 CRDs via deleteStalePackageRevisions.
		result.AllPackageRevisions = validDiscovered
		return result
	}

	validDBOnly := filterValidDraftsLogic(dbOnlyPkgRevs)
	dbSkipped := len(dbOnlyPkgRevs) - len(validDBOnly)

	allPkgRevs := combinePackages(validDiscovered, validDBOnly)

	result.AllPackageRevisions = allPkgRevs
	result.ValidDBOnlyCount = len(validDBOnly)
	result.SkippedCount = discoveredSkipped + dbSkipped

	return result
}

// filterValidDraftsLogic filters packages by DNS-1123 workspace name validity.
func filterValidDraftsLogic(drafts []repository.PackageRevision) []repository.PackageRevision {
	var valid []repository.PackageRevision
	for _, draft := range drafts {
		if isValidDNS1123Subdomain(draft.Key().WorkspaceName) {
			valid = append(valid, draft)
		}
	}
	return valid
}

// syncV1Alpha2Migration discovers and migrates v1alpha1 packages to v1alpha2 CRDs.
// On DB discovery errors, migration is aborted to prevent deleteStalePackageRevisions from deleting existing CRDs.
// Packages with invalid workspace names (DNS-1123 constraint) are filtered out and skipped.
//
// When invalid workspace names are encountered, the user must recreate the package revision
// with a valid workspace name (max 63 characters, lowercase, alphanumeric and hyphens only).
func (r *RepositoryReconciler) syncV1Alpha2Migration(ctx context.Context, repo *configapi.Repository, repoHandle repository.Repository, discoveredPkgRevs []repository.PackageRevision) {
	log := log.FromContext(ctx)

	dbOnlyPkgRevs, err := discoverDBOnlyPackages(ctx, repoHandle)
	result := orchestrateMigration(discoveredPkgRevs, dbOnlyPkgRevs, err)

	if result.DBDiscoveryErr != nil {
		log.Error(result.DBDiscoveryErr, "Aborting v1alpha2 migration due to DB discovery error", "repo", repo.Name)
		return
	}
	if result.SkippedCount > 0 {
		log.Info("Migration skipped packages with invalid workspace names (DNS-1123 constraint)",
			"repo", repo.Name, "skippedCount", result.SkippedCount)
	}

	if err := r.syncPackageRevisions(ctx, repo, result.AllPackageRevisions); err != nil {
		log.Error(err, "Failed to sync v1alpha2 PackageRevisions during migration", "repo", repo.Name)
	}
}
