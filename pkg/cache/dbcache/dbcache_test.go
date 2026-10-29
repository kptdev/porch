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
	"fmt"
	"os/user"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/kptdev/porch/api/porch/key"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/testutil"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo"
	"github.com/kptdev/porch/pkg/repository"
	mockdbcache "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/dbcache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

const defaultPorchSQLSchema = "api/sql/porch-db.sql"

type DbTestSuite struct {
	suite.Suite

	ctx            context.Context
	nextPkgRev     int
	savedDBHandler *DBHandler
}

func Test_DbTestSuite(t *testing.T) {
	if u, err := user.Current(); err == nil && u.Username == "root" {
		t.Fatalf("This test cannot run as %q user", u.Username)
	}

	suite.Run(t, &DbTestSuite{nextPkgRev: 1, ctx: t.Context()})
}

func (t *DbTestSuite) Context() context.Context {
	t.T().Helper()
	return t.ctx
}

func (t *DbTestSuite) SetupSuite() {
	if err := godotenv.Load("../../../.env"); err != nil {
		t.T().Logf("Failed to load .env file: %v", err)
	}

	pg := testutil.StartEmbeddedPostgres(t.T())

	dbOpts := &cachetypes.CacheOptions{
		DBCacheOptions: cachetypes.DBCacheOptions{
			Driver:     "pgx",
			DataSource: pg.URL,
		},
	}

	err := OpenDB(t.Context(), *dbOpts)
	t.Require().NoError(err, "could not connect to test database")

	t.T().Cleanup(func() {
		// TODO: is this sleep necessary?
		time.Sleep(5 * time.Second)

		if err := CloseDB(t.Context()); err != nil {
			t.T().Log(err)
		}
	})

	testutil.ApplySchema(t.T(), defaultPorchSQLSchema, func(sql string) error {
		_, err := GetDB().db.Exec(context.Background(), sql)
		return err
	})
}

type mockNotifier struct {
	calls []struct {
		eventType watch.EventType
		obj       repository.PackageRevision
	}
	returnVal int
}

func (n *mockNotifier) NotifyPackageRevisionChange(eventType watch.EventType, obj repository.PackageRevision) int {
	n.calls = append(n.calls, struct {
		eventType watch.EventType
		obj       repository.PackageRevision
	}{eventType: eventType, obj: obj})
	return n.returnVal
}

func (t *DbTestSuite) switchToMockSQL() {
	mockDBCache := mockdbcache.NewMockdbSQLInterface(t.T())

	t.savedDBHandler = GetDB()
	dbHandler = nil

	err := CloseDB(t.Context())
	t.NoError(err)

	dbHandler = &DBHandler{
		dBCacheOptions: t.savedDBHandler.dBCacheOptions,
		dataSource:     t.savedDBHandler.dataSource,
		db:             mockDBCache,
	}
	t.NotNil(dbHandler)
}

func (t *DbTestSuite) revertToPostgreSQL() {
	dbHandler = t.savedDBHandler
}

func (t *DbTestSuite) TestDBRepositoryCrud() {
	externalrepo.ExternalRepoInUnitTestMode = true

	ctx := t.Context()
	repositorySpec := &configapi.Repository{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "my-ns",
			Name:      "my-repo",
		},
	}
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	fakeClient := testutil.NewFakeClientWithStatus(scheme, repositorySpec)

	options := cachetypes.CacheOptions{
		CoreClient: fakeClient,
	}
	dbCache, err := new(DBCacheFactory).NewCache(ctx, options)
	t.NoError(err)
	t.Empty(dbCache.GetRepositories())

	testRepo, err := dbCache.OpenRepository(ctx, repositorySpec)
	t.NoError(err)
	t.Equal("my-repo", testRepo.Key().Name)

	gotRepo := dbCache.GetRepository(testRepo.Key())
	t.Equal(testRepo.Key(), gotRepo.Key())

	repositorySpec.Spec.Description = "My lovely Repo"

	err = dbCache.UpdateRepository(ctx, repositorySpec)
	t.NoError(err)

	err = dbCache.CloseRepository(ctx, repositorySpec, nil)
	t.NoError(err)
}

func (t *DbTestSuite) TestDBRepositoryConnectivityCheck() {
	// Save original state
	originalMode := externalrepo.ExternalRepoInUnitTestMode
	defer func() {
		externalrepo.ExternalRepoInUnitTestMode = originalMode
	}()

	ctx := t.Context()
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	// Test failure case - disable unit test mode
	externalrepo.ExternalRepoInUnitTestMode = false
	failureSpec := &configapi.Repository{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-repo-fail",
		},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				Repo: "https://invalid-repo-url.com/test/repo",
			},
		},
	}
	fakeClient := testutil.NewFakeClientWithStatus(scheme, failureSpec)
	options := cachetypes.CacheOptions{
		CoreClient: fakeClient,
	}
	dbCache, err := new(DBCacheFactory).NewCache(ctx, options)
	t.NoError(err)
	_, err = dbCache.OpenRepository(ctx, failureSpec)
	assert.Error(t.T(), err, "Expected connectivity check to fail for invalid repository")

	// Test success case - enable unit test mode
	externalrepo.ExternalRepoInUnitTestMode = true
	successSpec := &configapi.Repository{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-repo-success",
		},
		Spec: configapi.RepositorySpec{
			Type: "fake", // Use fake type to avoid real network calls
		},
	}
	fakeClient = testutil.NewFakeClientWithStatus(scheme, successSpec)
	options.CoreClient = fakeClient
	dbCache, err = new(DBCacheFactory).NewCache(ctx, options)
	t.NoError(err)
	testRepo, err := dbCache.OpenRepository(ctx, successSpec)
	assert.NoError(t.T(), err, "Expected connectivity check to succeed in unit test mode")
	assert.NotNil(t.T(), testRepo)
	assert.Equal(t.T(), "test-repo-success", testRepo.Key().Name)
}

func (t *DbTestSuite) createTestRepo(namespace, name string) *dbRepository {
	dbRepo := dbRepository{
		repoKey: key.RepositoryKey{
			Namespace: namespace,
			Name:      name,
		},
		repoPRChangeNotifier: &mockNotifier{returnVal: 1},
		spec: &configapi.Repository{
			Spec: configapi.RepositorySpec{
				Git: &configapi.GitRepository{
					Repo: "http://www.gitrepo.org/my-repo",
				},
			},
		},
	}
	err := repoWriteToDB(t.Context(), &dbRepo)
	t.NoError(err)

	return &dbRepo
}

func (t *DbTestSuite) deleteTestRepo(key key.RepositoryKey) {
	err := repoDeleteFromDB(t.Context(), key)
	t.NoError(err)
}

func (t *DbTestSuite) createTestPkg(repoKey key.RepositoryKey, name string) dbPackage {
	dbPkg := dbPackage{
		repo: cachetypes.CacheInstance.GetRepository(repoKey).(*dbRepository),
		pkgKey: key.PackageKey{
			RepoKey: repoKey,
			Package: name,
		},
	}

	err := pkgWriteToDB(t.Context(), &dbPkg)
	t.NoError(err)

	return dbPkg
}

func (t *DbTestSuite) createTestPkgWithPath(repoKey key.RepositoryKey, name, path string) dbPackage {
	dbPkg := dbPackage{
		repo: cachetypes.CacheInstance.GetRepository(repoKey).(*dbRepository),
		pkgKey: key.PackageKey{
			RepoKey: repoKey,
			Package: name,
			Path:    path,
		},
	}

	err := pkgWriteToDB(t.Context(), &dbPkg)
	t.NoError(err)

	return dbPkg
}

func (t *DbTestSuite) createTestPkgs(repoKey key.RepositoryKey, namePrefix string, count int) []dbPackage {
	var testPkgs []dbPackage

	for i := range count {
		testPkgs = append(testPkgs, t.createTestPkg(repoKey, fmt.Sprintf("%s-%d", namePrefix, i)))
	}

	return testPkgs
}

func (t *DbTestSuite) createTestPR(pkgKey key.PackageKey, name string) dbPackageRevision {
	dbPkgRev := dbPackageRevision{
		pkgRevKey: key.PackageRevisionKey{
			PkgKey:        pkgKey,
			WorkspaceName: name,
			Revision:      t.nextPkgRev,
		},
		lifecycle: "Published",
		resources: map[string]string{"Hello.txt": "Hello", "Goodbye.txt": "Goodbye"},
	}

	err := pkgRevWriteToDB(t.Context(), &dbPkgRev)
	t.NoError(err)

	return dbPkgRev
}

func (t *DbTestSuite) createTestPRs(packages []dbPackage, wsNamePrefix string, count int) []dbPackageRevision {
	var testPRs []dbPackageRevision

	for _, pkg := range packages {
		t.nextPkgRev = 1
		for prNo := range count {
			testPRs = append(testPRs, t.createTestPR(pkg.Key(), fmt.Sprintf("%s-%d", wsNamePrefix, prNo)))
			t.nextPkgRev++
		}
	}
	return testPRs
}

func (t *DbTestSuite) TestEvictCachedRepository() {
	externalrepo.ExternalRepoInUnitTestMode = true
	defer func() { externalrepo.ExternalRepoInUnitTestMode = false }()

	ctx := t.Context()
	scheme := runtime.NewScheme()
	_ = configapi.AddToScheme(scheme)

	tests := []struct {
		name           string
		setupRepo      bool // whether to open the repo before evicting
		expectInDB     bool // whether repo should remain in DB after eviction
		expectCacheLen int  // expected cache length after eviction
		expectEvictErr bool
	}{
		{
			name:           "evicts opened repo from cache but keeps in DB",
			setupRepo:      true,
			expectInDB:     true,
			expectCacheLen: 0,
		},
		{
			name:           "evicting repo not in cache does not error",
			setupRepo:      false,
			expectCacheLen: 0,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func() {
			repoName := fmt.Sprintf("evict-repo-%d", i)
			repositorySpec := &configapi.Repository{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "evict-ns",
					Name:      repoName,
				},
			}
			fakeClient := testutil.NewFakeClientWithStatus(scheme, repositorySpec)
			options := cachetypes.CacheOptions{CoreClient: fakeClient}
			dbCache, err := new(DBCacheFactory).NewCache(ctx, options)
			t.NoError(err)

			var repoKey key.RepositoryKey
			if tt.setupRepo {
				repo, err := dbCache.OpenRepository(ctx, repositorySpec)
				t.NoError(err)
				t.Equal(repoName, repo.Key().Name)
				repoKey = repo.Key()

				defer func() {
					// Cleanup: EvictCachedRepository only removes from the in-memory map,
					// so we need to delete the DB row directly to avoid test pollution.
					_ = repoDeleteFromDB(ctx, repoKey)
				}()
			}

			err = dbCache.EvictCachedRepository(ctx, "evict-ns", repoName)
			if tt.expectEvictErr {
				t.Error(err)
			} else {
				t.NoError(err)
			}
			t.Equal(tt.expectCacheLen, len(dbCache.GetRepositories()))

			if tt.expectInDB {
				_, err := repoReadFromDB(ctx, repoKey)
				t.NoError(err, "Expected repo to still exist in DB after eviction")
			}
		})
	}
}
