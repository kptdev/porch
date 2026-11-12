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
	"sort"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/upstreamdependents"
)

const (
	// upstreamDependentsRetries bounds conflict retries in the read-modify-write loop.
	upstreamDependentsRetries = 5

	// conditionCapReached marks an object at the per-object cap.
	conditionCapReached = "CapReached"

	reasonMaxDependentsReached = "MaxDependentsReached"
)

// ErrUpstreamDependentsCapReached means the upstream object is full.
var ErrUpstreamDependentsCapReached = errors.New("upstream dependents list is full")

// upstreamTarget is one UpstreamDependents object and the entries to add or remove on it.
type upstreamTarget struct {
	objectName string
	identity   string
	add        []porchv1alpha2.UpstreamDependent
	remove     []porchv1alpha2.UpstreamDependent
}

// syncUpstreamDependents brings each affected upstream's entries in line with a dependent's
// subpackage upstreams. Equal lists write nothing. Safe to repeat.
func (r *PackageRevisionReconciler) syncUpstreamDependents(ctx context.Context, pr *porchv1alpha2.PackageRevision,
	oldUps, newUps []porchv1alpha2.SubpackageUpstream) error {
	if equality.Semantic.DeepEqual(oldUps, newUps) {
		return nil
	}

	targets := map[string]*upstreamTarget{}
	target := func(up porchv1alpha2.SubpackageUpstream) *upstreamTarget {
		identity, readable, ok := upstreamIdentity(up)
		if !ok {
			return nil
		}
		name := upstreamdependents.ObjectName(identity, readable)
		t, exists := targets[name]
		if !exists {
			t = &upstreamTarget{objectName: name, identity: identity}
			targets[name] = t
		}
		return t
	}
	entry := func(up porchv1alpha2.SubpackageUpstream) porchv1alpha2.UpstreamDependent {
		return porchv1alpha2.UpstreamDependent{Name: pr.Name, Path: up.Path}
	}

	for _, up := range oldUps {
		if t := target(up); t != nil {
			t.remove = append(t.remove, entry(up))
		}
	}
	for _, up := range newUps {
		if t := target(up); t != nil {
			t.add = append(t.add, entry(up))
		}
	}

	var errs []error
	for _, t := range targets {
		err := r.writeUpstreamDependents(ctx, pr.Namespace, t)
		if err == nil {
			log.FromContext(ctx).V(3).Info("upstream dependents recorded", "object", t.objectName,
				"added", len(t.add), "removed", len(t.remove))
			continue
		}
		// A cap refusal is logged by refuseAtCap. It cannot succeed until the upstream is reset,
		// so it gets no prompt retry. Any other failure is logged here and retried.
		if !errors.Is(err, ErrUpstreamDependentsCapReached) {
			log.FromContext(ctx).Error(err, "failed to update upstream dependents; will retry", "object", t.objectName)
			markPersistRetry(ctx)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// persistRetry flags one reconcile for a prompt retry after a failed dependent write.
//
// Unlike other status write failures, this one affects safety. The dependent's status is the
// only record of its upstream entries. If that write is lost, nothing repeats it, and the
// upstream can become deletable while still in use. Cap refusals are excluded (#1365).
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

// subpackageUpstreamsToPersist syncs the dependent's upstreams and returns the list to store.
// The new list is stored only if every write succeeded. Otherwise the previous list is kept,
// so the next reconcile retries the same diff.
func (r *PackageRevisionReconciler) subpackageUpstreamsToPersist(ctx context.Context, pr *porchv1alpha2.PackageRevision,
	upstreams []porchv1alpha2.SubpackageUpstream) []porchv1alpha2.SubpackageUpstream {
	if err := r.syncUpstreamDependents(ctx, pr, pr.Status.SubpackageUpstreams, upstreams); err != nil {
		return pr.Status.SubpackageUpstreams
	}
	return upstreams
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

// writeUpstreamDependents applies t, retrying when another writer changes the object first.
func (r *PackageRevisionReconciler) writeUpstreamDependents(ctx context.Context, namespace string, t *upstreamTarget) error {
	for range upstreamDependentsRetries {
		retry, err := r.writeUpstreamDependentsOnce(ctx, namespace, t)
		if err != nil {
			return err
		}
		if !retry {
			return nil
		}
	}
	return fmt.Errorf("upstream dependents %s/%s: gave up after %d conflicts", namespace, t.objectName, upstreamDependentsRetries)
}

// writeUpstreamDependentsOnce makes one read-modify-write attempt. It reports retry when the
// caller should read again: after creating the object, or on a conflict. The read goes to the
// API server, not the cache.
func (r *PackageRevisionReconciler) writeUpstreamDependentsOnce(ctx context.Context, namespace string, t *upstreamTarget) (retry bool, err error) {
	key := types.NamespacedName{Namespace: namespace, Name: t.objectName}
	obj := &porchv1alpha2.UpstreamDependents{}
	switch err := r.apiReader.Get(ctx, key, obj); {
	case apierrors.IsNotFound(err):
		return r.createUpstreamDependents(ctx, namespace, t)
	case err != nil:
		return false, fmt.Errorf("read upstream dependents %s/%s: %w", namespace, t.objectName, err)
	}

	if err := upstreamdependents.CheckIdentity(obj.Annotations, t.identity); err != nil {
		return false, fmt.Errorf("upstream dependents %s/%s: %w", namespace, t.objectName, err)
	}

	entries, changed := applyEntries(obj.Status.Dependents, t.add, t.remove)
	if !changed {
		return false, nil
	}
	if len(entries) > upstreamdependents.MaxDependents {
		return false, r.refuseAtCap(ctx, namespace, t, obj)
	}
	return r.saveEntries(ctx, namespace, t, obj, entries)
}

// createUpstreamDependents creates the object when there is something to add, then writes its
// entries from the create response, so no re-read is needed. If another writer created it first,
// it reports retry so the caller re-reads.
func (r *PackageRevisionReconciler) createUpstreamDependents(ctx context.Context, namespace string, t *upstreamTarget) (bool, error) {
	if len(t.add) == 0 {
		return false, nil
	}
	obj := newUpstreamDependents(namespace, t)
	switch err := r.Create(ctx, obj, client.FieldOwner(fieldManagerPRController)); {
	case err == nil:
		entries, _ := applyEntries(nil, t.add, t.remove)
		return r.saveEntries(ctx, namespace, t, obj, entries)
	case apierrors.IsAlreadyExists(err):
		return true, nil
	default:
		return false, fmt.Errorf("create upstream dependents %s/%s: %w", namespace, t.objectName, err)
	}
}

// refuseAtCap returns ErrUpstreamDependentsCapReached. The first refusal marks the object
// truncated with one write. Later refusals skip the write, so retries do not churn the object.
func (r *PackageRevisionReconciler) refuseAtCap(ctx context.Context, namespace string, t *upstreamTarget, obj *porchv1alpha2.UpstreamDependents) error {
	if obj.Status.Truncated {
		log.FromContext(ctx).V(2).Info("upstream dependents still at cap; dependent not recorded", "object", t.objectName)
		return ErrUpstreamDependentsCapReached
	}
	log.FromContext(ctx).Error(ErrUpstreamDependentsCapReached,
		"upstream dependents reached cap; deletes of this upstream fail closed",
		"object", t.objectName, "max", upstreamdependents.MaxDependents)
	setCapCondition(obj)
	err := r.Status().Update(ctx, obj, client.FieldOwner(fieldManagerPRController))
	if err != nil && !apierrors.IsConflict(err) {
		return fmt.Errorf("mark upstream dependents %s/%s truncated: %w", namespace, t.objectName, err)
	}
	return ErrUpstreamDependentsCapReached
}

// saveEntries writes the new entry list. It reports retry on a conflict. Truncated stays set
// until the object is removed, since an unrecorded dependent may remain.
func (r *PackageRevisionReconciler) saveEntries(ctx context.Context, namespace string, t *upstreamTarget,
	obj *porchv1alpha2.UpstreamDependents, entries []porchv1alpha2.UpstreamDependent) (bool, error) {
	obj.Status.Dependents = entries
	obj.Status.DependentCount = len(entries)

	err := r.Status().Update(ctx, obj, client.FieldOwner(fieldManagerPRController))
	switch {
	case err == nil:
		return false, nil
	case apierrors.IsConflict(err):
		return true, nil
	default:
		return false, fmt.Errorf("update upstream dependents %s/%s: %w", namespace, t.objectName, err)
	}
}

// newUpstreamDependents returns a new object with its identity annotation. Status is written on
// the next pass.
func newUpstreamDependents(namespace string, t *upstreamTarget) *porchv1alpha2.UpstreamDependents {
	return &porchv1alpha2.UpstreamDependents{
		ObjectMeta: metav1.ObjectMeta{
			Name:        t.objectName,
			Namespace:   namespace,
			Annotations: map[string]string{upstreamdependents.AnnotationUpstreamKey: t.identity},
		},
		Spec: porchv1alpha2.UpstreamDependentsSpec{UpstreamKey: t.identity},
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
		Message: fmt.Sprintf("more than %d dependents; deletes of this upstream fail closed", upstreamdependents.MaxDependents),
	})
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
