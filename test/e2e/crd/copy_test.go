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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Copy", Ordered, Label("lifecycle"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should copy a published package to a new workspace", func() {
		By("creating and publishing the source package")
		src := newPackageRevision(env.Namespace, env.RepoName, "copy-pkg", "v1", withInit("source for copy"))
		Expect(k8sClient.Create(env.Ctx, src)).To(Succeed())
		waitForReady(env.Ctx, src)
		publishPackage(env.Ctx, src)

		By("creating a copy to a new workspace")
		dst := newPackageRevision(env.Namespace, env.RepoName, "copy-pkg", "v2", withCopyFrom(src.Name))
		Expect(k8sClient.Create(env.Ctx, dst)).To(Succeed())
		waitForReady(env.Ctx, dst)

		By("verifying copy metadata")
		Expect(dst.Status.CreationSource).To(Equal("copy"))
		Expect(dst.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDraft))

		By("verifying copied content matches source")
		resources := getPRRResources(env.Ctx, env.Namespace, dst.Name)
		Expect(resources).To(HaveKey("Kptfile"))
		Expect(resources["Kptfile"]).To(ContainSubstring("source for copy"))
	})

	It("should publish a copied package and update latest-revision", func() {
		By("creating and publishing v1")
		src := newPackageRevision(env.Namespace, env.RepoName, "pub-copy", "v1", withInit("publish copy test"))
		Expect(k8sClient.Create(env.Ctx, src)).To(Succeed())
		waitForReady(env.Ctx, src)
		publishPackage(env.Ctx, src)

		By("copying to v2 and publishing")
		dst := newPackageRevision(env.Namespace, env.RepoName, "pub-copy", "v2", withCopyFrom(src.Name))
		Expect(k8sClient.Create(env.Ctx, dst)).To(Succeed())
		waitForReady(env.Ctx, dst)
		publishPackage(env.Ctx, dst)

		By("verifying v2 is latest")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(dst), dst)).To(Succeed())
			g.Expect(dst.Labels).To(HaveKeyWithValue(porchv1alpha2.LatestPackageRevisionKey, porchv1alpha2.LatestPackageRevisionValue))
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("verifying v1 is no longer latest")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(src), src)).To(Succeed())
			g.Expect(src.Labels[porchv1alpha2.LatestPackageRevisionKey]).To(Equal("false"))
		}).WithTimeout(defaultTimeout).Should(Succeed())
	})

	It("should fail copy from different package name", func() {
		By("creating and publishing a source package")
		src := newPackageRevision(env.Namespace, env.RepoName, "src-pkg", "v1", withInit("wrong name test"))
		Expect(k8sClient.Create(env.Ctx, src)).To(Succeed())
		waitForReady(env.Ctx, src)
		publishPackage(env.Ctx, src)

		By("attempting to copy into a different package name")
		bad := newPackageRevision(env.Namespace, env.RepoName, "wrong-pkg", "v1", withCopyFrom(src.Name))
		Expect(k8sClient.Create(env.Ctx, bad)).To(Succeed())
		waitForReadyFalse(env.Ctx, bad)

		By("verifying error message mentions same package")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(bad), bad)).To(Succeed())
		readyCond := findCondition(bad.Status.Conditions, porchv1alpha2.ConditionReady)
		Expect(readyCond).NotTo(BeNil())
		Expect(readyCond.Message).To(ContainSubstring("same package"))
	})

	It("should fail copy from non-existent source", func() {
		By("attempting to copy from a source that doesn't exist")
		bad := newPackageRevision(env.Namespace, env.RepoName, "no-src", "v1",
			withCopyFrom(crdName(env.RepoName, "no-src", "does-not-exist")))
		Expect(k8sClient.Create(env.Ctx, bad)).To(Succeed())

		By("verifying Ready=False")
		waitForReadyFalse(env.Ctx, bad)
	})
})
