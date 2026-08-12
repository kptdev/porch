// Copyright 2025 The kpt Authors
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
	"os/user"
	"sync"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
)

func (t *DbTestSuite) TestUtil() {
	// We can't marshal a function into JSON
	jsonVal := valueAsJSON(t.TestDBSQL)
	t.Require().Equal("", jsonVal)

	secondValue := time.Second
	setValueFromJSON("", &secondValue)
	t.Equal(time.Second, secondValue)
}

func (t *DbTestSuite) TestGetCurrentUser() {
	got := getCurrentUser()
	t.NotEmpty(got)

	if u, err := user.Current(); err == nil {
		t.Equal(u.Username, got)
	}
}

func (t *DbTestSuite) TestValueAsJSONAndSetValueFromJSON() {
	type myStruct struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	original := myStruct{Name: "test", Value: 42}
	jsonStr := valueAsJSON(original)
	t.NotEmpty(jsonStr)

	var restored myStruct
	setValueFromJSON(jsonStr, &restored)
	t.Equal(original, restored)
}

func (t *DbTestSuite) TestValueAsJSONInvalidInput() {
	ch := make(chan int)
	jsonStr := valueAsJSON(ch)
	t.Equal("", jsonStr)
}

func (t *DbTestSuite) TestSetValueFromJSONInvalidInput() {
	original := 99
	setValueFromJSON("not-valid-json{{{", &original)
	t.Equal(99, original)
}

func (t *DbTestSuite) TestLockManagerGetLock() {
	lm := &lockManager{locks: make(map[string]*sync.Mutex)}

	lock1 := lm.getLock("key1")
	t.NotNil(lock1)

	lock1Again := lm.getLock("key1")
	t.Same(lock1, lock1Again)

	lock2 := lm.getLock("key2")
	t.NotNil(lock2)
	t.NotSame(lock1, lock2)
}

func (t *DbTestSuite) TestLockManagerDeleteLock() {
	lm := &lockManager{locks: make(map[string]*sync.Mutex)}

	lock := lm.getLock("mykey")
	t.NotNil(lock)
	t.Len(lm.locks, 1)

	lm.deleteLock("mykey")
	t.Len(lm.locks, 0)

	lm.deleteLock("non-existent")
}

func (t *DbTestSuite) TestGetOrInsertRepoLock() {
	repoKey := repository.RepositoryKey{Namespace: "ns", Name: "repo"}
	lock := getOrInsertRepoLock(repoKey)
	t.NotNil(lock)

	lock2 := getOrInsertRepoLock(repoKey)
	t.Same(lock, lock2)
}

func (t *DbTestSuite) TestGetOrInsertPkgLockAndDeletePkgLock() {
	pkgKey := repository.PackageKey{
		RepoKey: repository.RepositoryKey{Namespace: "ns", Name: "repo"},
		Package: "my-pkg",
	}

	lock := getOrInsertPkgLock(pkgKey)
	t.NotNil(lock)

	lock2 := getOrInsertPkgLock(pkgKey)
	t.Same(lock, lock2)

	deletePkgLock(pkgKey)
	lock3 := getOrInsertPkgLock(pkgKey)
	t.NotNil(lock3)

	deletePkgLock(pkgKey)
}

func (t *DbTestSuite) TestExtPRCommit() {
	t.Equal("", extPRCommit(&dbPackageRevision{}))
	t.Equal("abc123", extPRCommit(&dbPackageRevision{
		extPRID: kptfilev1.Locator{Git: &kptfilev1.GitLock{Commit: "abc123"}},
	}))
	t.Equal(unpushedGitCommit, extPRCommit(&dbPackageRevision{
		extPRID: kptfilev1.Locator{Git: &kptfilev1.GitLock{Commit: unpushedGitCommit}},
	}))
}

func (t *DbTestSuite) TestHasBeenPushedToGit() {
	now := time.Now()
	t.False(hasBeenPushedToGit(&dbPackageRevision{}))
	t.True(hasBeenPushedToGit(&dbPackageRevision{lastPushedDbUpdated: &now}))
	t.True(hasBeenPushedToGit(&dbPackageRevision{
		extPRID: kptfilev1.Locator{Git: &kptfilev1.GitLock{Commit: "abc123"}},
	}))
	t.False(hasBeenPushedToGit(&dbPackageRevision{
		extPRID: kptfilev1.Locator{Git: &kptfilev1.GitLock{Commit: unpushedGitCommit}},
	}))
}

func (t *DbTestSuite) TestDbContentChangedSincePush() {
	now := time.Now()

	pr := &dbPackageRevision{updated: now}
	t.True(dbContentChangedSincePush(pr))

	pr.lastPushedDbUpdated = &now
	t.False(dbContentChangedSincePush(pr))

	later := now.Add(time.Second)
	pr.updated = later
	t.True(dbContentChangedSincePush(pr))
}

func (t *DbTestSuite) TestCommitTaskForPush() {
	now := time.Now()
	pr := &dbPackageRevision{lastPushedDbUpdated: &now}
	task := commitTaskForPush(pr, false)
	t.Require().NotNil(task)
	t.Equal(porchapi.TaskTypePush, task.Type)

	prWithExtCommit := &dbPackageRevision{
		extPRID: kptfilev1.Locator{Git: &kptfilev1.GitLock{Commit: "abc123"}},
		tasks:   []porchapi.Task{{Type: porchapi.TaskTypeEdit}},
	}
	taskExt := commitTaskForPush(prWithExtCommit, false)
	t.Require().NotNil(taskExt)
	t.Equal(porchapi.TaskTypePush, taskExt.Type)

	prExistingInGit := &dbPackageRevision{tasks: []porchapi.Task{{Type: porchapi.TaskTypeEdit}}}
	taskExisting := commitTaskForPush(prExistingInGit, true)
	t.Require().NotNil(taskExisting)
	t.Equal(porchapi.TaskTypePush, taskExisting.Type)

	pr2 := &dbPackageRevision{tasks: []porchapi.Task{{Type: porchapi.TaskTypeRender}}}
	t.Nil(commitTaskForPush(pr2, false))

	pr3 := &dbPackageRevision{tasks: []porchapi.Task{{Type: porchapi.TaskTypeInit}}}
	task3 := commitTaskForPush(pr3, false)
	t.Require().NotNil(task3)
	t.Equal(porchapi.TaskTypeInit, task3.Type)

	pr4 := &dbPackageRevision{tasks: []porchapi.Task{{Type: porchapi.TaskTypeClone}}}
	task4 := commitTaskForPush(pr4, false)
	t.Require().NotNil(task4)
	t.Equal(porchapi.TaskTypeClone, task4.Type)

	pr5 := &dbPackageRevision{}
	t.Nil(commitTaskForPush(pr5, false))
}

func (t *DbTestSuite) TestCommitTaskForPublishedPush() {
	editTasks := []porchapi.Task{{Type: porchapi.TaskTypeEdit}}

	task := commitTaskForPublishedPush(editTasks, true)
	t.Require().NotNil(task)
	t.Equal(porchapi.TaskTypePush, task.Type)

	taskNew := commitTaskForPublishedPush(editTasks, false)
	t.Require().NotNil(taskNew)
	t.Equal(porchapi.TaskTypeEdit, taskNew.Type)

	taskDefault := commitTaskForPublishedPush(nil, false)
	t.Require().NotNil(taskDefault)
	t.Equal(porchapi.TaskTypePush, taskDefault.Type)
}

func (t *DbTestSuite) TestPrNeedsPushToGit() {
	now := time.Now()

	pr := &dbPackageRevision{}
	t.True(prNeedsPushToGit(pr))

	pr.updated = now
	pr.lastPushedDbUpdated = &now
	t.False(prNeedsPushToGit(pr))

	later := now.Add(time.Second)
	pr.updated = later
	t.True(prNeedsPushToGit(pr))
}

func (t *DbTestSuite) TestPreservePushMarkersIfUnset() {
	dbUpdated := time.Now()

	existing := &dbPackageRevision{
		lastPushedDbUpdated: &dbUpdated,
		extPRID: kptfilev1.Locator{
			Git: &kptfilev1.GitLock{Commit: "abc123"},
		},
	}

	pr := &dbPackageRevision{
		extPRID: kptfilev1.Locator{
			Git: &kptfilev1.GitLock{Commit: unpushedGitCommit},
		},
	}
	preservePushMarkersIfUnset(pr, existing)
	t.Require().NotNil(pr.lastPushedDbUpdated)
	t.Equal(dbUpdated, *pr.lastPushedDbUpdated)
	t.Equal("abc123", extPRCommit(pr))

	otherUpdated := dbUpdated.Add(time.Minute)
	prWithMarker := &dbPackageRevision{lastPushedDbUpdated: &otherUpdated}
	preservePushMarkersIfUnset(prWithMarker, existing)
	t.Equal(otherUpdated, *prWithMarker.lastPushedDbUpdated)

	prEmpty := &dbPackageRevision{}
	preservePushMarkersIfUnset(prEmpty, &dbPackageRevision{})
	t.Nil(prEmpty.lastPushedDbUpdated)
}
