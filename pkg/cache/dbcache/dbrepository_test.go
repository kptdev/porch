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
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	mocksql "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/dbcache"
	mockcachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func (t *DbTestSuite) TestDBRepository() {
	shellRepo := dbRepository{
		repoKey: key.RepositoryKey{
			Namespace: "my-ns",
			Name:      "my-repo-name",
		},
	}

	t.Equal("my-ns", shellRepo.KubeObjectNamespace())
	t.Equal("my-repo-name", shellRepo.KubeObjectName())
	t.Equal(types.UID("82d3ab92-4a01-5679-8c52-b1c3daf6f016"), shellRepo.UID())
	t.Equal(key.RepositoryKey{Namespace: "my-ns", Name: "my-repo-name"}, shellRepo.Key())
}

func (t *DbTestSuite) TestDBRepositoryPRCrud() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache

	externalrepo.ExternalRepoInUnitTestMode = true

	ctx := t.Context()

	testRepo := t.createTestRepo("my-ns", "my-repo-name")
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)

	version, err := testRepo.Version(t.Context())
	t.Require().NoError(err)
	t.Equal("", version)

	// Test BranchCommitHash delegation
	commitHash, err := testRepo.BranchCommitHash(t.Context())
	t.Require().NoError(err)
	t.Equal("fake-commit-hash", commitHash) // Fake repo returns this

	pkgList, err := testRepo.ListPackages(ctx, repository.ListPackageFilter{})
	t.Require().NoError(err)
	t.Equal(0, len(pkgList))

	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Equal(0, len(prList))

	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "my-repo-name",
			PackageName:    "my-package",
			WorkspaceName:  "my-workspace",
		},
	}
	newPRDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)
	t.Require().NotNil(newPRDraft)

	newPR, err := testRepo.ClosePackageRevisionDraft(ctx, newPRDraft, -1)
	t.Require().NoError(err)
	t.Require().NotNil(newPR)

	updatedPRDraft, err := testRepo.UpdatePackageRevision(ctx, newPR)
	t.Require().NoError(err)
	t.Require().NotNil(updatedPRDraft)

	err = updatedPRDraft.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycleProposed)
	t.Require().NoError(err)

	updatedPR, err := testRepo.ClosePackageRevisionDraft(ctx, updatedPRDraft, -1)
	t.Require().NoError(err)
	t.Require().NotNil(updatedPR)

	updatedPRDraft, err = testRepo.UpdatePackageRevision(ctx, newPR)
	t.Require().NoError(err)
	t.Require().NotNil(updatedPRDraft)

	err = updatedPRDraft.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished)
	t.Require().NoError(err)

	prMeta := updatedPR.GetMeta()
	prMeta.Finalizers = []string{"a-finalizer"}
	err = updatedPR.SetMeta(ctx, prMeta)
	t.Require().NoError(err)

	updatedPR, err = testRepo.ClosePackageRevisionDraft(ctx, updatedPRDraft, -1)
	t.Require().NoError(err)
	t.Require().NotNil(updatedPR)

	fakeRepo := testRepo.externalRepo.(*fake.Repository)
	fakeExtPR := fake.FakePackageRevision{
		PrKey: updatedPR.Key(),
	}
	fakeRepo.PackageRevisions = append(fakeRepo.PackageRevisions, &fakeExtPR)

	err = testRepo.DeletePackageRevision(ctx, updatedPR)
	t.Require().NoError(err)

	prMeta = updatedPR.GetMeta()
	prMeta.Finalizers = nil
	err = updatedPR.SetMeta(ctx, prMeta)
	t.Require().NoError(err)

	err = testRepo.DeletePackageRevision(ctx, updatedPR)
	t.Require().NoError(err)

	err = testRepo.Close(ctx)
	t.Require().NoError(err)
}

func (t *DbTestSuite) TestDBRepositorySync() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache

	externalrepo.ExternalRepoInUnitTestMode = true

	ctx := t.Context()

	testRepo := t.createTestRepo("my-ns", "my-repo-name")
	testRepo.spec = &configapi.Repository{
		Spec: configapi.RepositorySpec{},
	}
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)

	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	repoObj := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-repo-name",
			Namespace: "my-ns",
		},
	}
	fakeClient := k8sfake.NewClientBuilder().WithScheme(scheme).WithObjects(repoObj).Build()
	cacheOptions := cachetypes.CacheOptions{
		CoreClient: fakeClient,
	}

	testRepo.repositorySync = newRepositorySync(testRepo, cacheOptions)

	err = testRepo.Refresh(ctx)
	t.Require().True(err == nil || strings.Contains(err.Error(), "already in progress"))

	err = testRepo.Close(ctx)
	t.Require().NoError(err)
}

func (t *DbTestSuite) TestDBRepositorCorner() {
	t.switchToMockSQL()

	mockSQL := dbHandler.db.(*mocksql.MockdbSQLInterface)
	mockSQL.EXPECT().Exec(
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything).Return(nil, errors.New("DB exec failed")).Maybe()

	mockSQL.EXPECT().Close().Return(errors.New("DB close failed")).Maybe()

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache

	externalrepo.ExternalRepoInUnitTestMode = true

	ctx := t.Context()

	dbRepo := dbRepository{
		repoKey: key.RepositoryKey{
			Namespace: "my-ns",
			Name:      "my-repo-name",
		},
	}
	err := repoWriteToDB(ctx, &dbRepo)
	t.Require().NotNil(err)

	t.revertToPostgreSQL()
}

// newPushDraftsToGitTestRepo returns a repository backed by a mock external repository, so
// that calls made to git are asserted rather than silently absorbed by the fake repository.
func (t *DbTestSuite) newPushDraftsToGitTestRepo(namespace, name string, pushDraftsToGit bool) (*dbRepository, *mockrepo.MockRepository) {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	externalrepo.ExternalRepoInUnitTestMode = true

	testRepo := t.createTestRepo(namespace, name)
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	extRepo := mockrepo.NewMockRepository(t.T())
	extRepo.EXPECT().Key().Return(key.RepositoryKey{Namespace: namespace, Name: name}).Maybe()

	testRepo.externalRepo = extRepo
	testRepo.pushDraftsToGit = pushDraftsToGit

	return testRepo, extRepo
}

// TestDBRepositoryDeleteDraftWithPushDraftsToGit checks that deleting an unpublished
// package revision also deletes it from git when pushDraftsToGit is set. The draft branch
// only exists in git because the flag is set, so it has to be cleaned up on delete;
// otherwise the branch is orphaned.
func (t *DbTestSuite) TestDBRepositoryDeleteDraftWithPushDraftsToGit() {
	ctx := t.Context()
	namespace := "my-ns"
	repoName := "delete-draft-push-repo"

	testRepo, extRepo := t.newPushDraftsToGitTestRepo(namespace, repoName, true)

	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: repoName,
			PackageName:    "my-package",
			WorkspaceName:  "my-workspace",
			Lifecycle:      porchapi.PackageRevisionLifecycleDraft,
		},
	}

	prDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)

	dbPR, err := testRepo.ClosePackageRevisionDraft(ctx, prDraft, 0)
	t.Require().NoError(err)
	t.Require().Equal(porchapi.PackageRevisionLifecycleDraft, dbPR.Lifecycle(ctx))

	// Drafts are stored in the database; git cleanup on delete is still required when pushDraftsToGit is set.
	// This would fail if the external delete were still gated on the lifecycle being published.
	extRepo.EXPECT().DeletePackageRevision(mock.Anything, dbPR).Return(nil).Once()

	err = testRepo.DeletePackageRevision(ctx, dbPR)
	t.Require().NoError(err)

	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Empty(prList, "package revision should be gone from the database")

	t.deleteTestRepo(testRepo.Key())
}

// TestDBRepositoryDeleteDraftWithoutPushDraftsToGit is the counterpart of the test above:
// with pushDraftsToGit unset, an unpublished package revision never existed in git, so no
// delete may be issued against it. No expectation is registered for
// DeletePackageRevision, which makes the mock fail the test if it is called.
func (t *DbTestSuite) TestDBRepositoryDeleteDraftWithoutPushDraftsToGit() {
	ctx := t.Context()
	namespace := "my-ns"
	repoName := "delete-draft-no-push-repo"

	testRepo, _ := t.newPushDraftsToGitTestRepo(namespace, repoName, false)

	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: repoName,
			PackageName:    "my-package",
			WorkspaceName:  "my-workspace",
			Lifecycle:      porchapi.PackageRevisionLifecycleDraft,
		},
	}

	prDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)

	dbPR, err := testRepo.ClosePackageRevisionDraft(ctx, prDraft, 0)
	t.Require().NoError(err)

	err = testRepo.DeletePackageRevision(ctx, dbPR)
	t.Require().NoError(err)

	prList, err := testRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	t.Require().NoError(err)
	t.Empty(prList)

	t.deleteTestRepo(testRepo.Key())
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

	assert.Equal(t, int64(5), r.spec.Generation)
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
	assert.Equal(t, int64(newest), r.spec.Generation)
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

	assert.Same(t, ociSpec, r.spec)
	assert.Equal(t, 0, ext.callCount(), "SetSecretName must not be called for a non-git spec")
}

func TestApplySpecSecretErrorStillAppliesSpecAndReleasesLock(t *testing.T) {
	ext := &secretRecorder{err: errors.New("secret not found")}
	r := newApplySpecTestRepo(applySpecTestSpec(1), ext)

	// A failed secret switch is logged; the spec is still applied.
	failing := applySpecTestSpec(2)
	r.applySpec(context.Background(), failing)
	assert.Same(t, failing, r.spec)
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

func newApplySpecTestRepo(spec *configapi.Repository, ext repository.Repository) *dbRepository {
	return &dbRepository{repoKey: key.RepositoryKey{Namespace: "ns", Name: "repo"}, spec: spec, externalRepo: ext}
}
