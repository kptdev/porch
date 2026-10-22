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
	"fmt"
	"os/user"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/suite"

	"github.com/kptdev/porch/api/porch/key"
	"github.com/kptdev/porch/pkg/cache/testutil"
)

const prrStoreSchemaFile = "api/sql/porch-db.sql"

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

// ===== Store-only marker =====

func (s *PRRStoreTestSuite) testRepoKey() key.RepositoryKey {
	// A repository key with the fields a real handle has beyond namespace and name,
	// to show ListStoreOnly returns the caller's key rather than one rebuilt from a name.
	return key.RepositoryKey{Namespace: "default", Name: "test-repo", Path: "some/dir", PlaceholderWSname: "main"}
}

func (s *PRRStoreTestSuite) TestWriteResourcesAs_SetsMarker() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	prKey := s.testPRKey("default", "pr1")
	_, err = store.WriteResourcesAs(s.ctx, prKey, map[string]string{"Kptfile": "v1"}, StoreOnlyDraft)
	s.Require().NoError(err)

	revs, err := store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Require().Len(revs, 1)
	s.Equal(StoreOnlyDraft, revs[0].Lifecycle)
	s.Equal("pr1", revs[0].Key.PkgKey.Package)
	s.Equal("default", revs[0].Key.WorkspaceName)
	s.Equal(0, revs[0].Key.Revision)
	s.Equal(s.testRepoKey(), revs[0].Key.RKey(), "key must carry the caller's repository key")

	got, err := store.GetResources(s.ctx, prKey)
	s.Require().NoError(err)
	s.Equal(map[string]string{"Kptfile": "v1"}, got, "content is written together with the marker")
}

func (s *PRRStoreTestSuite) TestWriteResourcesAs_ProposedAndInvalid() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	prKey := s.testPRKey("default", "pr1")
	_, err = store.WriteResourcesAs(s.ctx, prKey, map[string]string{"Kptfile": "v1"}, StoreOnlyProposed)
	s.Require().NoError(err)
	revs, err := store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Require().Len(revs, 1)
	s.Equal(StoreOnlyProposed, revs[0].Lifecycle)

	// An invalid value is rejected before anything is written.
	_, err = store.WriteResourcesAs(s.ctx, prKey, map[string]string{"Kptfile": "v2"}, "Published")
	s.Error(err)
	got, err := store.GetResources(s.ctx, prKey)
	s.Require().NoError(err)
	s.Equal(map[string]string{"Kptfile": "v1"}, got, "rejected write must not change content")
}

func (s *PRRStoreTestSuite) TestWriteResourcesAs_EmptyClearsMarker() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	prKey := s.testPRKey("default", "pr1")
	_, err = store.WriteResourcesAs(s.ctx, prKey, map[string]string{"Kptfile": "v1"}, StoreOnlyDraft)
	s.Require().NoError(err)

	// Publish hands the revision back to git: marker cleared, content updated, one tx.
	_, err = store.WriteResourcesAs(s.ctx, prKey, map[string]string{"Kptfile": "v2"}, "")
	s.Require().NoError(err)

	revs, err := store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Empty(revs)
	got, err := store.GetResources(s.ctx, prKey)
	s.Require().NoError(err)
	s.Equal(map[string]string{"Kptfile": "v2"}, got)
}

// A PRR push (WriteResources) or a render (WriteResourcesIfUnchanged) to a store-only
// draft must not make it disappear: neither may touch the marker.
func (s *PRRStoreTestSuite) TestMarkerSurvivesPlainAndCASWrites() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	prKey := s.testPRKey("default", "pr1")
	_, err = store.WriteResourcesAs(s.ctx, prKey, map[string]string{"Kptfile": "v1"}, StoreOnlyProposed)
	s.Require().NoError(err)

	_, err = store.WriteResources(s.ctx, prKey, map[string]string{"Kptfile": "pushed"})
	s.Require().NoError(err)
	revs, err := store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Require().Len(revs, 1, "WriteResources must preserve the marker")
	s.Equal(StoreOnlyProposed, revs[0].Lifecycle)

	base, err := store.GetResourceVersion(s.ctx, prKey)
	s.Require().NoError(err)
	_, conflict, err := store.WriteResourcesIfUnchanged(s.ctx, prKey, map[string]string{"Kptfile": "rendered"}, base)
	s.Require().NoError(err)
	s.Require().False(conflict)
	revs, err = store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Require().Len(revs, 1, "WriteResourcesIfUnchanged must preserve the marker")
	s.Equal(StoreOnlyProposed, revs[0].Lifecycle)
}

func (s *PRRStoreTestSuite) TestPlainWriteCreatesUnmarkedRow() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	// Discovery and the published path use plain WriteResources: git owns the revision.
	_, err = store.WriteResources(s.ctx, s.testPRKey("default", "pr1"), map[string]string{"Kptfile": "v1"})
	s.Require().NoError(err)

	revs, err := store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Empty(revs)
}

func (s *PRRStoreTestSuite) TestListStoreOnly_ScopedToRepository() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	_, err = store.WriteResourcesAs(s.ctx, s.testPRKey("default", "mine"), map[string]string{"Kptfile": "v1"}, StoreOnlyDraft)
	s.Require().NoError(err)

	// Same namespace, different repository.
	other := key.PackageRevisionKey{
		PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "default", Name: "other-repo"}, Package: "theirs"},
		WorkspaceName: "v1",
	}
	_, err = store.WriteResourcesAs(s.ctx, other, map[string]string{"Kptfile": "v1"}, StoreOnlyDraft)
	s.Require().NoError(err)

	// Different namespace, same repository name.
	otherNS := s.testPRKey("other-ns", "elsewhere")
	_, err = store.WriteResourcesAs(s.ctx, otherNS, map[string]string{"Kptfile": "v1"}, StoreOnlyDraft)
	s.Require().NoError(err)

	revs, err := store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Require().Len(revs, 1)
	s.Equal("mine", revs[0].Key.PkgKey.Package)
}

func (s *PRRStoreTestSuite) TestListStoreOnly_SubpackagePathRoundTrips() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	nested := key.PackageRevisionKey{
		PkgKey:        key.PackageKey{RepoKey: s.testRepoKey(), Path: "a/b", Package: "leaf"},
		WorkspaceName: "v1",
	}
	_, err = store.WriteResourcesAs(s.ctx, nested, map[string]string{"Kptfile": "v1"}, StoreOnlyDraft)
	s.Require().NoError(err)

	revs, err := store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Require().Len(revs, 1)
	s.Equal("a/b", revs[0].Key.PkgKey.Path)
	s.Equal("leaf", revs[0].Key.PkgKey.Package)
	s.Equal(nested.K8SName(), revs[0].Key.K8SName(), "rebuilt key must address the same row")
}

func (s *PRRStoreTestSuite) TestDeleteResourcesRemovesMarker() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	prKey := s.testPRKey("default", "pr1")
	_, err = store.WriteResourcesAs(s.ctx, prKey, map[string]string{"Kptfile": "v1"}, StoreOnlyDraft)
	s.Require().NoError(err)
	s.Require().NoError(store.DeleteResources(s.ctx, prKey))

	revs, err := store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Empty(revs, "a deleted revision must not be listed")
}

// ===== Repository deletion =====

func (s *PRRStoreTestSuite) countRows(table, ns string) int {
	var n int
	err := s.pool.QueryRow(s.ctx, "SELECT count(*) FROM "+table+" WHERE k8s_name_space = $1", ns).Scan(&n)
	s.Require().NoError(err)
	return n
}

func (s *PRRStoreTestSuite) TestDeleteRepositoryResources() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	repoKey := s.testRepoKey()
	res := map[string]string{"Kptfile": "v1", "README.md": "# hi"}

	// Everything the repository can hold: a store-only draft, a store-only proposed,
	// an unmarked (git-owned) revision and a nested package.
	mine := []key.PackageRevisionKey{
		s.testPRKey("default", "draft"),
		s.testPRKey("default", "proposed"),
		s.testPRKey("default", "git-owned"),
		{PkgKey: key.PackageKey{RepoKey: repoKey, Path: "a/b", Package: "leaf"}, WorkspaceName: "v1"},
	}
	_, err = store.WriteResourcesAs(s.ctx, mine[0], res, StoreOnlyDraft)
	s.Require().NoError(err)
	_, err = store.WriteResourcesAs(s.ctx, mine[1], res, StoreOnlyProposed)
	s.Require().NoError(err)
	_, err = store.WriteResources(s.ctx, mine[2], res)
	s.Require().NoError(err)
	_, err = store.WriteResourcesAs(s.ctx, mine[3], res, StoreOnlyDraft)
	s.Require().NoError(err)

	// Rows that must survive: another repository in the same namespace, one whose name
	// merely starts with this repository's name, and the same name in another namespace.
	survivors := []key.PackageRevisionKey{
		{PkgKey: key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "default", Name: "other-repo"}, Package: "p"}, WorkspaceName: "v1"},
		{PkgKey: key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "default", Name: "test-repo-2"}, Package: "p"}, WorkspaceName: "v1"},
		s.testPRKey("other-ns", "p"),
	}
	for _, k := range survivors {
		_, err = store.WriteResourcesAs(s.ctx, k, res, StoreOnlyDraft)
		s.Require().NoError(err)
	}

	s.Require().NoError(store.DeleteRepositoryResources(s.ctx, repoKey))

	for _, k := range mine {
		got, err := store.GetResources(s.ctx, k)
		s.Require().NoError(err)
		s.Empty(got, "resources of %s must be removed", k)
		version, err := store.GetResourceVersion(s.ctx, k)
		s.Require().NoError(err)
		s.Empty(version, "version of %s must be removed", k)
	}

	revs, err := store.ListStoreOnly(s.ctx, repoKey)
	s.Require().NoError(err)
	s.Empty(revs, "no store-only marker may outlive the repository")

	for _, k := range survivors {
		got, err := store.GetResources(s.ctx, k)
		s.Require().NoError(err)
		s.Equal(res, got, "resources of %s must be untouched", k)
		version, err := store.GetResourceVersion(s.ctx, k)
		s.Require().NoError(err)
		s.NotEmpty(version, "version of %s must be untouched", k)
	}
	s.Equal(2, s.countRows("prr_resource_versions", "default"))
	s.Equal(4, s.countRows("package_revision_resources", "default"), "2 survivors x 2 resources")
	s.Equal(1, s.countRows("prr_resource_versions", "other-ns"))
}

// A re-registered repository of the same name must start empty, not inherit ghost drafts.
func (s *PRRStoreTestSuite) TestDeleteRepositoryResources_NoGhostDraftsAfterReregistration() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	_, err = store.WriteResourcesAs(s.ctx, s.testPRKey("default", "ghost"), map[string]string{"Kptfile": "v1"}, StoreOnlyDraft)
	s.Require().NoError(err)

	s.Require().NoError(store.DeleteRepositoryResources(s.ctx, s.testRepoKey()))

	revs, err := store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Empty(revs)
}

func (s *PRRStoreTestSuite) TestDeleteRepositoryResources_Idempotent() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	s.NoError(store.DeleteRepositoryResources(s.ctx, s.testRepoKey()), "unknown repository is not an error")

	_, err = store.WriteResources(s.ctx, s.testPRKey("default", "pr1"), map[string]string{"Kptfile": "v1"})
	s.Require().NoError(err)
	s.NoError(store.DeleteRepositoryResources(s.ctx, s.testRepoKey()))
	s.NoError(store.DeleteRepositoryResources(s.ctx, s.testRepoKey()), "second delete is a no-op")
}

// ===== Error paths =====

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func (s *PRRStoreTestSuite) TestListStoreOnly_QueryError() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	_, err = store.ListStoreOnly(cancelledContext(), s.testRepoKey())
	s.Error(err)
}

// A marked row whose name is not a package revision name must not hide the valid ones.
func (s *PRRStoreTestSuite) TestListStoreOnly_SkipsUnparseableName() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	_, err = s.pool.Exec(s.ctx,
		`INSERT INTO prr_resource_versions (k8s_name_space, k8s_name, resource_version, store_only_lifecycle)
		 VALUES ('default', 'not-a-package-revision-name', 'v', 'Draft')`)
	s.Require().NoError(err)
	_, err = store.WriteResourcesAs(s.ctx, s.testPRKey("default", "valid"), map[string]string{"Kptfile": "v1"}, StoreOnlyDraft)
	s.Require().NoError(err)

	revs, err := store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Require().Len(revs, 1)
	s.Equal("valid", revs[0].Key.PkgKey.Package)
}

func (s *PRRStoreTestSuite) TestDeleteRepositoryResources_CancelledContext() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	s.Error(store.DeleteRepositoryResources(cancelledContext(), s.testRepoKey()))
}

// breakTable renames a table so statements against it fail, and restores it on cleanup.
func (s *PRRStoreTestSuite) breakTable(table string) {
	_, err := s.pool.Exec(s.ctx, "ALTER TABLE "+table+" RENAME TO "+table+"_broken")
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		_, err := s.pool.Exec(context.Background(), "ALTER TABLE "+table+"_broken RENAME TO "+table)
		s.NoError(err)
	})
}

// Repositories of a v1alpha1-only install are deleted through here as well, and that
// install never applies the PRRStore schema. Deleting one must not fail.
func (s *PRRStoreTestSuite) TestDeleteRepositoryResources_SchemaNotInstalledIsNotAnError() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	s.breakTable("package_revision_resources")
	s.breakTable("prr_resource_versions")

	s.NoError(store.DeleteRepositoryResources(s.ctx, s.testRepoKey()))
}

func (s *PRRStoreTestSuite) TestDeleteRepositoryResources_ResourcesDeleteError() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	s.breakTable("package_revision_resources")

	err = store.DeleteRepositoryResources(s.ctx, s.testRepoKey())
	s.Require().Error(err)
	s.Contains(err.Error(), "delete resources for repo")
}

// The two deletes are one transaction: a failure on the second must undo the first.
func (s *PRRStoreTestSuite) TestDeleteRepositoryResources_VersionsDeleteErrorRollsBack() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	prKey := s.testPRKey("default", "pr1")
	_, err = store.WriteResourcesAs(s.ctx, prKey, map[string]string{"Kptfile": "v1"}, StoreOnlyDraft)
	s.Require().NoError(err)

	s.breakTable("prr_resource_versions")

	err = store.DeleteRepositoryResources(s.ctx, s.testRepoKey())
	s.Require().Error(err)
	s.Contains(err.Error(), "delete versions for repo")

	got, gerr := store.GetResources(s.ctx, prKey)
	s.Require().NoError(gerr)
	s.Equal(map[string]string{"Kptfile": "v1"}, got, "resources deleted before the failure must be rolled back")
}

// ===== Failed write releases its connection =====

// A write that fails part-way (a NUL byte is not valid in a Postgres text value) must
// roll back, or its pooled connection stays checked out. On a small pool a few such
// failures would otherwise leave every later call waiting for a connection.
func (s *PRRStoreTestSuite) TestFailedWriteDoesNotLeakConnections() {
	cfg, err := pgxpool.ParseConfig(s.url)
	s.Require().NoError(err)
	cfg.MaxConns = 2
	small, err := pgxpool.NewWithConfig(s.ctx, cfg)
	s.Require().NoError(err)
	s.T().Cleanup(small.Close)
	store, err := NewSQLStore(small)
	s.Require().NoError(err)

	prKey := s.testPRKey("default", "leak")
	for i := 0; i < 6; i++ {
		_, werr := store.WriteResources(s.ctx, prKey, map[string]string{"bad.yaml": "nul\x00byte"})
		s.Require().Error(werr)
		_, _, cerr := store.WriteResourcesIfUnchanged(s.ctx, prKey, map[string]string{"bad.yaml": "nul\x00byte"}, "")
		s.Require().Error(cerr)
	}

	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	_, err = store.WriteResources(ctx, prKey, map[string]string{"Kptfile": "ok"})
	s.Require().NoError(err, "the pool must still have connections after failed writes")
	got, err := store.GetResources(ctx, prKey)
	s.Require().NoError(err)
	s.Equal(map[string]string{"Kptfile": "ok"}, got, "a failed write must leave the previous content intact")
}

// ===== Per-revision serialisation =====

// Two concurrent writes of different file sets must leave one set whole, never a mix.
func (s *PRRStoreTestSuite) TestConcurrentWritesNeverLeaveAMixOfFiles() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)
	prKey := s.testPRKey("default", "mix")

	const writers = 24
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, werr := store.WriteResources(s.ctx, prKey, map[string]string{
				fmt.Sprintf("a-%d.yaml", i): "a",
				fmt.Sprintf("b-%d.yaml", i): "b",
			})
			s.NoError(werr)
		}(i)
	}
	wg.Wait()

	got, err := store.GetResources(s.ctx, prKey)
	s.Require().NoError(err)
	s.Len(got, 2, "exactly one writer's two files remain, not a union: %v", got)
}

// Of several first writes against a missing version row, exactly one may win.
func (s *PRRStoreTestSuite) TestConcurrentFirstWritesOnlyOneWins() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)
	prKey := s.testPRKey("default", "first")

	const writers = 24
	var wg sync.WaitGroup
	var won, conflicted atomic.Int32
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, conflict, werr := store.WriteResourcesIfUnchanged(s.ctx, prKey, map[string]string{fmt.Sprintf("f-%d.yaml", i): "x"}, "")
			s.NoError(werr)
			if conflict {
				conflicted.Add(1)
			} else {
				won.Add(1)
			}
		}(i)
	}
	wg.Wait()

	s.Equal(int32(1), won.Load(), "only one first write may succeed")
	s.Equal(int32(writers-1), conflicted.Load())
}

// Pushes and renders hit the same revision from both directions; with one lock order
// they finish instead of deadlocking.
func (s *PRRStoreTestSuite) TestPushAndRenderOnOneRevisionDoNotDeadlock() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)
	prKey := s.testPRKey("default", "lockorder")
	_, err = store.WriteResources(s.ctx, prKey, map[string]string{"Kptfile": "v0"})
	s.Require().NoError(err)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_, werr := store.WriteResources(ctx, prKey, map[string]string{"Kptfile": fmt.Sprintf("push-%d", i)})
			s.NoError(werr)
		}(i)
		go func(i int) {
			defer wg.Done()
			version, verr := store.GetResourceVersion(ctx, prKey)
			s.NoError(verr)
			_, _, werr := store.WriteResourcesIfUnchanged(ctx, prKey, map[string]string{"Kptfile": fmt.Sprintf("render-%d", i)}, version)
			s.NoError(werr)
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		s.Fail("pushes and renders did not finish: deadlock")
	}
}

// ===== Conditional marker write and marker-only update =====

func (s *PRRStoreTestSuite) TestWriteResourcesAsIfUnchanged() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)
	prKey := s.testPRKey("default", "cas")

	// First write: no row yet, so "" is the expected version. It sets the marker.
	v1, conflict, err := store.WriteResourcesAsIfUnchanged(s.ctx, prKey, map[string]string{"Kptfile": "v1"}, StoreOnlyDraft, "")
	s.Require().NoError(err)
	s.False(conflict)
	revs, err := store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Require().Len(revs, 1)
	s.Equal(StoreOnlyDraft, revs[0].Lifecycle)

	// A push moves the version on; a write based on the old one writes nothing.
	_, err = store.WriteResources(s.ctx, prKey, map[string]string{"Kptfile": "pushed"})
	s.Require().NoError(err)
	_, conflict, err = store.WriteResourcesAsIfUnchanged(s.ctx, prKey, map[string]string{"Kptfile": "stale"}, StoreOnlyProposed, v1)
	s.Require().NoError(err)
	s.True(conflict)
	got, err := store.GetResources(s.ctx, prKey)
	s.Require().NoError(err)
	s.Equal(map[string]string{"Kptfile": "pushed"}, got, "the newer content is kept")
	revs, err = store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Equal(StoreOnlyDraft, revs[0].Lifecycle, "a conflicting write does not change the marker")

	// Based on the current version it writes, and clears the marker when asked.
	current, err := store.GetResourceVersion(s.ctx, prKey)
	s.Require().NoError(err)
	_, conflict, err = store.WriteResourcesAsIfUnchanged(s.ctx, prKey, map[string]string{"Kptfile": "final"}, "", current)
	s.Require().NoError(err)
	s.False(conflict)
	revs, err = store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Empty(revs)

	_, _, err = store.WriteResourcesAsIfUnchanged(s.ctx, prKey, nil, "Published", "")
	s.Error(err, "only Draft or Proposed are valid markers")
}

func (s *PRRStoreTestSuite) TestSetStoreOnlyLifecycle() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)
	prKey := s.testPRKey("default", "marker")
	version, err := store.WriteResources(s.ctx, prKey, map[string]string{"Kptfile": "k"})
	s.Require().NoError(err)

	s.Require().NoError(store.SetStoreOnlyLifecycle(s.ctx, prKey, StoreOnlyProposed))
	revs, err := store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Require().Len(revs, 1)
	s.Equal(StoreOnlyProposed, revs[0].Lifecycle)

	after, err := store.GetResourceVersion(s.ctx, prKey)
	s.Require().NoError(err)
	s.Equal(version, after, "the marker changes without a new version")
	got, err := store.GetResources(s.ctx, prKey)
	s.Require().NoError(err)
	s.Equal(map[string]string{"Kptfile": "k"}, got, "content is untouched")

	s.Require().NoError(store.SetStoreOnlyLifecycle(s.ctx, prKey, ""))
	revs, err = store.ListStoreOnly(s.ctx, s.testRepoKey())
	s.Require().NoError(err)
	s.Empty(revs)

	s.Error(store.SetStoreOnlyLifecycle(s.ctx, prKey, "Published"))
	s.NoError(store.SetStoreOnlyLifecycle(s.ctx, s.testPRKey("default", "missing"), StoreOnlyDraft), "a missing revision is not an error")
	s.Error(store.SetStoreOnlyLifecycle(cancelledContext(), prKey, StoreOnlyDraft))
}

// ===== Schema check and upgrade =====

func (s *PRRStoreTestSuite) TestCheckSchema() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)
	s.NoError(store.CheckSchema(s.ctx))

	s.Error(store.CheckSchema(cancelledContext()))

	s.Run("a missing table", func() {
		s.breakTable("package_revision_resources")
		err := store.CheckSchema(s.ctx)
		s.Require().Error(err)
		s.Contains(err.Error(), "porch-db.sql", "the message says how to fix it")
	})
}

// A table created by an earlier build of the schema has no marker column; that is reported
// when a repository is opened rather than as a column error on the first write.
func (s *PRRStoreTestSuite) TestCheckSchemaReportsMissingMarkerColumn() {
	store, err := NewSQLStore(s.pool)
	s.Require().NoError(err)

	_, err = s.pool.Exec(s.ctx, `ALTER TABLE prr_resource_versions DROP COLUMN store_only_lifecycle`)
	s.Require().NoError(err)
	s.T().Cleanup(func() { // leave later tests a complete table
		_, err := s.pool.Exec(context.Background(), `
			ALTER TABLE prr_resource_versions
			    ADD COLUMN store_only_lifecycle TEXT CHECK (store_only_lifecycle IN ('Draft', 'Proposed'));
			CREATE INDEX IF NOT EXISTS idx_prr_resource_versions_store_only
			    ON prr_resource_versions (k8s_name_space) WHERE store_only_lifecycle IS NOT NULL`)
		s.NoError(err)
	})

	err = store.CheckSchema(s.ctx)
	s.Require().Error(err)
	s.Contains(err.Error(), "porch-db.sql")
}
