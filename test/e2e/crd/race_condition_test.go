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
	porchv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("PRR Race Condition - Ready=True Guarantees Resource Availability", Ordered, Label("race-condition"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should set Rendered=True condition after render completes", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "race-test-pkg", "v1", withInit("race condition test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())

		By("waiting for Ready condition")
		waitForReady(env.Ctx, pr)

		By("verifying Rendered condition is set to True")
		Eventually(func(g Gomega) {
			updated := &porchv1alpha2.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), updated)).To(Succeed())
			g.Expect(updated.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(porchv1alpha2.ConditionRendered)),
				HaveField("Status", Equal(metav1.ConditionTrue)),
				HaveField("Reason", Equal(porchv1alpha2.ReasonRendered)),
			)))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	It("should guarantee PRR is queryable when Ready=True", func() {
		By("creating a draft package with content")
		pr := newPackageRevision(env.Namespace, env.RepoName, "prr-query-test", "v1", withInit("PRR query test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("fetching PRR should succeed (guaranteed by Ready=True condition)")
		prr := &porchv1alpha1.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
			Namespace: env.Namespace,
			Name:      pr.Name,
		}, prr)).To(Succeed())
		Expect(prr.Spec.Resources).To(HaveKey("Kptfile"))
	})

	It("should maintain Ready=True when PRR is updated", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "prr-update-test", "v1", withInit("PRR update test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("updating PRR with new content")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"test.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-cm\n",
		})

		By("waiting for async render to complete")
		waitForRendered(env.Ctx, pr)

		By("verifying Ready condition is still True after render completes")
		Eventually(func(g Gomega) {
			updated := &porchv1alpha2.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), updated)).To(Succeed())
			g.Expect(updated.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(porchv1alpha2.ConditionReady)),
				HaveField("Status", Equal(metav1.ConditionTrue)),
			)))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("verifying updated PRR is immediately queryable")
		prr := &porchv1alpha1.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
			Namespace: env.Namespace,
			Name:      pr.Name,
		}, prr)).To(Succeed())
		Expect(prr.Spec.Resources).To(HaveKey("test.yaml"))
	})

	It("should guarantee PRR availability after initial render", func() {
		By("creating a package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "transient-test", "v1", withInit("transient state test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())

		By("waiting for Ready condition")
		waitForReady(env.Ctx, pr)

		By("verifying Ready=True guarantees PRR queryability")
		prr := &porchv1alpha1.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
			Namespace: env.Namespace,
			Name:      pr.Name,
		}, prr)).To(Succeed())
		Expect(prr.Spec.Resources).To(HaveKey("Kptfile"))

		By("verifying Ready condition is present")
		updated := &porchv1alpha2.PackageRevision{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), updated)).To(Succeed())
		Expect(updated.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", Equal(porchv1alpha2.ConditionReady)),
			HaveField("Status", Equal(metav1.ConditionTrue)),
		)))
	})

	It("should verify resources are available before setting Ready=True", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "render-fail-test", "v1", withInit("render fail test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("verifying Rendered=True after initial render")
		Eventually(func(g Gomega) {
			updated := &porchv1alpha2.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), updated)).To(Succeed())
			g.Expect(updated.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(porchv1alpha2.ConditionRendered)),
				HaveField("Status", Equal(metav1.ConditionTrue)),
			)))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("updating PRR with invalid content to trigger render failure")
		// Update with a broken function reference that will fail to render
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-package
spec:
  pipeline:
    mutators:
    - image: invalid-function:does-not-exist
`,
		})

		By("waiting for render to fail")
		Eventually(func(g Gomega) {
			updated := &porchv1alpha2.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), updated)).To(Succeed())
			// Rendered should be False after failure
			g.Expect(updated.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(porchv1alpha2.ConditionRendered)),
				HaveField("Status", Equal(metav1.ConditionFalse)),
			)))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("verifying Ready=False after render failure")
		updated := &porchv1alpha2.PackageRevision{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), updated)).To(Succeed())
		Expect(updated.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", Equal(porchv1alpha2.ConditionReady)),
			HaveField("Status", Equal(metav1.ConditionFalse)),
		)))
	})
})
