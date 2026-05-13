// Copyright 2024-2025 The kpt Authors
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
	"database/sql"
	"fmt"

	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/klog/v2"
)

func pkgRevResourceReadFromDB(ctx context.Context, prk repository.PackageRevisionKey, resKey string) (string, string, error) {
	_, span := tracer.Start(ctx, "dbpackagerevisionresourcessql::pkgRevResourceReadFromDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("pkgRevResourceReadFromDB: reading package revision resource %+v:%q", prk, resKey)

	sqlStatement := `SELECT resource_value FROM resources WHERE k8s_name_space=$1 AND k8s_name=$2 AND resource_key=$3`

	var resVal string

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

	klog.V(5).Infof("pkgRevResourceReadFromDB: reading package succeeded %+v:%q", prk, resKey)

	return resKey, resVal, err
}

func pkgRevResourcesReadFromDB(ctx context.Context, prk repository.PackageRevisionKey) (map[string]string, error) {
	_, span := tracer.Start(ctx, "dbpackagerevisionresourcessql::pkgRevResourcesReadFromDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("pkgRevResourcesReadFromDB: reading package revision resource %+v", prk)

	sqlStatement := `SELECT resource_key, resource_value FROM resources WHERE k8s_name_space=$1 AND k8s_name=$2`

	resources := make(map[string]string)

	klog.V(6).Infof("pkgRevResourcesReadFromDB: running query %q on package revision %+v", sqlStatement, prk)
	rows, err := GetDB().db.Query(ctx, sqlStatement, prk.K8SNS(), prk.K8SName())
	if err != nil {
		klog.Warningf("pkgRevResourcesReadFromDB: query failed for %+v: %q", prk, err)
		return nil, err
	}
	defer rows.Close()

	klog.V(5).Infof("pkgRevResourcesReadFromDB: query succeeded for %q", prk)

	for rows.Next() {
		var resKey, resVal string

		if err := rows.Scan(&resKey, &resVal); err != nil {
			return nil, err
		}
		resources[resKey] = resVal
	}

	return resources, nil
}

func pkgRevResourceWriteToDB(ctx context.Context, prk repository.PackageRevisionKey, resKey, resVal string) error {
	_, span := tracer.Start(ctx, "dbpackagerevisionresourcessql::pkgRevResourceWriteToDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("pkgRevResourceWriteToDB: writing package revision resource %+v=%q for %q", resKey, resVal, prk)

	sqlStatement := `
		INSERT INTO resources (k8s_name_space, k8s_name, revision, resource_key, resource_value)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (k8s_name_space, k8s_name, resource_key) 
			DO UPDATE SET resource_value = EXCLUDED.resource_value`

	klog.V(6).Infof("pkgRevResourceWriteToDB: running query %q on package revision %+v", sqlStatement, prk)
	if _, err := GetDB().db.Exec(ctx, sqlStatement, prk.K8SNS(), prk.K8SName(), prk.Revision, resKey, resVal); err == nil {
		klog.V(5).Infof("pkgRevResourceWriteToDB: query succeeded, row created/updated")
		return nil
	} else {
		klog.Warningf("pkgRevResourceWriteToDB: query failed on package revision %+v: %q", prk, err)
		return err
	}
}

func pkgRevResourcesWriteToDB(ctx context.Context, pr *dbPackageRevision) error {
	_, span := tracer.Start(ctx, "dbpackagerevisionresourcessql::pkgRevResourcesWriteToDB", trace.WithAttributes())
	defer span.End()

	prk := pr.Key()

	tx, err := GetDB().db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("pkgRevResourcesWriteToDB: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Delete all existing resources within the transaction.
	if _, err := tx.ExecContext(ctx, `DELETE FROM resources WHERE k8s_name_space=$1 AND k8s_name=$2`, prk.K8SNS(), prk.K8SName()); err != nil {
		klog.Warningf("pkgRevResourcesWriteToDB: delete failed for %+v: %q", prk, err)
		return err
	}

	if len(pr.resources) == 0 {
		klog.Warningf("pkgRevResourcesWriteToDB: pr %+v has no resources", prk)
		return tx.Commit()
	}

	klog.V(5).Infof("pkgRevResourcesWriteToDB: writing package revision resources for %+v", prk)

	for resourceKey, resourceValue := range pr.resources {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO resources (k8s_name_space, k8s_name, revision, resource_key, resource_value)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (k8s_name_space, k8s_name, resource_key)
				DO UPDATE SET resource_value = EXCLUDED.resource_value`,
			prk.K8SNS(), prk.K8SName(), prk.Revision, resourceKey, resourceValue); err != nil {
			klog.Warningf("pkgRevResourcesWriteToDB: insert failed for %+v key %q: %q", prk, resourceKey, err)
			return err
		}
	}

	klog.V(5).Infof("pkgRevResourcesWriteToDB: query succeeded, row created/updated")
	return tx.Commit()
}

func pkgRevResourcesDeleteFromDB(ctx context.Context, prk repository.PackageRevisionKey) error {
	_, span := tracer.Start(ctx, "dbpackagerevisionresourcessql::pkgRevResourcesDeleteFromDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("pkgRevResourcesDeleteFromDB: deleting package revision %+v", prk)

	sqlStatement := `DELETE FROM resources WHERE k8s_name_space=$1 AND k8s_name=$2`

	klog.V(6).Infof("pkgRevResourcesDeleteFromDB: running query %q on package revision %+v", sqlStatement, prk)
	_, err := GetDB().db.Exec(ctx, sqlStatement, prk.K8SNS(), prk.K8SName())

	if err == nil {
		klog.V(5).Infof("pkgRevResourcesDeleteFromDB: deleted package revision resources for %+v", prk)
	} else {
		klog.Warningf("pkgRevResourcesDeleteFromDB: deletion of package revision resources for %+v failed: %q", prk, err)
	}

	return err
}

func pkgRevResourceDeleteFromDB(ctx context.Context, prk repository.PackageRevisionKey, resKey string) error {
	_, span := tracer.Start(ctx, "dbpackagerevisionresourcessql::pkgRevResourceDeleteFromDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("pkgRevResourceDeleteFromDB: deleting package revision resource %q from %+v", resKey, prk)

	sqlStatement := `DELETE FROM resources WHERE k8s_name_space=$1 AND k8s_name=$2 AND resource_key=$3`

	klog.V(6).Infof("pkgRevResourceDeleteFromDB: running query %q on package revision %+v", sqlStatement, prk)
	_, err := GetDB().db.Exec(ctx, sqlStatement, prk.K8SNS(), prk.K8SName(), resKey)

	if err == nil {
		klog.V(5).Infof("pkgRevResourceDeleteFromDB: deleted package revision resource %q from %+v", resKey, prk)
	} else {
		klog.Warningf("pkgRevResourceDeleteFromDB: deletion of package revision resource %q from %+v failed", resKey, prk)
	}

	return err
}
