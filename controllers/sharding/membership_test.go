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

package sharding

import (
	"context"
	"errors"
	"os"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var errBoom = errors.New("boom")

// errReader is a client.Reader whose Get always fails with a (non-NotFound)
// error, used to exercise error propagation in Prime.
type errReader struct {
	client.Reader
	err error
}

func (r errReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return r.err
}

const (
	testNS  = "porch-system"
	testSTS = "porch-controllers"
)

func membershipScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatalf("add appsv1 to scheme: %v", err)
	}
	return s
}

func statefulSet(replicas *int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: testSTS, Namespace: testNS},
		Spec:       appsv1.StatefulSetSpec{Replicas: replicas},
	}
}

//go:fix inline
func int32p(v int32) *int32 { return new(v) }

func reconcileReq(ns, name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}}
}

func TestMembership_Reconcile_UpdatesCountFromReplicas(t *testing.T) {
	sharding := NewSharding(0, 1)
	c := fake.NewClientBuilder().WithScheme(membershipScheme(t)).
		WithObjects(statefulSet(int32p(3))).Build()

	m := NewMembershipProvider(c, testNS, testSTS, sharding)
	if _, err := m.Reconcile(context.Background(), reconcileReq(testNS, testSTS)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if sharding.NumShards() != 3 {
		t.Errorf("NumShards = %d, want 3", sharding.NumShards())
	}
}

func TestMembership_Reconcile_NilReplicasMeansOne(t *testing.T) {
	sharding := NewSharding(0, 5)
	c := fake.NewClientBuilder().WithScheme(membershipScheme(t)).
		WithObjects(statefulSet(nil)).Build()

	m := NewMembershipProvider(c, testNS, testSTS, sharding)
	if _, err := m.Reconcile(context.Background(), reconcileReq(testNS, testSTS)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if sharding.NumShards() != 1 {
		t.Errorf("NumShards = %d, want 1 (nil replicas)", sharding.NumShards())
	}
	if !sharding.Disabled() {
		t.Error("expected sharding disabled when replicas is nil")
	}
}

func TestMembership_Reconcile_IgnoresOtherStatefulSets(t *testing.T) {
	sharding := NewSharding(0, 2)
	// A different StatefulSet in the same namespace must not affect our count.
	other := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "something-else", Namespace: testNS},
		Spec:       appsv1.StatefulSetSpec{Replicas: int32p(9)},
	}
	c := fake.NewClientBuilder().WithScheme(membershipScheme(t)).WithObjects(other).Build()

	m := NewMembershipProvider(c, testNS, testSTS, sharding)
	if _, err := m.Reconcile(context.Background(), reconcileReq(testNS, "something-else")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if sharding.NumShards() != 2 {
		t.Errorf("NumShards = %d, want 2 (unchanged — event was for another STS)", sharding.NumShards())
	}
}

func TestMembership_Reconcile_StatefulSetDeletedIsNoError(t *testing.T) {
	sharding := NewSharding(0, 3)
	// Watched STS matches the key but doesn't exist (deleted) → IgnoreNotFound,
	// count left as-is.
	c := fake.NewClientBuilder().WithScheme(membershipScheme(t)).Build()

	m := NewMembershipProvider(c, testNS, testSTS, sharding)
	if _, err := m.Reconcile(context.Background(), reconcileReq(testNS, testSTS)); err != nil {
		t.Fatalf("Reconcile() error = %v (want nil on not-found)", err)
	}
	if sharding.NumShards() != 3 {
		t.Errorf("NumShards = %d, want 3 (unchanged on not-found)", sharding.NumShards())
	}
}

func TestMembership_Prime_SetsInitialCount(t *testing.T) {
	sharding := NewSharding(0, 1)
	c := fake.NewClientBuilder().WithScheme(membershipScheme(t)).
		WithObjects(statefulSet(int32p(4))).Build()

	m := NewMembershipProvider(c, testNS, testSTS, sharding)
	if err := m.Prime(context.Background(), c); err != nil {
		t.Fatalf("Prime() error = %v", err)
	}
	if sharding.NumShards() != 4 {
		t.Errorf("after Prime: NumShards = %d, want 4", sharding.NumShards())
	}
}

func TestMembership_Prime_NoStatefulSetKeepsFlagCount(t *testing.T) {
	// Running as a plain Deployment (no StatefulSet) must leave the seeded count
	// untouched — sharding stays off.
	sharding := NewSharding(0, 1)
	c := fake.NewClientBuilder().WithScheme(membershipScheme(t)).Build()

	m := NewMembershipProvider(c, testNS, testSTS, sharding)
	if err := m.Prime(context.Background(), c); err != nil {
		t.Fatalf("Prime() error = %v (want nil when STS absent)", err)
	}
	if sharding.NumShards() != 1 {
		t.Errorf("after Prime with no STS: NumShards = %d, want 1 (unchanged)", sharding.NumShards())
	}
}

func TestMembership_Prime_PropagatesNonNotFoundError(t *testing.T) {
	sharding := NewSharding(0, 1)
	// A reader that fails with a non-NotFound error must surface from Prime.
	m := NewMembershipProvider(nil, testNS, testSTS, sharding)
	err := m.Prime(context.Background(), errReader{err: errBoom})
	if err == nil {
		t.Fatal("Prime() = nil, want error on non-NotFound read failure")
	}
	if sharding.NumShards() != 1 {
		t.Errorf("NumShards = %d, want 1 (unchanged on error)", sharding.NumShards())
	}
}

func TestPodNameFromEnv(t *testing.T) {
	// POD_NAME wins over HOSTNAME.
	t.Setenv("POD_NAME", "porch-controllers-2")
	t.Setenv("HOSTNAME", "some-host")
	if got := PodNameFromEnv(); got != "porch-controllers-2" {
		t.Errorf("PodNameFromEnv() = %q, want porch-controllers-2", got)
	}

	// Falls back to HOSTNAME when POD_NAME is unset.
	os.Unsetenv("POD_NAME")
	if got := PodNameFromEnv(); got != "some-host" {
		t.Errorf("PodNameFromEnv() = %q, want some-host", got)
	}

	// Falls back to os.Hostname() when both env vars are empty.
	os.Unsetenv("HOSTNAME")
	hn, _ := os.Hostname()
	if got := PodNameFromEnv(); got != hn {
		t.Errorf("PodNameFromEnv() = %q, want os.Hostname() %q", got, hn)
	}
}
