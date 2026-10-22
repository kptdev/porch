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

package dbcache

import (
	"errors"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/testutil"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo"
	"github.com/kptdev/porch/pkg/repository/prrbackend"
	mockprrstore "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/prrstore"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// newPRRStoreTestCache returns a dbCache wired to a mock PRRStore, plus a v1alpha2
// repository spec for it. The mock fails the test on any call that is not expected.
func (t *DbTestSuite) newPRRStoreTestCache(repoName string) (*dbCache, *mockprrstore.MockPRRStore, *configapi.Repository) {
	t.T().Helper()
	externalrepo.ExternalRepoInUnitTestMode = true

	spec := &configapi.Repository{
		ObjectMeta: v1.ObjectMeta{
			Namespace:   "prr-ns",
			Name:        repoName,
			Annotations: map[string]string{configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled},
		},
	}
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	cache, err := new(DBCacheFactory).NewCache(t.Context(), cachetypes.CacheOptions{
		CoreClient: testutil.NewFakeClientWithStatus(scheme, spec),
	})
	t.Require().NoError(err)

	dbc, ok := cache.(*dbCache)
	t.Require().True(ok)

	store := mockprrstore.NewMockPRRStore(t.T())
	dbc.SetPRRStore(store)
	return dbc, store, spec
}

// Closing an open v1alpha2 repository must remove its PRRStore rows, or they survive
// the repository and list as ghost drafts if the name is registered again.
func (t *DbTestSuite) TestCloseRepositoryDeletesPRRStoreContent() {
	dbc, store, spec := t.newPRRStoreTestCache("close-open")

	repo, err := dbc.OpenRepository(t.Context(), spec)
	t.Require().NoError(err)
	_, isPRR := repo.(*prrbackend.Repository)
	t.Require().True(isPRR, "annotation must select the v1alpha2 handle")

	repoKey, err := externalrepo.RepositoryKey(spec)
	t.Require().NoError(err)
	store.EXPECT().DeleteRepositoryResources(t.Context(), repoKey).Return(nil).Once()

	t.Require().NoError(dbc.CloseRepository(t.Context(), spec, nil))
	t.Nil(dbc.GetRepository(repoKey), "handle must be dropped from the cache")
}

// After a restart the handle is not in the in-memory map, but the rows are still there.
func (t *DbTestSuite) TestCloseRepositoryNotCachedDeletesPRRStoreContent() {
	dbc, store, spec := t.newPRRStoreTestCache("close-uncached")

	repoKey, err := externalrepo.RepositoryKey(spec)
	t.Require().NoError(err)
	t.Require().Nil(dbc.GetRepository(repoKey))
	store.EXPECT().DeleteRepositoryResources(t.Context(), repoKey).Return(nil).Once()

	t.Require().NoError(dbc.CloseRepository(t.Context(), spec, nil))
}

// A failed cleanup must fail the close, so the repository controller retries the
// deletion instead of dropping its finalizer with rows left behind.
func (t *DbTestSuite) TestCloseRepositoryReportsPRRStoreCleanupFailure() {
	dbc, store, spec := t.newPRRStoreTestCache("close-fails")

	_, err := dbc.OpenRepository(t.Context(), spec)
	t.Require().NoError(err)

	repoKey, err := externalrepo.RepositoryKey(spec)
	t.Require().NoError(err)
	store.EXPECT().DeleteRepositoryResources(t.Context(), repoKey).Return(errors.New("db down")).Once()

	err = dbc.CloseRepository(t.Context(), spec, nil)
	t.Require().Error(err)
	t.Contains(err.Error(), "db down")

	// The retry finds no handle (it was dropped) and must still clean up.
	store.EXPECT().DeleteRepositoryResources(t.Context(), repoKey).Return(nil).Once()
	t.Require().NoError(dbc.CloseRepository(t.Context(), spec, nil))
}

// Evicting a handle (closing it without deleting the repository) must keep the rows.
func (t *DbTestSuite) TestEvictCachedRepositoryKeepsPRRStoreContent() {
	dbc, _, spec := t.newPRRStoreTestCache("evict-keeps")

	_, err := dbc.OpenRepository(t.Context(), spec)
	t.Require().NoError(err)

	// No DeleteRepositoryResources expectation: the mock fails the test if it is called.
	t.Require().NoError(dbc.EvictCachedRepository(t.Context(), spec.Namespace, spec.Name))

	repoKey, err := externalrepo.RepositoryKey(spec)
	t.Require().NoError(err)
	t.Nil(dbc.GetRepository(repoKey))
}
