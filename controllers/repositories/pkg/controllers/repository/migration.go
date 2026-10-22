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
	"fmt"
	"maps"
	"strings"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/repository"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// isValidWorkspaceName reports whether name is a valid workspace segment:
// a DNS-1123 label (max 63 chars, lowercase alphanumerics and '-', no dots).
func isValidWorkspaceName(name string) bool {
	return len(validation.IsDNS1123Label(name)) == 0
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
		if isValidWorkspaceName(draft.Key().WorkspaceName) {
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

// errV1Alpha2Unavailable marks a repository annotated for v1alpha2 that this controller
// is not configured to serve.
var errV1Alpha2Unavailable = errors.New("repository is annotated for v1alpha2 but the repository controller is not configured for it")

// Migration support: enriches v1alpha2 CRDs with v1alpha1 metadata during hybrid-mode transitions.

// isMigrating checks if a repository is in active v1alpha2 migration mode.
func isMigrating(repo *configapi.Repository) bool {
	if repo == nil || repo.Annotations == nil {
		return false
	}
	return repo.Annotations[configapi.AnnotationKeyV1Alpha2Migration] == configapi.AnnotationValueMigrationEnabled
}

// v1alpha2Unavailable explains why a repository annotated for v1alpha2 cannot be served by
// this controller, or returns nil. The annotation alone moves a repository to the PRRStore
// and hides it from the v1alpha1 API, but only a controller with CRD creation enabled and
// the DB cache completes the move. Without both, the repository's packages would be
// visible in neither API. Repositories without the annotation are never affected.
func (r *RepositoryReconciler) v1alpha2Unavailable(repo *configapi.Repository) error {
	if !isMigrating(repo) {
		return nil
	}
	if !r.CreateV1Alpha2Rpkg {
		return fmt.Errorf("%w: --repositories.create-v1alpha2-rpkg is false; enable it or remove the %s annotation",
			errV1Alpha2Unavailable, configapi.AnnotationKeyV1Alpha2Migration)
	}
	if !strings.EqualFold(r.cacheType, string(cachetypes.DBCacheType)) {
		return fmt.Errorf("%w: v1alpha2 needs --repositories.cache-type=DB, but it is %q; change it or remove the %s annotation",
			errV1Alpha2Unavailable, r.cacheType, configapi.AnnotationKeyV1Alpha2Migration)
	}
	return nil
}

// enrichMetadataFromV1Alpha1 queries v1alpha1 PackageRevision metadata to preserve user-defined labels/annotations.
func (r *RepositoryReconciler) enrichMetadataFromV1Alpha1(ctx context.Context, repo *configapi.Repository, gitPackageName string) (map[string]string, map[string]string) {
	log := log.FromContext(ctx)

	// Only enrich during active migration
	if !isMigrating(repo) {
		return nil, nil
	}

	// Query v1alpha1 PackageRevision from aggregated API.
	var v1Pr porchapi.PackageRevision
	prName := gitPackageName // repo controller uses same name for git packages and v1 PRs

	err := r.Get(ctx, client.ObjectKey{
		Namespace: repo.Namespace,
		Name:      prName,
	}, &v1Pr)

	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.V(3).Info("Failed to query v1alpha1 PR for metadata enrichment",
				"pr", prName, "repo", repo.Name, "err", err)
		}
		// Not found is OK; package may not have a v1 counterpart yet
		return nil, nil
	}

	log.V(5).Info("Enriching git package with v1alpha1 PR metadata",
		"pr", prName, "labels", len(v1Pr.Labels), "annotations", len(v1Pr.Annotations))

	// Extract non-system labels and all annotations
	labels := make(map[string]string)
	for k, v := range v1Pr.Labels {
		if !systemLabels[k] {
			labels[k] = v
		}
	}

	annotations := make(map[string]string)
	for k, v := range v1Pr.Annotations {
		// Exclude internal porch annotations like render requests
		if !strings.HasPrefix(k, "porch.kpt.dev/") {
			annotations[k] = v
		}
	}

	return labels, annotations
}

// mergeMetadata combines git and v1alpha1 metadata (git takes precedence).
func mergeMetadata(gitMetadata, v1Metadata map[string]string) map[string]string {
	if len(gitMetadata) == 0 && len(v1Metadata) == 0 {
		return nil
	}

	result := make(map[string]string)

	// Start with v1 metadata, override with git metadata
	maps.Copy(result, v1Metadata)

	// Override with git metadata (git takes precedence)
	maps.Copy(result, gitMetadata)

	return result
}
