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

package prrstore

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kptdev/porch/api/porch/key"
	"github.com/kptdev/porch/pkg/cache/pgpool"
	"k8s.io/klog/v2"
)

// sqlStore implements PRRStore using PostgreSQL.
type sqlStore struct {
	pool *pgxpool.Pool
}

// NewSQLStore creates a new SQL-backed PRRStore using an existing pgxpool.
func NewSQLStore(pool *pgxpool.Pool) (PRRStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("database pool cannot be nil")
	}
	return &sqlStore{pool: pool}, nil
}

// NewSQLStoreWithManager creates a new SQL-backed PRRStore using the pgpool manager.
// This is the preferred way to initialize the PRRStore; it ensures a single pool per process.
func NewSQLStoreWithManager(ctx context.Context, config *pgxpool.Config) (PRRStore, error) {
	pool, err := pgpool.Get(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get pgpool: %w", err)
	}
	return NewSQLStore(pool)
}

// GetResources returns all resources for a package revision.
func (s *sqlStore) GetResources(ctx context.Context, prKey key.PackageRevisionKey) (map[string]string, error) {
	const query = `
		SELECT resource_key, resource_value 
		FROM package_revision_resources
		WHERE k8s_name_space = $1 AND k8s_name = $2
		ORDER BY resource_key
	`

	rows, err := s.pool.Query(ctx, query, prKey.K8SNS(), prKey.K8SName())
	if err != nil {
		return nil, fmt.Errorf("query resources for %s: %w", prKey, err)
	}
	defer rows.Close()

	resources := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan resource row for %s: %w", prKey, err)
		}
		resources[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resource rows for %s: %w", prKey, err)
	}

	klog.V(5).Infof("GetResources for %s: retrieved %d resources", prKey, len(resources))
	return resources, nil
}

// GetFilteredResources returns resources filtered by caller-provided subset.
// The filter is applied in-memory; the database returns all and caller provides the filter.
// This matches the API contract where the caller supplies the keys they want.
func (s *sqlStore) GetFilteredResources(ctx context.Context, prKey key.PackageRevisionKey, filter map[string]string) (map[string]string, error) {
	// Get all resources
	all, err := s.GetResources(ctx, prKey)
	if err != nil {
		return nil, err
	}

	// Apply filter: return only keys that are in the filter map
	if len(filter) == 0 {
		return all, nil
	}

	filtered := make(map[string]string)
	for key := range filter {
		if value, ok := all[key]; ok {
			filtered[key] = value
		}
	}

	klog.V(5).Infof("GetFilteredResources for %s: filtered %d/%d resources", prKey, len(filtered), len(all))
	return filtered, nil
}

// WriteResources atomically writes all resources and bumps the version.
// Transaction ensures atomicity: DELETE all + INSERT new + UPDATE version in one tx.
// It leaves the store-only marker alone (NULL for a new row); use WriteResourcesAs to change it.
func (s *sqlStore) WriteResources(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string) (string, error) {
	return s.writeResources(ctx, prKey, resources, false, "")
}

// WriteResourcesAs writes resources like WriteResources and, in the same transaction,
// sets or clears the store-only marker. See PRRWriter.WriteResourcesAs.
func (s *sqlStore) WriteResourcesAs(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string, storeOnlyLifecycle string) (string, error) {
	switch storeOnlyLifecycle {
	case "", StoreOnlyDraft, StoreOnlyProposed:
	default:
		return "", fmt.Errorf("invalid store-only lifecycle %q for %s", storeOnlyLifecycle, prKey)
	}
	return s.writeResources(ctx, prKey, resources, true, storeOnlyLifecycle)
}

// writeResources is the shared write transaction. setMarker false leaves the marker
// column untouched; true sets it to storeOnlyLifecycle, where "" clears it.
func (s *sqlStore) writeResources(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string, setMarker bool, storeOnlyLifecycle string) (string, error) {
	// Begin transaction
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin transaction for %s: %w", prKey, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	// Step 1: Delete all existing resources for this PR
	delQuery := `DELETE FROM package_revision_resources WHERE k8s_name_space = $1 AND k8s_name = $2`
	_, err = tx.Exec(ctx, delQuery, prKey.K8SNS(), prKey.K8SName())
	if err != nil {
		return "", fmt.Errorf("delete existing resources for %s: %w", prKey, err)
	}
	klog.V(5).Infof("WriteResources: deleted existing resources for %s", prKey)

	// Step 2: Insert new resources (skip if empty)
	if len(resources) > 0 {
		insertQuery := `
			INSERT INTO package_revision_resources (k8s_name_space, k8s_name, resource_key, resource_value)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (k8s_name_space, k8s_name, resource_key) 
			DO UPDATE SET resource_value = EXCLUDED.resource_value
		`
		for resourceKey, resourceValue := range resources {
			_, err := tx.Exec(ctx, insertQuery, prKey.K8SNS(), prKey.K8SName(), resourceKey, resourceValue)
			if err != nil {
				return "", fmt.Errorf("insert resource %q for %s: %w", resourceKey, prKey, err)
			}
		}
		klog.V(5).Infof("WriteResources: inserted %d resources for %s", len(resources), prKey)
	}

	// Step 3: Generate and bump version
	newVersion := generateResourceVersion()

	if setMarker {
		// nil is NULL: git owns the revision.
		var marker any
		if storeOnlyLifecycle != "" {
			marker = storeOnlyLifecycle
		}
		versionQuery := `
			INSERT INTO prr_resource_versions (k8s_name_space, k8s_name, resource_version, store_only_lifecycle)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (k8s_name_space, k8s_name)
			DO UPDATE SET resource_version = EXCLUDED.resource_version,
			              store_only_lifecycle = EXCLUDED.store_only_lifecycle
		`
		_, err = tx.Exec(ctx, versionQuery, prKey.K8SNS(), prKey.K8SName(), newVersion, marker)
	} else {
		versionQuery := `
			INSERT INTO prr_resource_versions (k8s_name_space, k8s_name, resource_version)
			VALUES ($1, $2, $3)
			ON CONFLICT (k8s_name_space, k8s_name)
			DO UPDATE SET resource_version = EXCLUDED.resource_version
		`
		_, err = tx.Exec(ctx, versionQuery, prKey.K8SNS(), prKey.K8SName(), newVersion)
	}
	if err != nil {
		return "", fmt.Errorf("update version for %s: %w", prKey, err)
	}
	klog.V(5).Infof("WriteResources: bumped version for %s to %s", prKey, newVersion)

	// Commit transaction
	err = tx.Commit(ctx)
	if err != nil {
		return "", fmt.Errorf("commit transaction for %s: %w", prKey, err)
	}

	klog.V(4).Infof("WriteResources succeeded for %s, new version: %s", prKey, newVersion)
	return newVersion, nil
}

// GetResourceVersion returns the current resourceVersion for a PR, or "" if none.
func (s *sqlStore) GetResourceVersion(ctx context.Context, prKey key.PackageRevisionKey) (string, error) {
	const query = `
		SELECT resource_version
		FROM prr_resource_versions
		WHERE k8s_name_space = $1 AND k8s_name = $2
	`
	var version string
	err := s.pool.QueryRow(ctx, query, prKey.K8SNS(), prKey.K8SName()).Scan(&version)
	if err != nil {
		// No row means no version yet; treat as empty (not an error).
		if err.Error() == "no rows in result set" {
			return "", nil
		}
		return "", fmt.Errorf("query resource version for %s: %w", prKey, err)
	}
	return version, nil
}

// ListStoreOnly returns repoKey's store-only revisions. Keys are parsed from the object
// name but carry repoKey itself, which keeps its path and placeholder workspace.
func (s *sqlStore) ListStoreOnly(ctx context.Context, repoKey key.RepositoryKey) ([]StoreOnlyRevision, error) {
	const query = `
		SELECT k8s_name, store_only_lifecycle
		FROM prr_resource_versions
		WHERE k8s_name_space = $1 AND store_only_lifecycle IS NOT NULL
		ORDER BY k8s_name
	`

	rows, err := s.pool.Query(ctx, query, repoKey.Namespace)
	if err != nil {
		return nil, fmt.Errorf("query store-only revisions for repo %s: %w", repoKey, err)
	}
	defer rows.Close()

	var revs []StoreOnlyRevision
	for rows.Next() {
		var k8sName, lifecycle string
		if err := rows.Scan(&k8sName, &lifecycle); err != nil {
			return nil, fmt.Errorf("scan store-only row for repo %s: %w", repoKey, err)
		}
		prKey, perr := key.PkgRevK8sName2Key(repoKey.Namespace, k8sName)
		if perr != nil {
			klog.Warningf("ListStoreOnly: skipping unparseable name %q in namespace %s: %v", k8sName, repoKey.Namespace, perr)
			continue
		}
		// A namespace can host more than one repository.
		if prKey.RKey().Name != repoKey.Name {
			continue
		}
		prKey.PkgKey.RepoKey = repoKey
		revs = append(revs, StoreOnlyRevision{Key: prKey, Lifecycle: lifecycle})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate store-only rows for repo %s: %w", repoKey, err)
	}

	klog.V(5).Infof("ListStoreOnly for repo %s: %d revisions", repoKey, len(revs))
	return revs, nil
}

// WriteResourcesIfUnchanged atomically writes resources only if the stored version
// still equals expectedVersion (compare-and-swap). Returns conflict=true without
// writing if the version advanced. An empty expectedVersion requires that no version
// row currently exists.
func (s *sqlStore) WriteResourcesIfUnchanged(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string, expectedVersion string) (string, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("begin transaction for %s: %w", prKey, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	// Lock the version row (if any) and read the current version within the tx so the
	// compare-and-swap is race-free against concurrent writers.
	var currentVersion string
	lockQuery := `
		SELECT resource_version FROM prr_resource_versions
		WHERE k8s_name_space = $1 AND k8s_name = $2
		FOR UPDATE
	`
	err = tx.QueryRow(ctx, lockQuery, prKey.K8SNS(), prKey.K8SName()).Scan(&currentVersion)
	if err != nil {
		if err.Error() == "no rows in result set" {
			currentVersion = ""
		} else {
			return "", false, fmt.Errorf("lock version for %s: %w", prKey, err)
		}
	}

	// Compare-and-swap guard: bail without writing if the base version was superseded.
	if currentVersion != expectedVersion {
		klog.V(4).Infof("WriteResourcesIfUnchanged: version conflict for %s (expected %q, found %q)", prKey, expectedVersion, currentVersion)
		return "", true, nil
	}

	delQuery := `DELETE FROM package_revision_resources WHERE k8s_name_space = $1 AND k8s_name = $2`
	if _, err = tx.Exec(ctx, delQuery, prKey.K8SNS(), prKey.K8SName()); err != nil {
		return "", false, fmt.Errorf("delete existing resources for %s: %w", prKey, err)
	}

	if len(resources) > 0 {
		insertQuery := `
			INSERT INTO package_revision_resources (k8s_name_space, k8s_name, resource_key, resource_value)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (k8s_name_space, k8s_name, resource_key)
			DO UPDATE SET resource_value = EXCLUDED.resource_value
		`
		for resourceKey, resourceValue := range resources {
			if _, err = tx.Exec(ctx, insertQuery, prKey.K8SNS(), prKey.K8SName(), resourceKey, resourceValue); err != nil {
				return "", false, fmt.Errorf("insert resource %q for %s: %w", resourceKey, prKey, err)
			}
		}
	}

	newVersion := generateResourceVersion()
	versionQuery := `
		INSERT INTO prr_resource_versions (k8s_name_space, k8s_name, resource_version)
		VALUES ($1, $2, $3)
		ON CONFLICT (k8s_name_space, k8s_name)
		DO UPDATE SET resource_version = EXCLUDED.resource_version
	`
	if _, err = tx.Exec(ctx, versionQuery, prKey.K8SNS(), prKey.K8SName(), newVersion); err != nil {
		return "", false, fmt.Errorf("update version for %s: %w", prKey, err)
	}

	if err = tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("commit transaction for %s: %w", prKey, err)
	}
	committed = true

	klog.V(4).Infof("WriteResourcesIfUnchanged succeeded for %s, new version: %s", prKey, newVersion)
	return newVersion, false, nil
}

// DeleteResources removes all resources and version metadata for a PR.
// Idempotent: no error if PR does not exist.
func (s *sqlStore) DeleteResources(ctx context.Context, prKey key.PackageRevisionKey) error {
	// Begin transaction
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction for delete %s: %w", prKey, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	// Delete resources
	_, err = tx.Exec(ctx, `DELETE FROM package_revision_resources WHERE k8s_name_space = $1 AND k8s_name = $2`,
		prKey.K8SNS(), prKey.K8SName())
	if err != nil {
		return fmt.Errorf("delete resources for %s: %w", prKey, err)
	}

	// Delete version
	_, err = tx.Exec(ctx, `DELETE FROM prr_resource_versions WHERE k8s_name_space = $1 AND k8s_name = $2`,
		prKey.K8SNS(), prKey.K8SName())
	if err != nil {
		return fmt.Errorf("delete version for %s: %w", prKey, err)
	}

	err = tx.Commit(ctx)
	if err != nil {
		return fmt.Errorf("commit delete transaction for %s: %w", prKey, err)
	}

	klog.V(4).Infof("DeleteResources succeeded for %s", prKey)
	return nil
}

// DeleteRepositoryResources removes every row of a repository in one transaction
// (idempotent). Object names are "<repo>.<path>.<package>.<workspace>" and repository
// names contain no dot, so the prefix "<repo>." selects this repository's rows and not
// those of "<repo>-2". left() avoids having to escape the name for LIKE.
func (s *sqlStore) DeleteRepositoryResources(ctx context.Context, repoKey key.RepositoryKey) (err error) {
	// A v1alpha1-only install never applies the PRRStore schema, and its repositories are
	// deleted through here too. With neither table there is nothing to delete.
	var schemaAbsent bool
	const schemaQuery = `SELECT to_regclass('package_revision_resources') IS NULL AND to_regclass('prr_resource_versions') IS NULL`
	if err = s.pool.QueryRow(ctx, schemaQuery).Scan(&schemaAbsent); err != nil {
		return fmt.Errorf("check PRRStore schema for delete of repo %s: %w", repoKey, err)
	}
	if schemaAbsent {
		klog.V(4).Infof("DeleteRepositoryResources: PRRStore schema not installed, nothing to delete for repo %s", repoKey)
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction for delete of repo %s: %w", repoKey, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	const resourcesQuery = `
		DELETE FROM package_revision_resources
		WHERE k8s_name_space = $1 AND left(k8s_name, char_length($2::text) + 1) = $2::text || '.'
	`
	resTag, err := tx.Exec(ctx, resourcesQuery, repoKey.Namespace, repoKey.Name)
	if err != nil {
		return fmt.Errorf("delete resources for repo %s: %w", repoKey, err)
	}

	const versionsQuery = `
		DELETE FROM prr_resource_versions
		WHERE k8s_name_space = $1 AND left(k8s_name, char_length($2::text) + 1) = $2::text || '.'
	`
	verTag, err := tx.Exec(ctx, versionsQuery, repoKey.Namespace, repoKey.Name)
	if err != nil {
		return fmt.Errorf("delete versions for repo %s: %w", repoKey, err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete transaction for repo %s: %w", repoKey, err)
	}

	klog.V(4).Infof("DeleteRepositoryResources succeeded for repo %s: %d resource rows, %d revisions", repoKey, resTag.RowsAffected(), verTag.RowsAffected())
	return nil
}

// generateResourceVersion returns a stable, deterministic resource version token.
// Uses UUID for uniqueness and stability across retries.
// This token is used as the render-request annotation value in v1alpha2.
func generateResourceVersion() string {
	// UUID provides uniqueness and stability
	// Timestamp provides rough ordering (for debugging/auditing)
	version := fmt.Sprintf("%s-%d", uuid.New().String(), time.Now().UnixMilli())
	return version
}
