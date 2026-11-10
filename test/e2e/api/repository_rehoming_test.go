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

package api

import (
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Repository rehoming = changing an existing Repository's spec.git.repo URL after
// registration (spec.git.repo is mutable). The change is always accepted at the
// API level; validation happens asynchronously during the repository sync. The
// sync's connectivity check compares the new remote branch HEAD commit hash
// against the hash already recorded in Status.GitCommitHash:
//   - match   -> sync succeeds, Ready=True.
//   - mismatch-> sync fails, Ready=False/Error; the CRD change still sticks and
//     Status.GitCommitHash is left at the last-good value.
//   - reverting to a URL whose HEAD matches again clears the error (the spec
//     change forces an immediate re-sync), returning the repo to Ready=True.

// getRepoGitCommitHash returns the currently recorded Status.GitCommitHash.
func (t *PorchSuite) getRepoGitCommitHash(repoName string) string {
	t.T().Helper()
	var repo configapi.Repository
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repoName}, &repo)
	return repo.Status.GitCommitHash
}

// getRepoURL returns the currently configured spec.git.repo.
func (t *PorchSuite) getRepoURL(repoName string) string {
	t.T().Helper()
	var repo configapi.Repository
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repoName}, &repo)
	return repo.Spec.Git.Repo
}

// TestRepositoryRehomeToMatchingHash verifies that rehoming a repository to a new
// URL whose branch HEAD matches the recorded Status.GitCommitHash is accepted and
// the repository stays Ready. A Gitea fork is a byte-for-byte copy of history, so
// the fork's HEAD matches the source's.
func (t *PorchSuite) TestRepositoryRehomeToMatchingHash() {
	const (
		repoName   = "rehome-match"
		sourceRepo = "rehome-match-src"
		forkRepo   = "rehome-match-fork"
	)

	// Register against a dedicated source repo and let it sync so a
	// Status.GitCommitHash is recorded.
	sourceURL := t.CreateGiteaRepo(sourceRepo)
	t.RegisterGitRepositoryF(sourceURL, repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	originalHash := t.getRepoGitCommitHash(repoName)
	t.Require().NotEmpty(originalHash, "repository must record a GitCommitHash after its initial sync")

	// Fork the source so the fork's HEAD hash is identical to originalHash.
	forkURL := t.ForkGiteaRepo(sourceRepo, forkRepo)
	t.Require().Equal(
		t.GiteaGetBranchLatestCommitSHA(sourceRepo, "main"),
		t.GiteaGetBranchLatestCommitSHA(forkRepo, "main"),
		"fork HEAD must match source HEAD for the matching-hash rehome case",
	)

	// Rehome to the fork. The hashes match, so the sync must succeed.
	t.RehomeRepo(repoName, forkURL)

	t.WaitUntilRepositoryReady(repoName, t.Namespace)

	if got := t.getRepoURL(repoName); got != forkURL {
		t.Errorf("spec.git.repo after rehome: got %q, want %q", got, forkURL)
	}
	if got := t.getRepoGitCommitHash(repoName); got != originalHash {
		t.Errorf("GitCommitHash after matching-hash rehome: got %q, want unchanged %q", got, originalHash)
	}
	t.Logf("Rehome to matching-hash URL %q succeeded; repository Ready", forkURL)
}

// TestRepositoryRehomeToMismatchedHashFailsSync verifies that rehoming to a URL
// whose branch HEAD does NOT match the recorded Status.GitCommitHash is accepted
// at the API level but fails during sync, leaving the repository in an error
// state with Status.GitCommitHash unchanged.
func (t *PorchSuite) TestRepositoryRehomeToMismatchedHashFailsSync() {
	const (
		repoName      = "rehome-mismatch"
		sourceRepo    = "rehome-mismatch-src"
		divergentRepo = "rehome-mismatch-div"
	)

	sourceURL := t.CreateGiteaRepo(sourceRepo)
	t.RegisterGitRepositoryF(sourceURL, repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	originalHash := t.getRepoGitCommitHash(repoName)
	t.Require().NotEmpty(originalHash, "repository must record a GitCommitHash after its initial sync")

	// A freshly auto-initialized repo has an independent initial commit, so its
	// HEAD diverges from originalHash.
	divergentURL := t.CreateGiteaRepo(divergentRepo)
	t.Require().NotEqual(
		originalHash,
		t.GiteaGetBranchLatestCommitSHA(divergentRepo, "main"),
		"divergent repo HEAD must differ from the recorded hash for the mismatch case",
	)

	// Rehome to the divergent URL. The CRD update is accepted; the sync fails.
	t.RehomeRepo(repoName, divergentURL)

	t.WaitUntilRepositoryInError(repoName, t.Namespace, "not present in the history")

	// The spec change stuck even though the sync failed.
	if got := t.getRepoURL(repoName); got != divergentURL {
		t.Errorf("spec.git.repo after rejected rehome: got %q, want %q (CRD change must persist)", got, divergentURL)
	}
	// A failed sync must not overwrite the last-good recorded hash.
	if got := t.getRepoGitCommitHash(repoName); got != originalHash {
		t.Errorf("GitCommitHash after failed rehome: got %q, want unchanged %q", got, originalHash)
	}
	t.Logf("Rehome to mismatched-hash URL %q was accepted but sync failed as expected", divergentURL)
}

// TestRepositoryRehomeRecoversAfterRevert verifies that once a mismatched rehome
// has left the repository in an error state, pointing spec.git.repo back at a URL
// whose HEAD matches the recorded hash clears the error and returns the
// repository to Ready. The spec change forces an immediate re-sync (hasSpecChanged
// is evaluated before the error-retry backoff), so no backoff wait is needed.
func (t *PorchSuite) TestRepositoryRehomeRecoversAfterRevert() {
	const (
		repoName      = "rehome-recover"
		sourceRepo    = "rehome-recover-src"
		divergentRepo = "rehome-recover-div"
		forkRepo      = "rehome-recover-fork"
	)

	sourceURL := t.CreateGiteaRepo(sourceRepo)
	t.RegisterGitRepositoryF(sourceURL, repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	originalHash := t.getRepoGitCommitHash(repoName)
	t.Require().NotEmpty(originalHash, "repository must record a GitCommitHash after its initial sync")

	// Break it: rehome to a divergent URL and confirm it lands in error.
	divergentURL := t.CreateGiteaRepo(divergentRepo)
	t.RehomeRepo(repoName, divergentURL)
	t.WaitUntilRepositoryInError(repoName, t.Namespace, "not present in the history")
	t.Logf("Repository %s is in error state after bad rehome", repoName)

	// Recover: point back at a URL whose HEAD matches the recorded hash. We use
	// a fork of the original source (identical HEAD); reverting to sourceURL
	// itself would work equally well.
	forkURL := t.ForkGiteaRepo(sourceRepo, forkRepo)
	t.Require().Equal(
		originalHash,
		t.GiteaGetBranchLatestCommitSHA(forkRepo, "main"),
		"recovery fork HEAD must match the recorded hash",
	)

	t.RehomeRepo(repoName, forkURL)

	// The spec change forces an immediate sync; the hashes now match, so the
	// error clears and the repository returns to Ready.
	t.WaitUntilRepositoryReady(repoName, t.Namespace)

	if got := t.getRepoURL(repoName); got != forkURL {
		t.Errorf("spec.git.repo after recovery: got %q, want %q", got, forkURL)
	}
	if got := t.getRepoGitCommitHash(repoName); got != originalHash {
		t.Errorf("GitCommitHash after recovery: got %q, want unchanged %q", got, originalHash)
	}
	t.Logf("Repository %s recovered to Ready after reverting to a matching-hash URL", repoName)
}
