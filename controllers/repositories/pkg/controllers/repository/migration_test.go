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

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	cachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
)

func repoWithAnnotation(value string) *configapi.Repository {
	repo := &configapi.Repository{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"}}
	if value != "" {
		repo.Annotations = map[string]string{configapi.AnnotationKeyV1Alpha2Migration: value}
	}
	return repo
}

func TestV1Alpha2Unavailable(t *testing.T) {
	enabled := configapi.AnnotationValueMigrationEnabled

	tests := []struct {
		name       string
		repo       *configapi.Repository
		createCRDs bool
		cacheType  string
		wantErr    string // substring; empty means no error
	}{
		// Repositories that are not annotated must never be affected, whatever the config.
		{name: "nil repository", repo: nil},
		{name: "no annotation, nothing enabled", repo: repoWithAnnotation(""), cacheType: "CR"},
		{name: "annotation not enabled", repo: repoWithAnnotation("false"), cacheType: "CR"},
		{name: "no annotation, v1alpha2 enabled", repo: repoWithAnnotation(""), createCRDs: true, cacheType: "DB"},

		// Annotated: both the flag and the DB cache are needed.
		{name: "annotated, flag off", repo: repoWithAnnotation(enabled), cacheType: "DB", wantErr: "create-v1alpha2-rpkg is false"},
		{name: "annotated, flag off and CR cache reports the flag", repo: repoWithAnnotation(enabled), cacheType: "CR", wantErr: "create-v1alpha2-rpkg is false"},
		{name: "annotated, CR cache", repo: repoWithAnnotation(enabled), createCRDs: true, cacheType: "CR", wantErr: `cache-type=DB, but it is "CR"`},
		{name: "annotated, cache type unset", repo: repoWithAnnotation(enabled), createCRDs: true, wantErr: "cache-type=DB"},
		{name: "annotated, fully configured", repo: repoWithAnnotation(enabled), createCRDs: true, cacheType: "DB"},
		{name: "annotated, cache type is case insensitive", repo: repoWithAnnotation(enabled), createCRDs: true, cacheType: "db"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{CreateV1Alpha2Rpkg: tt.createCRDs, cacheType: tt.cacheType}

			err := r.v1alpha2Unavailable(tt.repo)

			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, errV1Alpha2Unavailable)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Contains(t, err.Error(), configapi.AnnotationKeyV1Alpha2Migration, "the message names the annotation to remove")
		})
	}
}

// A misconfigured v1alpha2 repository must fail before anything is opened or written.
func TestSyncRepository_V1Alpha2UnavailableDoesNotTouchTheCache(t *testing.T) {
	mockCache := cachetypes.NewMockCache(t) // no expectations: any call fails the test
	r := &RepositoryReconciler{Cache: mockCache, cacheType: "DB"}

	count, hash, err := r.syncRepository(context.Background(), repoWithAnnotation(configapi.AnnotationValueMigrationEnabled))

	require.ErrorIs(t, err, errV1Alpha2Unavailable)
	assert.Zero(t, count)
	assert.Empty(t, hash)
}

// The failure is reported as an error status with a long retry, not a tight loop.
func TestHandleSync_V1Alpha2UnavailableIsReportedWithLongRetry(t *testing.T) {
	r := &RepositoryReconciler{cacheType: "DB"}
	s := &Sync{reconciler: r}

	before := time.Now()
	result := s.HandleSync(context.Background(), repoWithAnnotation(configapi.AnnotationValueMigrationEnabled))

	assert.Equal(t, RepositoryStatusError, result.Status)
	require.ErrorIs(t, result.Error, errV1Alpha2Unavailable)
	require.NotNil(t, result.NextSyncTime)
	assert.GreaterOrEqual(t, result.NextSyncTime.Sub(before), 10*time.Minute)
}
