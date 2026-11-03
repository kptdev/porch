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

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
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
	if parent := branchParentHash(t, gitRepo, branch); parent != "" {
		repoSpec = &configapi.Repository{
			Spec: configapi.RepositorySpec{
				Git: &configapi.GitRepository{Repo: address, Branch: branch},
			},
			Status: configapi.RepositoryStatus{GitCommitHash: parent},
		}
		err = gf.ValidateRepositoryHistory(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
		assert.NoError(t, err, "an ancestor of the branch head must be accepted (forward progress)")
	}

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

// branchParentHash returns the hash of the first parent of the branch HEAD, or
// "" if the HEAD commit has no parent (single-commit history).
func branchParentHash(t *testing.T, repo *gogit.Repository, branch string) string {
	t.Helper()
	head := resolveReference(t, repo, plumbing.NewBranchReferenceName(branch))
	commit := getCommitObject(t, repo, head.Hash())
	if commit.NumParents() == 0 {
		return ""
	}
	parent, err := commit.Parent(0)
	if err != nil {
		t.Fatalf("failed to get parent of %q: %v", head.Hash(), err)
	}
	return parent.Hash.String()
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
