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
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/kptdev/porch/api/porch/key"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/util/selector"
	mockcachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	"github.com/stretchr/testify/assert"
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

func (t *DbTestSuite) TestPkgRevResourcesWriteToDBRoundTrip() {
	dbPR := t.createResourcesFixture("write-ns", "write-repo", "write-package", "write-pr")
	defer t.deleteTestRepo(dbPR.Key().RKey())

	dbPR.resources = map[string]string{
		"a.yaml": "aaa",
		"b.yaml": "bbb",
	}

	err := pkgRevResourcesWriteToDB(t.Context(), &dbPR)
	t.Require().NoError(err)

	got, err := pkgRevResourcesReadFromDB(t.Context(), dbPR.Key(), selector.AllFiles)
	t.Require().NoError(err)
	t.Equal(dbPR.resources, got)
}

// --- PathOnly unit tests ---

func (t *DbTestSuite) TestPkgRevResourcesQuerySQLPathOnlyOmitsValueColumn() {
	prk := key.PackageRevisionKey{}
	prk.PkgKey.RepoKey.Namespace = "ns"
	prk.PkgKey.RepoKey.Name = "repo"
	prk.WorkspaceName = "ws"

	query, _ := pkgRevResourcesQuerySQL(prk, selector.PRRGet{PathOnly: true})
	t.NotContains(query, "resource_blobs")
	t.Contains(query, "resource_key")
}

func (t *DbTestSuite) TestPkgRevResourcesQuerySQLFullContentsIncludesValueColumn() {
	prk := key.PackageRevisionKey{}
	prk.PkgKey.RepoKey.Namespace = "ns"
	prk.PkgKey.RepoKey.Name = "repo"
	prk.WorkspaceName = "ws"

	query, _ := pkgRevResourcesQuerySQL(prk, selector.AllFiles)
	t.Contains(query, "resource_blobs")
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
		var resKey, resVal, missing string
		t.Require().NoError(rows.Scan(&resKey, &resVal, &missing))
		t.Require().Equal("false", missing, "content of %s", resKey)
		resources[resKey] = resVal
	}
	t.Require().NoError(rows.Err())
	return resources
}

type forceStdlibResourceQuerySQL struct {
	dbSQLInterface
}

func (f *forceStdlibResourceQuerySQL) ScanTextColumns(context.Context, string, []any, int, func(cols []string) error) error {
	return ErrPgxQueryUnsupported
}

type failingStdlibResourceQuerySQL struct {
	dbSQLInterface
	queryErr error
}

func (f *failingStdlibResourceQuerySQL) ScanTextColumns(context.Context, string, []any, int, func(cols []string) error) error {
	return ErrPgxQueryUnsupported
}

func (f *failingStdlibResourceQuerySQL) Query(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, f.queryErr
}

type oneColumnResourceQuerySQL struct {
	dbSQLInterface
}

func (f *oneColumnResourceQuerySQL) ScanTextColumns(context.Context, string, []any, int, func(cols []string) error) error {
	return ErrPgxQueryUnsupported
}

func (f *oneColumnResourceQuerySQL) Query(ctx context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return f.dbSQLInterface.Query(ctx, `SELECT 'only-one-column'`)
}

type failingScanTwoTextColumnsSQL struct {
	dbSQLInterface
	err error
}

func (f *failingScanTwoTextColumnsSQL) ScanTextColumns(context.Context, string, []any, int, func(cols []string) error) error {
	return f.err
}

// --- PathOnly fakes ---

type forceStdlibOneColumnQuerySQL struct {
	dbSQLInterface
}

func (f *forceStdlibOneColumnQuerySQL) ScanTextColumns(context.Context, string, []any, int, func(cols []string) error) error {
	return ErrPgxQueryUnsupported
}

type failingStdlibOneColumnQuerySQL struct {
	dbSQLInterface
	queryErr error
}

func (f *failingStdlibOneColumnQuerySQL) ScanTextColumns(context.Context, string, []any, int, func(cols []string) error) error {
	return ErrPgxQueryUnsupported
}

func (f *failingStdlibOneColumnQuerySQL) Query(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, f.queryErr
}

type failingScanOneTextColumnSQL struct {
	dbSQLInterface
	err error
}

func (f *failingScanOneTextColumnSQL) ScanTextColumns(context.Context, string, []any, int, func(cols []string) error) error {
	return f.err
}

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// --- Content deduplication tests ---

func (t *DbTestSuite) countMappings(content string) int {
	t.T().Helper()
	var n int
	t.Require().NoError(GetDB().db.QueryRow(t.Context(),
		`SELECT count(*) FROM resources WHERE content_hash=$1`, resourceContentHash(content)).Scan(&n))
	return n
}

func (t *DbTestSuite) TestResourcesIdenticalContentIsStoredOnceAcrossRevisions() {
	first := t.createResourcesFixture("dedup-share-ns", "dedup-share-repo-a", "dedup-share-pkg", "dedup-share-ws-a")
	defer t.deleteTestRepo(first.Key().RKey())
	second := t.createResourcesFixture("dedup-share-ns", "dedup-share-repo-b", "dedup-share-pkg", "dedup-share-ws-b")
	defer t.deleteTestRepo(second.Key().RKey())

	// Both fixtures hold the same two files. Each package revision maps them, but the content is stored once.
	t.Equal(1, t.countBlobs(helloResourceContent))
	t.Equal(1, t.countBlobs(goodbyeResourceContent))
	t.GreaterOrEqual(t.countMappings(helloResourceContent), 2)

	for _, pr := range []dbPackageRevision{first, second} {
		got, err := pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
		t.Require().NoError(err)
		t.Equal(map[string]string{helloResourceFile: helloResourceContent, goodbyeResourceFile: goodbyeResourceContent}, got)
	}
}

func (t *DbTestSuite) TestResourcesWriteUpdatesAddsAndRemovesFiles() {
	pr := t.createResourcesFixture("dedup-diff-ns", "dedup-diff-repo", "dedup-diff-pkg", "dedup-diff-ws")
	defer t.deleteTestRepo(pr.Key().RKey())

	// Keep Hello.txt, change Goodbye.txt, drop nothing yet, add a new file.
	pr.resources = map[string]string{
		helloResourceFile:   helloResourceContent,
		goodbyeResourceFile: "dedup-diff changed goodbye",
		"added.yaml":        "dedup-diff added",
	}
	t.Require().NoError(pkgRevResourcesWriteToDB(t.Context(), &pr))
	got, err := pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
	t.Require().NoError(err)
	t.Equal(pr.resources, got)

	// Remove a file and make two files share one content. Only one blob is needed for the shared content.
	pr.resources = map[string]string{
		helloResourceFile: "dedup-diff shared",
		"copy.yaml":       "dedup-diff shared",
	}
	t.Require().NoError(pkgRevResourcesWriteToDB(t.Context(), &pr))
	got, err = pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
	t.Require().NoError(err)
	t.Equal(pr.resources, got)
	t.Equal(1, t.countBlobs("dedup-diff shared"))
	t.Equal(2, t.countMappings("dedup-diff shared"))

	// Writing no files removes every mapping.
	pr.resources = nil
	t.Require().NoError(pkgRevResourcesWriteToDB(t.Context(), &pr))
	got, err = pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
	t.Require().NoError(err)
	t.Empty(got)
}

func (t *DbTestSuite) TestResourcesWriteRefreshesRevisionOnUnchangedFiles() {
	pr := t.createResourcesFixture("dedup-rev-ns", "dedup-rev-repo", "dedup-rev-pkg", "dedup-rev-ws")
	defer t.deleteTestRepo(pr.Key().RKey())

	// Publishing changes the revision number while the files stay the same.
	pr.pkgRevKey.Revision += 100
	t.Require().NoError(pkgRevResourcesWriteToDB(t.Context(), &pr))

	var distinctRevisions, wrong int
	t.Require().NoError(GetDB().db.QueryRow(t.Context(),
		`SELECT count(DISTINCT revision), count(*) FILTER (WHERE revision <> $3) FROM resources WHERE k8s_name_space=$1 AND k8s_name=$2`,
		pr.Key().K8SNS(), pr.Key().K8SName(), pr.Key().Revision).Scan(&distinctRevisions, &wrong))
	t.Equal(1, distinctRevisions)
	t.Equal(0, wrong)
}

func TestPlanResourceWrite(t *testing.T) {
	existing := map[string][]byte{
		"same.yaml":    resourceContentHash("same"),
		"changed.yaml": resourceContentHash("old"),
		"gone.yaml":    resourceContentHash("gone"),
	}
	desired := map[string]string{
		"same.yaml":    "same",
		"changed.yaml": "new",
		"added.yaml":   "shared",
		"copy.yaml":    "shared",
	}

	plan := planResourceWrite(existing, desired)

	assert.ElementsMatch(t, []string{"changed.yaml", "added.yaml", "copy.yaml"}, plan.changedKeys)
	assert.Equal(t, []string{"gone.yaml"}, plan.removedKeys)

	// changedHashes line up with changedKeys.
	for i, k := range plan.changedKeys {
		assert.Equal(t, resourceContentHash(desired[k]), plan.changedHashes[i], k)
	}

	// Two files with the same content need one blob; unchanged content needs none.
	assert.Equal(t, map[string]string{
		string(resourceContentHash("new")):    "new",
		string(resourceContentHash("shared")): "shared",
	}, plan.blobs)
}

func TestPlanResourceWriteNothingChanged(t *testing.T) {
	existing := map[string][]byte{"a.yaml": resourceContentHash("a")}
	plan := planResourceWrite(existing, map[string]string{"a.yaml": "a"})

	assert.Empty(t, plan.changedKeys)
	assert.Empty(t, plan.blobs)
	assert.Empty(t, plan.removedKeys)
}

type failingBeginTxSQL struct {
	dbSQLInterface
}

func (f *failingBeginTxSQL) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, errors.New("begin failed")
}

func (t *DbTestSuite) TestResourcesWriteReturnsBeginTxError() {
	pr := t.createResourcesFixture("dedup-begin-ns", "dedup-begin-repo", "dedup-begin-pkg", "dedup-begin-ws")
	defer t.deleteTestRepo(pr.Key().RKey())

	origDB := GetDB().db
	GetDB().db = &failingBeginTxSQL{dbSQLInterface: origDB}
	defer func() { GetDB().db = origDB }()

	t.Require().ErrorContains(pkgRevResourcesWriteToDB(t.Context(), &pr), "begin failed")
}

func (t *DbTestSuite) TestResourcesWriteFailureLeavesStoredFilesUnchanged() {
	pr := t.createResourcesFixture("dedup-fail-ns", "dedup-fail-repo", "dedup-fail-pkg", "dedup-fail-ws")
	defer t.deleteTestRepo(pr.Key().RKey())
	want := map[string]string{helloResourceFile: helloResourceContent, goodbyeResourceFile: goodbyeResourceContent}

	for name, files := range map[string]map[string]string{
		// PostgreSQL TEXT cannot hold NUL, so the blob insert fails.
		"blob insert": {helloResourceFile: "changed\x00content"},
		// resources.resource_key must not be empty, so the mapping insert fails after the blob insert succeeded.
		"mapping insert": {"": "dedup-fail orphaned by rollback"},
	} {
		pr.resources = files
		err := pkgRevResourcesWriteToDB(t.Context(), &pr)
		t.Require().ErrorContains(err, name, name)

		// The whole write is one transaction, so a failed step leaves the previous files in place.
		got, err := pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
		t.Require().NoError(err, name)
		t.Equal(want, got, name)
	}
	t.Equal(0, t.countBlobs("dedup-fail orphaned by rollback"), "blob insert is rolled back with the failed mapping insert")
}

// breakResourceContent points one file of a package revision at content that is not stored.
func (t *DbTestSuite) breakResourceContent(pr dbPackageRevision, resourceKey string) {
	t.T().Helper()
	res, err := GetDB().db.Exec(t.Context(),
		`UPDATE resources SET content_hash = $4 WHERE k8s_name_space=$1 AND k8s_name=$2 AND resource_key=$3`,
		pr.Key().K8SNS(), pr.Key().K8SName(), resourceKey, resourceContentHash("dedup content that was never stored"))
	t.Require().NoError(err)
	n, err := res.RowsAffected()
	t.Require().NoError(err)
	t.Require().Equal(int64(1), n)
}

func (t *DbTestSuite) TestResourcesReadReportsMissingContent() {
	pr := t.createResourcesFixture("dedup-missing-ns", "dedup-missing-repo", "dedup-missing-pkg", "dedup-missing-ws")
	defer t.deleteTestRepo(pr.Key().RKey())
	t.breakResourceContent(pr, helloResourceFile)

	_, err := pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
	t.Require().ErrorIs(err, ErrResourceContentMissing, "native read of all files")
	t.ErrorContains(err, helloResourceFile)

	_, err = pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.PRRGet{FilePaths: []string{helloResourceFile}})
	t.Require().ErrorIs(err, ErrResourceContentMissing, "native read of the broken file")

	_, _, err = pkgRevResourceReadFromDB(t.Context(), pr.Key(), helloResourceFile)
	t.Require().ErrorIs(err, ErrResourceContentMissing, "single-file read")

	origDB := GetDB().db
	GetDB().db = &forceStdlibResourceQuerySQL{dbSQLInterface: origDB}
	_, err = pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
	GetDB().db = origDB
	t.Require().ErrorIs(err, ErrResourceContentMissing, "stdlib fallback read")

	// Files whose content is stored, and path-only reads, are unaffected.
	got, err := pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.PRRGet{FilePaths: []string{goodbyeResourceFile}})
	t.Require().NoError(err)
	t.Equal(map[string]string{goodbyeResourceFile: goodbyeResourceContent}, got)
	keys, err := pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.PRRGet{PathOnly: true})
	t.Require().NoError(err)
	t.ElementsMatch([]string{helloResourceFile, goodbyeResourceFile}, mapKeys(keys))
}

func (t *DbTestSuite) TestResourceWriteToDBWritesOneFileAndLeavesTheRest() {
	pr := t.createResourcesFixture("dedup-single-ns", "dedup-single-repo", "dedup-single-pkg", "dedup-single-ws")
	defer t.deleteTestRepo(pr.Key().RKey())

	t.Require().NoError(pkgRevResourceWriteToDB(t.Context(), pr.Key(), helloResourceFile, "dedup-single changed"))
	t.Require().NoError(pkgRevResourceWriteToDB(t.Context(), pr.Key(), "new.yaml", "dedup-single new"))

	got, err := pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
	t.Require().NoError(err)
	t.Equal(map[string]string{
		helloResourceFile:   "dedup-single changed",
		goodbyeResourceFile: goodbyeResourceContent,
		"new.yaml":          "dedup-single new",
	}, got)

	_, val, err := pkgRevResourceReadFromDB(t.Context(), pr.Key(), helloResourceFile)
	t.Require().NoError(err)
	t.Equal("dedup-single changed", val)
}

func (t *DbTestSuite) TestResourceWriteToDBReturnsBeginTxError() {
	pr := t.createResourcesFixture("dedup-singlebegin-ns", "dedup-singlebegin-repo", "dedup-singlebegin-pkg", "dedup-singlebegin-ws")
	defer t.deleteTestRepo(pr.Key().RKey())

	origDB := GetDB().db
	GetDB().db = &failingBeginTxSQL{dbSQLInterface: origDB}
	defer func() { GetDB().db = origDB }()

	t.Require().ErrorContains(pkgRevResourceWriteToDB(t.Context(), pr.Key(), helloResourceFile, "x"), "begin failed")
}

// A package with more files, and more content, than one bulk insert statement allows is written as several
// statements and reads back whole.
func (t *DbTestSuite) TestResourcesWriteSplitsLargePackagesIntoSeveralStatements() {
	pr := t.createResourcesFixture("dedup-large-ns", "dedup-large-repo", "dedup-large-pkg", "dedup-large-ws")
	defer t.deleteTestRepo(pr.Key().RKey())

	files := make(map[string]string)
	filler := strings.Repeat("z", 5000) // 1,300 files of ~5 KB: over both the row and the 4 MiB limits
	for i := 0; i < resourceInsertMaxRows+300; i++ {
		files[fmt.Sprintf("dir/file-%04d.yaml", i)] = fmt.Sprintf("file %d\n%s", i, filler)
	}
	pr.resources = files

	t.Require().NoError(pkgRevResourcesWriteToDB(t.Context(), &pr))
	got, err := pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
	t.Require().NoError(err)
	t.Len(got, len(files))
	t.True(maps.Equal(files, got))

	// A second write of the same files changes nothing and a one-file change touches one file.
	files["dir/file-0000.yaml"] = "changed"
	pr.resources = files
	t.Require().NoError(pkgRevResourcesWriteToDB(t.Context(), &pr))
	got, err = pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
	t.Require().NoError(err)
	t.True(maps.Equal(files, got))
}

// Two full writes to the same package revision must not merge: the result has to be one of the submitted file
// sets. Without serialization each writer diffs against the same stored hashes and changes a different file, so
// the stored set would be a mix that nobody wrote.
func (t *DbTestSuite) TestResourcesConcurrentWritesDoNotMerge() {
	pr := t.createResourcesFixture("dedup-race-ns", "dedup-race-repo", "dedup-race-pkg", "dedup-race-ws")
	defer t.deleteTestRepo(pr.Key().RKey())

	base := map[string]string{"a.yaml": "a old", "b.yaml": "b old"}
	writeA := map[string]string{"a.yaml": "a from A", "b.yaml": "b old"}
	writeB := map[string]string{"a.yaml": "a old", "b.yaml": "b from B"}

	for round := 0; round < 25; round++ {
		pr.resources = base
		t.Require().NoError(pkgRevResourcesWriteToDB(t.Context(), &pr))

		start := make(chan struct{})
		errs := make(chan error, 2)
		for _, files := range []map[string]string{writeA, writeB} {
			writer := pr
			writer.resources = files
			go func() {
				<-start
				errs <- pkgRevResourcesWriteToDB(t.Context(), &writer)
			}()
		}
		close(start)
		t.Require().NoError(<-errs)
		t.Require().NoError(<-errs)

		got, err := pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
		t.Require().NoError(err)
		t.Require().Truef(maps.Equal(got, writeA) || maps.Equal(got, writeB),
			"round %d: stored files %v are neither write A nor write B", round, got)
	}
}

// A write that cannot finish in time, for example because another write holds the package revision lock, is
// cancelled instead of running past the window in which the sweep is safe.
func (t *DbTestSuite) TestResourcesWriteIsBoundedInTime() {
	pr := t.createResourcesFixture("dedup-bound-ns", "dedup-bound-repo", "dedup-bound-pkg", "dedup-bound-ws")
	defer t.deleteTestRepo(pr.Key().RKey())

	saved := resourceBlobTimings
	resourceBlobTimings = resourceBlobSweepTimings{interval: time.Minute, grace: 400 * time.Millisecond}
	defer func() { resourceBlobTimings = saved }()

	holder, err := GetDB().db.BeginTx(t.Context(), nil)
	t.Require().NoError(err)
	defer holder.Rollback() //nolint:errcheck
	t.Require().NoError(lockPackageRevisionForResourceWriteInTx(t.Context(), holder, pr.Key()))

	pr.resources = map[string]string{helloResourceFile: "dedup-bound changed"}
	start := time.Now()
	err = pkgRevResourcesWriteToDB(t.Context(), &pr)
	t.Require().Error(err)
	t.Less(time.Since(start), 5*time.Second, "the write gives up at its deadline")

	t.Require().NoError(holder.Rollback())
	got, err := pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
	t.Require().NoError(err)
	t.Equal(helloResourceContent, got[helloResourceFile], "the cancelled write changed nothing")
}

// The single-file write takes the same package revision lock as the full write and is bounded in time too: it also
// writes blobs, so it must not outlive the window in which the sweep is safe.
func (t *DbTestSuite) TestResourceWriteToDBWaitsForTheRevisionLockAndIsBounded() {
	pr := t.createResourcesFixture("dedup-singlelock-ns", "dedup-singlelock-repo", "dedup-singlelock-pkg", "dedup-singlelock-ws")
	defer t.deleteTestRepo(pr.Key().RKey())

	saved := resourceBlobTimings
	resourceBlobTimings = resourceBlobSweepTimings{interval: time.Minute, grace: 400 * time.Millisecond}
	defer func() { resourceBlobTimings = saved }()

	holder, err := GetDB().db.BeginTx(t.Context(), nil)
	t.Require().NoError(err)
	defer holder.Rollback() //nolint:errcheck
	t.Require().NoError(lockPackageRevisionForResourceWriteInTx(t.Context(), holder, pr.Key()))

	start := time.Now()
	err = pkgRevResourceWriteToDB(t.Context(), pr.Key(), helloResourceFile, "dedup-singlelock changed")
	t.Require().Error(err, "blocked behind the lock until its deadline")
	t.Less(time.Since(start), 5*time.Second)

	t.Require().NoError(holder.Rollback())
	_, val, err := pkgRevResourceReadFromDB(t.Context(), pr.Key(), helloResourceFile)
	t.Require().NoError(err)
	t.Equal(helloResourceContent, val, "the cancelled write changed nothing")
}
