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
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/upstreamdependents"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Subpackage dependency tracking. A subpackage cloned from a published in-cluster
// PackageRevision records that upstream on the dependent's status, and the
// controller mirrors the relationship into an UpstreamDependents object keyed by
// the upstream. Deleting the upstream is refused while a live dependent still
// references it; the check verifies each listed dependent from the cache, so
// stale entries never block.
var _ = Describe("UpstreamDependents", Ordered, Label("lifecycle", "upstream-dependents"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("records a subpackage dependency on both the dependent and the upstream", func() {
		const (
			repo = "ud-bidir"
			dir  = "network"
		)
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-a", "v1")
		parent := createSubpkgPR(env, repo, "consumer-a", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, parent)

		By("cloning the published upstream into a subpackage")
		Expect(cloneSubpackage(env.Ctx, parent, upstream.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, parent)

		By("recording the upstream on the dependent's status")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(parent), parent)).To(Succeed())
			g.Expect(parent.Status.SubpackageUpstreams).To(ContainElement(SatisfyAll(
				HaveField("Path", dir),
				HaveField("Name", upstream.Name),
			)))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("recording the dependent on the upstream's UpstreamDependents object")
		udWaitForDependent(env, upstream.Name, parent.Name, dir)
	})

	It("blocks deleting an upstream that a subpackage still uses", func() {
		const (
			repo = "ud-blocks"
			dir  = "network"
		)
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-b", "v1")
		parent := createSubpkgPR(env, repo, "consumer-b", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, parent)

		Expect(cloneSubpackage(env.Ctx, parent, upstream.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, parent)
		udWaitForDependent(env, upstream.Name, parent.Name, dir)

		By("attempting to delete the upstream")
		err := udAttemptDelete(env.Ctx, upstream)
		Expect(err).To(HaveOccurred(), "deleting a referenced upstream must be refused")
		Expect(err.Error()).To(ContainSubstring("referenced by"))
		Expect(err.Error()).To(ContainSubstring(parent.Name))

		By("confirming the upstream still exists")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(upstream), upstream)).To(Succeed())
	})

	It("allows deleting the upstream once the dependent is deleted", func() {
		const (
			repo = "ud-clears"
			dir  = "network"
		)
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-c", "v1")
		parent := createSubpkgPR(env, repo, "consumer-c", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, parent)

		Expect(cloneSubpackage(env.Ctx, parent, upstream.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, parent)
		udWaitForDependent(env, upstream.Name, parent.Name, dir)

		By("deleting the dependent")
		deletePackage(env.Ctx, parent)

		By("deleting the upstream; the entry left behind by the dependent must not block")
		udDeleteWhenFree(env.Ctx, upstream)
	})

	It("moves the dependency to the new upstream when a subpackage is upgraded", func() {
		const (
			repo = "ud-upgrade"
			dir  = "network"
		)
		udSetupRepo(env, repo)

		v1 := udPublishedUpstream(env, repo, "vendor-d", "v1")
		v2 := udPublishedUpstream(env, repo, "vendor-d", "v2")
		parent := createSubpkgPR(env, repo, "consumer-d", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, parent)

		Expect(cloneSubpackage(env.Ctx, parent, v1.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, parent)
		udWaitForDependent(env, v1.Name, parent.Name, dir)

		By("upgrading the subpackage from v1 to v2")
		Expect(upgradeSubpackage(env.Ctx, parent, v1.Name, v2.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, parent)

		By("the dependent now records v2 and no longer v1")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(parent), parent)).To(Succeed())
			g.Expect(parent.Status.SubpackageUpstreams).To(ContainElement(SatisfyAll(
				HaveField("Path", dir),
				HaveField("Name", v2.Name),
			)))
			g.Expect(parent.Status.SubpackageUpstreams).NotTo(ContainElement(HaveField("Name", v1.Name)))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("v1 is deletable even though its UpstreamDependents entry is now stale")
		udDeleteWhenFree(env.Ctx, v1)

		By("v2 is blocked by the dependent")
		udWaitForDependent(env, v2.Name, parent.Name, dir)
		err := udAttemptDelete(env.Ctx, v2)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("referenced by"))
	})

	It("stays blocked until every dependent is gone", func() {
		const (
			repo = "ud-multi"
		)
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-e", "v1")
		first := createSubpkgPR(env, repo, "consumer-e1", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, first)
		second := createSubpkgPR(env, repo, "consumer-e2", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, second)

		Expect(cloneSubpackage(env.Ctx, first, upstream.Name, "a")).To(Succeed())
		waitForReady(env.Ctx, first)
		Expect(cloneSubpackage(env.Ctx, second, upstream.Name, "b")).To(Succeed())
		waitForReady(env.Ctx, second)
		udWaitForDependent(env, upstream.Name, first.Name, "a")
		udWaitForDependent(env, upstream.Name, second.Name, "b")

		By("deleting the first dependent")
		deletePackage(env.Ctx, first)

		By("the upstream stays blocked, and the remaining dependent is named")
		err := udAttemptDelete(env.Ctx, upstream)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(second.Name))

		By("deleting the second dependent")
		deletePackage(env.Ctx, second)

		By("the upstream is now deletable")
		udDeleteWhenFree(env.Ctx, upstream)
	})
})

// udObjectName returns the UpstreamDependents object name for an in-cluster upstream.
func udObjectName(upstreamName string) string {
	return upstreamdependents.ObjectName(
		upstreamdependents.IdentityForPackageRevision(upstreamName), upstreamName)
}

// udSetupRepo registers a dedicated gitea repo and its Repository, cleaned up after the test.
func udSetupRepo(env *testEnv, repo string) {
	createGiteaRepo(repo)
	registerV1Alpha2Repo(env.Ctx, env.Namespace, repo)
	DeferCleanup(func() {
		cleanupRepo(env.Ctx, env.Namespace, repo)
		deleteGiteaRepo(repo)
	})
}

// udPublishedUpstream creates an init'd package and publishes it, so it can be cloned from.
func udPublishedUpstream(env *testEnv, repo, pkg, ws string) *porchv1alpha2.PackageRevision {
	pr := createSubpkgPR(env, repo, pkg, ws)
	DeferCleanup(deletePackageIfPresent, env.Ctx, pr)
	publishPackage(env.Ctx, pr)
	return pr
}

// udAttemptDelete makes one attempt to delete a package, proposing deletion first when
// it is Published. It returns the first error from the webhook, or nil on success.
func udAttemptDelete(ctx context.Context, pr *porchv1alpha2.PackageRevision) error {
	current := &porchv1alpha2.PackageRevision{}
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pr), current); err != nil {
		return err
	}
	if current.Spec.Lifecycle == porchv1alpha2.PackageRevisionLifecyclePublished {
		current.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDeletionProposed
		if err := k8sClient.Update(ctx, current); err != nil {
			return err
		}
	}
	return k8sClient.Delete(ctx, current)
}

// udDeleteWhenFree retries deletion until the webhook allows it, then waits for the object to go.
func udDeleteWhenFree(ctx context.Context, pr *porchv1alpha2.PackageRevision) {
	Eventually(func(g Gomega) {
		g.Expect(udAttemptDelete(ctx, pr)).To(Succeed())
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

	Eventually(func() bool {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pr), pr)
		return apierrors.IsNotFound(err)
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(BeTrue())
}

// udWaitForDependent waits until the upstream's UpstreamDependents object lists the dependent at path.
func udWaitForDependent(env *testEnv, upstreamName, dependentName, path string) {
	Eventually(func(g Gomega) {
		obj := &porchv1alpha2.UpstreamDependents{}
		g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
			Namespace: env.Namespace,
			Name:      udObjectName(upstreamName),
		}, obj)).To(Succeed())
		g.Expect(obj.Status.Dependents).To(ContainElement(
			porchv1alpha2.UpstreamDependent{Name: dependentName, Path: path}))
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
}

var _ = Describe("UpstreamDependents lifecycle edges", Ordered, Label("lifecycle", "upstream-dependents"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("refuses to propose deletion of a referenced upstream, leaving it Published", func() {
		const (
			repo = "ud-propose"
			dir  = "network"
		)
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-f", "v1")
		parent := createSubpkgPR(env, repo, "consumer-f", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, parent)

		Expect(cloneSubpackage(env.Ctx, parent, upstream.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, parent)
		udWaitForDependent(env, upstream.Name, parent.Name, dir)

		By("proposing deletion of the upstream on its own")
		current := &porchv1alpha2.PackageRevision{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(upstream), current)).To(Succeed())
		current.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDeletionProposed
		err := k8sClient.Update(env.Ctx, current)
		Expect(err).To(HaveOccurred(), "proposing deletion of a referenced upstream must be refused")
		Expect(err.Error()).To(ContainSubstring("referenced by"))

		By("confirming the upstream is still Published")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(upstream), current)).To(Succeed())
		Expect(current.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
	})

	It("releases the upstream when its repository is removed along with the dependent", func() {
		const (
			repo = "ud-cascade"
			dir  = "network"
		)
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-g", "v1")
		parent := createSubpkgPR(env, repo, "consumer-g", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, parent)

		Expect(cloneSubpackage(env.Ctx, parent, upstream.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, parent)
		udWaitForDependent(env, upstream.Name, parent.Name, dir)

		By("removing the repository, which removes its packages")
		cleanupRepo(env.Ctx, env.Namespace, repo)

		By("waiting for the dependent and then the upstream to be gone")
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(parent), parent))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(BeTrue())
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(upstream), upstream))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(BeTrue())
	})

	It("records an external git dependency by locator, keyed off the upstream's locator", func() {
		const (
			repo        = "ud-external"
			externalReg = "ud-external-src"
			dir         = "external-subpackage"
		)
		udSetupRepo(env, repo)

		// The external upstream lives in its own registered repo, published via the normal flow.
		createGiteaRepo(externalReg)
		registerV1Alpha2Repo(env.Ctx, env.Namespace, externalReg)
		DeferCleanup(func() {
			cleanupRepo(env.Ctx, env.Namespace, externalReg)
			deleteGiteaRepo(externalReg)
		})
		externalPR := createSubpkgPR(env, externalReg, "ext-pkg", "v1")
		publishPackage(env.Ctx, externalPR)
		DeferCleanup(deletePackageIfPresent, env.Ctx, externalPR)

		parent := createSubpkgPR(env, repo, "consumer-h", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, parent)

		By("cloning the subpackage from the external git source by ref and directory")
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(parent), parent); err != nil {
				return err
			}
			parent.Spec.SubpackageOperation = &porchv1alpha2.SubpackageOperation{
				SubpackageDir: dir,
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					Type: porchv1alpha2.RepositoryTypeGit,
					Git: &porchv1alpha2.GitPackage{
						Repo:      giteaBaseURL() + "/porch/" + externalReg + ".git",
						Ref:       "ext-pkg/v1",
						Directory: "/ext-pkg",
					},
				},
			}
			return k8sClient.Update(env.Ctx, parent)
		})).To(Succeed())
		waitForReady(env.Ctx, parent)

		By("finding the locator recorded for the subpackage")
		var locator *porchv1alpha2.Locator
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(parent), parent)).To(Succeed())
			for _, up := range parent.Status.SubpackageUpstreams {
				if up.Path == dir && up.Name == "" && up.Locator != nil {
					locator = up.Locator
				}
			}
			g.Expect(locator).NotTo(BeNil())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("recording the dependent on the UpstreamDependents object keyed by that locator")
		objName := upstreamdependents.ObjectName(
			upstreamdependents.IdentityForLocator(locator),
			upstreamdependents.ReadableForLocator(locator))
		Eventually(func(g Gomega) {
			obj := &porchv1alpha2.UpstreamDependents{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: objName}, obj)).To(Succeed())
			g.Expect(obj.Status.Dependents).To(ContainElement(
				porchv1alpha2.UpstreamDependent{Name: parent.Name, Path: dir}))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})
})

var _ = Describe("UpstreamDependents under contention and rehoming", Ordered, Label("lifecycle", "upstream-dependents"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("records every dependent cloned concurrently onto one upstream object", func() {
		const (
			repo      = "ud-concurrent"
			dependent = 6
		)
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-i", "v1")

		parents := make([]*porchv1alpha2.PackageRevision, dependent)
		for i := range parents {
			parents[i] = createSubpkgPR(env, repo, fmt.Sprintf("consumer-i%d", i), "v1")
			DeferCleanup(deletePackageIfPresent, env.Ctx, parents[i])
		}

		By("cloning the upstream into every dependent at the same time")
		errs := make(chan error, dependent)
		var wg sync.WaitGroup
		for i, parent := range parents {
			wg.Add(1)
			go func(i int, parent *porchv1alpha2.PackageRevision) {
				defer wg.Done()
				errs <- cloneSubpackage(env.Ctx, parent, upstream.Name, fmt.Sprintf("dir-%d", i))
			}(i, parent)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			Expect(err).NotTo(HaveOccurred())
		}

		By("waiting for every dependent to reference the upstream")
		for i, parent := range parents {
			waitForReady(env.Ctx, parent)
			udWaitForDependent(env, upstream.Name, parent.Name, fmt.Sprintf("dir-%d", i))
		}

		By("confirming the shared object lists all dependents and no more")
		Eventually(func(g Gomega) {
			obj, err := udGetDependents(env.Ctx, env.Namespace, upstream.Name)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(obj.Status.Dependents).To(HaveLen(dependent))
			g.Expect(obj.Status.DependentCount).To(Equal(dependent))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	It("keeps the upstream blocked after its repository is rehomed", func() {
		const (
			repo      = "ud-rehome"
			divergent = "ud-rehome-div"
			dir       = "network"
		)
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-j", "v1")
		parent := createSubpkgPR(env, repo, "consumer-j", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, parent)

		Expect(cloneSubpackage(env.Ctx, parent, upstream.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, parent)
		udWaitForDependent(env, upstream.Name, parent.Name, dir)

		By("creating an unrelated repository to rehome to")
		createGiteaRepo(divergent)
		DeferCleanup(deleteGiteaRepo, divergent)

		By("rehoming the repository to it; the commit hash check fails, but the CRD change sticks")
		rehomeRepo(env.Ctx, env.Namespace, repo, giteaRepoURL(divergent))
		waitForRepoError(env.Ctx, env.Namespace, repo, "not present in the history")

		By("confirming the upstream is still refused deletion, because identity is by name")
		err := udAttemptDelete(env.Ctx, upstream)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("referenced by"))
	})
})

// udGetDependents returns the UpstreamDependents object for an upstream in the shared namespace.
func udGetDependents(ctx context.Context, namespace, upstreamName string) (*porchv1alpha2.UpstreamDependents, error) {
	obj := &porchv1alpha2.UpstreamDependents{}
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: udObjectName(upstreamName)}, obj)
	return obj, err
}

var _ = Describe("UpstreamDependents with an unparseable subpackage Kptfile", Ordered, Label("lifecycle", "upstream-dependents"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("keeps the upstream blocked while the dependent's subpackage Kptfile is unparseable", func() {
		const (
			repo = "ud-badkptfile"
			dir  = "network"
		)
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-k", "v1")
		parent := createSubpkgPR(env, repo, "consumer-k", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, parent)

		Expect(cloneSubpackage(env.Ctx, parent, upstream.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, parent)
		udWaitForDependent(env, upstream.Name, parent.Name, dir)

		By("breaking the subpackage Kptfile so it no longer parses")
		updatePRRResources(env.Ctx, env.Namespace, parent.Name, map[string]string{
			dir + "/Kptfile": "kind: [unterminated",
		})

		By("confirming the upstream is still refused deletion")
		Eventually(func(g Gomega) {
			err := udAttemptDelete(env.Ctx, upstream)
			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring("referenced by"))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})
})

// The main placeholder tracks the git HEAD of the branch, so a subpackage cloned
// into a later revision stays in main's content after that revision is deleted.
// Publishing must refresh main's dependencies, or the upstream becomes deletable.
var _ = Describe("UpstreamDependents via the main placeholder", Ordered, Label("lifecycle", "upstream-dependents"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("keeps the upstream blocked after the revision that cloned it is deleted", func() {
		const (
			repo = "ud-main"
			pkg  = "consumer-main"
			dir  = "network"
		)
		udSetupRepo(env, repo)

		// The upstream is cleaned up with the repository: main keeps referencing it
		// until the repository, and with it the main placeholder, is removed.
		upstream := createSubpkgPR(env, repo, "vendor-main", "v1")
		publishPackage(env.Ctx, upstream)

		parent := createSubpkgPR(env, repo, pkg, "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, parent)
		publishPackage(env.Ctx, parent)

		By("copying the parent to a second revision that clones the upstream")
		rev2 := createSubpkgCopy(env, repo, parent, "v2")
		DeferCleanup(deletePackageIfPresent, env.Ctx, rev2)
		waitForReady(env.Ctx, rev2)
		Expect(cloneSubpackage(env.Ctx, rev2, upstream.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, rev2)
		waitForRendered(env.Ctx, rev2)

		By("publishing the second revision")
		publishPackage(env.Ctx, rev2)

		mainName := fmt.Sprintf("%s.%s.main", repo, pkg)
		By("waiting for the main placeholder to record the cloned upstream")
		main := &porchv1alpha2.PackageRevision{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: mainName}, main)).To(Succeed())
			g.Expect(main.Status.SubpackageUpstreams).To(ContainElement(HaveField("Name", upstream.Name)))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		udWaitForDependent(env, upstream.Name, mainName, dir)

		By("deleting the second revision")
		deletePackage(env.Ctx, rev2)

		By("attempting to delete the upstream; main's content still clones it")
		err := udAttemptDelete(env.Ctx, upstream)
		Expect(err).To(HaveOccurred(), "deleting an upstream still cloned by main must be refused")
		Expect(err.Error()).To(ContainSubstring("referenced by"))
		Expect(err.Error()).To(ContainSubstring(mainName))

		By("confirming the upstream still exists")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(upstream), upstream)).To(Succeed())
	})
})

// Releasing an upstream from the main placeholder: once a later publish no longer
// clones it and the revisions that did are gone, main stops recording it.
var _ = Describe("UpstreamDependents release via the main placeholder", Ordered, Label("lifecycle", "upstream-dependents"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("releases an upstream once a later publish upgrades the subpackage away from it", func() {
		const (
			repo = "ud-main-release"
			pkg  = "consumer-release"
			dir  = "network"
		)
		udSetupRepo(env, repo)

		// The upstreams have no cleanup of their own: main keeps referencing upstream B
		// until the repository, and with it the main placeholder, is removed.
		upA := createSubpkgPR(env, repo, "vendor-ra", "v1")
		publishPackage(env.Ctx, upA)
		upB := createSubpkgPR(env, repo, "vendor-rb", "v1")
		publishPackage(env.Ctx, upB)

		parent := createSubpkgPR(env, repo, pkg, "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, parent)
		publishPackage(env.Ctx, parent)

		mainName := fmt.Sprintf("%s.%s.main", repo, pkg)

		By("publishing a second revision that clones upstream A")
		rev2 := createSubpkgCopy(env, repo, parent, "v2")
		DeferCleanup(deletePackageIfPresent, env.Ctx, rev2)
		waitForReady(env.Ctx, rev2)
		Expect(cloneSubpackage(env.Ctx, rev2, upA.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, rev2)
		waitForRendered(env.Ctx, rev2)
		publishPackage(env.Ctx, rev2)
		udWaitForDependent(env, upA.Name, mainName, dir)

		By("publishing a third revision that upgrades the subpackage to upstream B")
		rev3 := createSubpkgCopy(env, repo, rev2, "v3")
		DeferCleanup(deletePackageIfPresent, env.Ctx, rev3)
		waitForReady(env.Ctx, rev3)
		Expect(upgradeSubpackage(env.Ctx, rev3, upA.Name, upB.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, rev3)
		waitForRendered(env.Ctx, rev3)
		publishPackage(env.Ctx, rev3)

		By("deleting the second revision, which still clones upstream A")
		deletePackage(env.Ctx, rev2)

		By("waiting for main to stop recording upstream A and record upstream B")
		Eventually(func(g Gomega) {
			main := &porchv1alpha2.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: mainName}, main)).To(Succeed())
			g.Expect(main.Status.SubpackageUpstreams).NotTo(ContainElement(HaveField("Name", upA.Name)))
			g.Expect(main.Status.SubpackageUpstreams).To(ContainElement(HaveField("Name", upB.Name)))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("deleting upstream A, which no live package or main references")
		udDeleteWhenFree(env.Ctx, upA)

		By("upstream B stays blocked by the third revision and main")
		udWaitForDependent(env, upB.Name, mainName, dir)
		err := udAttemptDelete(env.Ctx, upB)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("referenced by"))
	})
})

// udRow is one dependent edge as the package-wide lookup prints it.
type udRow struct {
	Upstream  string
	Dependent string
	Path      string
}

// udDependentsOfPackage returns the dependent edges of every upstream revision of one package.
// It applies the same filter as the documented kubectl lookup: list the namespace's objects,
// keep those whose spec.upstreamPackage matches, then flatten their dependents.
func udDependentsOfPackage(ctx context.Context, namespace, packageKey string) []udRow {
	list := &porchv1alpha2.UpstreamDependentsList{}
	Expect(k8sClient.List(ctx, list, client.InNamespace(namespace))).To(Succeed())

	var rows []udRow
	for _, obj := range list.Items {
		if obj.Spec.UpstreamPackage != packageKey {
			continue
		}
		for _, d := range obj.Status.Dependents {
			rows = append(rows, udRow{Upstream: obj.Spec.UpstreamKey, Dependent: d.Name, Path: d.Path})
		}
	}
	return rows
}

// udFetch returns the UpstreamDependents object for an upstream revision.
func udFetch(env *testEnv, upstreamName string) *porchv1alpha2.UpstreamDependents {
	obj := &porchv1alpha2.UpstreamDependents{}
	Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
		Namespace: env.Namespace,
		Name:      udObjectName(upstreamName),
	}, obj)).To(Succeed())
	return obj
}

// udCloneWhole creates a PackageRevision that clones the whole upstream package.
func udCloneWhole(env *testEnv, repo, pkg, ws, upstreamName string) *porchv1alpha2.PackageRevision {
	pr := newPackageRevision(env.Namespace, repo, pkg, ws, withCloneFromRef(upstreamName))
	Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
	DeferCleanup(deletePackageIfPresent, env.Ctx, pr)
	return pr
}

var _ = Describe("UpstreamDependents whole-package coverage", Ordered, Label("lifecycle", "upstream-dependents"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("records a whole-package clone with an empty path and the package key", func() {
		const repo = "ud-cov-whole"
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-w", "v1")
		dep := udCloneWhole(env, repo, "consumer-w", "v1", upstream.Name)
		waitForReady(env.Ctx, dep)

		By("listing the dependent with an empty path on the upstream's object")
		udWaitForDependent(env, upstream.Name, dep.Name, "")

		By("setting the package key from the upstream's repository and package")
		Expect(udFetch(env, upstream.Name).Spec.UpstreamPackage).To(Equal(repo + ".vendor-w"))

		By("giving bottom-up the dependent's own upstream lock")
		Expect(dep.Status.UpstreamLock).NotTo(BeNil())
	})

	It("refuses DeletionProposed for a referenced upstream and leaves it Published", func() {
		const repo = "ud-cov-propose"
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-p", "v1")
		dep := udCloneWhole(env, repo, "consumer-p", "v1", upstream.Name)
		waitForReady(env.Ctx, dep)
		udWaitForDependent(env, upstream.Name, dep.Name, "")

		By("proposing deletion of the referenced upstream")
		current := &porchv1alpha2.PackageRevision{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(upstream), current)).To(Succeed())
		current.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDeletionProposed
		err := k8sClient.Update(env.Ctx, current)
		Expect(err).To(HaveOccurred(), "the proposal must be refused before the destructive step")
		Expect(err.Error()).To(ContainSubstring("referenced by"))

		By("the upstream stays Published, so no blocked version is left in DeletionProposed")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(upstream), current)).To(Succeed())
		Expect(current.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
	})

	It("blocks deleting a whole-package upstream until the dependent is deleted", func() {
		const repo = "ud-cov-blocks"
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-b", "v1")
		dep := udCloneWhole(env, repo, "consumer-b", "v1", upstream.Name)
		waitForReady(env.Ctx, dep)
		udWaitForDependent(env, upstream.Name, dep.Name, "")

		By("refusing the delete and naming the whole-package dependent")
		err := udAttemptDelete(env.Ctx, upstream)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("referenced by"))
		Expect(err.Error()).To(ContainSubstring(dep.Name))

		By("deleting the dependent removes its entry from the upstream")
		deletePackage(env.Ctx, dep)
		Eventually(func(g Gomega) {
			g.Expect(udFetch(env, upstream.Name).Status.Dependents).To(BeEmpty())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("the upstream is deletable")
		udDeleteWhenFree(env.Ctx, upstream)
	})

	It("keeps the old upstream blocked until the dependent is gone, and moves the dependency on upgrade", func() {
		const repo = "ud-cov-upgrade"
		udSetupRepo(env, repo)

		v1 := udPublishedUpstream(env, repo, "vendor-y", "v1")
		v2 := udPublishedUpstream(env, repo, "vendor-y", "v2")
		dep := udCloneWhole(env, repo, "consumer-y", "v1", v1.Name)
		waitForReady(env.Ctx, dep)
		udWaitForDependent(env, v1.Name, dep.Name, "")

		By("publishing the dependent, since an upgrade needs a published current package")
		publishPackage(env.Ctx, dep)

		By("creating an upgrade of the dependent from v1 to v2")
		upgraded := newPackageRevision(env.Namespace, repo, "consumer-y", "v2",
			withUpgrade(v1.Name, v2.Name, dep.Name))
		Expect(k8sClient.Create(env.Ctx, upgraded)).To(Succeed())
		DeferCleanup(deletePackageIfPresent, env.Ctx, upgraded)
		waitForReady(env.Ctx, upgraded)

		By("the upgraded revision is recorded against v2")
		udWaitForDependent(env, v2.Name, upgraded.Name, "")

		By("v1 is still blocked by the original dependent")
		err := udAttemptDelete(env.Ctx, v1)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(dep.Name))

		By("v2 is blocked by the upgraded revision")
		err = udAttemptDelete(env.Ctx, v2)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(upgraded.Name))

		By("once the original dependent is gone, v1 is deletable")
		deletePackage(env.Ctx, dep)
		udDeleteWhenFree(env.Ctx, v1)
	})

	It("lists every revision's dependents for one package and excludes other packages", func() {
		const repo = "ud-cov-pkg"
		udSetupRepo(env, repo)

		v1 := udPublishedUpstream(env, repo, "vendor-z", "v1")
		v2 := udPublishedUpstream(env, repo, "vendor-z", "v2")
		other := udPublishedUpstream(env, repo, "other-z", "v1")

		whole := udCloneWhole(env, repo, "consumer-za", "v1", v1.Name)
		waitForReady(env.Ctx, whole)

		sub := createSubpkgPR(env, repo, "consumer-zb", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, sub)
		Expect(cloneSubpackage(env.Ctx, sub, v2.Name, "network")).To(Succeed())
		waitForReady(env.Ctx, sub)

		unrelated := udCloneWhole(env, repo, "consumer-zc", "v1", other.Name)
		waitForReady(env.Ctx, unrelated)

		By("waiting for each upstream to record its dependent")
		udWaitForDependent(env, v1.Name, whole.Name, "")
		udWaitForDependent(env, v2.Name, sub.Name, "network")
		udWaitForDependent(env, other.Name, unrelated.Name, "")

		By("reporting the count and truncation state alongside the list")
		for _, up := range []*porchv1alpha2.PackageRevision{v1, v2} {
			obj := udFetch(env, up.Name)
			Expect(obj.Status.DependentCount).To(Equal(len(obj.Status.Dependents)))
			Expect(obj.Status.Truncated).To(BeFalse())
		}

		By("the package-wide lookup returns both revisions' dependents and no others")
		Eventually(func(g Gomega) {
			g.Expect(udDependentsOfPackage(env.Ctx, env.Namespace, repo+".vendor-z")).To(ConsistOf(
				udRow{Upstream: v1.Name, Dependent: whole.Name, Path: ""},
				udRow{Upstream: v2.Name, Dependent: sub.Name, Path: "network"},
			))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	It("answers who depends on an upstream through a field selector on the API server", func() {
		const repo = "ud-cov-query"
		udSetupRepo(env, repo)

		up := udPublishedUpstream(env, repo, "vendor-q", "v1")
		dep := udCloneWhole(env, repo, "consumer-q", "v1", up.Name)
		waitForReady(env.Ctx, dep)
		udWaitForDependent(env, up.Name, dep.Name, "")

		By("listing by upstream key returns that upstream's object with its dependents and write time")
		Eventually(func(g Gomega) {
			list := &porchv1alpha2.UpstreamDependentsList{}
			g.Expect(k8sClient.List(env.Ctx, list, client.InNamespace(env.Namespace),
				client.MatchingFields{"spec.upstreamKey": up.Name})).To(Succeed())
			g.Expect(list.Items).To(HaveLen(1))
			g.Expect(list.Items[0].Status.Dependents).To(ContainElement(porchv1alpha2.UpstreamDependent{Name: dep.Name}))
			g.Expect(list.Items[0].Status.LastWritten).NotTo(BeNil())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("listing by package key returns the same object")
		Eventually(func(g Gomega) {
			list := &porchv1alpha2.UpstreamDependentsList{}
			g.Expect(k8sClient.List(env.Ctx, list, client.InNamespace(env.Namespace),
				client.MatchingFields{"spec.upstreamPackage": repo + ".vendor-q"})).To(Succeed())
			g.Expect(list.Items).To(HaveLen(1))
			g.Expect(list.Items[0].Spec.UpstreamKey).To(Equal(up.Name))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})
})

// The restart spec restarts the PackageRevision controllers, which adds about two minutes to a run,
// so it is optional. Set E2E_RESTART=1 to run it. It covers the startup pass in one restart: a lost
// entry comes back, an object without a package key gets one, and an orphaned object is swept.
var _ = Describe("UpstreamDependents startup pass on restart", Ordered, Label("lifecycle", "upstream-dependents", "restart"), func() {
	var env *testEnv

	BeforeAll(func() {
		if os.Getenv("E2E_RESTART") != "1" {
			Skip("controller restart spec is optional; set E2E_RESTART=1 to run it")
		}
		env = sharedEnv()
	})

	It("restores lost entries, fills in package keys, prunes stale entries and sweeps orphans in one restart", func() {
		const repo = "ud-cov-restart"
		udSetupRepo(env, repo)

		By("recording a dependent, then losing its upstream's object")
		lost := udPublishedUpstream(env, repo, "vendor-k", "v1")
		lostDep := udCloneWhole(env, repo, "consumer-k", "v1", lost.Name)
		waitForReady(env.Ctx, lostDep)
		udWaitForDependent(env, lost.Name, lostDep.Name, "")
		Expect(k8sClient.Delete(env.Ctx, udFetch(env, lost.Name))).To(Succeed())

		By("recording a dependent on an object that has no package key")
		legacy := udPublishedUpstream(env, repo, "vendor-l", "v1")
		legacyDep := udCloneWhole(env, repo, "consumer-l", "v1", legacy.Name)
		waitForReady(env.Ctx, legacyDep)
		udWaitForDependent(env, legacy.Name, legacyDep.Name, "")
		Expect(k8sClient.Patch(env.Ctx, udFetch(env, legacy.Name),
			client.RawPatch(types.MergePatchType, []byte(`{"spec":{"upstreamPackage":null}}`)))).To(Succeed())
		Expect(udFetch(env, legacy.Name).Spec.UpstreamPackage).To(BeEmpty())

		By("adding a stale entry for a dependent that no longer exists to the live upstream")
		Eventually(func(g Gomega) {
			obj := udFetch(env, legacy.Name)
			obj.Status.Dependents = append(obj.Status.Dependents, porchv1alpha2.UpstreamDependent{Name: "gone-stale"})
			obj.Status.DependentCount = len(obj.Status.Dependents)
			g.Expect(k8sClient.Status().Update(env.Ctx, obj)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("creating an orphaned object whose only dependent does not exist")
		orphanUpstream := repo + "-orphan"
		orphan := &porchv1alpha2.UpstreamDependents{
			ObjectMeta: metav1.ObjectMeta{Name: udObjectName(orphanUpstream), Namespace: env.Namespace},
			Spec: porchv1alpha2.UpstreamDependentsSpec{
				UpstreamKey: upstreamdependents.IdentityForPackageRevision(orphanUpstream),
			},
		}
		Expect(k8sClient.Create(env.Ctx, orphan)).To(Succeed())
		orphan.Status.Dependents = []porchv1alpha2.UpstreamDependent{{Name: "gone-dependent"}}
		orphan.Status.DependentCount = 1
		Expect(k8sClient.Status().Update(env.Ctx, orphan)).To(Succeed())

		By("restarting the controllers")
		out, err := exec.Command("kubectl", "rollout", "restart", "deployment/porch-controllers",
			"-n", "porch-system").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
		out, err = exec.Command("kubectl", "rollout", "status", "deployment/porch-controllers",
			"-n", "porch-system", "--timeout=5m").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))

		By("the startup pass restores the lost entry")
		Eventually(func(g Gomega) {
			g.Expect(udGet(g, env, lost.Name).Status.Dependents).To(ContainElement(
				porchv1alpha2.UpstreamDependent{Name: lostDep.Name}))
		}).WithTimeout(6 * time.Minute).WithPolling(defaultInterval).Should(Succeed())

		By("the startup pass fills in the missing package key")
		Eventually(func(g Gomega) {
			g.Expect(udGet(g, env, legacy.Name).Spec.UpstreamPackage).To(Equal(repo + ".vendor-l"))
		}).WithTimeout(6 * time.Minute).WithPolling(defaultInterval).Should(Succeed())

		By("the startup sweep removes the stale entry and keeps the live dependent")
		Eventually(func(g Gomega) {
			deps := udGet(g, env, legacy.Name).Status.Dependents
			g.Expect(deps).NotTo(ContainElement(porchv1alpha2.UpstreamDependent{Name: "gone-stale"}))
			g.Expect(deps).To(ContainElement(porchv1alpha2.UpstreamDependent{Name: legacyDep.Name}))
		}).WithTimeout(6 * time.Minute).WithPolling(defaultInterval).Should(Succeed())

		By("the startup sweep removes the orphaned object")
		Eventually(func() bool {
			err := k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: udObjectName(orphanUpstream)},
				&porchv1alpha2.UpstreamDependents{})
			return apierrors.IsNotFound(err)
		}).WithTimeout(6 * time.Minute).WithPolling(defaultInterval).Should(BeTrue())
	})
})

// udGet reads an UpstreamDependents object inside an Eventually, so a missing object is retried.
func udGet(g Gomega, env *testEnv, upstreamName string) *porchv1alpha2.UpstreamDependents {
	obj := &porchv1alpha2.UpstreamDependents{}
	g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: udObjectName(upstreamName)},
		obj)).To(Succeed())
	return obj
}

var _ = Describe("UpstreamDependents subpackage release", Ordered, Label("lifecycle", "upstream-dependents"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("releases the upstream once the subpackage's files are removed from the dependent", func() {
		const (
			repo = "ud-cov-remove"
			dir  = "network"
		)
		udSetupRepo(env, repo)

		upstream := udPublishedUpstream(env, repo, "vendor-r", "v1")
		parent := createSubpkgPR(env, repo, "consumer-r", "v1")
		DeferCleanup(deletePackageIfPresent, env.Ctx, parent)

		Expect(cloneSubpackage(env.Ctx, parent, upstream.Name, dir)).To(Succeed())
		waitForReady(env.Ctx, parent)
		udWaitForDependent(env, upstream.Name, parent.Name, dir)

		By("removing the subpackage's files from the dependent")
		resources := getPRRResources(env.Ctx, env.Namespace, parent.Name)
		kept := map[string]string{}
		for path, content := range resources {
			if !strings.HasPrefix(path, dir+"/") {
				kept[path] = content
			}
		}
		replacePRRResources(env.Ctx, env.Namespace, parent.Name, kept)

		By("the dependent no longer records the subpackage upstream")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(parent), parent)).To(Succeed())
			g.Expect(parent.Status.SubpackageUpstreams).To(BeEmpty())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("the upstream no longer lists the dependent")
		Eventually(func(g Gomega) {
			g.Expect(udFetch(env, upstream.Name).Status.Dependents).NotTo(ContainElement(
				porchv1alpha2.UpstreamDependent{Name: parent.Name, Path: dir}))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("the upstream is deletable")
		udDeleteWhenFree(env.Ctx, upstream)
	})
})

// The subpackage delete guard reads the controller's cache index, which can lag the API server. This spec
// measures the gap rather than asserting it: a delete attempted right after a subpackage clone may be
// accepted, and the clone becomes visible to the guard after a short delay. The numbers go to
// GinkgoWriter. Run it with -ginkgo.label-filter=upstream-dependents-race.
var _ = Describe("UpstreamDependents delete race window", Ordered, Label("upstream-dependents-race"), func() {
	var env *testEnv

	BeforeAll(func() {
		if os.Getenv("E2E_RACE") != "1" {
			Skip("race-window measurement is optional; set E2E_RACE=1 to run it")
		}
		env = sharedEnv()
	})

	It("measures how long a fresh subpackage clone takes to reach the delete guard", func() {
		const (
			repo       = "ud-race"
			iterations = 10
		)
		udSetupRepo(env, repo)

		accepted := 0
		var visible []time.Duration
		for i := range iterations {
			upstream := createSubpkgPR(env, repo, fmt.Sprintf("vendor-r%d", i), "v1")
			publishPackage(env.Ctx, upstream)
			parent := createSubpkgPR(env, repo, fmt.Sprintf("consumer-r%d", i), "v1")

			start := time.Now()
			Expect(cloneSubpackage(env.Ctx, parent, upstream.Name, "network")).To(Succeed())

			// A delete attempted right after the clone is the racing case.
			if err := udAttemptDelete(env.Ctx, upstream); err == nil {
				// The guard let it through, so the upstream is already going. Wait for it to be gone.
				accepted++
				fmt.Fprintf(GinkgoWriter, "iteration %d: delete accepted inside the window\n", i)
				Eventually(func() bool {
					err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(upstream), upstream)
					return apierrors.IsNotFound(err)
				}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(BeTrue())
				deletePackageIfPresent(env.Ctx, parent)
				continue
			}

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(parent), parent)).To(Succeed())
				g.Expect(parent.Status.SubpackageUpstreams).To(ContainElement(HaveField("Name", upstream.Name)))
			}).WithTimeout(defaultTimeout).WithPolling(10 * time.Millisecond).Should(Succeed())
			visible = append(visible, time.Since(start))

			deletePackageIfPresent(env.Ctx, parent)
			udDeleteWhenFree(env.Ctx, upstream)
		}

		sort.Slice(visible, func(a, b int) bool { return visible[a] < visible[b] })
		fmt.Fprintf(GinkgoWriter, "race window: %d of %d immediate deletes accepted\n", accepted, iterations)
		if len(visible) > 0 {
			fmt.Fprintf(GinkgoWriter, "clone to guard-visible: p50=%s max=%s (over %d refused cases)\n",
				visible[len(visible)/2], visible[len(visible)-1], len(visible))
		}
	})
})
