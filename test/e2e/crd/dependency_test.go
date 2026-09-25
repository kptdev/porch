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

// Exercises the dependency-tracking feature: sub-package upstream projection
// onto PackageRevision status, reverse dependency lookup, and pre-deletion
// guards (DELETE and DeletionProposed) that block removing an in-use upstream.
var _ = Describe("Dependency tracking", Ordered, Label("dependency"), func() {
	var (
		env           *testEnv
		repo          = "dep-track"
		subpackageDir = "vendored/netfn"
	)

	BeforeAll(func() {
		env = sharedEnv()
		createGiteaRepo(repo)
		registerV1Alpha2Repo(env.Ctx, env.Namespace, repo)
		DeferCleanup(func() {
			cleanupRepo(env.Ctx, env.Namespace, repo)
			deleteGiteaRepo(repo)
		})
	})

	// vendorKeyFor returns the upstream key the controller will record for a
	// sub-package cloned from the given published vendor. It is derived from the
	// vendor's own resolved selfLock (repo|dir|ref) — the exact locator the
	// sub-package's upstreamLock will carry — rather than guessing the ref.
	vendorKeyFor := func(vendor *porchv1alpha2.PackageRevision) string {
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(vendor), vendor)).To(Succeed())
		Expect(vendor.Status.SelfLock).NotTo(BeNil(), "vendor selfLock not resolved")
		return porchv1alpha2.UpstreamKey(vendor.Status.SelfLock)
	}

	It("projects sub-package upstreams and supports bidirectional lookup", func() {
		By("publishing a vendor blueprint")
		vendor := createSubpkgPR(env, repo, "clonee-pkg", "v1")
		publishPackage(env.Ctx, vendor)
		DeferCleanup(deletePackage, env.Ctx, vendor)

		By("cloning the vendor as a sub-package into a customized blueprint")
		custom := createSubpkgPR(env, repo, "parent-pkg", "v1")
		DeferCleanup(deletePackage, env.Ctx, custom)
		Expect(cloneSubpackage(env.Ctx, custom, vendor.Name, subpackageDir)).To(Succeed())
		waitForReady(env.Ctx, custom)

		By("verifying the sub-package Kptfile was fetched with a resolved upstreamLock")
		Eventually(func(g Gomega) {
			res := getPRRResources(env.Ctx, env.Namespace, custom.Name)
			g.Expect(res[subpackageDir+"/Kptfile"]).To(ContainSubstring("upstreamLock:"))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("bottom-up: custom's status projects the sub-package upstream")
		key := vendorKeyFor(vendor)
		waitForUpstreamKeys(env.Ctx, custom, key)
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(custom), custom)).To(Succeed())
		Expect(custom.Status.SubpackageUpstreams).To(ContainElement(SatisfyAll(
			HaveField("Path", Equal(subpackageDir)),
			HaveField("Upstream", Not(BeNil())),
		)))
		Expect(custom.Status.DependencyTruncated).To(BeFalse())

		By("top-down: reverse lookup finds the customized blueprint as a dependent")
		deps := reverseQueryDependents(env.Ctx, env.Namespace, key)
		names := make([]string, len(deps))
		for i, d := range deps {
			names[i] = d.Name
		}
		Expect(names).To(ContainElement(custom.Name))
	})

	It("blocks deletion of a vendor blueprint referenced as a sub-package", func() {
		By("publishing a vendor and cloning it as a sub-package")
		vendor := createSubpkgPR(env, repo, "clonee-pkg", "guard-v1")
		publishPackage(env.Ctx, vendor)

		// This spec deletes both custom and vendor itself (to prove unblocking),
		// so no DeferCleanup for them.
		custom := createSubpkgPR(env, repo, "parent-pkg", "guard-c1")
		Expect(cloneSubpackage(env.Ctx, custom, vendor.Name, subpackageDir)).To(Succeed())
		waitForReady(env.Ctx, custom)
		waitForUpstreamKeys(env.Ctx, custom, vendorKeyFor(vendor))

		By("DeletionProposed on the vendor is rejected (early guard)")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(vendor), vendor)).To(Succeed())
		vendor.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDeletionProposed
		err := k8sClient.Update(env.Ctx, vendor)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cannot propose deletion"))

		By("direct DELETE of the vendor is rejected and names the dependent")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(vendor), vendor)).To(Succeed())
		err = k8sClient.Delete(env.Ctx, vendor)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(custom.Name))

		By("removing the dependent unblocks vendor deletion")
		deletePackage(env.Ctx, custom)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(custom), custom)).To(MatchError(ContainSubstring("not found")))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(vendor), vendor)).To(Succeed())
			vendor.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDeletionProposed
			g.Expect(k8sClient.Update(env.Ctx, vendor)).To(Succeed())
			g.Expect(k8sClient.Delete(env.Ctx, vendor)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	It("does not block deletion of an unreferenced blueprint", func() {
		By("publishing a standalone vendor with no dependents")
		lonely := createSubpkgPR(env, repo, "clonee-pkg", "lonely-v1")
		publishPackage(env.Ctx, lonely)

		By("DeletionProposed and DELETE both succeed")
		patchLifecycle(env.Ctx, lonely, porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
		Expect(k8sClient.Delete(env.Ctx, lonely)).To(Succeed())
	})

	It("clears the dependency after a sub-package upgrade moves to a new vendor version", func() {
		By("publishing two independent vendor blueprints")
		// Independent packages (distinct names) so neither depends on the other;
		// this isolates the upgrade's effect on the customized blueprint.
		vOld := createSubpkgPR(env, repo, "vendor-old", "v1")
		publishPackage(env.Ctx, vOld)
		DeferCleanup(deletePackage, env.Ctx, vOld)
		vNew := createSubpkgPR(env, repo, "vendor-new", "v1")
		publishPackage(env.Ctx, vNew)
		DeferCleanup(deletePackage, env.Ctx, vNew)

		var custom *porchv1alpha2.PackageRevision
		DeferCleanup(func() {
			if custom != nil {
				deletePackage(env.Ctx, custom)
			}
		})

		keyOld := porchv1alpha2.UpstreamKey(func() *porchv1alpha2.Locator {
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(vOld), vOld)).To(Succeed())
			return vOld.Status.SelfLock
		}())
		keyNew := porchv1alpha2.UpstreamKey(func() *porchv1alpha2.Locator {
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(vNew), vNew)).To(Succeed())
			return vNew.Status.SelfLock
		}())

		By("cloning vendor-old as a sub-package, then upgrading to vendor-new")
		custom = createSubpkgPR(env, repo, "parent-pkg", "up-c1")
		Expect(cloneSubpackage(env.Ctx, custom, vOld.Name, subpackageDir)).To(Succeed())
		waitForReady(env.Ctx, custom)
		waitForUpstreamKeys(env.Ctx, custom, keyOld)

		Expect(upgradeSubpackage(env.Ctx, custom, vOld.Name, vNew.Name, subpackageDir)).To(Succeed())
		waitForReady(env.Ctx, custom)

		By("the projection now points at vendor-new and no longer at vendor-old")
		waitForUpstreamKeys(env.Ctx, custom, keyNew)
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(custom), custom)).To(Succeed())
		Expect(custom.Status.UpstreamKeys).NotTo(ContainElement(keyOld))

		By("vendor-old is now deletable, vendor-new is not")
		patchLifecycle(env.Ctx, vOld, porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(vNew), vNew)).To(Succeed())
		vNew.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDeletionProposed
		Expect(k8sClient.Update(env.Ctx, vNew)).To(HaveOccurred())
	})
})

var _ = Describe("Dependency tracking - top-level clone", Ordered, Label("dependency"), func() {
	var env *testEnv

	BeforeAll(func() { env = sharedEnv() })

	It("projects a top-level clone upstream via status.upstreamLock", func() {
		By("cloning a blueprint at the top level (not a sub-package)")
		pr := newPackageRevision(env.Namespace, porchTestRepo, "basens", "dep-top-v1",
			withCloneFromRef(crdName(testBlueprintsRepo, "basens", "v1")))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		DeferCleanup(deletePackage, env.Ctx, pr)

		By("status.upstreamKeys includes the top-level upstream")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
			g.Expect(pr.Status.UpstreamLock).NotTo(BeNil())
			g.Expect(pr.Status.UpstreamKeys).To(ContainElement(porchv1alpha2.UpstreamKey(pr.Status.UpstreamLock)))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})
})
