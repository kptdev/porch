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

import "context"

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
