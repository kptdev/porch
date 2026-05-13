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

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
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
	gates := porchv1alpha2.KptfileToReadinessGates(kf)
	meta := porchv1alpha2.KptfileToPackageMetadata(kf)
	conds := porchv1alpha2.KptfileToPackageConditions(kf)

	// Skip if there's nothing to sync — avoids SSA taking ownership of empty fields.
	if len(gates) == 0 && meta == nil && len(conds) == 0 {
		return
	}

	if len(gates) > 0 || meta != nil {
		specObj := &porchv1alpha2.PackageRevision{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PackageRevision",
				APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      pr.Name,
				Namespace: pr.Namespace,
			},
			Spec: porchv1alpha2.PackageRevisionSpec{
				ReadinessGates:  gates,
				PackageMetadata: meta,
			},
		}
		if err := r.Patch(ctx, specObj, client.Apply, client.FieldOwner(fieldManagerPRControllerKptfile), client.ForceOwnership); err != nil {
			log.FromContext(ctx).Error(err, "failed to update Kptfile-derived spec fields")
		}
	}

	if len(conds) > 0 {
		statusObj := &porchv1alpha2.PackageRevision{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PackageRevision",
				APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      pr.Name,
				Namespace: pr.Namespace,
			},
			Status: porchv1alpha2.PackageRevisionStatus{
				PackageConditions: conds,
			},
		}
		if err := r.Status().Patch(ctx, statusObj, client.Apply, client.FieldOwner(fieldManagerPRControllerKptfile), client.ForceOwnership); err != nil {
			log.FromContext(ctx).Error(err, "failed to update Kptfile-derived status fields")
		}
	}
}
