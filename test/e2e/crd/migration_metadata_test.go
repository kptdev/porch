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

	porchv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Migration Metadata Preservation", Ordered, Label("migration"), func() {
	// Tests that labels and annotations set on v1alpha1 PackageRevision
	// resources are preserved on the v1alpha2 CRDs after migration.
	//
	// Flow:
	//   1. Register a repo as v1alpha1 (no migration annotation)
	//   2. Create a package via v1alpha1 API with labels and annotations
	//   3. Publish the package
	//   4. Set additional labels/annotations on the published PR
	//   5. Enable v1alpha2 migration annotation
	//   6. Verify the v1alpha2 CRD carries those labels and annotations

	const (
		repoName  = "mig-meta-repo"
		pkgName   = "labeled-pkg"
		workspace = "v1"
	)

	var env *testEnv
	var v1PrName string

	BeforeAll(func() {
		env = sharedEnv()

		By("creating a fresh gitea repo for migration metadata test")
		deleteGiteaRepo(repoName)
		createGiteaRepo(repoName)

		By("registering repo as v1alpha1 (no migration annotation)")
		registerV1Alpha1Repo(env.Ctx, env.Namespace, repoName)

		By("creating and publishing a package via v1alpha1 API")
		published := createAndPublishV1Alpha1Package(env.Ctx, env.Namespace, repoName, pkgName, workspace)
		v1PrName = published.name

		By("setting custom labels and annotations on the v1alpha1 PackageRevision")
		Eventually(func(g Gomega) {
			pr := &porchv1alpha1.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: v1PrName}, pr)).To(Succeed())

			if pr.Labels == nil {
				pr.Labels = map[string]string{}
			}
			pr.Labels["team"] = "networking"
			pr.Labels["env"] = "production"

			if pr.Annotations == nil {
				pr.Annotations = map[string]string{}
			}
			pr.Annotations["contact"] = "team-net@example.com"
			pr.Annotations["purpose"] = "infra-base"

			g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	AfterAll(func() {
		cleanupMigrationResources(env.Ctx, env.Namespace, repoName)
		deleteGiteaRepo(repoName)
	})

	It("should preserve labels and annotations during migration", func() {
		By("verifying labels are persisted on v1alpha1")
		pr := &porchv1alpha1.PackageRevision{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: v1PrName}, pr)).To(Succeed())
		Expect(pr.Labels).To(HaveKeyWithValue("team", "networking"))
		Expect(pr.Labels).To(HaveKeyWithValue("env", "production"))
		Expect(pr.Annotations).To(HaveKeyWithValue("contact", "team-net@example.com"))
		Expect(pr.Annotations).To(HaveKeyWithValue("purpose", "infra-base"))

		By("enabling v1alpha2 migration and triggering sync")
		enableV1Alpha2Migration(env.Ctx, env.Namespace, repoName)

		By("waiting for v1alpha2 CRD to appear with seeded labels")
		v2Name := crdName(repoName, pkgName, workspace)
		Eventually(func(g Gomega) {
			v2pr := &porchv1alpha2.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: v2Name}, v2pr)).To(Succeed())
			// Wait for the seed apply to complete — labels appear after the main apply
			g.Expect(v2pr.Labels).To(HaveKeyWithValue("team", "networking"),
				"seed labels not yet applied, current labels: %v", v2pr.Labels)
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("verifying source labels are preserved on the v1alpha2 CRD")
		v2pr := &porchv1alpha2.PackageRevision{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: v2Name}, v2pr)).To(Succeed())

		Expect(v2pr.Labels).To(HaveKeyWithValue("team", "networking"))
		Expect(v2pr.Labels).To(HaveKeyWithValue("env", "production"))

		By("verifying source annotations are preserved on the v1alpha2 CRD")
		Expect(v2pr.Annotations).To(HaveKeyWithValue("contact", "team-net@example.com"))
		Expect(v2pr.Annotations).To(HaveKeyWithValue("purpose", "infra-base"))

		By("verifying system labels are also present (not clobbered)")
		Expect(v2pr.Labels).To(HaveKeyWithValue(porchv1alpha2.RepositoryLabelKey, repoName))
		Expect(v2pr.Labels).To(HaveKey(porchv1alpha2.LatestPackageRevisionKey))
	})

	It("should not carry system-only labels as source metadata", func() {
		// The v1alpha1 PR will also have system labels (latest-revision, repository).
		// These must NOT be double-applied via the seed — they are already managed
		// by the main repo controller field manager.
		v2Name := crdName(repoName, pkgName, workspace)
		v2pr := &porchv1alpha2.PackageRevision{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: v2Name}, v2pr)).To(Succeed())

		// The repo label should be the repo name (set by repo controller, not seed)
		Expect(v2pr.Labels[porchv1alpha2.RepositoryLabelKey]).To(Equal(repoName))

		// Count non-system labels — should be exactly our 2 custom ones
		nonSystemCount := 0
		for k := range v2pr.Labels {
			if k != porchv1alpha2.RepositoryLabelKey && k != porchv1alpha2.LatestPackageRevisionKey {
				nonSystemCount++
			}
		}
		Expect(nonSystemCount).To(Equal(2), "only custom source labels should be present")
	})
})

var _ = Describe("Migration Kptfile-Derived Metadata", Ordered, Label("migration"), func() {
	// Tests that Kptfile-derived metadata (spec.packageMetadata) is properly
	// seeded during v1alpha1→v1alpha2 migration and that those labels are
	// promoted to object.metadata.labels on the v1alpha2 CRD.

	const (
		repoName  = "mig-kptfile-repo"
		pkgName   = "kptfile-meta"
		workspace = "v1"
	)

	var env *testEnv
	var v1PrName string

	BeforeAll(func() {
		env = sharedEnv()

		By("creating fresh gitea repo")
		deleteGiteaRepo(repoName)
		createGiteaRepo(repoName)

		By("registering repo as v1alpha1")
		registerV1Alpha1Repo(env.Ctx, env.Namespace, repoName)

		By("creating v1alpha1 package via Init task")
		pr := &porchv1alpha1.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{Namespace: env.Namespace},
			Spec: porchv1alpha1.PackageRevisionSpec{
				PackageName:    pkgName,
				WorkspaceName:  workspace,
				RepositoryName: repoName,
				Tasks: []porchv1alpha1.Task{
					{Type: porchv1alpha1.TaskTypeInit, Init: &porchv1alpha1.PackageInitTaskSpec{Description: pkgName}},
				},
			},
		}
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		v1PrName = pr.Name

		By("waiting for draft to be created")
		Eventually(func(g Gomega) {
			draft := &porchv1alpha1.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: v1PrName}, draft)).To(Succeed())
			g.Expect(draft.Spec.Lifecycle).To(Equal(porchv1alpha1.PackageRevisionLifecycleDraft))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("updating Kptfile with labels and annotations (while Draft)")
		kptfileContent := `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: ` + pkgName + `
  labels:
    app: migration-test
    environment: testing
    kpt.dev/version: v1beta1
  annotations:
    description: test-package-for-migration
    team: platform
`
		updatePRRResources(env.Ctx, env.Namespace, v1PrName, map[string]string{
			"Kptfile": kptfileContent,
		})

		By("publishing the package via Draft → Proposed → Published lifecycle")
		publishV1Alpha1Package(env.Ctx, env.Namespace, v1PrName)

		By("setting custom labels and annotations on the published v1alpha1 package")
		Eventually(func(g Gomega) {
			pr := &porchv1alpha1.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: v1PrName}, pr)).To(Succeed())
			if pr.Labels == nil {
				pr.Labels = make(map[string]string)
			}
			pr.Labels["owner"] = "platform-team"
			pr.Labels["cost-center"] = "engineering"
			if pr.Annotations == nil {
				pr.Annotations = make(map[string]string)
			}
			pr.Annotations["contact"] = "platform@example.com"
			pr.Annotations["runbook"] = "https://wiki.example.com/runbook"
			g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	AfterAll(func() {
		cleanupMigrationResources(env.Ctx, env.Namespace, repoName)
		deleteGiteaRepo(repoName)
	})

	It("should preserve Kptfile metadata in spec.packageMetadata and promote labels", func() {
		By("enabling v1alpha2 migration and triggering sync")
		enableV1Alpha2Migration(env.Ctx, env.Namespace, repoName)

		By("waiting for v1alpha2 CRD to be created")
		v2Name := crdName(repoName, pkgName, workspace)
		v2pr := &porchv1alpha2.PackageRevision{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: v2Name}, v2pr)).To(Succeed())
		}).WithTimeout(defaultTimeout * 3).WithPolling(defaultInterval).Should(Succeed())

		By("waiting for migration sync to complete")
		waitForRepoReady(env.Ctx, env.Namespace, repoName)

		By("re-fetching v1alpha2 CRD after sync completes")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: v2Name}, v2pr)).To(Succeed())

		By("verifying spec.packageMetadata was seeded from Kptfile")
		Expect(v2pr.Spec.PackageMetadata).NotTo(BeNil(),
			"spec.packageMetadata should be seeded from Kptfile")
		Expect(v2pr.Spec.PackageMetadata.Labels).To(HaveKeyWithValue("app", "migration-test"),
			"Kptfile label 'app' should be in spec.packageMetadata.Labels")
		Expect(v2pr.Spec.PackageMetadata.Labels).To(HaveKeyWithValue("environment", "testing"),
			"Kptfile label 'environment' should be in spec.packageMetadata.Labels")
		Expect(v2pr.Spec.PackageMetadata.Labels).To(HaveKeyWithValue("kpt.dev/version", "v1beta1"),
			"Kptfile label 'kpt.dev/version' (with slash) should be in spec.packageMetadata.Labels")
		Expect(v2pr.Spec.PackageMetadata.Annotations).To(HaveKeyWithValue("description", "test-package-for-migration"),
			"Kptfile annotation 'description' should be in spec.packageMetadata.Annotations")
		Expect(v2pr.Spec.PackageMetadata.Annotations).To(HaveKeyWithValue("team", "platform"),
			"Kptfile annotation 'team' should be in spec.packageMetadata.Annotations")

		By("verifying custom object.metadata.labels from v1alpha1 are preserved")
		Expect(v2pr.Labels).To(HaveKeyWithValue("owner", "platform-team"),
			"Custom label 'owner' from v1alpha1 should be preserved")
		Expect(v2pr.Labels).To(HaveKeyWithValue("cost-center", "engineering"),
			"Custom label 'cost-center' from v1alpha1 should be preserved")

		By("verifying packageMetadata labels are promoted with porch.kpt.dev/kptfile-label__ prefix")
		// Kptfile labels are promoted to object.metadata.labels with the porch.kpt.dev/kptfile-label__ prefix
		// for immediate kubectl -l queryability (no wait for PR controller render).
		// The "/" in label keys is escaped to "__" for Kubernetes label key compatibility.
		// Example: kpt.dev/version → porch.kpt.dev/kptfile-label__kpt.dev__version
		Expect(v2pr.Labels).To(HaveKeyWithValue("porch.kpt.dev/kptfile-label__app", "migration-test"),
			"PackageMetadata label 'app' should be promoted to object.metadata.labels with prefix")
		Expect(v2pr.Labels).To(HaveKeyWithValue("porch.kpt.dev/kptfile-label__environment", "testing"),
			"PackageMetadata label 'environment' should be promoted to object.metadata.labels with prefix")
		Expect(v2pr.Labels).To(HaveKeyWithValue("porch.kpt.dev/kptfile-label__kpt.dev__version", "v1beta1"),
			"PackageMetadata label 'kpt.dev/version' should be promoted (escaped to kpt.dev__version) with prefix for immediate queryability")

		By("verifying custom object.metadata.annotations from v1alpha1 are preserved")
		Expect(v2pr.Annotations).To(HaveKeyWithValue("contact", "platform@example.com"),
			"Custom annotation 'contact' from v1alpha1 should be preserved")
		Expect(v2pr.Annotations).To(HaveKeyWithValue("runbook", "https://wiki.example.com/runbook"),
			"Custom annotation 'runbook' from v1alpha1 should be preserved")
	})

	It("should preserve seeded label values", func() {
		// Verify that seeded labels persist with correct values.
		// If PR controller later processes the package, kptfileLabelsToObjectLabels()
		// detects matching labels and skips apply (idempotent).
		v2Name := crdName(repoName, pkgName, workspace)
		v2pr := &porchv1alpha2.PackageRevision{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: v2Name}, v2pr)).To(Succeed())

		Expect(v2pr.Labels).To(HaveKeyWithValue("porch.kpt.dev/kptfile-label__app", "migration-test"))
		Expect(v2pr.Labels).To(HaveKeyWithValue("porch.kpt.dev/kptfile-label__kpt.dev__version", "v1beta1"))
	})
})

// Helper functions for migration metadata tests

// enableV1Alpha2Migration enables v1alpha2 migration and triggers a sync on the repository.
func enableV1Alpha2Migration(ctx context.Context, namespace, repoName string) {
	Eventually(func(g Gomega) {
		repo := &configapi.Repository{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: repoName}, repo)).To(Succeed())
		if repo.Annotations == nil {
			repo.Annotations = make(map[string]string)
		}
		repo.Annotations["porch.kpt.dev/v1alpha2-migration"] = "true"
		now := metav1.Now()
		if repo.Spec.Sync == nil {
			repo.Spec.Sync = &configapi.RepositorySync{}
		}
		repo.Spec.Sync.RunOnceAt = &now
		g.Expect(k8sClient.Update(ctx, repo)).To(Succeed())
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
}

// publishV1Alpha1Package transitions a v1alpha1 package from Draft → Proposed → Published.
func publishV1Alpha1Package(ctx context.Context, namespace, prName string) {
	Eventually(func(g Gomega) {
		proposed := &porchv1alpha1.PackageRevision{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: prName}, proposed)).To(Succeed())
		proposed.Spec.Lifecycle = porchv1alpha1.PackageRevisionLifecycleProposed
		g.Expect(k8sClient.Update(ctx, proposed)).To(Succeed())
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

	Eventually(func(g Gomega) {
		published := &porchv1alpha1.PackageRevision{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: prName}, published)).To(Succeed())
		published.Spec.Lifecycle = porchv1alpha1.PackageRevisionLifecyclePublished
		g.Expect(k8sClient.SubResource("approval").Update(ctx, published)).To(Succeed())
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

	Eventually(func(g Gomega) {
		pub := &porchv1alpha1.PackageRevision{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: prName}, pub)).To(Succeed())
		g.Expect(pub.Spec.Lifecycle).To(Equal(porchv1alpha1.PackageRevisionLifecyclePublished))
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
}
