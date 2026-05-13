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

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ensureLatestRevisionLabel sets the latest-revision label to "false" if not already set.
func (r *PackageRevisionReconciler) ensureLatestRevisionLabel(ctx context.Context, pr *porchv1alpha2.PackageRevision) {
	if _, ok := pr.Labels[porchv1alpha2.LatestPackageRevisionKey]; ok {
		return
	}
	patch := client.MergeFrom(pr.DeepCopy())
	if pr.Labels == nil {
		pr.Labels = map[string]string{}
	}
	pr.Labels[porchv1alpha2.LatestPackageRevisionKey] = "false"
	if err := r.Patch(ctx, pr, patch); err != nil {
		log.FromContext(ctx).Error(err, "failed to set latest-revision label")
	}
}

// updateLatestRevisionLabels sets latest-revision=true on the published package
// with the highest revision number, and false on all others for the same package/repo.
func (r *PackageRevisionReconciler) updateLatestRevisionLabels(ctx context.Context, pr *porchv1alpha2.PackageRevision) {
	log := log.FromContext(ctx)

	var allRevs porchv1alpha2.PackageRevisionList
	if err := r.List(ctx, &allRevs,
		client.InNamespace(pr.Namespace),
		client.MatchingFields{
			string(porchv1alpha2.PkgRevSelectorRepository):  pr.Spec.RepositoryName,
			string(porchv1alpha2.PkgRevSelectorPackageName): pr.Spec.PackageName,
		},
	); err != nil {
		log.Error(err, "failed to list package revisions for latest-revision update")
		return
	}

	highestRev := 0
	var latestName string
	for i := range allRevs.Items {
		rev := &allRevs.Items[i]
		if !rev.DeletionTimestamp.IsZero() {
			continue
		}
		if porchv1alpha2.LifecycleIsPublished(rev.Spec.Lifecycle) && rev.Status.Revision > highestRev {
			highestRev = rev.Status.Revision
			latestName = rev.Name
		}
	}

	for i := range allRevs.Items {
		rev := &allRevs.Items[i]
		if !rev.DeletionTimestamp.IsZero() {
			continue
		}
		desiredLabel := "false"
		if rev.Name == latestName {
			desiredLabel = porchv1alpha2.LatestPackageRevisionValue
		}
		currentLabel := rev.Labels[porchv1alpha2.LatestPackageRevisionKey]
		if currentLabel == desiredLabel {
			continue
		}
		log.V(1).Info("updating latest-revision label", "name", rev.Name, "value", desiredLabel)
		patch := client.MergeFrom(rev.DeepCopy())
		if rev.Labels == nil {
			rev.Labels = map[string]string{}
		}
		rev.Labels[porchv1alpha2.LatestPackageRevisionKey] = desiredLabel
		if err := r.Patch(ctx, rev, patch); err != nil {
			log.Error(err, "failed to update latest-revision label", "name", rev.Name)
		}
	}
}
