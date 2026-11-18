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
	"encoding/json"
	"fmt"
	"time"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/repositories/pkg/webhooks"
	secretutil "github.com/kptdev/porch/pkg/util/secret"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	defaultMaxConcurrentReconciles    = 200
	defaultMaxConcurrentSyncs         = 100
	defaultHealthCheckFrequency       = 5 * time.Minute
	defaultFullSyncFrequency          = 1 * time.Hour
	defaultSyncStaleTimeout           = 20 * time.Minute
	defaultRepoOperationRetryAttempts = 3
)

// validateConfig restores defaults for values that cannot be zero or negative.
func (r *RepositoryReconciler) validateConfig() {
	if r.Cfg.HealthCheckFrequency.Duration <= 0 {
		r.Cfg.HealthCheckFrequency.Duration = defaultHealthCheckFrequency
	}
	if r.Cfg.FullSyncFrequency.Duration <= 0 {
		r.Cfg.FullSyncFrequency.Duration = defaultFullSyncFrequency
	}
	if r.Cfg.SyncStaleTimeout.Duration <= 0 {
		r.Cfg.SyncStaleTimeout.Duration = defaultSyncStaleTimeout
	}
	if r.Cfg.MaxConcurrentReconciles <= 0 {
		r.Cfg.MaxConcurrentReconciles = defaultMaxConcurrentReconciles
	}
	if r.Cfg.MaxConcurrentSyncs <= 0 {
		r.Cfg.MaxConcurrentSyncs = defaultMaxConcurrentSyncs
	}
	if r.Cfg.RepoOperationRetryAttempts <= 0 {
		r.Cfg.RepoOperationRetryAttempts = defaultRepoOperationRetryAttempts
	}
}

// Init wires runtime dependencies that require the manager.
// It registers the Repository validating webhook on the shared webhook server
// and sets up field indexes for efficient conflict detection queries.
func (r *RepositoryReconciler) Init(mgr ctrl.Manager) error {
	r.validateConfig()
	log := ctrl.Log.WithName(r.Name())

	// Set up field indexes for Repository conflict detection
	// These indexes allow efficient querying by git location without listing all repositories
	ctx := context.Background()
	if err := mgr.GetFieldIndexer().IndexField(ctx, &configapi.Repository{}, "spec.git.repo", func(o client.Object) []string {
		repository := o.(*configapi.Repository)
		if repository.Spec.Git == nil || repository.Spec.Git.Repo == "" {
			return nil
		}
		return []string{repository.Spec.Git.Repo}
	}); err != nil {
		return fmt.Errorf("error indexing Repository by git.repo: %w", err)
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &configapi.Repository{}, "spec.git.branch", func(o client.Object) []string {
		repository := o.(*configapi.Repository)
		if repository.Spec.Git == nil || repository.Spec.Git.Branch == "" {
			return nil
		}
		return []string{repository.Spec.Git.Branch}
	}); err != nil {
		return fmt.Errorf("error indexing Repository by git.branch: %w", err)
	}

	// Register Repository validating webhook.
	// The validator implements admission.Handler interface via its Handle method.
	// Use GetAPIReader() for strong consistency during admission validation.
	// See: deployments/porch/3-porch-controllers.yaml
	validator := webhooks.NewRepositoryValidator(mgr.GetAPIReader())

	mgr.GetWebhookServer().Register(
		"/validate-repository",
		&admission.Webhook{
			Handler: validator,
		})
	log.Info("Repository validating webhook registered")

	return nil
}

// LogConfig logs the controller configuration
func (r *RepositoryReconciler) LogConfig(log interface {
	Info(msg string, keysAndValues ...any)
}) {
	loggableConfig := r.Cfg
	secretutil.RedactDataSourcePassword(&loggableConfig.DB.DataSource)
	if pw := &loggableConfig.Database.Password; *pw != "" {
		*pw = "***"
	}
	confstr, _ := json.MarshalIndent(loggableConfig, "", "  ")
	log.Info(fmt.Sprintf("Repository controller configuration:\n%s", confstr))

	if r.Cfg.HealthCheckFrequency.Duration < defaultHealthCheckFrequency {
		log.Info("Health check frequency is lower than recommended default",
			"configured", r.Cfg.HealthCheckFrequency.Duration,
			"recommended", defaultHealthCheckFrequency)
	}
	if r.Cfg.FullSyncFrequency.Duration < defaultFullSyncFrequency {
		log.Info("Full sync frequency is lower than recommended default",
			"configured", r.Cfg.FullSyncFrequency.Duration,
			"recommended", defaultFullSyncFrequency)
	}
}
