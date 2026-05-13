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

	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/klog/v2"
)

func pkgReadFromDB(ctx context.Context, pk repository.PackageKey) (*dbPackage, error) {
	_, span := tracer.Start(ctx, "dbpackagesql::pkgReadFromDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("pkgReadFromDB: reading package %+v", pk)

	sqlStatement := `
		SELECT
			repositories.k8s_name_space,
			repositories.k8s_name,
			repositories.directory,
			repositories.default_ws_name,
			packages.k8s_name,
			packages.package_path,
			packages.meta,
			packages.spec,
			packages.updated,
			packages.updatedby
		FROM packages INNER JOIN repositories
			ON packages.k8s_name_space=repositories.k8s_name_space AND packages.repo_k8s_name=repositories.k8s_name
		WHERE packages.k8s_name_space=$1 AND packages.k8s_name=$2
		ORDER BY packages.k8s_name_space, packages.k8s_name
	`

	klog.V(5).Infof("pkgReadFromDB: running query %q on package %+v", sqlStatement, pk)
	rows, err := GetDB().db.Query(ctx, sqlStatement, pk.K8SNS(), pk.K8SName())
	if err != nil {
		klog.Warningf("pkgReadFromDB: reading package %+v returned err: %q", pk, err)
		return nil, err
	}

	klog.V(5).Infof("pkgReadPkgFromDB: query succeeded for %+v", pk)

	pkgs, err := pkgScanRowsFromDB(ctx, rows)
	if err != nil {
		klog.Warningf("pkgReadFromDB: reading package %+v returned err: %q", pk, err)
		return nil, err
	}

	if len(pkgs) == 0 {
		return nil, sql.ErrNoRows
	}

	if len(pkgs) > 1 {
		err := fmt.Errorf("pkgReadFromDB: reading package %+v should return 1 package, it returned %d packages", pk, len(pkgs))
		klog.Warning(err.Error())
		return nil, err
	}

	return pkgs[0], err
}

func pkgListPkgsFromDB(ctx context.Context, filter repository.ListPackageFilter) ([]*dbPackage, error) {
	_, span := tracer.Start(ctx, "dbrepositorysql::pkgListPkgsFromDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("pkgListPkgsFromDB: listing packages for filter %+v", filter)

	sqlStatement := `
		SELECT
			repositories.k8s_name_space,
			repositories.k8s_name,
			repositories.directory,
			repositories.default_ws_name,
			packages.k8s_name,
			packages.package_path,
			packages.meta,
			packages.spec,
			packages.updated,
			packages.updatedby
		FROM packages INNER JOIN repositories
			ON packages.k8s_name_space=repositories.k8s_name_space AND packages.repo_k8s_name=repositories.k8s_name
	`

	sqlStatement += pkgListFilter2WhereClause(filter)

	sqlStatement += `
			ORDER BY packages.k8s_name_space, packages.k8s_name
	`

	klog.V(6).Infof("pkgRevListPRsFromDB: running query %q on package revisions with filter %+v", sqlStatement, filter)
	rows, err := GetDB().db.Query(ctx, sqlStatement)
	if err != nil {
		klog.Warningf("pkgRevListPRsFromDB: reading package revision list for filter %+v returned err: %q", filter, err)
		return nil, err
	}

	return pkgScanRowsFromDB(ctx, rows)
}

func pkgReadPkgsFromDB(ctx context.Context, rk repository.RepositoryKey) ([]*dbPackage, error) {
	_, span := tracer.Start(ctx, "dbpackagesql::pkgReadPkgsFromDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("pkgReadFromDB: reading packages from repository %+v", rk)

	sqlStatement := `
		SELECT
			repositories.k8s_name_space,
			repositories.k8s_name,
			repositories.directory,
			repositories.default_ws_name,
			packages.k8s_name,
			packages.package_path,
			packages.meta,
			packages.spec,
			packages.updated,
			packages.updatedby
		FROM packages INNER JOIN repositories
			ON packages.k8s_name_space=repositories.k8s_name_space AND packages.repo_k8s_name=repositories.k8s_name
		WHERE packages.k8s_name_space=$1 AND packages.repo_k8s_name=$2
		ORDER BY packages.k8s_name_space, packages.k8s_name
	`

	klog.V(5).Infof("pkgReadPkgsFromDB: running query %q on packages in repository %+v", sqlStatement, rk)
	rows, err := GetDB().db.Query(ctx, sqlStatement, rk.Namespace, rk.Name)
	if err != nil {
		return nil, err
	}

	klog.V(5).Infof("pkgReadPkgsFromDB: query succeeded for %+v", rk)

	pkgs, err := pkgScanRowsFromDB(ctx, rows)

	if err != nil {
		if err == sql.ErrNoRows {
			klog.V(5).Infof("pkgReadPkgsFromDB: packages for repo %+v not found in db", rk)
		} else {
			klog.Warningf("pkgReadFromDB: reading packages from repo %+v returned err: %q", rk, err)
		}
		return nil, err
	}

	return pkgs, nil
}

func pkgScanRowsFromDB(ctx context.Context, rows *sql.Rows) ([]*dbPackage, error) {
	_, span := tracer.Start(ctx, "dbpackagesql::pkgScanRowsFromDB", trace.WithAttributes())
	defer span.End()

	defer rows.Close()

	klog.V(5).Infof("pkgScanRowsFromDB: scanning query rows")

	var dbPkgs []*dbPackage

	for rows.Next() {
		var dbPkg dbPackage
		var pkgK8SName, metaAsJSON, specAsJSON string

		err := rows.Scan(
			&dbPkg.pkgKey.RepoKey.Namespace,
			&dbPkg.pkgKey.RepoKey.Name,
			&dbPkg.pkgKey.RepoKey.Path,
			&dbPkg.pkgKey.RepoKey.PlaceholderWSname,
			&pkgK8SName,
			&dbPkg.pkgKey.Path,
			&metaAsJSON,
			&specAsJSON,
			&dbPkg.updated,
			&dbPkg.updatedBy)

		if err != nil {
			klog.Warningf("pkgScanRowsFromDB: query row scanning failed: %q", err)
			return nil, err
		}

		repo := cachetypes.CacheInstance.GetRepository(dbPkg.pkgKey.RepoKey)
		if repo == nil {
			klog.V(5).Infof("pkgScanRowsFromDB: repository %+v not found in cache, skipping package", dbPkg.pkgKey.RepoKey)
			continue
		}
		dbPkg.repo = repo.(*dbRepository)
		dbPkg.pkgKey.Package = repository.K8SName2PkgName(pkgK8SName)
		setValueFromJSON(metaAsJSON, &dbPkg.meta)
		setValueFromJSON(specAsJSON, &dbPkg.spec)

		dbPkgs = append(dbPkgs, &dbPkg)
	}

	klog.V(5).Infof("pkgScanRowsFromDB: query rows scanned")

	return dbPkgs, nil
}

func pkgWriteToDB(ctx context.Context, p *dbPackage) error {
	_, span := tracer.Start(ctx, "dbpackagesql::pkgWriteToDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("pkgWriteToDB: writing package %+v", p.Key())

	sqlStatement := `
        INSERT INTO packages (k8s_name_space, k8s_name, repo_k8s_name, package_path, meta, spec, updated, updatedby)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	klog.V(6).Infof("pkgWriteToDB: running query %q on package %+v", sqlStatement, p.Key())
	pk := p.Key()
	if _, err := GetDB().db.Exec(ctx, sqlStatement,
		pk.K8SNS(), pk.K8SName(),
		pk.RepoKey.K8SName(), pk.Path, valueAsJSON(p.meta), valueAsJSON(p.spec), p.updated, p.updatedBy); err == nil {
		klog.V(5).Infof("pkgWriteToDB: query succeeded for %+v", p.Key())
		return nil
	} else {
		klog.Warningf("pkgWriteToDB: query failed for %+v: %q", p.Key(), err)
		return err
	}
}

func pkgUpdateDB(ctx context.Context, p *dbPackage) error {
	_, span := tracer.Start(ctx, "dbpackagesql::pkgUpdateDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("pkgUpdateDB: updating package %+v", p.Key())

	sqlStatement := `
        UPDATE packages SET repo_k8s_name=$3, package_path=$4, meta=$5, spec=$6, updated=$7, updatedby=$8
        WHERE k8s_name_space=$1 AND k8s_name=$2`

	klog.V(6).Infof("pkgUpdateDB: running query %q on package %+v", sqlStatement, p.Key())
	pk := p.Key()
	result, err := GetDB().db.Exec(ctx,
		sqlStatement,
		pk.K8SNS(), pk.K8SName(),
		pk.RepoKey.K8SName(), pk.Path,
		valueAsJSON(p.meta), valueAsJSON(p.spec), p.updated, p.updatedBy)

	if err == nil {
		if rowsAffected, _ := result.RowsAffected(); rowsAffected == 1 {
			klog.V(5).Infof("pkgUpdateDB: query succeeded for %+v", pk)
			return nil
		}
		err = fmt.Errorf("update failed, no rows or multiple rows found for updating")
	}

	klog.Warningf("pkgUpdateDB: query failed for %+v: %q", pk, err)
	return err
}

func pkgDeleteFromDB(ctx context.Context, pk repository.PackageKey) error {
	_, span := tracer.Start(ctx, "dbpackagesql::pkgDeleteFromDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("pkgDeleteFromDB: deleting package %+v", pk)

	sqlStatement := `DELETE FROM packages WHERE k8s_name_space=$1 AND k8s_name=$2`

	klog.V(6).Infof("pkgDeleteFromDB: DB Connection: running query %q on package %+v", sqlStatement, pk)
	if _, err := GetDB().db.Exec(ctx, sqlStatement, pk.K8SNS(), pk.K8SName()); err == nil {
		klog.V(5).Infof("pkgDeleteFromDB: query succeeded for %+v", pk)
		return nil
	} else {
		klog.Warningf("pkgDeleteFromDB: query failed for %+v: %q", pk, err)
		return err
	}
}
