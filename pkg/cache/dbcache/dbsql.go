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
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/stdlib"
)

var (
	ErrCopyUnsupported     = errors.New("bulk copy not supported by this driver")
	ErrPgxQueryUnsupported = errors.New("native pgx query not supported by this driver")
)

type dbSQLInterface interface {
	Close() error
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *sql.Row
	ScanTwoTextColumns(ctx context.Context, query string, args []any, scan func(col1, col2 string) error) error
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

func (ds *dbSQL) ScanTwoTextColumns(ctx context.Context, query string, args []any, scan func(col1, col2 string) error) error {
	if ds.db == nil {
		return fmt.Errorf("cannot query database, database is not initialized")
	}

	conn, err := ds.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	return conn.Raw(func(driverConn any) error {
		stdlibConn, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return ErrPgxQueryUnsupported
		}

		rows, err := stdlibConn.Conn().Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var col1, col2 string
			if err := rows.Scan(&col1, &col2); err != nil {
				return err
			}
			if err := scan(col1, col2); err != nil {
				return err
			}
		}

		return rows.Err()
	})
}
