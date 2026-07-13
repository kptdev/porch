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
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Webhook Validation", Ordered, Label("validation"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	// --- Repository validation tests ---

	Describe("Repository validation", func() {
		It("should reject CREATE when repository does not exist", func() {
			By("attempting to create a package with non-existent repository")
			pr := newPackageRevision(env.Namespace, "nonexistent-repo", "pkg", "v1", withInit("test"))
			err := k8sClient.Create(env.Ctx, pr)

			By("verifying creation is rejected with error")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Or(
				ContainSubstring("repository"),
				ContainSubstring("not found"),
				ContainSubstring("webhook")),
				"error should mention repository or webhook validation")
		})

		It("should reject CREATE when repository lacks v1alpha2-migration annotation", func() {
			By("creating a new gitea repo without v1alpha2-migration annotation")
			repoName := "no-annot-repo"
			createGiteaRepo(repoName)
			DeferCleanup(deleteGiteaRepo, repoName)

			secretName := repoName + "-auth"
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: env.Namespace,
				},
				Immutable: ptr.To(true),
				Data: map[string][]byte{
					"username": []byte(giteaUser),
					"password": []byte(giteaPassword),
				},
				Type: corev1.SecretTypeBasicAuth,
			}
			Expect(k8sClient.Create(env.Ctx, secret)).To(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(env.Ctx, secret) //nolint:errcheck
			})

			repo := &configapi.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:        repoName,
					Namespace:   env.Namespace,
					Annotations: map[string]string{}, // Deliberately omit v1alpha2-migration
				},
				Spec: configapi.RepositorySpec{
					Type: configapi.RepositoryTypeGit,
					Git: &configapi.GitRepository{
						Repo:   giteaRepoURL(repoName),
						Branch: "main",
						SecretRef: configapi.SecretRef{
							Name: secretName,
						},
					},
				},
			}
			Expect(k8sClient.Create(env.Ctx, repo)).To(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(env.Ctx, repo) //nolint:errcheck
			})
			waitForRepoReady(env.Ctx, env.Namespace, repoName)

			By("creating a package in repository without v1alpha2-migration annotation")
			pr := newPackageRevision(env.Namespace, repoName, "pkg", "v1", withInit("test"))
			err := k8sClient.Create(env.Ctx, pr)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Or(ContainSubstring("v1alpha2-migration"), ContainSubstring("not enabled")))
		})
	})

	// --- Source specification validation tests ---

	Describe("Source specification validation", func() {
		It("should reject CREATE when source has no fields specified", func() {
			By("creating a package with empty source")
			pr := newPackageRevision(env.Namespace, env.RepoName, "empty-src", "v1")
			pr.Spec.Source = &porchv1alpha2.PackageSource{} // No Init, CopyFrom, etc.

			err := k8sClient.Create(env.Ctx, pr)

			By("verifying creation is rejected")
			Expect(err).To(HaveOccurred())
			// CEL validation rejects this before webhook runs
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
		})
	})

	// --- Lifecycle validation tests ---

	Describe("Lifecycle validation", func() {
		It("should allow CREATE with Draft lifecycle", func() {
			By("creating a package with Draft lifecycle")
			pr := newPackageRevision(env.Namespace, env.RepoName, "draft-allowed", "v1",
				withInit("draft test"))
			Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDraft))

			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			DeferCleanup(func() {
				k8sClient.Delete(env.Ctx, pr) //nolint:errcheck
			})
		})

		It("should allow CREATE with Proposed lifecycle", func() {
			By("creating a package with Proposed lifecycle")
			pr := newPackageRevision(env.Namespace, env.RepoName, "proposed-allowed", "v1",
				withInit("proposed test"))
			pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleProposed

			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			DeferCleanup(func() {
				k8sClient.Delete(env.Ctx, pr) //nolint:errcheck
			})
		})

		It("should reject UPDATE: Published → Draft", func() {
			By("creating and publishing a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "noupgrade-pub-draft", "v1", withInit("test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			publishPackage(env.Ctx, pr)
			DeferCleanup(func() {
				deletePackage(env.Ctx, pr) //nolint:errcheck
			})

			By("attempting to downgrade back to Draft with retries on conflict")
			var finalErr error
			const maxRetries = 3
			for i := 0; i < maxRetries; i++ {
				prFresh := &porchv1alpha2.PackageRevision{}
				Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), prFresh)).To(Succeed())
				prFresh.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDraft
				finalErr = k8sClient.Update(env.Ctx, prFresh)
				if finalErr == nil || !apierrors.IsConflict(finalErr) {
					break
				}
			}

			By("verifying the final error is a validation error (invalid transition)")
			Expect(finalErr).To(HaveOccurred())
			Expect(finalErr.Error()).To(SatisfyAny(
				ContainSubstring("lifecycle"),
				ContainSubstring("transition"),
				ContainSubstring("invalid"),
			))
		})

		It("should reject UPDATE: Published → Proposed", func() {
			By("creating and publishing a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "noupgrade-pub-proposed", "v1", withInit("test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			publishPackage(env.Ctx, pr)
			DeferCleanup(func() {
				deletePackage(env.Ctx, pr) //nolint:errcheck
			})

			By("attempting to transition back to Proposed with retries on conflict")
			var finalErr error
			const maxRetries = 3
			for i := 0; i < maxRetries; i++ {
				prFresh := &porchv1alpha2.PackageRevision{}
				Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), prFresh)).To(Succeed())
				prFresh.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleProposed
				finalErr = k8sClient.Update(env.Ctx, prFresh)
				if finalErr == nil || !apierrors.IsConflict(finalErr) {
					break
				}
			}

			By("verifying the final error is a validation error (invalid transition)")
			Expect(finalErr).To(HaveOccurred())
			Expect(finalErr.Error()).To(SatisfyAny(
				ContainSubstring("lifecycle"),
				ContainSubstring("transition"),
				ContainSubstring("invalid"),
			))
		})

		It("should allow UPDATE: DeletionProposed → Published", func() {
			By("creating and publishing a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "republish-allow", "v1", withInit("test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			publishPackage(env.Ctx, pr)
			DeferCleanup(func() {
				k8sClient.Delete(env.Ctx, pr) //nolint:errcheck
			})

			By("proposing deletion")
			patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
			waitForReady(env.Ctx, pr)
			Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDeletionProposed))

			By("rejecting deletion (transition DeletionProposed → Published)")
			patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
			waitForReady(env.Ctx, pr)

			By("verifying the lifecycle changed to Published")
			Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
		})
	})

	// --- Immutable fields validation tests ---

	Describe("Immutable fields validation", func() {
		It("should reject UPDATE when repository is changed", func() {
			By("creating a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "immut-repo", "v1", withInit("test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			DeferCleanup(func() {
				k8sClient.Delete(env.Ctx, pr) //nolint:errcheck
			})

			By("attempting to change repository")
			prFresh := &porchv1alpha2.PackageRevision{}
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), prFresh)).To(Succeed())
			prFresh.Spec.RepositoryName = "different-repo"
			err := k8sClient.Update(env.Ctx, prFresh)

			By("verifying the update is rejected")
			Expect(err).To(HaveOccurred())
			// Accept either validation error or conflict (both indicate immutability is enforced)
			errMsg := err.Error()
			Expect(errMsg).To(SatisfyAny(
				ContainSubstring("immutable"),
				ContainSubstring("repository"),
				ContainSubstring("object has been modified"),
			))
		})

		It("should reject UPDATE when packageName is changed", func() {
			By("creating a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "immut-pkg", "v1", withInit("test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			DeferCleanup(func() {
				k8sClient.Delete(env.Ctx, pr) //nolint:errcheck
			})

			By("attempting to change packageName")
			prFresh := &porchv1alpha2.PackageRevision{}
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), prFresh)).To(Succeed())
			prFresh.Spec.PackageName = "different-package"
			err := k8sClient.Update(env.Ctx, prFresh)

			By("verifying the update is rejected")
			Expect(err).To(HaveOccurred())
			errMsg := err.Error()
			Expect(errMsg).To(SatisfyAny(
				ContainSubstring("immutable"),
				ContainSubstring("packageName"),
				ContainSubstring("object has been modified"),
			))
		})

		It("should reject UPDATE when workspaceName is changed", func() {
			By("creating a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "immut-ws", "v1", withInit("test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			DeferCleanup(func() {
				k8sClient.Delete(env.Ctx, pr) //nolint:errcheck
			})

			By("attempting to change workspaceName")
			prFresh := &porchv1alpha2.PackageRevision{}
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), prFresh)).To(Succeed())
			prFresh.Spec.WorkspaceName = "v2"
			err := k8sClient.Update(env.Ctx, prFresh)

			By("verifying the update is rejected")
			Expect(err).To(HaveOccurred())
			errMsg := err.Error()
			Expect(errMsg).To(SatisfyAny(
				ContainSubstring("immutable"),
				ContainSubstring("workspaceName"),
				ContainSubstring("object has been modified"),
			))
		})

		It("should reject UPDATE when source is changed", func() {
			By("creating a package with Init source")
			pr := newPackageRevision(env.Namespace, env.RepoName, "immut-src", "v1", withInit("original"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			DeferCleanup(func() {
				k8sClient.Delete(env.Ctx, pr) //nolint:errcheck
			})

			By("fetching fresh copy and changing source")
			prFresh := &porchv1alpha2.PackageRevision{}
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), prFresh)).To(Succeed())

			prFresh.Spec.Source = &porchv1alpha2.PackageSource{
				Init: &porchv1alpha2.PackageInitSpec{Description: "changed"},
			}
			err := k8sClient.Update(env.Ctx, prFresh)

			By("verifying the update is rejected")
			Expect(err).To(HaveOccurred())
			errMsg := err.Error()
			Expect(errMsg).To(SatisfyAny(
				ContainSubstring("immutable"),
				ContainSubstring("source"),
				ContainSubstring("object has been modified"),
			))
		})
	})

	// --- Workspace uniqueness tests ---

	Describe("Workspace uniqueness validation", func() {
		It("should reject CREATE when workspace already exists in same repo+package", func() {
			By("creating the first workspace revision")
			pr1 := newPackageRevision(env.Namespace, env.RepoName, "dup-ws-pkg", "v1",
				withInit("first"))
			Expect(k8sClient.Create(env.Ctx, pr1)).To(Succeed())
			waitForReady(env.Ctx, pr1)

			By("verifying the first package is indexed")
			Eventually(func(g Gomega) {
				prList := &porchv1alpha2.PackageRevisionList{}
				g.Expect(k8sClient.List(env.Ctx, prList, client.InNamespace(env.Namespace))).To(Succeed())
				found := false
				for _, p := range prList.Items {
					if p.Name == pr1.Name {
						found = true
						break
					}
				}
				g.Expect(found).To(BeTrue())
			}).WithTimeout(defaultTimeout).Should(Succeed())

			By("attempting to create a second revision with the same workspace")
			pr2 := newPackageRevision(env.Namespace, env.RepoName, "dup-ws-pkg", "v1",
				withInit("second"))
			err := k8sClient.Create(env.Ctx, pr2)

			By("verifying creation is rejected")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Or(Or(ContainSubstring("workspace"), ContainSubstring("unique")), ContainSubstring("already")))

			// Cleanup
			k8sClient.Delete(env.Ctx, pr1)
		})

		It("should allow CREATE when workspace is in different package", func() {
			By("creating a workspace in package A")
			pr1 := newPackageRevision(env.Namespace, env.RepoName, "ws-pkg-a", "same-ws",
				withInit("package a"))
			Expect(k8sClient.Create(env.Ctx, pr1)).To(Succeed())
			waitForReady(env.Ctx, pr1)

			By("creating the same workspace in package B")
			pr2 := newPackageRevision(env.Namespace, env.RepoName, "ws-pkg-b", "same-ws",
				withInit("package b"))
			Expect(k8sClient.Create(env.Ctx, pr2)).To(Succeed())
			waitForReady(env.Ctx, pr2)

			By("verifying both packages exist")
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr1), pr1)).To(Succeed())
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr2), pr2)).To(Succeed())

			// Cleanup
			k8sClient.Delete(env.Ctx, pr1)
			k8sClient.Delete(env.Ctx, pr2)
		})
	})

	// --- Upstream reference protection tests ---

	Describe("Upstream reference protection", func() {
		It("should reject DELETE when other packages reference this one", func() {
			By("creating source package")
			source := newPackageRevision(env.Namespace, env.RepoName, "pkg", "v1", withInit("source package"))
			Expect(k8sClient.Create(env.Ctx, source)).To(Succeed())
			waitForReady(env.Ctx, source)
			publishPackage(env.Ctx, source)
			DeferCleanup(func() {
				k8sClient.Delete(env.Ctx, source) //nolint:errcheck
			})

			By("creating a copy to a new workspace")
			dependent := newPackageRevision(env.Namespace, env.RepoName, "pkg", "v2", withCopyFrom(source.Name))
			Expect(k8sClient.Create(env.Ctx, dependent)).To(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(env.Ctx, dependent) //nolint:errcheck
			})

			By("waiting for copy to be visible with CopyFrom reference")
			Eventually(func(g Gomega) {
				depFresh := &porchv1alpha2.PackageRevision{}
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(dependent), depFresh)).To(Succeed())
				g.Expect(depFresh.Spec.Source).NotTo(BeNil())
				g.Expect(depFresh.Spec.Source.CopyFrom).NotTo(BeNil())
				g.Expect(depFresh.Spec.Source.CopyFrom.Name).To(Equal(source.Name))
			}).WithTimeout(defaultTimeout).Should(Succeed())

			By("attempting to delete the upstream source package")
			err := k8sClient.Delete(env.Ctx, source)

			By("verifying the error indicates upstream references")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Or(ContainSubstring("upstream"), ContainSubstring("referenced")))
		})
	})

	// --- Required fields validation tests ---

	Describe("Required fields validation", func() {
		It("should reject CREATE when repositoryName is missing", func() {
			By("creating a package without repositoryName")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg", "v1",
				withInit("test"))
			pr.Spec.RepositoryName = ""

			err := k8sClient.Create(env.Ctx, pr)

			By("verifying creation is rejected")
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			// CEL validation catches empty repository field
			Expect(err.Error()).To(ContainSubstring("repository"))
		})

		It("should reject CREATE when packageName is missing", func() {
			By("creating a package without packageName")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg", "v1",
				withInit("test"))
			pr.Spec.PackageName = ""

			err := k8sClient.Create(env.Ctx, pr)

			By("verifying creation is rejected")
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("packageName"))
		})

		It("should reject CREATE when workspaceName is missing", func() {
			By("creating a package without workspaceName")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg", "v1",
				withInit("test"))
			pr.Spec.WorkspaceName = ""

			err := k8sClient.Create(env.Ctx, pr)

			By("verifying creation is rejected")
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("workspaceName"))
		})

		It("should reject CREATE when lifecycle is missing", func() {
			By("creating a package without lifecycle")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg", "v1",
				withInit("test"))
			pr.Spec.Lifecycle = ""

			err := k8sClient.Create(env.Ctx, pr)

			By("verifying creation is rejected")
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("lifecycle"))
		})
	})

	// --- Render race prevention tests ---

	Describe("Render race prevention", func() {
		It("should reject Propose when render has not been observed yet", func() {
			By("creating a draft package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "render-not-observed", "v1", withInit("test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

			By("pushing content with a render pipeline and setting render request annotation manually")
			updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
				"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: render-not-observed\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.5\n    configMap:\n      namespace: obs-ns\n",
			})

			By("manually setting render request annotation to simulate stale annotation")
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
			patch := client.MergeFrom(pr.DeepCopy())
			if pr.Annotations == nil {
				pr.Annotations = make(map[string]string)
			}
			pr.Annotations[porchv1alpha2.AnnotationRenderRequest] = "stale-version"
			Expect(k8sClient.Patch(env.Ctx, pr, patch)).To(Succeed())

			By("immediately attempting to transition to Proposed while annotation is stale")
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
			patch = client.MergeFrom(pr.DeepCopy())
			pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleProposed
			err := k8sClient.Patch(env.Ctx, pr, patch)

			By("verifying webhook blocks with render race prevention error")
			if err != nil {
				Expect(err.Error()).To(ContainSubstring("render race prevention"))
			}

			// Cleanup
			k8sClient.Delete(env.Ctx, pr) //nolint:errcheck
		})
	})

	// --- Valid operations tests ---

	Describe("Valid operations", func() {
		It("should allow valid lifecycle transitions", func() {
			By("creating a draft package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "valid-lifecycle", "v1",
				withInit("test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDraft))

			By("transitioning Draft → Proposed")
			patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleProposed)
			waitForReady(env.Ctx, pr)
			Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleProposed))

			By("transitioning Proposed → Published")
			patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
			waitForPublished(env.Ctx, pr)
			Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))

			By("transitioning Published → DeletionProposed")
			patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
			waitForReady(env.Ctx, pr)
			Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDeletionProposed))

			// Cleanup
			deletePackage(env.Ctx, pr)
		})

		It("should allow valid DELETE operations", func() {
			By("creating a draft package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "valid-delete-draft", "v1",
				withInit("test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)

			By("deleting the draft package")
			Expect(k8sClient.Delete(env.Ctx, pr)).To(Succeed())

			By("verifying the package is gone")
			Eventually(func() bool {
				err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)
				return apierrors.IsNotFound(err)
			}).WithTimeout(defaultTimeout).Should(BeTrue())
		})
	})
})
