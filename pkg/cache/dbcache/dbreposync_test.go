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
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/testutil"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	mockcachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (t *DbTestSuite) TestDBRepoSync() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	repoName := "my-repo-name"
	namespace := "my-ns"
	externalrepo.ExternalRepoInUnitTestMode = true

	ctx := t.Context()
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	repoObj := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: namespace,
		},
	}
	repoObj.SetGroupVersionKind(configapi.GroupVersion.WithKind("Repository"))

	fakeClient := testutil.NewFakeClientWithStatus(scheme, repoObj)

	testRepo := t.createTestRepo(namespace, repoName)
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)

	cacheOptions := cachetypes.CacheOptions{
		CoreClient: fakeClient,
	}

	testRepo.repositorySync = newRepositorySync(testRepo, cacheOptions)
	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: repoName,
			PackageName:    "my-package",
			WorkspaceName:  "my-workspace",
			Lifecycle:      porchapi.PackageRevisionLifecyclePublished,
		},
	}
	dbPRDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)
	t.Require().NotNil(dbPRDraft)

	dbPR, err := testRepo.ClosePackageRevisionDraft(ctx, dbPRDraft, 0)
	t.Require().NoError(err)

	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycleProposed)
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR)

	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished)
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR)

	// Explicitly trigger sync
	err = testRepo.repositorySync.SyncOnce(ctx)
	t.Require().NoError(err)

	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Equal(0, len(prList)) // Sync should have deleted the cached PR that is not in the external repo

	// Add the PR to the external repo
	fakeRepo := testRepo.externalRepo.(*fake.Repository)
	fakeExtPR := fake.FakePackageRevision{
		PrKey:           dbPR.Key(),
		PackageRevision: &newPRDef,
		Resources:       &porchapi.PackageRevisionResources{},
		Kptfile: kptfilev1.KptFile{
			Upstream:     &kptfilev1.Upstream{},
			UpstreamLock: &kptfilev1.Locator{},
		},
	}
	fakeRepo.PackageRevisions = append(fakeRepo.PackageRevisions, &fakeExtPR)

	// Sync should not add PR because version hasn't changed
	err = testRepo.repositorySync.SyncOnce(ctx)
	t.Require().NoError(err)

	prList, err = testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Equal(0, len(prList)) // The version of the external repo has not changed

	fakeRepo.CurrentVersion = "bar"

	// Explicitly trigger sync after version change
	err = testRepo.repositorySync.SyncOnce(ctx)
	t.Require().NoError(err)

	prList, err = testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Equal(1, len(prList)) // Sync should have added a cached PR that is in the external repo

	err = testRepo.Close(ctx)
	t.Require().NoError(err)
}

func (t *DbTestSuite) TestDBRepoSyncWithPushDraftsToGit_DraftInExternalKept() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	repoName := "push-drafts-repo"
	namespace := "push-drafts-ns"
	externalrepo.ExternalRepoInUnitTestMode = true

	ctx := t.Context()
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	repoObj := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: namespace,
		},
	}
	repoObj.SetGroupVersionKind(configapi.GroupVersion.WithKind("Repository"))

	fakeClient := testutil.NewFakeClientWithStatus(scheme, repoObj)

	testRepo := t.createTestRepo(namespace, repoName)
	testRepo.pushDraftsToGit = true
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)

	cacheOptions := cachetypes.CacheOptions{
		CoreClient: fakeClient,
	}
	testRepo.repositorySync = newRepositorySync(testRepo, cacheOptions)

	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: repoName,
			PackageName:    "my-package",
			WorkspaceName:  "my-workspace",
			Lifecycle:      porchapi.PackageRevisionLifecycleDraft,
		},
	}
	dbPRDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)
	t.Require().NotNil(dbPRDraft)

	dbPR, err := testRepo.ClosePackageRevisionDraft(ctx, dbPRDraft, 0)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR)
	t.Require().Equal(porchapi.PackageRevisionLifecycleDraft, dbPR.Lifecycle(ctx))

	// Simulate the draft having been pushed to external git: add it to the fake repo.
	fakeRepo := testRepo.externalRepo.(*fake.Repository)
	newPRDefDraft := newPRDef.DeepCopy()
	newPRDefDraft.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDraft
	fakeExtPR := &fake.FakePackageRevision{
		PrKey:            dbPR.Key(),
		PackageRevision:  newPRDefDraft,
		PackageLifecycle: porchapi.PackageRevisionLifecycleDraft,
		Resources:        &porchapi.PackageRevisionResources{},
		Kptfile: kptfilev1.KptFile{
			Upstream:     &kptfilev1.Upstream{},
			UpstreamLock: &kptfilev1.Locator{},
		},
	}
	fakeRepo.PackageRevisions = append(fakeRepo.PackageRevisions, fakeExtPR)
	fakeRepo.CurrentVersion = "draft-v1"

	// Explicitly trigger sync
	err = testRepo.repositorySync.SyncOnce(ctx)
	t.Require().NoError(err)

	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Equal(1, len(prList), "with pushDraftsToGit enabled, draft in both cache and external should be kept by sync")

	err = testRepo.Close(ctx)
	t.Require().NoError(err)
}

func (t *DbTestSuite) TestDBRepoSyncWithPushDraftsToGit_DraftOnlyInCacheQueuedForPush() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	repoName := "push-drafts-removed-repo"
	namespace := "push-drafts-removed-ns"
	externalrepo.ExternalRepoInUnitTestMode = true

	ctx := t.Context()
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	repoObj := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: namespace,
		},
	}
	repoObj.SetGroupVersionKind(configapi.GroupVersion.WithKind("Repository"))

	fakeClient := testutil.NewFakeClientWithStatus(scheme, repoObj)

	testRepo := t.createTestRepo(namespace, repoName)
	testRepo.pushDraftsToGit = true
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)

	cacheOptions := cachetypes.CacheOptions{
		CoreClient: fakeClient,
	}
	testRepo.repositorySync = newRepositorySync(testRepo, cacheOptions)

	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: repoName,
			PackageName:    "my-package",
			WorkspaceName:  "my-workspace",
			Lifecycle:      porchapi.PackageRevisionLifecycleDraft,
		},
	}
	dbPRDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)
	t.Require().NotNil(dbPRDraft)

	closedPR, err := testRepo.ClosePackageRevisionDraft(ctx, dbPRDraft, 0)
	t.Require().NoError(err)
	t.Require().NotNil(closedPR)
	prKey := closedPR.Key()

	// Do not add the draft to the external repo. Sync should queue a git push instead of deleting it.
	// Explicitly trigger sync
	err = testRepo.repositorySync.SyncOnce(ctx)
	t.Require().NoError(err)

	t.Eventually(func() bool {
		freshPR, err := pkgRevReadFromDB(ctx, prKey, false)
		if err != nil {
			return false
		}
		return hasBeenPushedToGit(freshPR)
	}, 5*time.Second, 50*time.Millisecond, "async PushDraftPackageRevision should record last_pushed_db_updated in DB")

	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Equal(1, len(prList), "with pushDraftsToGit enabled, draft only in cache should be pushed to git, not removed")

	err = testRepo.Close(ctx)
	t.Require().NoError(err)
}

func (t *DbTestSuite) TestDBRepoSyncWithPushDraftsToGitDisabled_DraftNotConsideredBySync() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	repoName := "no-push-drafts-repo"
	namespace := "no-push-drafts-ns"
	externalrepo.ExternalRepoInUnitTestMode = true

	ctx := t.Context()
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	repoObj := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: namespace,
		},
	}
	repoObj.SetGroupVersionKind(configapi.GroupVersion.WithKind("Repository"))

	fakeClient := testutil.NewFakeClientWithStatus(scheme, repoObj)

	testRepo := t.createTestRepo(namespace, repoName)
	t.Require().False(testRepo.pushDraftsToGit)
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)

	cacheOptions := cachetypes.CacheOptions{
		CoreClient: fakeClient,
	}
	testRepo.repositorySync = newRepositorySync(testRepo, cacheOptions)

	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: repoName,
			PackageName:    "my-package",
			WorkspaceName:  "my-workspace",
			Lifecycle:      porchapi.PackageRevisionLifecycleDraft,
		},
	}
	dbPRDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)
	t.Require().NotNil(dbPRDraft)

	_, err = testRepo.ClosePackageRevisionDraft(ctx, dbPRDraft, 0)
	t.Require().NoError(err)

	// Sync only considers Published/DeletionProposed when pushDraftsToGit is false, so the draft is not "cached only".
	// Explicitly trigger sync
	err = testRepo.repositorySync.SyncOnce(ctx)
	t.Require().NoError(err)

	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Equal(1, len(prList), "with pushDraftsToGit disabled, draft in cache should not be removed by sync")

	err = testRepo.Close(ctx)
	t.Require().NoError(err)
}

func (t *DbTestSuite) TestDBSyncRunOnceAt() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	repoName := "runonce-repo"
	namespace := "runonce-ns"

	externalrepo.ExternalRepoInUnitTestMode = true

	ctx := t.Context()
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	runOnceTime := metav1.NewTime(time.Now().Add(7 * time.Second))

	repoObj := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: namespace,
		},
		Spec: configapi.RepositorySpec{
			Sync: &configapi.RepositorySync{
				RunOnceAt: &runOnceTime,
			},
		},
	}
	repoObj.SetGroupVersionKind(configapi.GroupVersion.WithKind("Repository"))

	fakeClient := testutil.NewFakeClientWithStatus(scheme, repoObj)
	testRepo := t.createTestRepo(namespace, repoName)
	testRepo.spec.Spec.Sync = &configapi.RepositorySync{
		RunOnceAt: &runOnceTime,
	}
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)

	cacheOptions := cachetypes.CacheOptions{
		CoreClient: fakeClient,
	}

	sync := newRepositorySync(testRepo, cacheOptions)
	testRepo.repositorySync = sync

	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: repoName,
			PackageName:    "my-package",
			WorkspaceName:  "my-workspace",
			Lifecycle:      porchapi.PackageRevisionLifecyclePublished,
		},
	}
	dbPRDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)
	t.Require().NotNil(dbPRDraft)

	dbPR, err := testRepo.ClosePackageRevisionDraft(ctx, dbPRDraft, 0)
	t.Require().NoError(err)

	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycleProposed)
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR)

	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished)
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR)

	// Add the PR to the external repo
	fakeRepo := testRepo.externalRepo.(*fake.Repository)
	fakeExtPR := fake.FakePackageRevision{
		PrKey:           dbPR.Key(),
		PackageRevision: &newPRDef,
		Resources:       &porchapi.PackageRevisionResources{},
		Kptfile: kptfilev1.KptFile{
			Upstream:     &kptfilev1.Upstream{},
			UpstreamLock: &kptfilev1.Locator{},
		},
	}
	fakeRepo.PackageRevisions = append(fakeRepo.PackageRevisions, &fakeExtPR)
	testRepo.externalRepo.(*fake.Repository).CurrentVersion = "bar"

	// Wait until externalRepo.Version(ctx) returns "bar"
	timeout := time.After(5 * time.Second)
	tick := time.Tick(100 * time.Millisecond)

	versionReady := false
	for !versionReady {
		select {
		case <-timeout:
			t.T().Fatal("Timed out waiting for externalRepo version to be 'bar'")
		case <-tick:
			version, _ := testRepo.externalRepo.Version(ctx)
			if version == "bar" {
				t.T().Log("externalRepo version is 'bar'")
				versionReady = true
			}
		}
	}

	// Explicitly trigger sync
	err = testRepo.repositorySync.SyncOnce(ctx)
	t.Require().NoError(err)

	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Equal(1, len(prList)) // Sync should have deleted the cached PR that is not in the external repo and
	// it should have added a cached PR that is in the external repo

	// Check that sync stats were updated
	t.Require().NotNil(sync.lastSyncStats)

	err = testRepo.Close(ctx)
	t.Require().NoError(err)
}
func (t *DbTestSuite) TestRepositorySync_SyncOnce() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true
	testRepo := t.createTestRepo("test-ns", "sync-once-repo")
	defer t.deleteTestRepo(testRepo.Key())

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	sync := &repositorySync{
		repo: testRepo,
	}

	err = sync.SyncOnce(ctx)
	t.Require().NoError(err)
}

func (t *DbTestSuite) TestNewRepositorySync() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true
	testRepo := t.createTestRepo("test-ns", "new-sync-repo")
	defer t.deleteTestRepo(testRepo.Key())

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)
	fakeClient := testutil.NewFakeClientWithStatus(scheme)

	options := cachetypes.CacheOptions{
		CoreClient: fakeClient,
	}

	sync := newRepositorySync(testRepo, options)

	t.NotNil(sync)
	t.Equal(testRepo, sync.repo)
}

// TestCacheExternalPRs_SkipsBinaryFiles verifies that sync skips binary files
// to prevent invalid UTF-8 content from causing PostgreSQL errors
func (t *DbTestSuite) TestCacheExternalPRs_SkipsBinaryFiles() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo("binary-ns", "binary-repo")
	defer t.deleteTestRepo(testRepo.Key())

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	repoSync := &repositorySync{
		repo: testRepo,
	}

	// Prepare test data with mixed text and binary files
	prKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: testRepo.Key(),
			Package: "test-pkg",
		},
		Revision:      1,
		WorkspaceName: "ws",
	}

	prDef := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "binary-ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "binary-repo",
			PackageName:    "test-pkg",
			WorkspaceName:  "ws",
			Lifecycle:      porchapi.PackageRevisionLifecyclePublished,
		},
	}

	// Simulate resources from external repo where image.png contains invalid UTF-8 bytes
	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"Kptfile":     "apiVersion: kpt.dev/v1\nkind: Kptfile\n",
				"config.yaml": "key: value\n",
				"image.png":   "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", // PNG header, not valid UTF-8
			},
		},
	}

	fakeExtPR := &fake.FakePackageRevision{
		PrKey:            prKey,
		PackageRevision:  prDef,
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources:        resources,
		Kptfile: kptfilev1.KptFile{
			Upstream:     &kptfilev1.Upstream{},
			UpstreamLock: &kptfilev1.Locator{},
		},
	}

	extPRMap := map[repository.PackageRevisionKey]repository.PackageRevision{
		prKey: fakeExtPR,
	}
	inExternalOnly := []repository.PackageRevisionKey{prKey}

	// Execute cacheExternalPRs, should succeed without failing due to binary file
	err = repoSync.cacheExternalPRs(ctx, extPRMap, inExternalOnly)
	t.Require().NoError(err, "sync should not fail due to binary file")

	// Verify resources read directly from DB
	cachedResources, err := pkgRevResourcesReadFromDB(ctx, prKey)
	t.Require().NoError(err)

	// Text files should exist
	_, hasKptfile := cachedResources["Kptfile"]
	_, hasConfig := cachedResources["config.yaml"]
	t.True(hasKptfile, "Kptfile should be cached")
	t.True(hasConfig, "config.yaml should be cached")

	// Binary file should be skipped
	_, hasBinary := cachedResources["image.png"]
	t.False(hasBinary, "image.png (binary) should be skipped")
}

// TestCacheExternalPRs_AllTextFiles verifies all text files are cached
func (t *DbTestSuite) TestCacheExternalPRs_AllTextFiles() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo("text-ns", "text-repo")
	defer t.deleteTestRepo(testRepo.Key())

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	repoSync := &repositorySync{
		repo: testRepo,
	}

	prKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: testRepo.Key(),
			Package: "text-pkg",
		},
		Revision:      1,
		WorkspaceName: "ws",
	}

	prDef := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "text-pr",
			Namespace:         "text-ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "text-repo",
			PackageName:    "text-pkg",
			WorkspaceName:  "ws",
			Lifecycle:      porchapi.PackageRevisionLifecyclePublished,
		},
	}

	// All files are valid UTF-8 text files
	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"Kptfile":         "apiVersion: kpt.dev/v1\nkind: Kptfile\n",
				"deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\n",
				"README.md":       "# Hello World\n",
			},
		},
	}

	fakeExtPR := &fake.FakePackageRevision{
		PrKey:            prKey,
		PackageRevision:  prDef,
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources:        resources,
		Kptfile: kptfilev1.KptFile{
			Upstream:     &kptfilev1.Upstream{},
			UpstreamLock: &kptfilev1.Locator{},
		},
	}

	extPRMap := map[repository.PackageRevisionKey]repository.PackageRevision{
		prKey: fakeExtPR,
	}
	inExternalOnly := []repository.PackageRevisionKey{prKey}

	err = repoSync.cacheExternalPRs(ctx, extPRMap, inExternalOnly)
	t.Require().NoError(err)

	cachedResources, err := pkgRevResourcesReadFromDB(ctx, prKey)
	t.Require().NoError(err)

	// All 3 files should exist
	t.Len(cachedResources, 3, "all text files should be cached")
	_, hasKptfile := cachedResources["Kptfile"]
	_, hasDeployment := cachedResources["deployment.yaml"]
	_, hasReadme := cachedResources["README.md"]
	t.True(hasKptfile, "Kptfile should be cached")
	t.True(hasDeployment, "deployment.yaml should be cached")
	t.True(hasReadme, "README.md should be cached")
}

// TestCacheExternalPRs_AllBinaryFiles verifies all binary files are skipped without error
func (t *DbTestSuite) TestCacheExternalPRs_AllBinaryFiles() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo("allbin-ns", "allbin-repo")
	defer t.deleteTestRepo(testRepo.Key())

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	repoSync := &repositorySync{
		repo: testRepo,
	}

	prKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: testRepo.Key(),
			Package: "allbin-pkg",
		},
		Revision:      1,
		WorkspaceName: "ws",
	}

	prDef := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "allbin-pr",
			Namespace:         "allbin-ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "allbin-repo",
			PackageName:    "allbin-pkg",
			WorkspaceName:  "ws",
			Lifecycle:      porchapi.PackageRevisionLifecyclePublished,
		},
	}

	// All files are binary (invalid UTF-8)
	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"image.png": "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR",
				"data.bin":  "\x00\x01\x02\x03\xff\xfe\xfd",
			},
		},
	}

	fakeExtPR := &fake.FakePackageRevision{
		PrKey:            prKey,
		PackageRevision:  prDef,
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources:        resources,
		Kptfile: kptfilev1.KptFile{
			Upstream:     &kptfilev1.Upstream{},
			UpstreamLock: &kptfilev1.Locator{},
		},
	}

	extPRMap := map[repository.PackageRevisionKey]repository.PackageRevision{
		prKey: fakeExtPR,
	}
	inExternalOnly := []repository.PackageRevisionKey{prKey}

	// Should not return error
	err = repoSync.cacheExternalPRs(ctx, extPRMap, inExternalOnly)
	t.Require().NoError(err, "all binary files should not cause error")

	cachedResources, err := pkgRevResourcesReadFromDB(ctx, prKey)
	t.Require().NoError(err)

	// All should be skipped
	t.Empty(cachedResources, "all binary files should be skipped")
}

// TestCacheExternalPRs_EmptyResources verifies empty resources do not cause error
func (t *DbTestSuite) TestCacheExternalPRs_EmptyResources() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo("empty-ns", "empty-repo")
	defer t.deleteTestRepo(testRepo.Key())

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	repoSync := &repositorySync{
		repo: testRepo,
	}

	prKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: testRepo.Key(),
			Package: "empty-pkg",
		},
		Revision:      1,
		WorkspaceName: "ws",
	}

	prDef := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "empty-pr",
			Namespace:         "empty-ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "empty-repo",
			PackageName:    "empty-pkg",
			WorkspaceName:  "ws",
			Lifecycle:      porchapi.PackageRevisionLifecyclePublished,
		},
	}

	// Empty resources map
	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{},
		},
	}

	fakeExtPR := &fake.FakePackageRevision{
		PrKey:            prKey,
		PackageRevision:  prDef,
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources:        resources,
		Kptfile: kptfilev1.KptFile{
			Upstream:     &kptfilev1.Upstream{},
			UpstreamLock: &kptfilev1.Locator{},
		},
	}

	extPRMap := map[repository.PackageRevisionKey]repository.PackageRevision{
		prKey: fakeExtPR,
	}
	inExternalOnly := []repository.PackageRevisionKey{prKey}

	err = repoSync.cacheExternalPRs(ctx, extPRMap, inExternalOnly)
	t.Require().NoError(err, "empty resources should not cause error")

	cachedResources, err := pkgRevResourcesReadFromDB(ctx, prKey)
	t.Require().NoError(err)
	t.Empty(cachedResources, "empty resources should return empty map")
}

// TestCacheExternalPRs_SkipsNulByteContent verifies that files containing NUL bytes
// are skipped even though NUL (0x00) is valid UTF-8, because PostgreSQL TEXT rejects it
func (t *DbTestSuite) TestCacheExternalPRs_SkipsNulByteContent() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo("nul-ns", "nul-repo")
	defer t.deleteTestRepo(testRepo.Key())

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	repoSync := &repositorySync{
		repo: testRepo,
	}

	prKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: testRepo.Key(),
			Package: "nul-pkg",
		},
		Revision:      1,
		WorkspaceName: "ws",
	}

	prDef := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "nul-pr",
			Namespace:         "nul-ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "nul-repo",
			PackageName:    "nul-pkg",
			WorkspaceName:  "ws",
			Lifecycle:      porchapi.PackageRevisionLifecyclePublished,
		},
	}

	// script.sh contains a NUL byte but is otherwise valid UTF-8
	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"Kptfile":     "apiVersion: kpt.dev/v1\nkind: Kptfile\n",
				"config.yaml": "key: value\n",
				"script.sh":   "#!/bin/sh\necho hello\x00world\n",
			},
		},
	}

	fakeExtPR := &fake.FakePackageRevision{
		PrKey:            prKey,
		PackageRevision:  prDef,
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources:        resources,
		Kptfile: kptfilev1.KptFile{
			Upstream:     &kptfilev1.Upstream{},
			UpstreamLock: &kptfilev1.Locator{},
		},
	}

	extPRMap := map[repository.PackageRevisionKey]repository.PackageRevision{
		prKey: fakeExtPR,
	}
	inExternalOnly := []repository.PackageRevisionKey{prKey}

	err = repoSync.cacheExternalPRs(ctx, extPRMap, inExternalOnly)
	t.Require().NoError(err, "sync should not fail due to NUL byte content")

	cachedResources, err := pkgRevResourcesReadFromDB(ctx, prKey)
	t.Require().NoError(err)

	// Text files should be cached
	_, hasKptfile := cachedResources["Kptfile"]
	_, hasConfig := cachedResources["config.yaml"]
	t.True(hasKptfile, "Kptfile should be cached")
	t.True(hasConfig, "config.yaml should be cached")

	// File with NUL byte should be skipped
	_, hasScript := cachedResources["script.sh"]
	t.False(hasScript, "script.sh (contains NUL byte) should be skipped")
}

// TestCacheExternalPRs_SkipsInvalidFilePath verifies that files with invalid UTF-8
// in their path (resource_key) are skipped, since resource_key is also a PostgreSQL TEXT column
func (t *DbTestSuite) TestCacheExternalPRs_SkipsInvalidFilePath() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo("path-ns", "path-repo")
	defer t.deleteTestRepo(testRepo.Key())

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	repoSync := &repositorySync{
		repo: testRepo,
	}

	prKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: testRepo.Key(),
			Package: "path-pkg",
		},
		Revision:      1,
		WorkspaceName: "ws",
	}

	prDef := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "path-pr",
			Namespace:         "path-ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "path-repo",
			PackageName:    "path-pkg",
			WorkspaceName:  "ws",
			Lifecycle:      porchapi.PackageRevisionLifecyclePublished,
		},
	}

	// File with invalid UTF-8 in filename (e.g. Latin-1 encoded path from older systems)
	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"Kptfile":              "apiVersion: kpt.dev/v1\nkind: Kptfile\n",
				"config.yaml":          "key: value\n",
				"data/\xc0\xaf/f.yaml": "valid: content\n",
			},
		},
	}

	fakeExtPR := &fake.FakePackageRevision{
		PrKey:            prKey,
		PackageRevision:  prDef,
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources:        resources,
		Kptfile: kptfilev1.KptFile{
			Upstream:     &kptfilev1.Upstream{},
			UpstreamLock: &kptfilev1.Locator{},
		},
	}

	extPRMap := map[repository.PackageRevisionKey]repository.PackageRevision{
		prKey: fakeExtPR,
	}
	inExternalOnly := []repository.PackageRevisionKey{prKey}

	err = repoSync.cacheExternalPRs(ctx, extPRMap, inExternalOnly)
	t.Require().NoError(err, "sync should not fail due to invalid filepath")

	cachedResources, err := pkgRevResourcesReadFromDB(ctx, prKey)
	t.Require().NoError(err)

	// Valid files should be cached
	_, hasKptfile := cachedResources["Kptfile"]
	_, hasConfig := cachedResources["config.yaml"]
	t.True(hasKptfile, "Kptfile should be cached")
	t.True(hasConfig, "config.yaml should be cached")

	// File with invalid UTF-8 filepath should be skipped
	_, hasBadPath := cachedResources["data/\xc0\xaf/f.yaml"]
	t.False(hasBadPath, "file with invalid UTF-8 filepath should be skipped")
}

// TestCacheExternalPRs_NilResources verifies that nil return from GetResources
// does not cause a panic, since the interface contract allows (nil, nil)
func (t *DbTestSuite) TestCacheExternalPRs_NilResources() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo("nilres-ns", "nilres-repo")
	defer t.deleteTestRepo(testRepo.Key())

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	repoSync := &repositorySync{
		repo: testRepo,
	}

	prKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: testRepo.Key(),
			Package: "nilres-pkg",
		},
		Revision:      1,
		WorkspaceName: "ws",
	}

	prDef := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "nilres-pr",
			Namespace:         "nilres-ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "nilres-repo",
			PackageName:    "nilres-pkg",
			WorkspaceName:  "ws",
			Lifecycle:      porchapi.PackageRevisionLifecyclePublished,
		},
	}

	// GetResources returns nil (simulating interface contract edge case)
	fakeExtPR := &fake.FakePackageRevision{
		PrKey:            prKey,
		PackageRevision:  prDef,
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources:        nil,
		Kptfile: kptfilev1.KptFile{
			Upstream:     &kptfilev1.Upstream{},
			UpstreamLock: &kptfilev1.Locator{},
		},
	}

	extPRMap := map[repository.PackageRevisionKey]repository.PackageRevision{
		prKey: fakeExtPR,
	}
	inExternalOnly := []repository.PackageRevisionKey{prKey}

	// Should not panic or return error
	err = repoSync.cacheExternalPRs(ctx, extPRMap, inExternalOnly)
	t.Require().NoError(err, "nil resources should not cause panic or error")

	cachedResources, err := pkgRevResourcesReadFromDB(ctx, prKey)
	t.Require().NoError(err)
	t.Empty(cachedResources, "nil resources should result in empty cached resources")
}

// TestComparePRMaps verifies that comparePRMaps correctly partitions keys into
// left-only, both, and right-only sets.
func (t *DbTestSuite) TestComparePRMaps() {
	testRepo := t.createTestRepo("compare-ns", "compare-repo")
	defer t.deleteTestRepo(testRepo.Key())

	s := &repositorySync{repo: testRepo}
	repoKey := testRepo.Key()

	keyA := repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{RepoKey: repoKey, Package: "pkg-a"},
		Revision:      1,
		WorkspaceName: "ws1",
	}
	keyB := repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{RepoKey: repoKey, Package: "pkg-b"},
		Revision:      1,
		WorkspaceName: "ws1",
	}
	keyC := repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{RepoKey: repoKey, Package: "pkg-c"},
		Revision:      1,
		WorkspaceName: "ws1",
	}

	leftMap := map[repository.PackageRevisionKey]repository.PackageRevision{
		keyA: nil, // left only
		keyB: nil, // in both
	}
	rightMap := map[repository.PackageRevisionKey]repository.PackageRevision{
		keyB: nil, // in both
		keyC: nil, // right only
	}

	leftOnly, both, rightOnly := s.comparePRMaps(t.Context(), leftMap, rightMap)

	t.Len(leftOnly, 1)
	t.Equal(keyA, leftOnly[0])

	t.Len(both, 1)
	t.Equal(keyB, both[0])

	t.Len(rightOnly, 1)
	t.Equal(keyC, rightOnly[0])
}

// TestComparePRMaps_EmptyMaps verifies comparePRMaps handles empty inputs correctly.
func (t *DbTestSuite) TestComparePRMaps_EmptyMaps() {
	testRepo := t.createTestRepo("compareempty-ns", "compareempty-repo")
	defer t.deleteTestRepo(testRepo.Key())

	s := &repositorySync{repo: testRepo}

	leftOnly, both, rightOnly := s.comparePRMaps(
		t.Context(),
		map[repository.PackageRevisionKey]repository.PackageRevision{},
		map[repository.PackageRevisionKey]repository.PackageRevision{},
	)

	t.Empty(leftOnly)
	t.Empty(both)
	t.Empty(rightOnly)
}

// TestComparePRMaps_AllInBoth verifies comparePRMaps when both maps are identical.
func (t *DbTestSuite) TestComparePRMaps_AllInBoth() {
	testRepo := t.createTestRepo("compareboth-ns", "compareboth-repo")
	defer t.deleteTestRepo(testRepo.Key())

	s := &repositorySync{repo: testRepo}
	repoKey := testRepo.Key()

	key1 := repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{RepoKey: repoKey, Package: "pkg-1"},
		Revision:      1,
		WorkspaceName: "ws1",
	}
	key2 := repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{RepoKey: repoKey, Package: "pkg-2"},
		Revision:      2,
		WorkspaceName: "ws2",
	}

	sharedMap := map[repository.PackageRevisionKey]repository.PackageRevision{
		key1: nil,
		key2: nil,
	}

	leftOnly, both, rightOnly := s.comparePRMaps(t.Context(), sharedMap, sharedMap)

	t.Empty(leftOnly)
	t.Len(both, 2)
	t.Empty(rightOnly)
}

// TestSanitizeResources_NilInput verifies sanitizeResources returns empty map for nil input.
func (t *DbTestSuite) TestSanitizeResources_NilInput() {
	testRepo := t.createTestRepo("san-nil-ns", "san-nil-repo")
	defer t.deleteTestRepo(testRepo.Key())

	s := &repositorySync{repo: testRepo}
	prKey := repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{RepoKey: testRepo.Key(), Package: "pkg"},
		Revision:      1,
		WorkspaceName: "ws",
	}

	result, _ := s.sanitizeResources(prKey, nil)
	t.Empty(result, "nil resources should return empty map")
}

// TestSanitizeResources_NilResourceMap verifies sanitizeResources handles nil Resources map.
func (t *DbTestSuite) TestSanitizeResources_NilResourceMap() {
	testRepo := t.createTestRepo("san-nilmap-ns", "san-nilmap-repo")
	defer t.deleteTestRepo(testRepo.Key())

	s := &repositorySync{repo: testRepo}
	prKey := repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{RepoKey: testRepo.Key(), Package: "pkg"},
		Revision:      1,
		WorkspaceName: "ws",
	}

	result, _ := s.sanitizeResources(prKey, &porchapi.PackageRevisionResources{})
	t.Empty(result, "nil Resources map should return empty map")
}

// TestSanitizeResources_FiltersInvalidContent verifies sanitizeResources drops
// files with invalid UTF-8 or NUL bytes in key or value.
func (t *DbTestSuite) TestSanitizeResources_FiltersInvalidContent() {
	testRepo := t.createTestRepo("san-filter-ns", "san-filter-repo")
	defer t.deleteTestRepo(testRepo.Key())

	s := &repositorySync{repo: testRepo}
	prKey := repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{RepoKey: testRepo.Key(), Package: "pkg"},
		Revision:      1,
		WorkspaceName: "ws",
	}

	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"valid.yaml":        "valid content",
				"invalid\xff.yaml":  "valid content",     // invalid UTF-8 in key
				"nul-in-value.yaml": "content\x00here",   // NUL byte in value
				"nul-in-key\x00":    "valid content",     // NUL byte in key
				"binary.bin":        "\x89PNG\r\n\x1a\n", // binary content
			},
		},
	}

	result, _ := s.sanitizeResources(prKey, resources)

	t.Len(result, 1, "only the valid file should be retained")
	t.Contains(result, "valid.yaml")
	t.NotContains(result, "invalid\xff.yaml")
	t.NotContains(result, "nul-in-value.yaml")
	t.NotContains(result, "nul-in-key\x00")
	t.NotContains(result, "binary.bin")
}

// TestSanitizeResources_AllValid verifies sanitizeResources returns all files when all are valid.
func (t *DbTestSuite) TestSanitizeResources_AllValid() {
	testRepo := t.createTestRepo("san-allvalid-ns", "san-allvalid-repo")
	defer t.deleteTestRepo(testRepo.Key())

	s := &repositorySync{repo: testRepo}
	prKey := repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{RepoKey: testRepo.Key(), Package: "pkg"},
		Revision:      1,
		WorkspaceName: "ws",
	}

	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"Kptfile":     "apiVersion: kpt.dev/v1\nkind: Kptfile\n",
				"config.yaml": "key: value\n",
				"README.md":   "# Hello World\n",
			},
		},
	}

	result, _ := s.sanitizeResources(prKey, resources)
	t.Len(result, 3, "all valid files should be retained")
}

// TestCacheExternalPRs_SkipsRevision0Published verifies that a PR with revision=0 and
// Published lifecycle is skipped and not cached, since that combination is invalid.
func (t *DbTestSuite) TestCacheExternalPRs_SkipsRevision0Published() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo("r0pub-ns", "r0pub-repo")
	defer t.deleteTestRepo(testRepo.Key())

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	repoSync := &repositorySync{repo: testRepo}

	// revision=0 with Published lifecycle is an invalid combination that should be skipped
	prKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: testRepo.Key(),
			Package: "r0pub-pkg",
		},
		Revision:      0, // invalid: revision=0 with Published lifecycle
		WorkspaceName: "ws",
	}

	prDef := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "r0pub-pr",
			Namespace:         "r0pub-ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "r0pub-repo",
			PackageName:    "r0pub-pkg",
			WorkspaceName:  "ws",
			Lifecycle:      porchapi.PackageRevisionLifecyclePublished,
		},
	}

	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\n",
			},
		},
	}

	fakeExtPR := &fake.FakePackageRevision{
		PrKey:            prKey,
		PackageRevision:  prDef,
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources:        resources,
		Kptfile: kptfilev1.KptFile{
			Upstream:     &kptfilev1.Upstream{},
			UpstreamLock: &kptfilev1.Locator{},
		},
	}

	extPRMap := map[repository.PackageRevisionKey]repository.PackageRevision{prKey: fakeExtPR}
	inExternalOnly := []repository.PackageRevisionKey{prKey}

	// Should succeed and silently skip the invalid PR
	err = repoSync.cacheExternalPRs(ctx, extPRMap, inExternalOnly)
	t.Require().NoError(err, "revision=0+Published should be skipped without error")

	// PR should NOT be cached since it was skipped
	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Empty(prList, "revision=0+Published PR should not be cached")
}

// TestHandleInCachedOnly_DeletesPublishedCachedOnly verifies that a published workspace PR
// listed as cached-only is removed from the database while other revisions (e.g. main) remain.
func (t *DbTestSuite) TestHandleInCachedOnly_DeletesPublishedCachedOnly() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo("nodelete-ns", "nodelete-repo")
	defer t.deleteTestRepo(testRepo.Key())

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	repoSync := &repositorySync{
		repo: testRepo,
	}

	// Create and publish a PR so it appears in the "cached only" set
	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "nodelete-repo",
			PackageName:    "my-pkg",
			WorkspaceName:  "my-ws",
			Lifecycle:      porchapi.PackageRevisionLifecyclePublished,
		},
	}
	prDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)

	dbPR, err := testRepo.ClosePackageRevisionDraft(ctx, prDraft, 0)
	t.Require().NoError(err)

	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycleProposed)
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)

	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished)
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)

	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Len(prList, 2, "PR should exist before calling handleInCachedOnly (workspace PR + main branch PR created on publish)")

	cachedPrMap := repository.PrSlice2Map(prList)
	inCachedOnly := []repository.PackageRevisionKey{dbPR.Key()}

	// Call handleInCachedOnly – should delete the cached-only workspace PR.
	err = repoSync.handleInCachedOnly(ctx, cachedPrMap, inCachedOnly)
	t.Require().NoError(err)

	prListAfter, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Len(prListAfter, 1, "cached-only published workspace PR should be deleted; main branch revision remains")
}

// TestHandleInCachedOnly_DraftNotPushed_PushDraftsToGitFalse verifies that a Draft PR
// that has not been pushed to git is not deleted when pushDraftsToGit=false.
func (t *DbTestSuite) TestHandleInCachedOnly_DraftNotPushed_PushDraftsToGitFalse() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo("draftkeep-ns", "draftkeep-repo")
	defer t.deleteTestRepo(testRepo.Key())

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	repoSync := &repositorySync{
		repo: testRepo,
		// pushDraftsToGit is false (default zero value)
	}

	// Create a Draft PR (lastPushedDbUpdated will be nil)
	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "draftkeep-repo",
			PackageName:    "draft-pkg",
			WorkspaceName:  "draft-ws",
			Lifecycle:      porchapi.PackageRevisionLifecycleDraft,
		},
	}
	prDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)

	dbPR, err := testRepo.ClosePackageRevisionDraft(ctx, prDraft, 0)
	t.Require().NoError(err)

	// Verify there is a draft PR in the DB
	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
		Lifecycles: []porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecycleDraft},
	})
	t.Require().NoError(err)
	t.Len(prList, 1, "Draft PR should exist before calling handleInCachedOnly")

	// Build cachedPrMap directly with the dbPackageRevision (which has not been pushed yet)
	cachedPRTyped := dbPR.(*dbPackageRevision)
	t.False(hasBeenPushedToGit(cachedPRTyped), "new draft should not have been pushed to git")

	cachedPrMap := map[repository.PackageRevisionKey]repository.PackageRevision{
		dbPR.Key(): cachedPRTyped,
	}
	inCachedOnly := []repository.PackageRevisionKey{dbPR.Key()}

	// Should not delete the Draft PR since pushDraftsToGit=false means it is kept
	err = repoSync.handleInCachedOnly(ctx, cachedPrMap, inCachedOnly)
	t.Require().NoError(err)

	prListAfter, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
		Lifecycles: []porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecycleDraft},
	})
	t.Require().NoError(err)
	t.Len(prListAfter, 1, "unpushed Draft PR should not be deleted when pushDraftsToGit=false")
}

// TestDeleteCachedOnlyPR_AlreadyDeleted verifies that deleteCachedOnlyPR returns nil (no error)
// when the PR no longer exists in the database (sql.ErrNoRows path).
func (t *DbTestSuite) TestDeleteCachedOnlyPR_AlreadyDeleted() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo("alreadydel-ns", "alreadydel-repo")
	defer t.deleteTestRepo(testRepo.Key())

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	repoSync := &repositorySync{repo: testRepo}

	// Key for a PR that has never been persisted to the DB
	nonExistentKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: testRepo.Key(),
			Package: "nonexistent-pkg",
		},
		Revision:      99,
		WorkspaceName: "nonexistent-ws",
	}
	snapshot := &dbPackageRevision{pkgRevKey: nonExistentKey}

	// Should return nil because pkgRevReadFromDB returns sql.ErrNoRows
	err = repoSync.deleteCachedOnlyPR(ctx, nonExistentKey, snapshot)
	t.Require().NoError(err, "should not error when PR is already absent from DB")
}

// TestDeleteCachedOnlyPR_ChangedSinceSnapshot verifies that deleteCachedOnlyPR skips
// deletion when the PR was updated after the cached snapshot was taken.
func (t *DbTestSuite) TestDeleteCachedOnlyPR_ChangedSinceSnapshot() {
	ctx := t.Context()
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo("changed-ns", "changed-repo")
	defer t.deleteTestRepo(testRepo.Key())

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)
	defer func() {
		if err := testRepo.Close(ctx); err != nil {
			t.T().Logf("Failed to close test repo: %v", err)
		}
	}()

	repoSync := &repositorySync{
		repo: testRepo,
	}

	// Create and publish a PR
	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "changed-repo",
			PackageName:    "changed-pkg",
			WorkspaceName:  "changed-ws",
			Lifecycle:      porchapi.PackageRevisionLifecyclePublished,
		},
	}
	prDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)

	dbPR, err := testRepo.ClosePackageRevisionDraft(ctx, prDraft, 0)
	t.Require().NoError(err)

	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycleProposed)
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)

	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished)
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)

	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Len(prList, 2, "PR should exist in DB (workspace PR + main branch PR created on publish)")

	// Construct a STALE snapshot with a timestamp in the past (different from current updated)
	staleSnapshot := &dbPackageRevision{
		pkgRevKey: dbPR.Key(),
		updated:   time.Now().Add(-1 * time.Hour), // stale - differs from actual DB timestamp
		lifecycle: porchapi.PackageRevisionLifecyclePublished,
	}

	// deleteCachedOnlyPR should skip deletion because the snapshot is stale
	err = repoSync.deleteCachedOnlyPR(ctx, dbPR.Key(), staleSnapshot)
	t.Require().NoError(err, "stale snapshot should cause deletion to be skipped without error")

	// PR should still exist in the DB
	prListAfter, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Len(prListAfter, 2, "PR should not be deleted when snapshot is stale")
}
