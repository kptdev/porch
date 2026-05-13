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
	"context"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBranchCommitHash(t *testing.T) {
	testCases := []struct {
		name           string
		tarfile        string
		branch         string
		expectHash     bool
		expectError    bool
		validateCommit bool
	}{
		{
			name:           "main branch with commits",
			tarfile:        "simple-repository.tar",
			branch:         "main",
			expectHash:     true,
			expectError:    false,
			validateCommit: true,
		},
		{
			name:        "empty repository",
			tarfile:     "empty-repository.tar",
			branch:      "main",
			expectHash:  false, // Empty repo has no commits
			expectError: false,
		},
		{
			name:        "non-existent branch",
			tarfile:     "simple-repository.tar",
			branch:      "non-existent-branch",
			expectHash:  true, // Falls back to main branch with SkipVerification
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			tempdir := t.TempDir()
			tarfile := filepath.Join("testdata", tc.tarfile)

			_, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, tc.branch)

			const (
				name      = "test-repo"
				namespace = "default"
			)

			spec := &configapi.GitRepository{
				Repo:   address,
				Branch: tc.branch,
			}

			opts := testGitRepositoryOptions()
			opts.MainBranchStrategy = SkipVerification

			repo, err := OpenRepository(ctx, name, namespace, spec, false, tempdir, opts)
			require.NoError(t, err)
			defer repo.Close(ctx)

			// Get commit hash
			hash, err := repo.BranchCommitHash(ctx)

			// Check error expectation
			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "branch", "Error should mention branch issue")
				return
			}
			require.NoError(t, err)

			// Check hash expectation
			if tc.expectHash {
				assert.NotEmpty(t, hash, "Expected non-empty commit hash")
				assert.Len(t, hash, 40, "Expected 40-character SHA-1 hash")
				assert.Regexp(t, "^[0-9a-f]{40}$", hash, "Expected valid hex hash")

				// Validate commit exists if requested
				if tc.validateCommit {
					gitRepo := repo.(*gitRepository)
					err := gitRepo.sharedDir.withRLock(func(r *gogit.Repository) error {
						_, err := r.CommitObject(plumbing.NewHash(hash))
						return err
					})
					assert.NoError(t, err, "Commit hash should be valid")
				}
			} else {
				assert.Empty(t, hash, "Expected empty commit hash")
			}
		})
	}
}

func TestBranchCommitHashConcurrency(t *testing.T) {
	ctx := context.Background()
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "simple-repository.tar")

	_, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, "main")

	const (
		name      = "test-repo"
		namespace = "default"
	)

	spec := &configapi.GitRepository{
		Repo:   address,
		Branch: "main",
	}

	opts := testGitRepositoryOptions()
	opts.MainBranchStrategy = SkipVerification

	repo, err := OpenRepository(ctx, name, namespace, spec, false, tempdir, opts)
	require.NoError(t, err)
	defer repo.Close(ctx)

	// Test concurrent reads
	const numGoroutines = 10
	results := make(chan string, numGoroutines)
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			hash, err := repo.BranchCommitHash(ctx)
			if err != nil {
				errors <- err
			} else {
				results <- hash
			}
		}()
	}

	// Collect results
	var hashes []string
	for i := 0; i < numGoroutines; i++ {
		select {
		case hash := <-results:
			hashes = append(hashes, hash)
		case err := <-errors:
			t.Fatalf("Unexpected error in concurrent read: %v", err)
		}
	}

	// All hashes should be identical
	require.Len(t, hashes, numGoroutines)
	firstHash := hashes[0]
	for _, hash := range hashes {
		assert.Equal(t, firstHash, hash, "All concurrent reads should return same hash")
	}
}

func TestBranchCommitHashAfterFetch(t *testing.T) {
	ctx := context.Background()
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "simple-repository.tar")

	_, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, "main")

	const (
		name      = "test-repo"
		namespace = "default"
	)

	spec := &configapi.GitRepository{
		Repo:   address,
		Branch: "main",
	}

	opts := testGitRepositoryOptions()
	opts.MainBranchStrategy = SkipVerification

	repo, err := OpenRepository(ctx, name, namespace, spec, false, tempdir, opts)
	require.NoError(t, err)
	defer repo.Close(ctx)

	// Get initial hash
	hash1, err := repo.BranchCommitHash(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, hash1)

	// Fetch (should not change hash since remote hasn't changed)
	gitRepo := repo.(*gitRepository)
	err = gitRepo.fetchRemoteRepositoryWithRetry(ctx)
	require.NoError(t, err)

	// Get hash again
	hash2, err := repo.BranchCommitHash(ctx)
	require.NoError(t, err)

	// Should be same hash
	assert.Equal(t, hash1, hash2, "Hash should not change after fetch with no remote changes")
}

func TestBranchCommitHashDifferentBranches(t *testing.T) {
	ctx := context.Background()
	tempdir := t.TempDir()
	tarfile := filepath.Join("testdata", "simple-repository.tar")

	_, address := ServeGitRepositoryWithBranch(t, tarfile, tempdir, "main")

	const (
		name      = "test-repo"
		namespace = "default"
	)

	spec := &configapi.GitRepository{
		Repo:   address,
		Branch: "main",
	}

	opts := testGitRepositoryOptions()
	opts.MainBranchStrategy = SkipVerification

	// Create repos pointing to same branch
	repo1, err := OpenRepository(ctx, name, namespace, spec, false, tempdir, opts)
	require.NoError(t, err)
	defer repo1.Close(ctx)

	repo2, err := OpenRepository(ctx, name+"2", namespace, spec, false, tempdir+"2", opts)
	require.NoError(t, err)
	defer repo2.Close(ctx)

	hash1, err := repo1.BranchCommitHash(ctx)
	require.NoError(t, err)

	hash2, err := repo2.BranchCommitHash(ctx)
	require.NoError(t, err)

	// Same branch should have same hash
	assert.Equal(t, hash1, hash2, "Same branch should have same commit hash")
}
