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
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		waitForRepoError(env.Ctx, env.Namespace, repoName, "not present in the history")

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
		waitForRepoError(env.Ctx, env.Namespace, repoName, "not present in the history")

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

	// The next two cover cache behaviour after a rehome. The cache key excludes
	// the git URL, so before the evict-on-URL-change fix a rehome reused the
	// cached git handle still bound to the OLD remote, and syncs kept reading it
	// until the controller restarted. The fix evicts the stale handle on the URL
	// change so the next sync reads the NEW remote.

	It("reads the new remote after rehoming to a fork whose HEAD advanced", func() {
		const (
			repoName   = "rehome-resync"
			sourceRepo = "rehome-resync-src"
			forkRepo   = "rehome-resync-fork"
		)

		By("registering the repository against a dedicated source repo")
		createGiteaRepo(sourceRepo)
		DeferCleanup(deleteGiteaRepo, sourceRepo)
		DeferCleanup(cleanupRepo, env.Ctx, env.Namespace, repoName)
		registerV1Alpha2Repo(env.Ctx, env.Namespace, repoName, withRepoURL(giteaRepoURL(sourceRepo)))

		originalHash := repoGitCommitHash(env.Ctx, env.Namespace, repoName)
		Expect(originalHash).NotTo(BeEmpty(), "repository must record a GitCommitHash after its initial sync")

		By("forking the source so the fork starts at the recorded hash")
		forkGiteaRepo(sourceRepo, forkRepo)
		DeferCleanup(deleteGiteaRepo, forkRepo)
		Expect(giteaBranchHeadSHA(forkRepo, "main")).To(Equal(originalHash),
			"fork HEAD must initially match source HEAD")

		By("advancing ONLY the fork's HEAD with a new commit")
		// The new commit is a descendant of originalHash (so the fork's history
		// still contains the recorded commit -> a compatible rehome), but it
		// exists ONLY on the fork, not on the source. The source HEAD stays at
		// originalHash.
		newForkHash := commitFileToGiteaRepo(forkRepo, "main", "rehome-resync.txt", "advanced on fork\n")
		Expect(newForkHash).NotTo(Equal(originalHash))
		Expect(giteaBranchHeadSHA(sourceRepo, "main")).To(Equal(originalHash),
			"the source repo must NOT have the new commit (it was made only on the fork)")

		By("rehoming to the fork URL")
		// Changing spec.git.repo bumps the generation, which the controller treats
		// as a spec change and responds to with a full sync (no run-once-at timing
		// dependency). That sync must read the fork (the new URL): with the stale
		// handle evicted it records the fork's advanced HEAD; without the fix it
		// would still read the source and record originalHash.
		rehomeRepo(env.Ctx, env.Namespace, repoName, giteaRepoURL(forkRepo))

		By("verifying the sync read the fork's advanced HEAD without a controller restart")
		Eventually(func(g Gomega) {
			repo := &configapi.Repository{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: repoName}, repo)).To(Succeed())
			// Gate on the current generation being observed and Ready, so we read
			// the result of the rehome sync, not a stale pre-rehome status.
			g.Expect(repo.Status.ObservedGeneration).To(Equal(repo.Generation))
			g.Expect(repo.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(configapi.RepositoryReady)),
				HaveField("Status", Equal(metav1.ConditionTrue)),
			)))
			g.Expect(repo.Spec.Git.Repo).To(Equal(giteaRepoURL(forkRepo)))
			g.Expect(repo.Status.GitCommitHash).To(Equal(newForkHash),
				"after rehome the sync must read the fork's advanced HEAD, proving the stale old-URL handle was evicted")
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	// NOTE: there is intentionally no "old URL deleted after rehome" test. A
	// rehome requires the NEW URL to be reachable and history-compatible, and the
	// rehome sync rebuilds the cached handle onto the new URL as part of that
	// sync (verified by the test above, which observes the new URL's advanced
	// HEAD). Once the rehome has succeeded there is no handle left pointing at the
	// old URL, so deleting the old URL afterwards cannot affect subsequent syncs.
	// The stale-handle failure mode only occurs while the handle has NOT been
	// rebuilt, which is exactly what the eviction fix prevents and what the test
	// above covers.

	It("still rejects a rehome to a divergent remote after the cache change", func() {
		const (
			repoName      = "rehome-resync-bad"
			sourceRepo    = "rehome-resync-bad-src"
			divergentRepo = "rehome-resync-bad-div"
		)

		By("registering the repository against a dedicated source repo")
		createGiteaRepo(sourceRepo)
		DeferCleanup(deleteGiteaRepo, sourceRepo)
		DeferCleanup(cleanupRepo, env.Ctx, env.Namespace, repoName)
		registerV1Alpha2Repo(env.Ctx, env.Namespace, repoName, withRepoURL(giteaRepoURL(sourceRepo)))

		originalHash := repoGitCommitHash(env.Ctx, env.Namespace, repoName)
		Expect(originalHash).NotTo(BeEmpty(), "repository must record a GitCommitHash after its initial sync")

		By("creating an independent divergent repo whose history does not contain the recorded hash")
		createGiteaRepo(divergentRepo)
		DeferCleanup(deleteGiteaRepo, divergentRepo)
		Expect(giteaBranchHeadSHA(divergentRepo, "main")).NotTo(Equal(originalHash),
			"divergent repo HEAD must differ from the recorded hash")

		By("rehoming to the divergent URL and verifying the repository goes to error")
		rehomeRepo(env.Ctx, env.Namespace, repoName, giteaRepoURL(divergentRepo))
		waitForRepoError(env.Ctx, env.Namespace, repoName, "not present in the history")

		By("verifying the recorded hash was not overwritten by the rejected rehome")
		Expect(repoGitCommitHash(env.Ctx, env.Namespace, repoName)).To(Equal(originalHash),
			"a rejected rehome must not overwrite the last-good GitCommitHash")
	})
})
