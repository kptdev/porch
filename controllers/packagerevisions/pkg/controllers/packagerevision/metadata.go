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

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

// reconcilePackageMetadata syncs user-set spec.packageMetadata to the Kptfile.
// Only applies to Draft packages; Proposed and Published are immutable (aligns with v1alpha1).
func (r *PackageRevisionReconciler) reconcilePackageMetadata(ctx context.Context, pr *porchv1alpha2.PackageRevision, repoKey repository.RepositoryKey) (*ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Only Draft packages can have metadata synced.
	if pr.Spec.Lifecycle != porchv1alpha2.PackageRevisionLifecycleDraft {
		return nil, nil
	}

	// Nothing to sync if user hasn't set packageMetadata.
	if pr.Spec.PackageMetadata == nil {
		return nil, nil
	}

	// Skip if a render is already pending (from PRR push) to respect "PRR push wins" semantics.
	if pr.Annotations[porchv1alpha2.AnnotationRenderRequest] != pr.Status.ObservedPrrResourceVersion {
		log.V(3).Info("render pending from PRR push, skipping metadata sync")
		return nil, nil
	}

	// Skip if source render hasn't completed yet — content is still being initialized.
	if pr.Status.CreationSource != "" && !isRenderedTrue(pr) {
		log.V(3).Info("source render pending, skipping metadata sync")
		return nil, nil
	}

	// Read and parse current package content.
	resources, kf, err := r.readAndParseKptfile(ctx, repoKey, pr)
	if err != nil {
		return nil, nil
	}

	// Apply metadata changes and sync to draft. Returns false if Kptfile already matches spec.
	result, err := r.applyAndWriteMetadata(ctx, repoKey, pr, resources, kf)
	if err != nil {
		return nil, nil
	}

	if result {
		return r.triggerRenderIfNeeded(ctx, pr)
	}

	return nil, nil
}

// readAndParseKptfile reads all package resources and parses the Kptfile.
func (r *PackageRevisionReconciler) readAndParseKptfile(ctx context.Context, repoKey repository.RepositoryKey, pr *porchv1alpha2.PackageRevision) (map[string]string, kptfilev1.KptFile, error) {
	log := log.FromContext(ctx)

	content, err := r.ContentCache.GetPackageContent(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName)
	if err != nil {
		log.Error(err, "failed to get package content")
		return nil, kptfilev1.KptFile{}, err
	}

	resources, err := content.GetResourceContents(ctx)
	if err != nil {
		log.Error(err, "failed to read resources")
		return nil, kptfilev1.KptFile{}, err
	}

	kf, err := kptfileFromResources(resources)
	if err != nil {
		log.Error(err, "failed to parse Kptfile")
		return nil, kptfilev1.KptFile{}, err
	}

	return resources, kf, nil
}

// applyAndWriteMetadata applies metadata changes and writes to a draft.
func (r *PackageRevisionReconciler) applyAndWriteMetadata(ctx context.Context, repoKey repository.RepositoryKey, pr *porchv1alpha2.PackageRevision, resources map[string]string, kf kptfilev1.KptFile) (bool, error) {
	log := log.FromContext(ctx)

	// Apply spec.packageMetadata to Kptfile (merge mode).
	if !applyPackageMetadataToKptfile(&kf, pr) {
		return false, nil // No changes, nothing to write
	}

	// Serialize Kptfile.
	updatedKfBytes, err := yaml.MarshalWithOptions(&kf, &yaml.EncoderOptions{SeqIndent: yaml.WideSequenceStyle})
	if err != nil {
		log.Error(err, "failed to serialize Kptfile")
		return false, err
	}

	// Create draft and write updated resources.
	draft, err := r.ContentCache.CreateDraftFromExisting(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName)
	if err != nil {
		log.Error(err, "failed to create draft")
		return false, err
	}

	// UpdateResources is a full replace — include all files to avoid data loss.
	resources["Kptfile"] = string(updatedKfBytes)
	log.Info("metadata sync writing resources", "resourceCount", len(resources))
	if err := draft.UpdateResources(ctx, resources, "metadata-sync"); err != nil {
		log.Error(err, "failed to write resources")
		return false, err
	}

	if err := r.ContentCache.CloseDraft(ctx, repoKey, draft, 0); err != nil {
		log.Error(err, "failed to close draft")
		return false, err
	}

	log.V(3).Info("metadata synced to draft")
	return true, nil
}

// triggerRenderIfNeeded triggers a render cycle for packages that have already been rendered.
// For new (unrendered) packages, render will occur via sourceTrigger without annotation patching.
func (r *PackageRevisionReconciler) triggerRenderIfNeeded(ctx context.Context, pr *porchv1alpha2.PackageRevision) (*ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Trigger render based on package state:
	// - Already-rendered packages: patch annotation to trigger render in next cycle via annotationTrigger
	// - New packages: skip annotation, rely on sourceTrigger (no requeue needed)
	if isRenderedTrue(pr) {
		if err := r.setRenderRequestAnnotation(ctx, pr); err != nil {
			log.Error(err, "failed to set render annotation")
			return nil, nil
		}
		log.Info("metadata updated in already-rendered draft, render queued via annotation")
		return &ctrl.Result{Requeue: true}, nil
	}

	log.Info("metadata synced to new package, render will trigger via sourceTrigger")
	return nil, nil
}

// applyPackageMetadataToKptfile applies labels and annotations to Kptfile (merge mode).
func applyPackageMetadataToKptfile(kf *kptfilev1.KptFile, pr *porchv1alpha2.PackageRevision) bool {
	if pr.Spec.PackageMetadata == nil {
		return false
	}

	var labelsChanged bool
	kf.Labels, labelsChanged = applyMetadataMap(kf.Labels, pr.Spec.PackageMetadata.Labels)

	var annotationsChanged bool
	kf.Annotations, annotationsChanged = applyMetadataMap(kf.Annotations, pr.Spec.PackageMetadata.Annotations)

	return labelsChanged || annotationsChanged
}

// applyMetadataMap merges desired key-value pairs into current, returning the resulting map and whether any changes were made.
// Safe to call with nil current or desired maps.
func applyMetadataMap(current, desired map[string]string) (map[string]string, bool) {
	if len(desired) == 0 {
		return current, false
	}

	if current == nil {
		current = make(map[string]string, len(desired))
	}

	changed := false
	for k, v := range desired {
		if cv, exists := current[k]; !exists || cv != v {
			current[k] = v
			changed = true
		}
	}

	return current, changed
}

// setRenderRequestAnnotation triggers render by updating the render-request annotation with nanosecond precision.
func (r *PackageRevisionReconciler) setRenderRequestAnnotation(ctx context.Context, pr *porchv1alpha2.PackageRevision) error {
	// Capture original before mutation to generate a proper MergeFrom patch.
	original := pr.DeepCopy()

	if pr.Annotations == nil {
		pr.Annotations = make(map[string]string)
	}
	// Value just needs to differ from the previous one to trigger a reconcile.
	// Nanosecond precision avoids collisions on rapid successive updates; human-readable format aids debugging.
	pr.Annotations[porchv1alpha2.AnnotationRenderRequest] = metav1.Now().Format("2006-01-02T15:04:05.000000000Z07:00")
	return r.Patch(ctx, pr, client.MergeFrom(original))
}
