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
	if err := json.Unmarshal([]byte(jsonValue), &value); err != nil {
		klog.Errorf("unmarshal of json value %v failed, %v ", jsonValue, err)
	}
}

type lockManager struct {
	mu    sync.RWMutex
	locks map[string]*sync.Mutex
}

var globalLockManager = &lockManager{
	locks: make(map[string]*sync.Mutex),
}

func (lm *lockManager) getLock(key string) *sync.Mutex {
	lm.mu.RLock()
	if m, exists := lm.locks[key]; exists {
		lm.mu.RUnlock()
		return m
	}
	lm.mu.RUnlock()

	lm.mu.Lock()
	defer lm.mu.Unlock()
	if m, exists := lm.locks[key]; exists {
		return m
	}

	lm.locks[key] = new(sync.Mutex)
	return lm.locks[key]
}

func (lm *lockManager) deleteLock(key string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	delete(lm.locks, key)
}

func getOrInsertRepoLock(repoKey repository.RepositoryKey) *sync.Mutex {
	return globalLockManager.getLock(repoKey.String())
}

func getOrInsertPkgLock(pkgKey repository.PackageKey) *sync.Mutex {
	return globalLockManager.getLock(pkgKey.String())
}

func deletePkgLock(pkgKey repository.PackageKey) {
	globalLockManager.deleteLock(pkgKey.String())
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
