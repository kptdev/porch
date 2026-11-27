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
	"fmt"
	"maps"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	fieldManagerRepoController     = "repository-controller"
	fieldManagerRepoControllerSeed = "repository-controller-seed"
	RepositoryLabel                = porchv1alpha2.RepositoryLabelKey
)

// syncPackageRevisions creates, updates, or deletes PackageRevision resources
// to match the packages discovered by ListPackageRevisions.
//
// Individual apply/delete failures are logged but do not fail the sync.
// The repo is considered healthy if git is reachable — etcd plumbing errors
// (conflicts, quota, etc.) are transient and resolve on the next sync.
//
// TODO: When the system is fully async (PR controller active), introduce a
// degraded condition on the Repository CR (e.g. PackageRevisionSyncComplete=False)
// to surface partial sync failures to users without marking the repo as unhealthy.
func (r *RepositoryReconciler) syncPackageRevisions(ctx context.Context, repo *configapi.Repository, pkgRevs []repository.PackageRevision) error {
	existingByName, err := r.listExistingPackageRevisions(ctx, repo)
	if err != nil {
		return err
	}

	desiredNames := r.applyDesiredPackageRevisions(ctx, repo, pkgRevs, existingByName)
	r.deleteStalePackageRevisions(ctx, existingByName, desiredNames)

	return nil
}

func (r *RepositoryReconciler) listExistingPackageRevisions(ctx context.Context, repo *configapi.Repository) (map[string]*porchv1alpha2.PackageRevision, error) {
	existing := &porchv1alpha2.PackageRevisionList{}
	if err := r.List(ctx, existing,
		client.InNamespace(repo.Namespace),
		client.MatchingLabels{RepositoryLabel: repo.Name},
	); err != nil {
		return nil, fmt.Errorf("failed to list existing PackageRevisions: %w", err)
	}
	result := make(map[string]*porchv1alpha2.PackageRevision, len(existing.Items))
	for i := range existing.Items {
		result[existing.Items[i].Name] = &existing.Items[i]
	}
	return result, nil
}

func (r *RepositoryReconciler) applyDesiredPackageRevisions(ctx context.Context, repo *configapi.Repository, pkgRevs []repository.PackageRevision, existingByName map[string]*porchv1alpha2.PackageRevision) map[string]bool {
	log := log.FromContext(ctx)
	desiredNames := make(map[string]bool, len(pkgRevs))
	latestByName := computeLatestRevisions(ctx, pkgRevs)

	for _, pkgRev := range pkgRevs {
		name := pkgRev.KubeObjectName()
		ex, isUpdate := existingByName[name]

		// If a user-created package (Source != nil) already exists with this name,
		// skip repo management. User-created packages are managed by the PR controller,
		// not the repo controller. Mark as desired to prevent deletion.
		if isUpdate && ex.Spec.Source != nil {
			log.V(5).Info("PackageRevision is user-created, skipping repo sync management", "name", name)
			desiredNames[name] = true
			continue
		}

		isLatest := latestByName[name]
		desired, err := buildPackageRevision(ctx, repo, pkgRev, isLatest)
		if err != nil {
			log.Error(err, "Failed to build PackageRevision", "key", pkgRev.Key())
			continue
		}
		desiredNames[desired.Name] = true

		if isUpdate {
			keepPublishedLifecycle(ex, desired)
		}

		// Apply repo-owned fields only when they changed, to avoid patch churn.
		repoFieldsUpToDate := isUpdate && packageRevisionUpToDate(ex, desired)
		if !repoFieldsUpToDate {
			if err := r.applyPackageRevision(ctx, desired); err != nil {
				continue // apply failed; retry on next sync
			}
		}

		// applySeedFields reads the Kptfile from git, so skip it when already seeded.
		if !repoFieldsUpToDate || !isSeeded(ex) {
			r.applySeedFields(ctx, repo, pkgRev, desired)
		}

		switch {
		case !isUpdate:
			log.V(3).Info("Created PackageRevision", "name", desired.Name)
		case repoFieldsUpToDate:
			log.V(5).Info("PackageRevision up-to-date", "name", desired.Name)
		default:
			log.V(3).Info("Updated PackageRevision", "name", desired.Name)
		}
	}

	return desiredNames
}

func (r *RepositoryReconciler) deleteStalePackageRevisions(ctx context.Context, existingByName map[string]*porchv1alpha2.PackageRevision, desiredNames map[string]bool) {
	log := log.FromContext(ctx)
	for name, ex := range existingByName {
		if desiredNames[name] {
			continue
		}
		// Never delete user-created packages (Source != nil). These are managed
		// by the PR controller, not the repo controller. They may not yet have
		// a corresponding git branch if the PR controller hasn't reconciled.
		if ex.Spec.Source != nil {
			log.V(5).Info("skipping deletion of user-created PackageRevision", "name", name)
			continue
		}
		if err := r.Delete(ctx, ex); err != nil {
			log.Error(err, "Failed to delete stale PackageRevision", "name", name)
		}
	}
}

// applyPackageRevision applies repo-controller-owned spec and status fields
// via SSA with ForceOwnership. Includes: identity, labels, ownerRef, locks, deployment, and lifecycle.
// Lifecycle is determined from git state and set by the repo controller on initial creation.
func (r *RepositoryReconciler) applyPackageRevision(ctx context.Context, pr *porchv1alpha2.PackageRevision) error {
	log := log.FromContext(ctx)

	savedStatus := pr.Status

	opts := []client.PatchOption{client.FieldOwner(fieldManagerRepoController), client.ForceOwnership}
	if err := r.Patch(ctx, pr, client.Apply, opts...); err != nil {
		log.Error(err, "Failed to apply PackageRevision", "name", pr.Name)
		return err
	}

	statusObj := &porchv1alpha2.PackageRevision{
		TypeMeta:   pr.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{Name: pr.Name, Namespace: pr.Namespace},
		Status:     savedStatus,
	}
	statusOpts := []client.SubResourcePatchOption{client.FieldOwner(fieldManagerRepoController), client.ForceOwnership}
	if err := r.Status().Patch(ctx, statusObj, client.Apply, statusOpts...); err != nil {
		log.Error(err, "Failed to apply PackageRevision status", "name", pr.Name)
		return err
	}

	return nil
}

// applySeedFields seeds Kptfile-derived fields (packageMetadata, readinessGates, labels,
// annotations, publish metadata) via a dedicated field manager. Idempotent: the spec/status
// appliers skip their patch when the fields already match. During migration it also enriches
// with v1alpha1 PR metadata from the aggregated API.
func (r *RepositoryReconciler) applySeedFields(ctx context.Context, repo *configapi.Repository, pkgRev repository.PackageRevision, crd *porchv1alpha2.PackageRevision) {
	log := log.FromContext(ctx)

	lifecycle := porchv1alpha2.PackageRevisionLifecycle(pkgRev.Lifecycle(ctx))
	kf, err := pkgRev.GetKptfile(ctx)
	if err != nil {
		// Proceed with an empty Kptfile; seed fields degrade to defaults.
		log.V(3).Info("Could not read Kptfile for seeding", "name", crd.Name, "err", err)
	}

	desiredReadinessGates := porchv1alpha2.KptfileToReadinessGates(kf)
	desiredPackageMetadata := porchv1alpha2.KptfileToPackageMetadata(kf)
	desiredSourceLabels, desiredSourceAnnotations := sourceMetadata(pkgRev)

	if isMigrating(repo) {
		v1Labels, v1Annos := r.enrichMetadataFromV1Alpha1(ctx, repo, pkgRev.KubeObjectName())
		desiredSourceLabels = mergeMetadata(desiredSourceLabels, v1Labels)
		desiredSourceAnnotations = mergeMetadata(desiredSourceAnnotations, v1Annos)
	}

	r.applySeedSpec(ctx, crd, desiredReadinessGates, desiredPackageMetadata, desiredSourceLabels, desiredSourceAnnotations)
	r.applySeedStatus(ctx, crd, lifecycle, pkgRev, kf)
}

// applySeedSpec applies seed spec fields via SSA, skipping the patch when already up-to-date.
func (r *RepositoryReconciler) applySeedSpec(ctx context.Context, crd *porchv1alpha2.PackageRevision,
	desiredReadinessGates []porchv1alpha2.ReadinessGate, desiredPackageMetadata *porchv1alpha2.PackageMetadata,
	desiredSourceLabels, desiredSourceAnnotations map[string]string) {
	log := log.FromContext(ctx)

	if specFieldsUpToDate(crd, desiredReadinessGates, desiredPackageMetadata, desiredSourceLabels, desiredSourceAnnotations) {
		log.V(5).Info("Seed spec fields unchanged, skipping spec patch", "name", crd.Name)
		return
	}

	seedObjMeta := metav1.ObjectMeta{Name: crd.Name, Namespace: crd.Namespace}
	objMetaLabels := r.buildSeedObjectLabels(desiredSourceLabels, desiredPackageMetadata)
	if len(objMetaLabels) > 0 {
		seedObjMeta.Labels = objMetaLabels
	}
	if len(desiredSourceAnnotations) > 0 {
		seedObjMeta.Annotations = desiredSourceAnnotations
	}

	seedSpec := &porchv1alpha2.PackageRevision{
		TypeMeta:   crd.TypeMeta,
		ObjectMeta: seedObjMeta,
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle:       porchv1alpha2.PackageRevisionLifecycle(crd.Spec.Lifecycle),
			ReadinessGates:  desiredReadinessGates,
			PackageMetadata: desiredPackageMetadata,
		},
	}
	if err := r.Patch(ctx, seedSpec, client.Apply, client.FieldOwner(fieldManagerRepoControllerSeed)); err != nil {
		log.Info("Seed spec apply failed", "name", crd.Name, "err", err)
	}
}

// buildSeedObjectLabels merges source labels with promoted packageMetadata labels.
// packageMetadata labels are promoted with porch.kpt.dev/kptfile-label__ prefix,
// matching the PR controller's post-render behavior for immediate kubectl -l queryability.
func (r *RepositoryReconciler) buildSeedObjectLabels(desiredSourceLabels map[string]string, desiredPackageMetadata *porchv1alpha2.PackageMetadata) map[string]string {
	objMetaLabels := make(map[string]string)
	for k, v := range desiredSourceLabels {
		objMetaLabels[k] = v
	}
	if desiredPackageMetadata != nil && len(desiredPackageMetadata.Labels) > 0 {
		for k, v := range desiredPackageMetadata.Labels {
			// Escape "/" as "__" to fit Kubernetes label key constraints.
			escapedKey := "porch.kpt.dev/kptfile-label__" + strings.ReplaceAll(k, "/", "__")
			objMetaLabels[escapedKey] = v
		}
	}
	return objMetaLabels
}

// applySeedStatus applies seed status fields via SSA with a dedicated field manager.
// Skips the patch if fields already match the desired state (optimization).
func (r *RepositoryReconciler) applySeedStatus(ctx context.Context, crd *porchv1alpha2.PackageRevision, lifecycle porchv1alpha2.PackageRevisionLifecycle,
	pkgRev repository.PackageRevision, kf kptfilev1.KptFile) {
	log := log.FromContext(ctx)

	desiredPackageConditions := porchv1alpha2.KptfileToPackageConditions(kf)
	desiredRevision, desiredPublishedBy, desiredPublishedAt := r.buildPublishMetadata(lifecycle, pkgRev)

	if statusFieldsUpToDate(crd, desiredPackageConditions, desiredRevision, desiredPublishedBy, desiredPublishedAt) {
		log.V(5).Info("Seed status fields unchanged, skipping status patch", "name", crd.Name)
		return
	}

	seedStatus := porchv1alpha2.PackageRevisionStatus{
		PackageConditions: desiredPackageConditions,
		Revision:          desiredRevision,
		PublishedBy:       desiredPublishedBy,
		PublishedAt:       desiredPublishedAt,
	}
	statusObj := &porchv1alpha2.PackageRevision{
		TypeMeta:   crd.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{Name: crd.Name, Namespace: crd.Namespace},
		Status:     seedStatus,
	}
	if err := r.Status().Patch(ctx, statusObj, client.Apply, client.FieldOwner(fieldManagerRepoControllerSeed)); err != nil {
		log.V(3).Info("Seed status apply skipped (fields likely owned by PR controller)", "name", crd.Name, "err", err)
	}
}

// buildPublishMetadata extracts publish metadata (revision, author, timestamp) for published packages.
func (r *RepositoryReconciler) buildPublishMetadata(lifecycle porchv1alpha2.PackageRevisionLifecycle, pkgRev repository.PackageRevision) (int, string, *metav1.Time) {
	if !porchv1alpha2.LifecycleIsPublished(lifecycle) {
		return 0, "", nil
	}
	key := pkgRev.Key()
	revision := key.Revision
	commitTime, commitAuthor := pkgRev.GetCommitInfo()
	var publishedAt *metav1.Time
	if !commitTime.IsZero() {
		t := metav1.NewTime(commitTime)
		publishedAt = &t
	}
	return revision, commitAuthor, publishedAt
}

// specFieldsUpToDate returns true if the seed spec fields are already correct.
// Compares ReadinessGates, PackageMetadata, source labels/annotations, and promoted packageMetadata labels against current state.
func specFieldsUpToDate(crd *porchv1alpha2.PackageRevision, desiredGates []porchv1alpha2.ReadinessGate, desiredMeta *porchv1alpha2.PackageMetadata, desiredSourceLabels, desiredSourceAnnotations map[string]string) bool {
	// Check ReadinessGates match.
	if !equality.Semantic.DeepEqual(crd.Spec.ReadinessGates, desiredGates) {
		return false
	}

	// Check PackageMetadata match using shared utility.
	if !porchv1alpha2.PackageMetadataEqual(crd.Spec.PackageMetadata, desiredMeta) {
		return false
	}

	// Build expected object.metadata.labels: source labels + promoted packageMetadata labels
	// packageMetadata labels are promoted with porch.kpt.dev/kptfile-label__ prefix
	expectedObjLabels := make(map[string]string)
	maps.Copy(expectedObjLabels, desiredSourceLabels)
	// Promote packageMetadata labels with the porch.kpt.dev/kptfile-label__ prefix
	if desiredMeta != nil && len(desiredMeta.Labels) > 0 {
		for k, v := range desiredMeta.Labels {
			// Escape "/" as "__" to fit Kubernetes label key constraints.
			escapedKey := "porch.kpt.dev/kptfile-label__" + strings.ReplaceAll(k, "/", "__")
			expectedObjLabels[escapedKey] = v
		}
	}

	// Get current object.metadata labels (excluding system labels managed by repo controller)
	currentObjLabels := filterSystemLabels(crd.Labels)

	// Check that object.metadata.labels (excluding system labels) match expected
	if !maps.Equal(currentObjLabels, expectedObjLabels) {
		return false
	}

	// Check source annotations match.
	if !maps.Equal(crd.Annotations, desiredSourceAnnotations) {
		return false
	}

	return true
}

// statusFieldsUpToDate returns true if the seed status fields are already correct.
// Compares PackageConditions, Revision, PublishedBy, and PublishedAt against current state.
func statusFieldsUpToDate(crd *porchv1alpha2.PackageRevision, desiredConditions []porchv1alpha2.PackageCondition, desiredRevision int, desiredPublishedBy string, desiredPublishedAt *metav1.Time) bool {
	// Check PackageConditions match.
	if !equality.Semantic.DeepEqual(crd.Status.PackageConditions, desiredConditions) {
		return false
	}

	// Check Revision match.
	if crd.Status.Revision != desiredRevision {
		return false
	}

	// Check PublishedBy match.
	if crd.Status.PublishedBy != desiredPublishedBy {
		return false
	}

	// Check PublishedAt match using shared utility.
	if !porchv1alpha2.TimesEqual(crd.Status.PublishedAt, desiredPublishedAt) {
		return false
	}

	return true
}

// filterSystemLabels returns all labels except system-managed ones.
func filterSystemLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	filtered := make(map[string]string)
	for k, v := range labels {
		if !systemLabels[k] {
			filtered[k] = v
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// packageRevisionUpToDate returns true if the repo-controller-owned fields
// in the existing resource match the desired state. Skips immutable identity
// fields (package name, repo, workspace) and fields owned by other controllers
// (lifecycle, conditions, publish metadata).
func packageRevisionUpToDate(existing, desired *porchv1alpha2.PackageRevision) bool {
	return repoOwnedLabelsMatch(existing.Labels, desired.Labels) &&
		existing.Status.Deployment == desired.Status.Deployment &&
		resourcesSizeBytesUpToDate(existing.Status.ResourcesSizeBytes, desired.Status.ResourcesSizeBytes) &&
		equality.Semantic.DeepEqual(existing.Status.UpstreamLock, desired.Status.UpstreamLock) &&
		equality.Semantic.DeepEqual(existing.Status.SelfLock, desired.Status.SelfLock)
}

// repoOwnedLabelsMatch returns true if the labels the repo controller owns
// (repository label and latest-revision) match between existing and desired.
// Labels owned by other controllers or users are ignored.
func repoOwnedLabelsMatch(existing, desired map[string]string) bool {
	if existing[RepositoryLabel] != desired[RepositoryLabel] {
		return false
	}
	return existing[porchv1alpha2.LatestPackageRevisionKey] == desired[porchv1alpha2.LatestPackageRevisionKey]
}

// isSeeded reports whether applySeedFields has already run for this package.
// Seeding is the only path that sets Status.Revision, and Published packages
// always get a revision >= 1, so a recorded revision means seeding is done.
func isSeeded(pr *porchv1alpha2.PackageRevision) bool {
	return porchv1alpha2.LifecycleIsPublished(porchv1alpha2.PackageRevisionLifecycle(pr.Spec.Lifecycle)) &&
		pr.Status.Revision > 0
}

// resourcesSizeBytesUpToDate returns true if the size field doesn't need updating.
// When desired is 0 (size couldn't be computed, e.g. GetResources failed), we
// treat the existing value as up-to-date to avoid patch churn.
func resourcesSizeBytesUpToDate(existing, desired int64) bool {
	if desired == 0 {
		return true
	}
	return existing == desired
}

// buildPackageRevision constructs a PackageRevision resource containing
// repo-controller-owned fields: identity, labels, ownerRef, locks, deployment, and lifecycle.
// Lifecycle is determined from git state for discovered packages.
// Other seed fields (ReadinessGates, PackageMetadata, publish metadata) are applied
// separately via applySeedFields on create.
func buildPackageRevision(ctx context.Context, repo *configapi.Repository, pkgRev repository.PackageRevision, isLatest bool) (*porchv1alpha2.PackageRevision, error) {
	key := pkgRev.Key()
	_, upstreamLock, _ := pkgRev.GetUpstreamLock(ctx)
	_, selfLock, _ := pkgRev.GetLock(ctx)

	status := porchv1alpha2.PackageRevisionStatus{
		UpstreamLock: porchv1alpha2.KptLocatorToLocator(upstreamLock),
		SelfLock:     porchv1alpha2.KptLocatorToLocator(selfLock),
		Deployment:   repo.Spec.Deployment,
		// PackageConditions omitted — PR controller owns after first render.
	}

	// Calculate resource size for status.resourcesSizeBytes.
	if prr, err := pkgRev.GetResources(ctx); err == nil && prr != nil && prr.Spec.Resources != nil {
		status.ResourcesSizeBytes = repository.CalculateResourcesSize(prr.Spec.Resources)
	}

	crd := &porchv1alpha2.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pkgRev.KubeObjectName(),
			Namespace: pkgRev.KubeObjectNamespace(),
			Labels:    packageRevisionLabelsWithLatest(repo.Name, isLatest),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         configapi.GroupVersion.Identifier(),
					Kind:               configapi.TypeRepository.Kind,
					Name:               repo.Name,
					UID:                repo.UID,
					Controller:         new(true),
					BlockOwnerDeletion: new(true),
				},
			},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    key.PkgKey.ToPkgPathname(),
			RepositoryName: key.RKey().Name,
			WorkspaceName:  key.WorkspaceName,
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycle(pkgRev.Lifecycle(ctx)),
			// ReadinessGates and PackageMetadata omitted — PR controller owns after first render.
		},
		Status: status,
	}

	return crd, nil
}

// computeLatestRevisions determines which revision is the latest (highest
// revision number among Published packages) for each package, and returns
// a map from CRD object name to whether it is the latest.
func computeLatestRevisions(ctx context.Context, pkgRevs []repository.PackageRevision) map[string]bool {
	type candidate struct {
		name     string
		revision int
	}
	// Group by package pathname and find highest revision per package.
	latestPerPkg := make(map[string]candidate)
	for _, pkgRev := range pkgRevs {
		key := pkgRev.Key()
		lifecycle := pkgRev.Lifecycle(ctx)
		if !porchv1alpha2.LifecycleIsPublished(porchv1alpha2.PackageRevisionLifecycle(lifecycle)) {
			continue
		}
		if key.Revision <= 0 {
			continue
		}
		pkgPath := key.PkgKey.ToPkgPathname()
		if c, exists := latestPerPkg[pkgPath]; !exists || key.Revision > c.revision {
			latestPerPkg[pkgPath] = candidate{name: pkgRev.KubeObjectName(), revision: key.Revision}
		}
	}

	result := make(map[string]bool, len(pkgRevs))
	for _, c := range latestPerPkg {
		result[c.name] = true
	}
	return result
}

// packageRevisionLabelsWithLatest returns the standard labels for a
// PackageRevision, including the repository label and the latest-revision
// indicator. The repo controller owns this label via SSA and keeps it
// accurate across syncs. The PR controller may also update it on lifecycle
// transitions via MergePatch (publish, delete).
func packageRevisionLabelsWithLatest(repoName string, isLatest bool) map[string]string {
	labels := map[string]string{
		RepositoryLabel: repoName,
	}
	if isLatest {
		labels[porchv1alpha2.LatestPackageRevisionKey] = porchv1alpha2.LatestPackageRevisionValue
	} else {
		labels[porchv1alpha2.LatestPackageRevisionKey] = "false"
	}
	return labels
}

// packageRevisionLabels returns the standard labels for a PackageRevision,
// including the repository label and the latest-revision indicator.
// Used in tests only.
func packageRevisionLabels(repoName string, pkgRev repository.PackageRevision) map[string]string {
	labels := map[string]string{
		RepositoryLabel: repoName,
	}
	if pkgRev.IsLatestRevision() {
		labels[porchv1alpha2.LatestPackageRevisionKey] = porchv1alpha2.LatestPackageRevisionValue
	} else {
		labels[porchv1alpha2.LatestPackageRevisionKey] = "false"
	}
	return labels
}

// systemLabels are labels managed by controllers that should not be carried
// over as source metadata. Includes both v1alpha1 and v1alpha2 variants.
var systemLabels = map[string]bool{
	porchv1alpha2.RepositoryLabelKey:       true,
	porchv1alpha2.LatestPackageRevisionKey: true,
	"kpt.dev/latest-revision":              true, // v1alpha1 variant
}

// sourceMetadata extracts labels and annotations from the source package
// revision's Kubernetes ObjectMeta. System-managed labels are excluded.
func sourceMetadata(pkgRev repository.PackageRevision) (map[string]string, map[string]string) {
	meta := pkgRev.GetMeta()

	var labels map[string]string
	for k, v := range meta.Labels {
		if systemLabels[k] {
			continue
		}
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[k] = v
	}

	var annotations map[string]string
	for k, v := range meta.Annotations {
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[k] = v
	}

	return labels, annotations
}

// keepPublishedLifecycle keeps the existing lifecycle when both it and the lifecycle
// derived from git are Published or DeletionProposed.
func keepPublishedLifecycle(existing, desired *porchv1alpha2.PackageRevision) {
	if porchv1alpha2.LifecycleIsPublished(existing.Spec.Lifecycle) && porchv1alpha2.LifecycleIsPublished(desired.Spec.Lifecycle) {
		desired.Spec.Lifecycle = existing.Spec.Lifecycle
	}
}
