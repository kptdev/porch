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

	It("keeps syncing after the old URL is rehomed away and then deleted", func() {
		// This is the issue #1334 scenario end to end:
		//   1. repo on the old URL, old URL addressable
		//   2. full sync works
		//   3. rehome to the new URL
		//   4. full sync works
		//   5. the OLD URL disappears (repo deleted, no longer addressable)
		//   6. full sync STILL works (reads the new URL, not the stale old handle)
		// Before the evict-on-URL-change fix, step 6 failed: the cached handle
		// still targeted the deleted old URL and the fetch errored with
		// "connection refused" / "repository not found" until a controller restart.
		const (
			repoName = "rehome-survive-del"
			oldRepo  = "rehome-survive-del-old"
			newRepo  = "rehome-survive-del-new"
		)

		By("1-2: registering against the old repo and confirming the initial sync works")
		createGiteaRepo(oldRepo)
		// NOTE: no DeferCleanup for oldRepo — it is deleted explicitly in step 5.
		// deleteGiteaRepo is idempotent, so a defensive DeferCleanup is harmless
		// but unnecessary; omitting it avoids a double-delete log.
		DeferCleanup(cleanupRepo, env.Ctx, env.Namespace, repoName)
		registerV1Alpha2Repo(env.Ctx, env.Namespace, repoName, withRepoURL(giteaRepoURL(oldRepo)))

		originalHash := repoGitCommitHash(env.Ctx, env.Namespace, repoName)
		Expect(originalHash).NotTo(BeEmpty(), "repository must record a GitCommitHash after its initial sync")

		By("3: forking to the new URL (compatible history) and rehoming to it")
		forkGiteaRepo(oldRepo, newRepo)
		DeferCleanup(deleteGiteaRepo, newRepo)
		Expect(giteaBranchHeadSHA(newRepo, "main")).To(Equal(originalHash),
			"the new repo (fork) HEAD must initially match the recorded hash")
		rehomeRepo(env.Ctx, env.Namespace, repoName, giteaRepoURL(newRepo))

		By("4: confirming the full sync against the new URL works")
		waitForRepoReady(env.Ctx, env.Namespace, repoName)
		Expect(repoGitURL(env.Ctx, env.Namespace, repoName)).To(Equal(giteaRepoURL(newRepo)))

		By("5: deleting the OLD repo so the old URL is no longer addressable")
		deleteGiteaRepo(oldRepo)

		By("6: advancing the new repo and confirming a full sync still succeeds")
		// Advancing the new repo's HEAD gives the next sync an observable result
		// that can ONLY come from the new URL. The old URL is now gone, so if the
		// handle were still pointed at it the sync would fail rather than reach
		// this hash. A spec change isn't available here (the URL is already the
		// new one), so we use a one-time sync; the assertion polls until the
		// scheduled/one-time sync lands.
		advancedHash := commitFileToGiteaRepo(newRepo, "main", "survive-del.txt", "advanced after old URL deleted\n")
		Expect(advancedHash).NotTo(Equal(originalHash))

		triggerRepoSync(env.Ctx, env.Namespace, repoName)
		Eventually(func(g Gomega) {
			repo := &configapi.Repository{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: repoName}, repo)).To(Succeed())
			g.Expect(repo.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(configapi.RepositoryReady)),
				HaveField("Status", Equal(metav1.ConditionTrue)),
			)), "full sync must succeed against the new URL even though the old URL is gone")
			g.Expect(repo.Status.GitCommitHash).To(Equal(advancedHash),
				"the sync must read the new URL's advanced HEAD, proving it is not still targeting the deleted old URL")
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
