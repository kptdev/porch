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
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Migration Rollback", Ordered, Label("migration"), func() {
	// Validates emergency rollback from v1alpha2 back to v1alpha1: pre-migration packages
	// are fully recovered, v1alpha2-created packages are orphaned. See "Emergency Rollback
	// to v1alpha1" in the CRD-based PackageRevisions docs (User-Facing Differences).
	const (
		forkName = "rollback-blueprints"
		pkgName  = "basens"
	)

	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()

		By("forking test-blueprints for rollback test")
		deleteGiteaRepo(forkName)
		forkGiteaRepo(testBlueprintsRepo, forkName)
	})

	AfterAll(func() {
		cleanupMigrationResources(env.Ctx, env.Namespace, forkName)
		deleteGiteaRepo(forkName)
	})

	It("should preserve pre-migration v1alpha1 packages after emergency rollback", func() {
		By("registering fork as v1alpha1 (no migration annotation)")
		registerV1Alpha1Repo(env.Ctx, env.Namespace, forkName)

		By("verifying pre-migration packages are discoverable via v1alpha1")
		Eventually(func(g Gomega) {
			var v1List porchapi.PackageRevisionList
			g.Expect(k8sClient.List(env.Ctx, &v1List, client.InNamespace(env.Namespace))).To(Succeed())
			found := false
			for _, item := range v1List.Items {
				if item.Spec.RepositoryName == forkName && item.Spec.PackageName == pkgName {
					g.Expect(item.Spec.Lifecycle).To(Equal(porchapi.PackageRevisionLifecyclePublished))
					found = true
					break
				}
			}
			g.Expect(found).To(BeTrue(), "pre-migration package not discoverable via v1alpha1")
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("migrating repo to v1alpha2 and triggering sync atomically")
		Eventually(func(g Gomega) {
			repo := &configapi.Repository{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: forkName}, repo)).To(Succeed())
			if repo.Annotations == nil {
				repo.Annotations = map[string]string{}
			}
			repo.Annotations[configapi.AnnotationKeyV1Alpha2Migration] = configapi.AnnotationValueMigrationEnabled
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

		By("verifying v1alpha1 API no longer serves the repo (switched to v1alpha2)")
		Eventually(func(g Gomega) {
			var v1List porchapi.PackageRevisionList
			g.Expect(k8sClient.List(env.Ctx, &v1List, client.InNamespace(env.Namespace))).To(Succeed())
			for _, item := range v1List.Items {
				g.Expect(item.Spec.RepositoryName).NotTo(Equal(forkName),
					"v1alpha1 API should not serve migrated repo")
			}
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("rolling back: removing the v1alpha2 migration annotation (emergency rollback)")
		Eventually(func(g Gomega) {
			repo := &configapi.Repository{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: forkName}, repo)).To(Succeed())
			if repo.Annotations != nil {
				delete(repo.Annotations, configapi.AnnotationKeyV1Alpha2Migration)
			}
			now := metav1.Now()
			if repo.Spec.Sync == nil {
				repo.Spec.Sync = &configapi.RepositorySync{}
			}
			repo.Spec.Sync.RunOnceAt = &now
			g.Expect(k8sClient.Update(env.Ctx, repo)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("cleaning up orphaned v1alpha2 CRDs (created during migration)")
		var prList porchv1alpha2.PackageRevisionList
		Expect(k8sClient.List(env.Ctx, &prList, client.InNamespace(env.Namespace),
			client.MatchingFields{string(porchv1alpha2.PkgRevSelectorRepository): forkName},
		)).To(Succeed())
		for i := range prList.Items {
			prList.Items[i].Finalizers = nil
			k8sClient.Update(env.Ctx, &prList.Items[i]) //nolint:errcheck
			k8sClient.Delete(env.Ctx, &prList.Items[i]) //nolint:errcheck
		}

		By("verifying pre-migration package is fully recovered via v1alpha1 API")
		Eventually(func(g Gomega) {
			var v1List porchapi.PackageRevisionList
			g.Expect(k8sClient.List(env.Ctx, &v1List, client.InNamespace(env.Namespace))).To(Succeed())
			found := false
			for _, item := range v1List.Items {
				if item.Spec.RepositoryName == forkName && item.Spec.PackageName == pkgName {
					g.Expect(item.Spec.Lifecycle).To(Equal(porchapi.PackageRevisionLifecyclePublished))
					found = true
					break
				}
			}
			g.Expect(found).To(BeTrue(), "pre-migration package not recovered via v1alpha1 after rollback")
		}).WithTimeout(defaultTimeout).Should(Succeed())
	})
})
