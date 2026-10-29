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
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchkey "github.com/kptdev/porch/api/porch/key"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	pkgerrors "github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// fieldManagerPRControllerMain is the SSA field manager for the "main" placeholder CR.
const fieldManagerPRControllerMain = "packagerev-controller-main"

// fieldManagerPRControllerMainMeta owns only the main CR's spec.packageMetadata
// and spec.readinessGates, so refreshing them to track the latest published
// revision never stomps the user-owned spec.lifecycle.
const fieldManagerPRControllerMainMeta = "packagerev-controller-main-meta"

// createMainPackageRevision creates or updates the "main" branch placeholder CR
// (Revision -1) when a workspace is published, mirroring v1alpha1 but driven by
// the controller rather than repository sync. Source is nil (repo-discovered).
func (r *PackageRevisionReconciler) createMainPackageRevision(
	ctx context.Context,
	publishedPR *porchv1alpha2.PackageRevision,
	repoKey porchkey.RepositoryKey,
) error {
	log := log.FromContext(ctx)

	mainContent, err := r.ContentAccess.GetMainPackageContent(ctx, repoKey, publishedPR.Spec.PackageName, publishedPR.Spec.WorkspaceName)
	if err != nil {
		return err
	}

	key := mainContent.Key()
	name := porchkey.ComposePkgRevObjName(key)
	if name == "" {
		log.Info("skipping main revision: could not compose object name",
			"package", publishedPR.Spec.PackageName, "workspace", publishedPR.Spec.WorkspaceName)
		return nil
	}

	// Best-effort ownerReference: proceed without it if the repo can't be read.
	repo := &configapi.Repository{}
	var ownerRefs []metav1.OwnerReference
	if err := r.Get(ctx, types.NamespacedName{Namespace: publishedPR.Namespace, Name: publishedPR.Spec.RepositoryName}, repo); err != nil {
		log.V(3).Info("could not get owner repository for main revision ownerRef", "error", err.Error())
	} else {
		controller := true
		blockOwnerDeletion := true
		ownerRefs = []metav1.OwnerReference{{
			APIVersion:         configapi.GroupVersion.Identifier(),
			Kind:               configapi.TypeRepository.Kind,
			Name:               repo.Name,
			UID:                repo.UID,
			Controller:         &controller,
			BlockOwnerDeletion: &blockOwnerDeletion,
		}}
	}

	// patchMainStatus force-applies these under a stable field manager, so a
	// discarded read error would blank out valid locks it previously owned. Return
	// the error so the caller requeues instead of persisting empty locks.
	_, upstreamLock, err := mainContent.GetUpstreamLock(ctx)
	if err != nil {
		return pkgerrors.Wrap(err, "unable to read main PackageRevision upstream lock")
	}
	_, selfLock, err := mainContent.GetLock(ctx)
	if err != nil {
		return pkgerrors.Wrap(err, "unable to read main PackageRevision self lock")
	}
	commitTime, commitAuthor := mainContent.GetCommitInfo()

	resources, err := mainContent.GetResourceContents(ctx)
	if err != nil {
		return pkgerrors.Wrap(err, "unable to read main PackageRevision resources")
	}
	sizeBytes := repository.CalculateResourcesSize(resources)

	// spec.lifecycle on the main CR is user-owned once it exists: only create when
	// absent, never re-assert Published (a user propose-delete must stick).
	existing := &porchv1alpha2.PackageRevision{}
	getErr := r.Get(ctx, types.NamespacedName{Namespace: publishedPR.Namespace, Name: name}, existing)
	switch {
	case getErr == nil:
		// Leave untouched if deleting or no longer Published.
		if !existing.DeletionTimestamp.IsZero() ||
			existing.Spec.Lifecycle != porchv1alpha2.PackageRevisionLifecyclePublished {
			log.V(4).Info("main PackageRevision exists and is not Published; leaving untouched",
				"name", name, "lifecycle", existing.Spec.Lifecycle)
			return nil
		}
		// Published: refresh HEAD-tracking status fields and the package
		// metadata/readiness gates to track the latest published revision
		// (mirrors v1alpha1, which re-derives these on every publish).
		if err := r.patchMainSpecMetadata(ctx, publishedPR.Namespace, name, publishedPR); err != nil {
			return pkgerrors.Wrapf(err, "unable to update main PackageRevision metadata %s/%s", publishedPR.Namespace, name)
		}
		if err := r.patchMainStatus(ctx, publishedPR.Namespace, name, key.Revision, commitAuthor, commitTime, upstreamLock, selfLock, repo.Spec.Deployment, sizeBytes); err != nil {
			return pkgerrors.Wrapf(err, "unable to update main PackageRevision status %s/%s", publishedPR.Namespace, name)
		}
		return nil

	case apierrors.IsNotFound(getErr):
		// Create as Published with nil Source (repo-discovered).
		newMain := &porchv1alpha2.PackageRevision{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PackageRevision",
				APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: publishedPR.Namespace,
				Labels: map[string]string{
					porchv1alpha2.RepositoryLabelKey: key.RKey().Name,
					// The main revision is never the "latest" numbered revision.
					porchv1alpha2.LatestPackageRevisionKey: "false",
				},
				OwnerReferences: ownerRefs,
			},
			Spec: porchv1alpha2.PackageRevisionSpec{
				PackageName:    key.PKey().ToPkgPathname(),
				RepositoryName: key.RKey().Name,
				WorkspaceName:  key.WorkspaceName,
				Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
				// Carry package metadata and readiness gates from the published
				// revision the main HEAD now tracks (mirrors v1alpha1's
				// ToMainPackageRevision, which copies these onto the -1 placeholder).
				PackageMetadata: publishedPR.Spec.PackageMetadata.DeepCopy(),
				ReadinessGates:  append([]porchv1alpha2.ReadinessGate(nil), publishedPR.Spec.ReadinessGates...),
			},
		}
		if err := r.Create(ctx, newMain); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return nil // lost a create race; fine
			}
			return pkgerrors.Wrapf(err, "unable to create main PackageRevision %s/%s", publishedPR.Namespace, name)
		}
		if err := r.patchMainStatus(ctx, publishedPR.Namespace, name, key.Revision, commitAuthor, commitTime, upstreamLock, selfLock, repo.Spec.Deployment, sizeBytes); err != nil {
			return pkgerrors.Wrapf(err, "unable to update main PackageRevision status %s/%s", publishedPR.Namespace, name)
		}
		log.V(2).Info("created main PackageRevision on publish", "name", name, "package", key.PKey().ToPkgPathname())
		return nil

	default:
		return pkgerrors.Wrapf(getErr, "unable to look up main PackageRevision %s/%s", publishedPR.Namespace, name)
	}
}

// patchMainStatus refreshes the main CR's HEAD-tracking status via SSA, leaving
// the user-owned spec.lifecycle untouched.
func (r *PackageRevisionReconciler) patchMainStatus(
	ctx context.Context,
	namespace, name string,
	revision int,
	commitAuthor string,
	commitTime time.Time,
	upstreamLock, selfLock kptfilev1.Locator,
	deployment bool,
	sizeBytes int64,
) error {
	status := porchv1alpha2.PackageRevisionStatus{
		Revision:           revision,
		PublishedBy:        commitAuthor,
		UpstreamLock:       porchv1alpha2.KptLocatorToLocator(upstreamLock),
		SelfLock:           porchv1alpha2.KptLocatorToLocator(selfLock),
		Deployment:         deployment,
		ResourcesSizeBytes: sizeBytes,
	}
	if !commitTime.IsZero() {
		t := metav1.NewTime(commitTime)
		status.PublishedAt = &t
	}

	statusObj := &porchv1alpha2.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status:     status,
	}
	return r.Status().Patch(ctx, statusObj, client.Apply,
		client.FieldOwner(fieldManagerPRControllerMain), client.ForceOwnership)
}

// patchMainSpecMetadata refreshes the main CR's spec.packageMetadata and
// spec.readinessGates to track the latest published revision, via an SSA apply
// scoped to a dedicated field manager. It deliberately omits spec.lifecycle so
// the user-owned lifecycle (e.g. a propose-delete) is never stomped.
func (r *PackageRevisionReconciler) patchMainSpecMetadata(
	ctx context.Context,
	namespace, name string,
	publishedPR *porchv1alpha2.PackageRevision,
) error {
	applyObj := &porchv1alpha2.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageMetadata: publishedPR.Spec.PackageMetadata.DeepCopy(),
			ReadinessGates:  append([]porchv1alpha2.ReadinessGate(nil), publishedPR.Spec.ReadinessGates...),
		},
	}
	return r.Patch(ctx, applyObj, client.Apply,
		client.FieldOwner(fieldManagerPRControllerMainMeta), client.ForceOwnership)
}
