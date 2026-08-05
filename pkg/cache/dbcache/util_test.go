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
	"context"
	"errors"
	"os/user"
	"sync"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/mock"
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

func (t *DbTestSuite) TestExternalCommitInfo_NoGitLock() {
	ctx := context.Background()
	mockPR := mockrepo.NewMockPackageRevision(t.T())
	mockPR.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Once()

	commit, commitTime := externalCommitInfo(ctx, mockPR)
	t.Equal("", commit)
	t.True(commitTime.IsZero())
}

func (t *DbTestSuite) TestExternalCommitInfo_WithGitLock() {
	ctx := context.Background()
	mockPR := mockrepo.NewMockPackageRevision(t.T())
	mockPR.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{
		Git: &kptfilev1.GitLock{Commit: "abc123"},
	}, nil).Once()

	commit, commitTime := externalCommitInfo(ctx, mockPR)
	t.Equal("abc123", commit)
	t.True(commitTime.IsZero())
}

func (t *DbTestSuite) TestExternalCommitInfo_GetLockError() {
	ctx := context.Background()
	mockPR := mockrepo.NewMockPackageRevision(t.T())
	mockPR.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, errors.New("lock error")).Once()

	commit, commitTime := externalCommitInfo(ctx, mockPR)
	t.Equal("", commit)
	t.True(commitTime.IsZero())
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

func (t *DbTestSuite) TestExtCommitChangedSincePush() {
	now := time.Now()
	commit := "abc123"
	otherCommit := "def456"

	pr := &dbPackageRevision{}
	t.False(extCommitChangedSincePush(pr, "", now))

	t.False(extCommitChangedSincePush(pr, commit, now))

	pr.lastPushedCommit = &commit
	t.False(extCommitChangedSincePush(pr, commit, now))

	t.True(extCommitChangedSincePush(pr, otherCommit, now))

	earlier := now.Add(-time.Second)
	pr.lastPushedCommitTimestamp = &now
	t.False(extCommitChangedSincePush(pr, otherCommit, earlier))

	later := now.Add(time.Second)
	t.True(extCommitChangedSincePush(pr, otherCommit, later))
}

func (t *DbTestSuite) TestCommitTaskForPush() {
	commit := "abc123"
	pr := &dbPackageRevision{lastPushedCommit: &commit}
	task := commitTaskForPush(pr)
	t.Require().NotNil(task)
	t.Equal(porchapi.TaskTypePush, task.Type)

	pr2 := &dbPackageRevision{tasks: []porchapi.Task{{Type: porchapi.TaskTypeRender}}}
	t.Nil(commitTaskForPush(pr2))

	pr3 := &dbPackageRevision{tasks: []porchapi.Task{{Type: porchapi.TaskTypeInit}}}
	task3 := commitTaskForPush(pr3)
	t.Require().NotNil(task3)
	t.Equal(porchapi.TaskTypeInit, task3.Type)

	pr4 := &dbPackageRevision{tasks: []porchapi.Task{{Type: porchapi.TaskTypeClone}}}
	task4 := commitTaskForPush(pr4)
	t.Require().NotNil(task4)
	t.Equal(porchapi.TaskTypeClone, task4.Type)

	pr5 := &dbPackageRevision{}
	t.Nil(commitTaskForPush(pr5))
}

func (t *DbTestSuite) TestPrNeedsPushToGit() {
	now := time.Now()

	pr := &dbPackageRevision{}
	t.True(prNeedsPushToGit(pr))

	commit := "abc123"
	pr.lastPushedCommit = &commit
	t.True(prNeedsPushToGit(pr))

	pr.updated = now
	pr.lastPushedDbUpdated = &now
	t.False(prNeedsPushToGit(pr))

	later := now.Add(time.Second)
	pr.updated = later
	t.True(prNeedsPushToGit(pr))
}

func (t *DbTestSuite) TestPreservePushMarkersIfUnset() {
	commit := "abc123"
	commitTime := time.Now()
	dbUpdated := commitTime.Add(-time.Minute)

	existing := &dbPackageRevision{
		lastPushedCommit:          &commit,
		lastPushedCommitTimestamp: &commitTime,
		lastPushedDbUpdated:       &dbUpdated,
	}

	pr := &dbPackageRevision{}
	preservePushMarkersIfUnset(pr, existing)
	t.Require().NotNil(pr.lastPushedCommit)
	t.Equal(commit, *pr.lastPushedCommit)
	t.Equal(&commitTime, pr.lastPushedCommitTimestamp)
	t.Equal(&dbUpdated, pr.lastPushedDbUpdated)

	otherCommit := "other"
	prWithMarker := &dbPackageRevision{lastPushedCommit: &otherCommit}
	preservePushMarkersIfUnset(prWithMarker, existing)
	t.Equal(otherCommit, *prWithMarker.lastPushedCommit)

	prEmpty := &dbPackageRevision{}
	preservePushMarkersIfUnset(prEmpty, &dbPackageRevision{})
	t.Nil(prEmpty.lastPushedCommit)
}
