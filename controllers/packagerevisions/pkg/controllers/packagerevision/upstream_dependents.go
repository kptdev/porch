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
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	apiutil "github.com/kptdev/porch/api/util"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/upstreamdependents"
)

const (
	// upstreamDependentsRetries bounds conflict retries for one batch. At 50 concurrent clones of
	// one upstream, five immediate retries ran out about 780 times per 500 writes.
	upstreamDependentsRetries = 12
	// upstreamDependentsBackoffBase and upstreamDependentsBackoffMax bound the retry backoff.
	upstreamDependentsBackoffBase = 10 * time.Millisecond
	upstreamDependentsBackoffMax  = 500 * time.Millisecond

	// conditionCapReached marks an object at the per-object cap.
	conditionCapReached = "CapReached"

	reasonMaxDependentsReached = "MaxDependentsReached"
)

// backoffDelay returns the wait before retry attempt (0-based): the base doubled per attempt, capped,
// then jittered into the upper half of that range so waiting writers do not retry in step.
func backoffDelay(attempt int) time.Duration {
	d := upstreamDependentsBackoffMax
	if attempt < 16 {
		if next := upstreamDependentsBackoffBase << attempt; next < upstreamDependentsBackoffMax {
			d = next
		}
	}
	half := d / 2
	return half + rand.N(d-half+1)
}

// waitBackoff sleeps for d, or returns ctx's error when cancelled first.
func waitBackoff(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ErrUpstreamDependentsCapReached means the upstream object is full.
var ErrUpstreamDependentsCapReached = errors.New("upstream dependents list is full")

// upstreamTarget is one UpstreamDependents object and the entries to add or remove on it.
type upstreamTarget struct {
	objectName string
	identity   string
	// upstreamName is the in-cluster upstream PackageRevision, or "" for a locator upstream.
	upstreamName string
	// upstreamPackage is the Package object name of the upstream, resolved before the write.
	upstreamPackage string
	add             []porchv1alpha2.UpstreamDependent
	remove          []porchv1alpha2.UpstreamDependent
}

// directChange is the whole-package clone or upgrade edges to add and remove. Both are
// in-cluster upstream names. Empty when the spec has not changed since the last status write.
type directChange struct {
	add    []string
	remove []string
}

// directUpstreamChange returns the whole-package edges to sync. The spec is only consulted when
// its generation is newer than the last status write, so steady-state status updates make no
// API calls. The upgrade's old upstream is removed, and its new upstream is added.
func directUpstreamChange(pr *porchv1alpha2.PackageRevision) directChange {
	if pr.Status.ObservedGeneration == pr.Generation {
		return directChange{}
	}
	var c directChange
	c.add = upstreamNames(pr)
	if old := upgradeOldUpstream(pr); old != "" && !slices.Contains(c.add, old) {
		c.remove = append(c.remove, old)
	}
	return c
}

// upgradeOldUpstream returns the name of the upgrade's old upstream, or "" when there is none.
func upgradeOldUpstream(pr *porchv1alpha2.PackageRevision) string {
	if src := pr.Spec.Source; src != nil && src.Upgrade != nil {
		return src.Upgrade.OldUpstream.Name
	}
	return ""
}

// syncUpstreamDependents brings each affected upstream's entries in line with a dependent's
// upstreams: subpackage edges (oldUps to newUps) and whole-package edges (direct). Nothing
// to change writes nothing. Safe to repeat.
func (r *PackageRevisionReconciler) syncUpstreamDependents(ctx context.Context, pr *porchv1alpha2.PackageRevision,
	oldUps, newUps []porchv1alpha2.SubpackageUpstream, direct directChange) error {
	if equality.Semantic.DeepEqual(oldUps, newUps) && len(direct.add) == 0 && len(direct.remove) == 0 {
		return nil
	}

	targets := planUpstreamTargets(pr.Name, oldUps, newUps, direct)

	var errs []error
	for _, t := range targets {
		err := r.writeUpstreamDependents(ctx, pr.Namespace, t)
		if err == nil {
			log.FromContext(ctx).V(3).Info("upstream dependents recorded", "object", t.objectName,
				"added", len(t.add), "removed", len(t.remove))
			continue
		}
		// A cap refusal is logged by the writer. It cannot succeed until the upstream is reset, so
		// it gets no prompt retry. Any other failure is logged here and retried.
		if !errors.Is(err, ErrUpstreamDependentsCapReached) {
			log.FromContext(ctx).Error(err, "failed to update upstream dependents; will retry", "object", t.objectName)
			markPersistRetry(ctx)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// writeUpstreamDependents records t through the batching writer and waits for its batch.
func (r *PackageRevisionReconciler) writeUpstreamDependents(ctx context.Context, namespace string, t *upstreamTarget) error {
	return r.dependentWriter().submit(ctx, r, namespace, t)
}

// planUpstreamTargets groups the removals and additions of dependent by the object they affect.
// Upstreams without an identity are skipped.
func planUpstreamTargets(dependent string, oldUps, newUps []porchv1alpha2.SubpackageUpstream,
	direct directChange) map[string]*upstreamTarget {
	targets := map[string]*upstreamTarget{}
	target := func(up porchv1alpha2.SubpackageUpstream) *upstreamTarget {
		identity, readable, ok := upstreamIdentity(up)
		if !ok {
			return nil
		}
		name := upstreamdependents.ObjectName(identity, readable)
		t, exists := targets[name]
		if !exists {
			t = &upstreamTarget{objectName: name, identity: identity, upstreamName: up.Name}
			targets[name] = t
		}
		return t
	}
	entry := func(up porchv1alpha2.SubpackageUpstream) porchv1alpha2.UpstreamDependent {
		return porchv1alpha2.UpstreamDependent{Name: dependent, Path: up.Path}
	}

	for _, up := range append(slices.Clone(oldUps), namedUpstreams(direct.remove)...) {
		if t := target(up); t != nil {
			t.remove = append(t.remove, entry(up))
		}
	}
	for _, up := range append(slices.Clone(newUps), namedUpstreams(direct.add)...) {
		if t := target(up); t != nil {
			t.add = append(t.add, entry(up))
		}
	}
	return targets
}

// resolveUpstreamPackage sets t.upstreamPackage from the upstream PackageRevision in the cache,
// using its spec (repository and package name) rather than parsing its object name. If the
// upstream is not in the cache, the key stays empty: the object still blocks deletes by name,
// it just cannot be found by the package-wide query.
func (r *PackageRevisionReconciler) resolveUpstreamPackage(ctx context.Context, namespace string, t *upstreamTarget) error {
	if t.upstreamName == "" {
		return nil
	}
	up := &porchv1alpha2.PackageRevision{}
	switch err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: t.upstreamName}, up); {
	case apierrors.IsNotFound(err):
		log.FromContext(ctx).V(2).Info("upstream not in cache; package key left empty", "upstream", t.upstreamName)
		return nil
	case err != nil:
		return fmt.Errorf("read upstream %s/%s: %w", namespace, t.upstreamName, err)
	}
	t.upstreamPackage = apiutil.ComposePkgObjName(up.Spec.RepositoryName, "", up.Spec.PackageName)
	return nil
}

// persistRetry flags one reconcile for a prompt retry after a failed dependent write.
//
// Unlike other status write failures, this one affects safety. The dependent's status is the
// only record of its upstream entries. If that write is lost, nothing repeats it, and the
// upstream can become deletable while still in use. Cap refusals are excluded: they cannot succeed until
// the upstream is reset, so a prompt retry would only repeat them.
type persistRetry struct {
	pending bool
}

type persistRetryKey struct{}

// withPersistRetry returns a context carrying a fresh persistRetry.
func withPersistRetry(ctx context.Context) (context.Context, *persistRetry) {
	p := &persistRetry{}
	return context.WithValue(ctx, persistRetryKey{}, p), p
}

// markPersistRetry flags the reconcile in ctx. It is a no-op without a persistRetry.
func markPersistRetry(ctx context.Context) {
	if p, ok := ctx.Value(persistRetryKey{}).(*persistRetry); ok {
		p.pending = true
	}
}

// requeueIfPersistPending requests a prompt retry if one is pending and nothing else requeued.
func requeueIfPersistPending(result ctrl.Result, err error, p *persistRetry) ctrl.Result {
	if err != nil || p == nil || !p.pending || result != (ctrl.Result{}) {
		return result
	}
	return ctrl.Result{RequeueAfter: reconciledRetryInterval}
}

// recordUpstreamDependents syncs the dependent's upstreams and returns the subpackage list to
// store, and whether every write succeeded. On failure the previous list is kept, so the next
// reconcile retries the same diff. The caller must also keep ObservedGeneration unchanged on
// failure, so the whole-package edges are retried too.
func (r *PackageRevisionReconciler) recordUpstreamDependents(ctx context.Context, pr *porchv1alpha2.PackageRevision,
	upstreams []porchv1alpha2.SubpackageUpstream, direct directChange) ([]porchv1alpha2.SubpackageUpstream, bool) {
	if err := r.syncUpstreamDependents(ctx, pr, pr.Status.SubpackageUpstreams, upstreams, direct); err != nil {
		return pr.Status.SubpackageUpstreams, false
	}
	return upstreams, true
}

// removeUpstreamDependents removes every edge pr records, for both kinds, when pr is deleted.
// It covers the clone source, the upgrade's old and new upstreams, and the subpackage list.
func (r *PackageRevisionReconciler) removeUpstreamDependents(ctx context.Context, pr *porchv1alpha2.PackageRevision) error {
	direct := directChange{remove: upstreamNames(pr)}
	if old := upgradeOldUpstream(pr); old != "" {
		direct.remove = append(direct.remove, old)
	}
	return r.syncUpstreamDependents(ctx, pr, pr.Status.SubpackageUpstreams, nil, direct)
}

// namedUpstreams returns whole-package edges for in-cluster upstream names. Their Path is empty.
func namedUpstreams(names []string) []porchv1alpha2.SubpackageUpstream {
	out := make([]porchv1alpha2.SubpackageUpstream, 0, len(names))
	for _, n := range names {
		out = append(out, porchv1alpha2.SubpackageUpstream{Name: n})
	}
	return out
}

// upstreamIdentity returns the identity and readable hint. A name identifies an in-cluster
// upstream; otherwise the locator is used.
func upstreamIdentity(up porchv1alpha2.SubpackageUpstream) (identity, readable string, ok bool) {
	if up.Name != "" {
		return upstreamdependents.IdentityForPackageRevision(up.Name), up.Name, true
	}
	identity = upstreamdependents.IdentityForLocator(up.Locator)
	if identity == "" {
		return "", "", false
	}
	return identity, upstreamdependents.ReadableForLocator(up.Locator), true
}

// newUpstreamDependents returns a new object with its identity annotation. Status is written by
// the batch that created it.
func newUpstreamDependents(namespace string, t *upstreamTarget) *porchv1alpha2.UpstreamDependents {
	return &porchv1alpha2.UpstreamDependents{
		ObjectMeta: metav1.ObjectMeta{
			Name:        t.objectName,
			Namespace:   namespace,
			Annotations: map[string]string{upstreamdependents.AnnotationUpstreamKey: t.identity},
		},
		Spec: porchv1alpha2.UpstreamDependentsSpec{UpstreamKey: t.identity, UpstreamPackage: t.upstreamPackage},
	}
}

// applyEntries applies removals and additions. It returns the sorted entries and whether they
// changed.
func applyEntries(current, add, remove []porchv1alpha2.UpstreamDependent) ([]porchv1alpha2.UpstreamDependent, bool) {
	key := func(e porchv1alpha2.UpstreamDependent) string { return e.Name + "\x00" + e.Path }

	set := make(map[string]porchv1alpha2.UpstreamDependent, len(current)+len(add))
	for _, e := range current {
		set[key(e)] = e
	}
	for _, e := range remove {
		delete(set, key(e))
	}
	for _, e := range add {
		set[key(e)] = e
	}

	out := make([]porchv1alpha2.UpstreamDependent, 0, len(set))
	for _, e := range set {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})

	sortedCurrent := append([]porchv1alpha2.UpstreamDependent(nil), current...)
	sort.Slice(sortedCurrent, func(i, j int) bool {
		if sortedCurrent[i].Name != sortedCurrent[j].Name {
			return sortedCurrent[i].Name < sortedCurrent[j].Name
		}
		return sortedCurrent[i].Path < sortedCurrent[j].Path
	})
	if len(out) == 0 && len(sortedCurrent) == 0 {
		return out, false
	}
	return out, !equality.Semantic.DeepEqual(out, sortedCurrent)
}

func setCapCondition(obj *porchv1alpha2.UpstreamDependents) {
	obj.Status.Truncated = true
	obj.Status.Conditions = setCondition(obj.Status.Conditions, metav1.Condition{
		Type:    conditionCapReached,
		Status:  metav1.ConditionTrue,
		Reason:  reasonMaxDependentsReached,
		Message: fmt.Sprintf("more than %d dependents; the dependents list is incomplete", upstreamdependents.MaxDependents),
	})
}

// markWritten stamps the status with the time it is written. Readers use it to judge freshness.
func markWritten(obj *porchv1alpha2.UpstreamDependents) {
	now := metav1.Now()
	obj.Status.LastWritten = &now
}

func setCondition(conds []metav1.Condition, c metav1.Condition) []metav1.Condition {
	c.LastTransitionTime = metav1.Now()
	for i := range conds {
		if conds[i].Type == c.Type {
			conds[i] = c
			return conds
		}
	}
	return append(conds, c)
}
