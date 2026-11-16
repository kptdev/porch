/*
 Copyright 2025 The kpt Authors

 Licensed under the Apache License, Version 2.0 (the "License");
 You may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package git

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
)

func TestGitRepoFactory(t *testing.T) {

	gf := GitRepoFactory{}

	repoSpec := configapi.Repository{}
	_, err := gf.NewRepositoryImpl(context.TODO(), &repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, strings.Contains(err.Error(), "git property is required"))

	repoSpec.Spec = configapi.RepositorySpec{
		Git: &configapi.GitRepository{},
	}
	_, err = gf.NewRepositoryImpl(context.TODO(), &repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, strings.Contains(err.Error(), "git.repo property is required"))
	assert.True(t, err != nil)

	repoSpec.Spec.Git.Repo = "my-repo"
	repoSpec.Spec.Git.CreateBranch = true
	_, err = gf.NewRepositoryImpl(context.TODO(), &repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, err != nil)

	repoSpec.Spec.Git.Repo = "my-repo"
	repoSpec.Spec.Git.CreateBranch = false
	_, err = gf.NewRepositoryImpl(context.TODO(), &repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, err != nil)
}

func TestCheckRepositoryConnection(t *testing.T) {
	gf := &GitRepoFactory{}

	// nil repositorySpec
	err := gf.CheckRepositoryConnection(context.TODO(), nil, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, err != nil)
	assert.Contains(t, err.Error(), "repositorySpec is nil or missing Git configuration")

	// nil Git field
	repoSpec := &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: nil,
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, err != nil)
	assert.Contains(t, err.Error(), "repositorySpec is nil or missing Git configuration")

	// empty repo URL
	repoSpec.Spec.Git = &configapi.GitRepository{
		Repo:   "",
		Branch: "main",
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, err != nil)
	assert.Contains(t, err.Error(), "repository URL is empty")

	tempDir := t.TempDir()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")
	branch := "main"

	gitRepo, address := ServeGitRepositoryWithBranch(t, tarfile, tempDir, branch)

	// Resolve the actual commit hash of the served branch so the hash-match
	// cases below don't depend on the archive's exact contents.
	branchHash := resolveReference(t, gitRepo, plumbing.NewBranchReferenceName(branch)).Hash().String()

	// valid branch
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:   address,
				Branch: branch,
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err)

	// empty branch defaults to main
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:   address,
				Branch: "", // empty branch should default to main
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err)

	// branch not found but createBranch is set to true
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:         address,
				Branch:       "nonexistent-branch",
				CreateBranch: true,
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err)

	// branch not found
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:   address,
				Branch: "nonexistent-branch",
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `branch "nonexistent-branch" not found`)

	// empty secretName (no authentication)
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:      address,
				Branch:    branch,
				SecretRef: configapi.SecretRef{Name: ""},
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err)

	// remote list error
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:   "https://invalid-url.example.com/repo.git",
				Branch: "main",
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list remote refs")

	// registered GitCommitHash matches the remote branch head: connectivity OK
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:   address,
				Branch: branch,
			},
		},
		Status: configapi.RepositoryStatus{
			GitCommitHash: branchHash,
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err)

	// A recorded GitCommitHash is NOT validated by CheckRepositoryConnection; the
	// history check lives in ValidateRepositoryHistory (full sync only). So even a
	// bogus recorded hash must not make the connectivity check fail.
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:   address,
				Branch: branch,
			},
		},
		Status: configapi.RepositoryStatus{
			GitCommitHash: "0000000000000000000000000000000000000000",
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err, "connectivity check must not validate the commit hash")

	// Empty remote (reachable but no commits/refs): go-git reports
	// transport.ErrEmptyRemoteRepository from the ref listing. With createBranch=true
	// this must be permitted (the branch is initialized when the repo is opened).
	// This guards the full-sync-checks-connectivity-before-open path: an empty
	// remote with createBranch=true must pass connectivity and reach branch init.
	emptyDir := t.TempDir()
	emptyRepo := InitEmptyRepositoryWithWorktree(t, emptyDir)
	emptyAddress := ServeExistingRepository(t, emptyRepo)

	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:         emptyAddress,
				Branch:       "main",
				CreateBranch: true,
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err, "an empty remote with createBranch=true must pass connectivity")

	// Same empty remote WITHOUT createBranch: the configured branch genuinely does
	// not exist, so connectivity must still fail with branch-not-found.
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:   emptyAddress,
				Branch: "main",
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.Error(t, err, "an empty remote without createBranch must fail")
	assert.Contains(t, err.Error(), `branch "main" not found`)
}

func TestValidateRepositoryHistory(t *testing.T) {
	gf := &GitRepoFactory{}

	tempDir := t.TempDir()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")
	branch := "main"

	gitRepo, address := ServeGitRepositoryWithBranch(t, tarfile, tempDir, branch)
	branchHash := resolveReference(t, gitRepo, plumbing.NewBranchReferenceName(branch)).Hash().String()

	// Empty recorded hash (fresh registration): nothing to validate, no error.
	repoSpec := &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{Repo: address, Branch: branch},
		},
	}
	err := gf.ValidateRepositoryHistory(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err)

	// Recorded hash equals the branch HEAD: trivially reachable, no error.
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{Repo: address, Branch: branch},
		},
		Status: configapi.RepositoryStatus{GitCommitHash: branchHash},
	}
	err = gf.ValidateRepositoryHistory(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err)

	// Recorded hash is an ancestor of the branch HEAD (branch moved forward since
	// the last sync): still reachable, no error. This is the normal case the
	// migration/forward-progress scenarios rely on.
	//
	// trivial-repository.tar has a single root commit, so we first append a
	// descendant commit to advance the branch head. This makes the recorded
	// ancestor strictly older than HEAD, forcing ValidateRepositoryHistory past
	// its HEAD-equality fast path and through the real fetch + ancestry walk. The
	// assertion runs unconditionally: a regression that rejected any non-HEAD hash
	// would now fail here instead of silently skipping.
	ancestor := branchHash
	appendChildCommit(t, gitRepo, branch)
	newHead := resolveReference(t, gitRepo, plumbing.NewBranchReferenceName(branch)).Hash().String()
	assert.NotEqual(t, ancestor, newHead, "appended commit must advance the branch head")

	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{Repo: address, Branch: branch},
		},
		Status: configapi.RepositoryStatus{GitCommitHash: ancestor},
	}
	err = gf.ValidateRepositoryHistory(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err, "an ancestor of the branch head must be accepted (forward progress)")

	// Recorded hash is NOT in the branch's history: rejected. Guards rehoming to a
	// URL whose history diverges from what Porch has synced.
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{Repo: address, Branch: branch},
		},
		Status: configapi.RepositoryStatus{GitCommitHash: "0000000000000000000000000000000000000000"},
	}
	err = gf.ValidateRepositoryHistory(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not present in the history")

	// Empty remote with a recorded hash: the remote has no refs (ref listing
	// returns transport.ErrEmptyRemoteRepository, treated as an empty ref list),
	// so the recorded commit cannot be present. This must be rejected with the
	// same "not present in the history" message rather than a raw fetch error,
	// exercising the empty-remote branches of listRemoteRefs and
	// registeredHashReachable.
	emptyDir := t.TempDir()
	emptyRepo := InitEmptyRepositoryWithWorktree(t, emptyDir)
	emptyAddress := ServeExistingRepository(t, emptyRepo)
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{Repo: emptyAddress, Branch: branch},
		},
		Status: configapi.RepositoryStatus{GitCommitHash: branchHash},
	}
	err = gf.ValidateRepositoryHistory(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.Error(t, err, "an empty remote cannot contain the recorded commit")
	assert.Contains(t, err.Error(), "not present in the history")

	// Nil Git config: spec validation error is surfaced.
	err = gf.ValidateRepositoryHistory(context.TODO(), &configapi.Repository{
		Status: configapi.RepositoryStatus{GitCommitHash: branchHash},
	}, externalrepotypes.ExternalRepoOptions{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing Git configuration")

	// Credential resolution failure (secret ref set, resolver errors) is surfaced
	// before any remote access.
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:      address,
				Branch:    branch,
				SecretRef: configapi.SecretRef{Name: "creds"},
			},
		},
		Status: configapi.RepositoryStatus{GitCommitHash: branchHash},
	}
	err = gf.ValidateRepositoryHistory(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{
		CredentialResolver: &failingCredentialResolver{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve credentials")
}

// appendChildCommit creates a commit whose parent is the current branch HEAD and
// advances the branch reference to it, leaving the previous HEAD as a reachable
// ancestor. The child reuses the parent's tree (no worktree required), so the
// repository need not have been opened with a working tree. This lets a
// single-commit fixture exercise the ancestor (non-HEAD) branch of history
// validation.
func appendChildCommit(t *testing.T, repo *gogit.Repository, branch string) {
	t.Helper()

	branchRef := plumbing.NewBranchReferenceName(branch)
	head := resolveReference(t, repo, branchRef)
	parent := getCommitObject(t, repo, head.Hash())

	sig := object.Signature{
		Name:  "Test",
		Email: "test@kpt.dev",
		When:  time.Now(),
	}
	child := &object.Commit{
		Author:       sig,
		Committer:    sig,
		Message:      "forward-progress commit",
		TreeHash:     parent.TreeHash,
		ParentHashes: []plumbing.Hash{parent.Hash},
	}

	obj := repo.Storer.NewEncodedObject()
	if err := child.Encode(obj); err != nil {
		t.Fatalf("failed to encode child commit: %v", err)
	}
	childHash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		t.Fatalf("failed to store child commit: %v", err)
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(branchRef, childHash)); err != nil {
		t.Fatalf("failed to advance branch %q to child commit: %v", branch, err)
	}
}

// invalidCredential is a credential that resolves successfully but reports itself
// as invalid, exercising the !creds.Valid() branch of resolveGitAuth.
type invalidCredential struct{}

func (c *invalidCredential) Valid() bool                        { return false }
func (c *invalidCredential) ToAuthMethod() transport.AuthMethod { return nil }
func (c *invalidCredential) ToString() string                   { return "" }

type invalidCredentialResolver struct{}

func (r *invalidCredentialResolver) ResolveCredential(_ context.Context, _, _ string) (repository.Credential, error) {
	return &invalidCredential{}, nil
}

func TestResolveGitAuth(t *testing.T) {
	ctx := context.TODO()

	// No secret reference: returns nil auth and nil error (public repo path).
	repoNoSecret := &configapi.Repository{
		Spec: configapi.RepositorySpec{Git: &configapi.GitRepository{Repo: "https://example.com/repo.git"}},
	}
	auth, err := resolveGitAuth(ctx, repoNoSecret, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err)
	assert.Nil(t, auth)

	repoWithSecret := &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:      "https://example.com/repo.git",
				SecretRef: configapi.SecretRef{Name: "creds"},
			},
		},
	}

	// Resolver error: surfaced as a "failed to resolve credentials" error.
	_, err = resolveGitAuth(ctx, repoWithSecret, externalrepotypes.ExternalRepoOptions{
		CredentialResolver: &failingCredentialResolver{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve credentials")

	// Resolved but invalid credentials: rejected.
	_, err = resolveGitAuth(ctx, repoWithSecret, externalrepotypes.ExternalRepoOptions{
		CredentialResolver: &invalidCredentialResolver{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolved credentials are invalid")

	// Valid credentials: returns a non-nil auth method.
	auth, err = resolveGitAuth(ctx, repoWithSecret, externalrepotypes.ExternalRepoOptions{
		CredentialResolver: &mutableCredentialResolver{username: "u", password: "p"},
	})
	assert.NoError(t, err)
	assert.NotNil(t, auth)
}
