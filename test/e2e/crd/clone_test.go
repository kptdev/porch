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
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Clone", Ordered, Label("lifecycle"), func() {
	var (
		env            *testEnv
		upstreamRepo   string
		downstreamRepo string
	)

	BeforeAll(func() {
		env = sharedEnv()
		upstreamRepo = testBlueprintsRepo
		downstreamRepo = porchTestRepo
		// Both repos are registered in BeforeSuite
	})

	It("should clone from upstream ref", func() {
		upstreamCRD := crdName(upstreamRepo, "basens", "v1")

		By("creating a clone from upstream ref")
		pr := newPackageRevision(env.Namespace, downstreamRepo, "basens", "v1", withCloneFromRef(upstreamCRD))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("verifying creationSource is clone")
		Expect(pr.Status.CreationSource).To(Equal("clone"))

		By("verifying Kptfile has upstream and upstreamLock")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources).To(HaveKey("Kptfile"))
		Expect(resources["Kptfile"]).To(ContainSubstring("upstream:"))
		Expect(resources["Kptfile"]).To(ContainSubstring("upstreamLock:"))

		By("verifying selfLock is set")
		Expect(pr.Status.SelfLock).NotTo(BeNil())
		Expect(pr.Status.SelfLock.Git).NotTo(BeNil())
		Expect(pr.Status.SelfLock.Git.Commit).NotTo(BeEmpty())
	})

	It("should clone into a deployment repository", func() {
		By("creating and registering a deployment repo")
		deployRepo := "clone-deploy"
		createGiteaRepo(deployRepo)
		registerV1Alpha2Repo(env.Ctx, env.Namespace, deployRepo, withDeployment())
		DeferCleanup(func() {
			cleanupRepo(env.Ctx, env.Namespace, deployRepo)
			deleteGiteaRepo(deployRepo)
		})

		upstreamCRD := crdName(upstreamRepo, "basens", "v1")

		By("cloning into the deployment repo")
		pr := newPackageRevision(env.Namespace, deployRepo, "basens-deploy", "v1", withCloneFromRef(upstreamCRD))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("verifying Kptfile exists with upstream info")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources).To(HaveKey("Kptfile"))
		Expect(resources["Kptfile"]).To(ContainSubstring("upstream:"))
	})

	It("should clone with leading slash in directory", func() {
		By("cloning basens with leading slash in git directory")
		pr := newPackageRevision(env.Namespace, downstreamRepo, "basens-ls", "v1", func(pr *porchv1alpha2.PackageRevision) {
			pr.Spec.Source = &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					Type: porchv1alpha2.RepositoryTypeGit,
					Git: &porchv1alpha2.GitPackage{
						Repo:      giteaRepoURL(upstreamRepo),
						Ref:       "basens/v1",
						Directory: "/basens",
						SecretRef: porchv1alpha2.SecretRef{
							Name: downstreamRepo + "-auth",
						},
					},
				},
			}
		})
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("verifying package was created successfully")
		Expect(pr.Status.CreationSource).To(Equal("clone"))
	})

	It("should clone from git URL", func() {
		By("creating a clone from raw git URL")
		pr := newPackageRevision(env.Namespace, downstreamRepo, "basens", "git-clone", func(pr *porchv1alpha2.PackageRevision) {
			pr.Spec.Source = &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					Type: porchv1alpha2.RepositoryTypeGit,
					Git: &porchv1alpha2.GitPackage{
						Repo:      giteaRepoURL(upstreamRepo),
						Ref:       "basens/v1",
						Directory: "/basens",
						SecretRef: porchv1alpha2.SecretRef{
							Name: downstreamRepo + "-auth",
						},
					},
				},
			}
		})
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("verifying creationSource is clone")
		Expect(pr.Status.CreationSource).To(Equal("clone"))

		By("verifying Kptfile references the git URL")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources["Kptfile"]).To(ContainSubstring(upstreamRepo + ".git"))
	})

	It("should clone and publish through full lifecycle", func() {
		upstreamCRD := crdName(upstreamRepo, "basens", "v1")

		By("cloning from upstream")
		pr := newPackageRevision(env.Namespace, downstreamRepo, "basens", "pub-v1", withCloneFromRef(upstreamCRD))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("publishing the clone")
		publishPackage(env.Ctx, pr)

		By("verifying publish metadata")
		Expect(pr.Status.CreationSource).To(Equal("clone"))
		Expect(pr.Status.Revision).NotTo(Equal(0))
	})

	It("should clone a package with a render pipeline", func() {
		By("waiting for bucket/v1 discovery (has apply-setters pipeline)")
		waitForDiscovery(env.Ctx, env.Namespace, crdName(upstreamRepo, "bucket", "v1"))

		By("cloning bucket/v1")
		pr := newPackageRevision(env.Namespace, downstreamRepo, "bucket", "v1",
			withCloneFromRef(crdName(upstreamRepo, "bucket", "v1")))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		waitForRendered(env.Ctx, pr)

		By("verifying apply-setters rendered the content")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources).To(HaveKey("bucket.yaml"))
		Expect(resources["bucket.yaml"]).To(ContainSubstring("storageClass: nearline"))
	})

	Context("Upgrade", func() {
		It("should upgrade with resource-merge", func() {
			By("waiting for basens/v2 discovery")
			triggerRepoSync(env.Ctx, env.Namespace, upstreamRepo)
			waitForDiscovery(env.Ctx, env.Namespace, crdName(upstreamRepo, "basens", "v2"))

			By("cloning basens/v1 to downstream and publishing")
			downV1 := newPackageRevision(env.Namespace, downstreamRepo, "basens", "merge-v1",
				withCloneFromRef(crdName(upstreamRepo, "basens", "v1")))
			Expect(k8sClient.Create(env.Ctx, downV1)).To(Succeed())
			waitForReady(env.Ctx, downV1)
			publishPackage(env.Ctx, downV1)

			By("creating upgrade from v1 to v2")
			upgraded := newPackageRevision(env.Namespace, downstreamRepo, "basens", "v2-merge",
				withUpgrade(
					crdName(upstreamRepo, "basens", "v1"),
					crdName(upstreamRepo, "basens", "v2"),
					downV1.Name,
				))
			Expect(k8sClient.Create(env.Ctx, upgraded)).To(Succeed())
			waitForReady(env.Ctx, upgraded)

			By("verifying creationSource is upgrade")
			Expect(upgraded.Status.CreationSource).To(Equal("upgrade"))

			By("verifying merged content has both v1 and v2 files")
			resources := getPRRResources(env.Ctx, env.Namespace, upgraded.Name)
			Expect(resources).To(HaveKey("resourcequota.yaml"))
			Expect(resources).To(HaveKey("namespace.yaml"))
		})

		It("should upgrade with force-delete-replace", func() {
			By("waiting for basens/v2 discovery")
			triggerRepoSync(env.Ctx, env.Namespace, upstreamRepo)
			waitForDiscovery(env.Ctx, env.Namespace, crdName(upstreamRepo, "basens", "v2"))

			By("cloning basens/v1 to downstream and publishing")
			downV1 := newPackageRevision(env.Namespace, downstreamRepo, "basens", "fdr-v1",
				withCloneFromRef(crdName(upstreamRepo, "basens", "v1")))
			Expect(k8sClient.Create(env.Ctx, downV1)).To(Succeed())
			waitForReady(env.Ctx, downV1)
			publishPackage(env.Ctx, downV1)

			By("creating force-delete-replace upgrade")
			upgraded := newPackageRevision(env.Namespace, downstreamRepo, "basens", "v2-force",
				withUpgradeStrategy(
					crdName(upstreamRepo, "basens", "v1"),
					crdName(upstreamRepo, "basens", "v2"),
					downV1.Name,
					porchv1alpha2.ForceDeleteReplace,
				))
			Expect(k8sClient.Create(env.Ctx, upgraded)).To(Succeed())
			waitForReady(env.Ctx, upgraded)

			By("verifying v2 content present")
			resources := getPRRResources(env.Ctx, env.Namespace, upgraded.Name)
			Expect(resources).To(HaveKey("resourcequota.yaml"))
		})

		It("should upgrade with fast-forward", func() {
			By("waiting for basens/v2 discovery")
			triggerRepoSync(env.Ctx, env.Namespace, upstreamRepo)
			waitForDiscovery(env.Ctx, env.Namespace, crdName(upstreamRepo, "basens", "v2"))

			By("cloning basens/v1 to downstream and publishing")
			downV1 := newPackageRevision(env.Namespace, downstreamRepo, "basens", "ff-v1",
				withCloneFromRef(crdName(upstreamRepo, "basens", "v1")))
			Expect(k8sClient.Create(env.Ctx, downV1)).To(Succeed())
			waitForReady(env.Ctx, downV1)
			publishPackage(env.Ctx, downV1)

			By("creating fast-forward upgrade")
			upgraded := newPackageRevision(env.Namespace, downstreamRepo, "basens", "v2-ff",
				withUpgradeStrategy(
					crdName(upstreamRepo, "basens", "v1"),
					crdName(upstreamRepo, "basens", "v2"),
					downV1.Name,
					porchv1alpha2.FastForward,
				))
			Expect(k8sClient.Create(env.Ctx, upgraded)).To(Succeed())
			waitForReady(env.Ctx, upgraded)

			By("verifying v2 content present")
			resources := getPRRResources(env.Ctx, env.Namespace, upgraded.Name)
			Expect(resources).To(HaveKey("resourcequota.yaml"))
		})

		It("should upgrade with copy-merge", func() {
			By("waiting for basens/v2 discovery")
			triggerRepoSync(env.Ctx, env.Namespace, upstreamRepo)
			waitForDiscovery(env.Ctx, env.Namespace, crdName(upstreamRepo, "basens", "v2"))

			By("cloning basens/v1 to downstream and publishing")
			downV1 := newPackageRevision(env.Namespace, downstreamRepo, "basens", "cm-v1",
				withCloneFromRef(crdName(upstreamRepo, "basens", "v1")))
			Expect(k8sClient.Create(env.Ctx, downV1)).To(Succeed())
			waitForReady(env.Ctx, downV1)
			publishPackage(env.Ctx, downV1)

			By("creating copy-merge upgrade")
			upgraded := newPackageRevision(env.Namespace, downstreamRepo, "basens", "v2-cm",
				withUpgradeStrategy(
					crdName(upstreamRepo, "basens", "v1"),
					crdName(upstreamRepo, "basens", "v2"),
					downV1.Name,
					porchv1alpha2.CopyMerge,
				))
			Expect(k8sClient.Create(env.Ctx, upgraded)).To(Succeed())
			waitForReady(env.Ctx, upgraded)

			By("verifying v2 content present")
			resources := getPRRResources(env.Ctx, env.Namespace, upgraded.Name)
			Expect(resources).To(HaveKey("resourcequota.yaml"))
		})

		It("should fail upgrade with non-existent oldUpstream", func() {
			By("waiting for basens/v2 discovery")
			triggerRepoSync(env.Ctx, env.Namespace, upstreamRepo)
			waitForDiscovery(env.Ctx, env.Namespace, crdName(upstreamRepo, "basens", "v2"))

			By("cloning basens/v1 to downstream and publishing")
			downV1 := newPackageRevision(env.Namespace, downstreamRepo, "basens", "up-v1",
				withCloneFromRef(crdName(upstreamRepo, "basens", "v1")))
			Expect(k8sClient.Create(env.Ctx, downV1)).To(Succeed())
			waitForReady(env.Ctx, downV1)
			publishPackage(env.Ctx, downV1)

			By("creating upgrade with non-existent oldUpstream")
			bad := newPackageRevision(env.Namespace, downstreamRepo, "basens", "bad-old",
				withUpgrade("does-not-exist", crdName(upstreamRepo, "basens", "v2"), downV1.Name))
			Expect(k8sClient.Create(env.Ctx, bad)).To(Succeed())
			waitForReadyFalse(env.Ctx, bad)

			By("verifying error message mentions old upstream")
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(bad), bad)).To(Succeed())
			readyCond := findCondition(bad.Status.Conditions, porchv1alpha2.ConditionReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Message).To(ContainSubstring("old upstream"))
		})
	})

	It("should clone using bearer token authentication", func() {
		By("creating a gitea API token")
		tokenResp, err := createGiteaAPIToken("porch-e2e-bearer-token")
		Expect(err).NotTo(HaveOccurred())
		Expect(tokenResp).NotTo(BeEmpty())
		DeferCleanup(deleteGiteaAPIToken, "porch-e2e-bearer-token")

		By("creating a k8s secret with the bearer token")
		tokenSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bearer-token-secret",
				Namespace: env.Namespace,
			},
			Type: corev1.SecretTypeOpaque,
			StringData: map[string]string{
				"bearerToken": tokenResp,
			},
		}
		Expect(k8sClient.Create(env.Ctx, tokenSecret)).To(Succeed())

		By("cloning using bearer token auth")
		pr := newPackageRevision(env.Namespace, downstreamRepo, "basens", "token-clone", func(pr *porchv1alpha2.PackageRevision) {
			pr.Spec.Source = &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					Type: porchv1alpha2.RepositoryTypeGit,
					Git: &porchv1alpha2.GitPackage{
						Repo:      giteaRepoURL("test-blueprints"),
						Ref:       "basens/v1",
						Directory: "/basens",
						SecretRef: porchv1alpha2.SecretRef{
							Name: "bearer-token-secret",
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
		Expect(resources["Kptfile"]).To(ContainSubstring("upstream:"))
	})

	// CA bundle clone requires TLS-enabled gitea, which the current
	// kind dev env doesn't provide. Needs setup-dev-env.sh changes.
	PIt("should clone using CA bundle for TLS verification")
})

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
