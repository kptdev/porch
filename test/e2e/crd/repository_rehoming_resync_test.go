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

// These tests cover the cache behaviour after a repository is rehomed to a new
// git URL. The repository cache key excludes the git URL, so before the
// evict-on-URL-change fix a rehome reused the cached git handle, which kept its
// origin remote pointed at the OLD URL. Subsequent syncs then kept reading the
// old remote until the controller restarted. The fix evicts the stale handle on
// OpenRepository when the URL changed, so the next sync reads the NEW remote.
//
// The success case proves the new remote is actually read (a commit made ONLY on
// the fork, after the rehome, is picked up). The failure case proves the rehome
// history guard still rejects an incompatible remote — the eviction must not
// weaken that protection.
var _ = Describe("Repository rehoming re-sync", Label("infra"), func() {
	var env *testEnv

	BeforeEach(func() {
		env = sharedEnv()
	})

	It("reads the new remote after rehoming to a fork without a controller restart", func() {
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

		By("forking the source so the fork HEAD matches the recorded hash")
		forkGiteaRepo(sourceRepo, forkRepo)
		DeferCleanup(deleteGiteaRepo, forkRepo)
		Expect(giteaBranchHeadSHA(forkRepo, "main")).To(Equal(originalHash),
			"fork HEAD must match source HEAD for the matching-hash rehome case")

		By("rehoming to the fork URL")
		rehomeRepo(env.Ctx, env.Namespace, repoName, giteaRepoURL(forkRepo))
		waitForRepoReady(env.Ctx, env.Namespace, repoName)
		Expect(repoGitURL(env.Ctx, env.Namespace, repoName)).To(Equal(giteaRepoURL(forkRepo)))

		By("advancing ONLY the fork's HEAD with a new commit")
		// This commit exists on the fork but not on the original source. If the
		// cached handle were still reading the old (source) remote, this commit
		// would never be observed and the recorded hash would not advance to it.
		newForkHash := commitFileToGiteaRepo(forkRepo, "main", "rehome-resync.txt", "advanced on fork after rehome\n")
		Expect(newForkHash).NotTo(Equal(originalHash))
		Expect(giteaBranchHeadSHA(sourceRepo, "main")).To(Equal(originalHash),
			"the source repo must NOT have the new commit (it was made only on the fork)")

		By("triggering a re-sync and verifying it reads the fork (new) remote")
		triggerRepoSync(env.Ctx, env.Namespace, repoName)
		Eventually(func(g Gomega) {
			waitForRepoReady(env.Ctx, env.Namespace, repoName)
			g.Expect(repoGitCommitHash(env.Ctx, env.Namespace, repoName)).To(Equal(newForkHash),
				"after rehome the sync must read the fork's advanced HEAD, proving the stale old-URL handle was evicted")
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

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
