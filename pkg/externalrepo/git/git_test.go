// Copyright 2022, 2024-2025 The kpt Authors
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
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/go-cmp/cmp"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	extrepo "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	pkgerrors "github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const k8sAdmin = "kubernetes-admin"

// testGitRepositoryOptions returns GitRepositoryOptions with default retry attempts for tests
func testGitRepositoryOptions() GitRepositoryOptions {
	return GitRepositoryOptions{
		ExternalRepoOptions: extrepo.ExternalRepoOptions{
			RepoOperationRetryAttempts: 3,
		},
	}
}

func TestMain(m *testing.M) {
	klog.InitFlags(nil)
	flag.Parse()
	os.Exit(m.Run())
}

func TestGit(t *testing.T) {
	for _, gs := range []GitSuite{
		{branch: "main"},
		{branch: "feature"},
		{branch: "nested/release"},
	} {
		name := strings.ReplaceAll(gs.branch, "/", "-")
		t.Run(name, func(t *testing.T) {
			Run(gs, t)
		})
	}
}

func Run(suite interface{}, t *testing.T) {
	sv := reflect.ValueOf(suite)
	st := reflect.TypeOf(suite)

	for i, max := 0, st.NumMethod(); i < max; i++ {
		m := st.Method(i)
		if strings.HasPrefix(m.Name, "Test") {
			t.Run(m.Name, func(t *testing.T) {
				m.Func.Call([]reflect.Value{sv, reflect.ValueOf((t))})
			})
		}
	}
}

type GitSuite struct {
	branch string
}

func (g GitSuite) TestOpenEmptyRepository(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "empty-repository.tar")
	repo, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	ctx := context.Background()
	const (
		name       = "empty"
		namespace  = "default"
		deployment = true
	)

	repository := &configapi.GitRepository{
		Repo:      address,
		Branch:    g.branch,
		Directory: "/",
	}

	if _, err := OpenRepository(ctx, name, namespace, repository, deployment, tempdir, testGitRepositoryOptions()); err == nil {
		t.Errorf("Unexpectedly succeeded opening empty repository with main branch validation enabled.")
	}

	opts := testGitRepositoryOptions()
	opts.MainBranchStrategy = SkipVerification
	if _, err := OpenRepository(ctx, name, namespace, repository, deployment, tempdir, opts); err != nil {
		t.Errorf("Failed to open empty git repository with main branch validation disabled: %v", err)
	}

	opts = testGitRepositoryOptions()
	opts.MainBranchStrategy = CreateIfMissing
	if _, err := OpenRepository(ctx, name, namespace, repository, deployment, tempdir, opts); err != nil {
		t.Errorf("Failed to create new main branch: %v", err)
	}
	_, err := repo.Reference(branchName(g.branch).refInRemote(), false)
	if err != nil {
		t.Errorf("Couldn't find branch %q after opening repository with CreateIfMissing strategy: %v", g.branch, err)
	}
}

func (g GitSuite) TestOpenRepositoryBadBranchName(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "empty-repository.tar")
	_, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	ctx := context.Background()
	const (
		name       = "empty"
		namespace  = "default"
		deployment = true
	)

	repository := &configapi.GitRepository{
		Repo:      address,
		Branch:    "illegal$$$Characters",
		Directory: "/",
	}

	if _, err := OpenRepository(ctx, name, namespace, repository, deployment, tempdir, testGitRepositoryOptions()); err == nil {
		t.Errorf("Unexpectedly succeeded opening repository with a bad branch name.")
	}
}

// TestGitPackageRoundTrip creates a package in git and verifies we can read the contents back.
func (g GitSuite) TestGitPackageRoundTrip(t *testing.T) {
	tempdir := t.TempDir()
	p := filepath.Join(tempdir, "repo")
	serverRepo := InitEmptyRepositoryWithWorktree(t, p)

	if err := g.initRepo(serverRepo); err != nil {
		t.Fatalf("failed to init repo: %v", err)
	}

	ctx := context.Background()
	gitServerURL := ServeExistingRepository(t, serverRepo)

	// Now that we are running a git server, we can create a GitRepository backed by it

	const (
		repositoryName = "roundtrip"
		packageName    = "test-package"
		workspace      = "test-workspace"
		namespace      = "default"
		deployment     = true
	)
	spec := &configapi.GitRepository{
		Repo:   gitServerURL,
		Branch: g.branch,
	}

	root := filepath.Join(tempdir, "work")
	repo, err := OpenRepository(ctx, repositoryName, namespace, spec, deployment, root, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("failed to open repository: %v", err)
	}
	// TODO: is there any state? should we  defer repo.Close()

	t.Logf("repo is %#v", repo)

	// Push a package to the repo

	wantResources := map[string]string{
		"hello": "world",
	}

	// We require a Kptfile to indicate the package boundary
	wantResources["Kptfile"] = "placeholder"

	{
		packageRevision := &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
			},
			Spec: porchapi.PackageRevisionSpec{
				PackageName:    packageName,
				WorkspaceName:  workspace,
				RepositoryName: repositoryName,
			},
			Status: porchapi.PackageRevisionStatus{},
		}

		draft, err := repo.CreatePackageRevisionDraft(ctx, packageRevision)
		if err != nil {
			t.Fatalf("CreatePackageRevision(%#v) failed: %v", packageRevision, err)
		}

		newResources := &porchapi.PackageRevisionResources{}
		newResources.Spec.Resources = wantResources
		task := &porchapi.Task{}
		if err := draft.UpdateResources(ctx, newResources, task); err != nil {
			t.Fatalf("draft.UpdateResources(%#v, %#v) failed: %v", newResources, task, err)
		}

		revision, err := repo.ClosePackageRevisionDraft(ctx, draft, 1)
		if err != nil {
			t.Fatalf("draft.Close() failed: %v", err)
		}
		klog.Infof("created revision %v", revision.KubeObjectName())
	}

	// We approve the draft so that we can fetch it
	{
		revisions, err := repo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
		if err != nil {
			t.Fatalf("ListPackageRevisons failed: %v", err)
		}

		original := findPackageRevision(t, revisions, repository.ListPackageRevisionFilter{
			Key: repository.PackageRevisionKey{
				PkgKey: repository.PackageKey{
					RepoKey: repository.RepositoryKey{
						Name: repositoryName,
					},
					Package: packageName,
				},
				WorkspaceName: workspace,
			},
		})

		update, err := repo.UpdatePackageRevision(ctx, original)
		if err != nil {
			t.Fatalf("UpdatePackageRevision(%#v failed: %v", original, err)
		}
		if err := update.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished); err != nil {
			t.Fatalf("UpdateLifecycle failed: %v", err)
		}
		approved, err := repo.ClosePackageRevisionDraft(ctx, update, 1)
		if err != nil {
			t.Fatalf("Close() of %q, %q failed: %v", packageName, workspace, err)
		}
		if approved.Key().Revision != 1 {
			t.Fatalf("UpdateLifecycle did not assign correct revision number; got %d, want v1",
				approved.Key().Revision)
		}

		klog.Infof("approved revision %v", approved.KubeObjectName())
	}

	// Get the package again, the resources should match what we push
	{
		version := "v1"

		path := "test-package"
		packageRevision, gitLock, err := repo.GetPackageRevision(ctx, version, path)
		if err != nil {
			t.Fatalf("GetPackageRevision(%q, %q) failed: %v", version, path, err)
		}

		t.Logf("packageRevision is %s", packageRevision.KubeObjectName())
		t.Logf("gitLock is %#v", gitLock)

		resources, err := packageRevision.GetResources(ctx)
		if err != nil {
			t.Fatalf("GetResources() failed: %v", err)
		}

		t.Logf("resources is %v", resources.Spec.Resources)

		if !reflect.DeepEqual(resources.Spec.Resources, wantResources) {
			t.Fatalf("resources did not match expected; got %v, want %v", resources.Spec.Resources, wantResources)
		}
	}
}

// initRepo is a helper that creates a first commit, ensuring the repo is not empty.
func (g GitSuite) initRepo(repo *gogit.Repository) error {
	store := repo.Storer

	var objectHash plumbing.Hash
	{
		data := []byte("This is a test repo")
		eo := store.NewEncodedObject()
		eo.SetType(plumbing.BlobObject)
		eo.SetSize(int64(len(data)))

		w, err := eo.Writer()
		if err != nil {
			return fmt.Errorf("error creating object writer: %w", err)
		}

		if _, err = w.Write(data); err != nil {
			w.Close()
			return fmt.Errorf("error writing object data: %w", err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("error closing object data: %w", err)
		}

		if h, err := store.SetEncodedObject(eo); err != nil {
			return fmt.Errorf("error storing object: %w", err)
		} else {
			objectHash = h
		}
	}

	var treeHash plumbing.Hash
	{
		tree := object.Tree{}

		te := object.TreeEntry{
			Name: "README.md",
			Mode: filemode.Regular,
			Hash: objectHash,
		}
		tree.Entries = append(tree.Entries, te)

		eo := store.NewEncodedObject()
		if err := tree.Encode(eo); err != nil {
			return fmt.Errorf("error encoding tree: %w", err)
		}
		if h, err := store.SetEncodedObject(eo); err != nil {
			return fmt.Errorf("error storing tree: %w", err)
		} else {
			treeHash = h
		}
	}

	var commitHash plumbing.Hash
	{
		now := time.Now()
		commit := &object.Commit{
			Author: object.Signature{
				Name:  "Porch Author",
				Email: "author@kpt.dev",
				When:  now,
			},
			Committer: object.Signature{
				Name:  "Porch Committer",
				Email: "committer@kpt.dev",
				When:  now,
			},
			Message:  "First commit",
			TreeHash: treeHash,
		}

		eo := store.NewEncodedObject()
		if err := commit.Encode(eo); err != nil {
			return fmt.Errorf("error encoding commit: %w", err)
		}
		if h, err := store.SetEncodedObject(eo); err != nil {
			return fmt.Errorf("error storing commit: %w", err)
		} else {
			commitHash = h
		}
	}

	{
		name := plumbing.NewBranchReferenceName(g.branch)
		ref := plumbing.NewHashReference(name, commitHash)
		if err := repo.Storer.SetReference(ref); err != nil {
			return fmt.Errorf("error setting reference %q: %w", name, err)
		}

		// gogit uses suboptimal default reference name; delete it
		//nolint:errcheck
		repo.Storer.RemoveReference(plumbing.Master)

		// create correct HEAD as a symbolic reference of the branch
		head := plumbing.NewSymbolicReference(plumbing.HEAD, name)
		if err := repo.Storer.SetReference(head); err != nil {
			return fmt.Errorf("error creating HEAD ref to %q: %w", ref, err)
		}
	}

	return nil
}

const Kptfile = `
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: empty
info:
  description: Empty Package
`

func (g GitSuite) TestListPackagesTrivial(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")
	_, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	ctx := context.Background()
	const (
		repositoryName = "empty"
		namespace      = "default"
		deployment     = true
	)

	git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
		Repo:      address,
		Branch:    g.branch,
		Directory: "/",
		SecretRef: configapi.SecretRef{},
	}, deployment, tempdir, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q: %v", tarfile, err)
	}

	packageList, err := git.ListPackages(ctx, repository.ListPackageFilter{})
	assert.Nil(t, packageList)
	assert.Equal(t, "ListPackages() is only supported on repository caches", err.Error())

	revisions, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		t.Fatalf("Failed to list packages from %q: %v", tarfile, err)
	}
	if got, want := len(revisions), 0; got != want {
		t.Errorf("Number of packges in empty repository: got %d, want %d", got, want)
	}

	packageRevision := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    "test-package",
			WorkspaceName:  "test-workspace",
			RepositoryName: repositoryName,
			Lifecycle:      porchapi.PackageRevisionLifecycleDraft,
		},
	}

	// Create a package draft
	draft, err := git.CreatePackageRevisionDraft(ctx, packageRevision)
	if err != nil {
		t.Fatalf("CreatePackageRevision() failed: %v", err)
	}
	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"Kptfile": Kptfile,
			},
		},
	}
	if err := draft.UpdateResources(ctx, resources, &porchapi.Task{
		Type: porchapi.TaskTypeInit,
		Init: &porchapi.PackageInitTaskSpec{
			Description: "Empty Package",
		},
	}); err != nil {
		t.Fatalf("UpdateResources() failed: %v", err)
	}
	newRevision, err := git.ClosePackageRevisionDraft(ctx, draft, 0)
	if err != nil {
		t.Fatalf("draft.Close() failed: %v", err)
	}

	result, err := newRevision.GetPackageRevision(ctx)
	if err != nil {
		t.Errorf("didn't expect error, but got %v", err)
	}
	if got, want := result.Spec.Lifecycle, porchapi.PackageRevisionLifecycleDraft; got != want {
		t.Errorf("Newly created package type: got %q, want %q", got, want)
	}

	// Verify
	verify, err := gogit.PlainOpen(filepath.Join(tempdir, ".git"))
	if err != nil {
		t.Fatalf("Failed to open git repository for verification: %v", err)
	}
	logRefs(t, verify, "Ref: ")
	draftRefName := plumbing.NewBranchReferenceName("drafts/test-package/test-workspace")
	if _, err = verify.Reference(draftRefName, true); err != nil {
		t.Errorf("Failed to resolve %q references: %v", draftRefName, err)
	}
}

// trivial-repository.tar has a repon with a `main` branch and a single empty commit.
func (g GitSuite) TestCreatePackageInTrivialRepository(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")
	_, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	ctx := context.Background()
	const (
		repositoryName = "trivial"
		namespace      = "default"
		deployment     = true
	)

	git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
		Repo:      address,
		Branch:    g.branch,
		Directory: "/",
		SecretRef: configapi.SecretRef{},
	}, deployment, tempdir, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q: %v", tarfile, err)
	}

	revisions, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		t.Fatalf("Failed to list packages from %q: %v", tarfile, err)
	}
	if got, want := len(revisions), 0; got != want {
		t.Errorf("Number of packges in the trivial repository: got %d, want %d", got, want)
	}

	packageRevision := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    "test-package",
			WorkspaceName:  "test-workspace",
			RepositoryName: repositoryName,
			Lifecycle:      porchapi.PackageRevisionLifecycleDraft,
		},
	}

	// Create a package draft
	draft, err := git.CreatePackageRevisionDraft(ctx, packageRevision)
	if err != nil {
		t.Fatalf("CreatePackageRevision() failed: %v", err)
	}
	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"Kptfile": Kptfile,
			},
		},
	}
	if err := draft.UpdateResources(ctx, resources, &porchapi.Task{
		Type: porchapi.TaskTypeInit,
		Init: &porchapi.PackageInitTaskSpec{
			Description: "Empty Package",
		},
	}); err != nil {
		t.Fatalf("UpdateResources() failed: %v", err)
	}
	newRevision, err := git.ClosePackageRevisionDraft(ctx, draft, 0)
	if err != nil {
		t.Fatalf("draft.Close() failed: %v", err)
	}

	result, err := newRevision.GetPackageRevision(ctx)
	if err != nil {
		t.Errorf("didn't expect error, but got %v", err)
	}
	if got, want := result.Spec.Lifecycle, porchapi.PackageRevisionLifecycleDraft; got != want {
		t.Errorf("Newly created package type: got %q, want %q", got, want)
	}
}

func (g GitSuite) TestListPackagesSimple(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "simple-repository.tar")
	_, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	ctx := context.Background()
	const (
		repositoryName = "simple"
		namespace      = "default"
		deployment     = true
	)

	git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
		Repo:      address,
		Branch:    g.branch,
		Directory: "/",
		SecretRef: configapi.SecretRef{},
	}, deployment, tempdir, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q: %v", tarfile, err)
	}

	revisions, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		t.Fatalf("Failed to list packages from %q: %v", tarfile, err)
	}

	want := map[repository.PackageRevisionKey]porchapi.PackageRevisionLifecycle{
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "simple"}, Package: "empty"}, Revision: 1, WorkspaceName: "v1"}:   porchapi.PackageRevisionLifecyclePublished,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "simple"}, Package: "basens"}, Revision: 1, WorkspaceName: "v1"}:  porchapi.PackageRevisionLifecyclePublished,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "simple"}, Package: "basens"}, Revision: 2, WorkspaceName: "v2"}:  porchapi.PackageRevisionLifecyclePublished,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "simple"}, Package: "istions"}, Revision: 1, WorkspaceName: "v1"}: porchapi.PackageRevisionLifecyclePublished,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "simple"}, Package: "istions"}, Revision: 2, WorkspaceName: "v2"}: porchapi.PackageRevisionLifecyclePublished,

		// TODO: may want to filter these out, for example by including only those package
		// revisions from main branch that differ in content (their tree hash) from another
		// taged revision of the package.
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "simple"}, Package: "empty"}, Revision: -1, WorkspaceName: g.branch}:   porchapi.PackageRevisionLifecyclePublished,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "simple"}, Package: "basens"}, Revision: -1, WorkspaceName: g.branch}:  porchapi.PackageRevisionLifecyclePublished,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "simple"}, Package: "istions"}, Revision: -1, WorkspaceName: g.branch}: porchapi.PackageRevisionLifecyclePublished,
	}

	got := map[repository.PackageRevisionKey]porchapi.PackageRevisionLifecycle{}
	for _, r := range revisions {
		rev, err := r.GetPackageRevision(ctx)
		if err != nil {
			t.Errorf("didn't expect error, but got %v", err)
		}
		got[repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: rev.Spec.RepositoryName,
				},
				Package: rev.Spec.PackageName,
			},
			WorkspaceName: rev.Spec.WorkspaceName,
			Revision:      rev.Spec.Revision,
		}] = rev.Spec.Lifecycle
	}

	if !cmp.Equal(want, got) {
		t.Errorf("Package Revisions in simple-repository: (-want,+got): %s", cmp.Diff(want, got))
	}
}

func (g GitSuite) TestListPackagesDrafts(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "drafts-repository.tar")
	_, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	ctx := context.Background()
	const (
		repositoryName = "drafts"
		namespace      = "default"
		deployment     = true
	)

	git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
		Repo:      address,
		Branch:    g.branch,
		Directory: "/",
		SecretRef: configapi.SecretRef{},
	}, deployment, tempdir, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q: %v", tarfile, err)
	}

	revisions, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		t.Fatalf("Failed to list packages from %q: %v", tarfile, err)
	}

	want := map[repository.PackageRevisionKey]porchapi.PackageRevisionLifecycle{
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "drafts"}, Package: "empty"}, Revision: 1, WorkspaceName: "v1"}:   porchapi.PackageRevisionLifecyclePublished,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "drafts"}, Package: "basens"}, Revision: 1, WorkspaceName: "v1"}:  porchapi.PackageRevisionLifecyclePublished,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "drafts"}, Package: "basens"}, Revision: 2, WorkspaceName: "v2"}:  porchapi.PackageRevisionLifecyclePublished,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "drafts"}, Package: "istions"}, Revision: 1, WorkspaceName: "v1"}: porchapi.PackageRevisionLifecyclePublished,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "drafts"}, Package: "istions"}, Revision: 2, WorkspaceName: "v2"}: porchapi.PackageRevisionLifecyclePublished,

		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "drafts"}, Package: "bucket"}, WorkspaceName: "v1"}:           porchapi.PackageRevisionLifecycleDraft,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "drafts"}, Package: "none"}, WorkspaceName: "v1"}:             porchapi.PackageRevisionLifecycleDraft,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "drafts"}, Package: "pkg-with-history"}, WorkspaceName: "v1"}: porchapi.PackageRevisionLifecycleDraft,

		// TODO: filter main branch out? see above
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "drafts"}, Package: "basens"}, WorkspaceName: g.branch, Revision: -1}:  porchapi.PackageRevisionLifecyclePublished,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "drafts"}, Package: "empty"}, WorkspaceName: g.branch, Revision: -1}:   porchapi.PackageRevisionLifecyclePublished,
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "drafts"}, Package: "istions"}, WorkspaceName: g.branch, Revision: -1}: porchapi.PackageRevisionLifecyclePublished,
	}

	got := map[repository.PackageRevisionKey]porchapi.PackageRevisionLifecycle{}
	for _, r := range revisions {
		rev, err := r.GetPackageRevision(ctx)
		if err != nil {
			t.Errorf("didn't expect error, but got %v", err)
		}
		got[repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: rev.Spec.RepositoryName,
				},
				Package: rev.Spec.PackageName,
			},
			Revision:      rev.Spec.Revision,
			WorkspaceName: rev.Spec.WorkspaceName,
		}] = rev.Spec.Lifecycle
	}

	if !cmp.Equal(want, got) {
		t.Errorf("Package Revisions in drafts-repository: (-want,+got): %s", cmp.Diff(want, got))
	}
}

func (g GitSuite) TestCloseProposedPackage(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")
	repo, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	const (
		repositoryName = "proposed"
		namespace      = "default"
		packageName    = "test-pkg"
		workspace      = "v1"
		deployment     = false
	)

	ctx := context.Background()
	git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
		Repo:   address,
		Branch: g.branch,
	}, deployment, tempdir, testGitRepositoryOptions())
	require.NoError(t, err, "Failed to open Git repository")

	pr := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    packageName,
			WorkspaceName:  workspace,
			RepositoryName: repositoryName,
		},
	}

	draft, err := git.CreatePackageRevisionDraft(ctx, pr)
	require.NoError(t, err, "CreatePackageRevision() failed")

	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"Kptfile": Kptfile,
			},
		},
	}
	err = draft.UpdateResources(ctx, resources, &porchapi.Task{
		Type: porchapi.TaskTypeInit,
		Init: &porchapi.PackageInitTaskSpec{
			Description: "Test Package",
		},
	})
	require.NoError(t, err, "UpdateResources() failed")

	err = draft.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycleProposed)
	require.NoError(t, err, "UpdateLifecycle() failed")

	newRevision, err := git.ClosePackageRevisionDraft(ctx, draft, 0)
	require.NoError(t, err, "ClosePackageRevisionDraft() failed")

	result, err := newRevision.GetPackageRevision(ctx)
	require.NoError(t, err, "GetPackageRevision() failed")
	assert.Equal(t, porchapi.PackageRevisionLifecycleProposed, result.Spec.Lifecycle, "Package lifecycle")

	proposedBranch := createProposedName(newRevision.Key())
	refMustExist(t, repo, proposedBranch.refInRemote())
}

func (g GitSuite) TestApproveDraft(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "drafts-repository.tar")
	repo, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	const (
		repositoryName                            = "approve"
		namespace                                 = "default"
		draft              branchName             = "drafts/bucket/v1"
		finalReferenceName plumbing.ReferenceName = "refs/tags/bucket/v1"
		deployment                                = true
	)
	ctx := context.Background()
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
		t.Fatalf("ListPackageRevisions failed: %v", err)
	}

	bucket := findPackageRevision(t, revisions, repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				Package: "bucket",
			},
			WorkspaceName: "v1",
		},
	})

	// Before Update; Check server references. Draft must exist, final not.
	refMustExist(t, repo, draft.refInRemote())
	refMustNotExist(t, repo, finalReferenceName)

	update, err := git.UpdatePackageRevision(ctx, bucket)
	if err != nil {
		t.Fatalf("UpdatePackageRevision failed: %v", err)
	}
	err = update.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished)
	if err != nil {
		t.Fatalf("UpdateLifecycle failed: %v", err)
	}

	new, err := git.ClosePackageRevisionDraft(ctx, update, 1)
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	rev, err := new.GetPackageRevision(ctx)
	if err != nil {
		t.Errorf("didn't expect error, but got %v", err)
	}
	if got, want := rev.Spec.Lifecycle, porchapi.PackageRevisionLifecyclePublished; got != want {
		t.Errorf("Approved package lifecycle: got %s, want %s", got, want)
	}

	// After Update: Final must exist, draft must not exist
	refMustNotExist(t, repo, draft.refInRemote())
	refMustExist(t, repo, finalReferenceName)
}

func (g GitSuite) TestApproveDraftWithHistory(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "drafts-repository.tar")
	repo, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	const (
		repositoryName                            = "approve"
		namespace                                 = "default"
		draft              branchName             = "drafts/pkg-with-history/v1"
		finalReferenceName plumbing.ReferenceName = "refs/tags/pkg-with-history/v1"
		deployment                                = true
	)
	ctx := context.Background()
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
		t.Fatalf("ListPackageRevisions failed: %v", err)
	}

	bucket := findPackageRevision(t, revisions, repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: repositoryName,
				},
				Package: "pkg-with-history",
			},
			WorkspaceName: "v1",
		},
	})

	// Before Update; Check server references. Draft must exist, final not.
	refMustExist(t, repo, draft.refInRemote())
	refMustNotExist(t, repo, finalReferenceName)

	update, err := git.UpdatePackageRevision(ctx, bucket)
	if err != nil {
		t.Fatalf("UpdatePackageRevision failed: %v", err)
	}
	err = update.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished)
	if err != nil {
		t.Fatalf("UpdateLifecycle failed: %v", err)
	}

	new, err := git.ClosePackageRevisionDraft(ctx, update, 1)
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	rev, err := new.GetPackageRevision(ctx)
	if err != nil {
		t.Errorf("didn't expect error, but got %v", err)
	}
	if got, want := rev.Spec.Lifecycle, porchapi.PackageRevisionLifecyclePublished; got != want {
		t.Errorf("Approved package lifecycle: got %s, want %s", got, want)
	}
	if got, want := len(rev.Spec.Tasks), 1; got != want {
		t.Errorf("Approved package task count: got %d, want %d", got, want)
	}

	// After Update: Final must exist, draft must not exist
	refMustNotExist(t, repo, draft.refInRemote())
	refMustExist(t, repo, finalReferenceName)
}

func (g GitSuite) TestDeletePackages(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "drafts-repository.tar")
	repo, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	const (
		repositoryName = "delete"
		namespace      = "delete-namespace"
		deployment     = true
	)

	ctx := context.Background()
	git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
		Repo:   address,
		Branch: g.branch,
	}, deployment, tempdir, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("OpenRepository(%q) failed: %v", address, err)
	}

	// If we delete one of these packages, we expect the reference to be deleted too
	wantDeletedRefs := map[repository.PackageRevisionKey]plumbing.ReferenceName{
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "delete"}, Package: "bucket"}, Revision: 1}:  "refs/heads/drafts/bucket/v1",
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "delete"}, Package: "none"}, Revision: 1}:    "refs/heads/drafts/none/v1",
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "delete"}, Package: "basens"}, Revision: 1}:  "refs/tags/basens/v1",
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "delete"}, Package: "basens"}, Revision: 2}:  "refs/tags/basens/v2",
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "delete"}, Package: "empty"}, Revision: 1}:   "refs/tags/empty/v1",
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "delete"}, Package: "istions"}, Revision: 1}: "refs/tags/istions/v1",
		{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "delete"}, Package: "istions"}, Revision: 2}: "refs/tags/istions/v2",
	}

	// Delete all packages
	all, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		t.Fatalf("ListPackageRevisions failed: %v", err)
	}

	for len(all) > 0 {
		// Delete one of the packages
		deleting := all[0]
		pr, err := deleting.GetPackageRevision(ctx)
		if err != nil {
			t.Fatalf("didn't expect error, but got %v", err)
		}
		name := repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: pr.Spec.RepositoryName,
				},
				Package: pr.Spec.PackageName,
			},
			Revision:      pr.Spec.Revision,
			WorkspaceName: pr.Spec.WorkspaceName,
		}

		if rn, ok := wantDeletedRefs[name]; ok {
			// Verify the reference still exists
			refMustExist(t, repo, rn)
		}

		gitDeleting := deleting.(*gitPackageRevision)
		saveRef := gitDeleting.ref
		gitDeleting.ref = nil
		if err := git.DeletePackageRevision(ctx, deleting); err == nil {
			t.Fatalf("DeletePackageRevision(%q) should have failed on nil reference: %v", deleting.KubeObjectName(), err)
		}
		gitDeleting.ref = saveRef

		if err := git.DeletePackageRevision(ctx, deleting); err != nil {
			t.Fatalf("DeletePackageRevision(%q) failed: %v", deleting.KubeObjectName(), err)
		}

		if rn, ok := wantDeletedRefs[name]; ok {
			// Verify the reference no longer exists
			refMustNotExist(t, repo, rn)
		}

		// Re-list packages and check the deleted package is absent
		all, err = git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
		if err != nil {
			t.Fatalf("ListPackageRevisions failed: %v", err)
		}

		packageMustNotExist(t, all, name)
	}

	// The only got references should be main and HEAD
	got := map[plumbing.ReferenceName]bool{}
	forEachRef(t, repo, func(ref *plumbing.Reference) error {
		got[ref.Name()] = true
		return nil
	})

	// branch may be `refs/heads/main` for some test runs
	branch := plumbing.NewBranchReferenceName(g.branch)
	want := map[plumbing.ReferenceName]bool{
		branch:                   true,
		defaultMainReferenceName: true,
		"HEAD":                   true,
	}
	if !cmp.Equal(want, got) {
		t.Fatalf("Unexpected references after deleting all packages (-want, +got): %s", cmp.Diff(want, got))
	}

	// And there should be no packages in main branch
	main := resolveReference(t, repo, branch)
	tree := getCommitTree(t, repo, main.Hash())
	if len(tree.Entries) > 0 {
		var b bytes.Buffer
		for i := range tree.Entries {
			e := &tree.Entries[i]
			fmt.Fprintf(&b, "%s: %s (%s)", e.Name, e.Hash, e.Mode)
		}
		// Tree is not empty after deleting all packages
		t.Errorf("%q branch has non-empty tree after all packages have been deleted: %s", branch, b.String())
	}
}

// Test introduces package in the upstream repo and lists is after refresh.
func (g GitSuite) TestRefreshRepo(t *testing.T) {
	upstreamDir := t.TempDir()
	downstreamDir := t.TempDir()
	tarfile := filepath.Join("testdata", "simple-repository.tar")
	upstream := OpenGitRepositoryFromArchiveWithWorktree(t, tarfile, upstreamDir)
	InitializeBranch(t, upstream, g.branch)
	address := ServeExistingRepository(t, upstream)

	const (
		repositoryName = "refresh"
		namespace      = "refresh-namespace"
		deployment     = true
	)

	newPackageName := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Name: "refresh",
			},
			Package: "newpkg",
		},
		Revision:      3,
		WorkspaceName: "v3",
	}

	ctx := context.Background()
	git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
		Repo: address,
	}, deployment, downstreamDir, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("OpenRepository(%q) failed: %v", address, err)
	}

	all, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		t.Fatalf("ListPackageRevisions failed: %v", err)
	}

	// Confirm we listed some package(s)
	findPackageRevision(t, all, repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: "refresh",
				},
				Package: "basens",
			},
			Revision:      2,
			WorkspaceName: "v2",
		}})
	packageMustNotExist(t, all, newPackageName)

	// Create package in the upstream repository
	wt, err := upstream.Worktree()
	if err != nil {
		t.Fatalf("Worktree failed: %v", err)
	}

	name := plumbing.NewBranchReferenceName(g.branch)
	main := resolveReference(t, upstream, name)
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: main.Name(),
		Force:  true,
	}); err != nil {
		t.Fatalf("Checkout failed: %v", err)
	}

	const kptfileName = "newpkg/Kptfile"
	file, err := wt.Filesystem.Create(kptfileName)
	if err != nil {
		t.Fatalf("Filesystem.Create failed: %v", err)
	}
	if _, err := file.Write([]byte(Kptfile)); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}
	if _, err := wt.Add(kptfileName); err != nil {
		t.Fatalf("Failed to add file to index: %v", err)
	}
	sig := object.Signature{
		Name:  "Test",
		Email: "test@kpt.dev",
		When:  time.Now(),
	}
	commit, err := wt.Commit("Hello", &gogit.CommitOptions{
		Author:    &sig,
		Committer: &sig,
	})
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	tag := plumbing.NewHashReference(plumbing.NewTagReferenceName("newpkg/v3"), commit)
	if err := upstream.Storer.SetReference(tag); err != nil {
		t.Fatalf("Failed to create tag %s: %v", tag, err)
	}

	all, err = git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		t.Fatalf("ListPackageRevisions(Refresh) failed; %v", err)
	}
	findPackageRevision(t, all, repository.ListPackageRevisionFilter{Key: newPackageName})
}

// The test deletes packages on the upstream one by one and validates they were
// pruned in the registered repository on refresh.
func (g GitSuite) TestPruneRemotes(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "drafts-repository.tar")
	repo, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	const (
		name       = "prune"
		namespace  = "prune-namespace"
		deployment = true
	)

	ctx := context.Background()
	git, err := OpenRepository(ctx, name, namespace, &configapi.GitRepository{
		Repo:   address,
		Branch: g.branch,
	}, deployment, tempdir, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("OpenRepository(%q) failed: %v", address, err)
	}

	for _, pair := range []struct {
		ref plumbing.ReferenceName
		pkg repository.PackageRevisionKey
	}{
		{
			ref: "refs/heads/drafts/bucket/v1",
			pkg: repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "prune"}, Package: "bucket"}, WorkspaceName: "v1"},
		},
		{
			ref: "refs/heads/drafts/none/v1",
			pkg: repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "prune"}, Package: "none"}, WorkspaceName: "v1"},
		},
		{
			ref: "refs/tags/basens/v1",
			pkg: repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "prune"}, Package: "basens"}, Revision: 1, WorkspaceName: "v1"},
		},
		{
			ref: "refs/tags/basens/v2",
			pkg: repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "prune"}, Package: "basens"}, Revision: 2, WorkspaceName: "v2"},
		},
		{
			ref: "refs/tags/empty/v1",
			pkg: repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "prune"}, Package: "empty"}, Revision: 1, WorkspaceName: "v1"},
		},
		{
			ref: "refs/tags/istions/v1",
			pkg: repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "prune"}, Package: "istions"}, Revision: 1, WorkspaceName: "v1"},
		},
		{
			ref: "refs/tags/istions/v2",
			pkg: repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "prune"}, Package: "istions"}, Revision: 2, WorkspaceName: "v2"},
		},
	} {
		repositoryMustHavePackageRevision(t, git, pair.pkg)
		refMustExist(t, repo, pair.ref)
		if err := repo.Storer.RemoveReference(pair.ref); err != nil {
			t.Fatalf("RemoveReference(%q) failed: %v", pair.ref, err)
		}
		repositoryMustNotHavePackageRevision(t, git, pair.pkg)
	}
}

func (g GitSuite) TestNested(t *testing.T) {
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "nested-repository.tar")
	repo, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

	ctx := context.Background()
	const (
		repositoryName = "nested"
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

	// Name of the registered branch
	branch := plumbing.NewBranchReferenceName(g.branch)

	// Check that all tags and branches have their packages.
	want := map[string]porchapi.PackageRevisionLifecycle{}
	forEachRef(t, repo, func(ref *plumbing.Reference) error {
		switch name := string(ref.Name()); {
		case strings.HasPrefix(name, tagsPrefixInRemoteRepo):
			want[strings.TrimPrefix(name, tagsPrefixInRemoteRepo)] = porchapi.PackageRevisionLifecyclePublished
		case strings.HasPrefix(name, draftsPrefixInRemoteRepo):
			want[strings.TrimPrefix(name, draftsPrefixInRemoteRepo)] = porchapi.PackageRevisionLifecycleDraft
		case strings.HasPrefix(name, proposedPrefixInRemoteRepo):
			want[strings.TrimPrefix(name, proposedPrefixInRemoteRepo)] = porchapi.PackageRevisionLifecycleProposed
		case name == string(branch):
			// Skip the registered 'main' branch.
		case name == string(defaultMainReferenceName), name == "HEAD":
			// skip main and HEAD
		default:
			// There should be no other refs in the repository.
			return fmt.Errorf("unexpected reference: %s", ref)
		}
		return nil
	})

	got := map[string]porchapi.PackageRevisionLifecycle{}
	for _, pr := range revisions {
		rev, err := pr.GetPackageRevision(ctx)
		if err != nil {
			t.Errorf("didn't expect error, but got %v", err)
		}
		if rev.Spec.Revision == -1 {
			// skip packages with the revision of the main registered branch,
			// to match the above simplified package discovery algo.
			continue
		}
		if rev.Spec.Lifecycle == porchapi.PackageRevisionLifecyclePublished {
			got[fmt.Sprintf("%s/v%d", rev.Spec.PackageName, rev.Spec.Revision)] = rev.Spec.Lifecycle
		} else {
			got[fmt.Sprintf("%s/%s", rev.Spec.PackageName, rev.Spec.WorkspaceName)] = rev.Spec.Lifecycle
		}
	}

	if !cmp.Equal(want, got) {
		t.Errorf("Discovered packages differ: (-want,+got): %s", cmp.Diff(want, got))
	}
}

func createPackageRevisionMap(revisions []repository.PackageRevision) map[string]bool {
	result := map[string]bool{}
	for _, pr := range revisions {
		key := pr.Key()
		if key.PkgKey.Path != "" { // Ignore sub-paths in packages
			continue
		}
		if key.WorkspaceName != "" {
			result[fmt.Sprintf("%s/%s", key.PkgKey.Package, key.WorkspaceName)] = true
		} else {
			result[fmt.Sprintf("%s/%d", key.PkgKey.Package, key.Revision)] = true
		}
	}
	return result
}

func sliceToSet(s []string) map[string]bool {
	result := map[string]bool{}
	for _, v := range s {
		result[v] = true
	}
	return result
}

func (g GitSuite) TestNestedDirectories(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		repoName  string
		directory string
		packages  []string
	}{
		{
			repoName:  "sample",
			directory: "",
			packages: []string{
				"sample/v1",
				"sample/v2",
				"sample/" + g.branch,
			},
		},
		{
			repoName:  "nonexistent",
			directory: "nonexistent",
			packages:  []string{},
		},
		{
			repoName:  "catalog/gcp",
			directory: "catalog/gcp",
			packages: []string{
				"cloud-sql/v1",
				"spanner/v1",
				"bucket/v2",
				"bucket/v1",
				"bucket/" + g.branch,
			},
		},
	} {
		t.Run(tc.repoName, func(t *testing.T) {
			tempdir := t.TempDir()
			tarfile := filepath.Join("testdata", "nested-repository.tar")
			_, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

			const (
				repositoryName = "directory"
				namespace      = "default"
				deployment     = true
			)

			git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
				Repo:      address,
				Branch:    g.branch,
				Directory: tc.directory,
			}, deployment, tempdir, testGitRepositoryOptions())
			if err != nil {
				t.Fatalf("Failed to open Git repository loaded from %q with directory %q: %v", tarfile, tc.directory, err)
			}

			revisions, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
			if err != nil {
				t.Fatalf("Failed to list packages from %q: %v", tarfile, err)
			}

			got := createPackageRevisionMap(revisions)
			want := sliceToSet(tc.packages)

			if !cmp.Equal(want, got) {
				t.Errorf("Packages rooted in %q; Unexpected result (-want,+got): %s", tc.directory, cmp.Diff(want, got))
			}
		})
	}
}

func (g GitSuite) TestAuthor(t *testing.T) {
	ctx := context.Background()

	testCases := map[string]struct {
		pkg       string
		workspace string
		revision  int
		author    string
		timestamp time.Time
	}{
		"draft packagerevision does not have publishing info in status": {
			pkg:       "draft-pkg",
			workspace: "v1",
			author:    "",
			timestamp: time.Time{},
		},
		"published packagerevision on tag": {
			pkg:       "pkg-with-anno",
			revision:  1,
			workspace: "v1",
			author:    "pkg-with-anno-author@example.com",
			timestamp: time.Date(2022, time.August, 26, 22, 47, 35, 0, time.UTC),
		},
		"published packagerevision on main without commit annotations": {
			pkg:       "pkg-without-anno",
			revision:  -1,
			workspace: g.branch,
			author:    "",
			timestamp: time.Time{},
		},
		"published packagerevision on main with commit annotations": {
			pkg:       "pkg-with-anno",
			revision:  -1,
			workspace: g.branch,
			author:    "pkg-with-anno-author@example.com",
			timestamp: time.Date(2022, time.August, 26, 22, 47, 35, 0, time.UTC),
		},
	}

	for tn := range testCases {
		tc := testCases[tn]
		t.Run(tn, func(t *testing.T) {
			tempdir := t.TempDir()
			tarfile := filepath.Join("testdata", "publishinfo-repository.tar")
			_, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, g.branch)

			const (
				repositoryName = "directory"
				namespace      = "default"
				deployment     = true
			)

			git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
				Repo:   address,
				Branch: g.branch,
			}, deployment, tempdir, testGitRepositoryOptions())
			if err != nil {
				t.Fatalf("Failed to open Git repository loaded from %q: %v", tarfile, err)
			}

			revisions, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
			if err != nil {
				t.Fatalf("ListPackageRevisions failed: %v", err)
			}

			_ = revisions
			draftPkg := findPackageRevision(t, revisions, repository.ListPackageRevisionFilter{
				Key: repository.PackageRevisionKey{
					PkgKey: repository.PackageKey{
						RepoKey: repository.RepositoryKey{
							Name: repositoryName,
						},
						Package: tc.pkg,
					},
					WorkspaceName: tc.workspace,
					Revision:      tc.revision,
				},
			})
			rev, err := draftPkg.GetPackageRevision(ctx)
			if err != nil {
				t.Errorf("didn't expect error, but got %v", err)
			}
			if got, want := rev.Status.PublishedBy, tc.author; got != want {
				t.Errorf("expected %q, but got %q", want, got)
			}

			if got, want := rev.Status.PublishedAt.Time, tc.timestamp; !want.Equal(got) {
				t.Errorf("expected %v, but got %v", want, got)
			}

		})
	}
}

func TestDiscoverManuallyTaggedPackageWithTagMessage(t *testing.T) {
	ctx := context.Background()
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "manual-tagged-repository.tar")
	_, address := ServeGitRepository(t, tarfile, tempdir)

	const (
		repositoryName = "blueprint"
		namespace      = "default"
		deployment     = false
		packageName    = "pkg"
	)

	git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
		Repo: address,
	}, deployment, tempdir, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q: %v", tarfile, err)
	}

	expectedRevisions := []int{-1, 1, 2}

	prs, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		t.Fatalf("ListPackageRevisions failed: %v", err)
	}

	for _, pr := range prs {
		gitpr, err := pr.GetPackageRevision(ctx)
		if err != nil {
			t.Errorf("GetPackageRevision failed for %q: %v", pr.KubeObjectName(), err)
			continue
		}

		assert.Equal(t, packageName, gitpr.Spec.PackageName)
		assert.Contains(t, expectedRevisions, gitpr.Spec.Revision)
	}
}

func TestDiscoverWithBadKptAnnotationFromNestedRepository(t *testing.T) {
	ctx := context.Background()
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "nested-repository-with-invalid-kpt-annotation.tar")
	_, address := ServeGitRepository(t, tarfile, tempdir)

	const (
		repositoryName = "blueprint"
		directory      = "blueprint"
		namespace      = "default"
		deployment     = false
	)

	expectedRevisions := map[string][]int{
		"bp1": {-1, 1, 2, 3},
		"bp2": {-1, 1, 2},
	}

	git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
		Repo:      address,
		Directory: directory,
	}, deployment, tempdir, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q and directory %q: %v", tarfile, directory, err)
	}

	for _, packageName := range []string{"bp1", "bp2"} {
		prs, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
			Key: repository.PackageRevisionKey{
				PkgKey: repository.PackageKey{
					RepoKey: repository.RepositoryKey{
						Name: repositoryName,
					},
					Package: packageName,
				},
			},
		})
		if err != nil {
			t.Errorf("ListPackageRevisions failed for package %s: %v", packageName, err)
			continue
		}

		for _, pr := range prs {
			gitpr, err := pr.GetPackageRevision(ctx)
			if err != nil {
				t.Errorf("GetPackageRevision failed for %q: %v", pr.KubeObjectName(), err)
				continue
			}

			assert.Contains(t, expectedRevisions[packageName], gitpr.Spec.Revision)
		}
	}
}

func TestDiscoverWithBadKptAnnotationFromNestedRepositoryFromUnrelatedSubRepository(t *testing.T) {
	ctx := context.Background()
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "nested-repository-with-invalid-kpt-annotation.tar")
	_, address := ServeGitRepository(t, tarfile, tempdir)

	const (
		repositoryName = "blueprint2"
		directory      = "blueprint2"
		namespace      = "default"
		deployment     = false
		packageName    = "bp10"
	)

	expectedRevisions := []int{-1, 1}

	git, err := OpenRepository(ctx, repositoryName, namespace, &configapi.GitRepository{
		Repo:      address,
		Directory: directory,
	}, deployment, tempdir, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q and directory %q: %v", tarfile, directory, err)
	}

	prs, err := git.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		t.Fatalf("ListPackageRevisions failed: %v", err)
	}

	for _, pr := range prs {
		gitpr, err := pr.GetPackageRevision(ctx)
		if err != nil {
			t.Errorf("GetPackageRevision failed for %q: %v", pr.KubeObjectName(), err)
			continue
		}

		assert.Contains(t, expectedRevisions, gitpr.Spec.Revision)
		assert.Equal(t, packageName, gitpr.Spec.PackageName)
	}
}

type mockK8sUsp struct{}

func (*mockK8sUsp) GetUserInfo(context.Context) *repository.UserInfo {
	return &repository.UserInfo{
		Name:  k8sAdmin,
		Email: k8sAdmin,
	}
}

func createAndPublishPR(ctx context.Context, repo repository.Repository, pr *porchapi.PackageRevision) (repository.PackageRevision, error) {
	draft, err := repo.CreatePackageRevisionDraft(ctx, pr)
	if err != nil {
		return nil, pkgerrors.Wrap(err, "Failed to create package revision draft")
	}
	if err := draft.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished); err != nil {
		return nil, pkgerrors.Wrap(err, "Failed to update draft lifecycle")
	}

	prr, err := repo.ClosePackageRevisionDraft(ctx, draft, repository.Revision2Int(pr.Spec.WorkspaceName))
	if err != nil {
		return nil, pkgerrors.Wrap(err, "Failed to finalize draft")
	}

	return prr, nil
}

func findCommitWithAuthorAndEmail(t *testing.T, repo *gogit.Repository, author, email string) error {
	t.Logf("Looking for commit with author %q and email %q", author, email)
	log, err := repo.Log(&gogit.LogOptions{Order: gogit.LogOrderCommitterTime})
	if err != nil {
		return pkgerrors.Wrap(err, "Failed to walk commits")
	}

	for commit, err := log.Next(); err == nil; commit, err = log.Next() {
		if commit.Author.Name == author && commit.Author.Email == email {
			t.Logf("Commit found (%q)", commit.Hash)
			return nil
		} else {
			t.Logf("Rejecting commit: %v", commit)
		}
	}

	return pkgerrors.Errorf("Failed to find commit with author %q, email %q", author, email)
}

func TestCommitAuthor(t *testing.T) {
	const (
		author = "porch"
		email  = "porch@example.com"

		repoName   = "repo"
		namespace1 = "default"
		deployment = false

		packageName = "package"
		workspace   = "v1"
	)

	ctx := context.Background()

	testCases := []struct {
		author   string
		email    string
		expected repository.UserInfo
	}{
		{
			author: "",
			email:  "",
			expected: repository.UserInfo{
				Name:  k8sAdmin,
				Email: k8sAdmin,
			},
		},
		{
			author: author,
			email:  "",
			expected: repository.UserInfo{
				Name:  author,
				Email: "",
			},
		},
		{
			author: "",
			email:  email,
			expected: repository.UserInfo{
				Name:  email,
				Email: email,
			},
		},
		{
			author: author,
			email:  email,
			expected: repository.UserInfo{
				Name:  author,
				Email: email,
			},
		},
	}

	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")
	remotepath := filepath.Join(tempdir, "remote")
	gitRepo, address := ServeGitRepository(t, tarfile, remotepath)

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%q,%q", tc.author, tc.email), func(t *testing.T) {
			localpath := filepath.Join(tempdir, fmt.Sprintf("local-%d", i))
			repoSpec := &configapi.GitRepository{
				Repo:   address,
				Author: tc.author,
				Email:  tc.email,
			}
			repo, err := OpenRepository(ctx,
				repoName,
				namespace1,
				repoSpec,
				deployment,
				localpath,
				GitRepositoryOptions{
					ExternalRepoOptions: extrepo.ExternalRepoOptions{
						UserInfoProvider:           makeUserInfoProvider(repoSpec, &mockK8sUsp{}),
						RepoOperationRetryAttempts: 3,
					},
				},
			)
			if err != nil {
				t.Fatalf("Failed to open Git repository loaded from %q: %v", remotepath, err)
			}

			pr := &porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: repoName,
				},
				Spec: porchapi.PackageRevisionSpec{
					PackageName:    packageName,
					WorkspaceName:  workspace,
					RepositoryName: repoName,
					Tasks: []porchapi.Task{
						{
							Type: porchapi.TaskTypeInit,
							Init: &porchapi.PackageInitTaskSpec{
								Description: "Empty Package",
							},
						},
					},
				},
			}

			ppr, err := createAndPublishPR(ctx, repo, pr)
			if err != nil {
				t.Fatalf("Failed to create and publish package revision: %v", err)
			}

			if err := findCommitWithAuthorAndEmail(t, gitRepo, tc.expected.Name, tc.expected.Email); err != nil {
				t.Errorf("Failed to find commit with correct author: %v", err)
			}

			if err := repo.DeletePackageRevision(ctx, ppr); err != nil {
				t.Errorf("Failed to delete package revision: %v", err)
			}
		})
	}
}

func TestMultipleCommitAuthors(t *testing.T) {
	const (
		author1 = "porch"
		email1  = "porch@example.com"

		author2 = "hcrop"
		email2  = "hcrop@example.com"

		repo1Name  = "repo1"
		repo2Name  = "repo2"
		namespace  = "default"
		deployment = false

		packageName = "package"
		workspace1  = "v1"
		workspace2  = "v2"
	)

	ctx := context.Background()

	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")
	remotepath := filepath.Join(tempdir, "remote")
	gitRepo, address := ServeGitRepository(t, tarfile, remotepath)

	localpath1 := filepath.Join(tempdir, "local-1")
	repoSpec1 := &configapi.GitRepository{
		Repo:   address,
		Author: author1,
		Email:  email1,
	}
	repo1, err := OpenRepository(ctx,
		repo1Name,
		namespace,
		repoSpec1,
		deployment,
		localpath1,
		GitRepositoryOptions{
			ExternalRepoOptions: extrepo.ExternalRepoOptions{
				UserInfoProvider:           makeUserInfoProvider(repoSpec1, &mockK8sUsp{}),
				RepoOperationRetryAttempts: 3,
			},
		},
	)

	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q: %v", remotepath, err)
	}

	localpath2 := filepath.Join(tempdir, "local-2")
	repoSpec2 := &configapi.GitRepository{
		Repo:   address,
		Author: author2,
		Email:  email2,
	}
	repo2, err := OpenRepository(ctx,
		repo2Name,
		namespace,
		repoSpec2,
		deployment,
		localpath2,
		GitRepositoryOptions{
			ExternalRepoOptions: extrepo.ExternalRepoOptions{
				UserInfoProvider:           makeUserInfoProvider(repoSpec2, &mockK8sUsp{}),
				RepoOperationRetryAttempts: 3,
			},
		},
	)

	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q: %v", remotepath, err)
	}

	pr1 := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: repo1Name,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    packageName,
			WorkspaceName:  workspace1,
			RepositoryName: repo1Name,
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeInit,
					Init: &porchapi.PackageInitTaskSpec{
						Description: "Empty Package",
					},
				},
			},
		},
	}

	prv1, err := createAndPublishPR(ctx, repo1, pr1)
	if err != nil {
		t.Fatalf("Failed to create and publish package revision: %v", err)
	}
	t.Cleanup(func() {
		if err := repo1.DeletePackageRevision(ctx, prv1); err != nil {
			t.Logf("Failed to delete package revision: %v", err)
		}
	})

	pr2 := pr1.DeepCopy()
	pr2.GenerateName = repo2Name
	pr2.Spec.WorkspaceName = workspace2
	pr2.Spec.RepositoryName = repo2Name
	pr2.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeEdit,
			Edit: &porchapi.PackageEditTaskSpec{
				Source: &porchapi.PackageRevisionRef{
					Name: prv1.KubeObjectName(),
				},
			},
		},
	}

	prv2, err := createAndPublishPR(ctx, repo2, pr2)
	if err != nil {
		t.Fatalf("Failed to create v2: %v", err)
	}

	t.Cleanup(func() {
		if err := repo2.DeletePackageRevision(ctx, prv2); err != nil {
			t.Logf("Failed to delete v2: %v", err)
		}
	})

	if err := findCommitWithAuthorAndEmail(t, gitRepo, author1, email1); err != nil {
		t.Errorf("Failed to find commit with correct author: %v", err)
	}

	if err := findCommitWithAuthorAndEmail(t, gitRepo, author2, email2); err != nil {
		t.Errorf("Failed to find commit with correct author: %v", err)
	}
}

func TestApproveOnManuallyMovedMain(t *testing.T) {
	const (
		repoName       = "approve-on-manually-moved-repo"
		namespace      = "default"
		packageName    = "approve-on-manually-moved-pkg"
		workspace      = "approve-on-manually-moved-ws"
		newFile        = "new-file.md"
		newFileContent = "new-file-content"
	)

	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")
	remotepath := filepath.Join(tempdir, "remote")
	localpath := filepath.Join(tempdir, "local")
	gitRepo, address := ServeGitRepository(t, tarfile, remotepath)

	repoSpec := &configapi.GitRepository{
		Repo: address,
	}

	ctx := context.Background()

	localRepo, err := OpenRepository(ctx, repoName, namespace, repoSpec, false, localpath, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q: %v", remotepath, err)
	}

	pr := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: repoName,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    packageName,
			WorkspaceName:  workspace,
			RepositoryName: repoName,
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeInit,
					Init: &porchapi.PackageInitTaskSpec{
						Description: "Empty Package",
					},
				},
			},
		},
	}

	draft1, err := localRepo.CreatePackageRevisionDraft(ctx, pr)
	if err != nil {
		t.Fatalf("Failed to create draft PackageRevision %v", err)
	}

	err = draft1.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished)

	if err != nil {
		t.Fatalf("Failed to create commit: %v", err)
	}

	uip := makeUserInfoProvider(repoSpec, &mockK8sUsp{})
	mainBranchCommitHash := resolveReference(t, gitRepo, defaultMainReferenceName).Hash()
	ch, err := newCommitHelper(gitRepo, uip, mainBranchCommitHash, "", plumbing.ZeroHash)
	if err != nil {
		t.Fatalf("Failed to create commit helper: %v", err)
	}

	err = ch.storeFile(newFile, newFileContent)
	if err != nil {
		t.Fatalf("Failed to store new file: %v", err)
	}

	newHash, _, err := ch.commit(ctx, "Add new file", "")

	if err != nil {
		t.Fatalf("Failed to create commit: %v", err)
	}

	err = gitRepo.Storer.SetReference(plumbing.NewHashReference(defaultMainReferenceName, newHash))
	if err != nil {
		t.Fatalf("Failed to set reference: %v", err)
	}
	t.Logf("Moved %s from %s to %s", defaultMainReferenceName, mainBranchCommitHash, newHash)

	_, err = localRepo.ClosePackageRevisionDraft(ctx, draft1, 1)

	if err != nil {
		t.Fatalf("Failed to close draft PackageRevision: %v", err)
	}
}

// Be careful with the refernce handing in this test case, it's easy to turn the test to
// not actually test manually moved remotes.
func TestDeleteOnManuallyMovedTag(t *testing.T) {
	const (
		repoName       = "delete-on-manually-moved-repo"
		namespace      = "default"
		packageName    = "delete-on-manually-moved-pkg"
		workspace      = "delete-on-manually-moved-ws"
		newFile        = "new-file.md"
		newFileContent = "new-file-content"
	)

	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")
	remotepath := filepath.Join(tempdir, "remote")
	localpath := filepath.Join(tempdir, "local")
	gitRepo, address := ServeGitRepository(t, tarfile, remotepath)

	repoSpec := &configapi.GitRepository{
		Repo: address,
	}

	ctx := context.Background()

	localRepo, err := OpenRepository(ctx, repoName, namespace, repoSpec, false, localpath, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q: %v", remotepath, err)
	}

	pr1 := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: repoName,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    packageName,
			WorkspaceName:  workspace,
			RepositoryName: repoName,
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeInit,
					Init: &porchapi.PackageInitTaskSpec{
						Description: "Empty Package",
					},
				},
			},
		},
	}

	prv1, err := createAndPublishPR(ctx, localRepo, pr1)
	if err != nil {
		t.Fatalf("Failed to create PackageRevision: %v", err)
	}

	t.Logf("Created Published PackageRevision %s", prv1.Key())

	uip := makeUserInfoProvider(repoSpec, &mockK8sUsp{})
	ch, err := newCommitHelper(gitRepo, uip, prv1.(*gitPackageRevision).commit, "", plumbing.ZeroHash)
	if err != nil {
		t.Fatalf("Failed to create commit helper: %v", err)
	}
	err = ch.storeFile(newFile, newFileContent)
	if err != nil {
		t.Fatalf("Failed to store new file: %v", err)
	}
	newHash, _, err := ch.commit(ctx, "Add new file", "")
	if err != nil {
		t.Fatalf("Failed to create commit: %v", err)
	}
	err = gitRepo.Storer.SetReference(plumbing.NewHashReference(prv1.(*gitPackageRevision).ref.Name(), newHash))
	if err != nil {
		t.Fatalf("Failed to set reference: %v", err)
	}
	t.Logf("Moved %s from %s to %s", prv1.(*gitPackageRevision).ref, prv1.(*gitPackageRevision).commit, newHash)

	t.Logf("Trying to delete published PackageRevision with a remote that's moved %s", prv1.Key())
	if err := localRepo.DeletePackageRevision(ctx, prv1); err != nil {
		t.Fatalf("Failed to delete PackageRevision: %v", err)
	}
}

// Be careful with the refernce handing in this test case, it's easy to turn the test to
// not actually test manually moved remotes.
func TestDeleteManuallyMovedNonApproved(t *testing.T) {
	const (
		repoName  = "delete-on-manually-moved-draft-repo"
		namespace = "default"
	)

	tests := map[string]struct {
		packageName    string
		workspace      string
		newFile        string
		newFileContent string
		apiPr          *porchapi.PackageRevision
	}{
		"delete on manually moved draft": {
			packageName:    "delete-on-manually-moved-draft-pkg",
			workspace:      "delete-on-manually-moved-draft-ws",
			newFile:        "new-file.md",
			newFileContent: "new-file-content",
			apiPr: &porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: repoName,
				},
				Spec: porchapi.PackageRevisionSpec{
					PackageName:    "delete-on-manually-moved-draft-pkg",
					WorkspaceName:  "delete-on-manually-moved-draft-ws",
					RepositoryName: repoName,
					Lifecycle:      porchapi.PackageRevisionLifecycleDraft,
					Tasks: []porchapi.Task{
						{
							Type: porchapi.TaskTypeInit,
							Init: &porchapi.PackageInitTaskSpec{
								Description: "Empty Package",
							},
						},
					},
				},
			},
		},
		"delete on manually moved proposed": {
			packageName:    "delete-on-manually-moved-proposed-pkg",
			workspace:      "delete-on-manually-moved-proposed-ws",
			newFile:        "new-file.md",
			newFileContent: "new-file-content",
			apiPr: &porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: repoName,
				},
				Spec: porchapi.PackageRevisionSpec{
					PackageName:    "delete-on-manually-moved-proposed-pkg",
					WorkspaceName:  "delete-on-manually-moved-proposed-ws",
					RepositoryName: repoName,
					Lifecycle:      porchapi.PackageRevisionLifecycleProposed,
					Tasks: []porchapi.Task{
						{
							Type: porchapi.TaskTypeInit,
							Init: &porchapi.PackageInitTaskSpec{
								Description: "Empty Package",
							},
						},
					},
				},
			},
		},
	}

	ctx := context.Background()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")

	for tn, test := range tests {
		t.Run(tn, func(t *testing.T) {
			tempdir := t.TempDir()
			remotepath := filepath.Join(tempdir, "remote")
			localpath := filepath.Join(tempdir, "local")
			gitRepo, address := ServeGitRepository(t, tarfile, remotepath)

			repoSpec := &configapi.GitRepository{
				Repo: address,
			}

			localRepo, err := OpenRepository(ctx, repoName, namespace, repoSpec, false, localpath, testGitRepositoryOptions())
			if err != nil {
				t.Fatalf("Failed to open Git repository loaded from %q: %v", remotepath, err)
			}
			t.Cleanup(func() {
				localRepo.Close(ctx)
			})
			draft, err := localRepo.CreatePackageRevisionDraft(ctx, test.apiPr)
			if err != nil {
				t.Fatalf("Failed to create PackageRevision draft: %v", err)
			}

			savedPr, err := localRepo.ClosePackageRevisionDraft(ctx, draft, 0)
			if err != nil {
				t.Fatalf("Failed to close PackageRevision draft: %v", err)
			}

			t.Logf("Created draft PackageRevision %s", savedPr.Key())

			uip := makeUserInfoProvider(repoSpec, &mockK8sUsp{})
			ch, err := newCommitHelper(gitRepo, uip, savedPr.(*gitPackageRevision).commit, "", plumbing.ZeroHash)
			if err != nil {
				t.Fatalf("Failed to create commit helper: %v", err)
			}
			err = ch.storeFile(test.newFile, test.newFileContent)
			if err != nil {
				t.Fatalf("Failed to store new file: %v", err)
			}
			newHash, _, err := ch.commit(ctx, "Add new file", "")
			if err != nil {
				t.Fatalf("Failed to create commit: %v", err)
			}
			refNameInRemote, ok := getBranchNameInLocalRepo(savedPr.(*gitPackageRevision).ref.Name())
			if !ok {
				t.Fatalf("Invalid draft ref name: %q", savedPr.(*gitPackageRevision).ref.Name())
			}

			err = gitRepo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(refNameInRemote), newHash))
			if err != nil {
				t.Fatalf("Failed to set new remote ref: %v", err)
			}
			t.Logf("Moved %s from %s to %s", savedPr.(*gitPackageRevision).ref, savedPr.(*gitPackageRevision).commit, newHash)

			t.Logf("Trying to delete published PackageRevision with a remote that's moved %s", savedPr.Key())
			if err := localRepo.DeletePackageRevision(ctx, savedPr); err != nil {
				t.Fatalf("Failed to delete PackageRevision: %v", err)
			}
		})
	}
}

// Be careful with the refernce handing in this test case, it's easy to turn the test to
// not actually test manually moved remotes.
func TestDeleteOnManuallyMovedMainBranch(t *testing.T) {
	const (
		repoName       = "delete-on-manually-moved-main-repo"
		namespace      = "default"
		packageName    = "delete-on-manually-moved-main-pkg"
		workspace      = "delete-on-manually-moved-main-ws"
		newFile        = "new-file.md"
		newFileContent = "new-file-content"
	)

	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")
	remotepath := filepath.Join(tempdir, "remote")
	localpath := filepath.Join(tempdir, "local")
	gitRepo, address := ServeGitRepository(t, tarfile, remotepath)

	repoSpec := &configapi.GitRepository{
		Repo: address,
	}

	ctx := context.Background()

	localRepo, err := OpenRepository(ctx, repoName, namespace, repoSpec, false, localpath, testGitRepositoryOptions())
	if err != nil {
		t.Fatalf("Failed to open Git repository loaded from %q: %v", remotepath, err)
	}
	t.Cleanup(func() {
		localRepo.Close(ctx)
	})

	pr1 := &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: repoName,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    packageName,
			WorkspaceName:  workspace,
			RepositoryName: repoName,
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeInit,
					Init: &porchapi.PackageInitTaskSpec{
						Description: "Empty Package",
					},
				},
			},
		},
	}

	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				"Kptfile": Kptfile,
			},
		},
	}

	draft, err := localRepo.CreatePackageRevisionDraft(ctx, pr1)
	if err != nil {
		t.Fatalf("Failed to create PackageRevision draft: %v", err)
	}

	err = draft.UpdateResources(ctx, resources, nil)
	if err != nil {
		t.Fatalf("Failed to update draft resources: %v", err)
	}

	err = draft.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished)
	if err != nil {
		t.Fatalf("Failed to update draft lifecycle: %v", err)
	}

	prv1, err := localRepo.ClosePackageRevisionDraft(ctx, draft, repository.Revision2Int(pr1.Spec.WorkspaceName))
	if err != nil {
		t.Fatalf("Failed to finalize draft: %v", err)
	}

	t.Logf("Created Published PackageRevision %s", prv1.Key())

	uip := makeUserInfoProvider(repoSpec, &mockK8sUsp{})
	ch, err := newCommitHelper(gitRepo, uip, prv1.(*gitPackageRevision).commit, "", plumbing.ZeroHash)
	if err != nil {
		t.Fatalf("Failed to create commit helper: %v", err)
	}
	err = ch.storeFile(newFile, newFileContent)
	if err != nil {
		t.Fatalf("Failed to store new file: %v", err)
	}
	newHash, _, err := ch.commit(ctx, "Add new file", "")
	if err != nil {
		t.Fatalf("Failed to create commit: %v", err)
	}
	err = gitRepo.Storer.SetReference(plumbing.NewHashReference(plumbing.Main, newHash))
	if err != nil {
		t.Fatalf("Failed to set reference: %v", err)
	}
	t.Logf("Moved %s from %s to %s", plumbing.Main, prv1.(*gitPackageRevision).commit, newHash)

	t.Logf("Trying to delete published PackageRevision with a remote that's moved %s", prv1.Key())
	if err := localRepo.DeletePackageRevision(ctx, prv1.ToMainPackageRevision(ctx)); err != nil {
		t.Fatalf("Failed to delete PackageRevision: %v", err)
	}
}

func TestFormatCommitMessage(t *testing.T) {
	tests := []struct {
		name       string
		changeType porchapi.TaskType
		want       string
	}{
		{
			name:       "render commit",
			changeType: "render",
			want:       "Rendering package",
		},
		{
			name:       "edit commit",
			changeType: "edit",
			want:       "Creating new revision by copying previous revision",
		},
		{
			name:       "init commit",
			changeType: "init",
			want:       "Creating new empty revision",
		},
		{
			name:       "clone commit",
			changeType: "clone",
			want:       "Creating new revision by cloning",
		},
		{
			name:       "unknown type",
			changeType: "unknown",
			want:       "Intermediate commit: unknown",
		},
		{
			name:       "empty type",
			changeType: "",
			want:       "Intermediate commit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCommitMessage(tt.changeType); got != tt.want {
				t.Errorf("formatCommitMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPushRetryWithTransientError(t *testing.T) {
	const (
		repoName  = "push-permission-test-repo"
		namespace = "default"
	)

	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")
	remotepath := filepath.Join(tempdir, "remote")
	localpath := filepath.Join(tempdir, "local")
	_, address := ServeGitRepository(t, tarfile, remotepath)

	repoSpec := &configapi.GitRepository{
		Repo: address,
	}

	ctx := context.Background()

	opts := testGitRepositoryOptions()
	opts.RepoOperationRetryAttempts = 4

	localRepo, err := OpenRepository(ctx, repoName, namespace, repoSpec, false, localpath, opts)
	require.NoError(t, err, "Failed to open Git repository")
	t.Cleanup(func() {
		localRepo.Close(ctx)
	})

	testrepo := localRepo.(*gitRepository)

	// Create a new commit to push
	var newCommitHash plumbing.Hash
	err = testrepo.sharedDir.withLock(func(repo *gogit.Repository) error {
		mainRef, err := repo.Reference(testrepo.branch.refInLocal(), true)
		if err != nil {
			return err
		}

		uip := makeUserInfoProvider(repoSpec, &mockK8sUsp{})
		ch, err := newCommitHelper(repo, uip, mainRef.Hash(), "", plumbing.ZeroHash)
		if err != nil {
			return err
		}

		err = ch.storeFile("test-file.txt", "test content")
		if err != nil {
			return err
		}

		newCommitHash, _, err = ch.commit(ctx, "Test commit", "")
		return err
	})
	require.NoError(t, err, "Failed to create new commit")

	// Make remote directory read-only BEFORE push to cause errors
	err = os.Chmod(remotepath, 0444)
	require.NoError(t, err, "Failed to change permissions")

	// Restore permissions after delay to allow retry to succeed
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Chmod(remotepath, 0755)
	}()

	refSpecs := newPushRefSpecBuilder()
	refSpecs.addRefToPush(newCommitHash, testrepo.branch.refInLocal())

	err = testrepo.pushAndCleanup(ctx, refSpecs, nil)

	assert.NoError(t, err, "Push should have succeeded after retries")

	// Ensure permissions are restored
	os.Chmod(remotepath, 0755)
}

func TestPushFailures(t *testing.T) {
	tests := []struct {
		name      string
		chmodPath func(remotepath string) string
		restoreFn func(path string)
	}{
		{
			name: "objects directory inaccessible",
			chmodPath: func(remotepath string) string {
				return filepath.Join(remotepath, ".git", "objects")
			},
			restoreFn: func(path string) {
				os.Chmod(path, 0755)
			},
		},
		{
			name: "remote directory inaccessible",
			chmodPath: func(remotepath string) string {
				return remotepath
			},
			restoreFn: func(path string) {
				os.Chmod(path, 0755)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempdir := t.TempDir()
			tarfile := filepath.Join("testdata", "trivial-repository.tar")
			remotepath := filepath.Join(tempdir, "remote")
			localpath := filepath.Join(tempdir, "local")
			_, address := ServeGitRepository(t, tarfile, remotepath)

			repoSpec := &configapi.GitRepository{
				Repo: address,
			}

			ctx := context.Background()
			opts := testGitRepositoryOptions()
			opts.RepoOperationRetryAttempts = 3

			localRepo, err := OpenRepository(ctx, "push-test-repo", "default", repoSpec, false, localpath, opts)
			require.NoError(t, err, "Failed to open Git repository")
			t.Cleanup(func() {
				localRepo.Close(ctx)
			})

			testrepo := localRepo.(*gitRepository)

			var newCommitHash plumbing.Hash
			err = testrepo.sharedDir.withLock(func(repo *gogit.Repository) error {
				mainRef, err := repo.Reference(testrepo.branch.refInLocal(), true)
				if err != nil {
					return err
				}

				uip := makeUserInfoProvider(repoSpec, &mockK8sUsp{})
				ch, err := newCommitHelper(repo, uip, mainRef.Hash(), "", plumbing.ZeroHash)
				if err != nil {
					return err
				}

				err = ch.storeFile("test-file.txt", "test content")
				if err != nil {
					return err
				}

				newCommitHash, _, err = ch.commit(ctx, "Test commit", "")
				return err
			})
			require.NoError(t, err, "Failed to create new commit")

			targetPath := tt.chmodPath(remotepath)
			err = os.Chmod(targetPath, 0000)
			require.NoError(t, err, "Failed to change permissions")
			t.Cleanup(func() {
				tt.restoreFn(targetPath)
			})

			refSpecs := newPushRefSpecBuilder()
			refSpecs.addRefToPush(newCommitHash, testrepo.branch.refInLocal())

			err = testrepo.pushAndCleanup(ctx, refSpecs, nil)

			assert.Error(t, err, "Expected push to fail")
		})
	}
}

func TestAppendRetryableErrors(t *testing.T) {
	// Copy to avoid test pollution
	original := make([]string, len(retryableErrors))
	copy(original, retryableErrors)
	defer func() {
		retryableErrors = original
	}()

	tests := []struct {
		name     string
		patterns []string
		want     []string
	}{
		{
			name:     "patterns without spaces",
			patterns: []string{"test error", "another error"},
			want:     []string{"test error", "another error"},
		},
		{
			name:     "patterns with leading spaces",
			patterns: []string{" test error", " another error"},
			want:     []string{"test error", "another error"},
		},
		{
			name:     "patterns with trailing spaces",
			patterns: []string{"test error ", "another error "},
			want:     []string{"test error", "another error"},
		},
		{
			name:     "patterns with both leading and trailing spaces",
			patterns: []string{" test error ", " another error "},
			want:     []string{"test error", "another error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset to original state before each test
			retryableErrors = make([]string, len(original))
			copy(retryableErrors, original)

			initialLen := len(retryableErrors)
			AppendRetryableErrors(tt.patterns)

			// Verify the patterns were added
			assert.Equal(t, initialLen+len(tt.want), len(retryableErrors), "Expected patterns count")

			// Verify the last added patterns match expected (trimmed)
			for i, want := range tt.want {
				got := retryableErrors[initialLen+i]
				assert.Equal(t, want, got, "Pattern %d", i)
			}
		})
	}
}
