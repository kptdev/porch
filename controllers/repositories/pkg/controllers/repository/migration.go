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

	porchv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

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
	var v1Pr porchv1alpha1.PackageRevision
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
