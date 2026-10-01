// Copyright 2022, 2025-2026 The kpt Authors
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
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-cmp/cmp"
	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util/selector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

func (g GitSuite) TestLock(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "drafts-repository.tar")
	repo, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	ctx := context.Background()
	const (
		repositoryName = "lock"
		namespace      = "default"
		deployment     = true
	)

	git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
		Repo:      address,
		Branch:    g.branch,
		Directory: "/",
	}, deployment, tempdir, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q: %v", tarfile, err)
	}

	revisions, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		t.Fatalf("Failed to list packages from %q: %v", tarfile, err)
	}

	wantRefs := map[repository.PackageRevisionKey]string{
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Namespace: "default", Name: repositoryName, PlaceholderWSname: g.branch}, Package: "empty"}, Revision: 1, WorkspaceName: "v1"}:   "empty/v1",
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Namespace: "default", Name: repositoryName, PlaceholderWSname: g.branch}, Package: "basens"}, Revision: 1, WorkspaceName: "v1"}:  "basens/v1",
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Namespace: "default", Name: repositoryName, PlaceholderWSname: g.branch}, Package: "basens"}, Revision: 2, WorkspaceName: "v2"}:  "basens/v2",
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Namespace: "default", Name: repositoryName, PlaceholderWSname: g.branch}, Package: "istions"}, Revision: 1, WorkspaceName: "v1"}: "istions/v1",
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Namespace: "default", Name: repositoryName, PlaceholderWSname: g.branch}, Package: "istions"}, Revision: 2, WorkspaceName: "v2"}: "istions/v2",

		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Namespace: "default", Name: repositoryName, PlaceholderWSname: g.branch}, Package: "basens"}, Revision: -1, WorkspaceName: g.branch}:  g.branch,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Namespace: "default", Name: repositoryName, PlaceholderWSname: g.branch}, Package: "empty"}, Revision: -1, WorkspaceName: g.branch}:   g.branch,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Namespace: "default", Name: repositoryName, PlaceholderWSname: g.branch}, Package: "istions"}, Revision: -1, WorkspaceName: g.branch}: g.branch,
	}

	for _, rev := range revisions {
		if rev.Lifecycle(ctx) != porchapi.PackageRevisionLifecyclePublished {
			continue
		}

		upstream, lock, err := rev.GetLock(ctx)
		if err != nil {
			t.Errorf("GetUpstreamLock(%q) failed: %v", rev.Key(), err)
		}
		if got, want := upstream.Type, kptfilev1.GitOrigin; got != want {
			t.Errorf("upstream.Type: got %s, want %s", got, want)
		}
		if got, want := lock.Type, kptfilev1.GitOrigin; got != want {
			t.Errorf("lock.Type: got %s, want %s", got, want)
		}

		key := rev.Key()
		wantRef, ok := wantRefs[key]
		if !ok {
			t.Errorf("Unexpected package found; %q", rev.Key())
		}

		type gitAddress struct {
			Repo, Directory, Ref string
		}

		// Check upstream values
		if got, want := (gitAddress{
			Repo:      upstream.Git.Repo,
			Directory: upstream.Git.Directory,
			Ref:       upstream.Git.Ref,
		}), (gitAddress{
			Repo:      address,
			Directory: key.PkgKey.ToFullPathname(),
			Ref:       wantRef,
		}); !cmp.Equal(want, got) {
			t.Errorf("Package upstream differs (-want,+got): %s", cmp.Diff(want, got))
		}

		// Check upstream lock values
		if got, want := (gitAddress{
			Repo:      lock.Git.Repo,
			Directory: lock.Git.Directory,
			Ref:       lock.Git.Ref,
		}), (gitAddress{
			Repo:      address,
			Directory: key.PkgKey.ToFullPathname(),
			Ref:       wantRef,
		}); !cmp.Equal(want, got) {
			t.Errorf("Package upstream lock differs (-want,+got): %s", cmp.Diff(want, got))
		}

		// Check the commit
		if commit, err := repo.ResolveRevision(plumbing.Revision(wantRef)); err != nil {
			t.Errorf("ResolveRevision(%q) failed: %v", wantRef, err)
		} else if got, want := lock.Git.Commit, commit.String(); got != want {
			t.Errorf("Commit: got %s, want %s", got, want)
		}
	}
}

func TestPackageGetters(t *testing.T) {
	gitPr := gitPackageRevision{
		prKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name:      "my-repo",
					Namespace: "my-namespace",
				},
				Package: "my-package",
			},
			WorkspaceName: "my-workspace",
		},
	}

	assert.Equal(t, "my-repo.my-package.my-workspace", gitPr.KubeObjectName())
	assert.Equal(t, "my-namespace", gitPr.KubeObjectNamespace())
	assert.Equal(t, types.UID("7007e8aa-0928-50f9-b980-92a44942f055"), gitPr.UID())
	assert.False(t, gitPr.IsLatestRevision())

	ts, author := gitPr.GetCommitInfo()
	assert.True(t, ts.IsZero())
	assert.Empty(t, author)
}

func TestPackageGetters_WithCommitInfo(t *testing.T) {
	now := time.Now()
	gitPr := gitPackageRevision{
		updated:   now,
		updatedBy: "user@example.com",
	}

	ts, author := gitPr.GetCommitInfo()
	assert.Equal(t, now, ts)
	assert.Equal(t, "user@example.com", author)
}

func (g GitSuite) TestGetFilteredResourcesReturnsAllFiles(t *testing.T) {
	ctx, pkgRev := g.openSimpleEmptyPackage(t)

	all, err := pkgRev.GetResources(ctx)
	require.NoError(t, err)
	got, err := pkgRev.GetFilteredResources(ctx, selector.AllFiles)

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, all.Spec.Resources, got.Spec.Resources)
	require.Contains(t, got.Spec.Resources, kptfilev1.KptFileName)
	require.Contains(t, got.Spec.Resources, readmeFile)
}

func (g GitSuite) TestGetFilteredResourcesReturnsMatchingFiles(t *testing.T) {
	ctx, pkgRev := g.openSimpleEmptyPackage(t)
	all, err := pkgRev.GetResources(ctx)
	require.NoError(t, err)
	wantKptfile := all.Spec.Resources[kptfilev1.KptFileName]

	got, err := pkgRev.GetFilteredResources(ctx, selector.PRRGet{FilePaths: []string{kptfilev1.KptFileName}})

	require.NoError(t, err)
	require.Equal(t, map[string]string{kptfilev1.KptFileName: wantKptfile}, got.Spec.Resources)
}

func (g GitSuite) TestGetFilteredResourcesReturnsErrorForUnknownFile(t *testing.T) {
	ctx, pkgRev := g.openSimpleEmptyPackage(t)

	got, err := pkgRev.GetFilteredResources(ctx, selector.PRRGet{FilePaths: []string{missingPackageFile}})

	require.Nil(t, got)
	require.ErrorContains(t, err, "failed to load package resources")
}

func (g GitSuite) TestGetFilteredResourcesReturnsErrorForInvalidTree(t *testing.T) {
	ctx, pkgRev := g.openSimpleEmptyPackage(t)
	gitPR, ok := pkgRev.(*gitPackageRevision)
	require.True(t, ok)
	broken := *gitPR
	broken.tree = plumbing.ZeroHash

	got, err := broken.GetFilteredResources(ctx, selector.PRRGet{FilePaths: []string{kptfilev1.KptFileName}})

	require.Nil(t, got)
	require.ErrorContains(t, err, "failed to load package resources")
}

const (
	simpleRepositoryTar   = "simple-repository.tar"
	simpleRepositoryName  = "simple"
	emptyPackageName      = "empty"
	emptyPackageWorkspace = "v1"
	readmeFile            = "README.md"
	missingPackageFile    = "missing.yaml"
)

func (g GitSuite) openSimpleEmptyPackage(t *testing.T) (context.Context, repository.PackageRevision) {
	t.Helper()
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", simpleRepositoryTar)
	_, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	ctx := context.Background()
	git, err := OpenRepository(ctx, simpleRepositoryName, "default", &configapi.GitRepository{
		Repo:      address,
		Branch:    g.branch,
		Directory: "/",
	}, true, tempdir, testGitRepositoryOptions())
	require.NoError(t, err)

	revisions, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	require.NoError(t, err)

	return ctx, findPackageRevision(t, revisions, repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: simpleRepositoryName,
				},
				Package: emptyPackageName,
			},
			Revision:      1,
			WorkspaceName: emptyPackageWorkspace,
		},
	})
}
