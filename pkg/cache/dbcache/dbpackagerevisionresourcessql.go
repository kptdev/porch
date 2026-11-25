// Copyright 2024-2026 The kpt Authors
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
	"fmt"

	"github.com/kptdev/porch/api/porch/key"
	"github.com/kptdev/porch/internal/telemetry"
	"github.com/kptdev/porch/pkg/util/selector"
	"k8s.io/klog/v2"
)

func pkgRevResourceReadFromDB(ctx context.Context, prk key.PackageRevisionKey, resKey string) (string, string, error) {
	_, span := tracer.Start(ctx, "dbpackagerevisionresourcessql::pkgRevResourceReadFromDB")
	defer span.End()

	klog.V(5).Infof("pkgRevResourceReadFromDB: reading package revision resource %+v:%q", prk, resKey)

	sqlStatement := `SELECT b.value FROM resources r LEFT JOIN ` + resourceBlobJoinOn + `
		WHERE r.k8s_name_space=$1 AND r.k8s_name=$2 AND r.resource_key=$3`

	var resVal sql.NullString

	klog.V(6).Infof("pkgRevResourceReadFromDB: running query %q on package revision %+v key %q", sqlStatement, prk, resKey)
	err := GetDB().db.QueryRow(ctx, sqlStatement, prk.K8SNS(), prk.K8SName(), resKey).Scan(&resVal)

	if err != nil {
		if err == sql.ErrNoRows {
			klog.V(5).Infof("pkgRevResourceReadFromDB: package revision resource not found in db %+v:%q", prk, resKey)
		} else {
			klog.Warningf("pkgRevResourceReadFromDB: reading package revision resource %+v:%q returned err: %q", prk, resKey, err)
		}
		return resKey, "", err
	}

	if !resVal.Valid {
		err := resourceContentMissingError(prk, resKey)
		klog.Warningf("pkgRevResourceReadFromDB: %v", err)
		return resKey, "", err
	}

	klog.V(5).Infof("pkgRevResourceReadFromDB: reading package succeeded %+v:%q", prk, resKey)

	return resKey, resVal.String, nil
}

func pkgRevResourcesReadFromDB(ctx context.Context, prk key.PackageRevisionKey, selector selector.PRRGet) (map[string]string, error) {
	_, span := tracer.Start(ctx, "dbpackagerevisionresourcessql::pkgRevResourcesReadFromDB")
	defer span.End()

	klog.V(5).Infof("pkgRevResourcesReadFromDB: reading package revision resource %+v", prk)

	if selector.PathOnly {
		return pkgRevResourceKeysReadFromDB(ctx, prk, selector)
	}
	return pkgRevResourceContentsReadFromDB(ctx, prk, selector)
}

func pkgRevResourceKeysReadFromDB(ctx context.Context, prk key.PackageRevisionKey, sel selector.PRRGet) (map[string]string, error) {
	resources := make(map[string]string, len(sel.FilePaths))
	query, args := pkgRevResourcesQuerySQL(prk, sel)
	klog.V(6).Infof("pkgRevResourceKeysReadFromDB: running query %q on package revision %+v", query, prk)

	err := GetDB().db.ScanTextColumns(ctx, query, args, 1, func(cols []string) error {
		resources[cols[0]] = ""
		return nil
	})
	if err == nil {
		klog.V(5).Infof("pkgRevResourceKeysReadFromDB: query succeeded for %q", prk)
		return resources, nil
	}
	if !errors.Is(err, ErrPgxQueryUnsupported) {
		klog.Warningf("pkgRevResourceKeysReadFromDB: query failed for %+v: %q", prk, err)
		return nil, err
	}

	rows, err := pkgRevResourcesDbQuery(ctx, prk, sel)
	if err != nil {
		klog.Warningf("pkgRevResourceKeysReadFromDB: query failed for %+v: %q", prk, err)
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var resKey string
		if err := rows.Scan(&resKey); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		resources[resKey] = ""
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	klog.V(5).Infof("pkgRevResourceKeysReadFromDB: query succeeded for %q", prk)
	return resources, nil
}

func pkgRevResourceContentsReadFromDB(ctx context.Context, prk key.PackageRevisionKey, sel selector.PRRGet) (map[string]string, error) {
	var resources map[string]string
	if sel.IsAllFiles() {
		resources = make(map[string]string)
	} else {
		resources = make(map[string]string, len(sel.FilePaths))
	}
	query, args := pkgRevResourcesQuerySQL(prk, sel)
	klog.V(6).Infof("pkgRevResourceContentsReadFromDB: running query %q on package revision %+v", query, prk)

	err := GetDB().db.ScanTextColumns(ctx, query, args, 3, func(cols []string) error {
		if cols[2] == "true" {
			return resourceContentMissingError(prk, cols[0])
		}
		resources[cols[0]] = cols[1]
		return nil
	})
	if err == nil {
		klog.V(5).Infof("pkgRevResourceContentsReadFromDB: query succeeded for %q", prk)
		return resources, nil
	}
	if !errors.Is(err, ErrPgxQueryUnsupported) {
		klog.Warningf("pkgRevResourceContentsReadFromDB: query failed for %+v: %q", prk, err)
		return nil, err
	}

	rows, err := pkgRevResourcesDbQuery(ctx, prk, sel)
	if err != nil {
		klog.Warningf("pkgRevResourceContentsReadFromDB: query failed for %+v: %q", prk, err)
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var resKey, resVal, missing string
		if err := rows.Scan(&resKey, &resVal, &missing); err != nil {
			return nil, err
		}
		if missing == "true" {
			return nil, resourceContentMissingError(prk, resKey)
		}
		resources[resKey] = resVal
	}

	if err := rows.Err(); err != nil {
		klog.Warningf("pkgRevResourcesReadFromDB: row iteration failed for %+v: %q", prk, err)
		return nil, err
	}

	klog.V(5).Infof("pkgRevResourceContentsReadFromDB: query succeeded for %q", prk)
	return resources, nil
}

func pkgRevResourcesQuerySQL(prk key.PackageRevisionKey, selector selector.PRRGet) (string, []any) {
	// Path-only reads need just the keys, so they never touch the content table. Content reads use a LEFT JOIN
	// and return whether the content is missing, so a file whose content is gone is reported rather than dropped.
	from := "resources r"
	cols := "r.resource_key"
	if !selector.PathOnly {
		from = "resources r LEFT JOIN " + resourceBlobJoinOn
		cols = "r.resource_key, COALESCE(b.value, ''), (b.content_hash IS NULL)::text"
	}
	if selector.IsAllFiles() {
		return fmt.Sprintf(`SELECT %s FROM %s WHERE r.k8s_name_space=$1 AND r.k8s_name=$2`, cols, from),
			[]any{prk.K8SNS(), prk.K8SName()}
	}
	return fmt.Sprintf(`SELECT %s FROM %s WHERE r.k8s_name_space=$1 AND r.k8s_name=$2 AND r.resource_key=ANY($3)`, cols, from),
		[]any{prk.K8SNS(), prk.K8SName(), selector.FilePaths}
}

func pkgRevResourcesDbQuery(ctx context.Context, prk key.PackageRevisionKey, selector selector.PRRGet) (*sql.Rows, error) {
	sqlStatement, args := pkgRevResourcesQuerySQL(prk, selector)
	if selector.IsAllFiles() {
		klog.V(6).Infof("pkgRevResourcesReadFromDB: running query %q on package revision %+v", sqlStatement, prk)
	} else {
		klog.V(6).Infof("pkgRevResourcesReadFromDB: running query %q on package revision %+v filter the files %q", sqlStatement, prk, selector.FilePaths)
	}
	return GetDB().db.Query(ctx, sqlStatement, args...)
}

// pkgRevResourceWriteToDB writes one file of a package revision, leaving its other files alone. The content goes into
// resource_blobs and the file is mapped to it, in one transaction.
func pkgRevResourceWriteToDB(ctx context.Context, prk key.PackageRevisionKey, resKey, resVal string) error {
	_, span := tracer.Start(ctx, "dbpackagerevisionresourcessql::pkgRevResourceWriteToDB")
	defer span.End()

	klog.V(5).Infof("pkgRevResourceWriteToDB: writing package revision resource %+v=%q for %q", resKey, resVal, prk)

	hash := resourceContentHash(resVal)
	plan := resourceWritePlan{
		changedKeys:   []string{resKey},
		changedHashes: [][]byte{hash},
		blobs:         map[string]string{string(hash): resVal},
	}

	tx, err := GetDB().db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("pkgRevResourceWriteToDB: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if err := upsertResourceBlobsInTx(ctx, tx, plan.blobs); err != nil {
		klog.Warningf("pkgRevResourceWriteToDB: blob insert failed on package revision %+v: %q", prk, err)
		return fmt.Errorf("blob insert: %w", err)
	}
	if err := upsertResourceMappingsInTx(ctx, tx, prk, plan); err != nil {
		klog.Warningf("pkgRevResourceWriteToDB: mapping insert failed on package revision %+v: %q", prk, err)
		return fmt.Errorf("mapping insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	klog.V(5).Infof("pkgRevResourceWriteToDB: query succeeded, row created/updated")
	return nil
}

// resourceWritePlan is the difference between the files a package revision should have and the files stored.
type resourceWritePlan struct {
	// changedKeys and changedHashes are the files that are new or whose content changed, index for index.
	changedKeys   []string
	changedHashes [][]byte
	// blobs are the distinct contents those files need, keyed by string(hash), so a content used by several files
	// is stored once.
	blobs map[string]string
	// removedKeys are stored files that the package revision no longer has.
	removedKeys []string
}

// planResourceWrite compares the desired files with the stored content hashes. It does no I/O.
func planResourceWrite(existing map[string][]byte, desired map[string]string) resourceWritePlan {
	plan := resourceWritePlan{blobs: make(map[string]string)}
	for resourceKey, resourceValue := range desired {
		hash := resourceContentHash(resourceValue)
		if old, found := existing[resourceKey]; found && bytes.Equal(old, hash) {
			continue
		}
		plan.changedKeys = append(plan.changedKeys, resourceKey)
		plan.changedHashes = append(plan.changedHashes, hash)
		plan.blobs[string(hash)] = resourceValue
	}

	for resourceKey := range existing {
		if _, found := desired[resourceKey]; !found {
			plan.removedKeys = append(plan.removedKeys, resourceKey)
		}
	}
	return plan
}

// pkgRevResourcesWriteToDB stores the files of a package revision. Content goes into resource_blobs once per
// distinct content; resources maps each file to its content. Only files that are new or whose content changed
// are written, so rewriting a revision with few changes touches few rows.
func pkgRevResourcesWriteToDB(ctx context.Context, pr *dbPackageRevision) error {
	_, span := tracer.Start(ctx, "dbpackagerevisionresourcessql::pkgRevResourcesWriteToDB")
	defer span.End()

	prk := pr.Key()

	tx, err := GetDB().db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("pkgRevResourcesWriteToDB: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if len(pr.resources) == 0 {
		klog.Warningf("pkgRevResourcesWriteToDB: pr %+v has no resources", prk)
		if err := deleteAllResourceMappingsInTx(ctx, tx, prk); err != nil {
			klog.Warningf("pkgRevResourcesWriteToDB: delete failed for %+v: %q", prk, err)
			return err
		}
		return tx.Commit()
	}

	klog.V(5).Infof("pkgRevResourcesWriteToDB: writing %d resources for %+v", len(pr.resources), prk)

	existing, err := pkgRevResourceHashesInTx(ctx, tx, prk)
	if err != nil {
		klog.Warningf("pkgRevResourcesWriteToDB: reading existing resources failed for %+v: %q", prk, err)
		return err
	}

	plan := planResourceWrite(existing, pr.resources)
	if err := applyResourceWritePlanInTx(ctx, tx, prk, plan); err != nil {
		klog.Warningf("pkgRevResourcesWriteToDB: write failed for %+v: %q", prk, err)
		return err
	}

	klog.V(5).Infof("pkgRevResourcesWriteToDB: query succeeded, %d changed, %d removed of %d files", len(plan.changedKeys), len(plan.removedKeys), len(pr.resources))
	return tx.Commit()
}

// applyResourceWritePlanInTx runs the steps of a write plan in order. Blobs go first so a mapping never points at
// content that is not stored yet.
func applyResourceWritePlanInTx(ctx context.Context, tx *sql.Tx, prk key.PackageRevisionKey, plan resourceWritePlan) error {
	if err := upsertResourceBlobsInTx(ctx, tx, plan.blobs); err != nil {
		return fmt.Errorf("blob insert: %w", err)
	}
	if err := upsertResourceMappingsInTx(ctx, tx, prk, plan); err != nil {
		return fmt.Errorf("mapping insert: %w", err)
	}
	if err := deleteResourceMappingsInTx(ctx, tx, prk, plan.removedKeys); err != nil {
		return fmt.Errorf("delete of removed files: %w", err)
	}
	if err := refreshResourceRevisionInTx(ctx, tx, prk); err != nil {
		return fmt.Errorf("revision update: %w", err)
	}
	return nil
}

// upsertResourceMappingsInTx points the new and changed files of a package revision at their content, in statements
// of at most resourceInsertMaxRows files.
func upsertResourceMappingsInTx(ctx context.Context, tx *sql.Tx, prk key.PackageRevisionKey, plan resourceWritePlan) error {
	for start := 0; start < len(plan.changedKeys); start += resourceInsertMaxRows {
		end := min(start+resourceInsertMaxRows, len(plan.changedKeys))
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO resources (k8s_name_space, k8s_name, revision, resource_key, content_hash)
				SELECT $1::text, $2::text, $3::int, k, h FROM unnest($4::text[], $5::bytea[]) AS t(k, h)
				ON CONFLICT (k8s_name_space, k8s_name, resource_key)
				DO UPDATE SET content_hash = EXCLUDED.content_hash, revision = EXCLUDED.revision`,
			prk.K8SNS(), prk.K8SName(), prk.Revision, plan.changedKeys[start:end], plan.changedHashes[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// deleteAllResourceMappingsInTx removes every file of a package revision.
func deleteAllResourceMappingsInTx(ctx context.Context, tx *sql.Tx, prk key.PackageRevisionKey) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM resources WHERE k8s_name_space=$1 AND k8s_name=$2`, prk.K8SNS(), prk.K8SName())
	return err
}

// deleteResourceMappingsInTx removes the named files of a package revision. It does nothing when keys is empty.
func deleteResourceMappingsInTx(ctx context.Context, tx *sql.Tx, prk key.PackageRevisionKey, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx,
		`DELETE FROM resources WHERE k8s_name_space=$1 AND k8s_name=$2 AND resource_key = ANY($3)`,
		prk.K8SNS(), prk.K8SName(), keys)
	return err
}

// refreshResourceRevisionInTx updates the revision number on unchanged rows. It changes when a draft is published,
// and every row of the package revision carries it.
func refreshResourceRevisionInTx(ctx context.Context, tx *sql.Tx, prk key.PackageRevisionKey) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE resources SET revision=$3 WHERE k8s_name_space=$1 AND k8s_name=$2 AND revision <> $3`,
		prk.K8SNS(), prk.K8SName(), prk.Revision)
	return err
}

// pkgRevResourceHashesInTx returns the content hash currently stored for each file of a package revision.
func pkgRevResourceHashesInTx(ctx context.Context, tx *sql.Tx, prk key.PackageRevisionKey) (map[string][]byte, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT resource_key, content_hash FROM resources WHERE k8s_name_space=$1 AND k8s_name=$2`,
		prk.K8SNS(), prk.K8SName())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hashes := make(map[string][]byte)
	for rows.Next() {
		var resourceKey string
		var hash []byte
		if err := rows.Scan(&resourceKey, &hash); err != nil {
			return nil, err
		}
		hashes[resourceKey] = hash
	}
	return hashes, rows.Err()
}

func pkgRevResourcesDeleteFromDB(ctx context.Context, prk key.PackageRevisionKey) error {
	_, span := tracer.Start(ctx, "dbpackagerevisionresourcessql::pkgRevResourcesDeleteFromDB")
	defer span.End()

	klog.V(5).Infof("pkgRevResourcesDeleteFromDB: deleting package revision %+v", prk)

	sqlStatement := `DELETE FROM resources WHERE k8s_name_space=$1 AND k8s_name=$2`

	klog.V(6).Infof("pkgRevResourcesDeleteFromDB: running query %q on package revision %+v", sqlStatement, prk)
	_, err := GetDB().db.Exec(ctx, sqlStatement, prk.K8SNS(), prk.K8SName())

	if err == nil {
		klog.V(5).Infof("pkgRevResourcesDeleteFromDB: deleted package revision resources for %+v", prk)
		telemetry.RecordPackageRevisionResourcesSize(ctx, prk, 0)
	} else {
		klog.Warningf("pkgRevResourcesDeleteFromDB: deletion of package revision resources for %+v failed: %q", prk, err)
	}
	return err
}
