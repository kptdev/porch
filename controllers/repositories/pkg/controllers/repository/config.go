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
	"flag"
	"time"

	cachetypes "github.com/kptdev/porch/pkg/cache/types"
)

const (
	defaultMaxConcurrentReconciles    = 100
	defaultMaxConcurrentSyncs         = 50
	defaultHealthCheckFrequency       = 5 * time.Minute
	defaultFullSyncFrequency          = 1 * time.Hour
	defaultSyncStaleTimeout           = 20 * time.Minute
	defaultRepoOperationRetryAttempts = 3
	defaultCacheDirectory             = "/cache"
)

// InitDefaults initializes default values for standalone controller
func (r *RepositoryReconciler) InitDefaults() {
	r.MaxConcurrentReconciles = defaultMaxConcurrentReconciles
	r.MaxConcurrentSyncs = defaultMaxConcurrentSyncs
	r.HealthCheckFrequency = defaultHealthCheckFrequency
	r.FullSyncFrequency = defaultFullSyncFrequency
	r.SyncStaleTimeout = defaultSyncStaleTimeout
	r.RepoOperationRetryAttempts = defaultRepoOperationRetryAttempts
	r.cacheType = string(cachetypes.DBCacheType)
	r.cacheDirectory = defaultCacheDirectory
	r.validateConfig()
}

// BindFlags binds controller-specific command line flags
func (r *RepositoryReconciler) BindFlags(prefix string, flags *flag.FlagSet) {
	flags.IntVar(&r.MaxConcurrentReconciles, prefix+"max-concurrent-reconciles", defaultMaxConcurrentReconciles, "Maximum number of concurrent repository reconciles")
	flags.IntVar(&r.MaxConcurrentSyncs, prefix+"max-concurrent-syncs", defaultMaxConcurrentSyncs, "Maximum number of concurrent sync operations")
	flags.StringVar(&r.cacheType, prefix+"cache-type", string(cachetypes.DBCacheType), "Cache type (DB or CR)")
	flags.StringVar(&r.cacheDirectory, prefix+"cache-directory", defaultCacheDirectory, "Directory for git repository cache")
	flags.DurationVar(&r.HealthCheckFrequency, prefix+"health-check-frequency", defaultHealthCheckFrequency, "Frequency of repository health checks")
	flags.DurationVar(&r.FullSyncFrequency, prefix+"full-sync-frequency", defaultFullSyncFrequency, "Frequency of full repository sync")
	flags.DurationVar(&r.SyncStaleTimeout, prefix+"sync-stale-timeout", defaultSyncStaleTimeout, "Timeout for considering a sync stale")
	flags.IntVar(&r.RepoOperationRetryAttempts, prefix+"repo-operation-retry-attempts", defaultRepoOperationRetryAttempts, "Number of retry attempts for git operations")
	flags.BoolVar(&r.useUserDefinedCaBundle, prefix+"use-user-defined-ca-bundle", false, "Enable custom CA bundle support from secrets")
	flags.BoolVar(&r.CreateV1Alpha2Rpkg, prefix+"create-v1alpha2-rpkg", false, "Create v1alpha2 PackageRevision resources during repository sync")
	flags.BoolVar(&r.PushDraftsToGit, prefix+"push-drafts-to-git", false, "Push draft and proposed branches to git when using DB cache")
}

// validateConfig ensures configuration values are valid
func (r *RepositoryReconciler) validateConfig() {
	if r.HealthCheckFrequency <= 0 {
		r.HealthCheckFrequency = defaultHealthCheckFrequency
	}
	if r.FullSyncFrequency <= 0 {
		r.FullSyncFrequency = defaultFullSyncFrequency
	}
	if r.SyncStaleTimeout <= 0 {
		r.SyncStaleTimeout = defaultSyncStaleTimeout
	}
	if r.MaxConcurrentReconciles <= 0 {
		r.MaxConcurrentReconciles = defaultMaxConcurrentReconciles
	}
	if r.MaxConcurrentSyncs <= 0 {
		r.MaxConcurrentSyncs = defaultMaxConcurrentSyncs
	}
	if r.RepoOperationRetryAttempts <= 0 {
		r.RepoOperationRetryAttempts = defaultRepoOperationRetryAttempts
	}
}

// LogConfig logs the controller configuration
func (r *RepositoryReconciler) LogConfig(log interface {
	Info(msg string, keysAndValues ...any)
}) {
	log.Info("Repository controller configuration",
		"healthCheckFrequency", r.HealthCheckFrequency,
		"fullSyncFrequency", r.FullSyncFrequency,
		"maxConcurrentReconciles", r.MaxConcurrentReconciles,
		"maxConcurrentSyncs", r.MaxConcurrentSyncs,
		"syncStaleTimeout", r.SyncStaleTimeout,
		"repoOperationRetryAttempts", r.RepoOperationRetryAttempts,
		"cacheType", r.cacheType,
		"cacheDirectory", r.cacheDirectory,
		"createV1Alpha2Rpkg", r.CreateV1Alpha2Rpkg,
		"pushDraftsToGit", r.PushDraftsToGit)

	if r.HealthCheckFrequency < defaultHealthCheckFrequency {
		log.Info("Health check frequency is lower than recommended default",
			"configured", r.HealthCheckFrequency,
			"recommended", defaultHealthCheckFrequency)
	}
	if r.FullSyncFrequency < defaultFullSyncFrequency {
		log.Info("Full sync frequency is lower than recommended default",
			"configured", r.FullSyncFrequency,
			"recommended", defaultFullSyncFrequency)
	}
}
