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

package lgcache

import (
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/lgcache/meta"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo/git"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestListDoesNotContactRemote(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	var dials atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			conn.Close()
		}
	}()

	cache := t.TempDir()
	repoDir := filepath.Join(cache, "repos", "ns", "repo")
	ctx := t.Context()
	local, err := git.OpenRepository(ctx, "repo", "ns", &configapi.GitRepository{
		Repo:   "http://" + ln.Addr().String() + "/repo.git",
		Branch: "main",
	}, false, cache, git.GitRepositoryOptions{
		MainBranchStrategy: git.SkipVerification,
		Layout:             git.FullRepositoryLayout,
		ExternalRepoOptions: externalrepotypes.ExternalRepoOptions{
			DisableRemoteFetchOnRead:   true,
			AbsoluteCacheDir:           repoDir,
			SkipRemotePush:             true,
			RetainCacheOnClose:         true,
			RepoOperationRetryAttempts: 1,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = local.Close(ctx) })

	scheme := runtime.NewScheme()
	require.NoError(t, porchv1alpha2.AddToScheme(scheme))
	crd := &porchv1alpha2.PackageRevision{}
	crd.Name = "repo.pkg.ws"
	crd.Namespace = "ns"
	crd.Labels = map[string]string{porchv1alpha2.RepositoryLabelKey: "repo"}
	crd.Spec.PackageName = "pkg"
	crd.Spec.RepositoryName = "repo"
	crd.Spec.WorkspaceName = "ws"
	crd.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDraft
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(crd).Build()

	repo := &lgRepository{
		repoKey:     key.RepositoryKey{Namespace: "ns", Name: "repo"},
		spec:        &configapi.Repository{},
		pvcRoot:     cache,
		lockTimeout: time.Second,
		local:       local,
		meta:        &meta.Store{Client: cl},
	}
	listed, err := repo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	require.NoError(t, err)
	require.NotEmpty(t, listed)
	_, err = repo.Version(ctx)
	require.NoError(t, err)
	require.Nil(t, repo.remote)

	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(0), dials.Load())
}

func TestCloseDraftStaysOnPVC(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	var dials atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			conn.Close()
		}
	}()

	cache := t.TempDir()
	repoDir := filepath.Join(cache, "repos", "ns", "repo")
	ctx := t.Context()
	local, err := git.OpenRepository(ctx, "repo", "ns", &configapi.GitRepository{
		Repo:   "http://" + ln.Addr().String() + "/repo.git",
		Branch: "main",
	}, false, cache, git.GitRepositoryOptions{
		MainBranchStrategy: git.SkipVerification,
		Layout:             git.FullRepositoryLayout,
		ExternalRepoOptions: externalrepotypes.ExternalRepoOptions{
			DisableRemoteFetchOnRead:   true,
			AbsoluteCacheDir:           repoDir,
			SkipRemotePush:             true,
			RetainCacheOnClose:         true,
			RepoOperationRetryAttempts: 1,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = local.Close(ctx) })

	repo := &lgRepository{
		repoKey: key.RepositoryKey{Namespace: "ns", Name: "repo"},
		spec: &configapi.Repository{
			Spec: configapi.RepositorySpec{Git: &configapi.GitRepository{
				Repo:   "http://" + ln.Addr().String() + "/repo.git",
				Branch: "main",
			}},
		},
		pvcRoot:     cache,
		lockTimeout: time.Second,
		pushDrafts:  true,
		local:       local,
	}

	draft, err := repo.CreatePackageRevisionDraft(ctx, &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			PackageName:   "pkg",
			WorkspaceName: "v1",
			Lifecycle:     porchapi.PackageRevisionLifecycleDraft,
		},
	})
	require.NoError(t, err)
	require.NoError(t, draft.UpdateResources(ctx, &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: pkg\n",
		}},
	}, &porchapi.Task{Type: porchapi.TaskTypeInit}))

	closed, err := repo.ClosePackageRevisionDraft(ctx, draft, 0)
	require.NoError(t, err)
	require.Equal(t, porchapi.PackageRevisionLifecycleDraft, closed.Lifecycle(ctx))
	require.Nil(t, repo.remote)

	listed, err := repo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	require.NoError(t, err)
	require.NotEmpty(t, listed)

	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(0), dials.Load())
}

func TestListOpensRepositoryThatIsNotCached(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	var dials atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			conn.Close()
		}
	}()

	root := t.TempDir()
	repoDir := filepath.Join(root, "repos", "ns", "repo")
	ctx := t.Context()
	local, err := git.OpenRepository(ctx, "repo", "ns", &configapi.GitRepository{
		Repo:   "http://" + ln.Addr().String() + "/repo.git",
		Branch: "main",
	}, false, root, git.GitRepositoryOptions{
		MainBranchStrategy: git.SkipVerification,
		Layout:             git.FullRepositoryLayout,
		ExternalRepoOptions: externalrepotypes.ExternalRepoOptions{
			DisableRemoteFetchOnRead:   true,
			AbsoluteCacheDir:           repoDir,
			SkipRemotePush:             true,
			RetainCacheOnClose:         true,
			RepoOperationRetryAttempts: 1,
		},
	})
	require.NoError(t, err)
	require.NoError(t, local.Close(ctx))

	scheme := runtime.NewScheme()
	require.NoError(t, configapi.AddToScheme(scheme))
	require.NoError(t, porchv1alpha2.AddToScheme(scheme))
	repoSpec := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				Repo:   "http://" + ln.Addr().String() + "/repo.git",
				Branch: "main",
			},
		},
	}
	crd := &porchv1alpha2.PackageRevision{}
	crd.Name = "repo.pkg.ws"
	crd.Namespace = "ns"
	crd.Labels = map[string]string{porchv1alpha2.RepositoryLabelKey: "repo"}
	crd.Spec.PackageName = "pkg"
	crd.Spec.RepositoryName = "repo"
	crd.Spec.WorkspaceName = "ws"
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repoSpec, crd).Build()

	cache := &lgCache{
		options: cachetypes.CacheOptions{
			CoreClient: cl,
			LGCacheOptions: cachetypes.LGCacheOptions{
				PVCRoot:         root,
				RepoLockTimeout: time.Second,
			},
		},
		meta: &meta.Store{Client: cl},
	}
	listed, err := cache.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
		Key: key.PackageRevisionKey{PkgKey: key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "ns", Name: "repo"}}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, listed)
	require.NotEmpty(t, cache.GetRepositories())

	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(0), dials.Load())
}

func TestFactoryRequiresV1Alpha2AndWritableRoot(t *testing.T) {
	factory := &LGCacheFactory{}
	_, err := factory.NewCache(t.Context(), cacheOptionsForTest(t, false))
	require.Error(t, err)

	cache, err := factory.NewCache(t.Context(), cacheOptionsForTest(t, true))
	require.NoError(t, err)
	require.NotNil(t, cache)
	require.Empty(t, cache.GetRepositories())
}

func cacheOptionsForTest(t *testing.T, v1 bool) cachetypes.CacheOptions {
	t.Helper()
	return cachetypes.CacheOptions{
		CacheType: cachetypes.LGCacheType,
		ExternalRepoOptions: externalrepotypes.ExternalRepoOptions{
			LocalDirectory: t.TempDir(),
		},
		LGCacheOptions: cachetypes.LGCacheOptions{RequireV1Alpha2: v1},
	}
}
