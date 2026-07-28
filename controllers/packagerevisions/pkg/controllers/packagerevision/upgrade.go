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
	"strings"

	"github.com/kptdev/kpt/pkg/lib/kptops"
	"github.com/kptdev/porch/api/porch/v1alpha2"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	pkgerrors "github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// upgradePackage performs a 3-way merge between the old upstream, new upstream,
// and current local package, then updates the Kptfile upstream/upstreamLock to
// point at the new upstream.
func (r *PackageRevisionReconciler) upgradePackage(ctx context.Context, pr *porchv1alpha2.PackageRevision) (map[string]string, error) {
	upgrade := r.getUpgrade(pr)

	log := log.FromContext(ctx)
	log.V(1).Info("upgrading package", "oldUpstream", upgrade.OldUpstream.Name,
		"newUpstream", upgrade.NewUpstream.Name, "current", upgrade.CurrentPackage.Name)

	strategy := string(upgrade.Strategy)
	if strategy == "" {
		strategy = string(porchv1alpha2.ResourceMerge)
	}

	// Look up all three package revisions.
	oldUpstreamPR, err := r.getPublishedPackageRevision(ctx, pr.Namespace, upgrade.OldUpstream.Name)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "old upstream")
	}
	newUpstreamPR, err := r.getPublishedPackageRevision(ctx, pr.Namespace, upgrade.NewUpstream.Name)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "new upstream")
	}

	currentPR, err := r.getPackageRevisionForUpgrade(ctx, pr)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "current package")
	}

	// Read content and resources. Retain new upstream content for lock extraction.
	oldUpstreamResources, err := r.getPackageResources(ctx, oldUpstreamPR)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to read old upstream resources")
	}
	newUpstreamContent, newUpstreamResources, err := r.getPackageContentAndResources(ctx, newUpstreamPR)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to read new upstream resources")
	}
	currentResources, err := r.getPackageResourcesForUpgrade(ctx, currentPR)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to read package resources for upgrade")
	}

	// Workaround for kpt bug: fast-forward's hasKfDiff strips Upstream and
	// UpstreamLock but not Status, so the Rendered condition written by kpt
	// render is treated as a local modification. Only strip for fast-forward
	// since other strategies need status for the 3-way merge.
	if strategy == string(porchv1alpha2.FastForward) {
		currentResources = copyResources(currentResources)
		stripKptfileStatus(currentResources)
	}

	// 3-way merge.
	updated, err := (&repository.DefaultPackageUpdater{}).Update(ctx,
		repository.PackageResources{Contents: currentResources},
		repository.PackageResources{Contents: oldUpstreamResources},
		repository.PackageResources{Contents: newUpstreamResources},
		strategy,
	)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "3-way merge failed")
	}

	// Update Kptfile upstream/upstreamLock to point at new upstream.
	newUpstream, newUpstreamLock, err := newUpstreamContent.GetLock(ctx)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to get new upstream lock")
	}
	if err := kptops.UpdateKptfileUpstream(pr.Spec.PackageName, updated.Contents, newUpstream, newUpstreamLock); err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to update Kptfile upstream")
	}

	// Add merge-key comments to newly added resources.
	result, err := ensureMergeKey(updated.Contents)
	if err != nil {
		// Non-fatal — log and return unmodified resources.
		log.V(1).Info("merge-key annotation failed, using unmodified resources")
		result = updated.Contents
	}

	return result, nil
}

// getUpgrade returns the upstream package for a clone in the case of a source upgrade or a subpackage
// operation upgrade
func (r *PackageRevisionReconciler) getUpgrade(pr *porchv1alpha2.PackageRevision) *v1alpha2.PackageUpgradeSpec {
	if pr.Status.CreationSource != "" && pr.Spec.SubpackageOperation != nil && pr.Spec.SubpackageOperation.Upgrade != nil {
		return pr.Spec.SubpackageOperation.Upgrade
	}
	return pr.Spec.Source.Upgrade
}

// getUpgrade returns the upstream package for a clone in the case of a source upgrade or a subpackage
// operation upgrade
func (r *PackageRevisionReconciler) getPackageRevisionForUpgrade(ctx context.Context, pr *porchv1alpha2.PackageRevision) (*porchv1alpha2.PackageRevision, error) {
	if pr.Status.CreationSource != "" && pr.Spec.SubpackageOperation != nil && pr.Spec.SubpackageOperation.Upgrade != nil {
		return r.getDraftPackageRevision(ctx, pr.Namespace, pr.Spec.SubpackageOperation.Upgrade.CurrentPackage.Name)
	}
	return r.getPublishedPackageRevision(ctx, pr.Namespace, pr.Spec.Source.Upgrade.CurrentPackage.Name)
}

// getPackageResources reads the resource contents for a package revision via the cache.
func (r *PackageRevisionReconciler) getPackageResourcesForUpgrade(ctx context.Context, pr *porchv1alpha2.PackageRevision) (map[string]string, error) {
	currentResources, err := r.getPackageResources(ctx, pr)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to read current resources")
	}

	if pr.Status.CreationSource == "" || pr.Spec.SubpackageOperation == nil || pr.Spec.SubpackageOperation.Upgrade == nil {
		return currentResources, nil
	}

	subpackageResources := make(map[string]string)

	for localResourceKey, localResourceValue := range currentResources {
		if strings.HasPrefix(localResourceKey, pr.Spec.SubpackageOperation.SubpackageDir+"/") {
			subpackageResources[strings.TrimPrefix(localResourceKey, pr.Spec.SubpackageOperation.SubpackageDir+"/")] = localResourceValue
		}
	}

	if len(subpackageResources) == 0 {
		return nil, fmt.Errorf("subpackage %q not found in package", pr.Spec.SubpackageOperation.SubpackageDir)
	}

	return subpackageResources, nil
}
