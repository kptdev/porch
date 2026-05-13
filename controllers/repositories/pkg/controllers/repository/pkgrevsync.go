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
	RepositoryLabel                = "porch.kpt.dev/repository"
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

	for _, pkgRev := range pkgRevs {
		name := pkgRev.KubeObjectName()
		ex, isUpdate := existingByName[name]

		desired, err := buildPackageRevision(ctx, repo, pkgRev)
		if err != nil {
			log.Error(err, "Failed to build PackageRevision", "key", pkgRev.Key())
			continue
		}
		desiredNames[desired.Name] = true

		if isUpdate && packageRevisionUpToDate(ex, desired) {
			log.V(5).Info("PackageRevision unchanged, skipping", "name", desired.Name)
			continue
		}

		if err := r.applyPackageRevision(ctx, desired); err != nil {
			continue
		}
		if !isUpdate {
			r.applySeedFields(ctx, repo, pkgRev, desired)
		}
		if isUpdate {
			log.V(3).Info("Updated PackageRevision", "name", desired.Name)
		} else {
			log.V(3).Info("Created PackageRevision", "name", desired.Name)
		}
	}

	return desiredNames
}

func (r *RepositoryReconciler) deleteStalePackageRevisions(ctx context.Context, existingByName map[string]*porchv1alpha2.PackageRevision, desiredNames map[string]bool) {
	log := log.FromContext(ctx)
	for name, ex := range existingByName {
		if !desiredNames[name] {
			if err := r.Delete(ctx, ex); err != nil {
				log.Error(err, "Failed to delete stale PackageRevision", "name", name)
			}
		}
	}
}

// applyPackageRevision applies repo-controller-owned spec and status fields
// via SSA with ForceOwnership. Only includes fields the repo controller
// permanently owns: identity, labels, ownerRef, locks, deployment.
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

// applySeedFields applies non-repo-controller-owned fields (lifecycle, revision,
// Kptfile-derived, publish metadata) on initial creation only. Uses a
// separate field manager without ForceOwnership so these fields seed the value
// for discovered packages but never overwrite values already set by the user
// or the PR controller.
func (r *RepositoryReconciler) applySeedFields(ctx context.Context, repo *configapi.Repository, pkgRev repository.PackageRevision, crd *porchv1alpha2.PackageRevision) {
	log := log.FromContext(ctx)

	lifecycle := porchv1alpha2.PackageRevisionLifecycle(pkgRev.Lifecycle(ctx))
	kf, _ := pkgRev.GetKptfile(ctx)

	seedSpec := &porchv1alpha2.PackageRevision{
		TypeMeta:   crd.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{Name: crd.Name, Namespace: crd.Namespace},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle:       lifecycle,
			ReadinessGates:  porchv1alpha2.KptfileToReadinessGates(kf),
			PackageMetadata: porchv1alpha2.KptfileToPackageMetadata(kf),
		},
	}
	if err := r.Patch(ctx, seedSpec, client.Apply, client.FieldOwner(fieldManagerRepoControllerSeed)); err != nil {
		log.V(3).Info("Seed spec apply skipped (fields likely already owned)", "name", crd.Name, "err", err)
	}

	seedStatus := porchv1alpha2.PackageRevisionStatus{
		PackageConditions: porchv1alpha2.KptfileToPackageConditions(kf),
	}
	if porchv1alpha2.LifecycleIsPublished(lifecycle) {
		key := pkgRev.Key()
		seedStatus.Revision = key.Revision
		commitTime, commitAuthor := pkgRev.GetCommitInfo()
		seedStatus.PublishedBy = commitAuthor
		if !commitTime.IsZero() {
			t := metav1.NewTime(commitTime)
			seedStatus.PublishedAt = &t
		}
	}
	statusObj := &porchv1alpha2.PackageRevision{
		TypeMeta:   crd.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{Name: crd.Name, Namespace: crd.Namespace},
		Status:     seedStatus,
	}
	if err := r.Status().Patch(ctx, statusObj, client.Apply, client.FieldOwner(fieldManagerRepoControllerSeed)); err != nil {
		log.V(3).Info("Seed status apply skipped (fields likely already owned)", "name", crd.Name, "err", err)
	}
}

// packageRevisionUpToDate returns true if the repo-controller-owned fields
// in the existing resource match the desired state. Skips immutable identity
// fields (package name, repo, workspace) and fields owned by other controllers
// (lifecycle, conditions, publish metadata).
func packageRevisionUpToDate(existing, desired *porchv1alpha2.PackageRevision) bool {
	return equality.Semantic.DeepEqual(existing.Labels, desired.Labels) &&
		existing.Status.Deployment == desired.Status.Deployment &&
		equality.Semantic.DeepEqual(existing.Status.UpstreamLock, desired.Status.UpstreamLock) &&
		equality.Semantic.DeepEqual(existing.Status.SelfLock, desired.Status.SelfLock)
}

// buildPackageRevision constructs a PackageRevision resource containing only
// repo-controller-owned fields: identity, labels, ownerRef, locks, deployment.
// Seed fields (lifecycle, publish metadata, Kptfile-derived) are applied
// separately via applySeedFields on create.
func buildPackageRevision(ctx context.Context, repo *configapi.Repository, pkgRev repository.PackageRevision) (*porchv1alpha2.PackageRevision, error) {
	key := pkgRev.Key()
	_, upstreamLock, _ := pkgRev.GetUpstreamLock(ctx)
	_, selfLock, _ := pkgRev.GetLock(ctx)

	status := porchv1alpha2.PackageRevisionStatus{
		UpstreamLock: porchv1alpha2.KptLocatorToLocator(upstreamLock),
		SelfLock:     porchv1alpha2.KptLocatorToLocator(selfLock),
		Deployment:   repo.Spec.Deployment,
		// PackageConditions omitted — PR controller owns after first render.
	}

	crd := &porchv1alpha2.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pkgRev.KubeObjectName(),
			Namespace: pkgRev.KubeObjectNamespace(),
			Labels:    packageRevisionLabelsForUpdate(repo.Name),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: configapi.GroupVersion.Identifier(),
					Kind:       configapi.TypeRepository.Kind,
					Name:       repo.Name,
					UID:        repo.UID,
				},
			},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    key.PkgKey.ToPkgPathname(),
			RepositoryName: key.RKey().Name,
			WorkspaceName:  key.WorkspaceName,
			// ReadinessGates and PackageMetadata omitted — PR controller owns after first render.
		},
		Status: status,
	}

	return crd, nil
}

// packageRevisionLabels returns the standard labels for a PackageRevision,
// including the repository label and the latest-revision indicator.
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

// packageRevisionLabelsForUpdate returns labels for the update path.
// Omits latest-revision — that's PR-controller-owned after initial creation.
func packageRevisionLabelsForUpdate(repoName string) map[string]string {
	return map[string]string{
		RepositoryLabel: repoName,
	}
}
