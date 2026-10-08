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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	kptfileko "github.com/kptdev/krm-functions-sdk/go/fn/kptfileko"
	porchapi "github.com/kptdev/porch/api/porch"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	pkgerrors "github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

// applySubpackageOperation executes the independent subpackage operation and returns the resulting resources.
// Returns nil, nil if no source operation to be applied (subpackage operation already executed).
func (r *PackageRevisionReconciler) applySubpackageOperation(ctx context.Context, pr *porchv1alpha2.PackageRevision) (subpackageResources map[string]string, subpackageOperationType string, err error) {
	if r.shouldSkipSubpackageOperation(pr) {
		return
	}

	if validationErr := porchapi.IsValidSubpackageDir(pr.Spec.SubpackageOperation.SubpackageDir); validationErr != nil {
		err = pkgerrors.Wrapf(validationErr, "specified subpackage directory %q is invalid", pr.Spec.SubpackageOperation.SubpackageDir)
		return
	}

	switch {
	case pr.Spec.SubpackageOperation.CloneFrom != nil:
		subpackageResources, err = r.clonePackage(ctx, pr)
		subpackageOperationType = "subpackage clone"
		return
	case pr.Spec.SubpackageOperation.Upgrade != nil:
		subpackageResources, err = r.upgradePackage(ctx, pr)
		subpackageOperationType = "subpackage upgrade"
		return
	default:
		err = pkgerrors.Errorf("subpackageOperation has no fields set")
		return
	}
}

// upsertSubpackageResourcesInDraft updates the resources of the package revision draft with a clone or an upgrade of an independent subpackage.
func (r *PackageRevisionReconciler) upsertSubpackageResourcesInDraftResources(ctx context.Context, pr *porchv1alpha2.PackageRevision, parentResources, subpackageResources map[string]string) (map[string]string, error) {
	if r.shouldSkipSubpackageOperation(pr) {
		return parentResources, nil
	}

	switch {
	case pr.Spec.SubpackageOperation.CloneFrom != nil:
		return r.insertSubpackageResourcesInDraftResources(ctx, pr, parentResources, subpackageResources)
	case pr.Spec.SubpackageOperation.Upgrade != nil:
		return r.upgradeSubpackageResourcesInDraftResources(ctx, pr, parentResources, subpackageResources)
	default:
		return nil, pkgerrors.Errorf("subpackageOperation has no fields set")
	}
}

// insertSubpackageResourcesInDraftResources adds the resources of the independent subpackage to the parent package revision
// at `SubpackageDir`
func (r *PackageRevisionReconciler) insertSubpackageResourcesInDraftResources(ctx context.Context, pr *porchv1alpha2.PackageRevision, parentResources, subpackageResources map[string]string) (map[string]string, error) {
	subpackageDir := pr.Spec.SubpackageOperation.SubpackageDir

	logger := log.FromContext(ctx)
	logger.V(1).Info("cloning subpackage resources into parent at ", "subpackageDir", subpackageDir)

	for resourceKey := range parentResources {
		if parentSubpackageDir := r.parentSubpackageFound(subpackageDir, resourceKey); parentSubpackageDir != "" {
			return nil, fmt.Errorf("cannot clone subpackage into another subpackage, parent already has a subpackage at %q (requested subpackageDir: %q)", parentSubpackageDir, subpackageDir)
		}

		if resourceKey == subpackageDir || strings.HasPrefix(resourceKey, subpackageDir+"/") {
			return nil, fmt.Errorf("cannot clone subpackage into parent, parent already has content at %q", subpackageDir)
		}
	}

	for subpackageResourceKey, subpackageResourceValue := range subpackageResources {
		parentResources[subpackageDir+"/"+subpackageResourceKey] = subpackageResourceValue
	}

	// Clear the status field from the cloned subpackage's Kptfile, matching v1alpha1 behaviour.
	subpkgKptfileKey := subpackageDir + "/" + kptfilev1.KptFileName
	if kf, err := kptfileko.NewFromPackage(map[string]string{
		kptfilev1.KptFileName: parentResources[subpkgKptfileKey],
	}); err == nil {
		if err := kf.ClearStatus(); err == nil {
			tmp := map[string]string{kptfilev1.KptFileName: ""}
			if err := kf.WriteToPackage(tmp); err == nil {
				parentResources[subpkgKptfileKey] = tmp[kptfilev1.KptFileName]
			}
		}
	}

	logger.V(1).Info("cloned subpackage resources into parent at ", "subpackageDir", subpackageDir)
	return parentResources, nil
}

// upgradeSubpackageResourcesInDraftResources updates the resources of the independent subpackage in the parent package revision
// at `SubpackageDir`
func (r *PackageRevisionReconciler) upgradeSubpackageResourcesInDraftResources(ctx context.Context, pr *porchv1alpha2.PackageRevision, parentResources, subpackageResources map[string]string) (map[string]string, error) {
	subpackageDir := pr.Spec.SubpackageOperation.SubpackageDir

	logger := log.FromContext(ctx)
	logger.V(1).Info("upgrading subpackage resources in parent at ", "subpackageDir", subpackageDir)

	subpackageFound := false
	for resourceKey := range parentResources {
		if resourceKey == subpackageDir {
			return nil, fmt.Errorf("cannot upgrade subpackage in parent, parent already has content at %q", subpackageDir)
		}

		if strings.HasPrefix(resourceKey, subpackageDir+"/") {
			subpackageFound = true
			delete(parentResources, resourceKey)
			continue
		}

		if parentSubpackageDir := r.parentSubpackageFound(subpackageDir, resourceKey); parentSubpackageDir != "" {
			return nil, fmt.Errorf("cannot upgrade subpackage in another subpackage, parent already has a subpackage at %q (requested subpackageDir: %q)", parentSubpackageDir, subpackageDir)
		}

	}

	if !subpackageFound {
		return nil, fmt.Errorf("cannot find subpackage in parent, parent does not have a subpackage at %q", subpackageDir)
	}

	for subpackageResourceKey, subpackageResourceValue := range subpackageResources {
		parentResources[subpackageDir+"/"+subpackageResourceKey] = subpackageResourceValue
	}

	logger.V(1).Info("upgraded subpackage resources in parent at ", "subpackageDir", subpackageDir)
	return parentResources, nil
}

// shouldSkipSubpackageOperation checks if the subpackage operation should be skipped
// Returns true if no operation is specified or if the operation has already been executed
func (r *PackageRevisionReconciler) shouldSkipSubpackageOperation(pr *porchv1alpha2.PackageRevision) bool {
	if pr.Spec.SubpackageOperation == nil {
		return true
	}

	subpackageOperationHash := r.getSubpackageOperationHash(pr)

	if subpackageOperationHash == "" {
		return false
	}

	if subpackageOperationHash == pr.Status.LastSubpackageOperationHash {
		return true
	}

	return false
}

// getSubpackageOperationHash returns a hash of spec.subpackageOperation for idempotency.
// The hash covers the spec struct as stored in the CR — it does not resolve upstream
// content. If upstreamRef points to a draft PackageRevision whose content is mutated
// without changing its name, the hash is unchanged and the operation is skipped.
// To force re-execution, change any field in spec.subpackageOperation (e.g. point
// upstreamRef at a new revision).
func (r *PackageRevisionReconciler) getSubpackageOperationHash(pr *porchv1alpha2.PackageRevision) string {
	if pr.Spec.SubpackageOperation == nil {
		return pr.Status.LastSubpackageOperationHash
	}

	subpackageOperationBytes, err := yaml.Marshal(pr.Spec.SubpackageOperation)
	if err != nil {
		return ""
	}

	subpackageOperationHash := sha256.Sum256(subpackageOperationBytes)
	return "sha256:" + hex.EncodeToString(subpackageOperationHash[:])
}

func (r *PackageRevisionReconciler) parentSubpackageFound(subpackageDir, resourceKey string) string {
	if strings.HasSuffix(resourceKey, kptfilev1.KptFileName) {
		resourceKey = strings.TrimSuffix(resourceKey, "/"+kptfilev1.KptFileName)
	} else {
		return ""
	}

	if subpackageDir == resourceKey || strings.HasPrefix(subpackageDir, resourceKey+"/") {
		return resourceKey
	}

	return ""
}
