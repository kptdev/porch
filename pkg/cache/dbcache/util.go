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
	"context"
	"encoding/json"
	"os/user"
	"sync"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"k8s.io/klog/v2"
)

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

func externalCommitInfo(ctx context.Context, extPR repository.PackageRevision) (string, time.Time) {
	var commit string
	if _, lock, err := extPR.GetLock(ctx); err == nil && lock.Git != nil {
		commit = lock.Git.Commit
	}

	var commitTime time.Time
	if ctg, ok := extPR.(repository.CommitTimeGetter); ok {
		commitTime = ctg.CommitTimestamp()
	}

	return commit, commitTime
}

func dbContentChangedSincePush(pr *dbPackageRevision) bool {
	if pr.lastPushedDbUpdated == nil {
		return true
	}
	return !pr.updated.Equal(*pr.lastPushedDbUpdated)
}

func extCommitChangedSincePush(pr *dbPackageRevision, externalCommit string, externalCommitTime time.Time) bool {
	if externalCommit == "" || pr.lastPushedCommit == nil {
		return false
	}
	if externalCommit == *pr.lastPushedCommit {
		return false
	}
	if pr.lastPushedCommitTimestamp == nil {
		return true
	}
	return externalCommitTime.After(*pr.lastPushedCommitTimestamp)
}

func commitTaskForPush(pr *dbPackageRevision) *porchapi.Task {
	if pr.lastPushedCommit != nil {
		return &porchapi.Task{Type: porchapi.TaskTypePush}
	}

	for i := range pr.tasks {
		if porchapi.IsValidFirstTaskType(pr.tasks[i].Type) {
			return &pr.tasks[i]
		}
	}

	return nil
}

func prNeedsPushToGit(pr *dbPackageRevision) bool {
	if pr.lastPushedCommit == nil || pr.lastPushedDbUpdated == nil {
		return true
	}
	return !pr.lastPushedDbUpdated.Equal(pr.updated)
}
