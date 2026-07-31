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

package apiserver

import (
	"context"
	"fmt"
	"time"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const repoCacheFinalizer = "config.porch.kpt.dev/porch-server"

// RepoCacheReconciler watches Repository CRs and manages the porch-server
// in-memory cache:
//   - On Create/startup: requeues until the repo is Ready, then opens it.
//   - On spec change (generation bump): re-opens the repository.
//   - On deletion (DeletionTimestamp set): evicts from cache and removes finalizer.
type RepoCacheReconciler struct {
	client client.Client
	cache  cachetypes.Cache
}

func (r *RepoCacheReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	repo := &configapi.Repository{}
	if err := r.client.Get(ctx, req.NamespacedName, repo); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Skip repos managed by v1alpha2 (but still process deletions so our finalizer doesn't block GC)
	if repo.DeletionTimestamp == nil && repo.Annotations[configapi.AnnotationKeyV1Alpha2Migration] == configapi.AnnotationValueMigrationEnabled {
		return reconcile.Result{}, nil
	}

	// Handle deletion — evict from cache and remove finalizer
	if repo.DeletionTimestamp != nil {
		klog.Infof("Repo cache handler: evicting %s", req.NamespacedName)

		if err := r.cache.EvictCachedRepository(ctx, req.Namespace, req.Name); err != nil {
			klog.Warningf("Repo cache handler: failed to evict %s: %v", req.NamespacedName, err)
			return reconcile.Result{}, err
		}

		if controllerutil.ContainsFinalizer(repo, repoCacheFinalizer) {
			controllerutil.RemoveFinalizer(repo, repoCacheFinalizer)
			if err := r.client.Update(ctx, repo); err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to remove finalizer from %s: %w", req.NamespacedName, err)
			}
		}

		klog.Infof("Repo cache handler: evicted %s", req.NamespacedName)
		return reconcile.Result{}, nil
	}

	// Not Ready yet — requeue and wait for the repo controller to set status
	if !isRepositoryReady(repo) {
		klog.Infof("Repo cache handler: %s not Ready, requeuing", req.NamespacedName)
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Add finalizer — guarantees eviction on delete
	if !controllerutil.ContainsFinalizer(repo, repoCacheFinalizer) {
		controllerutil.AddFinalizer(repo, repoCacheFinalizer)
		if err := r.client.Update(ctx, repo); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to add finalizer to %s: %w", req.NamespacedName, err)
		}
		klog.Infof("Repo cache handler: added finalizer to %s", req.NamespacedName)
	}

	// Open the repository in the cache. Internally this uses the cache's
	// SafeRepoMap.LoadOrCreate, which is idempotent — it returns the existing
	// map entry if the repo is already cached, so this is a no-op for repeated reconciles.
	if _, err := r.cache.OpenRepository(ctx, repo); err != nil {
		klog.Warningf("Repo cache handler: failed to open %s: %v", req.NamespacedName, err)
		return reconcile.Result{}, err
	}

	klog.Infof("Repo cache handler: cached repository %s", req.NamespacedName)
	return reconcile.Result{}, nil
}

// setupRepoCacheController registers the repo cache controller with the given manager.
func setupRepoCacheController(mgr ctrl.Manager, cache cachetypes.Cache, maxConcurrency int) error {
	if maxConcurrency <= 0 {
		maxConcurrency = 20
	}
	r := &RepoCacheReconciler{
		client: mgr.GetClient(),
		cache:  cache,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&configapi.Repository{}).
		WithEventFilter(repoCachePredicate()).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrency,
		}).
		Named("repo-cache").
		Complete(r)
}

// isRepositoryReady checks if the repo controller has marked this repository as Ready.
func isRepositoryReady(repo *configapi.Repository) bool {
	for _, c := range repo.Status.Conditions {
		if c.Type == configapi.RepositoryReady && c.Status == "True" {
			return true
		}
	}
	return false
}

// repoCachePredicate filters events for the repo cache controller.
//   - Create: passed (startup resync + new repos — reconciler requeues until Ready)
//   - Update: passed only for generation change (spec) or DeletionTimestamp being set.
//     Status-only updates are filtered out to avoid re-opening on every health check.
//   - Delete: ignored (handled via finalizer when DeletionTimestamp is set)
//   - Generic: ignored
func repoCachePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld == nil || e.ObjectNew == nil {
				return false
			}
			// Already being deleted — we already handled this
			if e.ObjectOld.GetDeletionTimestamp() != nil {
				return false
			}
			// Spec changed — need to re-open
			if e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration() {
				return true
			}
			// DeletionTimestamp just set — need to evict
			if e.ObjectNew.GetDeletionTimestamp() != nil {
				return true
			}
			// Finalizer missing — need to add it (repo was opened by API call or missed Create)
			if !controllerutil.ContainsFinalizer(e.ObjectNew, repoCacheFinalizer) {
				return true
			}
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}
