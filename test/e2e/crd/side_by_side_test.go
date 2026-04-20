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
	porchv1alpha1 "github.com/nephio-project/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("SideBySide", Ordered, Label("migration"), func() {
	// Tests v1alpha1 and v1alpha2 repos coexisting in the same namespace.
	// Verifies API isolation, shared PRR, lifecycle independence, and
	// deletion isolation.

	const (
		v1Repo = "sbs-v1"
		v2Repo = "sbs-v2"
	)

	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()

		By("creating gitea repos")
		deleteGiteaRepo(v1Repo)
		forkGiteaRepo(testBlueprintsRepo, v1Repo)
		createGiteaRepo(v2Repo)

		By("registering v1alpha1 repo (no annotation)")
		registerV1Alpha1Repo(env.Ctx, env.Namespace, v1Repo)

		By("registering v1alpha2 repo (with annotation)")
		registerV1Alpha2Repo(env.Ctx, env.Namespace, v2Repo)
	})

	AfterAll(func() {
		// Clean up v1alpha2 packages
		var v2List porchv1alpha2.PackageRevisionList
		if err := k8sClient.List(env.Ctx, &v2List, client.InNamespace(env.Namespace)); err == nil {
			for i := range v2List.Items {
				if v2List.Items[i].Spec.RepositoryName == v2Repo {
					v2List.Items[i].Finalizers = nil
					k8sClient.Update(env.Ctx, &v2List.Items[i]) //nolint:errcheck
					k8sClient.Delete(env.Ctx, &v2List.Items[i])  //nolint:errcheck
				}
			}
		}
		cleanupRepo(env.Ctx, env.Namespace, v1Repo)
		cleanupRepo(env.Ctx, env.Namespace, v2Repo)
		deleteGiteaRepo(v1Repo)
		deleteGiteaRepo(v2Repo)
	})

	Context("API Isolation", func() {
		It("should create packages on each API version", func() {
			By("creating a v1alpha2 package")
			v2pr := newPackageRevision(env.Namespace, v2Repo, "sbs-pkg", "v1", withInit("v1alpha2 side-by-side"))
			Expect(k8sClient.Create(env.Ctx, v2pr)).To(Succeed())
			waitForReady(env.Ctx, v2pr)
		})

		It("should only show v1alpha1 repo packages in v1alpha1 API", func() {
			var v1List porchv1alpha1.PackageRevisionList
			Expect(k8sClient.List(env.Ctx, &v1List, client.InNamespace(env.Namespace))).To(Succeed())

			for _, pr := range v1List.Items {
				Expect(pr.Spec.RepositoryName).NotTo(Equal(v2Repo),
					"v1alpha1 API should not expose v1alpha2 repo packages")
			}
		})

		It("should only show v1alpha2 repo packages in v1alpha2 API", func() {
			var v2List porchv1alpha2.PackageRevisionList
			Expect(k8sClient.List(env.Ctx, &v2List, client.InNamespace(env.Namespace))).To(Succeed())

			for _, pr := range v2List.Items {
				Expect(pr.Spec.RepositoryName).NotTo(Equal(v1Repo),
					"v1alpha2 API should not expose v1alpha1 repo packages")
			}
		})

		It("should filter v1alpha2 packages by repository field selector", func() {
			var v2List porchv1alpha2.PackageRevisionList
			Expect(k8sClient.List(env.Ctx, &v2List, client.InNamespace(env.Namespace),
				client.MatchingFields{string(porchv1alpha2.PkgRevSelectorRepository): v2Repo},
			)).To(Succeed())

			Expect(v2List.Items).NotTo(BeEmpty())
			for _, pr := range v2List.Items {
				Expect(pr.Spec.RepositoryName).To(Equal(v2Repo))
			}
		})
	})

	Context("Shared PRR API", func() {
		It("should pull v1alpha2 package content via v1alpha1 PRR API", func() {
			prName := crdName(v2Repo, "sbs-pkg", "v1")
			resources := getPRRResources(env.Ctx, env.Namespace, prName)
			Expect(resources).To(HaveKey("Kptfile"))
			Expect(resources["Kptfile"]).To(ContainSubstring("v1alpha2 side-by-side"))
		})

		It("should push content to v1alpha2 package and trigger async render", func() {
			prName := crdName(v2Repo, "sbs-pkg", "v1")

			By("pushing content")
			updatePRRResources(env.Ctx, env.Namespace, prName, map[string]string{
				"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: sbs-cm\ndata:\n  source: v1alpha2\n",
			})

			By("verifying render-request annotation was set")
			pr := &porchv1alpha2.PackageRevision{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: prName}, pr)).To(Succeed())
				g.Expect(pr.Annotations).To(HaveKey(porchv1alpha2.AnnotationRenderRequest))
			}).WithTimeout(defaultTimeout).Should(Succeed())

			By("waiting for async render to complete")
			waitForRendered(env.Ctx, pr)

			By("verifying pushed content round-tripped")
			resources := getPRRResources(env.Ctx, env.Namespace, prName)
			Expect(resources).To(HaveKey("cm.yaml"))
			Expect(resources["cm.yaml"]).To(ContainSubstring("source: v1alpha2"))
		})
	})

	Context("Lifecycle Isolation", func() {
		It("should publish v1alpha2 package without affecting v1alpha1 repo", func() {
			prName := crdName(v2Repo, "sbs-pkg", "v1")
			pr := &porchv1alpha2.PackageRevision{}
			Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: prName}, pr)).To(Succeed())

			publishPackage(env.Ctx, pr)

			By("verifying v1alpha1 repo is still healthy")
			waitForRepoReady(env.Ctx, env.Namespace, v1Repo)
		})
	})

	Context("Deletion Isolation", func() {
		It("should delete v1alpha2 package without affecting v1alpha1 repo", func() {
			By("creating a throwaway v1alpha2 package")
			pr := newPackageRevision(env.Namespace, v2Repo, "sbs-del", "v1", withInit("deletion isolation"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)

			By("deleting the v1alpha2 package")
			Expect(k8sClient.Delete(env.Ctx, pr)).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr))
			}).WithTimeout(defaultTimeout).Should(BeTrue())

			By("verifying v1alpha1 repo is unaffected")
			waitForRepoReady(env.Ctx, env.Namespace, v1Repo)
		})

		It("should delete v1alpha2 repo without affecting v1alpha1 repo", func() {
			By("creating a throwaway v1alpha2 repo with a package")
			throwawayRepo := "sbs-throwaway"
			createGiteaRepo(throwawayRepo)
			registerV1Alpha2Repo(env.Ctx, env.Namespace, throwawayRepo)

			pr := newPackageRevision(env.Namespace, throwawayRepo, "throw-pkg", "v1", withInit("throwaway"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)

			By("deleting the throwaway repo")
			cleanupRepo(env.Ctx, env.Namespace, throwawayRepo)

			By("waiting for v1alpha2 CRD to be garbage collected")
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr))
			}).WithTimeout(defaultTimeout).Should(BeTrue())

			By("verifying v1alpha1 repo is unaffected")
			waitForRepoReady(env.Ctx, env.Namespace, v1Repo)

			deleteGiteaRepo(throwawayRepo)
		})
	})

	Context("Cross-Version Rejection", func() {
		It("should not find v1alpha2 package via v1alpha1 PackageRevision Get", func() {
			// The v1alpha2 package name exists as a CRD but should not be
			// served by the v1alpha1 aggregated API.
			v1pr := &porchv1alpha1.PackageRevision{}
			err := k8sClient.Get(env.Ctx, client.ObjectKey{
				Namespace: env.Namespace,
				Name:      crdName(v2Repo, "sbs-pkg", "v1"),
			}, v1pr)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(),
				"v1alpha1 API should return NotFound for v1alpha2 package")
		})

		It("should not find v1alpha1 package via v1alpha2 PackageRevision Get", func() {
			// v1alpha1 packages don't exist as CRDs, so a v1alpha2 Get
			// should return NotFound.
			v2pr := &porchv1alpha2.PackageRevision{}
			err := k8sClient.Get(env.Ctx, client.ObjectKey{
				Namespace: env.Namespace,
				Name:      crdName(v1Repo, "nonexistent", "v1"),
			}, v2pr)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(),
				"v1alpha2 API should return NotFound for v1alpha1 package")
		})

		It("should reject v1alpha1 Create on a v1alpha2 repo", func() {
			// A user who forgets --api-version=v1alpha2 and tries to create
			// a package on a migrated repo via the v1alpha1 aggregated API.
			v1pr := &porchv1alpha1.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Name:      crdName(v2Repo, "rejected-pkg", "v1"),
					Namespace: env.Namespace,
				},
				Spec: porchv1alpha1.PackageRevisionSpec{
					PackageName:    "rejected-pkg",
					RepositoryName: v2Repo,
					WorkspaceName:  "v1",
					Lifecycle:      porchv1alpha1.PackageRevisionLifecycleDraft,
					Tasks: []porchv1alpha1.Task{{
						Type: porchv1alpha1.TaskTypeInit,
						Init: &porchv1alpha1.PackageInitTaskSpec{
							Description: "should be rejected",
						},
					}},
				},
			}
			err := k8sClient.Create(env.Ctx, v1pr)
			Expect(err).To(HaveOccurred(), "v1alpha1 Create on v1alpha2 repo should fail")
		})
	})

	Context("Cross-Version Clone", func() {
		// Issue 35: upstreamRef fails cross-version because the PR controller
		// looks up a v1alpha2 CRD that doesn't exist for v1alpha1 repos.
		// The git URL workaround bypasses CRD lookup entirely.

		It("should clone from a v1alpha1 repo into a v1alpha2 repo via git URL", func() {
			By("creating and publishing a package in the v1alpha1 repo")
			v1Pr := createAndPublishV1Alpha1Package(env.Ctx, env.Namespace, v1Repo, "cross-src", "v1")

			By("cloning into the v1alpha2 repo via git URL")
			pr := newPackageRevision(env.Namespace, v2Repo, "cross-clone", "v1", func(pr *porchv1alpha2.PackageRevision) {
				pr.Spec.Source = &porchv1alpha2.PackageSource{
					CloneFrom: &porchv1alpha2.UpstreamPackage{
						Type: porchv1alpha2.RepositoryTypeGit,
						Git: &porchv1alpha2.GitPackage{
							Repo:      giteaRepoURL(v1Repo),
							Ref:       v1Pr.gitRef,
							Directory: "/" + v1Pr.packageName,
							SecretRef: porchv1alpha2.SecretRef{
								Name: v2Repo + "-auth",
							},
						},
					},
				}
			})
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)

			By("verifying clone succeeded")
			Expect(pr.Status.CreationSource).To(Equal("clone"))
			resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
			Expect(resources).To(HaveKey("Kptfile"))
		})
	})
})
