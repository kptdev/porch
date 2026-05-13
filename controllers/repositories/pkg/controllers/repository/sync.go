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
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/robfig/cron/v3"
)

// OperationType defines the type of operation
type OperationType int

const (
	OperationHealthCheck OperationType = iota // Lightweight connectivity check
	OperationFullSync                         // Complete data synchronization
)

// SyncDecision represents what type of operation is needed
type SyncDecision struct {
	Type                  OperationType
	SyncNecessary         bool
	UseExponentialBackoff bool          // Use controller backoff instead of fixed interval
	DelayBeforeNextSync   time.Duration // Requeue interval (ignored if UseExponentialBackoff=true)
}

// SyncResult contains the result of a sync operation
type SyncResult struct {
	Status       RepositoryStatus
	Error        error
	NextSyncTime *time.Time
}

// Sync handles repository synchronization operations
type Sync struct {
	reconciler *RepositoryReconciler
}

// HandleSync performs the sync operation and returns the result
func (s *Sync) HandleSync(ctx context.Context, repo *api.Repository) SyncResult {
	log := log.FromContext(ctx)

	packageCount, commitHash, err := s.reconciler.syncRepository(ctx, repo)

	if err != nil {
		repoURL, _, _ := getRepoFields(repo)
		log.Error(err, "Repository sync failed", "repoURL", repoURL)
		retryInterval := s.reconciler.determineRetryInterval(err)
		next := time.Now().Add(retryInterval)
		return SyncResult{Status: RepositoryStatusError, Error: err, NextSyncTime: &next}
	}

	// Sync succeeded
	syncReason := "scheduled"
	if repo.Spec.Sync != nil && repo.Spec.Sync.RunOnceAt != nil {
		syncReason = "runOnceAt"
	} else if repo.Spec.Sync != nil && repo.Spec.Sync.Schedule != "" {
		syncReason = "cron:" + repo.Spec.Sync.Schedule
	}
	log.Info("Repository full sync completed successfully", "trigger", syncReason)

	// Mark this repo as synced since startup (cold start complete)
	repoKey := repo.Namespace + "/" + repo.Name
	s.reconciler.coldStartRepos.Store(repoKey, true)

	// Update status fields
	now := metav1.Now()
	repo.Status.LastFullSyncTime = &now
	repo.Status.ObservedGeneration = repo.Generation
	if repo.Spec.Sync != nil && repo.Spec.Sync.RunOnceAt != nil {
		repo.Status.ObservedRunOnceAt = repo.Spec.Sync.RunOnceAt
	} else if repo.Status.ObservedRunOnceAt != nil {
		repo.Status.ObservedRunOnceAt = nil
	}
	repo.Status.PackageCount = packageCount
	repo.Status.GitCommitHash = commitHash

	next := s.reconciler.calculateNextFullSyncTime(repo)
	repo.Status.NextFullSyncTime = &metav1.Time{Time: next}

	return SyncResult{Status: RepositoryStatusReady, Error: nil, NextSyncTime: &next}
}

// PerformAsyncSync performs repository sync in background goroutine
func (s *Sync) PerformAsyncSync(ctx context.Context, repo *api.Repository) {
	log := log.FromContext(ctx)

	result := s.HandleSync(ctx, repo)

	if statusErr := s.reconciler.updateRepoStatusWithBackoff(ctx, repo, result.Status, result.Error, result.NextSyncTime); statusErr != nil {
		if client.IgnoreNotFound(statusErr) == nil {
			log.V(1).Info("Repository deleted during sync")
			return
		}
		log.Error(statusErr, "Failed to update repository status after sync")
		return
	}

	if result.Error == nil && repo.Spec.Sync != nil && repo.Spec.Sync.RunOnceAt != nil {
		if clearErr := s.reconciler.clearOneTimeSyncFlag(ctx, repo); clearErr != nil && client.IgnoreNotFound(clearErr) != nil {
			log.Error(clearErr, "Failed to clear one-time sync flag (non-critical)")
		}
	}
}

// syncRepository performs repository synchronization and returns sync metadata
func (r *RepositoryReconciler) syncRepository(ctx context.Context, repo *api.Repository) (packageCount int, commitHash string, err error) {
	log := log.FromContext(ctx)

	repoHandle, err := r.Cache.OpenRepository(ctx, repo)
	if err != nil {
		repoURL, _, _ := getRepoFields(repo)
		log.Error(err, "Failed to open repository for sync", "repo", repo.Name, "repoURL", repoURL)
		return 0, "", err
	}

	if err := repoHandle.Refresh(ctx); err != nil {
		repoURL, branch, _ := getRepoFields(repo)
		log.Error(err, "Repository refresh failed", "repo", repo.Name, "repoURL", repoURL, "branch", branch)
		return 0, "", err
	}

	pkgRevs, err := repoHandle.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		repoURL, _, directory := getRepoFields(repo)
		log.Error(err, "Repository package listing failed", "repo", repo.Name, "repoURL", repoURL, "directory", directory)
		return 0, "", err
	}

	if r.CreateV1Alpha2Rpkg && repo.Annotations[api.AnnotationKeyV1Alpha2Migration] == api.AnnotationValueMigrationEnabled {
		if err := r.syncPackageRevisions(ctx, repo, pkgRevs); err != nil {
			log.Error(err, "Failed to sync v1alpha2 PackageRevisions", "repo", repo.Name)
			// Non-fatal: don't fail the entire sync for v1alpha2 creation failures
		}
	}

	// Get commit hash (best effort - don't fail sync if this fails)
	commitHash, _ = repoHandle.BranchCommitHash(ctx)

	return len(pkgRevs), commitHash, nil
}

// determineSyncDecision decides what operation is needed and when to requeue
func (r *RepositoryReconciler) determineSyncDecision(ctx context.Context, repo *api.Repository) SyncDecision {
	// Don't start new operations if already in progress
	if r.isSyncInProgress(ctx, repo) {
		// Use exponential backoff to avoid tight loops while waiting for sync to complete
		return SyncDecision{Type: OperationHealthCheck, SyncNecessary: false, UseExponentialBackoff: true}
	}

	// After a controller restart the git cache is cold but LastFullSyncTime
	// may still be recent (persisted in etcd by the previous pod). Force a
	// full sync for each repo on the first reconcile to re-warm the cache.
	// Note: with many repos (1000+), the syncLimiter bounds concurrency
	// (MaxConcurrentSyncs) so repos sync in batches, not all at once.
	repoKey := repo.Namespace + "/" + repo.Name
	if _, synced := r.coldStartRepos.Load(repoKey); !synced {
		log.FromContext(ctx).Info("Cold start: forcing full sync to re-warm cache")
		return SyncDecision{Type: OperationFullSync, SyncNecessary: true, DelayBeforeNextSync: 0}
	}

	switch {
	case r.isOneTimeSyncDue(repo):
		// 1. One-time sync due → Full sync
		log.FromContext(ctx).Info("RunOnceAt sync triggered", "scheduledTime", repo.Spec.Sync.RunOnceAt.Format(time.RFC3339))
		return SyncDecision{Type: OperationFullSync, SyncNecessary: true, DelayBeforeNextSync: 0}

	case r.hasSpecChanged(repo):
		// 2. Spec changed → Check if only runOnceAt changed
		specRunOnceAt := getRunOnceAt(repo)
		statusRunOnceAt := repo.Status.ObservedRunOnceAt

		// If only runOnceAt changed (not other spec fields), update ObservedGeneration and skip sync
		// The sync will trigger when the scheduled time arrives via isOneTimeSyncDue
		if !equalTimes(specRunOnceAt, statusRunOnceAt) {
			// Just update ObservedGeneration to acknowledge we've seen the spec change
			// Don't update ObservedRunOnceAt yet - that happens after sync completes
			repo.Status.ObservedGeneration = repo.Generation
			return SyncDecision{Type: OperationHealthCheck, SyncNecessary: false, DelayBeforeNextSync: r.getRequeueInterval(repo)}
		}

		// runOnceAt didn't change, other spec fields changed → Full sync
		return SyncDecision{Type: OperationFullSync, SyncNecessary: true, DelayBeforeNextSync: 0}

	case r.isErrorRetryDue(repo):
		// 3. Error retry due → Health check (not full sync)
		return SyncDecision{Type: OperationHealthCheck, SyncNecessary: true, DelayBeforeNextSync: 0}

	case r.isFullSyncDue(repo):
		// 4. Full sync due → Full sync (only if not in error state)
		return SyncDecision{Type: OperationFullSync, SyncNecessary: true, DelayBeforeNextSync: 0}

	case r.isHealthCheckDue(repo):
		// 5. Health check due → Health check
		return SyncDecision{Type: OperationHealthCheck, SyncNecessary: true, DelayBeforeNextSync: 0}

	default:
		// Nothing needed, return next check interval
		return SyncDecision{Type: OperationHealthCheck, SyncNecessary: false, DelayBeforeNextSync: r.getRequeueInterval(repo)}
	}
}

// isSyncInProgress checks if repository sync is currently in progress
func (r *RepositoryReconciler) isSyncInProgress(ctx context.Context, repo *api.Repository) bool {
	for _, condition := range repo.Status.Conditions {
		if condition.Type == api.RepositoryReady && condition.Status == metav1.ConditionFalse && condition.Reason == api.ReasonReconciling {
			staleTimeout := r.getSyncStaleTimeout()
			if time.Since(condition.LastTransitionTime.Time) > staleTimeout {
				repoURL, _, _ := getRepoFields(repo)
				log.FromContext(ctx).Info("Sync appears stale, will retry", "duration", time.Since(condition.LastTransitionTime.Time), "timeout", staleTimeout, "repoURL", repoURL)
				return false
			}
			return true
		}
	}
	return false
}

// isOneTimeSyncDue checks if one-time sync is scheduled and due
func (r *RepositoryReconciler) isOneTimeSyncDue(repo *api.Repository) bool {
	if repo.Spec.Sync == nil || repo.Spec.Sync.RunOnceAt == nil {
		return false
	}
	// Only trigger if time has passed AND we haven't already observed this value
	if !time.Now().After(repo.Spec.Sync.RunOnceAt.Time) {
		return false
	}
	return !equalTimes(repo.Spec.Sync.RunOnceAt, repo.Status.ObservedRunOnceAt)
}

// isErrorRetryDue checks if error retry time has elapsed
func (r *RepositoryReconciler) isErrorRetryDue(repo *api.Repository) bool {
	for _, condition := range repo.Status.Conditions {
		if condition.Type == api.RepositoryReady && condition.Status == metav1.ConditionFalse && condition.Reason == api.ReasonError {
			if condition.Message != "" {
				err := fmt.Errorf("%s", condition.Message)
				retryInterval := r.determineRetryInterval(err)
				return time.Since(condition.LastTransitionTime.Time) >= retryInterval
			}
		}
	}
	return false
}

// isFullSyncDue checks if full sync is needed
func (r *RepositoryReconciler) isFullSyncDue(repo *api.Repository) bool {
	if repo.Spec.Sync != nil && repo.Spec.Sync.RunOnceAt != nil && time.Now().Before(repo.Spec.Sync.RunOnceAt.Time) {
		return false
	}

	// Don't attempt full sync if repository is in error state
	for _, condition := range repo.Status.Conditions {
		if condition.Type == api.RepositoryReady && condition.Status == metav1.ConditionFalse && condition.Reason == api.ReasonError {
			return false
		}
	}

	lastFullSync := r.getLastFullSyncTime(repo)
	if lastFullSync.IsZero() {
		log.FromContext(context.Background()).Info("Full sync due: no previous sync")
		return true
	}

	if repo.Spec.Sync != nil && repo.Spec.Sync.Schedule != "" {
		schedule, err := cron.ParseStandard(repo.Spec.Sync.Schedule)
		if err != nil {
			log.FromContext(context.Background()).Error(err, "Invalid cron expression, using default frequency", "schedule", repo.Spec.Sync.Schedule)
			return time.Since(lastFullSync) >= r.FullSyncFrequency
		}
		next := schedule.Next(lastFullSync)
		isDue := time.Now().After(next)
		log.FromContext(context.Background()).V(2).Info("Cron schedule check", "schedule", repo.Spec.Sync.Schedule, "lastFullSync", lastFullSync.Format(time.RFC3339), "nextScheduled", next.Format(time.RFC3339), "now", time.Now().Format(time.RFC3339), "isDue", isDue)
		return isDue
	}

	timeSinceLastSync := time.Since(lastFullSync)
	isDue := timeSinceLastSync >= r.FullSyncFrequency
	log.FromContext(context.Background()).V(2).Info("Full sync check", "lastFullSync", lastFullSync.Format(time.RFC3339), "timeSince", timeSinceLastSync, "frequency", r.FullSyncFrequency, "isDue", isDue)
	return isDue
}

// isHealthCheckDue checks if health check is needed
func (r *RepositoryReconciler) isHealthCheckDue(repo *api.Repository) bool {
	// Always perform health checks when in error state (handled by isErrorRetryDue)
	// This function only checks if routine health check is due for healthy repos
	for _, condition := range repo.Status.Conditions {
		if condition.Type == api.RepositoryReady && condition.Status == metav1.ConditionFalse && condition.Reason == api.ReasonError {
			return false
		}
	}

	lastUpdate := r.getLastStatusUpdateTime(repo)
	if lastUpdate.IsZero() {
		return true
	}
	return time.Since(lastUpdate) >= r.HealthCheckFrequency
}

// getLastStatusUpdateTime gets the most recent status update time
func (r *RepositoryReconciler) getLastStatusUpdateTime(repo *api.Repository) time.Time {
	if len(repo.Status.Conditions) == 0 {
		return time.Time{}
	}
	for _, condition := range repo.Status.Conditions {
		if condition.Type == api.RepositoryReady {
			return condition.LastTransitionTime.Time
		}
	}
	return time.Time{}
}

// getLastFullSyncTime gets timestamp of last successful full sync
func (r *RepositoryReconciler) getLastFullSyncTime(repo *api.Repository) time.Time {
	if repo.Status.LastFullSyncTime != nil {
		return repo.Status.LastFullSyncTime.Time
	}
	return time.Time{}
}

// getSyncStaleTimeout returns the timeout for considering a sync stale
func (r *RepositoryReconciler) getSyncStaleTimeout() time.Duration {
	if r.SyncStaleTimeout > 0 {
		return r.SyncStaleTimeout
	}
	return 20 * time.Minute
}

// getRequeueInterval determines appropriate requeue interval based on repository status
func (r *RepositoryReconciler) getRequeueInterval(repo *api.Repository) time.Duration {
	for _, condition := range repo.Status.Conditions {
		if condition.Type == api.RepositoryReady && condition.Status == metav1.ConditionFalse && condition.Reason == api.ReasonError {
			if condition.Message != "" {
				err := fmt.Errorf("%s", condition.Message)
				return r.determineRetryInterval(err)
			}
			return 30 * time.Second
		}
	}
	return r.calculateNextSyncInterval(repo)
}

// calculateNextSyncInterval determines when to requeue for next sync
func (r *RepositoryReconciler) calculateNextSyncInterval(repo *api.Repository) time.Duration {
	// If runOnceAt is set and in the future, wait until then (even if no status)
	if repo.Spec.Sync != nil && repo.Spec.Sync.RunOnceAt != nil {
		untilRunOnce := time.Until(repo.Spec.Sync.RunOnceAt.Time)
		if untilRunOnce > 0 {
			return untilRunOnce
		}
	}

	// If no status exists yet, use health check frequency to avoid tight loops
	lastUpdate := r.getLastStatusUpdateTime(repo)
	if lastUpdate.IsZero() {
		return r.HealthCheckFrequency
	}

	// If runOnceAt is in the past but still set, return small interval
	if repo.Spec.Sync != nil && repo.Spec.Sync.RunOnceAt != nil {
		// runOnceAt is in the past but still set - it will trigger sync soon
		return 1 * time.Second
	}

	nextHealthCheck := r.HealthCheckFrequency - time.Since(lastUpdate)
	if nextHealthCheck <= 0 {
		nextHealthCheck = r.HealthCheckFrequency
	}

	lastFullSync := r.getLastFullSyncTime(repo)
	nextFullSync := r.FullSyncFrequency
	if !lastFullSync.IsZero() {
		nextFullSync = r.FullSyncFrequency - time.Since(lastFullSync)
		if nextFullSync <= 0 {
			nextFullSync = r.FullSyncFrequency
		}
	}

	if repo.Spec.Sync != nil && repo.Spec.Sync.Schedule != "" {
		schedule, err := cron.ParseStandard(repo.Spec.Sync.Schedule)
		if err != nil {
			log.FromContext(context.Background()).Error(err, "Invalid cron expression, ignoring schedule", "schedule", repo.Spec.Sync.Schedule)
		} else {
			lastSyncTime := r.getLastFullSyncTime(repo)
			if lastSyncTime.IsZero() {
				lastSyncTime = time.Now()
			}
			next := schedule.Next(lastSyncTime)
			nextScheduled := time.Until(next)
			if nextScheduled > 0 && nextScheduled < nextFullSync {
				nextFullSync = nextScheduled
			}
		}
	}

	// Return the minimum of health check and full sync intervals
	if nextHealthCheck < nextFullSync {
		return nextHealthCheck
	}
	return nextFullSync
}

// determineRetryInterval returns appropriate retry interval based on error type
func (r *RepositoryReconciler) determineRetryInterval(err error) time.Duration {
	errStr := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errStr, "no such host"), strings.Contains(errStr, "connection refused"):
		return 30 * time.Second
	case strings.Contains(errStr, "authentication"), strings.Contains(errStr, "permission denied"),
		strings.Contains(errStr, "failed to resolve credentials"), strings.Contains(errStr, "resolved credentials are invalid"):
		return 10 * time.Minute
	case strings.Contains(errStr, "not found"), strings.Contains(errStr, "invalid"), strings.Contains(errStr, "branch"):
		return 2 * time.Minute
	case strings.Contains(errStr, "timeout"), strings.Contains(errStr, "deadline exceeded"):
		return 1 * time.Minute
	case strings.Contains(errStr, "certificate"), strings.Contains(errStr, "tls"), strings.Contains(errStr, "ssl"):
		return 5 * time.Minute
	case strings.Contains(errStr, "rate limit"), strings.Contains(errStr, "too many requests"):
		return 5 * time.Minute
	default:
		return 30 * time.Second
	}
}

// clearOneTimeSyncFlag clears the RunOnceAt flag after successful one-time sync
func (r *RepositoryReconciler) clearOneTimeSyncFlag(ctx context.Context, repo *api.Repository) error {
	if repo.Spec.Sync == nil || repo.Spec.Sync.RunOnceAt == nil {
		return nil
	}

	patch := client.MergeFrom(repo.DeepCopy())
	repo.Spec.Sync.RunOnceAt = nil
	return r.Patch(ctx, repo, patch)
}

// calculateNextFullSyncTime calculates when the next full sync should occur
// This must match the logic in isFullSyncDue to show accurate status
func (r *RepositoryReconciler) calculateNextFullSyncTime(repo *api.Repository) time.Time {
	if repo.Spec.Sync != nil && repo.Spec.Sync.Schedule != "" {
		if schedule, err := cron.ParseStandard(repo.Spec.Sync.Schedule); err == nil {
			lastSync := r.getLastFullSyncTime(repo)
			if lastSync.IsZero() {
				lastSync = time.Now()
			}
			// Use same logic as isFullSyncDue: next run after last sync
			return schedule.Next(lastSync)
		}
	}
	return time.Now().Add(r.FullSyncFrequency)
}

// getRunOnceAt safely extracts runOnceAt from repository spec
func getRunOnceAt(repo *api.Repository) *metav1.Time {
	if repo.Spec.Sync != nil && repo.Spec.Sync.RunOnceAt != nil {
		return repo.Spec.Sync.RunOnceAt
	}
	return nil
}

// equalTimes compares two metav1.Time pointers for equality
func equalTimes(t1, t2 *metav1.Time) bool {
	if t1 == nil && t2 == nil {
		return true
	}
	if t1 == nil || t2 == nil {
		return false
	}
	return t1.Equal(t2)
}

// InitializeSyncLimiter initializes the sync limiter for testing
func (r *RepositoryReconciler) InitializeSyncLimiter() {
	if r.MaxConcurrentSyncs <= 0 {
		r.MaxConcurrentSyncs = 100 // Default limit
	}
	r.syncLimiter = make(chan struct{}, r.MaxConcurrentSyncs)
}
