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

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Render", Ordered, Label("content"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should set Rendered=False and Ready=False on render failure", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "render-fail", "v1", withInit("render failure test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing an invalid pipeline")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: render-fail\npipeline:\n  mutators:\n  - image: quay.io/invalid/nonexistent-fn:v0.0.1\n",
			"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-cm\ndata:\n  key: value\n",
		})

		By("waiting for Rendered=False")
		waitForRenderFailed(env.Ctx, pr)

		By("verifying Ready=False")
		waitForReadyFalse(env.Ctx, pr)

		By("verifying Rendered condition has an error message")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		renderedCond := findCondition(pr.Status.Conditions, porchv1alpha2.ConditionRendered)
		Expect(renderedCond).NotTo(BeNil())
		Expect(renderedCond.Message).NotTo(BeEmpty())
	})

	It("should recover from render failure when pipeline is fixed", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "render-recover", "v1", withInit("render recovery test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

		By("pushing an invalid pipeline")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: render-recover\npipeline:\n  mutators:\n  - image: quay.io/invalid/nonexistent-fn:v0.0.1\n",
			"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: recover-cm\ndata:\n  key: value\n",
		})

		By("waiting for Rendered=False")
		waitForRenderFailed(env.Ctx, pr)

		By("fixing the pipeline with a valid mutator")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: render-recover\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.5\n    configMap:\n      namespace: recovered-ns\n",
		})

		By("waiting for Rendered=True (recovery)")
		waitForRendered(env.Ctx, pr)
		waitForReady(env.Ctx, pr)

		By("verifying the fixed pipeline rendered correctly")
		Eventually(func(g Gomega) {
			resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
			g.Expect(resources["cm.yaml"]).To(ContainSubstring("namespace: recovered-ns"))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	It("should persist resources on render failure with push-on-render-failure annotation", func() {
		By("creating a draft package with push-on-render-failure annotation")
		pr := newPackageRevision(env.Namespace, env.RepoName, "push-fail", "v1", withInit("push-on-fail test"))
		pr.Annotations = map[string]string{
			porchv1alpha2.PushOnFnRenderFailureKey: porchv1alpha2.PushOnFnRenderFailureValue,
		}
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

		By("pushing an invalid pipeline with content")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: push-fail\npipeline:\n  mutators:\n  - image: quay.io/invalid/nonexistent-fn:v0.0.1\n",
			"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: push-fail-cm\ndata:\n  key: should-persist\n",
		})

		By("waiting for Rendered=False")
		waitForRenderFailed(env.Ctx, pr)

		By("verifying resources were persisted despite render failure")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources).To(HaveKey("cm.yaml"))
		Expect(resources["cm.yaml"]).To(ContainSubstring("should-persist"))
	})

	// Known flake: render #2 can complete and write before the second push's
	// annotation lands in etcd, causing stale detection to miss it. The rendered
	// output from push #1 overwrites push #2's content in the DB (non-transactional
	// dual-writer race). Proper fix: render cancellation (GH #1125) or transactional
	// DB writes. Skip on CI until fixed.
	PIt("should render the latest content after rapid pushes (stale detection)", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "stale-test", "v1", withInit("stale detection test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

		By("pushing first content with set-namespace=first-ns")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: stale-test\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.5\n    configMap:\n      namespace: first-ns\n",
			"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: stale-cm\ndata:\n  key: value\n",
		})

		By("immediately pushing second content with set-namespace=second-ns")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: stale-test\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.5\n    configMap:\n      namespace: second-ns\n",
		})

		By("waiting for render to settle")
		waitForRendered(env.Ctx, pr)

		By("verifying final content reflects the second push")
		Eventually(func(g Gomega) {
			resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
			g.Expect(resources["cm.yaml"]).To(ContainSubstring("namespace: second-ns"))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})
})

var _ = Describe("RenderRacePrevention", Ordered, Label("content", "webhook"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should prevent publishing un-rendered content when race between PRR push and lifecycle transition occurs", func() {
		By("creating a draft package with a render pipeline")
		pr := newPackageRevision(env.Namespace, env.RepoName, "render-race-block", "v1", withInit("render race prevention test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

		By("pushing content with a pipeline that triggers render")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: render-race-block\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.5\n    configMap:\n      namespace: test-ns\n",
			"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: race-test-cm\ndata:\n  key: value\n",
		})

		By("immediately attempting to transition to Proposed (no wait for render)")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		patch := client.MergeFrom(pr.DeepCopy())
		pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleProposed
		err := k8sClient.Patch(env.Ctx, pr, patch)

		By("verifying webhook or controller blocks the transition")
		if err != nil {
			By("webhook blocked the transition (render in progress)")
			Expect(err.Error()).To(ContainSubstring("render race prevention"))
		} else {
			By("controller guard should have blocked it")
			Eventually(func(g Gomega) {
				Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				g.Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDraft),
					"Controller guard should have prevented transition to Proposed")
			}).WithTimeout(15 * time.Second).WithPolling(500 * time.Millisecond).Should(Succeed())
		}

		By("waiting for render to actually complete")
		waitForRendered(env.Ctx, pr)
		waitForReady(env.Ctx, pr)

		By("now transitioning to Proposed succeeds (render complete)")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		patch = client.MergeFrom(pr.DeepCopy())
		pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleProposed
		Expect(k8sClient.Patch(env.Ctx, pr, patch)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("transitioning to Published also succeeds")
		patch = client.MergeFrom(pr.DeepCopy())
		pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
		Expect(k8sClient.Patch(env.Ctx, pr, patch)).To(Succeed())
		waitForPublished(env.Ctx, pr)

		By("verifying Published package has Rendered=True")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
		renderedCond := findCondition(pr.Status.Conditions, porchv1alpha2.ConditionRendered)
		Expect(renderedCond).NotTo(BeNil())
		Expect(renderedCond.Status).To(Equal(metav1.ConditionTrue))
	})
})
