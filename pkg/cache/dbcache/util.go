// Copyright 2024-2025 The kpt Authors
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

package dbcache

import (
	"encoding/json"
	"os/user"
	"sync"

	kptfile "github.com/kptdev/kpt/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"k8s.io/klog/v2"
)

const unpushedGitCommit = "not-pushed"

func getCurrentUser() string {
	currentUser, err := user.Current()
	if err == nil {
		return currentUser.Username
	} else {
		return "undefined"
	}
}

func valueAsJSON(value any) string {
	if jsonValue, err := json.Marshal(value); err == nil {
		return string(jsonValue)
	} else {
		klog.Errorf("marshal of value %v failed, %v ", value, err)
		return ""
	}
}

func setValueFromJSON(jsonValue string, value any) {
	if err := json.Unmarshal([]byte(jsonValue), value); err != nil {
		klog.Errorf("unmarshal of json value %v failed, %v ", jsonValue, err)
	}
}

type keyedMutex struct {
	mu   sync.Mutex
	refs int
}

type lockManager struct {
	mu    sync.Mutex
	locks map[string]*keyedMutex
}

var globalLockManager = &lockManager{
	locks: make(map[string]*keyedMutex),
}

func (lm *lockManager) lockKey(key string) {
	lm.mu.Lock()
	km, exists := lm.locks[key]
	if !exists {
		km = &keyedMutex{}
		lm.locks[key] = km
	}
	km.refs++
	lm.mu.Unlock()

	km.mu.Lock()
}

func (lm *lockManager) unlockKey(key string) {
	lm.mu.Lock()
	km := lm.locks[key]
	km.mu.Unlock()
	km.refs--
	if km.refs == 0 {
		delete(lm.locks, key)
	}
	lm.mu.Unlock()
}

func lockRepoKey(repoKey repository.RepositoryKey) {
	globalLockManager.lockKey(repoKey.String())
}

func unlockRepoKey(repoKey repository.RepositoryKey) {
	globalLockManager.unlockKey(repoKey.String())
}

func lockPkgKey(pkgKey repository.PackageKey) {
	globalLockManager.lockKey(pkgKey.String())
}

func unlockPkgKey(pkgKey repository.PackageKey) {
	globalLockManager.unlockKey(pkgKey.String())
}

func extPRCommit(pr *dbPackageRevision) string {
	return extPRCommitFromLocator(pr.extPRID)
}

func extPRCommitFromLocator(loc kptfile.Locator) string {
	if loc.Git != nil {
		return loc.Git.Commit
	}
	return ""
}

func hasBeenPushedToGit(pr *dbPackageRevision) bool {
	if pr.lastPushedDbUpdated != nil {
		return true
	}
	commit := extPRCommit(pr)
	return commit != "" && commit != unpushedGitCommit
}

func dbContentChangedSincePush(pr *dbPackageRevision) bool {
	if pr.lastPushedDbUpdated == nil {
		return true
	}
	return !pr.updated.Equal(*pr.lastPushedDbUpdated)
}

func commitTaskForPush(pr *dbPackageRevision, existingInGit bool) *porchapi.Task {
	if existingInGit || hasBeenPushedToGit(pr) {
		return &porchapi.Task{Type: porchapi.TaskTypePush}
	}

	for i := range pr.tasks {
		if porchapi.IsValidFirstTaskType(pr.tasks[i].Type) {
			return &pr.tasks[i]
		}
	}

	return nil
}

func commitTaskForPublishedPush(tasks []porchapi.Task, existingInGit bool) *porchapi.Task {
	if existingInGit {
		return &porchapi.Task{Type: porchapi.TaskTypePush}
	}

	for i := range tasks {
		if porchapi.IsValidFirstTaskType(tasks[i].Type) {
			return &tasks[i]
		}
	}

	return &porchapi.Task{Type: porchapi.TaskTypePush}
}

func prNeedsPushToGit(pr *dbPackageRevision) bool {
	if pr.lastPushedDbUpdated == nil {
		return true
	}
	return !pr.lastPushedDbUpdated.Equal(pr.updated)
}
