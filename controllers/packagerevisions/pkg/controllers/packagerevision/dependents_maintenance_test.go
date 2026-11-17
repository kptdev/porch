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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/upstreamdependents"
)

// pruneUD returns an UpstreamDependents object for the upstream revision, with the given entries.
func pruneUD(upstream string, truncated bool, entries ...porchv1alpha2.UpstreamDependent) *porchv1alpha2.UpstreamDependents {
	identity := upstreamdependents.IdentityForPackageRevision(upstream)
	obj := &porchv1alpha2.UpstreamDependents{
		ObjectMeta: metav1ObjectMeta(upstreamdependents.ObjectName(identity, upstream), "default"),
		Spec:       porchv1alpha2.UpstreamDependentsSpec{UpstreamKey: identity},
		Status: porchv1alpha2.UpstreamDependentsStatus{
			Dependents:     entries,
			DependentCount: len(entries),
			Truncated:      truncated,
		},
	}
	if truncated {
		obj.Status.Conditions = []metav1.Condition{{Type: conditionCapReached, Status: metav1.ConditionTrue, Reason: reasonMaxDependentsReached}}
	}
	return obj
}

func newPruneClient(t *testing.T, objs ...client.Object) client.WithWatch {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, porchv1alpha2.AddToScheme(scheme))
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&porchv1alpha2.UpstreamDependents{}).
		Build()
}

// runSweep runs one sweep. The fake client serves both the cache and the API reader.
func runSweep(t *testing.T, c client.Client) {
	t.Helper()
	p, err := newDependentMaintenance(c, c, "04:00", false)
	require.NoError(t, err)
	require.NoError(t, p.pass(context.Background(), true))
}

func udKey(upstream string) types.NamespacedName {
	identity := upstreamdependents.IdentityForPackageRevision(upstream)
	return types.NamespacedName{Namespace: "default", Name: upstreamdependents.ObjectName(identity, upstream)}
}

func getPruneUD(t *testing.T, c client.Client, upstream string) *porchv1alpha2.UpstreamDependents {
	t.Helper()
	obj := &porchv1alpha2.UpstreamDependents{}
	require.NoError(t, c.Get(context.Background(), udKey(upstream), obj))
	return obj
}

func TestSweepKeepsLiveEntry(t *testing.T) {
	c := newPruneClient(t,
		clonedPR("custom-a", "vendor.v1"),
		pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "custom-a"}))

	runSweep(t, c)

	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a"}}, getPruneUD(t, c, "vendor.v1").Status.Dependents)
}

func TestSweepRemovesOrphanedEntryAndDeletesEmptyObject(t *testing.T) {
	c := newPruneClient(t,
		pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "gone"}))

	runSweep(t, c)

	err := c.Get(context.Background(), udKey("vendor.v1"), &porchv1alpha2.UpstreamDependents{})
	assert.True(t, apierrors.IsNotFound(err), "an object left empty is deleted")
}

func TestSweepRemovesEntryThatStoppedReferencing(t *testing.T) {
	// custom-a exists but no longer clones vendor.v1.
	c := newPruneClient(t,
		dependentPR("custom-a"),
		pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "custom-a"}))

	runSweep(t, c)

	err := c.Get(context.Background(), udKey("vendor.v1"), &porchv1alpha2.UpstreamDependents{})
	assert.True(t, apierrors.IsNotFound(err))
}

func TestSweepRebuildsTruncatedObjectAndClearsFlag(t *testing.T) {
	c := newPruneClient(t,
		clonedPR("custom-a", "vendor.v1"),
		clonedPR("custom-b", "vendor.v1"),
		pruneUD("vendor.v1", true))

	runSweep(t, c)

	obj := getPruneUD(t, c, "vendor.v1")
	assert.False(t, obj.Status.Truncated)
	assert.Empty(t, obj.Status.Conditions, "the cap condition is cleared")
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a"}, {Name: "custom-b"}}, obj.Status.Dependents)
	assert.Equal(t, 2, obj.Status.DependentCount)
	assert.NotNil(t, obj.Status.LastWritten, "a sweep rewrite stamps the status")
}

func TestSweepKeepsTruncatedObjectWhileOverCap(t *testing.T) {
	objs := []client.Object{pruneUD("vendor.v1", true)}
	for i := 0; i <= upstreamdependents.MaxDependents; i++ {
		objs = append(objs, clonedPR(fmt.Sprintf("dep-%05d", i), "vendor.v1"))
	}
	c := newPruneClient(t, objs...)

	runSweep(t, c)

	assert.True(t, getPruneUD(t, c, "vendor.v1").Status.Truncated, "a rebuild over the cap cannot clear the flag")
}

func TestSweepDoesNotResetWhileADependentIsTruncated(t *testing.T) {
	// A dependent with truncated upstream data could hide an edge, so the flag stays.
	hidden := clonedPR("hidden", "vendor.v1")
	hidden.Status.SubpackageUpstreamsTruncated = true
	c := newPruneClient(t,
		clonedPR("custom-a", "vendor.v1"),
		hidden,
		pruneUD("vendor.v1", true))

	runSweep(t, c)

	assert.True(t, getPruneUD(t, c, "vendor.v1").Status.Truncated)
}

func TestSweepKeepsEntryOfDependentWithTruncatedUpstreams(t *testing.T) {
	// Its uses are unknown, so the entry is kept rather than guessed away.
	dep := dependentPR("custom-a")
	dep.Status.SubpackageUpstreamsTruncated = true
	c := newPruneClient(t,
		dep,
		pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "custom-a"}))

	runSweep(t, c)

	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a"}}, getPruneUD(t, c, "vendor.v1").Status.Dependents)
}

func TestNextSweepIsTheNextOccurrenceOfTheConfiguredTime(t *testing.T) {
	p, err := newDependentMaintenance(nil, nil, "04:00", false)
	require.NoError(t, err)

	at := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		return ts
	}
	assert.Equal(t, at("2026-11-15T04:00:00Z"), p.nextSweep(at("2026-11-15T03:00:00Z")), "before the time: today")
	assert.Equal(t, at("2026-11-16T04:00:00Z"), p.nextSweep(at("2026-11-15T05:00:00Z")), "after the time: tomorrow")
	assert.Equal(t, at("2026-11-16T04:00:00Z"), p.nextSweep(at("2026-11-15T04:00:00Z")), "a sweep at the time does not repeat")
	// 23:59 at +01:00 is 22:59 UTC, so the next sweep is 04:00 UTC the following day.
	assert.Equal(t, at("2026-11-16T04:00:00Z"), p.nextSweep(at("2026-11-15T23:59:00+01:00")), "the schedule is in UTC")
}

func TestNewDependentPrunerRejectsBadTimes(t *testing.T) {
	for _, bad := range []string{"4am", "25:00", "", "04:60"} {
		_, err := newDependentMaintenance(nil, nil, bad, false)
		assert.Error(t, err, "time %q must be rejected", bad)
	}
}

func TestDependentPrunerStartStopsWhenCancelled(t *testing.T) {
	c := newPruneClient(t)
	p, err := newDependentMaintenance(c, c, "04:00", false)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	assert.NoError(t, p.Start(ctx))
}

// newBackfill returns a backfill with no start delay and a short write interval for tests.
func newBackfill(c client.Client) *dependentMaintenance {
	m, err := newDependentMaintenance(c, c, "04:00", false)
	if err != nil {
		panic(err)
	}
	m.startDelay = 0
	m.writeEvery = time.Millisecond
	return m
}

// existingDependent is a PackageRevision that cloned vendor.v1 as a whole package and a subpackage
// of vendor.v2, as its status records it. Its spec and status are both current.
func existingDependent(name string) *porchv1alpha2.PackageRevision {
	pr := clonedPR(name, "vendor.v1")
	pr.Status.ObservedGeneration = pr.Generation
	pr.Status.SubpackageUpstreams = []porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v2", "network")}
	return pr
}

func TestBackfillAddsMissingEntriesWithoutAnyObject(t *testing.T) {
	c := newPruneClient(t,
		upstreamPR("vendor.v1", "repo", "vendor/pkg"),
		upstreamPR("vendor.v2", "repo", "vendor/pkg"),
		existingDependent("custom-a"))

	require.NoError(t, newBackfill(c).pass(context.Background(), false))

	v1 := getPruneUD(t, c, "vendor.v1")
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a"}}, v1.Status.Dependents)
	assert.Equal(t, "repo.vendor.pkg", v1.Spec.UpstreamPackage)

	v2 := getPruneUD(t, c, "vendor.v2")
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a", Path: "network"}}, v2.Status.Dependents)
}

func TestBackfillAddsOnlyTheEntriesThatAreMissing(t *testing.T) {
	c := newPruneClient(t,
		upstreamPR("vendor.v1", "repo", "vendor/pkg"),
		existingDependent("custom-a"),
		pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "custom-b"}))

	require.NoError(t, newBackfill(c).pass(context.Background(), false))

	assert.ElementsMatch(t,
		[]porchv1alpha2.UpstreamDependent{{Name: "custom-a"}, {Name: "custom-b"}},
		getPruneUD(t, c, "vendor.v1").Status.Dependents)
}

func TestBackfillDoesNotWriteObjectsThatAreComplete(t *testing.T) {
	obj := pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "custom-a"})
	obj.Spec.UpstreamPackage = "repo.vendor.pkg"
	c := newPruneClient(t,
		upstreamPR("vendor.v1", "repo", "vendor/pkg"),
		existingDependent("custom-a"),
		obj)
	before := getPruneUD(t, c, "vendor.v1").ResourceVersion

	require.NoError(t, newBackfill(c).pass(context.Background(), false))

	// vendor.v2 is still missing its entry, so only that object is written.
	assert.Equal(t, before, getPruneUD(t, c, "vendor.v1").ResourceVersion, "a complete object is not written")
}

func TestBackfillSkipsTruncatedObjects(t *testing.T) {
	c := newPruneClient(t,
		upstreamPR("vendor.v1", "repo", "vendor/pkg"),
		existingDependent("custom-a"),
		pruneUD("vendor.v1", true))

	require.NoError(t, newBackfill(c).pass(context.Background(), false))

	obj := getPruneUD(t, c, "vendor.v1")
	assert.Empty(t, obj.Status.Dependents, "truncated objects are left to the cleanup job's rebuild")
	assert.True(t, obj.Status.Truncated)
}

func TestBackfillSkipsDependentsBeingDeleted(t *testing.T) {
	deleting := existingDependent("custom-a")
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	deleting.Finalizers = []string{porchv1alpha2.PackageRevisionFinalizer}
	c := newPruneClient(t, upstreamPR("vendor.v1", "repo", "vendor/pkg"), deleting)

	require.NoError(t, newBackfill(c).pass(context.Background(), false))

	err := c.Get(context.Background(), udKey("vendor.v1"), &porchv1alpha2.UpstreamDependents{})
	assert.Error(t, err, "no object is created for a dependent that is being deleted")
}

func TestBackfillStopsWhenCancelled(t *testing.T) {
	c := newPruneClient(t,
		upstreamPR("vendor.v1", "repo", "vendor/pkg"),
		existingDependent("custom-a"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := newBackfill(c).pass(ctx, false)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Error(t, c.Get(context.Background(), udKey("vendor.v1"), &porchv1alpha2.UpstreamDependents{}),
		"a cancelled run writes nothing")
}

func TestBackfillPacesWrites(t *testing.T) {
	const interval = 20 * time.Millisecond
	c := newPruneClient(t,
		upstreamPR("vendor.v1", "repo", "vendor/pkg"),
		upstreamPR("vendor.v2", "repo", "vendor/pkg"),
		existingDependent("custom-a"),
		existingDependent("custom-b"))
	b := newBackfill(c)
	b.writeEvery = interval

	start := time.Now()
	require.NoError(t, b.pass(context.Background(), false))

	// Two objects are missing entries, so there are two writes. Each waits one interval first.
	assert.GreaterOrEqual(t, time.Since(start), 2*interval, "writes are paced, not sent in a burst")
}

func TestStartupPassWithoutSweepKeepsStaleEntries(t *testing.T) {
	c := newPruneClient(t,
		upstreamPR("vendor.v1", "repo", "vendor/pkg"),
		existingDependent("custom-a"),
		pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "gone"}))

	require.NoError(t, newBackfill(c).pass(context.Background(), false))

	assert.ElementsMatch(t,
		[]porchv1alpha2.UpstreamDependent{{Name: "custom-a"}, {Name: "gone"}},
		getPruneUD(t, c, "vendor.v1").Status.Dependents, "without the sweep, stale entries are left for the daily run")
}

func TestStartupPassWithSweepRemovesStaleAndAddsMissingInOnePass(t *testing.T) {
	c := newPruneClient(t,
		upstreamPR("vendor.v1", "repo", "vendor/pkg"),
		existingDependent("custom-a"),
		pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "gone"}))
	b := newBackfill(c)

	require.NoError(t, b.pass(context.Background(), true))

	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a"}},
		getPruneUD(t, c, "vendor.v1").Status.Dependents)
}

func TestStartupPassWithSweepDeletesEmptyObject(t *testing.T) {
	c := newPruneClient(t,
		pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "gone"}))
	b := newBackfill(c)

	require.NoError(t, b.pass(context.Background(), true))

	err := c.Get(context.Background(), udKey("vendor.v1"), &porchv1alpha2.UpstreamDependents{})
	assert.True(t, apierrors.IsNotFound(err), "an object left empty by the sweep is deleted")
}

func TestStartupPassFillsPackageKeyOnCompleteObject(t *testing.T) {
	c := newPruneClient(t,
		upstreamPR("vendor.v1", "repo", "vendor/pkg"),
		existingDependent("custom-a"),
		pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "custom-a"}))

	require.NoError(t, newBackfill(c).pass(context.Background(), false))

	assert.Equal(t, "repo.vendor.pkg", getPruneUD(t, c, "vendor.v1").Spec.UpstreamPackage,
		"an object recorded before the package key existed gets it on the startup pass")
}

// errInjected is the failure the interceptors return.
var errInjected = errors.New("injected failure")

func isUDObject(obj client.Object) bool {
	_, ok := obj.(*porchv1alpha2.UpstreamDependents)
	return ok
}

func isPRObject(obj client.Object) bool {
	_, ok := obj.(*porchv1alpha2.PackageRevision)
	return ok
}

// startWithin runs Start in the background, waits until ready reports true, then cancels and checks
// that Start returns nil.
func startWithin(t *testing.T, m *dependentMaintenance, ready func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Start(ctx) }()
	if ready != nil {
		require.Eventually(t, ready, 5*time.Second, 5*time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after cancel")
	}
}

func TestStartRunsStartupPassThenSweepsOnTimer(t *testing.T) {
	c := newPruneClient(t, upstreamPR("vendor.v1", "repo", "vendor/pkg"), existingDependent("custom-a"))
	m := newBackfill(c)
	m.startupSweep = true
	// 0.1s before the 04:00 UTC sweep, so the timer fires once the startup pass has run.
	m.now = func() time.Time { return time.Date(2026, 11, 15, 3, 59, 59, 900_000_000, time.UTC) }
	start := time.Now()

	// Wait for the startup pass, then for the timer to fire a sweep as well.
	startWithin(t, m, func() bool {
		return c.Get(context.Background(), udKey("vendor.v1"), &porchv1alpha2.UpstreamDependents{}) == nil &&
			time.Since(start) > 300*time.Millisecond
	})
}

func TestStartWaitsForTheSweepTimeAndStopsOnCancel(t *testing.T) {
	c := newPruneClient(t, upstreamPR("vendor.v1", "repo", "vendor/pkg"), existingDependent("custom-a"))
	m := newBackfill(c)
	// Noon, so the next sweep is hours away and Start waits on its timer.
	m.now = func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) }

	startWithin(t, m, func() bool {
		return c.Get(context.Background(), udKey("vendor.v1"), &porchv1alpha2.UpstreamDependents{}) == nil
	})
}

func TestStartKeepsRunningAfterAFailedPass(t *testing.T) {
	c := newPruneClient(t, upstreamPR("vendor.v1", "repo", "vendor/pkg"))
	m := newBackfill(c)
	failed := make(chan struct{}, 1)
	m.apiReader = interceptor.NewClient(c, interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			select {
			case failed <- struct{}{}:
			default:
			}
			return errInjected
		},
	})

	// The failed startup pass is logged, not returned, so Start keeps waiting until cancel.
	startWithin(t, m, func() bool {
		select {
		case <-failed:
			return true
		default:
			return false
		}
	})
}

func TestPassReturnsListErrors(t *testing.T) {
	t.Run("upstream dependents list", func(t *testing.T) {
		c := newPruneClient(t)
		m := newBackfill(c)
		m.apiReader = interceptor.NewClient(c, interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				return errInjected
			},
		})
		assert.ErrorIs(t, m.pass(context.Background(), false), errInjected)
	})
	t.Run("package revision list", func(t *testing.T) {
		c := newPruneClient(t)
		m := newBackfill(c)
		m.client = interceptor.NewClient(c, interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*porchv1alpha2.PackageRevisionList); ok {
					return errInjected
				}
				return cl.List(ctx, list, opts...)
			},
		})
		assert.ErrorIs(t, m.pass(context.Background(), false), errInjected)
	})
}

func TestPassReturnsErrorWhenUpstreamCannotBeResolved(t *testing.T) {
	c := newPruneClient(t, upstreamPR("vendor.v1", "repo", "vendor/pkg"), existingDependent("custom-a"))
	m := newBackfill(c)
	m.client = interceptor.NewClient(c, interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if isPRObject(obj) && key.Name == "vendor.v1" {
				return errInjected
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	})
	assert.ErrorIs(t, m.pass(context.Background(), false), errInjected)
}

func TestPassFollowsUpstreamDependentsContinueTokens(t *testing.T) {
	c := newPruneClient(t, pruneUD("vendor.v1", false), pruneUD("vendor.v2", false))
	calls := 0
	m := newBackfill(c)
	m.apiReader = interceptor.NewClient(c, interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if err := cl.List(ctx, list, opts...); err != nil {
				return err
			}
			calls++
			if page, ok := list.(*porchv1alpha2.UpstreamDependentsList); ok && calls == 1 {
				page.Continue = "page-2"
			}
			return nil
		},
	})

	objs, err := m.listExisting(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 2, calls, "a second page is requested while a continue token is present")
	assert.Len(t, objs, 2)
}

func TestPassStopsWhenCancelledWhilePacingExistingObjectWrites(t *testing.T) {
	// The object lacks custom-a, so the pass must write it and then waits on the pace tick.
	c := newPruneClient(t, upstreamPR("vendor.v1", "repo", "vendor/pkg"), existingDependent("custom-a"),
		pruneUD("vendor.v1", false))
	m := newBackfill(c)
	m.writeEvery = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	assert.ErrorIs(t, m.pass(ctx, false), context.Canceled)
}

func TestPassStopsWhenCancelledWhilePacingCreates(t *testing.T) {
	c := newPruneClient(t, upstreamPR("vendor.v1", "repo", "vendor/pkg"), existingDependent("custom-a"))
	m := newBackfill(c)
	m.writeEvery = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	assert.ErrorIs(t, m.pass(ctx, false), context.Canceled)
}

func TestPassStopsWhenCancelledWhilePacingTruncatedRebuild(t *testing.T) {
	c := newPruneClient(t, clonedPR("custom-a", "vendor.v1"), pruneUD("vendor.v1", true))
	m := newBackfill(c)
	m.writeEvery = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	assert.ErrorIs(t, m.pass(ctx, true), context.Canceled)
}

func TestPassCountsFailedWritesAndContinues(t *testing.T) {
	c := newPruneClient(t, upstreamPR("vendor.v1", "repo", "vendor/pkg"), existingDependent("custom-a"),
		pruneUD("vendor.v1", false))
	m := newBackfill(c)
	m.client = interceptor.NewClient(c, interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, cl client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if isUDObject(obj) {
				return errInjected
			}
			return cl.SubResource(sub).Update(ctx, obj, opts...)
		},
	})

	// The failed write is counted and the pass finishes; the next pass retries it.
	require.NoError(t, m.pass(context.Background(), false))
	assert.Empty(t, getPruneUD(t, c, "vendor.v1").Status.Dependents)
}

func TestPassCountsFailedCreatesAndContinues(t *testing.T) {
	c := newPruneClient(t, upstreamPR("vendor.v1", "repo", "vendor/pkg"), existingDependent("custom-a"))
	m := newBackfill(c)
	m.client = interceptor.NewClient(c, interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if isUDObject(obj) {
				return errInjected
			}
			return cl.Create(ctx, obj, opts...)
		},
	})

	require.NoError(t, m.pass(context.Background(), false))
	assert.Error(t, c.Get(context.Background(), udKey("vendor.v1"), &porchv1alpha2.UpstreamDependents{}))
}

func TestPassCountsFailedPackageKeyPatches(t *testing.T) {
	// The entries are complete, so only the package key is missing and the patch is the write.
	c := newPruneClient(t, upstreamPR("vendor.v1", "repo", "vendor/pkg"), existingDependent("custom-a"),
		pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "custom-a"}))
	m := newBackfill(c)
	m.client = interceptor.NewClient(c, interceptor.Funcs{
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if isUDObject(obj) {
				return errInjected
			}
			return cl.Patch(ctx, obj, patch, opts...)
		},
	})

	require.NoError(t, m.pass(context.Background(), false))
	assert.Empty(t, getPruneUD(t, c, "vendor.v1").Spec.UpstreamPackage, "the failed patch left the key unset")
}

func TestSweepCountsFailedDeletesAndContinues(t *testing.T) {
	c := newPruneClient(t, pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "gone"}))
	m := newBackfill(c)
	m.client = interceptor.NewClient(c, interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if isUDObject(obj) {
				return errInjected
			}
			return cl.Delete(ctx, obj, opts...)
		},
	})

	require.NoError(t, m.pass(context.Background(), true))
	assert.NotEmpty(t, getPruneUD(t, c, "vendor.v1").Status.Dependents, "the failed delete left the entry")
}

func TestSweepCountsFailedDependentLookupsAndContinues(t *testing.T) {
	// The entry is not in the cache, so the sweep confirms it with the API server, which fails.
	c := newPruneClient(t, pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "gone"}))
	m := newBackfill(c)
	m.apiReader = interceptor.NewClient(c, interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if isPRObject(obj) {
				return errInjected
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	})

	require.NoError(t, m.pass(context.Background(), true))
	assert.NotEmpty(t, getPruneUD(t, c, "vendor.v1").Status.Dependents, "an unconfirmed entry is kept, not guessed away")
}

func TestSweepCountsFailedRebuildSavesAndContinues(t *testing.T) {
	c := newPruneClient(t, clonedPR("custom-a", "vendor.v1"), pruneUD("vendor.v1", true))
	m := newBackfill(c)
	m.client = interceptor.NewClient(c, interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, cl client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if isUDObject(obj) {
				return errInjected
			}
			return cl.SubResource(sub).Update(ctx, obj, opts...)
		},
	})

	require.NoError(t, m.pass(context.Background(), true))
	assert.True(t, getPruneUD(t, c, "vendor.v1").Status.Truncated, "the failed rebuild left the object truncated")
}

func TestSweepRebuildClearsCapConditionAndKeepsOthers(t *testing.T) {
	obj := pruneUD("vendor.v1", true)
	obj.Status.Conditions = append(obj.Status.Conditions, metav1.Condition{Type: "Other", Status: metav1.ConditionTrue, Reason: "Kept"})
	c := newPruneClient(t, clonedPR("custom-a", "vendor.v1"), obj)

	runSweep(t, c)

	conds := getPruneUD(t, c, "vendor.v1").Status.Conditions
	require.Len(t, conds, 1)
	assert.Equal(t, "Other", conds[0].Type)
}

func TestSweepKeepsEntryTheCacheDoesNotYetSee(t *testing.T) {
	// The dependent is on the API server but not in the cache. The sweep confirms it on the API server
	// and keeps the entry.
	api := newPruneClient(t, clonedPR("custom-a", "vendor.v1"),
		pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "custom-a"}))
	cache := newPruneClient(t)
	m := newBackfill(cache)
	m.apiReader = api

	require.NoError(t, m.pass(context.Background(), true))

	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a"}}, getPruneUD(t, api, "vendor.v1").Status.Dependents)
}

func TestSweepRebuildIncludesSubpackageEdges(t *testing.T) {
	// existingDependent has a subpackage upstream of vendor.v2, which the rebuild must record.
	c := newPruneClient(t, existingDependent("custom-a"), pruneUD("vendor.v2", true))

	runSweep(t, c)

	obj := getPruneUD(t, c, "vendor.v2")
	assert.False(t, obj.Status.Truncated)
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a", Path: "network"}}, obj.Status.Dependents)
}

func TestSweepVisitsEveryNamespace(t *testing.T) {
	// One orphan in each namespace. The sweep handles each namespace in turn and clears both.
	inOther := pruneUD("vendor.v2", false, porchv1alpha2.UpstreamDependent{Name: "gone-b"})
	inOther.Namespace = "other"
	c := newPruneClient(t,
		pruneUD("vendor.v1", false, porchv1alpha2.UpstreamDependent{Name: "gone-a"}),
		inOther)

	runSweep(t, c)

	err := c.Get(context.Background(), udKey("vendor.v1"), &porchv1alpha2.UpstreamDependents{})
	assert.True(t, apierrors.IsNotFound(err), "the default namespace's orphan is removed")
	err = c.Get(context.Background(), types.NamespacedName{Namespace: "other", Name: inOther.Name},
		&porchv1alpha2.UpstreamDependents{})
	assert.True(t, apierrors.IsNotFound(err), "the other namespace's orphan is removed")
}

// Reset tests run without the confirmation delay. The delay itself is covered by its own tests.
func init() {
	maintenanceConfirmDelay = 0
}

func TestRebuildRefusesWhenDependentsChangeDuringConfirmation(t *testing.T) {
	c := newPruneClient(t,
		clonedPR("custom-a", "vendor.v1"),
		pruneUD("vendor.v1", true))
	// A dependent appears between the first read and the confirming read. It is created on the
	// second PackageRevision list, which is the confirming read.
	late := clonedPR("custom-b", "vendor.v1")
	lists := 0
	wrapped := interceptor.NewClient(c, interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*porchv1alpha2.PackageRevisionList); ok {
				lists++
				if lists == 2 {
					if err := cl.Create(ctx, late); err != nil {
						return err
					}
				}
			}
			return cl.List(ctx, list, opts...)
		},
	})

	p, err := newDependentMaintenance(wrapped, c, "04:00", false)
	require.NoError(t, err)
	require.NoError(t, p.pass(context.Background(), true))

	obj := getPruneUD(t, c, "vendor.v1")
	assert.True(t, obj.Status.Truncated, "a dependent that appears during the confirmation read keeps the object truncated")
	assert.Empty(t, obj.Status.Dependents, "nothing is written when the reset is refused")
}

func TestWaitForReturnsAtOnceWithoutDelayAndStopsOnCancel(t *testing.T) {
	require.NoError(t, waitFor(context.Background(), 0))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, waitFor(ctx, time.Hour), context.Canceled)
}
