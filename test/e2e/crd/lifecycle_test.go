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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Lifecycle", Ordered, Label("lifecycle"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should transition Draft → Proposed → Published", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "test-pkg", "v1", withInit("lifecycle test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("proposing the package")
		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleProposed)
		waitForReady(env.Ctx, pr)

		By("publishing the package")
		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
		waitForPublished(env.Ctx, pr)

		By("verifying revision is set")
		Expect(pr.Status.Revision).NotTo(Equal(0))

		By("verifying publish metadata")
		Expect(pr.Status.PublishedBy).NotTo(BeEmpty())
		Expect(pr.Status.PublishedAt).NotTo(BeNil())
	})

	It("should transition Published → DeletionProposed → Published (undo)", func() {
		By("creating and publishing a package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "undo-pkg", "v1", withInit("undo test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		publishPackage(env.Ctx, pr)

		By("proposing deletion")
		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
		waitForReady(env.Ctx, pr)
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDeletionProposed))

		By("undoing deletion proposal (back to Published)")
		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
		waitForReady(env.Ctx, pr)
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
	})

	It("should transition Proposed → Draft (reject)", func() {
		By("creating and proposing a package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "reject-pkg", "v1", withInit("reject test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleProposed)
		waitForReady(env.Ctx, pr)
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleProposed))

		By("rejecting back to Draft")
		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleDraft)
		waitForReady(env.Ctx, pr)
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDraft))
	})

	It("should delete a Draft package directly", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "del-draft", "v1", withInit("delete draft"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("deleting the draft")
		Expect(k8sClient.Delete(env.Ctx, pr)).To(Succeed())

		By("verifying the package is gone")
		Eventually(func() bool {
			err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)
			return apierrors.IsNotFound(err)
		}).WithTimeout(defaultTimeout).Should(BeTrue())
	})

	It("should delete a Proposed package directly", func() {
		By("creating and proposing a package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "del-proposed", "v1", withInit("delete proposed"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleProposed)
		waitForReady(env.Ctx, pr)

		By("deleting the proposed package")
		Expect(k8sClient.Delete(env.Ctx, pr)).To(Succeed())

		By("verifying the package is gone")
		Eventually(func() bool {
			err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)
			return apierrors.IsNotFound(err)
		}).WithTimeout(defaultTimeout).Should(BeTrue())
	})

	It("should delete a Published package via DeletionProposed", func() {
		By("creating and publishing a package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "del-pub", "v1", withInit("delete published"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		publishPackage(env.Ctx, pr)

		By("proposing deletion and deleting")
		deletePackage(env.Ctx, pr)

		By("verifying the package is gone")
		Eventually(func() bool {
			err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)
			return apierrors.IsNotFound(err)
		}).WithTimeout(defaultTimeout).Should(BeTrue())
	})

	It("should delete and recreate a package with a new workspace", func() {
		By("creating and publishing v1")
		pr := newPackageRevision(env.Namespace, env.RepoName, "recreate-pkg", "v1", withInit("first"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		publishPackage(env.Ctx, pr)

		By("deleting v1")
		deletePackage(env.Ctx, pr)
		Eventually(func() bool {
			err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)
			return apierrors.IsNotFound(err)
		}).WithTimeout(defaultTimeout).Should(BeTrue())

		By("recreating with a new workspace")
		// Use a new workspace — the DB retains revision history per repo.pkg.ws
		pr2 := newPackageRevision(env.Namespace, env.RepoName, "recreate-pkg", "v2", withInit("second"))
		Expect(k8sClient.Create(env.Ctx, pr2)).To(Succeed())
		waitForReady(env.Ctx, pr2)
	})

	It("should restore latest-revision label after deleting the latest revision", func() {
		By("creating and publishing v1")
		pr1 := newPackageRevision(env.Namespace, env.RepoName, "latest-pkg", "v1", withInit("v1"))
		Expect(k8sClient.Create(env.Ctx, pr1)).To(Succeed())
		waitForReady(env.Ctx, pr1)
		publishPackage(env.Ctx, pr1)

		By("verifying v1 is latest")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr1), pr1)).To(Succeed())
			g.Expect(pr1.Labels).To(HaveKeyWithValue(porchv1alpha2.LatestPackageRevisionKey, porchv1alpha2.LatestPackageRevisionValue))
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("creating and publishing v2 (copy of v1)")
		pr2 := newPackageRevision(env.Namespace, env.RepoName, "latest-pkg", "v2", withCopyFrom(pr1.Name))
		Expect(k8sClient.Create(env.Ctx, pr2)).To(Succeed())
		waitForReady(env.Ctx, pr2)
		publishPackage(env.Ctx, pr2)

		By("verifying v2 is now latest")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr2), pr2)).To(Succeed())
			g.Expect(pr2.Labels).To(HaveKeyWithValue(porchv1alpha2.LatestPackageRevisionKey, porchv1alpha2.LatestPackageRevisionValue))
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("verifying v1 is no longer latest")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr1), pr1)).To(Succeed())
			g.Expect(pr1.Labels[porchv1alpha2.LatestPackageRevisionKey]).To(Equal("false"))
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("deleting v2")
		deletePackage(env.Ctx, pr2)
		Eventually(func() bool {
			err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr2), pr2)
			return apierrors.IsNotFound(err)
		}).WithTimeout(defaultTimeout).Should(BeTrue())

		By("verifying v1 is promoted back to latest")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr1), pr1)).To(Succeed())
			g.Expect(pr1.Labels).To(HaveKeyWithValue(porchv1alpha2.LatestPackageRevisionKey, porchv1alpha2.LatestPackageRevisionValue))
		}).WithTimeout(defaultTimeout).Should(Succeed())
	})

	It("should increment revision numbers correctly for subfolder packages", func() {
		// In v1alpha2, packages with subfolder paths use slashes in spec.packageName
		// but dots in metadata.name (RFC 1123 compliance). porchctl handles this
		// via ComposePkgRevObjName.

		By("creating and publishing a package with a subfolder path")
		pr1 := newPackageRevision(env.Namespace, env.RepoName, "sub/folder/pkg", "v1", withInit("subfolder v1"))
		pr1.Name = crdName(env.RepoName, "sub.folder.pkg", "v1")
		Expect(k8sClient.Create(env.Ctx, pr1)).To(Succeed())
		waitForReady(env.Ctx, pr1)

		By("verifying spec.packageName preserves the subfolder path")
		Expect(pr1.Spec.PackageName).To(Equal("sub/folder/pkg"))

		By("publishing and verifying revision is 1")
		publishPackage(env.Ctx, pr1)
		Expect(pr1.Status.Revision).To(Equal(1))

		By("copying to v2 and publishing")
		pr2 := newPackageRevision(env.Namespace, env.RepoName, "sub/folder/pkg", "v2", withCopyFrom(pr1.Name))
		pr2.Name = crdName(env.RepoName, "sub.folder.pkg", "v2")
		Expect(k8sClient.Create(env.Ctx, pr2)).To(Succeed())
		waitForReady(env.Ctx, pr2)
		publishPackage(env.Ctx, pr2)

		By("verifying revision incremented to 2")
		Expect(pr2.Status.Revision).To(Equal(2))
	})
})
