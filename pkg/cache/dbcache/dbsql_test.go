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
	"errors"
)

func (t *DbTestSuite) TestDBSQL() {
	dbSQL := dbSQL{}
	ctx := context.Background()

	err := dbSQL.Close()
	t.Require().ErrorContains(err, "cannot close database, database is not initialized")

	_, err = dbSQL.Exec(ctx, "")
	t.Require().ErrorContains(err, "cannot execute query on database, database is not initialized")

	_, err = dbSQL.Query(ctx, "")
	t.Require().ErrorContains(err, "cannot query database, database is not initialized")

	nilRow := dbSQL.QueryRow(ctx, "")
	t.Nil(nilRow)
}

func (t *DbTestSuite) TestScanTextColumnsNilDB() {
	ds := dbSQL{}
	err := ds.ScanTextColumns(context.Background(), "", nil, 1, func([]string) error { return nil })
	t.Require().ErrorContains(err, "cannot query database, database is not initialized")
}

func (t *DbTestSuite) TestScanTextColumnsOneColumn() {
	ds := GetDB().db.(*dbSQL)

	var got []string
	err := ds.ScanTextColumns(t.Context(), `SELECT 'hello'`, nil, 1, func(cols []string) error {
		got = append(got, cols[0])
		return nil
	})
	t.Require().NoError(err)
	t.Equal([]string{"hello"}, got)
}

func (t *DbTestSuite) TestScanTextColumnsTwoColumns() {
	ds := GetDB().db.(*dbSQL)

	type row struct{ a, b string }
	var got []row
	err := ds.ScanTextColumns(t.Context(), `SELECT 'key', 'value'`, nil, 2, func(cols []string) error {
		got = append(got, row{cols[0], cols[1]})
		return nil
	})
	t.Require().NoError(err)
	t.Equal([]row{{"key", "value"}}, got)
}

func (t *DbTestSuite) TestScanTextColumnsScanCallbackErrorHaltsIteration() {
	ds := GetDB().db.(*dbSQL)

	sentinel := errors.New("stop")
	calls := 0
	err := ds.ScanTextColumns(t.Context(), `SELECT unnest(ARRAY['a','b','c'])`, nil, 1, func([]string) error {
		calls++
		return sentinel
	})
	t.Require().ErrorIs(err, sentinel)
	t.Equal(1, calls)
}
