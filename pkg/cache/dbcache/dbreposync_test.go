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

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
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

func (t *DbTestSuite) TestDBRepoSyncWithPushDraftsToGit_DraftOnlyInCacheRemoved() {
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

	_, err = testRepo.ClosePackageRevisionDraft(ctx, dbPRDraft, 0)
	t.Require().NoError(err)

	// Do not add the draft to the external repo. Sync should treat it as "cached only" and remove it.
	// Explicitly trigger sync
	err = testRepo.repositorySync.SyncOnce(ctx)
	t.Require().NoError(err)

	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Equal(0, len(prList), "with pushDraftsToGit enabled, draft only in cache should be removed by sync")

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
