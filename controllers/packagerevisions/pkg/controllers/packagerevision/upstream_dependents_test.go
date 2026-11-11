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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/upstreamdependents"
)

func newUDTestReconciler(t *testing.T, objs ...client.Object) (*PackageRevisionReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, porchv1alpha2.AddToScheme(scheme))
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&porchv1alpha2.UpstreamDependents{}).
		Build()
	return &PackageRevisionReconciler{Client: c, apiReader: c}, c
}

func dependentPR(name string) *porchv1alpha2.PackageRevision {
	return &porchv1alpha2.PackageRevision{ObjectMeta: metav1ObjectMeta(name, "default")}
}

func namedUpstream(name, path string) porchv1alpha2.SubpackageUpstream {
	return porchv1alpha2.SubpackageUpstream{Path: path, Name: name}
}

func getUD(t *testing.T, c client.Client, identity string) *porchv1alpha2.UpstreamDependents {
	t.Helper()
	obj := &porchv1alpha2.UpstreamDependents{}
	name := upstreamdependents.ObjectName(identity, identity)
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: name}, obj))
	return obj
}

func TestSyncUpstreamDependentsCreatesEntry(t *testing.T) {
	r, c := newUDTestReconciler(t)
	pr := dependentPR("custom-a")

	require.NoError(t, r.syncUpstreamDependents(context.Background(), pr, nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")}))

	obj := getUD(t, c, "vendor.v1")
	assert.Equal(t, "vendor.v1", obj.Spec.UpstreamKey)
	assert.Equal(t, "vendor.v1", obj.Annotations[upstreamdependents.AnnotationUpstreamKey])
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a", Path: "network"}}, obj.Status.Dependents)
	assert.Equal(t, 1, obj.Status.DependentCount)
	assert.False(t, obj.Status.Truncated)
}

func TestSyncUpstreamDependentsAddsSecondDependent(t *testing.T) {
	r, c := newUDTestReconciler(t)
	ctx := context.Background()

	require.NoError(t, r.syncUpstreamDependents(ctx, dependentPR("custom-b"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")}))
	require.NoError(t, r.syncUpstreamDependents(ctx, dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "dns")}))

	obj := getUD(t, c, "vendor.v1")
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{
		{Name: "custom-a", Path: "dns"},
		{Name: "custom-b", Path: "network"},
	}, obj.Status.Dependents, "entries are sorted by name then path")
	assert.Equal(t, 2, obj.Status.DependentCount)
}

func TestSyncUpstreamDependentsRemovesOldEntryOnly(t *testing.T) {
	r, c := newUDTestReconciler(t)
	ctx := context.Background()

	require.NoError(t, r.syncUpstreamDependents(ctx, dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")}))
	require.NoError(t, r.syncUpstreamDependents(ctx, dependentPR("custom-b"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")}))

	// custom-a switches away from vendor.v1 to vendor.v2.
	require.NoError(t, r.syncUpstreamDependents(ctx, dependentPR("custom-a"),
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")},
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v2", "network")}))

	v1 := getUD(t, c, "vendor.v1")
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-b", Path: "network"}}, v1.Status.Dependents)

	v2 := getUD(t, c, "vendor.v2")
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a", Path: "network"}}, v2.Status.Dependents)
}

func TestSyncUpstreamDependentsNoChangeWritesNothing(t *testing.T) {
	r, c := newUDTestReconciler(t)
	ctx := context.Background()
	ups := []porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")}

	require.NoError(t, r.syncUpstreamDependents(ctx, dependentPR("custom-a"), nil, ups))
	before := getUD(t, c, "vendor.v1").ResourceVersion

	require.NoError(t, r.syncUpstreamDependents(ctx, dependentPR("custom-a"), ups, ups))
	assert.Equal(t, before, getUD(t, c, "vendor.v1").ResourceVersion, "equal lists must not write")
}

func TestSyncUpstreamDependentsRefusesIdentityMismatch(t *testing.T) {
	ud := &porchv1alpha2.UpstreamDependents{
		ObjectMeta: metav1ObjectMeta(upstreamdependents.ObjectName("vendor.v1", "vendor.v1"), "default"),
		Spec:       porchv1alpha2.UpstreamDependentsSpec{UpstreamKey: "other"},
	}
	ud.Annotations = map[string]string{upstreamdependents.AnnotationUpstreamKey: "other"}
	r, _ := newUDTestReconciler(t, ud)

	err := r.syncUpstreamDependents(context.Background(), dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manual review required")
}

func TestSyncUpstreamDependentsSkipsUnidentifiedUpstream(t *testing.T) {
	r, c := newUDTestReconciler(t)
	ups := []porchv1alpha2.SubpackageUpstream{{Path: "network"}} // no name and no locator

	require.NoError(t, r.syncUpstreamDependents(context.Background(), dependentPR("custom-a"), nil, ups))
	list := &porchv1alpha2.UpstreamDependentsList{}
	require.NoError(t, c.List(context.Background(), list))
	assert.Empty(t, list.Items)
}

func TestApplyEntriesDetectsChange(t *testing.T) {
	cur := []porchv1alpha2.UpstreamDependent{{Name: "a", Path: "p"}}

	out, changed := applyEntries(cur, nil, nil)
	assert.False(t, changed)
	assert.Equal(t, cur, out)

	_, changed = applyEntries(cur, []porchv1alpha2.UpstreamDependent{{Name: "a", Path: "p"}}, nil)
	assert.False(t, changed, "re-adding an existing entry is not a change")

	out, changed = applyEntries(cur, []porchv1alpha2.UpstreamDependent{{Name: "b", Path: "p"}}, cur)
	assert.True(t, changed)
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "b", Path: "p"}}, out)

	_, changed = applyEntries(nil, nil, nil)
	assert.False(t, changed)
}

func TestSyncUpstreamDependentsCapRefusesAndFailsClosed(t *testing.T) {
	full := make([]porchv1alpha2.UpstreamDependent, 0, upstreamdependents.MaxDependents)
	for i := 0; i < upstreamdependents.MaxDependents; i++ {
		full = append(full, porchv1alpha2.UpstreamDependent{Name: fmt.Sprintf("dep-%05d", i), Path: "p"})
	}
	name := upstreamdependents.ObjectName("vendor.v1", "vendor.v1")
	ud := &porchv1alpha2.UpstreamDependents{
		ObjectMeta: metav1ObjectMeta(name, "default"),
		Spec:       porchv1alpha2.UpstreamDependentsSpec{UpstreamKey: "vendor.v1"},
		Status:     porchv1alpha2.UpstreamDependentsStatus{Dependents: full, DependentCount: len(full)},
	}
	ud.Annotations = map[string]string{upstreamdependents.AnnotationUpstreamKey: "vendor.v1"}
	r, c := newUDTestReconciler(t, ud)

	err := r.syncUpstreamDependents(context.Background(), dependentPR("one-too-many"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
	require.ErrorIs(t, err, ErrUpstreamDependentsCapReached)

	obj := getUD(t, c, "vendor.v1")
	assert.True(t, obj.Status.Truncated, "a full object must fail closed")
	require.NotEmpty(t, obj.Status.Conditions)
	assert.Equal(t, conditionCapReached, obj.Status.Conditions[0].Type)
	assert.Len(t, obj.Status.Dependents, upstreamdependents.MaxDependents, "the refused entry is not stored")
}

func metav1ObjectMeta(name, namespace string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: namespace}
}

// interceptedReconciler returns a reconciler whose client and API reader are wrapped by the
// given funcs, plus the underlying fake client for assertions.
func interceptedReconciler(t *testing.T, funcs interceptor.Funcs, objs ...client.Object) (*PackageRevisionReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, porchv1alpha2.AddToScheme(scheme))
	base := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&porchv1alpha2.UpstreamDependents{}).
		Build()
	wrapped := interceptor.NewClient(base, funcs)
	return &PackageRevisionReconciler{Client: wrapped, apiReader: wrapped}, base
}

// capped returns an UpstreamDependents object for identity that is already at the cap.
func capped(identity string) *porchv1alpha2.UpstreamDependents {
	full := make([]porchv1alpha2.UpstreamDependent, 0, upstreamdependents.MaxDependents)
	for i := 0; i < upstreamdependents.MaxDependents; i++ {
		full = append(full, porchv1alpha2.UpstreamDependent{Name: fmt.Sprintf("dep-%05d", i), Path: "p"})
	}
	return &porchv1alpha2.UpstreamDependents{
		ObjectMeta: metav1ObjectMeta(upstreamdependents.ObjectName(identity, identity), "default"),
		Spec:       porchv1alpha2.UpstreamDependentsSpec{UpstreamKey: identity},
		Status:     porchv1alpha2.UpstreamDependentsStatus{Dependents: full, DependentCount: len(full)},
	}
}

func TestSyncUpstreamDependentsLocatorUpstream(t *testing.T) {
	r, c := newUDTestReconciler(t)
	loc := &porchv1alpha2.Locator{Git: &porchv1alpha2.GitLock{Repo: "https://x", Directory: "vendor", Ref: "v1", Commit: "c1"}}
	ups := []porchv1alpha2.SubpackageUpstream{{Path: "network", Locator: loc}}

	require.NoError(t, r.syncUpstreamDependents(context.Background(), dependentPR("custom-a"), nil, ups))

	list := &porchv1alpha2.UpstreamDependentsList{}
	require.NoError(t, c.List(context.Background(), list))
	require.Len(t, list.Items, 1)
	obj := list.Items[0]
	assert.Equal(t, "https://x|vendor|v1", obj.Spec.UpstreamKey, "locator identity excludes the commit")
	assert.Equal(t, upstreamdependents.ObjectName("https://x|vendor|v1", "vendor v1"), obj.Name)
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a", Path: "network"}}, obj.Status.Dependents)
}

func TestWriteUpstreamDependentsAlreadyRecordedWritesNothing(t *testing.T) {
	r, c := newUDTestReconciler(t)
	ctx := context.Background()
	ups := []porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")}
	require.NoError(t, r.syncUpstreamDependents(ctx, dependentPR("custom-a"), nil, ups))
	before := getUD(t, c, "vendor.v1").ResourceVersion

	// The entry is already present, so applying it again changes nothing.
	target := &upstreamTarget{
		identity:   "vendor.v1",
		objectName: upstreamdependents.ObjectName("vendor.v1", "vendor.v1"),
		add:        []porchv1alpha2.UpstreamDependent{{Name: "custom-a", Path: "network"}},
	}
	require.NoError(t, r.writeUpstreamDependents(ctx, "default", target))
	assert.Equal(t, before, getUD(t, c, "vendor.v1").ResourceVersion)
}

func TestSyncUpstreamDependentsRemovalWithoutObjectIsNoop(t *testing.T) {
	r, _ := newUDTestReconciler(t)
	err := r.syncUpstreamDependents(context.Background(), dependentPR("custom-a"),
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")}, nil)
	require.NoError(t, err)
}

func TestWriteUpstreamDependentsRetriesOnConflict(t *testing.T) {
	conflicts := 1
	r, c := interceptedReconciler(t, interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, cl client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if conflicts > 0 {
				conflicts--
				return apierrors.NewConflict(schema.GroupResource{Group: "porch.kpt.dev", Resource: "upstreamdependents"}, obj.GetName(), errors.New("stale"))
			}
			return cl.SubResource(sub).Update(ctx, obj)
		},
	})

	err := r.syncUpstreamDependents(context.Background(), dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
	require.NoError(t, err, "a single conflict is retried")
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{{Name: "custom-a", Path: "network"}},
		getUD(t, c, "vendor.v1").Status.Dependents)
}

func TestWriteUpstreamDependentsGivesUpAfterRepeatedConflicts(t *testing.T) {
	r, _ := interceptedReconciler(t, interceptor.Funcs{
		SubResourceUpdate: func(_ context.Context, _ client.Client, _ string, obj client.Object, _ ...client.SubResourceUpdateOption) error {
			return apierrors.NewConflict(schema.GroupResource{Group: "porch.kpt.dev", Resource: "upstreamdependents"}, obj.GetName(), errors.New("stale"))
		},
	})

	err := r.syncUpstreamDependents(context.Background(), dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gave up")
}

func TestWriteUpstreamDependentsCreateFailure(t *testing.T) {
	r, _ := interceptedReconciler(t, interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
			return errors.New("create refused")
		},
	})

	err := r.syncUpstreamDependents(context.Background(), dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create refused")
}

func TestWriteUpstreamDependentsReadFailure(t *testing.T) {
	r, _ := interceptedReconciler(t, interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*porchv1alpha2.UpstreamDependents); ok {
				return errors.New("read refused")
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	})

	err := r.syncUpstreamDependents(context.Background(), dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read refused")
}

func TestWriteUpstreamDependentsStatusWriteFailure(t *testing.T) {
	r, _ := interceptedReconciler(t, interceptor.Funcs{
		SubResourceUpdate: func(_ context.Context, _ client.Client, _ string, _ client.Object, _ ...client.SubResourceUpdateOption) error {
			return errors.New("status write refused")
		},
	})

	err := r.syncUpstreamDependents(context.Background(), dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status write refused")
}

func TestWriteUpstreamDependentsCapStatusWriteFailure(t *testing.T) {
	r, _ := interceptedReconciler(t, interceptor.Funcs{
		SubResourceUpdate: func(_ context.Context, _ client.Client, _ string, _ client.Object, _ ...client.SubResourceUpdateOption) error {
			return errors.New("status write refused")
		},
	}, capped("vendor.v1"))

	err := r.syncUpstreamDependents(context.Background(), dependentPR("one-too-many"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status write refused", "a failed cap write is reported, not swallowed")
}

func TestApplyEntriesHandlesUnsortedInput(t *testing.T) {
	cur := []porchv1alpha2.UpstreamDependent{
		{Name: "b", Path: "p"},
		{Name: "a", Path: "z"},
		{Name: "a", Path: "p"},
	}
	out, changed := applyEntries(cur, []porchv1alpha2.UpstreamDependent{{Name: "c", Path: "p"}},
		[]porchv1alpha2.UpstreamDependent{{Name: "b", Path: "p"}})

	assert.True(t, changed)
	assert.Equal(t, []porchv1alpha2.UpstreamDependent{
		{Name: "a", Path: "p"},
		{Name: "a", Path: "z"},
		{Name: "c", Path: "p"},
	}, out, "output is sorted by name then path")

	_, changed = applyEntries(cur, nil, nil)
	assert.False(t, changed, "unsorted but identical input is not a change")
}

func TestCapConditionHelpers(t *testing.T) {
	obj := &porchv1alpha2.UpstreamDependents{}
	setCapCondition(obj)
	setCapCondition(obj)
	require.Len(t, obj.Status.Conditions, 1, "setting the cap condition again replaces it")
	assert.True(t, obj.Status.Truncated)

	obj.Status.Conditions = append(obj.Status.Conditions, metav1.Condition{Type: "Other", Status: metav1.ConditionTrue})
	setCapCondition(obj)
	require.Len(t, obj.Status.Conditions, 2, "other conditions are kept")
	assert.Equal(t, conditionCapReached, obj.Status.Conditions[0].Type)
}

func TestSubpackageUpstreamsToPersist(t *testing.T) {
	ctx := context.Background()
	newUps := []porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")}
	oldUps := []porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v0", "network")}

	t.Run("success stores the new list", func(t *testing.T) {
		r, _ := newUDTestReconciler(t)
		pr := dependentPR("custom-a")
		pr.Status.SubpackageUpstreams = oldUps
		assert.Equal(t, newUps, r.subpackageUpstreamsToPersist(ctx, pr, newUps))
	})

	t.Run("transient failure keeps the previous list so the next reconcile retries", func(t *testing.T) {
		r, _ := interceptedReconciler(t, interceptor.Funcs{
			Create: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
				return errors.New("transient")
			},
		})
		pr := dependentPR("custom-a")
		pr.Status.SubpackageUpstreams = oldUps
		assert.Equal(t, oldUps, r.subpackageUpstreamsToPersist(ctx, pr, newUps))
	})

	t.Run("refused write at the cap keeps the previous list so the dependent is retried", func(t *testing.T) {
		r, _ := newUDTestReconciler(t, capped("vendor.v1"))
		pr := dependentPR("one-too-many")
		pr.Status.SubpackageUpstreams = oldUps
		assert.Equal(t, oldUps, r.subpackageUpstreamsToPersist(ctx, pr, newUps))
	})

	t.Run("a transient failure on one upstream masks nothing when another is capped", func(t *testing.T) {
		const badUpstream = "vendor.bad"
		badObject := upstreamdependents.ObjectName(badUpstream, badUpstream)
		r, _ := interceptedReconciler(t, interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if obj.GetName() == badObject {
					return errors.New("transient")
				}
				return cl.Create(ctx, obj, opts...)
			},
		}, capped("vendor.v1"))
		pr := dependentPR("one-too-many")
		pr.Status.SubpackageUpstreams = oldUps
		both := []porchv1alpha2.SubpackageUpstream{
			namedUpstream("vendor.v1", "network"),
			namedUpstream(badUpstream, "network"),
		}
		assert.Equal(t, oldUps, r.subpackageUpstreamsToPersist(ctx, pr, both),
			"the transient failure must be retried, so the previous list is kept")
	})
}

func TestCapTruncationIsSticky(t *testing.T) {
	ctx := context.Background()
	r, c := interceptedReconciler(t, interceptor.Funcs{}, capped("vendor.v1"))

	// Fill the object, then refuse one more dependent.
	err := r.syncUpstreamDependents(ctx, dependentPR("one-too-many"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
	require.ErrorIs(t, err, ErrUpstreamDependentsCapReached)

	// Remove one recorded dependent so the list is under the cap again.
	err = r.syncUpstreamDependents(ctx, dependentPR("dep-00000"),
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "p")}, nil)
	require.NoError(t, err)

	// The refused dependent is still unrecorded, so the object must stay truncated.
	obj := getUD(t, c, "vendor.v1")
	assert.Len(t, obj.Status.Dependents, upstreamdependents.MaxDependents-1)
	assert.True(t, obj.Status.Truncated, "truncation is not cleared while a dependent may be unrecorded")
	require.NotEmpty(t, obj.Status.Conditions)
	assert.Equal(t, conditionCapReached, obj.Status.Conditions[0].Type)
}

func TestCapRefusalOnTruncatedObjectDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	full := capped("vendor.v1")
	setCapCondition(full)
	r, c := newUDTestReconciler(t, full)
	before := getUD(t, c, "vendor.v1").ResourceVersion

	for i := range 3 {
		err := r.syncUpstreamDependents(ctx, dependentPR(fmt.Sprintf("retry-%d", i)), nil,
			[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
		require.ErrorIs(t, err, ErrUpstreamDependentsCapReached)
	}
	assert.Equal(t, before, getUD(t, c, "vendor.v1").ResourceVersion,
		"retries against an already-truncated object must not rewrite it")
}

func TestUpstreamDependentsWritesUseControllerFieldManager(t *testing.T) {
	var createdWith string
	r, _ := interceptedReconciler(t, interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			o := &client.CreateOptions{}
			o.ApplyOptions(opts)
			createdWith = o.FieldManager
			return cl.Create(ctx, obj, opts...)
		},
	})

	require.NoError(t, r.syncUpstreamDependents(context.Background(), dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")}))
	assert.Equal(t, fieldManagerPRController, createdWith)
}

func TestPersistRetryIsRequestedForTransientFailure(t *testing.T) {
	r, _ := interceptedReconciler(t, interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
			return errors.New("transient")
		},
	})
	ctx, persist := withPersistRetry(context.Background())

	err := r.syncUpstreamDependents(ctx, dependentPR("custom-a"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
	require.Error(t, err)
	assert.True(t, persist.pending, "a transient failure must request a prompt retry")
}

func TestPersistRetryIsNotRequestedForCapRefusal(t *testing.T) {
	r, _ := newUDTestReconciler(t, capped("vendor.v1"))
	ctx, persist := withPersistRetry(context.Background())

	err := r.syncUpstreamDependents(ctx, dependentPR("one-too-many"), nil,
		[]porchv1alpha2.SubpackageUpstream{namedUpstream("vendor.v1", "network")})
	require.ErrorIs(t, err, ErrUpstreamDependentsCapReached)
	assert.False(t, persist.pending, "a refused cap write cannot succeed on retry, so it does not requeue")
}

func TestRequeueIfPersistPending(t *testing.T) {
	pending := &persistRetry{pending: true}
	idle := &persistRetry{}
	boom := errors.New("boom")

	tests := []struct {
		name   string
		result ctrl.Result
		err    error
		p      *persistRetry
		want   ctrl.Result
	}{
		{name: "pending with no other requeue asks for a prompt retry", p: pending,
			want: ctrl.Result{RequeueAfter: reconciledRetryInterval}},
		{name: "not pending leaves the result alone", p: idle, want: ctrl.Result{}},
		{name: "an existing requeue is kept", p: pending, result: ctrl.Result{Requeue: true},
			want: ctrl.Result{Requeue: true}},
		{name: "an error is returned unchanged", p: pending, err: boom, want: ctrl.Result{}},
		{name: "a nil marker leaves the result alone", p: nil, want: ctrl.Result{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, requeueIfPersistPending(tt.result, tt.err, tt.p))
		})
	}
}
