// Copyright 2025 The kpt Authors
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
)

type dbSQLInterface interface {
	Close() error
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *sql.Row
}

var _ dbSQLInterface = &dbSQL{}

type dbSQL struct {
	db *sql.DB
}

func (ds *dbSQL) Close() error {
	if ds.db != nil {
		return ds.db.Close()
	} else {
		return fmt.Errorf("cannot close database, database is not initialized")
	}
}

func (ds *dbSQL) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	if ds.db != nil {
		return ds.db.BeginTx(ctx, opts)
	} else {
		return nil, fmt.Errorf("cannot begin transaction, database is not initialized")
	}
}

func (ds *dbSQL) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if ds.db != nil {
		return ds.db.ExecContext(ctx, query, args...)
	} else {
		return nil, fmt.Errorf("cannot execute query on database, database is not initialized")
	}
}

func (ds *dbSQL) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if ds.db != nil {
		return ds.db.QueryContext(ctx, query, args...)
	} else {
		return nil, fmt.Errorf("cannot query database, database is not initialized")
	}
}

func (ds *dbSQL) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	if ds.db != nil {
		return ds.db.QueryRowContext(ctx, query, args...)
	} else {
		return nil
	}
}
