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
	"sync/atomic"
	"testing"
	"time"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/upstreamdependents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// TestWriteBatchesConcurrentDependents checks that dependents writing to one upstream object
// concurrently all land, and that batching never needs more status writes than callers.
func TestWriteBatchesConcurrentDependents(t *testing.T) {
	var updates atomic.Int32
	r, c := interceptedReconciler(t, interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, cl client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			updates.Add(1)
			return cl.SubResource(sub).Update(ctx, obj, opts...)
		},
	})

	const dependents = 20
	var wg sync.WaitGroup
	errs := make(chan error, dependents)
	for i := range dependents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- syncSub(r, context.Background(), dependentPR(fmt.Sprintf("custom-%d", i)), nil,
				[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	assert.Len(t, getUD(t, c, "vendor.v1").Status.Dependents, dependents, "every dependent is recorded")
	assert.LessOrEqual(t, int(updates.Load()), dependents, "batching never writes more than once per caller")
	t.Logf("status writes for %d concurrent dependents: %d", dependents, updates.Load())
}

// TestSubmitReturnsWhenTheCallerCancelsWhileWaiting checks that a caller stops waiting when its context
// ends, even though its batch has not been written yet.
func TestSubmitReturnsWhenTheCallerCancelsWhileWaiting(t *testing.T) {
	release := make(chan struct{})
	r, _ := interceptedReconciler(t, interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			<-release
			return cl.Get(ctx, key, obj, opts...)
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- syncSub(r, ctx, dependentPR("custom-a"), nil,
			[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
	}()

	cancel()

	assert.ErrorIs(t, <-done, context.Canceled)
	close(release)
}

// TestWriteBatchAttemptsStopsWhenCancelledDuringBackoff checks that a conflict retry waits no longer than
// its context allows.
func TestWriteBatchAttemptsStopsWhenCancelledDuringBackoff(t *testing.T) {
	r, _ := interceptedReconciler(t, interceptor.Funcs{
		SubResourceUpdate: func(_ context.Context, _ client.Client, _ string, obj client.Object, _ ...client.SubResourceUpdateOption) error {
			return apierrors.NewConflict(schema.GroupResource{Group: "porch.kpt.dev", Resource: "upstreamdependents"}, obj.GetName(), errInjected)
		},
	})
	target := &upstreamTarget{
		objectName: upstreamdependents.ObjectName("vendor.v1", "vendor.v1"),
		identity:   "vendor.v1",
		add:        []porchv1alpha2.UpstreamDependent{{Name: "custom-a", Path: "network"}},
	}
	batch := []*writeRequest{{ctx: context.Background(), target: target, result: make(chan error, 1)}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.writeBatchAttempts(ctx, "default", target.objectName, batch)

	assert.ErrorIs(t, err, context.Canceled)
}

// TestBatchReportsAnUpstreamThatCannotBeReadToTheDependent checks that a failed upstream read fails only the
// request that needs it, and that the caller gets that error.
func TestBatchReportsAnUpstreamThatCannotBeReadToTheDependent(t *testing.T) {
	r, _ := interceptedReconciler(t, interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if isPRObject(obj) && key.Name == "vendor.v1" {
				return errInjected
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	})

	err := syncSub(r, context.Background(), dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})

	assert.ErrorIs(t, err, errInjected)
}

// TestCreateRetriesWhenAnotherWriterCreatedTheObjectFirst checks that an already-existing object is re-read
// and the entry is written to it.
func TestCreateRetriesWhenAnotherWriterCreatedTheObjectFirst(t *testing.T) {
	creates := 0
	r, c := interceptedReconciler(t, interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if !isUDObject(obj) {
				return cl.Create(ctx, obj, opts...)
			}
			creates++
			if creates == 1 {
				return apierrors.NewAlreadyExists(schema.GroupResource{Group: "porch.kpt.dev", Resource: "upstreamdependents"}, obj.GetName())
			}
			return cl.Create(ctx, obj, opts...)
		},
	})

	require.NoError(t, syncSub(r, context.Background(), dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")}))

	assert.Equal(t, 2, creates)
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a", Path: "network"}},
		getUD(t, c, "vendor.v1").Status.Dependents)
}

// TestBatchFailsWhenThePackageKeyCannotBePatched checks that a failed package-key patch fails the batch,
// after the entries were written.
func TestBatchFailsWhenThePackageKeyCannotBePatched(t *testing.T) {
	r, c := interceptedReconciler(t, interceptor.Funcs{
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if isUDObject(obj) {
				return errInjected
			}
			return cl.Patch(ctx, obj, patch, opts...)
		},
	}, upstreamPR("vendor.v1", "repo", "vendor/pkg"), pruneUD("vendor.v1", false))

	err := syncSub(r, context.Background(), dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})

	assert.ErrorIs(t, err, errInjected)
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a", Path: "network"}},
		getUD(t, c, "vendor.v1").Status.Dependents, "the entry was written before the key patch failed")
}

// TestWaitBackoffReturnsWhenCancelled checks the backoff wait ends early on cancel.
func TestWaitBackoffReturnsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()

	assert.ErrorIs(t, waitBackoff(ctx, time.Hour), context.Canceled)
	assert.Less(t, time.Since(start), time.Second)
}

// TestResolveUpstreamPackageReturnsReadErrors checks that only a read error other than not-found is returned.
func TestResolveUpstreamPackageReturnsReadErrors(t *testing.T) {
	r, _ := interceptedReconciler(t, interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return errInjected
		},
	})
	target := &upstreamTarget{upstreamName: "vendor.v1"}

	assert.ErrorIs(t, r.resolveUpstreamPackage(context.Background(), "default", target), errInjected)
}

// TestRemoveUpstreamDependentsRemovesTheUpgradeOldUpstream checks that deleting a dependent removes its entry
// from the upgrade's old upstream as well as its current upstreams.
func TestRemoveUpstreamDependentsRemovesTheUpgradeOldUpstream(t *testing.T) {
	r, c := newUDTestReconciler(t, pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "custom-a"}))
	pr := dependentPR("custom-a")
	pr.Spec.Source = &porchv1alpha2.PackageSource{
		Upgrade: &porchv1alpha2.PackageUpgradeSpec{
			OldUpstream: porchv1alpha2.PackageRevisionRef{Name: "vendor.v1"},
			NewUpstream: porchv1alpha2.PackageRevisionRef{Name: "vendor.v2"},
		},
	}

	require.NoError(t, r.removeUpstreamDependents(context.Background(), pr))

	assert.Empty(t, getUD(t, c, "vendor.v1").Status.Dependents)
}
