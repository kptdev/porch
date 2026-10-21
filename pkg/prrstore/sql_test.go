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
	"os/user"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/suite"

	"github.com/kptdev/porch/api/porch/key"
	"github.com/kptdev/porch/pkg/cache/testutil"
)

const prrStoreSchemaFile = "api/sql/porch-db-prrstore.sql"

type PRRStoreTestSuite struct {
	suite.Suite
	pool *pgxpool.Pool
	url  string
	ctx  context.Context
}

func TestPRRStoreTestSuite(t *testing.T) {
	if u, err := user.Current(); err == nil && u.Username == "root" {
		t.Fatalf("This test cannot run as %q user", u.Username)
	}
	suite.Run(t, &PRRStoreTestSuite{ctx: t.Context()})
}

func (s *PRRStoreTestSuite) SetupSuite() {
	// StartEmbeddedPostgres uses a dynamically-allocated port and per-test runtime/data
	// dirs so this package can run in parallel with the other postgres test packages.
	pg := testutil.StartEmbeddedPostgres(s.T())
	s.url = pg.URL

	pool, err := pgxpool.New(context.Background(), pg.URL)
	s.Require().NoError(err, "could not connect to test database")

	s.T().Cleanup(func() {
		time.Sleep(1 * time.Second)
		pool.Close()
	})

	testutil.ApplySchema(s.T(), prrStoreSchemaFile, func(sql string) error {
		_, err := pool.Exec(context.Background(), sql)
		return err
	})

	s.pool = pool
}

func (s *PRRStoreTestSuite) SetupTest() {
	// Clean up data between tests
	_, err := s.pool.Exec(s.ctx, "DELETE FROM package_revision_resources")
	s.NoError(err)
	_, err = s.pool.Exec(s.ctx, "DELETE FROM prr_resource_versions")
	s.NoError(err)
}

func (s *PRRStoreTestSuite) testPRKey(ns, name string) key.PackageRevisionKey {
	return key.PackageRevisionKey{
		PkgKey: key.PackageKey{
			RepoKey: key.RepositoryKey{
				Namespace: ns,
				Name:      "test-repo",
			},
			Path:    "",
			Package: name,
		},
		WorkspaceName: "default",
		Revision:      1,
	}
}

// ===== CRUD Tests =====

func (s *PRRStoreTestSuite) TestGetResources_Empty() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")
	resources, err := store.GetResources(s.ctx, prKey)

	s.NoError(err)
	s.Empty(resources)
}

func (s *PRRStoreTestSuite) TestWriteResources_Basic() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")
	input := map[string]string{
		"Kptfile":   "apiVersion: kpt.dev/v1",
		"README.md": "# Package",
	}

	version, err := store.WriteResources(s.ctx, prKey, input)
	s.NoError(err)
	s.NotEmpty(version)
}

func (s *PRRStoreTestSuite) TestGetResources_AfterWrite() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")
	input := map[string]string{
		"Kptfile":    "apiVersion: kpt.dev/v1",
		"README.md":  "# Package",
		"file1.yaml": "key: value1",
	}

	_, err = store.WriteResources(s.ctx, prKey, input)
	s.NoError(err)

	resources, err := store.GetResources(s.ctx, prKey)
	s.NoError(err)
	s.Equal(input, resources)
}

func (s *PRRStoreTestSuite) TestWriteResources_Overwrites() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")

	input1 := map[string]string{
		"Kptfile": "v1",
		"file1":   "content1",
	}
	version1, err := store.WriteResources(s.ctx, prKey, input1)
	s.NoError(err)

	input2 := map[string]string{
		"Kptfile": "v2",
		"file2":   "content2",
	}
	version2, err := store.WriteResources(s.ctx, prKey, input2)
	s.NoError(err)

	s.NotEqual(version1, version2)

	resources, err := store.GetResources(s.ctx, prKey)
	s.NoError(err)
	s.Equal(input2, resources)
}

func (s *PRRStoreTestSuite) TestGetFilteredResources() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")
	input := map[string]string{
		"file1": "content1",
		"file2": "content2",
		"file3": "content3",
	}
	_, err = store.WriteResources(s.ctx, prKey, input)
	s.NoError(err)

	filter := map[string]string{
		"file1": "",
		"file3": "",
	}
	resources, err := store.GetFilteredResources(s.ctx, prKey, filter)
	s.NoError(err)
	s.Equal(map[string]string{
		"file1": "content1",
		"file3": "content3",
	}, resources)
}

func (s *PRRStoreTestSuite) TestDeleteResources() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")
	input := map[string]string{"file": "content"}

	_, err = store.WriteResources(s.ctx, prKey, input)
	s.NoError(err)

	err = store.DeleteResources(s.ctx, prKey)
	s.NoError(err)

	resources, err := store.GetResources(s.ctx, prKey)
	s.NoError(err)
	s.Empty(resources)
}

func (s *PRRStoreTestSuite) TestDeleteResources_Idempotent() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")

	// Delete nonexistent PR (should not error)
	err = store.DeleteResources(s.ctx, prKey)
	s.NoError(err)

	// Delete again
	err = store.DeleteResources(s.ctx, prKey)
	s.NoError(err)
}

func (s *PRRStoreTestSuite) TestWriteResources_Empty() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")

	version, err := store.WriteResources(s.ctx, prKey, map[string]string{})
	s.NoError(err)
	s.NotEmpty(version)

	resources, err := store.GetResources(s.ctx, prKey)
	s.NoError(err)
	s.Empty(resources)
}

// ===== Resource Version / Compare-And-Swap Tests =====

func (s *PRRStoreTestSuite) TestGetResourceVersion_Empty() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")
	version, err := store.GetResourceVersion(s.ctx, prKey)
	s.NoError(err)
	s.Empty(version, "no version should exist before any write")
}

func (s *PRRStoreTestSuite) TestGetResourceVersion_AfterWrite() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")
	written, err := store.WriteResources(s.ctx, prKey, map[string]string{"Kptfile": "v1"})
	s.NoError(err)

	version, err := store.GetResourceVersion(s.ctx, prKey)
	s.NoError(err)
	s.Equal(written, version, "GetResourceVersion must match the version returned by WriteResources")
}

func (s *PRRStoreTestSuite) TestWriteResourcesIfUnchanged_FromEmpty() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")
	// expectedVersion "" means "only write if no entry exists yet".
	newVersion, conflict, err := store.WriteResourcesIfUnchanged(s.ctx, prKey, map[string]string{"Kptfile": "v1"}, "")
	s.NoError(err)
	s.False(conflict)
	s.NotEmpty(newVersion)

	resources, err := store.GetResources(s.ctx, prKey)
	s.NoError(err)
	s.Equal(map[string]string{"Kptfile": "v1"}, resources)
}

func (s *PRRStoreTestSuite) TestWriteResourcesIfUnchanged_MatchingVersion() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")
	base, err := store.WriteResources(s.ctx, prKey, map[string]string{"Kptfile": "v1"})
	s.NoError(err)

	newVersion, conflict, err := store.WriteResourcesIfUnchanged(s.ctx, prKey, map[string]string{"Kptfile": "v2"}, base)
	s.NoError(err)
	s.False(conflict, "write with matching base version must succeed")
	s.NotEqual(base, newVersion)

	resources, err := store.GetResources(s.ctx, prKey)
	s.NoError(err)
	s.Equal(map[string]string{"Kptfile": "v2"}, resources)
}

func (s *PRRStoreTestSuite) TestWriteResourcesIfUnchanged_ConflictOnStaleVersion() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")
	base, err := store.WriteResources(s.ctx, prKey, map[string]string{"Kptfile": "v1"})
	s.NoError(err)

	// A concurrent writer advances the version.
	_, err = store.WriteResources(s.ctx, prKey, map[string]string{"Kptfile": "v2"})
	s.NoError(err)

	// Now a stale CAS based on the old version must conflict and NOT write.
	_, conflict, err := store.WriteResourcesIfUnchanged(s.ctx, prKey, map[string]string{"Kptfile": "stale"}, base)
	s.NoError(err)
	s.True(conflict, "stale base version must produce a conflict")

	resources, err := store.GetResources(s.ctx, prKey)
	s.NoError(err)
	s.Equal(map[string]string{"Kptfile": "v2"}, resources, "conflicting CAS must not clobber newer content")
}

func (s *PRRStoreTestSuite) TestWriteResourcesIfUnchanged_ConflictWhenEntryExists() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "pr1")
	_, err = store.WriteResources(s.ctx, prKey, map[string]string{"Kptfile": "v1"})
	s.NoError(err)

	// expectedVersion "" requires no entry; an entry exists, so this must conflict.
	_, conflict, err := store.WriteResourcesIfUnchanged(s.ctx, prKey, map[string]string{"Kptfile": "v2"}, "")
	s.NoError(err)
	s.True(conflict)
}

// ===== Constructor Tests =====

func (s *PRRStoreTestSuite) TestNewSQLStore_NilPool() {
	store, err := NewSQLStore(nil)
	s.Error(err, "nil pool must be rejected")
	s.Nil(store)
	s.Contains(err.Error(), "pool cannot be nil")
}

func (s *PRRStoreTestSuite) TestNewSQLStore_Success() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)
	s.NotNil(store)
}

func (s *PRRStoreTestSuite) TestNewSQLStoreWithManager_Success() {
	config, err := pgxpool.ParseConfig(s.url)
	s.Require().NoError(err)

	// NewSQLStoreWithManager initializes the process-wide pgpool singleton and wraps
	// it in a SQLStore. It must return a usable store.
	store, err := NewSQLStoreWithManager(s.ctx, config)
	s.NoError(err)
	s.NotNil(store)

	// The store backed by the manager pool must be functional.
	prKey := s.testPRKey("default", "mgr-pr")
	version, err := store.WriteResources(s.ctx, prKey, map[string]string{"Kptfile": "v1"})
	s.NoError(err)
	s.NotEmpty(version)

	got, err := store.GetResources(s.ctx, prKey)
	s.NoError(err)
	s.Equal(map[string]string{"Kptfile": "v1"}, got)
}

// ===== Additional CRUD Edge Cases =====

func (s *PRRStoreTestSuite) TestWriteResources_MultipleKeys() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "multi")
	input := map[string]string{
		"Kptfile":            "apiVersion: kpt.dev/v1",
		"a/nested/file.yaml": "nested: true",
		"README.md":          "# doc",
		"config.yaml":        "key: value",
	}
	_, err = store.WriteResources(s.ctx, prKey, input)
	s.NoError(err)

	got, err := store.GetResources(s.ctx, prKey)
	s.NoError(err)
	s.Equal(input, got)
}

func (s *PRRStoreTestSuite) TestGetFilteredResources_MissingKeyIgnored() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "filt")
	_, err = store.WriteResources(s.ctx, prKey, map[string]string{"file1": "c1"})
	s.NoError(err)

	// Filter requests a key that does not exist; it must be silently omitted.
	got, err := store.GetFilteredResources(s.ctx, prKey, map[string]string{"file1": "", "missing": ""})
	s.NoError(err)
	s.Equal(map[string]string{"file1": "c1"}, got)
}

func (s *PRRStoreTestSuite) TestGetFilteredResources_EmptyFilterReturnsAll() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "filt-all")
	input := map[string]string{"file1": "c1", "file2": "c2"}
	_, err = store.WriteResources(s.ctx, prKey, input)
	s.NoError(err)

	// An empty filter means "no filtering" — return everything.
	got, err := store.GetFilteredResources(s.ctx, prKey, map[string]string{})
	s.NoError(err)
	s.Equal(input, got)
}

func (s *PRRStoreTestSuite) TestWriteResources_ClearsToEmpty() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "clear")
	_, err = store.WriteResources(s.ctx, prKey, map[string]string{"file1": "c1", "file2": "c2"})
	s.NoError(err)

	// Writing an empty map must clear all resources but still bump the version.
	version, err := store.WriteResources(s.ctx, prKey, map[string]string{})
	s.NoError(err)
	s.NotEmpty(version)

	got, err := store.GetResources(s.ctx, prKey)
	s.NoError(err)
	s.Empty(got)
}

func (s *PRRStoreTestSuite) TestDeleteResources_RemovesVersion() {
	store, err := NewSQLStore(s.pool)
	s.NoError(err)

	prKey := s.testPRKey("default", "del-ver")
	_, err = store.WriteResources(s.ctx, prKey, map[string]string{"file": "c"})
	s.NoError(err)

	err = store.DeleteResources(s.ctx, prKey)
	s.NoError(err)

	// After delete, the version anchor must be gone (back to empty).
	version, err := store.GetResourceVersion(s.ctx, prKey)
	s.NoError(err)
	s.Empty(version)
}
