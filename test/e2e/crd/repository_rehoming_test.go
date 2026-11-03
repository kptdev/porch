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

package crd

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
var _ = Describe("Repository rehoming", Label("infra"), func() {
	var env *testEnv

	BeforeEach(func() {
		env = sharedEnv()
	})

	It("should stay Ready when rehomed to a URL with a matching commit hash", func() {
		const (
			repoName   = "rehome-match"
			sourceRepo = "rehome-match-src"
			forkRepo   = "rehome-match-fork"
		)

		By("registering the repository against a dedicated source repo")
		createGiteaRepo(sourceRepo)
		DeferCleanup(deleteGiteaRepo, sourceRepo)
		DeferCleanup(cleanupRepo, env.Ctx, env.Namespace, repoName)
		registerV1Alpha2Repo(env.Ctx, env.Namespace, repoName, withRepoURL(giteaRepoURL(sourceRepo)))

		originalHash := repoGitCommitHash(env.Ctx, env.Namespace, repoName)
		Expect(originalHash).NotTo(BeEmpty(), "repository must record a GitCommitHash after its initial sync")

		By("forking the source so the fork HEAD matches the recorded hash")
		forkGiteaRepo(sourceRepo, forkRepo)
		DeferCleanup(deleteGiteaRepo, forkRepo)
		Expect(giteaBranchHeadSHA(forkRepo, "main")).To(Equal(giteaBranchHeadSHA(sourceRepo, "main")),
			"fork HEAD must match source HEAD for the matching-hash rehome case")

		By("rehoming to the fork URL")
		rehomeRepo(env.Ctx, env.Namespace, repoName, giteaRepoURL(forkRepo))

		By("verifying the repository stays Ready and the hash is unchanged")
		waitForRepoReady(env.Ctx, env.Namespace, repoName)
		Expect(repoGitURL(env.Ctx, env.Namespace, repoName)).To(Equal(giteaRepoURL(forkRepo)))
		Expect(repoGitCommitHash(env.Ctx, env.Namespace, repoName)).To(Equal(originalHash))
	})

	It("should accept the CRD change but fail sync when rehomed to a mismatched hash", func() {
		const (
			repoName      = "rehome-mismatch"
			sourceRepo    = "rehome-mismatch-src"
			divergentRepo = "rehome-mismatch-div"
		)

		By("registering the repository against a dedicated source repo")
		createGiteaRepo(sourceRepo)
		DeferCleanup(deleteGiteaRepo, sourceRepo)
		DeferCleanup(cleanupRepo, env.Ctx, env.Namespace, repoName)
		registerV1Alpha2Repo(env.Ctx, env.Namespace, repoName, withRepoURL(giteaRepoURL(sourceRepo)))

		originalHash := repoGitCommitHash(env.Ctx, env.Namespace, repoName)
		Expect(originalHash).NotTo(BeEmpty(), "repository must record a GitCommitHash after its initial sync")

		By("creating a divergent repo with an independent HEAD")
		createGiteaRepo(divergentRepo)
		DeferCleanup(deleteGiteaRepo, divergentRepo)
		Expect(giteaBranchHeadSHA(divergentRepo, "main")).NotTo(Equal(originalHash),
			"divergent repo HEAD must differ from the recorded hash for the mismatch case")

		By("rehoming to the divergent URL")
		rehomeRepo(env.Ctx, env.Namespace, repoName, giteaRepoURL(divergentRepo))

		By("verifying the repository goes to an error state")
		waitForRepoError(env.Ctx, env.Namespace, repoName, "does not match hash")

		By("verifying the CRD change persisted and the recorded hash was not overwritten")
		Expect(repoGitURL(env.Ctx, env.Namespace, repoName)).To(Equal(giteaRepoURL(divergentRepo)),
			"the spec.git.repo change must persist even though the sync failed")
		Expect(repoGitCommitHash(env.Ctx, env.Namespace, repoName)).To(Equal(originalHash),
			"a failed sync must not overwrite the last-good GitCommitHash")
	})

	It("should recover to Ready when reverted to a matching-hash URL", func() {
		const (
			repoName      = "rehome-recover"
			sourceRepo    = "rehome-recover-src"
			divergentRepo = "rehome-recover-div"
			forkRepo      = "rehome-recover-fork"
		)

		By("registering the repository against a dedicated source repo")
		createGiteaRepo(sourceRepo)
		DeferCleanup(deleteGiteaRepo, sourceRepo)
		DeferCleanup(cleanupRepo, env.Ctx, env.Namespace, repoName)
		registerV1Alpha2Repo(env.Ctx, env.Namespace, repoName, withRepoURL(giteaRepoURL(sourceRepo)))

		originalHash := repoGitCommitHash(env.Ctx, env.Namespace, repoName)
		Expect(originalHash).NotTo(BeEmpty(), "repository must record a GitCommitHash after its initial sync")

		By("breaking the repository with a divergent rehome")
		createGiteaRepo(divergentRepo)
		DeferCleanup(deleteGiteaRepo, divergentRepo)
		rehomeRepo(env.Ctx, env.Namespace, repoName, giteaRepoURL(divergentRepo))
		waitForRepoError(env.Ctx, env.Namespace, repoName, "does not match hash")

		By("reverting to a matching-hash URL (a fork of the original source)")
		forkGiteaRepo(sourceRepo, forkRepo)
		DeferCleanup(deleteGiteaRepo, forkRepo)
		Expect(giteaBranchHeadSHA(forkRepo, "main")).To(Equal(originalHash),
			"recovery fork HEAD must match the recorded hash")
		rehomeRepo(env.Ctx, env.Namespace, repoName, giteaRepoURL(forkRepo))

		By("verifying the error clears and the repository returns to Ready")
		// The spec change forces an immediate re-sync (hasSpecChanged is evaluated
		// before the error-retry backoff), so no backoff wait is needed.
		waitForRepoReady(env.Ctx, env.Namespace, repoName)
		Expect(repoGitURL(env.Ctx, env.Namespace, repoName)).To(Equal(giteaRepoURL(forkRepo)))
		Expect(repoGitCommitHash(env.Ctx, env.Namespace, repoName)).To(Equal(originalHash))
	})
})
