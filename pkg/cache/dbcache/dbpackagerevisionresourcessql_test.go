// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or granted to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dbcache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util/selector"
	mockcachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	"github.com/stretchr/testify/mock"
)

func (t *DbTestSuite) TestPkgRevResourcesReadFromDBFallsBackToStdlibQuery() {
	dbPR := t.createResourcesFixture("fallback-ns", "fallback-repo", "fallback-package", "fallback-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())
	origDB := GetDB().db
	GetDB().db = &forceStdlibResourceQuerySQL{dbSQLInterface: origDB}
	defer func() { GetDB().db = origDB }()

	resources, err := pkgRevResourcesReadFromDB(t.Context(), dbPR.Key(), selector.AllFiles)

	t.Require().NoError(err)
	t.Equal("Hello", resources["Hello.txt"])
	t.Equal("Goodbye", resources["Goodbye.txt"])
}

func (t *DbTestSuite) TestPkgRevResourcesReadFromDBFallsBackToStdlibQueryWithFileFilter() {
	dbPR := t.createResourcesFixture("filter-ns", "filter-repo", "filter-package", "filter-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())
	origDB := GetDB().db
	GetDB().db = &forceStdlibResourceQuerySQL{dbSQLInterface: origDB}
	defer func() { GetDB().db = origDB }()

	resources, err := pkgRevResourcesReadFromDB(t.Context(), dbPR.Key(), selector.PRRGet{FilePaths: []string{"Hello.txt"}})

	t.Require().NoError(err)
	t.Equal(map[string]string{"Hello.txt": "Hello"}, resources)
}

func (t *DbTestSuite) TestPkgRevResourcesReadFromDBReturnsQueryErrorOnFallback() {
	dbPR := t.createResourcesFixture("queryerr-ns", "queryerr-repo", "queryerr-package", "queryerr-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())
	origDB := GetDB().db
	GetDB().db = &failingStdlibResourceQuerySQL{
		dbSQLInterface: origDB,
		queryErr:       fmt.Errorf("query failed"),
	}
	defer func() { GetDB().db = origDB }()

	resources, err := pkgRevResourcesReadFromDB(t.Context(), dbPR.Key(), selector.AllFiles)

	t.Require().Nil(resources)
	t.Require().ErrorContains(err, "query failed")
}

func (t *DbTestSuite) TestPkgRevResourcesReadFromDBReturnsScanErrorOnFallback() {
	dbPR := t.createResourcesFixture("scanerr-ns", "scanerr-repo", "scanerr-package", "scanerr-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())
	origDB := GetDB().db
	GetDB().db = &oneColumnResourceQuerySQL{dbSQLInterface: origDB}
	defer func() { GetDB().db = origDB }()

	resources, err := pkgRevResourcesReadFromDB(t.Context(), dbPR.Key(), selector.AllFiles)

	t.Require().Nil(resources)
	t.Require().Error(err)
}

func (t *DbTestSuite) TestPkgRevResourcesReadFromDBReturnsScanTwoTextColumnsError() {
	dbPR := t.createResourcesFixture("scan2-ns", "scan2-repo", "scan2-package", "scan2-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())
	origDB := GetDB().db
	GetDB().db = &failingScanTwoTextColumnsSQL{
		dbSQLInterface: origDB,
		err:            errors.New("native scan failed"),
	}
	defer func() { GetDB().db = origDB }()

	resources, err := pkgRevResourcesReadFromDB(t.Context(), dbPR.Key(), selector.AllFiles)

	t.Require().Nil(resources)
	t.Require().ErrorContains(err, "native scan failed")
}

// --- PathOnly unit tests ---

func (t *DbTestSuite) TestPkgRevResourcesQuerySQLPathOnlyOmitsValueColumn() {
	prk := repository.PackageRevisionKey{}
	prk.PkgKey.RepoKey.Namespace = "ns"
	prk.PkgKey.RepoKey.Name = "repo"
	prk.WorkspaceName = "ws"

	query, _ := pkgRevResourcesQuerySQL(prk, selector.PRRGet{PathOnly: true})
	t.NotContains(query, "resource_value")
	t.Contains(query, "resource_key")
}

func (t *DbTestSuite) TestPkgRevResourcesQuerySQLFullContentsIncludesValueColumn() {
	prk := repository.PackageRevisionKey{}
	prk.PkgKey.RepoKey.Namespace = "ns"
	prk.PkgKey.RepoKey.Name = "repo"
	prk.WorkspaceName = "ws"

	query, _ := pkgRevResourcesQuerySQL(prk, selector.AllFiles)
	t.Contains(query, "resource_value")
	t.Contains(query, "resource_key")
}

func (t *DbTestSuite) TestPkgRevResourceKeysReadFromDBReturnsKeysWithEmptyValues() {
	dbPR := t.createResourcesFixture("pathonly-ns", "pathonly-repo", "pathonly-package", "pathonly-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())

	resources, err := pkgRevResourcesReadFromDB(t.Context(), dbPR.Key(), selector.PRRGet{PathOnly: true})

	t.Require().NoError(err)
	t.Require().NotNil(resources)
	t.ElementsMatch([]string{helloResourceFile, goodbyeResourceFile}, mapKeys(resources))
	for _, v := range resources {
		t.Equal("", v)
	}
}

func (t *DbTestSuite) TestPkgRevResourceKeysReadFromDBWithFileFilterReturnsKeysWithEmptyValues() {
	dbPR := t.createResourcesFixture("pathonly-filter-ns", "pathonly-filter-repo", "pathonly-filter-package", "pathonly-filter-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())

	resources, err := pkgRevResourcesReadFromDB(t.Context(), dbPR.Key(), selector.PRRGet{
		FilePaths: []string{helloResourceFile},
		PathOnly:  true,
	})

	t.Require().NoError(err)
	t.Equal(map[string]string{helloResourceFile: ""}, resources)
}

func (t *DbTestSuite) TestPkgRevResourceKeysReadFromDBFallsBackToStdlibQuery() {
	dbPR := t.createResourcesFixture("pathonly-fallback-ns", "pathonly-fallback-repo", "pathonly-fallback-package", "pathonly-fallback-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())
	origDB := GetDB().db
	GetDB().db = &forceStdlibOneColumnQuerySQL{dbSQLInterface: origDB}
	defer func() { GetDB().db = origDB }()

	resources, err := pkgRevResourcesReadFromDB(t.Context(), dbPR.Key(), selector.PRRGet{PathOnly: true})

	t.Require().NoError(err)
	t.ElementsMatch([]string{helloResourceFile, goodbyeResourceFile}, mapKeys(resources))
	for _, v := range resources {
		t.Equal("", v)
	}
}

func (t *DbTestSuite) TestPkgRevResourceKeysReadFromDBReturnsQueryErrorOnFallback() {
	dbPR := t.createResourcesFixture("pathonly-qerr-ns", "pathonly-qerr-repo", "pathonly-qerr-package", "pathonly-qerr-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())
	origDB := GetDB().db
	GetDB().db = &failingStdlibOneColumnQuerySQL{
		dbSQLInterface: origDB,
		queryErr:       fmt.Errorf("one-col query failed"),
	}
	defer func() { GetDB().db = origDB }()

	resources, err := pkgRevResourcesReadFromDB(t.Context(), dbPR.Key(), selector.PRRGet{PathOnly: true})

	t.Require().Nil(resources)
	t.Require().ErrorContains(err, "one-col query failed")
}

func (t *DbTestSuite) TestPkgRevResourceKeysReadFromDBReturnsScanOneTextColumnError() {
	dbPR := t.createResourcesFixture("pathonly-scanerr-ns", "pathonly-scanerr-repo", "pathonly-scanerr-package", "pathonly-scanerr-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())
	origDB := GetDB().db
	GetDB().db = &failingScanOneTextColumnSQL{
		dbSQLInterface: origDB,
		err:            errors.New("native one-col scan failed"),
	}
	defer func() { GetDB().db = origDB }()

	resources, err := pkgRevResourcesReadFromDB(t.Context(), dbPR.Key(), selector.PRRGet{PathOnly: true})

	t.Require().Nil(resources)
	t.Require().ErrorContains(err, "native one-col scan failed")
}

func (t *DbTestSuite) TestDBPackageRevisionGetFilteredResourcesPathOnlyReturnsEmptyValues() {
	dbPR := t.createResourcesFixture("gfr-pathonly-ns", "gfr-pathonly-repo", "gfr-pathonly-package", "gfr-pathonly-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())

	got, err := dbPR.GetFilteredResources(t.Context(), selector.PRRGet{PathOnly: true})

	t.Require().NoError(err)
	t.Require().NotNil(got)
	t.ElementsMatch([]string{helloResourceFile, goodbyeResourceFile}, mapKeys(got.Spec.Resources))
	for _, v := range got.Spec.Resources {
		t.Equal("", v)
	}
	t.assertPackageRevisionResourcesIdentity(got, dbPR)
}

func (t *DbTestSuite) TestPkgRevResourcesDbQueryReturnsAllFiles() {
	dbPR := t.createResourcesFixture("dbquery-ns", "dbquery-repo", "dbquery-package", "dbquery-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())

	rows, err := pkgRevResourcesDbQuery(t.Context(), dbPR.Key(), selector.AllFiles)

	t.Require().NoError(err)
	defer rows.Close()
	t.Equal(map[string]string{"Hello.txt": "Hello", "Goodbye.txt": "Goodbye"}, scanResourceRows(t, rows))
}

func (t *DbTestSuite) TestPkgRevResourcesDbQueryReturnsFilteredFiles() {
	dbPR := t.createResourcesFixture("dbqueryf-ns", "dbqueryf-repo", "dbqueryf-package", "dbqueryf-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())

	rows, err := pkgRevResourcesDbQuery(t.Context(), dbPR.Key(), selector.PRRGet{FilePaths: []string{"Goodbye.txt"}})

	t.Require().NoError(err)
	defer rows.Close()
	t.Equal(map[string]string{"Goodbye.txt": "Goodbye"}, scanResourceRows(t, rows))
}

func (t *DbTestSuite) createResourcesFixture(namespace, repoName, packageName, workspaceName string) dbPackageRevision {
	t.T().Helper()
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(&dbRepository{})

	dbRepo := t.createTestRepo(namespace, repoName)
	dbPkg := t.createTestPkg(dbRepo.Key(), packageName)
	dbPkg.repo = dbRepo
	dbPR := t.createTestPR(dbPkg.Key(), workspaceName)
	dbPR.repo = dbRepo
	return dbPR
}

func scanResourceRows(t *DbTestSuite, rows *sql.Rows) map[string]string {
	t.T().Helper()
	resources := map[string]string{}
	for rows.Next() {
		var resKey, resVal string
		t.Require().NoError(rows.Scan(&resKey, &resVal))
		resources[resKey] = resVal
	}
	t.Require().NoError(rows.Err())
	return resources
}

type forceStdlibResourceQuerySQL struct {
	dbSQLInterface
}

func (f *forceStdlibResourceQuerySQL) ScanTwoTextColumns(context.Context, string, []any, func(col1, col2 string) error) error {
	return ErrPgxQueryUnsupported
}

type failingStdlibResourceQuerySQL struct {
	dbSQLInterface
	queryErr error
}

func (f *failingStdlibResourceQuerySQL) ScanTwoTextColumns(context.Context, string, []any, func(col1, col2 string) error) error {
	return ErrPgxQueryUnsupported
}

func (f *failingStdlibResourceQuerySQL) Query(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, f.queryErr
}

type oneColumnResourceQuerySQL struct {
	dbSQLInterface
}

func (f *oneColumnResourceQuerySQL) ScanTwoTextColumns(context.Context, string, []any, func(col1, col2 string) error) error {
	return ErrPgxQueryUnsupported
}

func (f *oneColumnResourceQuerySQL) Query(ctx context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return f.dbSQLInterface.Query(ctx, `SELECT 'only-one-column'`)
}

type failingScanTwoTextColumnsSQL struct {
	dbSQLInterface
	err error
}

func (f *failingScanTwoTextColumnsSQL) ScanTwoTextColumns(context.Context, string, []any, func(col1, col2 string) error) error {
	return f.err
}

// --- PathOnly fakes ---

type forceStdlibOneColumnQuerySQL struct {
	dbSQLInterface
}

func (f *forceStdlibOneColumnQuerySQL) ScanOneTextColumn(context.Context, string, []any, func(col1 string) error) error {
	return ErrPgxQueryUnsupported
}

type failingStdlibOneColumnQuerySQL struct {
	dbSQLInterface
	queryErr error
}

func (f *failingStdlibOneColumnQuerySQL) ScanOneTextColumn(context.Context, string, []any, func(col1 string) error) error {
	return ErrPgxQueryUnsupported
}

func (f *failingStdlibOneColumnQuerySQL) Query(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, f.queryErr
}

type failingScanOneTextColumnSQL struct {
	dbSQLInterface
	err error
}

func (f *failingScanOneTextColumnSQL) ScanOneTextColumn(context.Context, string, []any, func(col1 string) error) error {
	return f.err
}

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
