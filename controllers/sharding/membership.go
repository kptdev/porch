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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// MembershipProvider keeps a Sharding's NumShards in sync with the StatefulSet
// spec.replicas. Watches spec (not readyReplicas) so all pods converge quickly.
type MembershipProvider struct {
	client   client.Client
	stsKey   types.NamespacedName
	sharding *Sharding
}

// NewMembershipProvider builds a provider that watches the StatefulSet and updates Sharding's count.
func NewMembershipProvider(c client.Client, namespace, statefulSetName string, sharding *Sharding) *MembershipProvider {
	return &MembershipProvider{
		client:   c,
		stsKey:   types.NamespacedName{Namespace: namespace, Name: statefulSetName},
		sharding: sharding,
	}
}

// Reconcile updates shard count from the watched StatefulSet's spec.replicas.
func (m *MembershipProvider) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.NamespacedName != m.stsKey {
		return ctrl.Result{}, nil
	}
	sts := &appsv1.StatefulSet{}
	if err := m.client.Get(ctx, m.stsKey, sts); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	n := 1
	if sts.Spec.Replicas != nil {
		n = int(*sts.Spec.Replicas)
	}
	if m.sharding.NumShards() != n {
		log.FromContext(ctx).Info("shard membership changed",
			"shardID", m.sharding.ShardID, "oldNumShards", m.sharding.NumShards(), "newNumShards", n)
		m.sharding.SetNumShards(n)
		// TODO(#1253-followup): Trigger re-reconciliation of all repositories and PackageRevisions
		// so that objects which changed ownership are picked up by the new owners.
		// This requires: (1) saving old NumShards, (2) computing ownership delta, (3) enqueuing owners.
	}
	return ctrl.Result{}, nil
}

// SetupWithManager watches the StatefulSet, filtering to spec changes.
func (m *MembershipProvider) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.StatefulSet{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("shard-membership").
		Complete(m)
}

// Prime sets the initial shard count from the StatefulSet before the watch starts.
// If the StatefulSet is absent, the shard keeps its initial count (1, disabled).
func (m *MembershipProvider) Prime(ctx context.Context, reader client.Reader) error {
	sts := &appsv1.StatefulSet{}
	if err := reader.Get(ctx, m.stsKey, sts); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil // no StatefulSet: keep the initial count
		}
		return fmt.Errorf("priming shard membership from %s: %w", m.stsKey, err)
	}
	if sts.Spec.Replicas != nil {
		m.sharding.SetNumShards(int(*sts.Spec.Replicas))
	}
	return nil
}
