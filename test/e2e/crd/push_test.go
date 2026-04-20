// Copyright 2026 The Nephio Authors
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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	porchv1alpha1 "github.com/nephio-project/porch/api/porch/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Push", Ordered, Label("content"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should update resources via PRR and verify content persisted", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "push-pkg", "v1", withInit("push test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing a new ConfigMap via PRR")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"configmap.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: push-test-cm\ndata:\n  key: value\n",
		})

		By("waiting for async render to complete")
		waitForRendered(env.Ctx, pr)

		By("pulling and verifying the pushed content")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources).To(HaveKey("configmap.yaml"))
		Expect(resources["configmap.yaml"]).To(ContainSubstring("push-test-cm"))
		Expect(resources).To(HaveKey("Kptfile"))
	})

	It("should handle empty PRR update without error", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "empty-patch", "v1", withInit("empty patch test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("updating PRR with no changes")
		prr := &porchv1alpha1.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: pr.Name}, prr)).To(Succeed())
		Expect(k8sClient.Update(env.Ctx, prr)).To(Succeed())

		By("verifying package is still ready")
		waitForReady(env.Ctx, pr)
	})

	It("should update resources with a render pipeline and verify rendered output", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "render-push", "v1", withInit("render push test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing Kptfile with set-namespace:v0.4.1 (builtin) using configMap")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: render-push\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.1\n    configMap:\n      namespace: render-push-ns\n",
			"deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: test-deploy\nspec:\n  replicas: 1\n  selector:\n    matchLabels:\n      app: test\n  template:\n    metadata:\n      labels:\n        app: test\n    spec:\n      containers:\n      - name: nginx\n        image: nginx:latest\n",
		})

		By("waiting for async render")
		waitForRendered(env.Ctx, pr)

		By("verifying set-namespace rendered the content")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources["deployment.yaml"]).To(ContainSubstring("namespace: render-push-ns"))
	})

	It("should publish pushed content to git", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "pub-push", "v1", withInit("publish push test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing content")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"data.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: published-data\ndata:\n  env: production\n",
		})
		waitForRendered(env.Ctx, pr)

		By("publishing the package")
		publishPackage(env.Ctx, pr)

		By("verifying content survives publish")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources).To(HaveKey("data.yaml"))
		Expect(resources["data.yaml"]).To(ContainSubstring("published-data"))
	})

	It("should handle a large package revision", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "large-pkg", "v1", withInit("large package test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing a 5MB resource")
		largeValue := strings.Repeat("a", 5*1024*1024)
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"largefile.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: large-data\ndata:\n  value: \"" + largeValue + "\"\n",
		})
		waitForRendered(env.Ctx, pr)

		By("pulling and verifying the large content round-tripped")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources).To(HaveKey("largefile.yaml"))
		Expect(len(resources["largefile.yaml"])).To(BeNumerically(">", 5*1024*1024))
	})

	It("should reject PRR push to a Published package", func() {
		By("creating and publishing a package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "push-pub", "v1", withInit("push to published"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		publishPackage(env.Ctx, pr)

		By("attempting to push content to the Published package")
		prr := &porchv1alpha1.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: pr.Name}, prr)).To(Succeed())
		prr.Spec.Resources["illegal.yaml"] = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: illegal\n"
		err := k8sClient.Update(env.Ctx, prr)
		Expect(err).To(HaveOccurred(), "PRR push to Published package should be rejected")
	})
})
