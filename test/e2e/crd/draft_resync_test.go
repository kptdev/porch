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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Draft content durability across repo controller syncs.
//
// For v1alpha2, a PRR content push writes ONLY to the PRRStore (the authoritative
// content store); it never touches git. The repo controller's full sync discovers
// packages from git and rehydrates the PRRStore from git content. These specs pin
// the invariant that a repo sync must NOT clobber newer PRRStore content (pushed but
// not yet published) with the older git draft content, and that draft content
// survives and publishes correctly after a sync.
//
// These are regression guards for changes to the discovery / draft-push coupling in
// the repo controller sync (cacheExternalPackageResources) and prrbackend close path.
var _ = Describe("Draft content across repo sync", Ordered, Label("content"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should not clobber pushed draft content on a repo sync before publish", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "draft-resync-clobber", "v1", withInit("draft resync clobber"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing content via PRR (writes to PRRStore, not git)")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"data.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: draft-data\ndata:\n  stage: pre-publish\n",
		})
		waitForRendered(env.Ctx, pr)

		By("confirming the pushed content is present before the sync")
		Eventually(func(g Gomega) {
			resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
			g.Expect(resources).To(HaveKey("data.yaml"))
			g.Expect(resources["data.yaml"]).To(ContainSubstring("draft-data"))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("forcing a full repo controller sync (exercises git->PRRStore discovery)")
		triggerRepoSync(env.Ctx, env.Namespace, env.RepoName)
		waitForRepoReady(env.Ctx, env.Namespace, env.RepoName)

		By("verifying the sync did not revert/clobber the pushed draft content")
		Consistently(func(g Gomega) {
			resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
			g.Expect(resources).To(HaveKey("data.yaml"))
			g.Expect(resources["data.yaml"]).To(ContainSubstring("draft-data"))
			g.Expect(resources["data.yaml"]).To(ContainSubstring("stage: pre-publish"))
		}).WithTimeout(15 * time.Second).WithPolling(defaultInterval).Should(Succeed())
	})

	It("should publish pushed draft content correctly after an intervening repo sync", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "draft-resync-publish", "v1", withInit("draft resync publish"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing content via PRR")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"data.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: resync-publish-data\ndata:\n  env: production\n",
		})
		waitForRendered(env.Ctx, pr)

		By("forcing a repo sync between push and publish")
		triggerRepoSync(env.Ctx, env.Namespace, env.RepoName)
		waitForRepoReady(env.Ctx, env.Namespace, env.RepoName)

		By("publishing the package")
		publishPackage(env.Ctx, pr)

		By("verifying the published content is the pushed content, not pre-push init content")
		Eventually(func(g Gomega) {
			resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
			g.Expect(resources).To(HaveKey("data.yaml"))
			g.Expect(resources["data.yaml"]).To(ContainSubstring("resync-publish-data"))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	It("should preserve an API-created draft and its content across a repo sync", func() {
		By("creating a draft and pushing content (content lives only in PRRStore)")
		pr := newPackageRevision(env.Namespace, env.RepoName, "draft-resync-survive", "v1", withInit("draft resync survive"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"keep.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keep-me\n",
		})
		waitForRendered(env.Ctx, pr)

		By("forcing a repo sync")
		triggerRepoSync(env.Ctx, env.Namespace, env.RepoName)
		waitForRepoReady(env.Ctx, env.Namespace, env.RepoName)

		By("verifying the draft CRD is not deleted and its content is intact")
		Consistently(func(g Gomega) {
			resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
			g.Expect(resources).To(HaveKey("keep.yaml"))
			g.Expect(resources["keep.yaml"]).To(ContainSubstring("keep-me"))
		}).WithTimeout(15 * time.Second).WithPolling(defaultInterval).Should(Succeed())
	})
})
