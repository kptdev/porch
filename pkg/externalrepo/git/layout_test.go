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

package git

import (
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kptdev/porch/api/porch/key"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/require"
)

func TestZeroLayoutIsBare(t *testing.T) {
	dir := t.TempDir()
	repo, err := initEmptyRepository(filepath.Join(dir, "bare"), GitRepositoryOptions{})
	require.NoError(t, err)
	require.NotNil(t, repo)
	_, err = os.Stat(filepath.Join(dir, "bare", "HEAD"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "bare", ".git"))
	require.True(t, os.IsNotExist(err))
}

func TestFullLayoutHasWorktree(t *testing.T) {
	dir := t.TempDir()
	repo, err := initEmptyRepository(filepath.Join(dir, "full"), GitRepositoryOptions{Layout: FullRepositoryLayout})
	require.NoError(t, err)
	require.NotNil(t, repo)
	_, err = os.Stat(filepath.Join(dir, "full", ".git", "HEAD"))
	require.NoError(t, err)
}

func TestFullLayoutReadsCheckedOutFiles(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "full")
	_, err := initEmptyRepository(cache, GitRepositoryOptions{Layout: FullRepositoryLayout})
	require.NoError(t, err)
	pkgDir := filepath.Join(cache, "pkg")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "Kptfile"), []byte("kind: Kptfile\n"), 0o644))

	pr := &gitPackageRevision{
		repo: &gitRepository{layout: FullRepositoryLayout, cacheDir: cache},
		prKey: key.PackageRevisionKey{
			PkgKey: key.PackageKey{Package: "pkg"},
		},
	}
	resources, ok := pr.readCheckedOutResources()
	require.True(t, ok)
	require.Contains(t, resources, "Kptfile")

	pr.repo.layout = BareRepositoryLayout
	_, ok = pr.readCheckedOutResources()
	require.False(t, ok)
}

func TestDisableRemoteFetchDoesNotDial(t *testing.T) {
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
	repoDir := filepath.Join(cache, "repo")
	ctx := t.Context()
	repo, err := OpenRepository(ctx, "repo", "ns", &configapi.GitRepository{
		Repo:   "http://" + ln.Addr().String() + "/repo.git",
		Branch: "main",
	}, false, cache, GitRepositoryOptions{
		MainBranchStrategy: SkipVerification,
		ExternalRepoOptions: externalrepotypes.ExternalRepoOptions{
			DisableRemoteFetchOnRead:   true,
			AbsoluteCacheDir:           repoDir,
			SkipRemotePush:             true,
			RetainCacheOnClose:         true,
			RepoOperationRetryAttempts: 1,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = repo.Close(ctx) })

	_, err = repo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	require.NoError(t, err)
	_, err = repo.Version(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(0), dials.Load())
}
