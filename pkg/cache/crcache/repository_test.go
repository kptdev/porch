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

package crcache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/testutil"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	mockmeta "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/crcache/meta"
	mockcachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

func TestCachedRepoRefresh(t *testing.T) {
	mockRepo := mockrepo.NewMockRepository(t)
	mockMeta := mockmeta.NewMockMetadataStore(t)
	mockNotifier := mockcachetypes.NewMockRepoPRChangeNotifier(t)
	repoName := "the-name"
	namespace := "the-ns"
	repoSpec := configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: namespace,
		},
	}
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)
	fakeClient := testutil.NewFakeClientWithStatus(scheme, &repoSpec)

	options := cachetypes.CacheOptions{
		RepoPRChangeNotifier: mockNotifier,
		CoreClient:           fakeClient,
	}

	metaMap := []metav1.ObjectMeta{}

	mockRepo.On("Key").Return(key.RepositoryKey{Namespace: namespace, Name: repoName}).Maybe()
	mockRepo.EXPECT().Refresh(mock.Anything).Return(nil).Maybe()
	repoVersionCall := mockRepo.EXPECT().Version(mock.Anything).Return("v1.0", nil).Maybe()
	repoListPRCall := mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	repoClosePRDCall := mockRepo.EXPECT().ClosePackageRevisionDraft(mock.Anything, mock.Anything, 1).Return(nil, errors.New("create draft error")).Maybe()

	metaListCall := mockMeta.EXPECT().List(mock.Anything, mock.Anything).Return(metaMap, nil).Maybe()
	mockNotifier.EXPECT().NotifyPackageRevisionChange(mock.Anything, mock.Anything).Return(0).Maybe()

	repoKey := key.RepositoryKey{
		Namespace: namespace,
		Name:      repoName,
	}
	cr := newRepository(repoKey, &repoSpec, mockRepo, mockMeta, options)
	assert.Equal(t, repoKey, cr.Key())

	prKey := key.PackageRevisionKey{
		PkgKey: key.PackageKey{
			RepoKey: key.RepositoryKey{
				Namespace: namespace,
				Name:      repoName,
			},
			Path:    "",
			Package: "the-package",
		},
		WorkspaceName: "the-ws",
		Revision:      0,
	}

	fpr := fake.FakePackageRevision{
		PrKey: prKey,
	}

	cr.cachedPackageRevisions = make(map[key.PackageRevisionKey]*cachedPackageRevision)
	cr.cachedPackageRevisions[prKey] = &cachedPackageRevision{
		PackageRevision:  &fpr,
		metadataStore:    mockMeta,
		isLatestRevision: false,
	}
	assert.False(t, cr.cachedPackageRevisions[prKey].IsLatestRevision())

	ts, author := cr.cachedPackageRevisions[prKey].GetCommitInfo()
	assert.True(t, ts.IsZero())
	assert.Empty(t, author)

	err := cr.Refresh(context.TODO())
	assert.True(t, err == nil)

	repoVersionCall.Return("", errors.New("version error")).Maybe()
	err = cr.Refresh(context.TODO())
	assert.False(t, err == nil)
	repoVersionCall.Return("v2.0", nil).Maybe()

	metaListCall.Return(nil, errors.New("meta list error")).Maybe()
	err = cr.Refresh(context.TODO())
	assert.False(t, err == nil)
	metaListCall.Return(metaMap, nil).Maybe()

	repoListPRCall.Return(nil, errors.New("list error")).Maybe()
	err = cr.Refresh(context.TODO())
	assert.False(t, err == nil)
	repoListPRCall.Return(nil, nil).Maybe()

	repoVersionCall.Return("v3.0", nil).Maybe()
	metaDeleteCall := mockMeta.EXPECT().Delete(mock.Anything, mock.Anything, mock.Anything).Return(metav1.ObjectMeta{}, errors.New("delete error")).Maybe()
	err = cr.Refresh(context.TODO())
	assert.True(t, err == nil)
	metaDeleteCall.Return(metav1.ObjectMeta{}, nil).Maybe()

	prMeta := porchapi.PackageRevision{}

	repoCreatePRDCall := mockRepo.EXPECT().CreatePackageRevisionDraft(mock.Anything, mock.Anything).Return(nil, errors.New("create draft error")).Maybe()
	_, err = cr.CreatePackageRevisionDraft(context.TODO(), &prMeta)
	assert.True(t, err != nil)

	repoCreatePRDCall.Return(&fpr, nil).Maybe()
	prd, err := cr.CreatePackageRevisionDraft(context.TODO(), &prMeta)
	assert.True(t, err == nil)
	assert.Equal(t, 0, prd.Key().Revision)

	repoVersionCall.Return("", errors.New("version 2 error")).Maybe()
	_, err = cr.ClosePackageRevisionDraft(context.TODO(), prd, 1)
	assert.True(t, err != nil)

	repoVersionCall.Return("v3.2.4", nil).Maybe()
	_, err = cr.ClosePackageRevisionDraft(context.TODO(), prd, 1)
	assert.True(t, err != nil)

	_, err = cr.ClosePackageRevisionDraft(context.TODO(), prd, 1)
	assert.True(t, err != nil)
	repoClosePRDCall.Return(&fpr, nil).Maybe()

	repoClosePRDCall.Return(prd, nil).Maybe()
	metaCreateCall := mockMeta.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(metav1.ObjectMeta{}, errors.New("meta create error")).Maybe()

	_, err = cr.ClosePackageRevisionDraft(context.TODO(), prd, 1)
	assert.True(t, err != nil)
	repoClosePRDCall.Return(&fpr, nil).Maybe()
	metaCreateCall.Return(metav1.ObjectMeta{}, nil)

	mockGet := mockMeta.EXPECT().Get(mock.Anything, mock.Anything).Return(metav1.ObjectMeta{}, nil).Maybe()
	mockUpdate := mockMeta.EXPECT().Update(mock.Anything, mock.Anything).Return(metav1.ObjectMeta{}, nil).Maybe()
	pr, err := cr.ClosePackageRevisionDraft(context.TODO(), prd, 1)
	assert.True(t, err == nil)
	assert.True(t, pr != nil)

	mockUpdate.Return(metav1.ObjectMeta{}, errors.New("meta update error")).Maybe()
	if cachedPR := cr.cachedPackageRevisions[prKey]; cachedPR != nil {
		err = cachedPR.SetMeta(context.TODO(), metav1.ObjectMeta{Name: "Hello"})
		assert.True(t, err != nil)
	}
	mockUpdate.Return(metav1.ObjectMeta{}, nil).Maybe()

	mockGet.Return(metav1.ObjectMeta{}, errors.New("meta get error")).Maybe()
	if cachedPR := cr.cachedPackageRevisions[prKey]; cachedPR != nil {
		err = cachedPR.SetMeta(context.TODO(), metav1.ObjectMeta{Name: "Hello"})
		assert.True(t, err != nil)
	}
	mockGet.Return(metav1.ObjectMeta{}, nil).Maybe()

	returnedMeta := metav1.ObjectMeta{
		Finalizers: []string{
			"finalizer",
		},
	}
	metaDeleteCall.Return(returnedMeta, errors.New("Delete on meta error")).Maybe()
	err = cr.DeletePackageRevision(context.TODO(), cr.cachedPackageRevisions[prKey])
	assert.True(t, err == nil)

}

const (
	repoName  = "the-name"
	namespace = "the-ns"
)

func TestCRDeleteLatestRevision(t *testing.T) {
	mockRepo := mockrepo.NewMockRepository(t)
	mockMeta := mockmeta.NewMockMetadataStore(t)
	mockNotifier := mockcachetypes.NewMockRepoPRChangeNotifier(t)

	repoSpec := configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: namespace,
		},
	}

	repoKey := key.RepositoryKey{
		Namespace: namespace,
		Name:      repoName,
	}

	// Create two package revisions for the same package
	prKey1 := key.PackageRevisionKey{
		PkgKey: key.PackageKey{
			RepoKey: repoKey,
			Path:    "",
			Package: "test-package",
		},
		WorkspaceName: "v1",
		Revision:      1,
	}

	prKey2 := key.PackageRevisionKey{
		PkgKey: key.PackageKey{
			RepoKey: repoKey,
			Path:    "",
			Package: "test-package",
		},
		WorkspaceName: "v2",
		Revision:      2,
	}

	fpr1 := &fake.FakePackageRevision{
		PrKey:            prKey1,
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
	}

	fpr2 := &fake.FakePackageRevision{
		PrKey:            prKey2,
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
	}

	// Setup mocks
	mockRepo.On("Key").Return(repoKey).Maybe()
	mockRepo.EXPECT().DeletePackageRevision(mock.Anything, mock.Anything).Return(nil).Maybe()
	mockMeta.EXPECT().Delete(mock.Anything, mock.Anything, mock.Anything).Return(metav1.ObjectMeta{}, nil).Maybe()

	// Expect exactly 2 notifications: one for deletion, one for async notification
	mockNotifier.EXPECT().NotifyPackageRevisionChange(watch.Deleted, mock.Anything).Return(1).Once()
	mockNotifier.EXPECT().NotifyPackageRevisionChange(watch.Modified, mock.Anything).Return(1).Once()

	// Create repository manually to avoid sync manager
	cr := &cachedRepository{
		key:                  repoKey,
		repoSpec:             &repoSpec,
		repo:                 mockRepo,
		metadataStore:        mockMeta,
		repoPRChangeNotifier: mockNotifier,
		cachedPackages:       make(map[key.PackageKey]*cachedPackage), // Initialize to enable deletion
	}

	// Initialize cache with two revisions
	cr.cachedPackageRevisions = make(map[key.PackageRevisionKey]*cachedPackageRevision)
	cr.cachedPackageRevisions[prKey1] = &cachedPackageRevision{
		PackageRevision:  fpr1,
		metadataStore:    mockMeta,
		isLatestRevision: false,
	}
	cr.cachedPackageRevisions[prKey2] = &cachedPackageRevision{
		PackageRevision:  fpr2,
		metadataStore:    mockMeta,
		isLatestRevision: false,
	}

	// Identify latest revisions (revision 2 should be latest)
	identifyLatestRevisions(context.TODO(), cr.cachedPackageRevisions)

	// Verify revision 2 is marked as latest before deletion
	assert.True(t, cr.cachedPackageRevisions[prKey2].IsLatestRevision())
	assert.False(t, cr.cachedPackageRevisions[prKey1].IsLatestRevision())

	// Delete the latest revision (revision 2)
	err := cr.DeletePackageRevision(context.TODO(), cr.cachedPackageRevisions[prKey2])
	assert.NoError(t, err)

	// Give time for async notification to complete
	time.Sleep(100 * time.Millisecond)

	// Now delete pkg1 - should only send 1 notification (deletion) since no more revisions remain
	mockNotifier.EXPECT().NotifyPackageRevisionChange(watch.Deleted, mock.Anything).Return(1).Once()
	// No async notification expected since no revisions remain

	err = cr.DeletePackageRevision(context.TODO(), cr.cachedPackageRevisions[prKey1])
	assert.NoError(t, err)

	// Give time for any potential async notification
	time.Sleep(100 * time.Millisecond)

	// Verify all expected mock calls were made
	mockNotifier.AssertExpectations(t)
}

func TestCachedRepoBranchCommitHash(t *testing.T) {
	mockRepo := mockrepo.NewMockRepository(t)
	mockMeta := mockmeta.NewMockMetadataStore(t)
	mockNotifier := mockcachetypes.NewMockRepoPRChangeNotifier(t)

	repoName := "test-repo"
	namespace := "test-ns"
	repoKey := key.RepositoryKey{
		Namespace: namespace,
		Name:      repoName,
	}

	repoSpec := configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: namespace,
		},
	}

	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)
	fakeClient := testutil.NewFakeClientWithStatus(scheme, &repoSpec)

	options := cachetypes.CacheOptions{
		RepoPRChangeNotifier: mockNotifier,
		CoreClient:           fakeClient,
	}

	// Mock BranchCommitHash to return a test hash
	mockRepo.EXPECT().BranchCommitHash(mock.Anything).Return("abc123def456", nil).Once()
	mockRepo.On("Key").Return(repoKey).Maybe()

	cr := newRepository(repoKey, &repoSpec, mockRepo, mockMeta, options)

	// Test BranchCommitHash delegation
	hash, err := cr.BranchCommitHash(context.TODO())
	assert.NoError(t, err)
	assert.Equal(t, "abc123def456", hash)

	mockRepo.AssertExpectations(t)
}

// secretRecorder is a fake external repository that records the secret names it is given.
// If err is set, SetSecretName returns it (after recording the call).
type secretRecorder struct {
	fake.Repository
	mu    sync.Mutex
	last  string
	calls int
	err   error
}

func (r *secretRecorder) SetSecretName(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return r.err
	}
	r.last = name
	return nil
}

func (r *secretRecorder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *secretRecorder) lastSecret() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func applySpecTestSpec(generation int64) *configapi.Repository {
	return &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns", Generation: generation},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				Repo:      "https://example.com/repo.git",
				SecretRef: configapi.SecretRef{Name: fmt.Sprintf("secret-%d", generation)},
			},
		},
	}
}

func TestApplySpecIgnoresStaleGeneration(t *testing.T) {
	ext := &secretRecorder{}
	r := newApplySpecTestRepo(applySpecTestSpec(1), ext)

	r.applySpec(context.Background(), applySpecTestSpec(5))
	r.applySpec(context.Background(), applySpecTestSpec(4)) // stale snapshot

	assert.Equal(t, int64(5), r.repoSpec.Generation)
	assert.Equal(t, "secret-5", ext.lastSecret())
}

func TestApplySpecConcurrentNeverRollsBack(t *testing.T) {
	ext := &secretRecorder{}
	r := newApplySpecTestRepo(applySpecTestSpec(0), ext)

	const newest = 50
	var wg sync.WaitGroup
	for g := int64(1); g <= newest; g++ {
		wg.Add(1)
		go func(g int64) {
			defer wg.Done()
			r.applySpec(context.Background(), applySpecTestSpec(g))
		}(g)
	}
	wg.Wait()

	// Whatever order the calls ran in, the newest spec and its secret must win.
	assert.Equal(t, int64(newest), r.repoSpec.Generation)
	assert.Equal(t, fmt.Sprintf("secret-%d", newest), ext.lastSecret())
}

func TestApplySpecNonGitSpecDoesNotTouchSecret(t *testing.T) {
	ext := &secretRecorder{}
	r := newApplySpecTestRepo(applySpecTestSpec(1), ext)

	ociSpec := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns", Generation: 2},
		Spec:       configapi.RepositorySpec{Type: configapi.RepositoryTypeOCI},
	}
	r.applySpec(context.Background(), ociSpec)

	assert.Same(t, ociSpec, r.repoSpec)
	assert.Equal(t, 0, ext.callCount(), "SetSecretName must not be called for a non-git spec")
}

func TestApplySpecSecretErrorStillAppliesSpecAndReleasesLock(t *testing.T) {
	ext := &secretRecorder{err: errors.New("secret not found")}
	r := newApplySpecTestRepo(applySpecTestSpec(1), ext)

	// A failed secret switch is logged; the spec is still applied.
	failing := applySpecTestSpec(2)
	r.applySpec(context.Background(), failing)
	assert.Same(t, failing, r.repoSpec)
	assert.Equal(t, 1, ext.callCount())

	// The lock must have been released: a later call (e.g. once the Secret exists) completes
	// and retries the secret switch for the same generation.
	ext.mu.Lock()
	ext.err = nil
	ext.mu.Unlock()

	done := make(chan struct{})
	go func() {
		r.applySpec(context.Background(), applySpecTestSpec(2))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("applySpec did not return; specMutex was not released after an error")
	}
	assert.Equal(t, 2, ext.callCount())
	assert.Equal(t, "secret-2", ext.lastSecret())
}

func newApplySpecTestRepo(spec *configapi.Repository, ext repository.Repository) *cachedRepository {
	return &cachedRepository{key: key.RepositoryKey{Namespace: "ns", Name: "repo"}, repoSpec: spec, repo: ext}
}
