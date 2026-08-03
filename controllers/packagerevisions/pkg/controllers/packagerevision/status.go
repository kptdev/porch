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

package packagerevision

import (
	"context"
	"maps"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	fieldManagerPRController        = "packagerev-controller"
	fieldManagerPRControllerRender  = "packagerev-controller-render"
	fieldManagerPRControllerKptfile = "packagerev-controller-kptfile"
)

// updateStatus applies the PR-controller-owned status fields via SSA.
// When content is non-nil and represents a published package, publish metadata
// (revision, publishedBy, publishedAt) is included in the apply.
func (r *PackageRevisionReconciler) updateStatus(ctx context.Context, pr *porchv1alpha2.PackageRevision, content repository.PackageContent, creationSource string, conditions ...metav1.Condition) {
	if creationSource == "" {
		creationSource = pr.Status.CreationSource
	}

	status := porchv1alpha2.PackageRevisionStatus{
		ObservedGeneration: pr.Generation,
		Conditions:         conditions,
		CreationSource:     creationSource,
	}

	if content != nil {
		if porchv1alpha2.LifecycleIsPublished(porchv1alpha2.PackageRevisionLifecycle(content.Lifecycle(ctx))) {
			status.Revision = content.Key().Revision
			commitTime, commitAuthor := content.GetCommitInfo()
			status.PublishedBy = commitAuthor
			if !commitTime.IsZero() {
				t := metav1.NewTime(commitTime)
				status.PublishedAt = &t
			}
		}
		if _, selfLock, err := content.GetLock(ctx); err == nil {
			status.SelfLock = porchv1alpha2.KptLocatorToLocator(selfLock)
		}
		if _, upstreamLock, err := content.GetUpstreamLock(ctx); err == nil {
			status.UpstreamLock = porchv1alpha2.KptLocatorToLocator(upstreamLock)
		}
		if resources, err := content.GetResourceContents(ctx); err == nil {
			status.ResourcesSizeBytes = repository.CalculateResourcesSize(resources)
		}
	}

	applyObj := &porchv1alpha2.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pr.Name,
			Namespace: pr.Namespace,
		},
		Status: status,
	}

	if err := r.Status().Patch(ctx, applyObj, client.Apply, client.FieldOwner(fieldManagerPRController), client.ForceOwnership); err != nil {
		log.FromContext(ctx).Error(err, "failed to apply status")
	}
}

// updateRenderStatus patches render tracking fields and Rendered condition via SSA.
// Uses a separate field manager to avoid stomping fields owned by updateStatus.
func (r *PackageRevisionReconciler) updateRenderStatus(ctx context.Context, pr *porchv1alpha2.PackageRevision, renderingVersion, observedVersion string, conditions ...metav1.Condition) {
	status := porchv1alpha2.PackageRevisionStatus{
		RenderingPrrResourceVersion: renderingVersion,
		ObservedPrrResourceVersion:  observedVersion,
		Conditions:                  conditions,
	}

	if observedVersion == "" {
		status.ObservedPrrResourceVersion = pr.Status.ObservedPrrResourceVersion
	}

	applyObj := &porchv1alpha2.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pr.Name,
			Namespace: pr.Namespace,
		},
		Status: status,
	}

	if err := r.Status().Patch(ctx, applyObj, client.Apply, client.FieldOwner(fieldManagerPRControllerRender), client.ForceOwnership); err != nil {
		log.FromContext(ctx).Error(err, "failed to update render status")
	}
}

// refreshRenderedGeneration bumps the Rendered condition's observedGeneration
// when no render is needed but the condition is stale (e.g. after lifecycle patch).
// It preserves all other condition fields (reason, message, lastTransitionTime)
// to avoid misleading status churn.
func (r *PackageRevisionReconciler) refreshRenderedGeneration(ctx context.Context, pr *porchv1alpha2.PackageRevision) {
	for _, c := range pr.Status.Conditions {
		if c.Type == porchv1alpha2.ConditionRendered && c.Status == metav1.ConditionTrue && c.ObservedGeneration < pr.Generation {
			// Only bump observedGeneration — preserve existing reason, message, and transition time.
			c.ObservedGeneration = pr.Generation
			r.updateRenderStatus(ctx, pr, pr.Status.RenderingPrrResourceVersion, "",
				c,
			)
			return
		}
	}
}

// setSourceFailed logs the error and sets Ready=False and Rendered=False.
// Rendered is set even though rendering was never attempted — the package
// content didn't land successfully, so "not rendered" is accurate.
func (r *PackageRevisionReconciler) setSourceFailed(ctx context.Context, pr *porchv1alpha2.PackageRevision, err error) error {
	log.FromContext(ctx).Error(err, "source execution failed")
	r.updateStatus(ctx, pr, nil, "",
		readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonFailed, err.Error()),
		renderedCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonFailed, err.Error()),
	)
	return err
}

func (r *PackageRevisionReconciler) setRenderFailed(ctx context.Context, pr *porchv1alpha2.PackageRevision, err error) {
	// Pass the render-request annotation as observedVersion so the controller
	// records that this request was processed (even though it failed).
	// Without this, the render trigger keeps re-firing indefinitely.
	observed := pr.Annotations[porchv1alpha2.AnnotationRenderRequest]
	r.updateRenderStatus(ctx, pr, "", observed,
		renderedCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonRenderFailed, err.Error()),
	)
	// Also set Ready=False — a failed render means the package is not ready.
	r.updateStatus(ctx, pr, nil, "",
		readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonRenderFailed, "render failed"),
	)
}

func readyCondition(generation int64, status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{
		Type:               porchv1alpha2.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: generation,
	}
}

func renderedCondition(generation int64, status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{
		Type:               porchv1alpha2.ConditionRendered,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: generation,
	}
}

// updateKptfileFields updates the CRD with Kptfile-derived fields after render.
// Uses the main PR controller field manager with ForceOwnership to take
// ownership from the repo controller (which sets these on create).
func (r *PackageRevisionReconciler) updateKptfileFields(ctx context.Context, pr *porchv1alpha2.PackageRevision, kf kptfilev1.KptFile) {
	log := log.FromContext(ctx)

	gates := porchv1alpha2.KptfileToReadinessGates(kf)
	meta := porchv1alpha2.KptfileToPackageMetadata(kf)
	conds := porchv1alpha2.KptfileToPackageConditions(kf)

	if len(gates) == 0 && meta == nil && len(conds) == 0 {
		return
	}

	// Batch spec fields into single SSA patch to ensure atomic updates.
	// Multiple separate patches can cause visibility issues where subsequent reads don't see all changes.
	spec := porchv1alpha2.PackageRevisionSpec{}
	hasSpecFields := false

	if len(gates) > 0 {
		spec.ReadinessGates = gates
		hasSpecFields = true
	}

	// Kptfile is authoritative source for metadata. Sync if it differs from spec.
	if meta != nil && !packageMetadataEqual(pr.Spec.PackageMetadata, meta) {
		spec.PackageMetadata = meta
		hasSpecFields = true
	}

	if hasSpecFields {
		r.applySpec(ctx, pr, spec)
	}

	// Apply conditions via status API (separate endpoint from spec).
	if len(conds) > 0 {
		log.V(3).Info("syncing package conditions from Kptfile", "conditionCount", len(conds))
		r.applyStatus(ctx, pr, porchv1alpha2.PackageRevisionStatus{PackageConditions: conds})
	}
}

func (r *PackageRevisionReconciler) applySpec(ctx context.Context, pr *porchv1alpha2.PackageRevision, spec porchv1alpha2.PackageRevisionSpec) {
	log := log.FromContext(ctx)

	obj := &porchv1alpha2.PackageRevision{
		TypeMeta:   metav1.TypeMeta{Kind: "PackageRevision", APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier()},
		ObjectMeta: metav1.ObjectMeta{Name: pr.Name, Namespace: pr.Namespace},
		Spec:       spec,
	}
	if err := r.Patch(ctx, obj, client.Apply, client.FieldOwner(fieldManagerPRControllerKptfile), client.ForceOwnership); err != nil {
		log.Error(err, "failed to apply spec fields")
	}
}

func (r *PackageRevisionReconciler) applyStatus(ctx context.Context, pr *porchv1alpha2.PackageRevision, status porchv1alpha2.PackageRevisionStatus) {
	obj := &porchv1alpha2.PackageRevision{
		TypeMeta:   metav1.TypeMeta{Kind: "PackageRevision", APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier()},
		ObjectMeta: metav1.ObjectMeta{Name: pr.Name, Namespace: pr.Namespace},
		Status:     status,
	}
	if err := r.Status().Patch(ctx, obj, client.Apply, client.FieldOwner(fieldManagerPRControllerKptfile), client.ForceOwnership); err != nil {
		log.FromContext(ctx).Error(err, "failed to apply status fields")
	}
}

// packageMetadataEqual returns true if two PackageMetadata values have identical labels and annotations.
func packageMetadataEqual(a, b *porchv1alpha2.PackageMetadata) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return maps.Equal(a.Labels, b.Labels) && maps.Equal(a.Annotations, b.Annotations)
}
