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
	"bytes"
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kptdev/porch/pkg/util/selector"
	"github.com/stretchr/testify/assert"
)

func (t *DbTestSuite) countBlobs(content string) int {
	t.T().Helper()
	var n int
	t.Require().NoError(GetDB().db.QueryRow(t.Context(),
		`SELECT count(*) FROM resource_blobs WHERE content_hash=$1`, resourceContentHash(content)).Scan(&n))
	return n
}

func (t *DbTestSuite) TestResourceBlobSweepRemovesOnlyOldUnreferencedBlobs() {
	pr := t.createResourcesFixture("dedup-sweep-ns", "dedup-sweep-repo", "dedup-sweep-pkg", "dedup-sweep-ws")
	defer t.deleteTestRepo(pr.Key().RKey())

	insert := func(content, age string) {
		_, err := GetDB().db.Exec(t.Context(),
			`INSERT INTO resource_blobs (content_hash, value, created) VALUES ($1, $2, now() - $3::interval)`,
			resourceContentHash(content), content, age)
		t.Require().NoError(err)
	}
	insert("dedup-sweep old orphan", "1 hour")
	insert("dedup-sweep young orphan", "1 minute")
	// Make a blob that is old but still referenced by backdating one that the fixture uses.
	_, err := GetDB().db.Exec(t.Context(),
		`UPDATE resource_blobs SET created = now() - interval '1 hour' WHERE content_hash=$1`, resourceContentHash(helloResourceContent))
	t.Require().NoError(err)

	removed, err := pkgRevResourceBlobsSweep(t.Context())
	t.Require().NoError(err)
	t.GreaterOrEqual(removed, int64(1))

	t.Equal(0, t.countBlobs("dedup-sweep old orphan"), "old unreferenced blob is removed")
	t.Equal(1, t.countBlobs("dedup-sweep young orphan"), "young unreferenced blob is kept for the grace period")
	t.Equal(1, t.countBlobs(helloResourceContent), "old but referenced blob is kept")

	got, err := pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
	t.Require().NoError(err)
	t.Equal(helloResourceContent, got[helloResourceFile])
}

func (t *DbTestSuite) TestResourceBlobIsRefreshedWhenAnOldBlobIsReused() {
	pr := t.createResourcesFixture("dedup-touch-ns", "dedup-touch-repo", "dedup-touch-pkg", "dedup-touch-ws")
	defer t.deleteTestRepo(pr.Key().RKey())

	const orphan = "dedup-touch old orphan"
	_, err := GetDB().db.Exec(t.Context(),
		`INSERT INTO resource_blobs (content_hash, value, created) VALUES ($1, $2, now() - interval '1 hour')`,
		resourceContentHash(orphan), orphan)
	t.Require().NoError(err)

	// A write that reuses the old orphan must refresh it, so a sweep that follows cannot remove it.
	pr.resources = map[string]string{helloResourceFile: orphan}
	t.Require().NoError(pkgRevResourcesWriteToDB(t.Context(), &pr))
	_, err = pkgRevResourceBlobsSweep(t.Context())
	t.Require().NoError(err)

	t.Equal(1, t.countBlobs(orphan))
	got, err := pkgRevResourcesReadFromDB(t.Context(), pr.Key(), selector.AllFiles)
	t.Require().NoError(err)
	t.Equal(map[string]string{helloResourceFile: orphan}, got)
}

func (t *DbTestSuite) TestResourceBlobReferrersCoverEveryContentHashTable() {
	rows, err := GetDB().db.Query(t.Context(),
		`SELECT table_name FROM information_schema.columns
			WHERE column_name = 'content_hash' AND table_schema = current_schema() AND table_name <> 'resource_blobs'`)
	t.Require().NoError(err)
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		t.Require().NoError(rows.Scan(&name))
		tables = append(tables, name)
	}
	t.Require().NoError(rows.Err())

	// A table with a content_hash column that the sweep does not know about would have its blobs deleted.
	t.ElementsMatch(resourceBlobReferrers, tables,
		"every table with a content_hash column must be listed in resourceBlobReferrers, and vice versa")
}

func (t *DbTestSuite) TestResourceBlobSweepKeepsBlobsReferencedByAnyReferrer() {
	_, err := GetDB().db.Exec(t.Context(), `CREATE TABLE sweep_test_referrer (content_hash BYTEA NOT NULL)`)
	t.Require().NoError(err)
	defer func() {
		_, _ = GetDB().db.Exec(t.Context(), `DROP TABLE IF EXISTS sweep_test_referrer`)
	}()

	const kept, dropped = "dedup-referrer kept", "dedup-referrer dropped"
	for _, content := range []string{kept, dropped} {
		_, err := GetDB().db.Exec(t.Context(),
			`INSERT INTO resource_blobs (content_hash, value, created) VALUES ($1, $2, now() - interval '1 hour')`,
			resourceContentHash(content), content)
		t.Require().NoError(err)
	}
	_, err = GetDB().db.Exec(t.Context(), `INSERT INTO sweep_test_referrer (content_hash) VALUES ($1)`, resourceContentHash(kept))
	t.Require().NoError(err)

	saved := resourceBlobReferrers
	resourceBlobReferrers = append(append([]string{}, saved...), "sweep_test_referrer")
	defer func() { resourceBlobReferrers = saved }()

	_, err = pkgRevResourceBlobsSweep(t.Context())
	t.Require().NoError(err)

	t.Equal(1, t.countBlobs(kept), "blob referenced only by the second table is kept")
	t.Equal(0, t.countBlobs(dropped), "blob referenced by no table is removed")
}

func TestSortedResourceBlobs(t *testing.T) {
	blobs := map[string]string{}
	for _, v := range []string{"a", "b", "c", "d", "e"} {
		blobs[string(resourceContentHash(v))] = v
	}

	hashes, values := sortedResourceBlobs(blobs)

	assert.Len(t, hashes, len(blobs))
	assert.True(t, sort.SliceIsSorted(hashes, func(i, j int) bool { return bytes.Compare(hashes[i], hashes[j]) < 0 }))
	for i, h := range hashes {
		assert.Equal(t, resourceContentHash(values[i]), h, "values line up with hashes")
	}

	emptyHashes, emptyValues := sortedResourceBlobs(nil)
	assert.Empty(t, emptyHashes)
	assert.Empty(t, emptyValues)
}

type failingExecSQL struct {
	dbSQLInterface
}

func (f *failingExecSQL) Exec(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("exec failed")
}

func (t *DbTestSuite) insertOldOrphanBlobs(prefix string, n int) {
	t.T().Helper()
	_, err := GetDB().db.Exec(t.Context(),
		`INSERT INTO resource_blobs (content_hash, value, created)
			SELECT sha256(convert_to($1 || i, 'UTF8')), $1 || i, now() - interval '1 hour' FROM generate_series(1, $2) i`,
		prefix, n)
	t.Require().NoError(err)
}

func (t *DbTestSuite) countBlobsWithPrefix(prefix string) int {
	t.T().Helper()
	var n int
	t.Require().NoError(GetDB().db.QueryRow(t.Context(),
		`SELECT count(*) FROM resource_blobs WHERE value LIKE $1 || '%'`, prefix).Scan(&n))
	return n
}

func (t *DbTestSuite) TestResourceBlobSweepDeletesInBatchesUntilDone() {
	const prefix = "dedup-batch orphan "
	t.insertOldOrphanBlobs(prefix, resourceBlobSweepBatch+5)

	removed, err := pkgRevResourceBlobsSweep(t.Context())
	t.Require().NoError(err)

	t.GreaterOrEqual(removed, int64(resourceBlobSweepBatch+5), "the sweep keeps going after a full batch")
	t.Equal(0, t.countBlobsWithPrefix(prefix))
}

func (t *DbTestSuite) TestResourceBlobSweepReturnsExecError() {
	origDB := GetDB().db
	GetDB().db = &failingExecSQL{dbSQLInterface: origDB}
	defer func() { GetDB().db = origDB }()

	removed, err := pkgRevResourceBlobsSweep(t.Context())
	t.Require().ErrorContains(err, "exec failed")
	t.Zero(removed)
}

func (t *DbTestSuite) TestResourceBlobSweeperRunsUntilCancelled() {
	const prefix = "dedup-runner orphan "
	t.insertOldOrphanBlobs(prefix, 3)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		runResourceBlobSweeper(ctx, 10*time.Millisecond)
		close(done)
	}()

	t.Eventually(func() bool { return t.countBlobsWithPrefix(prefix) == 0 }, 5*time.Second, 20*time.Millisecond,
		"the sweeper removes old orphans on its own")

	// A failing sweep is logged and the sweeper keeps running.
	origDB := GetDB().db
	GetDB().db = &failingExecSQL{dbSQLInterface: origDB}
	time.Sleep(50 * time.Millisecond)
	GetDB().db = origDB

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fail("sweeper did not stop after its context was cancelled")
	}
}

func TestResourceBlobSweepTimingsFromEnv(t *testing.T) {
	env := func(vars map[string]string) func(string) (string, bool) {
		return func(name string) (string, bool) { v, ok := vars[name]; return v, ok }
	}
	defaults := resourceBlobSweepTimings{interval: defaultResourceBlobSweepInterval, grace: defaultResourceBlobSweepGrace}

	for name, tc := range map[string]struct {
		vars map[string]string
		want resourceBlobSweepTimings
	}{
		"unset uses defaults": {vars: nil, want: defaults},
		"empty uses defaults": {vars: map[string]string{envResourceBlobSweepInterval: "", envResourceBlobSweepGrace: ""}, want: defaults},
		"valid overrides": {
			vars: map[string]string{envResourceBlobSweepInterval: "5s", envResourceBlobSweepGrace: "20s"},
			want: resourceBlobSweepTimings{interval: 5 * time.Second, grace: 20 * time.Second},
		},
		"malformed falls back per value": {
			vars: map[string]string{envResourceBlobSweepInterval: "soon", envResourceBlobSweepGrace: "20s"},
			want: resourceBlobSweepTimings{interval: defaultResourceBlobSweepInterval, grace: 20 * time.Second},
		},
		"too small grace cannot weaken the safety margin": {
			vars: map[string]string{envResourceBlobSweepGrace: "1s"},
			want: defaults,
		},
		"too small interval falls back": {
			vars: map[string]string{envResourceBlobSweepInterval: "10ms"},
			want: defaults,
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, resourceBlobSweepTimingsFromEnv(env(tc.vars)))
		})
	}
}

func TestResourceBlobTouchAfterLeavesWritersHalfTheGrace(t *testing.T) {
	timings := resourceBlobSweepTimings{interval: time.Minute, grace: 20 * time.Second}
	assert.Equal(t, 10*time.Second, timings.touchAfter())
	assert.Less(t, timings.touchAfter(), timings.grace)
}

// The 1.6.0-1.6.4 migration hashes existing content in SQL, and the write path hashes new content in Go. If the two
// disagreed, migrated content would be stored under keys that new writes never find, and dedup would silently stop.
func (t *DbTestSuite) TestResourceContentHashMatchesSQLHash() {
	for _, value := range []string{"", "Hello", "line one\nline two\n", "unicode: ünïcödé ✓ 日本語", "trailing space "} {
		var sqlHash []byte
		t.Require().NoError(GetDB().db.QueryRow(t.Context(), `SELECT sha256(convert_to($1, 'UTF8'))`, value).Scan(&sqlHash))
		t.Equal(sqlHash, resourceContentHash(value), "value %q", value)
	}
}

type panickingExecSQL struct {
	dbSQLInterface
}

func (f *panickingExecSQL) Exec(context.Context, string, ...any) (sql.Result, error) {
	panic("sweep bug")
}

type blockingExecSQL struct {
	dbSQLInterface
}

func (f *blockingExecSQL) Exec(ctx context.Context, _ string, _ ...any) (sql.Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (t *DbTestSuite) TestResourceBlobSweepSkipsWhenDatabaseIsClosed() {
	saved := dbHandler
	dbHandler = nil
	defer func() { dbHandler = saved }()

	_, err := pkgRevResourceBlobsSweep(t.Context())
	t.Require().ErrorIs(err, errResourceBlobSweepDBClosed)
	t.NotPanics(func() { sweepResourceBlobsOnce(t.Context(), time.Second) })
}

func (t *DbTestSuite) TestResourceBlobSweepRoundSurvivesPanic() {
	origDB := GetDB().db
	GetDB().db = &panickingExecSQL{dbSQLInterface: origDB}
	defer func() { GetDB().db = origDB }()

	t.NotPanics(func() { sweepResourceBlobsOnce(t.Context(), time.Second) })
}

func (t *DbTestSuite) TestResourceBlobSweepRoundIsBoundedByTimeout() {
	origDB := GetDB().db
	GetDB().db = &blockingExecSQL{dbSQLInterface: origDB}
	defer func() { GetDB().db = origDB }()

	start := time.Now()
	sweepResourceBlobsOnce(t.Context(), 100*time.Millisecond)
	t.Less(time.Since(start), 5*time.Second, "a hung sweep statement is cancelled at the round's deadline")
}

func TestResourceBlobChunks(t *testing.T) {
	big := strings.Repeat("x", resourceInsertMaxValueBytes/2+1)

	for name, tc := range map[string]struct {
		values []string
		want   [][2]int
	}{
		"empty":            {values: nil, want: nil},
		"one small":        {values: []string{"a"}, want: [][2]int{{0, 1}}},
		"all fit":          {values: []string{"a", "b", "c"}, want: [][2]int{{0, 3}}},
		"split by bytes":   {values: []string{big, big, "a"}, want: [][2]int{{0, 1}, {1, 3}}},
		"oversize alone":   {values: []string{"a", strings.Repeat("y", resourceInsertMaxValueBytes+1), "b"}, want: [][2]int{{0, 1}, {1, 2}, {2, 3}}},
		"oversize first":   {values: []string{strings.Repeat("y", resourceInsertMaxValueBytes+1), "b"}, want: [][2]int{{0, 1}, {1, 2}}},
		"split by rows":    {values: make([]string, resourceInsertMaxRows+1), want: [][2]int{{0, resourceInsertMaxRows}, {resourceInsertMaxRows, resourceInsertMaxRows + 1}}},
		"exactly one full": {values: make([]string, resourceInsertMaxRows), want: [][2]int{{0, resourceInsertMaxRows}}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, resourceBlobChunks(tc.values))
		})
	}
}
