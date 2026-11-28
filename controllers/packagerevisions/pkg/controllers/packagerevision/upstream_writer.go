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
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/upstreamdependents"
)

// upstreamWriteTimeout bounds one batch, including its retries.
const upstreamWriteTimeout = time.Minute

// upstreamWriter batches UpstreamDependents writes per object: writes queued while a batch is in flight
// share its read-modify-write. Each caller waits for its own result.
type upstreamWriter struct {
	mu sync.Mutex
	// pending holds the queued requests for each object, keyed by namespace/name. An entry exists
	// while a drain goroutine runs for that object.
	pending map[string][]*writeRequest
}

// writeRequest is one dependent's edges for one object, and where its result goes.
type writeRequest struct {
	ctx    context.Context
	target *upstreamTarget
	result chan error
}

func newUpstreamWriter() *upstreamWriter {
	return &upstreamWriter{pending: map[string][]*writeRequest{}}
}

// submit queues t and blocks until its batch is written or ctx ends. A caller whose ctx ends may still
// have its write applied later. That is safe, because entry writes are idempotent.
func (w *upstreamWriter) submit(ctx context.Context, r *PackageRevisionReconciler, namespace string, t *upstreamTarget) error {
	key := namespace + "/" + t.objectName
	req := &writeRequest{ctx: ctx, target: t, result: make(chan error, 1)}

	w.mu.Lock()
	queue, draining := w.pending[key]
	w.pending[key] = append(queue, req)
	w.mu.Unlock()
	if !draining {
		go w.drain(r, namespace, t.objectName, key)
	}

	select {
	case err := <-req.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// drain applies queued batches for one object until the queue is empty, then stops.
func (w *upstreamWriter) drain(r *PackageRevisionReconciler, namespace, objectName, key string) {
	for {
		w.mu.Lock()
		batch := w.pending[key]
		if len(batch) == 0 {
			delete(w.pending, key)
			w.mu.Unlock()
			return
		}
		w.pending[key] = nil
		w.mu.Unlock()
		r.writeBatch(namespace, objectName, batch)
	}
}

// writeBatch writes batch and reports each request's result. A failure of the whole batch is reported
// to every request.
func (r *PackageRevisionReconciler) writeBatch(namespace, objectName string, batch []*writeRequest) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(batch[0].ctx), upstreamWriteTimeout)
	defer cancel()

	results, err := r.writeBatchAttempts(ctx, namespace, objectName, batch)
	for i, req := range batch {
		if err != nil {
			req.result <- err
			continue
		}
		req.result <- results[i]
	}
}

// writeBatchAttempts retries writeBatchOnce on conflict, with jittered exponential backoff, so writers on
// one hot object spread out instead of retrying in step.
func (r *PackageRevisionReconciler) writeBatchAttempts(ctx context.Context, namespace, objectName string,
	batch []*writeRequest) ([]error, error) {
	for attempt := range upstreamDependentsRetries {
		results, retry, err := r.writeBatchOnce(ctx, namespace, objectName, batch)
		if err != nil {
			return nil, err
		}
		if !retry {
			return results, nil
		}
		if err := waitBackoff(ctx, backoffDelay(attempt)); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("upstream dependents %s/%s: gave up after %d conflicts", namespace, objectName, upstreamDependentsRetries)
}

// writeBatchOnce makes one read-modify-write attempt for batch. It returns a result per request. A request
// that fails on its own, such as an identity clash or a cap refusal, gets an error and does not stop the
// others. It reports retry when another writer changed the object first.
func (r *PackageRevisionReconciler) writeBatchOnce(ctx context.Context, namespace, objectName string,
	batch []*writeRequest) (results []error, retry bool, err error) {
	obj, found, err := r.loadBatchObject(ctx, namespace, objectName, batch[0].target)
	if err != nil {
		return nil, false, err
	}
	merged := r.mergeBatch(ctx, namespace, objectName, obj, batch)
	if !merged.changed {
		return merged.results, false, nil
	}
	return r.persistBatch(ctx, namespace, objectName, obj, found, merged)
}

// loadBatchObject reads the object from the API server, not the cache. A cached read would start a watch on
// UpstreamDependents, which costs more bytes than the reads it saves. When the object does not exist, it
// returns a new object for the batch's upstream and found is false.
func (r *PackageRevisionReconciler) loadBatchObject(ctx context.Context, namespace, objectName string,
	first *upstreamTarget) (obj *porchv1alpha2.UpstreamDependents, found bool, err error) {
	obj = &porchv1alpha2.UpstreamDependents{}
	switch getErr := r.apiReader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: objectName}, obj); {
	case apierrors.IsNotFound(getErr):
		return newUpstreamDependents(namespace, first), false, nil
	case getErr != nil:
		return nil, false, fmt.Errorf("read upstream dependents %s/%s: %w", namespace, objectName, getErr)
	}
	return obj, true, nil
}

// batchMerge is the in-memory outcome of applying a batch to an object's entries.
type batchMerge struct {
	results []error
	entries []porchv1alpha2.UpstreamDependent
	changed bool
	// pkgTarget is the package key to set on an object that lacks one. It comes from the first request
	// whose upstream resolves.
	pkgTarget string
}

// mergeBatch applies each request's edges in order, and records a per-request result. A cap refusal marks
// obj truncated, once for the batch.
func (r *PackageRevisionReconciler) mergeBatch(ctx context.Context, namespace, objectName string,
	obj *porchv1alpha2.UpstreamDependents, batch []*writeRequest) batchMerge {
	m := batchMerge{results: make([]error, len(batch)), entries: obj.Status.Dependents}
	truncated := obj.Status.Truncated
	for i, req := range batch {
		t := req.target
		if err := upstreamdependents.CheckIdentity(obj.Annotations, t.identity); err != nil {
			m.results[i] = fmt.Errorf("upstream dependents %s/%s: %w", namespace, objectName, err)
			continue
		}
		if err := r.resolveUpstreamPackage(ctx, namespace, t); err != nil {
			m.results[i] = err
			continue
		}
		if obj.Spec.UpstreamPackage == "" && m.pkgTarget == "" {
			m.pkgTarget = t.upstreamPackage
		}

		next, differs := applyEntries(m.entries, t.add, t.remove)
		if !differs {
			continue
		}
		if len(next) > upstreamdependents.MaxDependents {
			m.results[i] = ErrUpstreamDependentsCapReached
			if !truncated {
				setCapCondition(obj)
				truncated = true
				m.changed = true
				log.FromContext(ctx).Error(ErrUpstreamDependentsCapReached,
					"upstream dependents reached cap; the dependents list is incomplete",
					"object", objectName, "max", upstreamdependents.MaxDependents)
			} else {
				log.FromContext(ctx).V(2).Info("upstream dependents still at cap; dependent not recorded", "object", objectName)
			}
			continue
		}
		m.entries = next
		m.changed = true
	}
	return m
}

// persistBatch writes the merged entries. The status is written first. Its resourceVersion catches a
// concurrent change to the entries, which a patch would not. It reports retry on a conflict.
func (r *PackageRevisionReconciler) persistBatch(ctx context.Context, namespace, objectName string,
	obj *porchv1alpha2.UpstreamDependents, found bool, m batchMerge) (results []error, retry bool, err error) {
	if !found {
		if m.pkgTarget != "" {
			obj.Spec.UpstreamPackage = m.pkgTarget
		}
		// The API server drops status on create, so the entries are written by the status update below.
		switch createErr := r.Create(ctx, obj, client.FieldOwner(fieldManagerPRController)); {
		case apierrors.IsAlreadyExists(createErr):
			// Another writer created it first. Re-read on the next attempt.
			return nil, true, nil
		case createErr != nil:
			return nil, false, fmt.Errorf("create upstream dependents %s/%s: %w", namespace, objectName, createErr)
		}
	}
	obj.Status.Dependents = m.entries
	obj.Status.DependentCount = len(m.entries)

	markWritten(obj)
	switch statusErr := r.Status().Update(ctx, obj, client.FieldOwner(fieldManagerPRController)); {
	case apierrors.IsConflict(statusErr):
		return nil, true, nil
	case statusErr != nil:
		return nil, false, fmt.Errorf("update upstream dependents %s/%s: %w", namespace, objectName, statusErr)
	}

	// Objects written before the package key existed get it here. The patch touches only that field.
	if found && m.pkgTarget != "" {
		if err := setPackageKey(ctx, r, obj, m.pkgTarget); err != nil {
			return nil, false, fmt.Errorf("patch upstream dependents spec %s/%s: %w", namespace, objectName, err)
		}
	}
	return m.results, false, nil
}

// setPackageKey patches spec.upstreamPackage on obj and nothing else.
func setPackageKey(ctx context.Context, r *PackageRevisionReconciler, obj *porchv1alpha2.UpstreamDependents, pkg string) error {
	base := obj.DeepCopy()
	obj.Spec.UpstreamPackage = pkg
	return r.Patch(ctx, obj, client.MergeFrom(base), client.FieldOwner(fieldManagerPRController))
}

// dependentWriter returns the reconciler's batching writer, creating it on first use.
func (r *PackageRevisionReconciler) dependentWriter() *upstreamWriter {
	r.dependentWriterOnce.Do(func() { r.dependentWriterState = newUpstreamWriter() })
	return r.dependentWriterState
}
