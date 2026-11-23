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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// setRepositoryLabel sets the porch.kpt.dev/repository label to spec.repositoryName, reporting whether it changed.
// The repo controller's label selector depends on it, so it is re-asserted on every reconcile.
func setRepositoryLabel(pr *porchv1alpha2.PackageRevision) bool {
	if pr.Labels[porchv1alpha2.RepositoryLabelKey] == pr.Spec.RepositoryName {
		return false
	}
	if pr.Labels == nil {
		pr.Labels = map[string]string{}
	}
	pr.Labels[porchv1alpha2.RepositoryLabelKey] = pr.Spec.RepositoryName
	return true
}

// setLatestRevisionLabelDefault adds latest-revision=false when the label is absent, reporting whether it changed.
// It never overwrites the "true" winner set by updateLatestRevisionLabels.
func setLatestRevisionLabelDefault(pr *porchv1alpha2.PackageRevision) bool {
	if _, ok := pr.Labels[porchv1alpha2.LatestPackageRevisionKey]; ok {
		return false
	}
	if pr.Labels == nil {
		pr.Labels = map[string]string{}
	}
	pr.Labels[porchv1alpha2.LatestPackageRevisionKey] = "false"
	return true
}

// updateLatestRevisionLabels sets latest-revision=true on the highest published
// revision and false on the rest. It returns the name of the latest revision so
// the caller can tell whether the in-hand pr is the winner (e.g. only the latest
// should refresh the main CR), and whether a published sibling was skipped because its
// revision is not yet observed, so the caller can retry once it is. Returns an error so the
// caller can requeue when a stale cached read would otherwise strand a wrong label.
//
// TODO: race condition - concurrent publishes of sibling revisions can
// transiently mislabel (two reconciles compute latestName from independent
// cached List reads and may briefly leave 0 or 2 revisions latest=true). It is
// self-correcting: the next reconcile with a fully-observed list converges to
// the single correct winner. Fix if the window matters: optimistic concurrency
// on the patch (resourceVersion precondition), or a per-package mutex.
func (r *PackageRevisionReconciler) updateLatestRevisionLabels(ctx context.Context, pr *porchv1alpha2.PackageRevision) (latestName string, unobserved bool, err error) {
	log := log.FromContext(ctx)

	var allRevs porchv1alpha2.PackageRevisionList
	if err := r.List(ctx, &allRevs,
		client.InNamespace(pr.Namespace),
		client.MatchingFields{
			string(porchv1alpha2.PkgRevSelectorRepository):  pr.Spec.RepositoryName,
			string(porchv1alpha2.PkgRevSelectorPackageName): pr.Spec.PackageName,
		},
	); err != nil {
		return "", false, fmt.Errorf("failed to list package revisions for latest-revision update: %w", err)
	}

	// Seed the winner with the in-hand pr (fresher than its cached copy).
	highestRev := 0
	if pr.DeletionTimestamp.IsZero() && porchv1alpha2.LifecycleIsPublished(pr.Spec.Lifecycle) && pr.Status.Revision > 0 {
		highestRev = pr.Status.Revision
		latestName = pr.Name
	}

	for i := range allRevs.Items {
		rev := &allRevs.Items[i]
		if !rev.DeletionTimestamp.IsZero() {
			continue
		}
		if !porchv1alpha2.LifecycleIsPublished(rev.Spec.Lifecycle) {
			continue
		}
		revNum := rev.Status.Revision
		if rev.Name == pr.Name && pr.Status.Revision > revNum {
			revNum = pr.Status.Revision
		}
		// A published sibling whose revision number hasn't been observed yet
		// (Status.Revision == 0) is skipped, not treated as fatal. Erroring here
		// would requeue the whole operation; at scale, when several siblings are
		// briefly unobserved, that requeue storm starves the very reconciles that
		// would populate Status.Revision — a self-sustaining livelock that also
		// blocks finalizer removal on the delete path. Skipping lets us label the
		// observed revisions now; when the unobserved sibling's own reconcile lands
		// its Status.Revision, that reconcile re-runs this and corrects the winner.
		if revNum == 0 {
			unobserved = true
			log.V(4).Info("skipping published revision with unobserved revision number for latest-revision labeling",
				"name", rev.Name)
			continue
		}
		if revNum > highestRev {
			highestRev = revNum
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
			return "", false, fmt.Errorf("failed to update latest-revision label on %q: %w", rev.Name, err)
		}
	}
	return latestName, unobserved, nil
}
