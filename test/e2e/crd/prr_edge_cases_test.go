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
	"maps"
	"slices"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("PRR Edge Cases", Ordered, Label("content"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should return resource paths in ResourcePaths and nil Resources when path-only is set", func() {
		By("creating a draft and pushing files")
		pr := newPackageRevision(env.Namespace, env.RepoName, "path-only", "v1", withInit("path only test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"deploy.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: deploy\n",
			"config.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: config\n",
		})
		waitForRendered(env.Ctx, pr)

		By("fetching with path-only query parameter")
		prr := &porchapi.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
			Namespace: env.Namespace,
			Name:      pr.Name + "?path-only",
		}, prr)).To(Succeed())

		By("verifying ResourcePaths are present and Resources is nil")
		Expect(prr.Spec.Resources).To(BeNil())
		Expect(prr.Spec.ResourcePaths).To(ContainElements("deploy.yaml", "config.yaml", "Kptfile"))

		By("verifying a normal GET still returns full content")
		prrFull := &porchapi.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
			Namespace: env.Namespace,
			Name:      pr.Name,
		}, prrFull)).To(Succeed())
		Expect(prrFull.Spec.Resources["deploy.yaml"]).NotTo(BeEmpty())
	})

	It("should return both ResourcePaths and Resources when no query parameters are set", func() {
		By("creating a draft and pushing files")
		pr := newPackageRevision(env.Namespace, env.RepoName, "no-query", "v1", withInit("no query test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"deploy.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: deploy\n",
			"config.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: config\n",
		})
		waitForRendered(env.Ctx, pr)

		By("fetching with no query parameters")
		prr := &porchapi.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
			Namespace: env.Namespace,
			Name:      pr.Name,
		}, prr)).To(Succeed())

		By("verifying both ResourcePaths and Resources are populated for all files")
		Expect(prr.Spec.ResourcePaths).To(ContainElements("deploy.yaml", "config.yaml", "Kptfile"))
		Expect(prr.Spec.Resources).To(HaveKey("deploy.yaml"))
		Expect(prr.Spec.Resources).To(HaveKey("config.yaml"))
		Expect(prr.Spec.Resources).To(HaveKey("Kptfile"))
		Expect(prr.Spec.Resources["deploy.yaml"]).NotTo(BeEmpty())
		Expect(prr.Spec.Resources["config.yaml"]).NotTo(BeEmpty())

		By("verifying the two views agree on the key set")
		Expect(prr.Spec.ResourcePaths).To(ConsistOf(slices.Collect(maps.Keys(prr.Spec.Resources))))
	})

	It("should filter both ResourcePaths and Resources when a file filter is set without path-only", func() {
		By("creating a draft and pushing files")
		pr := newPackageRevision(env.Namespace, env.RepoName, "file-filter", "v1", withInit("file filter test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"deploy.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: deploy\n",
			"config.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: config\n",
		})
		waitForRendered(env.Ctx, pr)

		By("fetching with a file filter query parameter")
		prr := &porchapi.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
			Namespace: env.Namespace,
			Name:      pr.Name + "?file=deploy.yaml",
		}, prr)).To(Succeed())

		By("verifying only the filtered file is returned, with content")
		Expect(prr.Spec.ResourcePaths).To(ConsistOf("deploy.yaml"))
		Expect(prr.Spec.Resources).To(HaveLen(1))
		Expect(prr.Spec.Resources["deploy.yaml"]).NotTo(BeEmpty())
		Expect(prr.Spec.Resources).NotTo(HaveKey("config.yaml"))
		Expect(prr.Spec.Resources).NotTo(HaveKey("Kptfile"))
	})

	It("should filter ResourcePaths and return empty Resources when both file filter and path-only are set", func() {
		By("creating a draft and pushing files")
		pr := newPackageRevision(env.Namespace, env.RepoName, "file-filter-path-only", "v1", withInit("file filter path only test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"deploy.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: deploy\n",
			"config.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: config\n",
		})
		waitForRendered(env.Ctx, pr)

		By("fetching with both file filter and path-only query parameters")
		prr := &porchapi.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
			Namespace: env.Namespace,
			Name:      pr.Name + "?file=deploy.yaml&path-only",
		}, prr)).To(Succeed())

		By("verifying only the filtered path is returned and Resources is nil")
		Expect(prr.Spec.Resources).To(BeNil())
		Expect(prr.Spec.ResourcePaths).To(ConsistOf("deploy.yaml"))
	})

	It("should delete a file by removing it from the resources map", func() {
		By("creating a draft and pushing two files")
		pr := newPackageRevision(env.Namespace, env.RepoName, "del-file", "v1", withInit("delete file test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"keep.yaml":   "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keep\n",
			"remove.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: remove\n",
		})
		waitForRendered(env.Ctx, pr)

		By("verifying both files exist")
		Eventually(func(g Gomega) {
			resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
			g.Expect(resources).To(HaveKey("keep.yaml"))
			g.Expect(resources).To(HaveKey("remove.yaml"))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("removing one file from the resources map")
		prr := &porchapi.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: pr.Name}, prr)).To(Succeed())
		delete(prr.Spec.Resources, "remove.yaml")
		Expect(k8sClient.Update(env.Ctx, prr)).To(Succeed())
		// Allow DB commit to propagate across connections before render reads.
		time.Sleep(time.Second)
		waitForRendered(env.Ctx, pr)

		By("verifying the file is gone")
		Eventually(func(g Gomega) {
			resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
			g.Expect(resources).To(HaveKey("keep.yaml"))
			g.Expect(resources).NotTo(HaveKey("remove.yaml"))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	It("should set Rendered=False when Kptfile has wrong apiVersion", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "bad-kptfile", "v1", withInit("bad kptfile test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing a Kptfile with wrong apiVersion")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: wrong/v1\nkind: Kptfile\nmetadata:\n  name: bad-kptfile\n",
		})

		By("waiting for Rendered=False")
		waitForRenderFailed(env.Ctx, pr)

		By("verifying error message is useful")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		renderedCond := findCondition(pr.Status.Conditions, porchv1alpha2.ConditionRendered)
		Expect(renderedCond).NotTo(BeNil())
		Expect(renderedCond.Message).NotTo(BeEmpty())
	})

	It("should set Rendered=False when Kptfile contains invalid YAML", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "bad-yaml", "v1", withInit("bad yaml test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing a Kptfile with unparseable YAML")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "this: is: not: valid: yaml: [[[",
		})

		By("waiting for Rendered=False")
		waitForRenderFailed(env.Ctx, pr)
	})

	It("should keep the files a partial update does not submit", func() {
		By("creating a draft and pushing two files")
		pr := newPackageRevision(env.Namespace, env.RepoName, "partial-update", "v1", withInit("partial update test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"keep.yaml":   "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keep\n",
			"change.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: change\ndata:\n  key: one\n",
		})
		waitForRendered(env.Ctx, pr)

		By("submitting only change.yaml with partial=true")
		prr := &porchapi.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: pr.Name}, prr)).To(Succeed())
		prr.Name = prr.Name + "?partial=true"
		prr.Spec.Resources = map[string]string{
			"change.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: change\ndata:\n  key: two\n",
		}
		Expect(k8sClient.Update(env.Ctx, prr)).To(Succeed())
		waitForRendered(env.Ctx, pr)

		By("verifying the submitted file changed and the others are still present")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources).To(HaveKey("Kptfile"))
		Expect(resources).To(HaveKey("keep.yaml"))
		Expect(resources["change.yaml"]).To(ContainSubstring("key: two"))
	})
})
