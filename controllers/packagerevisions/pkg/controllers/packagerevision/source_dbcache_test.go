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

package packagerevision

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/kptdev/porch/api/porch/key"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/contentcache"
	"github.com/kptdev/porch/pkg/cache/dbcache"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockcachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const sourceTestDBDataSource = "postgresql://porch:porch@localhost:55439/porch"

// newEmptyDBContentCache returns a content cache over a DB cache that has no
// repositories open, the state a source step sees while the repository
// controller cannot reach the database. In short mode it returns an empty
// mocked cache instead, since that path reads only the repository list and
// embedded PostgreSQL cannot start as root.
func newEmptyDBContentCache(t *testing.T) repository.ContentCache {
	t.Helper()
	if testing.Short() {
		cache := mockcachetypes.NewMockCache(t)
		cache.EXPECT().GetRepositories().Return(nil)
		return contentcache.NewContentCache(cache)
	}
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
		schemaDir = "../../../../.."
	}
	schemaSQL, err := os.ReadFile(filepath.Join(schemaDir, "api/sql/porch-db.sql"))
	require.NoError(t, err)
	db, err := sql.Open("pgx", sourceTestDBDataSource)
	require.NoError(t, err)
	_, err = db.Exec(string(schemaSQL))
	require.NoError(t, err)
	require.NoError(t, db.Close())

	cache, err := (&dbcache.DBCacheFactory{}).NewCache(t.Context(), cachetypes.CacheOptions{
		DBCacheOptions: cachetypes.DBCacheOptions{Driver: "pgx", DataSource: sourceTestDBDataSource},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbcache.CloseDB(context.Background()) })
	return contentcache.NewContentCache(cache)
}

func publishedUpstream(name string) porchv1alpha2.PackageRevision {
	return porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "upstream-pkg",
			RepositoryName: "upstream-repo",
			WorkspaceName:  name,
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}
}

type sourceStepCase struct {
	spec      porchv1alpha2.PackageRevisionSpec
	reconcile func(*PackageRevisionReconciler, context.Context, *porchv1alpha2.PackageRevision) (*ctrl.Result, error)
}

func sourceStepCases() map[string]sourceStepCase {
	upstreamRef := &porchv1alpha2.PackageRevisionRef{Name: "upstream.v1"}
	return map[string]sourceStepCase{
		"clone": {
			spec: porchv1alpha2.PackageRevisionSpec{Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{UpstreamRef: upstreamRef}}},
		},
		"copy": {
			spec: porchv1alpha2.PackageRevisionSpec{
				PackageName: "upstream-pkg", RepositoryName: "upstream-repo",
				Source: &porchv1alpha2.PackageSource{CopyFrom: upstreamRef}},
		},
		"upgrade": {
			spec: porchv1alpha2.PackageRevisionSpec{Source: &porchv1alpha2.PackageSource{Upgrade: &porchv1alpha2.PackageUpgradeSpec{
				OldUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.v1"},
				NewUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.v2"},
				CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "current.v1"},
			}}},
		},
		"subpackage clone": {
			spec: porchv1alpha2.PackageRevisionSpec{SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "sub",
				CloneFrom:     &porchv1alpha2.UpstreamPackage{UpstreamRef: upstreamRef}}},
			reconcile: (*PackageRevisionReconciler).subpackageStep,
		},
	}
}

// runSourceStep reconciles the source step of a draft built from tc against
// cache, and returns the Ready condition it patched and the result.
func runSourceStep(t *testing.T, cache repository.ContentCache, tc sourceStepCase) (metav1.Condition, *ctrl.Result) {
	t.Helper()
	upstreams := map[string]porchv1alpha2.PackageRevision{}
	for _, name := range []string{"upstream.v1", "upstream.v2", "current.v1"} {
		upstreams[name] = publishedUpstream(name)
	}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("my-repo.my-pkg.ws-1", "default", "my-repo"),
		Spec:       tc.spec,
	}
	if pr.Spec.PackageName == "" {
		pr.Spec.PackageName, pr.Spec.RepositoryName = "my-pkg", "my-repo"
	}
	pr.Spec.WorkspaceName = "ws-1"
	pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDraft

	mockClient := mockclient.NewMockClient(t)
	if tc.reconcile == nil {
		expectRepoGet(mockClient, "default", pr.Spec.RepositoryName, true)
	}
	mockClient.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		RunAndReturn(func(_ context.Context, k client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			upstream, ok := upstreams[k.Name]
			if !ok {
				return apierrors.NewNotFound(schema.GroupResource{}, k.Name)
			}
			*obj.(*porchv1alpha2.PackageRevision) = upstream
			return nil
		}).Maybe()

	var ready metav1.Condition
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			for _, c := range obj.(*porchv1alpha2.PackageRevision).Status.Conditions {
				if c.Type == porchv1alpha2.ConditionReady {
					ready = c
				}
			}
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{Client: mockClient, ContentCache: cache, TransientErrorRetryDeadline: defaultTransientErrorRetryDeadline}
	reconcile := tc.reconcile
	if reconcile == nil {
		reconcile = (*PackageRevisionReconciler).sourceStep
	}
	result, _ := reconcile(r, t.Context(), pr)
	return ready, result
}

// TestSourceRequeuesWhileDBCacheMissesRepository covers kptdev/porch#1373: a
// source step whose repository is missing from the DB cache must requeue in
// the transient-retry state, as the content read path does, not fail.
func TestSourceRequeuesWhileDBCacheMissesRepository(t *testing.T) {
	cache := newEmptyDBContentCache(t)
	for name, tc := range sourceStepCases() {
		t.Run(name, func(t *testing.T) {
			ready, result := runSourceStep(t, cache, tc)

			assert.Contains(t, ready.Message, "not found")
			assert.Equal(t, transientRetryReasonInternal, ready.Reason, "a repository missing from the cache must stay in transient retry")
			require.NotNil(t, result)
			assert.Equal(t, reconciledRetryInterval, result.RequeueAfter)
		})
	}
}

// TestSourceFailsWhenCacheMissesPackage checks that a source step whose
// repository is in the cache but whose package is not fails at once.
func TestSourceFailsWhenCacheMissesPackage(t *testing.T) {
	for name, tc := range sourceStepCases() {
		t.Run(name, func(t *testing.T) {
			repo := mockrepository.NewMockRepository(t)
			repo.EXPECT().ListPackageRevisions(mock.Anything, mock.Anything).Return(nil, nil)
			cache := mockcachetypes.NewMockCache(t)
			cache.EXPECT().GetRepositories().Return([]*configapi.Repository{
				gitRepositorySpec("my-repo"), gitRepositorySpec("upstream-repo"),
			})
			cache.EXPECT().GetRepository(mock.Anything).Return(repo)

			ready, result := runSourceStep(t, contentcache.NewContentCache(cache), tc)

			assert.Contains(t, ready.Message, "not found")
			assert.Equal(t, porchv1alpha2.ReasonFailed, ready.Reason, "a package missing from a cached repository must fail")
			assert.Nil(t, result)
		})
	}
}

func gitRepositorySpec(name string) *configapi.Repository {
	return &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeGit,
			Git:  &configapi.GitRepository{Repo: "https://example.com/" + name + ".git", Branch: "main"},
		},
	}
}

func (r *PackageRevisionReconciler) sourceStep(ctx context.Context, pr *porchv1alpha2.PackageRevision) (*ctrl.Result, error) {
	return r.reconcileSource(ctx, pr, key.RepositoryKey{Namespace: pr.Namespace, Name: pr.Spec.RepositoryName})
}

func (r *PackageRevisionReconciler) subpackageStep(ctx context.Context, pr *porchv1alpha2.PackageRevision) (*ctrl.Result, error) {
	return r.reconcileSubpackageOperation(ctx, pr, key.RepositoryKey{Namespace: pr.Namespace, Name: pr.Spec.RepositoryName})
}
