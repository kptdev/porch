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
	"fmt"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// handleDeletion gates deletion of Published packages: they must be
// DeletionProposed first, unless the owner Repository is already gone
// (GC cascade). For all other lifecycles, git refs are cleaned up before
// the finalizer is removed.
func (r *PackageRevisionReconciler) handleDeletion(ctx context.Context, pr *porchv1alpha2.PackageRevision) (*ctrl.Result, error) {
	if pr.Spec.Lifecycle == porchv1alpha2.PackageRevisionLifecyclePublished {
		repoGone, err := r.ownerRepoGone(ctx, pr)
		if err != nil {
			return nil, fmt.Errorf("failed to check owner repository: %w", err)
		}
		if !repoGone {
			// Validating webhook rejects DELETE on published packages (unless cascade deletion),
			// so this is unreachable in normal operation. Safety net for edge cases.
			log.FromContext(ctx).Info("blocking deletion: published package must be DeletionProposed first", "lifecycle", pr.Spec.Lifecycle)
			return &ctrl.Result{}, nil
		}
		// Repo is gone — best-effort git cleanup, don't block on failure
		// since all sibling revisions are being garbage-collected too.
		if err := r.deleteFromGit(ctx, pr); err != nil {
			log.FromContext(ctx).Error(err, "best-effort git cleanup failed during GC cascade")
		}
		return r.removeFinalizer(ctx, pr, false)
	}

	// Delete git refs before removing the finalizer. If this fails the
	// finalizer stays and the controller retries on the next reconcile.
	if err := r.deleteFromGit(ctx, pr); err != nil {
		return nil, fmt.Errorf("failed to delete package from git: %w", err)
	}
	return r.removeFinalizer(ctx, pr, true)
}

// deleteFromGit removes the package's git refs (tags, branches) via the
// shared content cache. "Not found" errors are treated as success — there
// is nothing to clean up if the package or repo doesn't exist in the cache.
func (r *PackageRevisionReconciler) deleteFromGit(ctx context.Context, pr *porchv1alpha2.PackageRevision) error {
	repoKey := repository.RepositoryKey{
		Namespace: pr.Namespace,
		Name:      pr.Spec.RepositoryName,
	}
	err := r.ContentCache.DeletePackage(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName)
	if repository.IsNotFoundError(err) {
		log.FromContext(ctx).Info("package not found in git, nothing to clean up")
		return nil
	}
	return err
}

func (r *PackageRevisionReconciler) removeFinalizer(ctx context.Context, pr *porchv1alpha2.PackageRevision, updateLabels bool) (*ctrl.Result, error) {
	patch := client.MergeFrom(pr.DeepCopy())
	if controllerutil.RemoveFinalizer(pr, porchv1alpha2.PackageRevisionFinalizer) {
		if err := r.Patch(ctx, pr, patch); client.IgnoreNotFound(err) != nil {
			return nil, fmt.Errorf("failed to remove finalizer: %w", err)
		}
	}

	if updateLabels {
		r.updateLatestRevisionLabels(ctx, pr)
	}

	return &ctrl.Result{}, nil
}

// ensureFinalizerAndOwner adds the finalizer and Repository ownerReference
// if not already present, in a single patch.
func (r *PackageRevisionReconciler) ensureFinalizerAndOwner(ctx context.Context, pr *porchv1alpha2.PackageRevision) error {
	patch := client.MergeFrom(pr.DeepCopy())
	needsPatch := controllerutil.AddFinalizer(pr, porchv1alpha2.PackageRevisionFinalizer)

	if !hasOwnerReference(pr, pr.Spec.RepositoryName) {
		if err := r.setOwnerReference(ctx, pr); err != nil {
			log.FromContext(ctx).Error(err, "failed to set owner reference")
		} else {
			needsPatch = true
		}
	}

	if needsPatch {
		if err := r.Patch(ctx, pr, patch); err != nil {
			return fmt.Errorf("failed to patch finalizer/ownerReference: %w", err)
		}
	}
	return nil
}

func hasOwnerReference(pr *porchv1alpha2.PackageRevision, repoName string) bool {
	for _, ref := range pr.OwnerReferences {
		if ref.Kind == configapi.TypeRepository.Kind && ref.Name == repoName {
			if ref.APIVersion != configapi.GroupVersion.Identifier() {
				// Wrong apiVersion — needs replacement.
				continue
			}
			if ref.UID == "" {
				// Missing UID — needs replacement.
				continue
			}
			if ref.Controller != nil && *ref.Controller && ref.BlockOwnerDeletion != nil && *ref.BlockOwnerDeletion {
				return true
			}
			// ownerRef exists but missing Controller/BlockOwnerDeletion — needs update.
			// Continue checking in case a complete ref exists later in the slice.
		}
	}
	return false
}

func (r *PackageRevisionReconciler) setOwnerReference(ctx context.Context, pr *porchv1alpha2.PackageRevision) error {
	var repo configapi.Repository
	if err := r.Get(ctx, types.NamespacedName{Namespace: pr.Namespace, Name: pr.Spec.RepositoryName}, &repo); err != nil {
		return err
	}
	controller := true
	blockOwnerDeletion := true
	desired := metav1.OwnerReference{
		APIVersion:         configapi.GroupVersion.Identifier(),
		Kind:               configapi.TypeRepository.Kind,
		Name:               repo.Name,
		UID:                repo.UID,
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}
	// Replace existing incomplete ref or append new one.
	for i, ref := range pr.OwnerReferences {
		if ref.Kind == configapi.TypeRepository.Kind && ref.Name == repo.Name {
			pr.OwnerReferences[i] = desired
			return nil
		}
	}
	pr.OwnerReferences = append(pr.OwnerReferences, desired)
	return nil
}

// ownerRepoGone returns true only when the Repository is confirmed deleted
// (NotFound). Transient errors are returned so the caller can retry.
func (r *PackageRevisionReconciler) ownerRepoGone(ctx context.Context, pr *porchv1alpha2.PackageRevision) (bool, error) {
	var repo configapi.Repository
	err := r.Get(ctx, types.NamespacedName{Namespace: pr.Namespace, Name: pr.Spec.RepositoryName}, &repo)
	if err == nil {
		return false, nil
	}
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	return false, err
}
