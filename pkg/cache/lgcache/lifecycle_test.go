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
	"path/filepath"
	"testing"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/git"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/stretchr/testify/require"
)

func TestLifecycleFollowsGitWhenCRDSpecAlreadyPublished(t *testing.T) {
	root := t.TempDir()
	ctx := t.Context()
	local, err := git.OpenRepository(ctx, "repo", "ns", &configapi.GitRepository{
		Repo:   "https://example.invalid/repo.git",
		Branch: "main",
	}, false, root, git.GitRepositoryOptions{
		MainBranchStrategy: git.SkipVerification,
		Layout:             git.FullRepositoryLayout,
		ExternalRepoOptions: externalrepotypes.ExternalRepoOptions{
			DisableRemoteFetchOnRead: true,
			AbsoluteCacheDir:         filepath.Join(root, "repos", "ns", "repo"),
			SkipRemotePush:           true,
			RetainCacheOnClose:       true,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = local.Close(ctx) })

	draft, err := local.CreatePackageRevisionDraft(ctx, &porchapi.PackageRevision{
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
	closed, err := local.ClosePackageRevisionDraft(ctx, draft, 0)
	require.NoError(t, err)

	wrapped := &lgPackageRevision{
		gitPR: closed,
		crd: &porchv1alpha2.PackageRevision{
			Spec: porchv1alpha2.PackageRevisionSpec{
				Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished,
			},
		},
	}
	require.Equal(t, porchapi.PackageRevisionLifecycleDraft, wrapped.Lifecycle(ctx))

	wrapped.crd.Status.Revision = 3
	main := wrapped.ToMainPackageRevision(ctx)
	require.Equal(t, -1, main.Key().Revision)
	require.Equal(t, "main", main.Key().WorkspaceName)
}
