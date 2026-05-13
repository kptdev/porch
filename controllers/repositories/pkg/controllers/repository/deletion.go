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

package repository

import (
	"context"
	"fmt"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
)

// handleDeletion handles Repository deletion with timeout protection
func (r *RepositoryReconciler) handleDeletion(ctx context.Context, repo *configapi.Repository) (ctrl.Result, error) {
	log.FromContext(ctx).Info("Repository deletion started", "repository", repo.Name)

	deleteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	allRepos, err := r.getAllRepositories(deleteCtx)
	if err != nil {
		if deleteCtx.Err() == context.DeadlineExceeded {
			log.FromContext(ctx).Error(err, "Repository deletion timed out", "repository", repo.Name)
			return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
		}
		return ctrl.Result{}, err
	}

	if err := r.cleanupRepositoryCache(deleteCtx, repo, allRepos); err != nil {
		if deleteCtx.Err() == context.DeadlineExceeded {
			log.FromContext(ctx).Error(err, "Cache cleanup timed out", "repository", repo.Name)
			return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
		}
		log.FromContext(ctx).Error(err, "Cache cleanup failed, will retry", "repository", repo.Name)
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	if err := r.removeFinalizer(ctx, repo); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// getAllRepositories retrieves all repositories from the cluster
func (r *RepositoryReconciler) getAllRepositories(ctx context.Context) ([]configapi.Repository, error) {
	allRepos := &configapi.RepositoryList{}
	if err := r.List(ctx, allRepos); err != nil {
		return nil, fmt.Errorf("failed to list repositories: %w", err)
	}
	return allRepos.Items, nil
}

// cleanupRepositoryCache closes the repository in the cache
func (r *RepositoryReconciler) cleanupRepositoryCache(ctx context.Context, repo *configapi.Repository, allRepos []configapi.Repository) error {
	log := log.FromContext(ctx)
	log.Info("Closing repository cache", "repository", repo.Name, "totalRepos", len(allRepos))

	if err := r.Cache.CloseRepository(ctx, repo, allRepos); err != nil {
		log.Error(err, "Failed to close repository cache", "repository", repo.Name)
		return err
	}
	log.Info("Repository cache closed successfully", "repository", repo.Name)
	return nil
}

// removeFinalizer removes the repository finalizer and updates the object
func (r *RepositoryReconciler) removeFinalizer(ctx context.Context, repo *configapi.Repository) error {
	controllerutil.RemoveFinalizer(repo, RepositoryFinalizer)
	return r.Update(ctx, repo)
}
