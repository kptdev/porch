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
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
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
		createGiteaRepo(v1Repo)
		createGiteaRepo(v2Repo)
		DeferCleanup(deleteGiteaRepo, v1Repo)
		DeferCleanup(deleteGiteaRepo, v2Repo)

		By("creating auth secrets")
		for _, repoName := range []string{v1Repo, v2Repo} {
			secretName := repoName + "-auth"
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: env.Namespace},
				Immutable:  new(true),
				Data:       map[string][]byte{"username": []byte(giteaUser), "password": []byte(giteaPassword)},
				Type:       corev1.SecretTypeBasicAuth,
			}
			Expect(k8sClient.Create(env.Ctx, secret)).To(Succeed())
		}

		By("registering v1alpha1 repo (no annotation)")
		v1repo := &configapi.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      v1Repo,
				Namespace: env.Namespace,
			},
			Spec: configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      giteaRepoURL(v1Repo),
					Branch:    "main",
					SecretRef: configapi.SecretRef{Name: v1Repo + "-auth"},
				},
			},
		}
		Expect(k8sClient.Create(env.Ctx, v1repo)).To(Succeed())
		DeferCleanup(cleanupRepo, env.Ctx, env.Namespace, v1Repo)
		waitForRepoReady(env.Ctx, env.Namespace, v1Repo)

		By("registering v1alpha2 repo (with annotation)")
		v2repo := &configapi.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      v2Repo,
				Namespace: env.Namespace,
				Annotations: map[string]string{
					configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled,
				},
			},
			Spec: configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      giteaRepoURL(v2Repo),
					Branch:    "main",
					SecretRef: configapi.SecretRef{Name: v2Repo + "-auth"},
				},
			},
		}
		Expect(k8sClient.Create(env.Ctx, v2repo)).To(Succeed())
		DeferCleanup(cleanupRepo, env.Ctx, env.Namespace, v2Repo)
		waitForRepoReady(env.Ctx, env.Namespace, v2Repo)
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
			Eventually(func(g Gomega) {
				resources := getPRRResources(env.Ctx, env.Namespace, prName)
				g.Expect(resources).To(HaveKey("cm.yaml"))
				g.Expect(resources["cm.yaml"]).To(ContainSubstring("source: v1alpha2"))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		})
	})

	Context("Lifecycle Isolation", func() {
		It("should publish v1alpha2 package without affecting v1alpha1 repo", func() {
			prName := crdName(v2Repo, "sbs-pkg", "v1")

			By("transitioning v1alpha2 package to Proposed")
			pr := &porchv1alpha2.PackageRevision{}
			Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: prName}, pr)).To(Succeed())
			patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleProposed)
			waitForReady(env.Ctx, pr)

			By("publishing v1alpha2 package")
			Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: prName}, pr)).To(Succeed())
			patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
			waitForReady(env.Ctx, pr)

			By("verifying v1alpha1 repo packages are unchanged")
			var v1List porchv1alpha1.PackageRevisionList
			Expect(k8sClient.List(env.Ctx, &v1List, client.InNamespace(env.Namespace))).To(Succeed())
			for _, p := range v1List.Items {
				if p.Spec.RepositoryName == v1Repo {
					Expect(p.Spec.Lifecycle).NotTo(Equal(porchv1alpha1.PackageRevisionLifecyclePublished),
						"v1alpha1 repo packages should not be affected by v1alpha2 publication")
				}
			}
		})
	})

	Context("Deletion Isolation", func() {
		It("should not cross-delete between v1alpha1 and v1alpha2", func() {
			By("transitioning v1alpha2 package to DeletionProposed")
			prName := crdName(v2Repo, "sbs-pkg", "v1")
			pr := &porchv1alpha2.PackageRevision{}
			Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: prName}, pr)).To(Succeed())
			patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
			waitForReady(env.Ctx, pr)

			By("deleting the v1alpha2 package")
			Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: prName}, pr)).To(Succeed())
			pr.Finalizers = nil
			Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
			Expect(k8sClient.Delete(env.Ctx, pr)).To(Succeed())

			By("verifying v1alpha1 repo still exists and is accessible")
			repo := &configapi.Repository{}
			Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: v1Repo}, repo)).To(Succeed())
			Expect(repo.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(configapi.RepositoryReady)),
				HaveField("Status", Equal(metav1.ConditionTrue)),
			)))
		})
	})
})
