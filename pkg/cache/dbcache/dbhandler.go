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

	"go.opentelemetry.io/otel/trace"
	"k8s.io/klog/v2"

	cachetypes "github.com/kptdev/porch/pkg/cache/types"
)

type DBHandler struct {
	dBCacheOptions cachetypes.DBCacheOptions
	dataSource     string
	db             dbSQLInterface
}

var dbHandler *DBHandler = nil

func OpenDB(ctx context.Context, opts cachetypes.CacheOptions) error {
	_, span := tracer.Start(ctx, "DBHandler::OpenDB", trace.WithAttributes())
	defer span.End()

	klog.V(4).Infof("OpenDB: with driver %+v", opts.DBCacheOptions.Driver)

	if dbHandler != nil {
		klog.V(4).Infof("OpenDB: database, already open")
		return nil
	}

	db, err := sql.Open(opts.DBCacheOptions.Driver, opts.DBCacheOptions.DataSource)
	if err != nil {
		klog.V(4).Infof("OpenDB: database open failed: %q", err)
		return err
	}

	// Configure connection pool limits
	db.SetMaxOpenConns(opts.DBCacheOptions.MaxConnections)
	db.SetMaxIdleConns(opts.DBCacheOptions.MaxIdleConnections)
	db.SetConnMaxLifetime(opts.DBCacheOptions.MaxConnLifetime)
	klog.Infof("OpenDB: configured connection pool - MaxOpenConns=%d, MaxIdleConns=%d, MaxConnLifetime=%v",
		opts.DBCacheOptions.MaxConnections, opts.DBCacheOptions.MaxIdleConnections, opts.DBCacheOptions.MaxConnLifetime)

	if err := db.Ping(); err != nil {
		db.Close()
		klog.V(4).Infof("OpenDB: database open failed")
		return err
	}

	klog.V(4).Infof("OpenDB: database opened")

	dbHandler = &DBHandler{
		dBCacheOptions: opts.DBCacheOptions,
		db: &dbSQL{
			db: db,
		},
	}

	return nil
}

func GetDB() *DBHandler {
	if dbHandler == nil {
		klog.Errorf("GetDB: the database is not open")
		return nil
	}

	return dbHandler
}

func CloseDB(ctx context.Context) error {
	_, span := tracer.Start(ctx, "DBHandler::CloseDB", trace.WithAttributes())
	defer span.End()

	if dbHandler == nil {
		klog.V(4).Infof("CloseDB: the database is already closed")
		return nil
	}

	var err error
	if err = dbHandler.db.Close(); err == nil {
		klog.V(4).Infof("CloseDB: database %q closed", dbHandler.dataSource)
	} else {
		klog.V(4).Infof("CloseDB: close failed to database %q: %q", dbHandler.dataSource, err)
	}

	dbHandler = nil
	return err
}
