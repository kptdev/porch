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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Migration Rollback", Ordered, func() {
	// Validates that a package created via v1alpha2 survives rollback
	// to v1alpha1. Git is the source of truth — the v1alpha1 API should
	// rediscover the package from git after the migration annotation is
	// removed. See MIGRATION_BACKWARDS_COMPAT.md.
	//
	// Known losses on rollback (by design):
	// - Custom labels/annotations on the v1alpha2 CRD (not in git)
	// - status.creationSource (v1alpha2-only field)
	// - Details of the creation task (e.g. the upstream of a clone): v1alpha2
	//   records only the task type in the git commit annotation
	//
	// Preserved on rollback:
	// - Creation task type (from git commit annotations)
	// - Package content (all resource files)
	// - Kptfile metadata (labels, annotations, readinessGates, pipeline)
	// - Revision number (from git tags)
	// - Lifecycle state (from git branch/tag location)
	// - Upstream/upstreamLock (from Kptfile)

	var (
		repoName = "rollback-test"
	)

	BeforeAll(func() {
		createGiteaRepo(repoName)
	})

	AfterAll(func() {
		cleanupRepo(sharedCtx, sharedNamespace, repoName)
		deleteGiteaRepo(repoName)
	})

	It("should preserve v1alpha2-created packages after rollback", func() {
		By("registering repo as v1alpha1 (no migration annotation)")
		secretName := repoName + "-auth"
		Expect(k8sClient.Create(sharedCtx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: sharedNamespace},
			Immutable:  new(true),
			Data:       map[string][]byte{"username": []byte(giteaUser), "password": []byte(giteaPassword)},
			Type:       corev1.SecretTypeBasicAuth,
		})).To(Succeed())

		repo := &configapi.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      repoName,
				Namespace: sharedNamespace,
			},
			Spec: configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      giteaRepoURL(repoName),
					Branch:    "main",
					SecretRef: configapi.SecretRef{Name: secretName},
				},
			},
		}
		Expect(k8sClient.Create(sharedCtx, repo)).To(Succeed())
		waitForRepoReady(sharedCtx, sharedNamespace, repoName)

		By("migrating repo to v1alpha2")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(sharedCtx, client.ObjectKey{Namespace: sharedNamespace, Name: repoName}, repo)).To(Succeed())
			patch := client.MergeFrom(repo.DeepCopy())
			if repo.Annotations == nil {
				repo.Annotations = map[string]string{}
			}
			repo.Annotations[configapi.AnnotationKeyV1Alpha2Migration] = configapi.AnnotationValueMigrationEnabled
			g.Expect(k8sClient.Patch(sharedCtx, repo, patch)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		triggerRepoSync(sharedCtx, sharedNamespace, repoName)

		By("creating and publishing a package via v1alpha2")
		pr := newPackageRevision(sharedNamespace, repoName, "rollback-pkg", "v1", withInit("rollback test"))
		Expect(k8sClient.Create(sharedCtx, pr)).To(Succeed())
		waitForReady(sharedCtx, pr)

		updatePRRResources(sharedCtx, sharedNamespace, pr.Name, map[string]string{
			"data.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: rollback-data\ndata:\n  source: v1alpha2\n",
		})
		waitForRendered(sharedCtx, pr)
		publishPackage(sharedCtx, pr)

		By("rolling back: removing the v1alpha2 migration annotation")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(sharedCtx, client.ObjectKey{Namespace: sharedNamespace, Name: repoName}, repo)).To(Succeed())
			patch := client.MergeFrom(repo.DeepCopy())
			delete(repo.Annotations, configapi.AnnotationKeyV1Alpha2Migration)
			g.Expect(k8sClient.Patch(sharedCtx, repo, patch)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("cleaning up v1alpha2 CRDs (rollback procedure)")
		var prList porchv1alpha2.PackageRevisionList
		Expect(k8sClient.List(sharedCtx, &prList, client.InNamespace(sharedNamespace),
			client.MatchingFields{string(porchv1alpha2.PkgRevSelectorRepository): repoName},
		)).To(Succeed())
		for i := range prList.Items {
			prList.Items[i].Finalizers = nil
			k8sClient.Update(sharedCtx, &prList.Items[i]) //nolint:errcheck
			k8sClient.Delete(sharedCtx, &prList.Items[i]) //nolint:errcheck
		}

		By("triggering repo sync so v1alpha1 discovers packages from git")
		triggerRepoSync(sharedCtx, sharedNamespace, repoName)

		By("verifying the v1alpha2-created package is discoverable via v1alpha1")
		var v1PkgName string
		Eventually(func(g Gomega) {
			var v1List porchapi.PackageRevisionList
			g.Expect(k8sClient.List(sharedCtx, &v1List, client.InNamespace(sharedNamespace))).To(Succeed())
			for _, item := range v1List.Items {
				if item.Spec.RepositoryName == repoName &&
					item.Spec.PackageName == "rollback-pkg" &&
					item.Spec.Revision > 0 {
					v1PkgName = item.Name
					g.Expect(item.Spec.Lifecycle).To(Equal(porchapi.PackageRevisionLifecyclePublished))
					g.Expect(item.Spec.Tasks).To(HaveLen(1))
					g.Expect(item.Spec.Tasks[0].Type).To(Equal(porchapi.TaskTypeInit))
					return
				}
			}
			g.Expect(false).To(BeTrue(), "v1alpha2-created package not discoverable via v1alpha1 after rollback")
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("verifying PRR content survived rollback")
		resources := getPRRResources(sharedCtx, sharedNamespace, v1PkgName)
		Expect(resources).To(HaveKey("data.yaml"))
		Expect(resources["data.yaml"]).To(ContainSubstring("rollback-data"))
	})
})
