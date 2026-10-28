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
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/require"
)

func TestShallowListMatchesFullListKeys(t *testing.T) {
	repo, dir := openFullRepo(t)
	require.NoError(t, seedMain(dir))
	ctx := t.Context()
	require.NoError(t, closeDraft(t, repo, "pkg", "v1", porchapi.PackageRevisionLifecycleDraft, 0))
	require.NoError(t, closeDraft(t, repo, "pkg", "v2", porchapi.PackageRevisionLifecyclePublished, 1))

	full, err := repo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	require.NoError(t, err)
	shallow, err := repo.ListPackageRevisionsShallow(ctx, repository.ListPackageRevisionFilter{})
	require.NoError(t, err)
	require.ElementsMatch(t, keysOf(full), keysOf(shallow))

	filtered, err := repo.ListPackageRevisionsShallow(ctx, repository.ListPackageRevisionFilter{
		Key: key.PackageRevisionKey{
			PkgKey:        key.PackageKey{Package: "pkg"},
			WorkspaceName: "v1",
		},
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, "v1", filtered[0].Key().WorkspaceName)
	resources, err := filtered[0].GetResources(ctx)
	require.NoError(t, err)
	require.Contains(t, resources.Spec.Resources, "Kptfile")

	maxRev, err := repo.MaxPublishedRevision(ctx, key.PackageKey{Package: "pkg"})
	require.NoError(t, err)
	require.Equal(t, 1, maxRev)

	found, err := repo.FindPackageRevisionByKey(ctx, key.PackageRevisionKey{
		PkgKey:        key.PackageKey{Package: "pkg"},
		WorkspaceName: "v2",
	})
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, 1, found.Key().Revision)
	apiPR, err := found.GetPackageRevision(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, apiPR.Spec.Tasks)

	head, err := repo.UpdatePackageRevisionHead(ctx, filtered[0])
	require.NoError(t, err)
	require.NoError(t, head.UpdateResources(ctx, &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: pkg\n",
		}},
	}, &porchapi.Task{Type: porchapi.TaskTypeRender}))
	_, err = repo.ClosePackageRevisionDraft(ctx, head, 0)
	require.NoError(t, err)
}

func openFullRepo(t *testing.T) (GitRepository, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "repos", "ns", "repo")
	repo, err := OpenRepository(t.Context(), "repo", "ns", &configapi.GitRepository{
		Repo:   "https://example.invalid/repo.git",
		Branch: "main",
	}, false, root, GitRepositoryOptions{
		MainBranchStrategy: SkipVerification,
		Layout:             FullRepositoryLayout,
		ExternalRepoOptions: externalrepotypes.ExternalRepoOptions{
			DisableRemoteFetchOnRead: true,
			AbsoluteCacheDir:         dir,
			SkipRemotePush:           true,
			RetainCacheOnClose:       true,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = repo.Close(t.Context()) })
	return repo, dir
}

func seedMain(dir string) error {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	hash, err := wt.Commit("init", &gogit.CommitOptions{
		AllowEmptyCommits: true,
		Author: &object.Signature{
			Name:  "porch",
			Email: "porch@example.invalid",
			When:  time.Unix(0, 0),
		},
	})
	if err != nil {
		return err
	}
	return repo.Storer.SetReference(plumbing.NewHashReference("refs/remotes/origin/main", hash))
}

func closeDraft(t *testing.T, repo GitRepository, pkg, workspace string, lifecycle porchapi.PackageRevisionLifecycle, version int) error {
	t.Helper()
	ctx := t.Context()
	draft, err := repo.CreatePackageRevisionDraft(ctx, &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			PackageName:   pkg,
			WorkspaceName: workspace,
			Lifecycle:     porchapi.PackageRevisionLifecycleDraft,
		},
	})
	if err != nil {
		return err
	}
	if err := draft.UpdateResources(ctx, &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: " + pkg + "\n",
		}},
	}, &porchapi.Task{Type: porchapi.TaskTypeInit}); err != nil {
		return err
	}
	if err := draft.UpdateLifecycle(ctx, lifecycle); err != nil {
		return err
	}
	_, err = repo.ClosePackageRevisionDraft(ctx, draft, version)
	return err
}

func keysOf(list []repository.PackageRevision) []key.PackageRevisionKey {
	out := make([]key.PackageRevisionKey, 0, len(list))
	for _, pr := range list {
		out = append(out, pr.Key())
	}
	return out
}
