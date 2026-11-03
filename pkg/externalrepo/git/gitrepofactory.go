// Copyright 2025 The kpt Authors
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
	"errors"
	"fmt"
	"path/filepath"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
)

var _ externalrepotypes.ExternalRepoFactory = &GitRepoFactory{}

type GitRepoFactory struct {
}

func (f *GitRepoFactory) NewRepositoryImpl(ctx context.Context, repositorySpec *configapi.Repository, options externalrepotypes.ExternalRepoOptions) (repository.Repository, error) {
	if repositorySpec.Spec.Git == nil {
		return nil, errors.New("git property is required")
	}
	if repositorySpec.Spec.Git.Repo == "" {
		return nil, errors.New("git.repo property is required")
	}

	var mbs MainBranchStrategy
	if repositorySpec.Spec.Git.CreateBranch {
		mbs = CreateIfMissing
	} else {
		mbs = ErrorIfMissing
	}

	repo, err := OpenRepository(ctx,
		repositorySpec.Name,
		repositorySpec.Namespace,
		repositorySpec.Spec.Git,
		repositorySpec.Spec.Deployment,
		filepath.Join(options.LocalDirectory, "git"),
		GitRepositoryOptions{
			ExternalRepoOptions: options,
			MainBranchStrategy:  mbs,
		})
	if err != nil {
		return nil, err
	}

	return repo, nil
}

func (f *GitRepoFactory) CheckRepositoryConnection(ctx context.Context, repositorySpec *configapi.Repository, options externalrepotypes.ExternalRepoOptions) error {
	if err := f.validateRepositorySpec(repositorySpec); err != nil {
		return err
	}

	branch := repositorySpec.Spec.Git.Branch
	if branch == "" {
		branch = "main"
	}

	auth, err := resolveGitAuth(ctx, repositorySpec, options)
	if err != nil {
		return err
	}

	remote := gogit.NewRemote(nil, &config.RemoteConfig{
		Name: "origin",
		URLs: []string{repositorySpec.Spec.Git.Repo},
	})

	refs, err := listRemoteRefs(remote, auth)
	if err != nil {
		return err
	}

	branchExists := false
	for _, ref := range refs {
		if ref.Name().IsBranch() && ref.Name().Short() == branch {
			branchExists = true
			break
		}
	}

	if !branchExists && !repositorySpec.Spec.Git.CreateBranch {
		return fmt.Errorf("branch %q not found in repository", branch)
	}

	return nil
}

// resolveGitAuth resolves the auth method for a git repository from its secret
// reference, if any. Returns nil auth (and nil error) when no secret is configured.
func resolveGitAuth(ctx context.Context, repositorySpec *configapi.Repository, options externalrepotypes.ExternalRepoOptions) (transport.AuthMethod, error) {
	secretName := repositorySpec.Spec.Git.SecretRef.Name
	if secretName == "" {
		return nil, nil
	}
	creds, err := options.CredentialResolver.ResolveCredential(ctx, repositorySpec.Namespace, secretName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve credentials: %w", err)
	}
	if !creds.Valid() {
		return nil, fmt.Errorf("resolved credentials are invalid")
	}
	return creds.ToAuthMethod(), nil
}

// listRemoteRefs lists the remote's refs, retrying without authentication when an
// authenticated attempt fails but no credentials were available (public repos).
func listRemoteRefs(remote *gogit.Remote, auth transport.AuthMethod) ([]*plumbing.Reference, error) {
	refs, err := remote.List(&gogit.ListOptions{Auth: auth, Timeout: 20})
	if err != nil {
		if auth != nil {
			return nil, fmt.Errorf("failed to list remote refs with authentication: %w", err)
		}
		refs, err = remote.List(&gogit.ListOptions{Timeout: 20})
		if err != nil {
			return nil, fmt.Errorf("failed to list remote refs: %w", err)
		}
	}
	return refs, nil
}

// ValidateRepositoryHistory verifies that the remote the repository now points at
// is the same repository Porch has already synced, by checking that the commit
// recorded in status.gitCommitHash is still present in the history of the
// configured branch. This guards the rehoming case: changing spec.git.repo to a
// URL whose branch does not contain the last-synced commit (a different or
// divergent repository) is rejected.
//
// This is intentionally separate from CheckRepositoryConnection — it only runs on
// full syncs, not on the frequent lightweight connectivity/health checks, because
// it may fetch the branch history to determine ancestry.
//
// The recorded commit does NOT have to be the current branch HEAD: the branch
// legitimately moves forward between syncs (every publish or Porch commit advances
// it). It only has to be reachable from the HEAD. The check is skipped when no
// hash has been recorded yet (a freshly registered repository that has never
// synced).
func (f *GitRepoFactory) ValidateRepositoryHistory(ctx context.Context, repositorySpec *configapi.Repository, options externalrepotypes.ExternalRepoOptions) error {
	if err := f.validateRepositorySpec(repositorySpec); err != nil {
		return err
	}

	registeredHash := repositorySpec.Status.GitCommitHash
	if registeredHash == "" {
		return nil
	}

	branch := repositorySpec.Spec.Git.Branch
	if branch == "" {
		branch = "main"
	}

	auth, err := resolveGitAuth(ctx, repositorySpec, options)
	if err != nil {
		return err
	}

	remote := gogit.NewRemote(nil, &config.RemoteConfig{
		Name: "origin",
		URLs: []string{repositorySpec.Spec.Git.Repo},
	})

	// Fast path: if the branch HEAD already equals the recorded hash (the common
	// case immediately after a sync), it is trivially reachable — no fetch needed.
	refs, err := listRemoteRefs(remote, auth)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.Name().IsBranch() && ref.Name().Short() == branch {
			if ref.Hash().String() == registeredHash {
				return nil
			}
			break
		}
	}

	reachable, err := registeredHashReachable(remote, auth, branch, plumbing.NewHash(registeredHash))
	if err != nil {
		return fmt.Errorf("failed to verify registered commit %q is in the remote branch %q history: %w", registeredHash, branch, err)
	}
	if !reachable {
		return fmt.Errorf("repository hash %q registered on the repository is not present in the history of branch %q at the remote; the git URL appears to point at a different repository", registeredHash, branch)
	}

	return nil
}

// registeredHashReachable reports whether registeredHash is reachable from the
// HEAD of the given branch at the remote (i.e. it is an ancestor of, or equal
// to, the branch head). It fetches the branch's history into an in-memory
// repository and walks the commit graph.
func registeredHashReachable(remote *gogit.Remote, auth transport.AuthMethod, branch string, registeredHash plumbing.Hash) (bool, error) {
	branchRef := plumbing.NewBranchReferenceName(branch)
	refSpec := config.RefSpec(fmt.Sprintf("+%s:%s", branchRef, branchRef))

	// go-git needs an in-memory repo backed by the fetched objects to walk history.
	repo, err := gogit.Init(memory.NewStorage(), nil)
	if err != nil {
		return false, err
	}
	originRemote, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: remote.Config().URLs})
	if err != nil {
		return false, err
	}
	fetch := func(a transport.AuthMethod) error {
		return originRemote.Fetch(&gogit.FetchOptions{
			Auth:     a,
			RefSpecs: []config.RefSpec{refSpec},
			Tags:     gogit.NoTags,
		})
	}

	if err := fetch(auth); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		// Retry without auth for public repositories, mirroring remote.List above.
		if auth != nil {
			return false, fmt.Errorf("fetch with authentication failed: %w", err)
		}
		if err2 := fetch(nil); err2 != nil && !errors.Is(err2, gogit.NoErrAlreadyUpToDate) {
			return false, fmt.Errorf("fetch failed: %w", err2)
		}
	}

	headRef, err := repo.Reference(branchRef, true)
	if err != nil {
		return false, fmt.Errorf("resolving branch %q after fetch: %w", branch, err)
	}

	// The registered commit must exist and be an ancestor of (or equal to) HEAD.
	registeredCommit, err := repo.CommitObject(registeredHash)
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			// The commit isn't in this branch's history at all.
			return false, nil
		}
		return false, err
	}
	headCommit, err := repo.CommitObject(headRef.Hash())
	if err != nil {
		return false, err
	}
	return registeredCommit.IsAncestor(headCommit)
}

func (f *GitRepoFactory) validateRepositorySpec(repositorySpec *configapi.Repository) error {
	if repositorySpec == nil || repositorySpec.Spec.Git == nil {
		return fmt.Errorf("repositorySpec is nil or missing Git configuration")
	}
	if repositorySpec.Spec.Git.Repo == "" {
		return fmt.Errorf("repository URL is empty")
	}
	return nil
}
