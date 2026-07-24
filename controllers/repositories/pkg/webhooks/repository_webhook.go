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

package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// RepositoryValidator validates config.porch.kpt.dev/v1alpha1 Repository CRDs.
// It enforces cross-resource conflict detection that CEL cannot perform:
// - Duplicate git location (URL + branch + directory) in the same namespace
// - Root directory conflicts with subdirectories
// - Nested directory conflicts
type RepositoryValidator struct {
	client client.Reader
}

// NewRepositoryValidator creates a new RepositoryValidator.
func NewRepositoryValidator(client client.Reader) *RepositoryValidator {
	return &RepositoryValidator{client: client}
}

// Handle implements the admission.Handler interface for webhook registration.
func (v *RepositoryValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	switch req.Operation {
	case admissionv1.Create, admissionv1.Update:
		return v.handleCreateOrUpdate(ctx, req)
	case admissionv1.Delete:
		return v.handleDelete(ctx, req)
	default:
		return admission.Allowed("")
	}
}

// handleDelete allows all DELETE operations without validation.
// No conflict detection is needed for deletes.
func (v *RepositoryValidator) handleDelete(_ context.Context, req admission.Request) admission.Response {
	log.Log.V(3).Info("repository deletion validated",
		"namespace", req.Namespace, "name", req.Name)
	return admission.Allowed("Repository deletion validated successfully")
}

// handleCreateOrUpdate validates CREATE and UPDATE operations for repository conflicts.
func (v *RepositoryValidator) handleCreateOrUpdate(ctx context.Context, req admission.Request) admission.Response {
	logger := log.FromContext(ctx)

	if len(req.Object.Raw) == 0 {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("request object is empty"))
	}

	var attempted configapi.Repository
	if err := json.Unmarshal(req.Object.Raw, &attempted); err != nil {
		return admission.Errored(http.StatusBadRequest,
			fmt.Errorf("failed to unmarshal repository: %w", err))
	}

	// NOTE: Immutability checks (URL, branch, directory) are handled by CEL validation in the CRD.
	// This webhook only performs complex cross-resource conflict detection that CEL cannot do.

	var repoList configapi.RepositoryList
	opts := []client.ListOption{client.InNamespace(attempted.Namespace)}
	if err := v.client.List(ctx, &repoList, opts...); err != nil {
		logger.Error(err, "failed to list repositories for conflict check")
		return admission.Errored(http.StatusInternalServerError,
			fmt.Errorf("could not list repositories: %w", err))
	}

	for i := range repoList.Items {
		existing := &repoList.Items[i]
		if existing.Name == attempted.Name && existing.Namespace == attempted.Namespace {
			continue
		}
		if IsConflict(existing, &attempted) {
			logger.Info("repository conflict detected",
				"attempted", fmt.Sprintf("%s/%s", attempted.Namespace, attempted.Name),
				"conflictsWith", fmt.Sprintf("%s/%s", existing.Namespace, existing.Name))
			return admission.Denied(
				fmt.Sprintf("Repository conflict with existing repository: %s/%s",
					existing.Namespace, existing.Name))
		}
	}

	logger.V(3).Info("repository validation passed",
		"namespace", attempted.Namespace, "name", attempted.Name)
	return admission.Allowed("Repository validated successfully")
}

// NormalizeURL converts a repository URL to a comparable format by replacing
// protocol separators and path characters with dashes.
func NormalizeURL(url string) string {
	replace := strings.NewReplacer("://", "---", ":", "-", "/", "-")
	return replace.Replace(url)
}

// IsConflict checks whether two repositories conflict based on their git location.
// Conflict rules:
//  1. Same URL, branch, and directory in the same namespace
//  2. Root directory conflicts with any subdirectory under the same URL and branch
//  3. Nested directory conflicts (one path is a prefix of another)
func IsConflict(existing, attempted *configapi.Repository) bool {
	existingURL := NormalizeURL(existing.Spec.Git.Repo)
	attemptedURL := NormalizeURL(attempted.Spec.Git.Repo)

	existingDir := strings.Trim(existing.Spec.Git.Directory, "/")
	attemptedDir := strings.Trim(attempted.Spec.Git.Directory, "/")

	// Branch defaults to "main" via CRD default, so no need to handle empty values
	existingBranch := existing.Spec.Git.Branch
	attemptedBranch := attempted.Spec.Git.Branch

	// Different URL or branch → no conflict possible
	if existingURL != attemptedURL || existingBranch != attemptedBranch {
		return false
	}

	// Rule 1: Same URL, branch and directory in same namespace → conflict
	if existingDir == attemptedDir && existing.Namespace == attempted.Namespace {
		return true
	}

	// Rule 2: Root directory conflicts with any other directory
	if (existingDir == "" && attemptedDir != "") || (existingDir != "" && attemptedDir == "") {
		return true
	}

	// Rule 3: Nested directory conflicts
	if IsNestedConflict(existingDir, attemptedDir) {
		return true
	}

	return false
}

// IsNestedConflict checks if one directory path is nested within the other.
func IsNestedConflict(a, b string) bool {
	relAtoB, err1 := filepath.Rel(a, b)
	relBtoA, err2 := filepath.Rel(b, a)

	if err1 == nil && !strings.HasPrefix(relAtoB, "../") && relAtoB != "." {
		return true // b is nested within a
	}
	if err2 == nil && !strings.HasPrefix(relBtoA, "../") && relBtoA != "." {
		return true // a is nested within b
	}

	return false
}
