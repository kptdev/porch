//go:build direct_publish

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
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Direct-publication specs require --allow-direct-publication-from-draft on
// porch-server and --packagerevisions.allow-direct-publication-from-draft on
// porch-controllers. This file is compiled only with -tags=direct_publish, so
// the default CRD suite and CI do not pick it up. Run with
// make test-e2e-crd-direct-publish after enabling the flags.
var _ = Describe("DirectPublish", Ordered, Label("direct-publish"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should transition Draft → Published directly", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "direct-publish-pkg", "v1", withInit("direct publish test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDraft))

		By("publishing the package without proposing")
		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
		waitForPublished(env.Ctx, pr)

		By("verifying published lifecycle and revision")
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
		Expect(pr.Status.Revision).NotTo(Equal(0))

		By("verifying publish metadata")
		Expect(pr.Status.PublishedBy).NotTo(BeEmpty())
		Expect(pr.Status.PublishedAt).NotTo(BeNil())

		By("verifying Rendered condition observedGeneration is current after direct publish")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
			g.Expect(pr.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(porchv1alpha2.ConditionRendered)),
				HaveField("ObservedGeneration", Equal(pr.Generation)),
			)))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	It("should allow UPDATE: Draft → Published", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "draft-to-published", "v1", withInit("test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDraft))
		DeferCleanup(func() {
			deletePackage(env.Ctx, pr) //nolint:errcheck
		})

		By("publishing directly from Draft")
		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
		waitForPublished(env.Ctx, pr)

		By("verifying the lifecycle changed to Published")
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
		Expect(pr.Status.Revision).NotTo(Equal(0))
	})
})
