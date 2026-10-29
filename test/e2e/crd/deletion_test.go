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
	"strings"
	"time"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Deletion covers Published <-> DeletionProposed and deletion for packages discovered
// from a repository, using a fork of test-blueprints so the shared fixture is untouched.
var _ = Describe("Deletion", Ordered, Label("lifecycle"), func() {
	const forkName = "del-blueprints"

	var env *testEnv

	getPR := func(name string) *porchv1alpha2.PackageRevision {
		pr := &porchv1alpha2.PackageRevision{}
		ExpectWithOffset(1, k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: name}, pr)).To(Succeed())
		return pr
	}
	hasDeletionProposedBranch := func(repoName string) bool {
		for _, b := range listGiteaBranches(repoName) {
			if strings.Contains(b, "deletionProposed") {
				return true
			}
		}
		return false
	}

	BeforeAll(func() {
		env = sharedEnv()

		By("forking test-blueprints and waiting for its tags")
		deleteGiteaRepo(forkName)
		forkGiteaRepo(testBlueprintsRepo, forkName)
		DeferCleanup(deleteGiteaRepo, forkName)
		Eventually(func() []string {
			return listGiteaRefs(forkName, "tags")
		}).WithTimeout(defaultTimeout).WithPolling(time.Second).Should(ContainElements("basens/v1", "empty/v1"))

		By("registering the fork and waiting for discovery")
		registerV1Alpha2Repo(env.Ctx, env.Namespace, forkName)
		DeferCleanup(cleanupRepo, env.Ctx, env.Namespace, forkName)
		triggerRepoSync(env.Ctx, env.Namespace, forkName)
		waitForDiscovery(env.Ctx, env.Namespace, crdName(forkName, "basens", "v1"))
		waitForDiscovery(env.Ctx, env.Namespace, crdName(forkName, "empty", "v1"))
	})

	It("should propose a discovered package for deletion without writing a git branch", func() {
		pr := getPR(crdName(forkName, "basens", "v1"))

		By("proposing the package for deletion")
		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
		waitForReady(env.Ctx, pr)
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDeletionProposed))

		By("verifying no deletionProposed branch exists in git")
		Expect(hasDeletionProposedBranch(forkName)).To(BeFalse())
		Expect(listGiteaRefs(forkName, "tags")).To(ContainElement("basens/v1"))
	})

	It("should keep DeletionProposed when the repository controller applies the package again", func() {
		pr := getPR(crdName(forkName, "basens", "v1"))

		By("making the repo-owned latest-revision label stale so the next sync applies the package")
		patch := client.MergeFrom(pr.DeepCopy())
		if pr.Labels == nil {
			pr.Labels = map[string]string{}
		}
		pr.Labels[porchv1alpha2.LatestPackageRevisionKey] = "stale"
		Expect(k8sClient.Patch(env.Ctx, pr, patch)).To(Succeed())

		By("syncing the repository")
		triggerRepoSync(env.Ctx, env.Namespace, forkName)
		waitForRepoReady(env.Ctx, env.Namespace, forkName)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
			g.Expect(pr.Labels[porchv1alpha2.LatestPackageRevisionKey]).NotTo(Equal("stale"))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("verifying the lifecycle was not reset")
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
			g.Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDeletionProposed))
		}).WithTimeout(5 * time.Second).WithPolling(500 * time.Millisecond).Should(Succeed())
	})

	It("should return a discovered package to Published without writing a git branch", func() {
		pr := getPR(crdName(forkName, "basens", "v1"))

		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
		waitForReady(env.Ctx, pr)
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
		Expect(hasDeletionProposedBranch(forkName)).To(BeFalse())
	})

	It("should propose a user-created package for deletion without writing a git branch", func() {
		pr := newPackageRevision(env.Namespace, env.RepoName, "del-user-pkg", "v1", withInit("deletion proposed"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		publishPackage(env.Ctx, pr)

		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
		waitForReady(env.Ctx, pr)
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDeletionProposed))
		Expect(hasDeletionProposedBranch(env.RepoName)).To(BeFalse())

		deletePackage(env.Ctx, pr)
	})

	It("should delete a discovered package and its git tag, and not recreate it on sync", func() {
		pr := getPR(crdName(forkName, "empty", "v1"))

		By("proposing the package for deletion")
		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
		waitForReady(env.Ctx, pr)

		By("deleting the package")
		deletePackage(env.Ctx, pr)
		Eventually(func() []string {
			return listGiteaRefs(forkName, "tags")
		}).WithTimeout(defaultTimeout).WithPolling(time.Second).ShouldNot(ContainElement("empty/v1"))

		By("verifying other packages in the fork are untouched")
		Expect(listGiteaRefs(forkName, "tags")).To(ContainElement("basens/v1"))

		By("syncing the repository and verifying the package does not reappear")
		triggerRepoSync(env.Ctx, env.Namespace, forkName)
		waitForRepoReady(env.Ctx, env.Namespace, forkName)
		Consistently(func() bool {
			err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), &porchv1alpha2.PackageRevision{})
			return apierrors.IsNotFound(err)
		}).WithTimeout(5 * time.Second).Should(BeTrue())
	})
})
