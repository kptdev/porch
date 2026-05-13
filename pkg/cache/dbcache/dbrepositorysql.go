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
	"fmt"

	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/klog/v2"
)

func repoReadFromDB(ctx context.Context, rk repository.RepositoryKey) (*dbRepository, error) {
	_, span := tracer.Start(ctx, "dbrepositorysql::repoReadFromDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("repoReadFromDB: reading repo %+v", rk)

	sqlStatement := `
		SELECT k8s_name_space, k8s_name, directory, default_ws_name, meta, spec, updated, updatedby, deployment
		FROM repositories
		WHERE k8s_name_space=$1 AND k8s_name=$2`

	var dbRepo dbRepository
	var metaAsJSON, specAsJSON string

	klog.V(6).Infof("repoReadFromDB: running query %q on repository %+v", sqlStatement, rk)
	err := GetDB().db.QueryRow(ctx, sqlStatement, rk.K8SNS(), rk.K8SName()).Scan(
		&dbRepo.repoKey.Namespace,
		&dbRepo.repoKey.Name,
		&dbRepo.repoKey.Path,
		&dbRepo.repoKey.PlaceholderWSname,
		&metaAsJSON,
		&specAsJSON,
		&dbRepo.updated,
		&dbRepo.updatedBy,
		&dbRepo.deployment)

	if err != nil {
		klog.Warningf("repoReadFromDB: error reading repo %+v: %q", rk, err)
		return nil, err
	}

	setValueFromJSON(metaAsJSON, &dbRepo.meta)
	setValueFromJSON(specAsJSON, &dbRepo.spec)

	klog.V(5).Infof("repoReadFromDB: read repo %+v", rk)

	return &dbRepo, err
}

func repoWriteToDB(ctx context.Context, r *dbRepository) error {
	_, span := tracer.Start(ctx, "dbrepositorysql::repoWriteToDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("repoWriteToDB: writing repo %+v", r.Key())

	sqlStatement := `
        INSERT INTO repositories (k8s_name_space, k8s_name, directory, default_ws_name, meta, spec, updated, updatedby, deployment)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	klog.V(6).Infof("repoWriteToDB: running query %q on repository %+v", sqlStatement, r)
	rk := r.Key()
	if _, err := GetDB().db.Exec(ctx,
		sqlStatement,
		rk.K8SNS(), rk.K8SName(), rk.Path, rk.PlaceholderWSname, valueAsJSON(r.meta), valueAsJSON(r.spec), r.updated, r.updatedBy, r.deployment); err == nil {
		klog.V(5).Infof("repoWriteToDB: query succeeded, row created for %+v", r.Key())
		return nil
	} else {
		klog.Warningf("repoWriteToDB: query failed %q", err)
		return err
	}
}

func repoUpdateDB(ctx context.Context, r *dbRepository) error {
	_, span := tracer.Start(ctx, "dbrepositorysql::repoUpdateDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("repoUpdateDB: updating repo %+v", r.Key())

	sqlStatement := `
        UPDATE repositories SET directory=$3, default_ws_name=$4 ,meta=$5, spec=$6, updated=$7, updatedby=$8, deployment=$9
        WHERE k8s_name_space=$1 AND k8s_name=$2`

	rk := r.Key()
	klog.V(6).Infof("repoUpdateDB: running query %q on repository %+v", sqlStatement, rk)
	result, err := GetDB().db.Exec(ctx,
		sqlStatement,
		rk.K8SNS(), rk.K8SName(), rk.Path, rk.PlaceholderWSname, valueAsJSON(r.meta), valueAsJSON(r.spec), r.updated, r.updatedBy, r.deployment)

	if err == nil {
		if rowsAffected, _ := result.RowsAffected(); rowsAffected == 1 {
			klog.V(5).Infof("repoUpdateDB: query succeeded for %q", rk)
			return nil
		}
		err = fmt.Errorf("update failed, no rows or multiple rows found for updating")
	}

	klog.Warningf("repoUpdateDB: query failed for %q: %q", rk, err)
	return err
}

func repoDeleteFromDB(ctx context.Context, rk repository.RepositoryKey) error {
	_, span := tracer.Start(ctx, "dbrepositorysql::repoDeleteFromDB", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("repoDeleteFromDB: deleting repo %+v", rk)

	sqlStatement := `DELETE FROM repositories WHERE k8s_name_space=$1 AND k8s_name=$2`

	klog.V(6).Infof("repoDeleteFromDB: running query %q on repository %+v", sqlStatement, rk)
	_, err := GetDB().db.Exec(ctx, sqlStatement, rk.K8SNS(), rk.K8SName())

	if err == nil {
		klog.V(5).Infof("repoDeleteFromDB: deleted repo %+v", rk)
	} else {
		klog.Warningf("repoDeleteFromDB: delete of repo %+v failed", rk)
	}

	return err
}
