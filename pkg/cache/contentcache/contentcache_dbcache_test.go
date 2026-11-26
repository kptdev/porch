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

package contentcache

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/kptdev/porch/pkg/cache/dbcache"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDBDataSource = "postgresql://porch:porch@localhost:55439/porch"

// TestGetPackageContent_RepoMissingFromDBCache checks that a repository the
// DB cache has not opened surfaces as ErrRepositoryNotFound, which callers
// treat as transient.
func TestGetPackageContent_RepoMissingFromDBCache(t *testing.T) {
	config := embeddedpostgres.DefaultConfig().
		Username("porch").Password("porch").Database("porch").Port(55439).
		RuntimePath(t.TempDir())
	if mavenMirror := os.Getenv("DB_MAVEN_MIRROR"); mavenMirror != "" {
		config = config.BinaryRepositoryURL(mavenMirror)
	}
	postgres := embeddedpostgres.NewDatabase(config)
	require.NoError(t, postgres.Start())
	t.Cleanup(func() { _ = postgres.Stop() })

	schemaDir := os.Getenv("PORCHDIR")
	if schemaDir == "" {
		schemaDir = "../../.."
	}
	schemaSQL, err := os.ReadFile(filepath.Join(schemaDir, "api/sql/porch-db.sql"))
	require.NoError(t, err)
	db, err := sql.Open("pgx", testDBDataSource)
	require.NoError(t, err)
	_, err = db.Exec(string(schemaSQL))
	require.NoError(t, err)
	require.NoError(t, db.Close())

	cache, err := (&dbcache.DBCacheFactory{}).NewCache(t.Context(), cachetypes.CacheOptions{
		DBCacheOptions: cachetypes.DBCacheOptions{Driver: "pgx", DataSource: testDBDataSource},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbcache.CloseDB(context.Background()) })

	_, err = NewContentCache(cache).GetPackageContent(t.Context(), testRepoKey, testPkg, testWS)
	assert.ErrorIs(t, err, repository.ErrRepositoryNotFound)
}
