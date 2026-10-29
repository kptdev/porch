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

package dbcache

import (
	"context"
	"fmt"
	"testing"

	"github.com/kptdev/porch/api/porch/key"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository/prrbackend"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func keyForTest(namespace, name string) key.RepositoryKey {
	return key.RepositoryKey{Namespace: namespace, Name: name}
}

func repoWithAnnotation(annotations map[string]string) *configapi.Repository {
	return &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-repo",
			Namespace:   "default",
			Annotations: annotations,
		},
	}
}

func TestShouldUsePRRRepository(t *testing.T) {
	tests := []struct {
		name string
		spec *configapi.Repository
		want bool
	}{
		{
			name: "nil spec",
			spec: nil,
			want: false,
		},
		{
			name: "nil annotations",
			spec: &configapi.Repository{},
			want: false,
		},
		{
			name: "empty annotations",
			spec: repoWithAnnotation(map[string]string{}),
			want: false,
		},
		{
			name: "unrelated annotation",
			spec: repoWithAnnotation(map[string]string{"other": "value"}),
			want: false,
		},
		{
			name: "migration annotation wrong value",
			spec: repoWithAnnotation(map[string]string{configapi.AnnotationKeyV1Alpha2Migration: "false"}),
			want: false,
		},
		{
			name: "migration annotation enabled",
			spec: repoWithAnnotation(map[string]string{configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled}),
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, shouldUsePRRRepository(tc.spec))
		})
	}
}

func TestRepoHandleTypeMismatch(t *testing.T) {
	dbRepo := &dbRepository{}
	prrRepo := &prrbackend.Repository{}

	tests := []struct {
		name              string
		repo              repository.Repository
		wantPRRRepository bool
		wantMismatch      bool
	}{
		{
			name:              "dbRepository wanting v1alpha1 - match",
			repo:              dbRepo,
			wantPRRRepository: false,
			wantMismatch:      false,
		},
		{
			name:              "dbRepository wanting v1alpha2 - mismatch",
			repo:              dbRepo,
			wantPRRRepository: true,
			wantMismatch:      true,
		},
		{
			name:              "PRRRepository wanting v1alpha2 - match",
			repo:              prrRepo,
			wantPRRRepository: true,
			wantMismatch:      false,
		},
		{
			name:              "PRRRepository wanting v1alpha1 - mismatch",
			repo:              prrRepo,
			wantPRRRepository: false,
			wantMismatch:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantMismatch, repoHandleTypeMismatch(tc.repo, tc.wantPRRRepository))
		})
	}
}

func TestIsTransientCacheOpenError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "unrelated error", err: fmt.Errorf("connection refused"), want: false},
		{name: "repository does not exist", err: fmt.Errorf("git: repository does not exist"), want: true},
		{name: "corrupted cache was removed", err: fmt.Errorf("corrupted cache was removed, retrying"), want: true},
		{name: "wrapped transient error", err: fmt.Errorf("open failed: %w", fmt.Errorf("repository does not exist")), want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isTransientCacheOpenError(tc.err))
		})
	}
}

func TestUpdateRepositoryByType_PRRRepositoryIsNoOp(t *testing.T) {
	// A PRRRepository must not trigger a SQL metadata update; updateRepositoryByType
	// returns nil without touching the DB.
	err := updateRepositoryByType(context.Background(), &prrbackend.Repository{})
	require.NoError(t, err)
}

func TestCloseRepositoryByType_DBRepositoryWithNilHandles(t *testing.T) {
	// A dbRepository with nil repositorySync and nil externalRepo must close cleanly
	// via the nil guards (no panic, no error).
	err := closeRepositoryByType(context.Background(), &dbRepository{})
	require.NoError(t, err)
}

func TestExtractRepositorySpec_DBRepository(t *testing.T) {
	spec := repoWithAnnotation(nil)
	dbRepo := &dbRepository{spec: spec}

	got := extractRepositorySpec(dbRepo)
	require.NotNil(t, got)
	require.Same(t, spec, got, "extractRepositorySpec must return the dbRepository's spec pointer")
}

func TestExtractRepositorySpec_PRRRepository(t *testing.T) {
	// A zero-value PRRRepository has a zero meta; extractRepositorySpec returns a
	// pointer to a copy of it (Meta() returns by value), so it must be non-nil.
	got := extractRepositorySpec(&prrbackend.Repository{})
	require.NotNil(t, got)
}

func TestEvictOnTypeChange_NoCachedHandle(t *testing.T) {
	c := &dbCache{}
	repoKey := keyForTest("default", "absent")

	// No handle cached for this key: must be a no-op (no panic).
	c.evictOnTypeChange(context.Background(), repoKey, true)

	_, ok := c.repositories.Load(repoKey)
	require.False(t, ok, "map must remain empty")
}

func TestEvictOnTypeChange_MatchingTypeNotEvicted(t *testing.T) {
	c := &dbCache{}
	repoKey := keyForTest("default", "match")

	// Seed a dbRepository handle via LoadOrCreate.
	seeded := &dbRepository{}
	_, err := c.repositories.LoadOrCreate(repoKey, func() (repository.Repository, error) {
		return seeded, nil
	})
	require.NoError(t, err)

	// wantPRRRepository=false matches the dbRepository type: must NOT evict.
	c.evictOnTypeChange(context.Background(), repoKey, false)

	got, ok := c.repositories.Load(repoKey)
	require.True(t, ok, "matching-type handle must remain cached")
	require.Same(t, seeded, got)
}

func TestEvictOnTypeChange_MismatchEvicts(t *testing.T) {
	c := &dbCache{}
	repoKey := keyForTest("default", "mismatch")

	// Seed a dbRepository with nil handles (closeRepositoryByType's nil guards make
	// its Close a safe no-op).
	seeded := &dbRepository{}
	_, err := c.repositories.LoadOrCreate(repoKey, func() (repository.Repository, error) {
		return seeded, nil
	})
	require.NoError(t, err)

	// wantPRRRepository=true mismatches the dbRepository type: must evict.
	c.evictOnTypeChange(context.Background(), repoKey, true)

	_, ok := c.repositories.Load(repoKey)
	require.False(t, ok, "mismatched-type handle must be evicted from the map")
}
