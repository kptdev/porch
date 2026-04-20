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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	porchapi "github.com/nephio-project/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	configapi "github.com/nephio-project/porch/api/porchconfig/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Validation", Ordered, Label("infra"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	Context("Source CEL Validation", func() {
		It("should reject creation with multiple sources set", func() {
			By("creating a package with both init and clone set")
			pr := newPackageRevision(env.Namespace, env.RepoName, "multi-src", "v1", func(pr *porchv1alpha2.PackageRevision) {
				pr.Spec.Source = &porchv1alpha2.PackageSource{
					Init: &porchv1alpha2.PackageInitSpec{
						Description: "should fail",
					},
					CopyFrom: &porchv1alpha2.PackageRevisionRef{
						Name: "does-not-matter",
					},
				}
			})
			err := k8sClient.Create(env.Ctx, pr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("exactly one"))
		})

		It("should reject creation with empty source (no fields set)", func() {
			By("creating a package with source set but no fields populated")
			pr := newPackageRevision(env.Namespace, env.RepoName, "empty-src", "v1", func(pr *porchv1alpha2.PackageRevision) {
				pr.Spec.Source = &porchv1alpha2.PackageSource{}
			})
			err := k8sClient.Create(env.Ctx, pr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("exactly one"))
		})
	})

	Context("Non-existent Repository", func() {
		It("should surface Ready=False for package in non-existent repository", func() {
			By("creating a package referencing a repo that doesn't exist")
			pr := newPackageRevision(env.Namespace, "no-such-repo", "orphan-pkg", "v1", withInit("orphan test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReadyFalse(env.Ctx, pr)
		})
	})

	Context("Invalid Lifecycle Transitions", func() {
		It("should fail Published → Draft transition", func() {
			// porchctl has no command for this transition. Server-side, the
			// controller delegates to the engine which rejects it. May move
			// to a validating webhook later.

			By("creating and publishing a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "bad-lc-pd", "v1", withInit("published to draft"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			publishPackage(env.Ctx, pr)

			By("attempting Published → Draft")
			patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleDraft)
			waitForReadyFalse(env.Ctx, pr)
		})

		It("should fail Draft → DeletionProposed transition", func() {
			// porchctl propose-delete only allows Published→DeletionProposed.
			// Server-side, the controller delegates to the engine which rejects it.
			// May move to a validating webhook later.

			By("creating a draft package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "bad-lc-dd", "v1", withInit("draft to deletion"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)

			By("attempting Draft → DeletionProposed")
			patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
			waitForReadyFalse(env.Ctx, pr)
		})

		It("should fail Published → Proposed transition", func() {
			// porchctl guards this client-side (propose only allows Draft→Proposed).
			// Server-side, the controller delegates to the engine which rejects it.
			// If a validating webhook is added later, this patch would be rejected
			// at admission instead of surfacing as Ready=False.

			By("creating and publishing a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "bad-lc-pp", "v1", withInit("published to proposed"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			publishPackage(env.Ctx, pr)

			By("attempting Published → Proposed")
			patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleProposed)
			waitForReadyFalse(env.Ctx, pr)
		})
	})

	Context("Published Package Immutability", func() {
		It("should reject PRR update on a Published package", func() {
			By("creating and publishing a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pub-immut", "v1", withInit("immutability test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			publishPackage(env.Ctx, pr)

			By("attempting to update PRR on the published package")
			prr := &porchapi.PackageRevisionResources{}
			Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: pr.Name}, prr)).To(Succeed())
			prr.Spec.Resources["new-file.yaml"] = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: should-fail\n"
			err := k8sClient.Update(env.Ctx, prr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Draft"))
		})

		It("should block deletion of Published package without DeletionProposed", func() {
			// Currently enforced by the controller's finalizer: Delete sets
			// DeletionTimestamp but handleDeletion refuses to remove the
			// finalizer while lifecycle is Published. If this moves to a
			// validating webhook, the Delete call itself will fail and the
			// Consistently block below can be replaced with Expect(err).

			By("creating and publishing a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pub-del-block", "v1", withInit("delete block test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			publishPackage(env.Ctx, pr)

			By("attempting to delete without DeletionProposed")
			Expect(k8sClient.Delete(env.Ctx, pr)).To(Succeed())

			By("verifying the package persists (finalizer blocks removal)")
			Consistently(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				g.Expect(pr.DeletionTimestamp).NotTo(BeNil())
				g.Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
			}).WithTimeout(5 * time.Second).Should(Succeed())

			By("setting DeletionProposed to allow deletion")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDeletionProposed
				g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("verifying the package is now deleted")
			Eventually(func() bool {
				err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)
				return apierrors.IsNotFound(err)
			}).WithTimeout(defaultTimeout).Should(BeTrue())
		})
	})

	Context("Upgrade Negative Cases", func() {
		It("should fail upgrade with non-existent newUpstream", func() {
			By("waiting for basens/v2 discovery")
			triggerRepoSync(env.Ctx, env.Namespace, testBlueprintsRepo)
			waitForDiscovery(env.Ctx, env.Namespace, crdName(testBlueprintsRepo, "basens", "v2"))

			By("cloning basens/v1 to downstream and publishing")
			downV1 := newPackageRevision(env.Namespace, porchTestRepo, "basens", "new-up-v1",
				withCloneFromRef(crdName(testBlueprintsRepo, "basens", "v1")))
			Expect(k8sClient.Create(env.Ctx, downV1)).To(Succeed())
			waitForReady(env.Ctx, downV1)
			publishPackage(env.Ctx, downV1)

			By("creating upgrade with non-existent newUpstream")
			bad := newPackageRevision(env.Namespace, porchTestRepo, "basens", "bad-new",
				withUpgrade(crdName(testBlueprintsRepo, "basens", "v1"), "does-not-exist", downV1.Name))
			Expect(k8sClient.Create(env.Ctx, bad)).To(Succeed())
			waitForReadyFalse(env.Ctx, bad)

			By("verifying error message mentions new upstream")
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(bad), bad)).To(Succeed())
			readyCond := findCondition(bad.Status.Conditions, porchv1alpha2.ConditionReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Message).To(ContainSubstring("new upstream"))
		})

		It("should fail upgrade with non-existent currentPackage", func() {
			By("waiting for basens/v2 discovery")
			triggerRepoSync(env.Ctx, env.Namespace, testBlueprintsRepo)
			waitForDiscovery(env.Ctx, env.Namespace, crdName(testBlueprintsRepo, "basens", "v2"))

			By("creating upgrade with non-existent currentPackage")
			bad := newPackageRevision(env.Namespace, porchTestRepo, "basens", "bad-cur",
				withUpgrade(
					crdName(testBlueprintsRepo, "basens", "v1"),
					crdName(testBlueprintsRepo, "basens", "v2"),
					"does-not-exist",
				))
			Expect(k8sClient.Create(env.Ctx, bad)).To(Succeed())
			waitForReadyFalse(env.Ctx, bad)
		})
	})

	Context("Field Selectors", func() {
		It("should filter by spec.workspaceName", func() {
			By("creating a package with a known workspace")
			pr := newPackageRevision(env.Namespace, env.RepoName, "ws-sel-pkg", "ws-test", withInit("workspace selector"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)

			By("listing with workspaceName filter")
			var list porchv1alpha2.PackageRevisionList
			Expect(k8sClient.List(env.Ctx, &list, client.InNamespace(env.Namespace),
				client.MatchingFields{string(porchv1alpha2.PkgRevSelectorWorkspaceName): "ws-test"},
			)).To(Succeed())
			Expect(list.Items).NotTo(BeEmpty())
			for _, item := range list.Items {
				Expect(item.Spec.WorkspaceName).To(Equal("ws-test"))
			}
		})

		It("should filter by status.revision", func() {
			By("creating and publishing a package to get revision=1")
			pr := newPackageRevision(env.Namespace, env.RepoName, "rev-sel-pkg", "v1", withInit("revision selector"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			publishPackage(env.Ctx, pr)

			By("listing with revision=1 filter")
			var list porchv1alpha2.PackageRevisionList
			Expect(k8sClient.List(env.Ctx, &list, client.InNamespace(env.Namespace),
				client.MatchingFields{string(porchv1alpha2.PkgRevSelectorRevision): "1"},
			)).To(Succeed())
			Expect(list.Items).NotTo(BeEmpty())
			for _, item := range list.Items {
				Expect(item.Status.Revision).To(Equal(1))
			}
		})
	})

	Context("Repository Webhook Conflicts", func() {
		It("should reject duplicate git URL+branch+directory in same namespace", func() {
			By("creating a gitea repo for conflict testing")
			repoName := "conflict-test"
			createGiteaRepo(repoName)
			DeferCleanup(deleteGiteaRepo, repoName)

			By("registering the repo")
			registerV1Alpha2Repo(env.Ctx, env.Namespace, repoName)
			DeferCleanup(cleanupRepo, env.Ctx, env.Namespace, repoName)

			By("attempting to register a second repo with the same URL+branch+directory")
			duplicate := &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      repoName + "-dup",
					Namespace: env.Namespace,
					Annotations: map[string]string{
						"porch.kpt.dev/v1alpha2-migration": "true",
					},
				},
				Spec: configapi.RepositorySpec{
					Type: configapi.RepositoryTypeGit,
					Git: &configapi.GitRepository{
						Repo:   giteaRepoURL(repoName),
						Branch: "main",
					},
				},
			}
			err := k8sClient.Create(env.Ctx, duplicate)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(SatisfyAny(
				ContainSubstring("conflict"),
				ContainSubstring("Conflict"),
			))

			By("verifying the duplicate was not created")
			Expect(apierrors.IsNotFound(
				k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: repoName + "-dup"}, &configapi.Repository{}),
			)).To(BeTrue())
		})

		It("should allow same git URL on different branches", func() {
			By("creating a gitea repo for branch testing")
			repoName := "branch-conflict"
			createGiteaRepo(repoName)
			DeferCleanup(deleteGiteaRepo, repoName)

			By("registering the repo on main branch")
			registerV1Alpha2Repo(env.Ctx, env.Namespace, repoName)
			DeferCleanup(cleanupRepo, env.Ctx, env.Namespace, repoName)

			By("registering a second repo on a different branch")
			secondRepo := &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      repoName + "-dev",
					Namespace: env.Namespace,
					Annotations: map[string]string{
						"porch.kpt.dev/v1alpha2-migration": "true",
					},
				},
				Spec: configapi.RepositorySpec{
					Type: configapi.RepositoryTypeGit,
					Git: &configapi.GitRepository{
						Repo:         giteaRepoURL(repoName),
						Branch:       "develop",
						CreateBranch: true,
					},
				},
			}
			Expect(k8sClient.Create(env.Ctx, secondRepo)).To(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(env.Ctx, secondRepo)
			})
		})
	})
})
