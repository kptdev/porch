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

package contentcache

import (
	"testing"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	mockcachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	testRepoKey = repository.RepositoryKey{Namespace: "ns", Name: "repo"}
	testPkg     = "my-pkg"
	testWS      = "ws-1"

	// Full key as produced by externalrepo.RepositoryKey for testRepoSpec.
	testFullRepoKey = repository.RepositoryKey{Namespace: "ns", Name: "repo", Path: "", PlaceholderWSname: "main"}

	testRepoSpec = &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "repo"},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeGit,
			Git:  &configapi.GitRepository{Repo: "https://example.com/repo.git", Branch: "main"},
		},
	}
)

// mockGetRepo sets up the GetRepositories → GetRepository chain on mockCache.
func mockGetRepo(mockCache *mockcachetypes.MockCache, mockRepo *mockrepository.MockRepository) {
	mockCache.EXPECT().GetRepositories().Return([]*configapi.Repository{testRepoSpec}).Maybe()
	mockCache.EXPECT().GetRepository(testFullRepoKey).Return(mockRepo).Maybe()
}

// mockGetRepoNotFound sets up GetRepositories returning empty (repo not in cache).
func mockGetRepoNotFound(mockCache *mockcachetypes.MockCache) {
	mockCache.EXPECT().GetRepositories().Return([]*configapi.Repository{}).Maybe()
}

func setupCacheWithPkgRev(t *testing.T, lifecycle porchapi.PackageRevisionLifecycle) (*contentCache, *mockcachetypes.MockCache, *mockrepository.MockRepository, *mockrepository.MockPackageRevision) {
	t.Helper()
	mockCache := mockcachetypes.NewMockCache(t)
	mockRepo := mockrepository.NewMockRepository(t)
	mockPkgRev := mockrepository.NewMockPackageRevision(t)

	mockGetRepo(mockCache, mockRepo)
	mockRepo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).
		Return([]repository.PackageRevision{mockPkgRev}, nil).Maybe()
	mockPkgRev.EXPECT().Key().Return(repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{Package: testPkg},
		WorkspaceName: testWS,
	}).Maybe()
	mockPkgRev.EXPECT().Lifecycle(mock.Anything).Return(lifecycle).Maybe()

	cc := &contentCache{cache: mockCache}
	return cc, mockCache, mockRepo, mockPkgRev
}
