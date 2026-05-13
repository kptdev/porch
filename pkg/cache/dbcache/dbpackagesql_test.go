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
	"database/sql"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/repository"
	mockcachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (t *DbTestSuite) TestPackageDBWriteRead() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(&dbRepository{})

	dbRepo := dbRepository{
		repoKey: repository.RepositoryKey{
			Namespace:         "my-ns",
			Name:              "my-repo",
			Path:              "my-path",
			PlaceholderWSname: "my-placeholder-ws",
		},
		meta:       metav1.ObjectMeta{},
		spec:       nil,
		updated:    time.Now().UTC(),
		updatedBy:  "porchuser",
		deployment: false,
	}

	dbPkg := dbPackage{
		pkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Namespace: "my-ns",
				Name:      "my-repo",
			},
			Path:    "my-path-to-pkg",
			Package: "network-function",
		},
		meta:      metav1.ObjectMeta{},
		spec:      nil,
		updated:   time.Now().UTC(),
		updatedBy: "porchuser",
	}

	dbPkgUpdate := dbPackage{
		repo: &dbRepo,
		pkgKey: repository.PackageKey{
			RepoKey: dbRepo.Key(),
			Path:    "my-path-to-pkg",
			Package: "network-function",
		},
		meta: metav1.ObjectMeta{
			Name:      "meta-new-name",
			Namespace: "meta-new-namespace",
		},
		spec:      &porchapi.PackageSpec{},
		updated:   time.Now().UTC(),
		updatedBy: "porchuser2",
	}

	t.pkgDBWriteReadTest(&dbRepo, dbPkg, dbPkgUpdate)

	dbPkg.pkgKey.Path = ""
	dbPkgUpdate.pkgKey.Path = ""
	dbPkgUpdate.updatedBy = "bart"
	t.pkgDBWriteReadTest(&dbRepo, dbPkg, dbPkgUpdate)
}

func (t *DbTestSuite) TestPackageDBSchema() {
	dbPkg := dbPackage{}
	err := pkgWriteToDB(t.Context(), &dbPkg)
	t.Require().ErrorContains(err, "violates check constraint")

	dbPkg.pkgKey = repository.PackageKey{
		RepoKey: repository.RepositoryKey{
			Namespace: "my-ns",
		},
	}
	err = pkgWriteToDB(t.Context(), &dbPkg)
	t.Require().ErrorContains(err, "violates check constraint")

	dbPkg.pkgKey.RepoKey.Name = "my-repo"
	err = pkgWriteToDB(t.Context(), &dbPkg)
	t.Require().ErrorContains(err, "violates check constraint")

	dbPkg.pkgKey.Package = "my-package"
	err = pkgWriteToDB(t.Context(), &dbPkg)
	t.Require().ErrorContains(err, "violates foreign key constraint")

	dbRepo := dbRepository{
		repoKey: repository.RepositoryKey{
			Namespace: "my-ns",
			Name:      "my-repo",
		},
	}
	err = repoWriteToDB(t.Context(), &dbRepo)
	t.Require().NoError(err)

	err = pkgWriteToDB(t.Context(), &dbPkg)
	t.Require().NoError(err)

	dbPkg.pkgKey.Path = "my-path"
	err = pkgUpdateDB(t.Context(), &dbPkg)
	t.Require().ErrorContains(err, "no rows or multiple rows found for updating")

	dbPkg.pkgKey.Path = ""
	dbPkg.updatedBy = "Marge"
	err = pkgUpdateDB(t.Context(), &dbPkg)
	t.Require().NoError(err)

	dbPkg.pkgKey.Path = "my-path"
	err = pkgUpdateDB(t.Context(), &dbPkg)
	t.Require().ErrorContains(err, "no rows or multiple rows found for updating")

	err = repoDeleteFromDB(t.Context(), dbRepo.Key())
	t.Require().NoError(err)

	_, err = pkgReadFromDB(t.Context(), dbPkg.Key())
	t.Require().NotNil(err)
	t.Equal(sql.ErrNoRows, err)
}

func (t *DbTestSuite) TestMultiPackageRepo() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(&dbRepository{})

	dbRepo11 := t.createTestRepo("my-ns1", "my-repo1")
	dbRepo12 := t.createTestRepo("my-ns1", "my-repo2")
	dbRepo21 := t.createTestRepo("my-ns2", "my-repo1")
	dbRepo22 := t.createTestRepo("my-ns2", "my-repo2")

	dbRepo11Pkgs := t.createTestPkgs(dbRepo11.Key(), "my-package", 4)
	dbRepo12Pkgs := t.createTestPkgs(dbRepo12.Key(), "my-package", 4)
	dbRepo21Pkgs := t.createTestPkgs(dbRepo21.Key(), "my-package", 4)
	dbRepo22Pkgs := t.createTestPkgs(dbRepo22.Key(), "my-package", 4)

	readRep11Pkgs, err := pkgReadPkgsFromDB(t.Context(), dbRepo11.Key())
	t.Require().NoError(err)

	readRep12Pkgs, err := pkgReadPkgsFromDB(t.Context(), dbRepo12.Key())
	t.Require().NoError(err)

	readRep21Pkgs, err := pkgReadPkgsFromDB(t.Context(), dbRepo21.Key())
	t.Require().NoError(err)

	readRep22Pkgs, err := pkgReadPkgsFromDB(t.Context(), dbRepo22.Key())
	t.Require().NoError(err)

	t.assertPackageListsEqual(dbRepo11Pkgs, readRep11Pkgs, 4)
	t.assertPackageListsEqual(dbRepo12Pkgs, readRep12Pkgs, 4)
	t.assertPackageListsEqual(dbRepo21Pkgs, readRep21Pkgs, 4)
	t.assertPackageListsEqual(dbRepo22Pkgs, readRep22Pkgs, 4)

	t.deleteTestRepo(dbRepo11.Key())
	t.deleteTestRepo(dbRepo12.Key())
	t.deleteTestRepo(dbRepo21.Key())
	t.deleteTestRepo(dbRepo22.Key())
}

func (t *DbTestSuite) TestPackageFilter() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(&dbRepository{})

	dbRepo := t.createTestRepo("my-ns", "my-repo")
	dbRepoPkgs := t.createTestPkgs(dbRepo.Key(), "my-package", 4)

	dbRepo2 := t.createTestRepo("my-ns", "other-repo")
	dbRepo2Pkgs := t.createTestPkgs(dbRepo2.Key(), "my-package", 2)

	// Create a nested package (same leaf name, different path)
	nestedPkg := t.createTestPkgWithPath(dbRepo.Key(), "my-package-0", "catalog")

	t.Run("EmptyFilter", func() {
		pkgFilter := repository.ListPackageFilter{}
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Len(listPkgs, 7)
	})

	t.Run("RepoAndPackageName", func() {
		pkgFilter := repository.ListPackageFilter{}
		pkgFilter.Key.RepoKey = dbRepo.repoKey
		pkgFilter.Key.Package = "my-package-2"
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Len(listPkgs, 1)
	})

	t.Run("RepoAndNonExistentPackage", func() {
		pkgFilter := repository.ListPackageFilter{}
		pkgFilter.Key.RepoKey = dbRepo.repoKey
		pkgFilter.Key.Package = "my-package-5"
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Empty(listPkgs)
	})

	t.Run("RepoAndPackageWithNonMatchingPath", func() {
		pkgFilter := repository.ListPackageFilter{}
		pkgFilter.Key.RepoKey = dbRepo.repoKey
		pkgFilter.Key.Package = "my-package-2"
		pkgFilter.Key.Path = "a/path"
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Empty(listPkgs)
	})

	t.Run("RepoAndPackageWithEmptyPath", func() {
		pkgFilter := repository.ListPackageFilter{}
		pkgFilter.Key.RepoKey = dbRepo.repoKey
		pkgFilter.Key.Package = "my-package-2"
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Len(listPkgs, 1)
	})

	t.Run("PackageNameOnlySharedAcrossRepos", func() {
		pkgFilter := repository.ListPackageFilter{}
		pkgFilter.Key.Package = "my-package-0"
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Len(listPkgs, 3) // root in each repo + nested in my-repo
	})

	t.Run("PackageNameOnlyUniqueToOneRepo", func() {
		pkgFilter := repository.ListPackageFilter{}
		pkgFilter.Key.Package = "my-package-3"
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Len(listPkgs, 1)
	})

	t.Run("PackageNameOnlyNonExistent", func() {
		pkgFilter := repository.ListPackageFilter{}
		pkgFilter.Key.Package = "no-such-package"
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Empty(listPkgs)
	})

	t.Run("RepositoryNameOnly", func() {
		pkgFilter := repository.ListPackageFilter{}
		pkgFilter.Key.RepoKey.Name = "my-repo"
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Len(listPkgs, 5)
	})

	t.Run("NamespaceOnly", func() {
		pkgFilter := repository.ListPackageFilter{}
		pkgFilter.Key.RepoKey.Namespace = "my-ns"
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Len(listPkgs, 7)
	})

	t.Run("PackageNameAndPathNoRepo", func() {
		pkgFilter := repository.ListPackageFilter{}
		pkgFilter.Key.Package = "my-package-0"
		pkgFilter.Key.Path = "catalog"
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Len(listPkgs, 1)
	})

	t.Run("PackageNameAndNonMatchingPathNoRepo", func() {
		pkgFilter := repository.ListPackageFilter{}
		pkgFilter.Key.Package = "my-package-0"
		pkgFilter.Key.Path = "other"
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Empty(listPkgs)
	})

	t.Run("PathOnlyNoRepo", func() {
		pkgFilter := repository.ListPackageFilter{}
		pkgFilter.Key.Path = "catalog"
		listPkgs, err := pkgListPkgsFromDB(t.Context(), pkgFilter)
		t.Require().NoError(err)
		t.Len(listPkgs, 1)
	})

	t.deleteTestRepo(dbRepo2.Key())
	t.deleteTestRepo(dbRepo.Key())

	t.Empty(t.readRepoPkgPRs(dbRepoPkgs))
	_ = dbRepo2Pkgs
	_ = nestedPkg
}

func (t *DbTestSuite) pkgDBWriteReadTest(dbRepo *dbRepository, dbPkg, dbPkgUpdate dbPackage) {
	err := pkgWriteToDB(t.Context(), &dbPkg)
	t.Require().ErrorContains(err, "violates foreign key constraint")

	err = repoWriteToDB(t.Context(), dbRepo)
	t.Require().NoError(err)

	dbPkg.repo = dbRepo
	dbPkg.pkgKey.RepoKey = dbRepo.Key()

	err = pkgWriteToDB(t.Context(), &dbPkg)
	t.Require().NoError(err)

	readPkg, err := pkgReadFromDB(t.Context(), dbPkg.Key())
	t.Require().NoError(err)
	t.assertPackagesEqual(&dbPkg, readPkg)

	err = pkgWriteToDB(t.Context(), &dbPkgUpdate)
	t.Require().ErrorContains(err, "violates unique constraint")

	err = pkgUpdateDB(t.Context(), &dbPkgUpdate)
	t.Require().NoError(err)

	readPkg, err = pkgReadFromDB(t.Context(), dbPkg.Key())
	t.Require().NoError(err)
	t.assertPackagesEqual(&dbPkgUpdate, readPkg)

	err = pkgDeleteFromDB(t.Context(), dbPkg.Key())
	t.Require().NoError(err)

	_, err = pkgReadFromDB(t.Context(), dbPkg.Key())
	t.Require().NotNil(err)
	t.Equal(sql.ErrNoRows, err)

	err = pkgUpdateDB(t.Context(), &dbPkgUpdate)
	t.Require().ErrorContains(err, "no rows or multiple rows found for updating")

	err = pkgDeleteFromDB(t.Context(), dbPkg.Key())
	t.Require().NoError(err)

	err = repoDeleteFromDB(t.Context(), dbRepo.Key())
	t.Require().NoError(err)
}

func (t *DbTestSuite) assertPackageListsEqual(left []dbPackage, right []*dbPackage, count int) {
	t.Len(left, count)
	t.Len(right, count)

	leftMap := make(map[repository.PackageKey]*dbPackage)
	for _, leftPkg := range left {
		leftMap[leftPkg.Key()] = &leftPkg
	}

	rightMap := make(map[repository.PackageKey]*dbPackage)
	for _, rightPkg := range right {
		rightMap[rightPkg.Key()] = rightPkg
	}

	for leftKey, leftPkg := range leftMap {
		rightPkg, ok := rightMap[leftKey]
		t.True(ok)

		t.assertPackagesEqual(leftPkg, rightPkg)
	}
}

func (t *DbTestSuite) assertPackagesEqual(left, right *dbPackage) {
	t.Equal(left.Key(), right.Key())
	t.Equal(left.meta.Namespace, right.meta.Namespace)
	t.Equal(left.meta.Name, right.meta.Name)
	t.Equal(left.spec, right.spec)
	t.Equal(left.updatedBy, right.updatedBy)
}
