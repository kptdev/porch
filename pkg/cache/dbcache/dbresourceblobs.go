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

// Content storage for package revision files. resource_blobs holds each distinct file content once, keyed by its
// SHA-256. Tables listed in resourceBlobReferrers map files to that content; this file owns writing blobs and the
// background sweep that removes blobs nothing references.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kptdev/porch/api/porch/key"
	"k8s.io/klog/v2"
)

const (
	// Environment variables that override the sweep timings, as Go durations (for example "30s"). They exist so
	// tests can see a sweep happen without waiting; production should keep the defaults.
	envResourceBlobSweepInterval = "PORCH_DB_BLOB_SWEEP_INTERVAL"
	envResourceBlobSweepGrace    = "PORCH_DB_BLOB_SWEEP_GRACE"

	defaultResourceBlobSweepInterval = 10 * time.Minute
	defaultResourceBlobSweepGrace    = 10 * time.Minute

	// minResourceBlobSweepGrace keeps an override from leaving writers too little time; see touchAfter.
	minResourceBlobSweepGrace    = 10 * time.Second
	minResourceBlobSweepInterval = time.Second

	// resourceBlobSweepBatch bounds how many blobs one sweep statement deletes, to keep locks short.
	resourceBlobSweepBatch = 1000

	// resourceInsertMaxRows and resourceInsertMaxValueBytes bound one bulk insert statement, so a package with
	// thousands of files or very large files is written as several bounded statements instead of one huge one.
	resourceInsertMaxRows       = 1000
	resourceInsertMaxValueBytes = 4 * 1024 * 1024
)

// resourceBlobSweepTimings controls when unreferenced blobs are removed.
type resourceBlobSweepTimings struct {
	// interval is how often each Porch process sweeps unreferenced blobs.
	interval time.Duration
	// grace is the minimum age of an unreferenced blob before the sweep removes it.
	grace time.Duration
}

// touchAfter is how old an existing blob must be before a writer that reuses it refreshes its created timestamp.
// Refreshing keeps the sweep from deleting a blob between a writer finding it and committing the mapping row that
// references it. The sweep is only safe if touchAfter plus the longest write transaction is less than grace, so
// touchAfter is derived as half the grace: a write transaction may then take up to grace/2. Blobs younger than
// touchAfter are left alone, so heavily shared blobs are not updated on every write.
func (t resourceBlobSweepTimings) touchAfter() time.Duration {
	return t.grace / 2
}

// maxWriteDuration bounds a resource write transaction. The sweep is safe while a write transaction lasts less than
// grace minus touchAfter (grace/2); half of that leaves a margin for the commit itself.
func (t resourceBlobSweepTimings) maxWriteDuration() time.Duration {
	return t.grace / 4
}

// resourceBlobTimings is set once when the DB cache starts, before any writes or sweeps.
var resourceBlobTimings = resourceBlobSweepTimings{
	interval: defaultResourceBlobSweepInterval,
	grace:    defaultResourceBlobSweepGrace,
}

// resourceBlobSweepTimingsFromEnv reads the sweep timing overrides. A missing, malformed or too small value is
// logged and replaced by its default, so a bad setting can never shorten the grace below the safe minimum.
func resourceBlobSweepTimingsFromEnv(lookup func(string) (string, bool)) resourceBlobSweepTimings {
	return resourceBlobSweepTimings{
		interval: durationFromEnv(lookup, envResourceBlobSweepInterval, defaultResourceBlobSweepInterval, minResourceBlobSweepInterval),
		grace:    durationFromEnv(lookup, envResourceBlobSweepGrace, defaultResourceBlobSweepGrace, minResourceBlobSweepGrace),
	}
}

func durationFromEnv(lookup func(string) (string, bool), name string, def, minimum time.Duration) time.Duration {
	raw, found := lookup(name)
	if !found || raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < minimum {
		klog.Warningf("Invalid %s value %q (must be a duration of at least %v), using default %v", name, raw, minimum, def)
		return def
	}
	klog.Infof("%s set to %v", name, d)
	return d
}

// resourceBlobJoinOn joins a resources table aliased r to the content of each file, exposed as b.value. Prefix it
// with JOIN or LEFT JOIN.
const resourceBlobJoinOn = "resource_blobs b ON b.content_hash = r.content_hash"

// ErrResourceContentMissing means a file of a package revision points at content that is not stored. Reads report
// it instead of silently leaving the file out.
var ErrResourceContentMissing = errors.New("package revision file content is missing")

func resourceContentMissingError(prk key.PackageRevisionKey, resourceKey string) error {
	return fmt.Errorf("%w: file %q of package revision %+v", ErrResourceContentMissing, resourceKey, prk)
}

// resourceContentHash returns the SHA-256 of a file's content. It matches sha256(convert_to(value, 'UTF8')) in SQL,
// which the 1.6.0-1.6.4 migration uses to hash existing rows.
func resourceContentHash(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

// sortedResourceBlobs flattens blobs (keyed by string(hash)) into hash-ordered slices that line up index for index.
// Blobs are always inserted in this order so concurrent writers lock rows in the same order and cannot deadlock.
func sortedResourceBlobs(blobs map[string]string) ([][]byte, []string) {
	hashes := make([][]byte, 0, len(blobs))
	for h := range blobs {
		hashes = append(hashes, []byte(h))
	}
	sort.Slice(hashes, func(i, j int) bool { return bytes.Compare(hashes[i], hashes[j]) < 0 })
	values := make([]string, len(hashes))
	for i, h := range hashes {
		values[i] = blobs[string(h)]
	}
	return hashes, values
}

// resourceBlobChunks splits blobs, already in hash order, into runs of at most resourceInsertMaxRows blobs and about
// resourceInsertMaxValueBytes of content. A blob larger than the byte bound gets a run of its own. Each run is
// returned as a [start, end) range into values.
func resourceBlobChunks(values []string) [][2]int {
	var chunks [][2]int
	start, chunkBytes := 0, 0
	for i, v := range values {
		if i > start && (i-start >= resourceInsertMaxRows || chunkBytes+len(v) > resourceInsertMaxValueBytes) {
			chunks = append(chunks, [2]int{start, i})
			start, chunkBytes = i, 0
		}
		chunkBytes += len(v)
	}
	if start < len(values) {
		chunks = append(chunks, [2]int{start, len(values)})
	}
	return chunks
}

// upsertResourceBlobsInTx stores contents, given keyed by string(hash). A blob that already exists is left alone,
// unless it is old enough that the sweep could remove it, in which case its created time is refreshed. The blobs
// are inserted in hash order across all chunks, so concurrent writers still lock rows in the same order.
func upsertResourceBlobsInTx(ctx context.Context, tx *sql.Tx, blobs map[string]string) error {
	if len(blobs) == 0 {
		return nil
	}
	hashes, values := sortedResourceBlobs(blobs)
	for _, chunk := range resourceBlobChunks(values) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO resource_blobs (content_hash, value)
				SELECT h, v FROM unnest($1::bytea[], $2::text[]) AS t(h, v)
				ON CONFLICT (content_hash) DO UPDATE SET created = now()
				WHERE resource_blobs.created < now() - make_interval(secs => $3::double precision)`,
			hashes[chunk[0]:chunk[1]], values[chunk[0]:chunk[1]], resourceBlobTimings.touchAfter().Seconds()); err != nil {
			return err
		}
	}
	return nil
}

// resourceBlobReferrers lists every table that has a content_hash column pointing into resource_blobs.
// The sweep treats a blob as in use if any of these tables references it, so a table that stores content_hash
// and is missing from this list would lose its content to the sweep. Add a table here in the same change that
// gives it a content_hash column; TestResourceBlobReferrersCoverEveryContentHashTable fails if the two disagree.
var resourceBlobReferrers = []string{"resources"}

// resourceBlobSweepSQL builds the sweep statement for the given referencing tables. Table names come from
// resourceBlobReferrers, never from input.
func resourceBlobSweepSQL(referrers []string) string {
	var notReferenced strings.Builder
	for _, table := range referrers {
		fmt.Fprintf(&notReferenced, "\n\t\t\t\t\tAND NOT EXISTS (SELECT 1 FROM %s r WHERE r.content_hash = b.content_hash)", table)
	}
	// The age check is repeated in the outer WHERE on purpose: if a writer refreshes a blob after the inner
	// select picked it, PostgreSQL re-evaluates the outer condition on the updated row and skips the delete.
	return fmt.Sprintf(`DELETE FROM resource_blobs
				WHERE created < now() - make_interval(secs => $1::double precision)
				AND content_hash IN (
					SELECT b.content_hash FROM resource_blobs b
					WHERE b.created < now() - make_interval(secs => $1::double precision)%s
					LIMIT $2)`, notReferenced.String())
}

// errResourceBlobSweepDBClosed is returned when the sweep runs while the database is closed.
var errResourceBlobSweepDBClosed = errors.New("database is not open")

// pkgRevResourceBlobsSweep deletes blobs that no referencing table uses and that are older than
// the sweep grace. Deleting package revisions removes their resources rows through the foreign key
// cascade but leaves the blobs, so this is what actually frees the storage. It is safe to run from several
// Porch processes at once.
//
// Each batch is one autocommit DELETE, so a sweep that is cancelled or whose process is killed loses nothing:
// PostgreSQL rolls back the batch in flight, earlier batches only removed blobs nothing used, and whatever is
// left is removed by the next sweep.
func pkgRevResourceBlobsSweep(ctx context.Context) (int64, error) {
	_, span := tracer.Start(ctx, "dbresourceblobs::pkgRevResourceBlobsSweep")
	defer span.End()

	handler := GetDB()
	if handler == nil {
		return 0, errResourceBlobSweepDBClosed
	}

	query := resourceBlobSweepSQL(resourceBlobReferrers)
	var total int64
	for {
		res, err := handler.db.Exec(ctx, query, resourceBlobTimings.grace.Seconds(), resourceBlobSweepBatch)
		if err != nil {
			return total, fmt.Errorf("pkgRevResourceBlobsSweep: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("pkgRevResourceBlobsSweep: rows affected: %w", err)
		}
		total += n
		if n < resourceBlobSweepBatch || ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}

// runResourceBlobSweeper sweeps unreferenced blobs every interval until ctx is cancelled. A failing, hanging or
// panicking sweep only affects that round; the sweeper keeps running so storage is still freed later.
func runResourceBlobSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepResourceBlobsOnce(ctx, interval)
		}
	}
}

// sweepResourceBlobsOnce runs one sweep round, bounded by timeout so a statement stuck on a lock is cancelled
// rather than stalling every later round.
func sweepResourceBlobsOnce(ctx context.Context, timeout time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			klog.Errorf("resource blob sweep panicked, will retry next interval: %v", r)
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	n, err := pkgRevResourceBlobsSweep(ctx)
	switch {
	case errors.Is(err, errResourceBlobSweepDBClosed):
		klog.V(3).Info("resource blob sweep skipped: database is not open")
	case err != nil:
		klog.Warningf("resource blob sweep failed after removing %d blobs: %v", n, err)
	case n > 0:
		klog.V(3).Infof("resource blob sweep removed %d unreferenced blobs", n)
	}
}
