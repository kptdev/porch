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

// dns1123Regex matches Kubernetes DNS-1123 subdomain format:
// alphanumeric (lowercase) and hyphen, starting and ending with alphanumeric,
// max 253 characters.
var dns1123Regex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// isValidDNS1123Subdomain returns true if name is a valid Kubernetes DNS-1123 subdomain.
// Rules: lowercase alphanumeric + hyphen, start/end with alphanumeric, max 63 chars.
// Migration-specific: ensures workspace names are valid for v1alpha2 CRD names.
// Note: DNS-1123 labels have max length 63 chars. Workspace names must comply.
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
// Returns empty list on error (non-fatal).
func discoverDBOnlyPackages(ctx context.Context, repoHandle repository.Repository) ([]repository.PackageRevision, error) {
	filter := repository.ListPackageRevisionFilter{
		Lifecycles: []porchapi.PackageRevisionLifecycle{
			porchapi.PackageRevisionLifecycleDraft,
			porchapi.PackageRevisionLifecycleProposed,
		},
	}
	return repoHandle.ListPackageRevisions(ctx, filter)
}

// combinePackages merges discovered (published) packages with valid DB-only packages.
func combinePackages(discovered []repository.PackageRevision, validDBOnly []repository.PackageRevision) []repository.PackageRevision {
	result := discovered
	if len(validDBOnly) > 0 {
		result = append(result, validDBOnly...)
	}
	return result
}

// syncV1Alpha2Migration handles v1alpha1 → v1alpha2 migration for a repository.
// Called when the v1alpha2-migration annotation is present on a Repository.
//
// Migration discovers both discovered packages (from git) and DB-only packages
// (Draft, Proposed) from the v1alpha1 DB, then creates corresponding v1alpha2
// PackageRevision CRDs for all packages.
//
// Parameter `discoveredPkgRevs` contains git-backed packages (Published, Proposed if git-pushed,
// and any Drafts if pushDraftsToGit=true).
//
// This function additionally queries for DB-only packages (Draft, Proposed) that are not
// in git, and combines them with the discovered packages to ensure zero-data-loss migration.
//
// Non-fatal errors are logged but don't fail the entire sync; the system
// prioritizes syncing git-backed packages over DB-only discovery failures.
func (r *RepositoryReconciler) syncV1Alpha2Migration(ctx context.Context, repo *configapi.Repository, repoHandle repository.Repository, discoveredPkgRevs []repository.PackageRevision) error {
	log := log.FromContext(ctx)

	// Discover drafts and proposed packages from v1alpha1 DB (migration-only).
	dbOnlyPkgRevs, err := discoverDBOnlyPackages(ctx, repoHandle)
	if err != nil {
		log.Error(err, "Failed to discover draft/proposed packages during migration", "repo", repo.Name)
		// Non-fatal: continue with published packages, skip DB-only discovery
		dbOnlyPkgRevs = []repository.PackageRevision{}
	}

	// Filter to only those with valid workspace names.
	// Invalid workspace names prevent CRD creation (DNS-1123 constraint on CRD names).
	validDBOnly := r.filterValidDrafts(dbOnlyPkgRevs)
	skippedCount := len(dbOnlyPkgRevs) - len(validDBOnly)
	if skippedCount > 0 {
		log.Info("Migration skipped draft/proposed packages with invalid workspace names",
			"repo", repo.Name, "skippedCount", skippedCount)
	}

	// Combine discovered packages (from git) with valid DB-only packages (from v1alpha1 DB).
	allPkgRevs := combinePackages(discoveredPkgRevs, validDBOnly)

	log.V(3).Info("v1alpha2 migration enabled, syncing PackageRevisions",
		"repo", repo.Name,
		"totalCount", len(allPkgRevs),
		"discoveredCount", len(discoveredPkgRevs),
		"dbOnlyCount", len(validDBOnly),
		"skippedCount", skippedCount)

	// Sync all valid packages (published + valid drafts) to v1alpha2 CRDs.
	// Individual apply/delete failures are non-fatal.
	if err := r.syncPackageRevisions(ctx, repo, allPkgRevs); err != nil {
		log.Error(err, "Failed to sync v1alpha2 PackageRevisions during migration", "repo", repo.Name)
		// Non-fatal: don't fail the entire sync for v1alpha2 creation failures
	}

	return nil
}

// filterValidDrafts returns only drafts with workspace names valid for Kubernetes CRD names.
// Drafts with invalid workspace names are silently filtered out.
func (r *RepositoryReconciler) filterValidDrafts(drafts []repository.PackageRevision) []repository.PackageRevision {
	var valid []repository.PackageRevision

	for _, draft := range drafts {
		key := draft.Key()
		if isValidDNS1123Subdomain(key.WorkspaceName) {
			valid = append(valid, draft)
		}
	}

	return valid
}
