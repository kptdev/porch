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
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilstrings "k8s.io/utils/strings"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
)

const (
	// maxPkgRevSyncFailuresInMessage caps how many failed PackageRevisions are
	// listed in the condition message; the rest are summarised as a count.
	maxPkgRevSyncFailuresInMessage = 3
	// maxPkgRevSyncErrorLen caps the length of each listed error.
	maxPkgRevSyncErrorLen = 256
)

// pkgRevSyncConditionUpdate tells a Repository status write what to do with the
// PackageRevisionSyncComplete condition. The zero value keeps the current condition
// unchanged, which is what every status write other than the end of a full sync uses.
type pkgRevSyncConditionUpdate struct {
	// condition, when non-nil, replaces the current condition.
	condition *metav1.Condition
	// remove drops the condition. Ignored when condition is set.
	remove bool
}

// setPkgRevSyncCondition returns an update that sets the condition.
func setPkgRevSyncCondition(condition metav1.Condition) pkgRevSyncConditionUpdate {
	return pkgRevSyncConditionUpdate{condition: &condition}
}

// removePkgRevSyncCondition returns an update that removes the condition.
func removePkgRevSyncCondition() pkgRevSyncConditionUpdate {
	return pkgRevSyncConditionUpdate{remove: true}
}

// resolve returns the PackageRevisionSyncComplete condition to write, or nil if
// none should be written. When setting, LastTransitionTime is preserved if the
// status is unchanged from the existing condition.
func (u pkgRevSyncConditionUpdate) resolve(existing []metav1.Condition) *metav1.Condition {
	current := meta.FindStatusCondition(existing, configapi.RepositoryPackageRevisionSyncComplete)
	switch {
	case u.condition != nil:
		var merged []metav1.Condition
		if current != nil {
			merged = []metav1.Condition{*current}
		}
		meta.SetStatusCondition(&merged, *u.condition)
		return &merged[0]
	case u.remove:
		return nil
	case current != nil:
		kept := *current
		return &kept
	default:
		return nil
	}
}

// pkgRevSyncResultCondition builds the condition for a PackageRevision sync that ran
// to completion: True when every PackageRevision synced, False when any failed.
func pkgRevSyncResultCondition(repo *configapi.Repository, result packageRevisionSyncResult) metav1.Condition {
	if len(result.Failures) == 0 {
		return newPkgRevSyncCondition(repo, metav1.ConditionTrue, configapi.ReasonSyncComplete,
			fmt.Sprintf("%d of %d PackageRevisions synced", result.Total, result.Total))
	}
	return newPkgRevSyncCondition(repo, metav1.ConditionFalse, configapi.ReasonPartialSyncFailure,
		formatPkgRevSyncFailures(result))
}

// pkgRevSyncFailedCondition builds the condition for a PackageRevision sync that
// could not run to completion.
func pkgRevSyncFailedCondition(repo *configapi.Repository, err error) metav1.Condition {
	return newPkgRevSyncCondition(repo, metav1.ConditionFalse, configapi.ReasonSyncFailed,
		"PackageRevision sync did not complete: "+utilstrings.ShortenString(err.Error(), maxPkgRevSyncErrorLen))
}

// pkgRevSyncDisabledCondition builds the condition for a repository annotated for
// v1alpha2 while the controller has v1alpha2 PackageRevision creation disabled.
func pkgRevSyncDisabledCondition(repo *configapi.Repository) metav1.Condition {
	return newPkgRevSyncCondition(repo, metav1.ConditionFalse, configapi.ReasonV1Alpha2SyncDisabled,
		fmt.Sprintf("repository is annotated with %s=%s but the controller is running with "+
			"--repositories.create-v1alpha2-rpkg=false; PackageRevisions are not being synced",
			configapi.AnnotationKeyV1Alpha2Migration, configapi.AnnotationValueMigrationEnabled))
}

func newPkgRevSyncCondition(repo *configapi.Repository, status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{
		Type:               configapi.RepositoryPackageRevisionSyncComplete,
		Status:             status,
		ObservedGeneration: repo.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

// formatPkgRevSyncFailures renders e.g.
// "13 of 18 PackageRevisions failed to sync: a: err; b: err; c: err; and 10 more".
// Failures are sorted by name so the message is stable across syncs.
func formatPkgRevSyncFailures(result packageRevisionSyncResult) string {
	failures := slices.Clone(result.Failures)
	slices.SortStableFunc(failures, func(a, b packageRevisionSyncFailure) int {
		return strings.Compare(a.Name, b.Name)
	})

	shown := min(len(failures), maxPkgRevSyncFailuresInMessage)
	details := make([]string, 0, shown+1)
	for _, f := range failures[:shown] {
		details = append(details, fmt.Sprintf("%s: %s", f.Name, utilstrings.ShortenString(f.Err.Error(), maxPkgRevSyncErrorLen)))
	}
	if rest := len(failures) - shown; rest > 0 {
		details = append(details, fmt.Sprintf("and %d more", rest))
	}

	return fmt.Sprintf("%d of %d PackageRevisions failed to sync: %s",
		len(failures), result.Total, strings.Join(details, "; "))
}
