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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
)

// RepositoryStatus represents the status of a repository sync operation
type RepositoryStatus string

const (
	RepositoryStatusSyncInProgress RepositoryStatus = "sync-in-progress"
	RepositoryStatusReady          RepositoryStatus = "ready"
	RepositoryStatusError          RepositoryStatus = "error"
)

// buildRepositoryCondition builds a repository condition for the given status.
func buildRepositoryCondition(repo *configapi.Repository, status RepositoryStatus, errorMsg string, nextSyncTime *time.Time) (metav1.Condition, error) {
	switch status {
	case RepositoryStatusSyncInProgress:
		return metav1.Condition{
			Type:               configapi.RepositoryReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: repo.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             configapi.ReasonReconciling,
			Message:            "Repository reconciliation in progress",
		}, nil
	case RepositoryStatusReady:
		return metav1.Condition{
			Type:               configapi.RepositoryReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: repo.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             configapi.ReasonReady,
			Message:            "Repository Ready",
		}, nil
	case RepositoryStatusError:
		if errorMsg == "" {
			errorMsg = "unknown error"
		}
		return metav1.Condition{
			Type:               configapi.RepositoryReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: repo.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             configapi.ReasonError,
			Message:            errorMsg,
		}, nil
	default:
		return metav1.Condition{}, fmt.Errorf("unknown status type: %s", status)
	}
}

// updateRepoStatusWithBackoff updates status with retry logic using Server-Side Apply
func (r *RepositoryReconciler) updateRepoStatusWithBackoff(ctx context.Context, repo *configapi.Repository, status RepositoryStatus, syncError error, nextSyncTime *time.Time) error {
	errorMsg := ""
	if syncError != nil {
		errorMsg = syncError.Error()
	}
	condition, buildErr := buildRepositoryCondition(repo, status, errorMsg, nextSyncTime)
	if buildErr != nil {
		log.FromContext(ctx).Error(buildErr, "Failed to build repository condition", "repository", repo.Name)
		return buildErr
	}

	// Create status patch with Server-Side Apply
	patch := &configapi.Repository{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configapi.TypeRepository.APIVersion(),
			Kind:       configapi.TypeRepository.Kind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      repo.Name,
			Namespace: repo.Namespace,
		},
		Status: configapi.RepositoryStatus{
			Conditions:         []metav1.Condition{condition},
			LastFullSyncTime:   repo.Status.LastFullSyncTime,
			ObservedGeneration: repo.Status.ObservedGeneration,
			ObservedRunOnceAt:  repo.Status.ObservedRunOnceAt,
			PackageCount:       repo.Status.PackageCount,
			GitCommitHash:      repo.Status.GitCommitHash,
			NextFullSyncTime:   repo.Status.NextFullSyncTime,
		},
	}

	return wait.ExponentialBackoff(wait.Backoff{
		Duration: 25 * time.Millisecond,
		Factor:   1.5,
		Jitter:   0.1,
		Steps:    2,
	}, func() (bool, error) {
		if err := r.Status().Patch(ctx, patch, client.Apply, client.FieldOwner("repository-controller"), client.ForceOwnership); err != nil {
			if errors.IsConflict(err) {
				return false, nil // Retry
			}
			return false, err
		}
		return true, nil
	})
}

// setCondition sets Repository condition
func (r *RepositoryReconciler) setCondition(repo *configapi.Repository, conditionType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: repo.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
	meta.SetStatusCondition(&repo.Status.Conditions, condition)
}

// hasSpecChanged determines if Repository spec changed using status ObservedGeneration
func (r *RepositoryReconciler) hasSpecChanged(repo *configapi.Repository) bool {
	return repo.Status.ObservedGeneration != repo.Generation
}
