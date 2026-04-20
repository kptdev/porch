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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	porchv1alpha1 "github.com/nephio-project/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	configapi "github.com/nephio-project/porch/api/porchconfig/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Migration", Ordered, Label("migration"), func() {
	// Tests the per-repo annotation migration flow:
	//   1. Register a fork of test-blueprints as v1alpha1
	//   2. Flip annotation → v1alpha2 CRDs appear, v1alpha1 stops serving
	//   3. Verify PRR pull works on migrated packages
	//   4. Rollback → remove annotation, v1alpha1 resumes
	//   5. Clean up orphaned v1alpha2 CRDs
	//
	// Uses a fork of test-blueprints so it has real tagged packages
	// (basens/v1, basens/v2, etc.) without colliding with BeforeSuite.

	const (
		forkName = "mig-blueprints"
		pkgCRD   = "mig-blueprints.basens.v1"
	)

	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()

		By("forking test-blueprints into " + forkName)
		deleteGiteaRepo(forkName)
		forkGiteaRepo(testBlueprintsRepo, forkName)
	})

	AfterAll(func() {
		cleanupMigrationResources(env.Ctx, env.Namespace, forkName)
		deleteGiteaRepo(forkName)
	})

	It("should establish v1alpha1 baseline", func() {
		By("registering fork WITHOUT v1alpha2 annotation")
		registerV1Alpha1Repo(env.Ctx, env.Namespace, forkName)

		By("verifying repo has no migration annotation")
		repo := &configapi.Repository{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: forkName}, repo)).To(Succeed())
		Expect(repo.Annotations).NotTo(HaveKey("porch.kpt.dev/v1alpha2-migration"))

		By("waiting for v1alpha1 package discovery")
		// Don't call triggerRepoSync here — the initial registration already
		// triggers a scheduled sync. Using RunOnceAt here would consume a
		// generation bump, and the migration step's RunOnceAt could race with it.
		Eventually(func(g Gomega) {
			var list porchv1alpha1.PackageRevisionList
			g.Expect(k8sClient.List(env.Ctx, &list, client.InNamespace(env.Namespace))).To(Succeed())
			found := false
			for _, pr := range list.Items {
				if pr.Spec.RepositoryName == forkName {
					found = true
					break
				}
			}
			g.Expect(found).To(BeTrue(), "v1alpha1 API should serve packages for unannotated repo")
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("verifying no v1alpha2 CRDs exist for this repo")
		var v2List porchv1alpha2.PackageRevisionList
		Expect(k8sClient.List(env.Ctx, &v2List, client.InNamespace(env.Namespace))).To(Succeed())
		for _, pr := range v2List.Items {
			Expect(pr.Spec.RepositoryName).NotTo(Equal(forkName),
				"no v1alpha2 CRDs should exist for unannotated repo")
		}
	})

	It("should migrate to v1alpha2 when annotation is added", func() {
		By("adding the v1alpha2 migration annotation and triggering sync atomically")
		Eventually(func(g Gomega) {
			repo := &configapi.Repository{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: forkName}, repo)).To(Succeed())
			if repo.Annotations == nil {
				repo.Annotations = map[string]string{}
			}
			repo.Annotations["porch.kpt.dev/v1alpha2-migration"] = "true"
			now := metav1.Now()
			if repo.Spec.Sync == nil {
				repo.Spec.Sync = &configapi.RepositorySync{}
			}
			repo.Spec.Sync.RunOnceAt = &now
			g.Expect(k8sClient.Update(env.Ctx, repo)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("waiting for v1alpha2 CRDs to appear")
		Eventually(func(g Gomega) {
			var v2List porchv1alpha2.PackageRevisionList
			g.Expect(k8sClient.List(env.Ctx, &v2List, client.InNamespace(env.Namespace))).To(Succeed())
			found := false
			for _, pr := range v2List.Items {
				if pr.Spec.RepositoryName == forkName {
					found = true
					break
				}
			}
			g.Expect(found).To(BeTrue(), "v1alpha2 CRDs should appear after annotation")
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("verifying v1alpha2 package count matches repo package count")
		Eventually(func(g Gomega) {
			repo := &configapi.Repository{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: forkName}, repo)).To(Succeed())
			var v2List porchv1alpha2.PackageRevisionList
			g.Expect(k8sClient.List(env.Ctx, &v2List, client.InNamespace(env.Namespace))).To(Succeed())
			v2Count := 0
			for _, pr := range v2List.Items {
				if pr.Spec.RepositoryName == forkName {
					v2Count++
				}
			}
			g.Expect(v2Count).To(Equal(repo.Status.PackageCount),
				"v1alpha2 CRD count should match repo status package count")
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("verifying v1alpha1 API no longer serves this repo")
		Eventually(func(g Gomega) {
			var v1List porchv1alpha1.PackageRevisionList
			g.Expect(k8sClient.List(env.Ctx, &v1List, client.InNamespace(env.Namespace))).To(Succeed())
			for _, pr := range v1List.Items {
				g.Expect(pr.Spec.RepositoryName).NotTo(Equal(forkName),
					"v1alpha1 API should stop serving annotated repo")
			}
		}).WithTimeout(defaultTimeout).Should(Succeed())
	})

	It("should have correct metadata on migrated v1alpha2 CRDs", func() {
		var v2List porchv1alpha2.PackageRevisionList
		Expect(k8sClient.List(env.Ctx, &v2List, client.InNamespace(env.Namespace))).To(Succeed())

		var migrated []porchv1alpha2.PackageRevision
		for _, pr := range v2List.Items {
			if pr.Spec.RepositoryName == forkName {
				migrated = append(migrated, pr)
			}
		}
		Expect(migrated).NotTo(BeEmpty())

		for _, pr := range migrated {
			Expect(pr.OwnerReferences).NotTo(BeEmpty(), "v1alpha2 CRD %s should have ownerReference", pr.Name)
			Expect(pr.OwnerReferences[0].Kind).To(Equal("Repository"))
		}
	})

	It("should allow PRR pull on migrated v1alpha2 packages", func() {
		resources := getPRRResources(env.Ctx, env.Namespace, pkgCRD)
		Expect(resources).To(HaveKey("Kptfile"))
	})

	It("should rollback to v1alpha1 when annotation is removed", func() {
		By("removing the v1alpha2 annotation and triggering sync atomically")
		Eventually(func(g Gomega) {
			repo := &configapi.Repository{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: forkName}, repo)).To(Succeed())
			delete(repo.Annotations, "porch.kpt.dev/v1alpha2-migration")
			now := metav1.Now()
			if repo.Spec.Sync == nil {
				repo.Spec.Sync = &configapi.RepositorySync{}
			}
			repo.Spec.Sync.RunOnceAt = &now
			g.Expect(k8sClient.Update(env.Ctx, repo)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("verifying v1alpha1 API serves packages again")
		Eventually(func(g Gomega) {
			var v1List porchv1alpha1.PackageRevisionList
			g.Expect(k8sClient.List(env.Ctx, &v1List, client.InNamespace(env.Namespace))).To(Succeed())
			found := false
			for _, pr := range v1List.Items {
				if pr.Spec.RepositoryName == forkName {
					found = true
					break
				}
			}
			g.Expect(found).To(BeTrue(), "v1alpha1 API should serve packages after rollback")
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("verifying orphaned v1alpha2 CRDs still exist")
		var v2List porchv1alpha2.PackageRevisionList
		Expect(k8sClient.List(env.Ctx, &v2List, client.InNamespace(env.Namespace))).To(Succeed())
		orphanCount := 0
		for _, pr := range v2List.Items {
			if pr.Spec.RepositoryName == forkName {
				orphanCount++
			}
		}
		Expect(orphanCount).To(BeNumerically(">", 0), "orphaned v1alpha2 CRDs should remain after rollback")
	})

	It("should allow cleanup of orphaned v1alpha2 CRDs without affecting v1alpha1", func() {
		By("deleting orphaned v1alpha2 CRDs by removing the repo (GC cascade)")
		// The controller blocks deletion of Published CRDs while the owner repo
		// exists. Deleting the repo triggers K8s GC cascade via ownerReference,
		// and the controller allows finalizer removal when ownerRepoExists=false.
		repo := &configapi.Repository{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: forkName}, repo)).To(Succeed())
			repo.Finalizers = nil
			g.Expect(k8sClient.Update(env.Ctx, repo)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		Expect(k8sClient.Delete(env.Ctx, repo)).To(Succeed())

		By("verifying orphans are gone")
		Eventually(func(g Gomega) {
			var remaining porchv1alpha2.PackageRevisionList
			g.Expect(k8sClient.List(env.Ctx, &remaining, client.InNamespace(env.Namespace))).To(Succeed())
			for _, pr := range remaining.Items {
				g.Expect(pr.Spec.RepositoryName).NotTo(Equal(forkName))
			}
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("verifying other repos are unaffected")
		waitForRepoReady(env.Ctx, env.Namespace, testBlueprintsRepo)
	})
})

// registerV1Alpha1Repo registers a repo WITHOUT the v1alpha2 migration annotation.
func registerV1Alpha1Repo(ctx context.Context, namespace, repoName string) {
	secretName := repoName + "-auth"

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Immutable: ptr.To(true),
		Data: map[string][]byte{
			"username": []byte(giteaUser),
			"password": []byte(giteaPassword),
		},
		Type: corev1.SecretTypeBasicAuth,
	}
	err := k8sClient.Create(ctx, secret)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: namespace,
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
	Expect(k8sClient.Create(ctx, repo)).To(Succeed())
	waitForRepoReady(ctx, namespace, repoName)
}

// cleanupMigrationResources removes all resources created by the migration test.
func cleanupMigrationResources(ctx context.Context, namespace, repoName string) {
	var v2List porchv1alpha2.PackageRevisionList
	if err := k8sClient.List(ctx, &v2List, client.InNamespace(namespace)); err == nil {
		for i := range v2List.Items {
			if v2List.Items[i].Spec.RepositoryName == repoName {
				v2List.Items[i].Finalizers = nil
				k8sClient.Update(ctx, &v2List.Items[i]) //nolint:errcheck
				k8sClient.Delete(ctx, &v2List.Items[i])  //nolint:errcheck
			}
		}
	}
	cleanupRepo(ctx, namespace, repoName)
}
