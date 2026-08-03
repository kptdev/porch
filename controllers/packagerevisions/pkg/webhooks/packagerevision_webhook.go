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
	"slices"
	"strings"
	"time"

	"github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// PackageRevisionValidator validates v1alpha2 PackageRevision CRDs.
// It enforces all CREATE, UPDATE, and DELETE validation rules per the spec.
type PackageRevisionValidator struct {
	client client.Reader
}

// NewPackageRevisionValidator creates a new PackageRevisionValidator.
func NewPackageRevisionValidator(client client.Reader) *PackageRevisionValidator {
	return &PackageRevisionValidator{client: client}
}

// ValidateCreate validates creation of a PackageRevision.
// Repo-discovered packages (Source == nil) can have any lifecycle (determined from git refs).
// User-created packages (Source != nil) must have Draft or Proposed lifecycle on CREATE.
func (v *PackageRevisionValidator) ValidateCreate(ctx context.Context, obj *v1alpha2.PackageRevision) (admission.Warnings, error) {
	// Distinguish between repo-discovered and user-created packages
	isRepoDiscovered := obj.Spec.Source == nil

	if isRepoDiscovered {
		// Repo-discovered packages: lifecycle is determined from git (Draft/Proposed/Published/DeletionProposed)
		// Only validate repository and workspace, not lifecycle values
		if err := v.validateRepositoryAnnotation(ctx, obj); err != nil {
			return nil, fmt.Errorf("repository validation failed: %w", err)
		}
		if err := v.validateWorkspaceUniqueness(ctx, obj); err != nil {
			return nil, fmt.Errorf("workspace uniqueness check failed: %w", err)
		}
		return nil, nil
	}

	// User-created packages: enforce lifecycle and source validation
	lifecycle := obj.Spec.Lifecycle
	if lifecycle == "" {
		return nil, fmt.Errorf("lifecycle is required on creation")
	}

	// User-created packages must start as Draft or Proposed
	if lifecycle != v1alpha2.PackageRevisionLifecycleDraft &&
		lifecycle != v1alpha2.PackageRevisionLifecycleProposed {
		return nil, fmt.Errorf("lifecycle must be Draft or Proposed on creation, got %s", lifecycle)
	}

	// Source reference existence (CopyFrom/CloneFrom/Upgrade targets) is validated at reconcile,
	// not admission. This follows standard K8s patterns (eventual consistency, transient races)
	// and surfaces errors via status.conditions.

	// Validate repository annotation
	if err := v.validateRepositoryAnnotation(ctx, obj); err != nil {
		return nil, fmt.Errorf("repository validation failed: %w", err)
	}

	// Validate workspace uniqueness
	if err := v.validateWorkspaceUniqueness(ctx, obj); err != nil {
		return nil, fmt.Errorf("workspace uniqueness check failed: %w", err)
	}

	return nil, nil
}

// ValidateUpdate validates updates to a PackageRevision.
func (v *PackageRevisionValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *v1alpha2.PackageRevision) (admission.Warnings, error) {
	// Validate immutable fields
	if err := v.validateImmutableFields(oldObj, newObj); err != nil {
		return nil, fmt.Errorf("immutable field validation failed: %w", err)
	}

	// Only validate lifecycle transition if lifecycle changed
	if oldObj.Spec.Lifecycle != newObj.Spec.Lifecycle {
		// Validate lifecycle transition
		if err := v.validateLifecycleTransition(oldObj.Spec.Lifecycle, newObj.Spec.Lifecycle); err != nil {
			return nil, fmt.Errorf("lifecycle transition validation failed: %w", err)
		}

		// Validate render race prevention for non-Draft transitions
		if newObj.Spec.Lifecycle != v1alpha2.PackageRevisionLifecycleDraft {
			if err := v.validateRenderRacePrevention(newObj); err != nil {
				return nil, fmt.Errorf("render race prevention failed: %w", err)
			}
		}
	}

	return nil, nil
}

// ValidateDelete validates deletion of a PackageRevision.
// Check ordering:
//  1. DeletionTimestamp (K8s GC already accepted a prior DELETE) - allow
//  2. Upstream references (data integrity) - block if dependents exist
//  3. Published lifecycle - block unless owner repo is gone (cascade deletion)
func (v *PackageRevisionValidator) ValidateDelete(ctx context.Context, obj *v1alpha2.PackageRevision) (admission.Warnings, error) {
	// Allow if marked for GC (K8s cascade deletion already accepted).
	if obj.DeletionTimestamp != nil {
		return nil, nil
	}

	// Block if other packages depend on this one (data integrity).
	if err := v.validateUpstreamReferences(ctx, obj); err != nil {
		return nil, fmt.Errorf("upstream reference validation failed: %w", err)
	}

	// Enforce lifecycle constraint: Published packages must transition to DeletionProposed first.
	// Exception: cascade deletion (owning Repository is gone or being deleted).
	if obj.Spec.Lifecycle == v1alpha2.PackageRevisionLifecyclePublished {
		if !v.isOwnerRepoGone(ctx, obj) {
			return nil, fmt.Errorf("cannot delete Published package directly; transition to DeletionProposed first")
		}
	}

	return nil, nil
}

// isOwnerRepoGone returns true if the owning Repository is not found or is being deleted.
// This detects cascade deletion scenarios where K8s GC is cleaning up orphaned PackageRevisions.
func (v *PackageRevisionValidator) isOwnerRepoGone(ctx context.Context, pr *v1alpha2.PackageRevision) bool {
	repo := &configapi.Repository{}
	err := v.client.Get(ctx, types.NamespacedName{
		Namespace: pr.Namespace,
		Name:      pr.Spec.RepositoryName,
	}, repo)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true
		}
		// On transient errors, assume repo exists (fail closed — block the delete).
		log.FromContext(ctx).V(3).Info("failed to check owner repository", "error", err)
		return false
	}
	// Repo exists but is being deleted — cascade deletion in progress.
	return repo.DeletionTimestamp != nil
}

// validateRepositoryAnnotation checks that the repository exists and has v1alpha2-migration annotation.
func (v *PackageRevisionValidator) validateRepositoryAnnotation(ctx context.Context, pr *v1alpha2.PackageRevision) error {
	if v.client == nil {
		return fmt.Errorf("internal error: validator client is nil")
	}

	repo := &configapi.Repository{}
	repoKey := types.NamespacedName{
		Namespace: pr.Namespace,
		Name:      pr.Spec.RepositoryName,
	}

	if err := v.client.Get(ctx, repoKey, repo); err != nil {
		return fmt.Errorf("repository %s/%s not found: %w", pr.Namespace, pr.Spec.RepositoryName, err)
	}

	annotationValue, hasAnnotation := repo.Annotations[configapi.AnnotationKeyV1Alpha2Migration]
	if !hasAnnotation || annotationValue != configapi.AnnotationValueMigrationEnabled {
		return fmt.Errorf("repository %s/%s not enabled for v1alpha2", repo.Namespace, repo.Name)
	}

	return nil
}

// validateWorkspaceUniqueness ensures no other PackageRevision exists with same repo+package+workspace.
// Filters by spec.repositoryName (present at creation) rather than the repository label
// (applied async by the PR controller) to avoid a race on concurrent CREATEs.
func (v *PackageRevisionValidator) validateWorkspaceUniqueness(ctx context.Context, pr *v1alpha2.PackageRevision) error {
	prList := &v1alpha2.PackageRevisionList{}
	if err := v.client.List(ctx, prList, client.InNamespace(pr.Namespace)); err != nil {
		return fmt.Errorf("failed to list PackageRevisions for workspace uniqueness check: %w", err)
	}

	for _, p := range prList.Items {
		if p.Name == pr.Name && p.Name != "" {
			continue
		}
		if p.Spec.RepositoryName == pr.Spec.RepositoryName &&
			p.Spec.PackageName == pr.Spec.PackageName &&
			p.Spec.WorkspaceName == pr.Spec.WorkspaceName {
			return fmt.Errorf("workspace %s already exists for package %s", pr.Spec.WorkspaceName, pr.Spec.PackageName)
		}
	}

	return nil
}

// validateImmutableFields checks that repository, packageName, workspaceName, and source are not modified.
func (v *PackageRevisionValidator) validateImmutableFields(oldPR, newPR *v1alpha2.PackageRevision) error {
	if oldPR.Spec.RepositoryName != newPR.Spec.RepositoryName {
		return fmt.Errorf("repository field is immutable")
	}
	if oldPR.Spec.PackageName != newPR.Spec.PackageName {
		return fmt.Errorf("packageName field is immutable")
	}
	if oldPR.Spec.WorkspaceName != newPR.Spec.WorkspaceName {
		return fmt.Errorf("workspaceName field is immutable")
	}
	if !sourceEqual(oldPR.Spec.Source, newPR.Spec.Source) {
		return fmt.Errorf("source field is immutable")
	}
	return nil
}

// sourceEqual compares two Source specifications for equality.
func sourceEqual(s1, s2 *v1alpha2.PackageSource) bool {
	if (s1 == nil) != (s2 == nil) {
		return false
	}
	if s1 == nil {
		return true
	}

	if (s1.Init != nil) != (s2.Init != nil) {
		return false
	}
	if s1.Init != nil && !initEqual(s1.Init, s2.Init) {
		return false
	}

	if (s1.CloneFrom != nil) != (s2.CloneFrom != nil) {
		return false
	}
	if s1.CloneFrom != nil && !cloneFromEqual(s1.CloneFrom, s2.CloneFrom) {
		return false
	}

	if (s1.CopyFrom != nil) != (s2.CopyFrom != nil) {
		return false
	}
	if s1.CopyFrom != nil && s1.CopyFrom.Name != s2.CopyFrom.Name {
		return false
	}

	if (s1.Upgrade != nil) != (s2.Upgrade != nil) {
		return false
	}
	if s1.Upgrade != nil && !upgradeEqual(s1.Upgrade, s2.Upgrade) {
		return false
	}

	return true
}

// initEqual compares two Init specs for equality.
func initEqual(i1, i2 *v1alpha2.PackageInitSpec) bool {
	if i1 == nil || i2 == nil {
		return i1 == i2
	}
	if i1.Description != i2.Description || i1.Site != i2.Site {
		return false
	}
	if len(i1.Keywords) != len(i2.Keywords) {
		return false
	}
	for idx, kw := range i1.Keywords {
		if kw != i2.Keywords[idx] {
			return false
		}
	}
	return true
}

// cloneFromEqual compares two CloneFrom specs for equality.
func cloneFromEqual(c1, c2 *v1alpha2.UpstreamPackage) bool {
	if c1 == nil || c2 == nil {
		return c1 == c2
	}
	if c1.Type != c2.Type {
		return false
	}
	if (c1.Git != nil) != (c2.Git != nil) {
		return false
	}
	if c1.Git != nil {
		if c1.Git.Repo != c2.Git.Repo || c1.Git.Ref != c2.Git.Ref ||
			c1.Git.Directory != c2.Git.Directory || c1.Git.SecretRef.Name != c2.Git.SecretRef.Name {
			return false
		}
	}
	if (c1.UpstreamRef != nil) != (c2.UpstreamRef != nil) {
		return false
	}
	if c1.UpstreamRef != nil && c1.UpstreamRef.Name != c2.UpstreamRef.Name {
		return false
	}
	return true
}

// upgradeEqual compares two Upgrade specs for equality.
func upgradeEqual(u1, u2 *v1alpha2.PackageUpgradeSpec) bool {
	if u1 == nil || u2 == nil {
		return u1 == u2
	}
	return u1.OldUpstream.Name == u2.OldUpstream.Name &&
		u1.NewUpstream.Name == u2.NewUpstream.Name &&
		u1.CurrentPackage.Name == u2.CurrentPackage.Name &&
		u1.Strategy == u2.Strategy
}

// validateLifecycleTransition checks if the lifecycle transition is allowed.
func (v *PackageRevisionValidator) validateLifecycleTransition(from, to v1alpha2.PackageRevisionLifecycle) error {
	allowedTransitions := map[v1alpha2.PackageRevisionLifecycle][]v1alpha2.PackageRevisionLifecycle{
		v1alpha2.PackageRevisionLifecycleDraft:            {v1alpha2.PackageRevisionLifecycleProposed},
		v1alpha2.PackageRevisionLifecycleProposed:         {v1alpha2.PackageRevisionLifecyclePublished, v1alpha2.PackageRevisionLifecycleDraft},
		v1alpha2.PackageRevisionLifecyclePublished:        {v1alpha2.PackageRevisionLifecycleDeletionProposed},
		v1alpha2.PackageRevisionLifecycleDeletionProposed: {v1alpha2.PackageRevisionLifecyclePublished},
	}

	allowed, exists := allowedTransitions[from]
	if !exists {
		return fmt.Errorf("invalid transition %s → %s", from, to)
	}

	if !containsLifecycle(allowed, to) {
		allowedStr := make([]string, len(allowed))
		for i, t := range allowed {
			allowedStr[i] = string(t)
		}
		return fmt.Errorf("invalid transition %s → %s (allowed: %s)", from, to, strings.Join(allowedStr, ", "))
	}

	return nil
}

// containsLifecycle checks if a lifecycle is in a list of lifecycles.
func containsLifecycle(lifecycles []v1alpha2.PackageRevisionLifecycle, target v1alpha2.PackageRevisionLifecycle) bool {
	return slices.Contains(lifecycles, target)
}

// validateRenderRacePrevention blocks lifecycle transitions while render is in progress.
func (v *PackageRevisionValidator) validateRenderRacePrevention(pr *v1alpha2.PackageRevision) error {
	if pr.Status.RenderingPrrResourceVersion != "" {
		return fmt.Errorf("cannot change lifecycle while render is in progress")
	}

	observedVersion := pr.Status.ObservedPrrResourceVersion
	renderRequestAnnotation := ""
	if pr.Annotations != nil {
		renderRequestAnnotation = pr.Annotations[v1alpha2.AnnotationRenderRequest]
	}

	if renderRequestAnnotation != "" && observedVersion != renderRequestAnnotation {
		return fmt.Errorf("cannot change lifecycle until content is rendered")
	}

	return nil
}

// validateUpstreamReferences checks if any other PackageRevisions reference this one as upstream.
// Fails closed: if the list cannot be completed the delete is rejected (retry later).
func (v *PackageRevisionValidator) validateUpstreamReferences(ctx context.Context, pr *v1alpha2.PackageRevision) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	prList := &v1alpha2.PackageRevisionList{}
	if err := v.client.List(ctx, prList, client.InNamespace(pr.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "upstream reference check failed (blocking deletion)",
			"namespace", pr.Namespace)
		return fmt.Errorf("cannot verify upstream references (retry later): %w", err)
	}

	for _, p := range prList.Items {
		if p.UID == pr.UID || p.Spec.Source == nil {
			continue
		}

		// Check all three reference types in one pass
		if v.isReferencedBy(&p, pr.Name) {
			return fmt.Errorf("PackageRevision referenced by: %s/%s", p.Namespace, p.Name)
		}
	}

	return nil
}

// isReferencedBy checks if the given PackageRevision references the target by name.
func (v *PackageRevisionValidator) isReferencedBy(p *v1alpha2.PackageRevision, targetName string) bool {
	if p.Spec.Source == nil {
		return false
	}

	// Check CopyFrom reference
	if p.Spec.Source.CopyFrom != nil && p.Spec.Source.CopyFrom.Name == targetName {
		return true
	}

	// Check CloneFrom reference
	if p.Spec.Source.CloneFrom != nil && p.Spec.Source.CloneFrom.UpstreamRef != nil && p.Spec.Source.CloneFrom.UpstreamRef.Name == targetName {
		return true
	}

	// Check Upgrade references (any of the three fields can reference this package)
	if p.Spec.Source.Upgrade != nil {
		up := p.Spec.Source.Upgrade
		if up.OldUpstream.Name == targetName || up.NewUpstream.Name == targetName || up.CurrentPackage.Name == targetName {
			return true
		}
	}

	return false
}

// Handle implements the admission.Handler interface for webhook registration.
// It dispatches to the validator's methods based on the operation type.
func (v *PackageRevisionValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	switch req.Operation {
	case admissionv1.Create:
		return v.handleCreate(ctx, req)
	case admissionv1.Update:
		return v.handleUpdate(ctx, req)
	case admissionv1.Delete:
		return v.handleDelete(ctx, req)
	default:
		return admission.Allowed("")
	}
}

// handleCreate processes CREATE admission requests.
func (v *PackageRevisionValidator) handleCreate(ctx context.Context, req admission.Request) admission.Response {
	pr, errResp := v.unmarshalPackageRevision(req.Object.Raw, "object")
	if errResp != nil {
		return *errResp
	}
	_, err := v.ValidateCreate(ctx, pr)
	if err != nil {
		return admission.Denied(err.Error())
	}
	return admission.Allowed("")
}

// handleUpdate processes UPDATE admission requests.
func (v *PackageRevisionValidator) handleUpdate(ctx context.Context, req admission.Request) admission.Response {
	oldPR := &v1alpha2.PackageRevision{}
	// OldObject is optional, only unmarshal if present
	if len(req.OldObject.Raw) > 0 {
		errResp := v.unmarshalInto(req.OldObject.Raw, oldPR, "old object")
		if errResp != nil {
			return *errResp
		}
	}

	newPR, errResp := v.unmarshalPackageRevision(req.Object.Raw, "object")
	if errResp != nil {
		return *errResp
	}

	_, err := v.ValidateUpdate(ctx, oldPR, newPR)
	if err != nil {
		return admission.Denied(err.Error())
	}
	return admission.Allowed("")
}

// handleDelete processes DELETE admission requests.
func (v *PackageRevisionValidator) handleDelete(ctx context.Context, req admission.Request) admission.Response {
	pr, errResp := v.unmarshalPackageRevision(req.OldObject.Raw, "delete object")
	if errResp != nil {
		return *errResp
	}

	_, err := v.ValidateDelete(ctx, pr)
	if err != nil {
		return admission.Denied(err.Error())
	}
	return admission.Allowed("")
}

// unmarshalPackageRevision unmarshals raw bytes into a PackageRevision, handling empty and malformed data.
func (v *PackageRevisionValidator) unmarshalPackageRevision(raw []byte, fieldName string) (*v1alpha2.PackageRevision, *admission.Response) {
	pr := &v1alpha2.PackageRevision{}
	errResp := v.unmarshalInto(raw, pr, fieldName)
	if errResp != nil {
		return nil, errResp
	}
	return pr, nil
}

// unmarshalInto unmarshals raw bytes into a target object, handling empty and malformed data.
func (v *PackageRevisionValidator) unmarshalInto(raw []byte, target interface{}, fieldName string) *admission.Response {
	if len(raw) == 0 {
		resp := admission.Errored(http.StatusBadRequest, fmt.Errorf("%s is empty", fieldName))
		return &resp
	}
	if err := json.Unmarshal(raw, target); err != nil {
		resp := admission.Errored(http.StatusBadRequest, fmt.Errorf("failed to unmarshal %s: %w", fieldName, err))
		return &resp
	}
	return nil
}
