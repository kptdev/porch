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

package repocache

import (
	"context"
	"fmt"
	"testing"
	"time"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	mockcache "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)
	return scheme
}

func readyRepo(name, namespace string) *configapi.Repository {
	return &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status: configapi.RepositoryStatus{
			Conditions: []metav1.Condition{
				{
					Type:   configapi.RepositoryReady,
					Status: metav1.ConditionTrue,
					Reason: configapi.ReasonReady,
				},
			},
		},
	}
}

func TestReconcileReadyRepoOpensAndAddsFinalizer(t *testing.T) {
	scheme := newTestScheme()
	repo := readyRepo("my-repo", "test-ns")
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).WithStatusSubresource(repo).Build()

	mc := mockcache.NewMockCache(t)
	mc.EXPECT().OpenRepository(mock.Anything, mock.Anything).Return(nil, nil).Once()

	r := &Reconciler{Client: fakeClient, Cache: mc}

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "my-repo", Namespace: "test-ns"},
	})

	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)

	// Verify finalizer was added
	updated := &configapi.Repository{}
	require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: "my-repo", Namespace: "test-ns"}, updated))
	assert.Contains(t, updated.Finalizers, repoCacheFinalizer)
}

func TestReconcileNotReadyRepoIsSkipped(t *testing.T) {
	scheme := newTestScheme()
	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "not-ready", Namespace: "test-ns"},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build()

	mc := mockcache.NewMockCache(t)
	// No OpenRepository or EvictCachedRepository calls expected

	r := &Reconciler{Client: fakeClient, Cache: mc}

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "not-ready", Namespace: "test-ns"},
	})

	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{RequeueAfter: 30 * time.Second}, result)
}

func TestReconcileDeletingRepoEvictsAndRemovesFinalizer(t *testing.T) {
	scheme := newTestScheme()
	// The fake client doesn't properly simulate the finalizer dance (DeletionTimestamp + finalizer
	// keeping the object alive). So we test the deletion path by constructing the object state
	// directly in the reconciler's logic — using a wrapper client that returns the deleting object.
	now := metav1.Now()
	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "del-repo",
			Namespace:         "test-ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{repoCacheFinalizer},
			ResourceVersion:   "1",
		},
	}

	mc := mockcache.NewMockCache(t)
	mc.EXPECT().EvictCachedRepository(mock.Anything, "test-ns", "del-repo").Return(nil).Once()

	// Use a wrapper that returns the deleting repo on Get and accepts Update
	fakeClient := &deletingClient{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		repo:   repo,
	}

	r := &Reconciler{Client: fakeClient, Cache: mc}

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "del-repo", Namespace: "test-ns"},
	})

	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)
	// Mock assertion verifies EvictCachedRepository was called
}

// deletingClient wraps a client and returns a pre-set repo on Get
type deletingClient struct {
	client.Client
	repo *configapi.Repository
}

func (d *deletingClient) Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
	if key.Name == d.repo.Name && key.Namespace == d.repo.Namespace {
		r := obj.(*configapi.Repository)
		*r = *d.repo
		return nil
	}
	return d.Client.Get(ctx, key, obj, opts...)
}

func (d *deletingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	// Accept the update (finalizer removal)
	return nil
}

func TestReconcileOpenRepositoryError(t *testing.T) {
	scheme := newTestScheme()
	repo := readyRepo("fail-repo", "test-ns")
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).WithStatusSubresource(repo).Build()

	mc := mockcache.NewMockCache(t)
	mc.EXPECT().OpenRepository(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("connection refused")).Once()

	r := &Reconciler{Client: fakeClient, Cache: mc}

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "fail-repo", Namespace: "test-ns"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
	assert.Equal(t, reconcile.Result{}, result)
}

func TestReconcileNotFoundIsNoOp(t *testing.T) {
	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	mc := mockcache.NewMockCache(t)

	r := &Reconciler{Client: fakeClient, Cache: mc}

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "gone", Namespace: "test-ns"},
	})

	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)
}

func TestRepoCachePredicate(t *testing.T) {
	p := repoCachePredicate()
	repo := &configapi.Repository{ObjectMeta: metav1.ObjectMeta{Name: "test"}}

	assert.True(t, p.Create(event.CreateEvent{Object: repo}), "create should pass (startup resync)")
	assert.True(t, p.Update(event.UpdateEvent{ObjectOld: repo, ObjectNew: repo}), "update should pass")
	assert.False(t, p.Delete(event.DeleteEvent{Object: repo}), "delete should be filtered (handled via finalizer)")
	assert.False(t, p.Generic(event.GenericEvent{Object: repo}), "generic should be filtered")
}
