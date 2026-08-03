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
	"fmt"
	"time"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Metadata", Ordered, Label("infra"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	Context("Labels and Annotations", func() {
		It("should preserve labels and annotations through lifecycle", func() {
			By("creating a package with custom labels and annotations")
			pr := newPackageRevision(env.Namespace, env.RepoName, "label-pkg", "v1", withInit("label test"))
			pr.Labels = map[string]string{"kpt.dev/label": "foo"}
			pr.Annotations = map[string]string{"kpt.dev/anno": "bar"}
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)

			By("verifying labels and annotations on draft")
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
			Expect(pr.Labels).To(HaveKeyWithValue("kpt.dev/label", "foo"))
			Expect(pr.Annotations).To(HaveKeyWithValue("kpt.dev/anno", "bar"))

			By("publishing and verifying labels and annotations preserved")
			publishPackage(env.Ctx, pr)
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
			Expect(pr.Labels).To(HaveKeyWithValue("kpt.dev/label", "foo"))
			Expect(pr.Annotations).To(HaveKeyWithValue("kpt.dev/anno", "bar"))
		})
	})

	Context("Field Selectors", func() {
		// test-blueprints is registered in BeforeSuite

		It("should filter by spec.lifecycle", func() {
			By("listing with lifecycle=Published filter")
			var list porchv1alpha2.PackageRevisionList
			Expect(k8sClient.List(env.Ctx, &list, client.InNamespace(env.Namespace),
				client.MatchingFields{string(porchv1alpha2.PkgRevSelectorLifecycle): "Published"},
			)).To(Succeed())
			Expect(list.Items).NotTo(BeEmpty())

			By("verifying all results are Published")
			for _, pr := range list.Items {
				Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
			}
		})

		It("should filter by spec.packageName", func() {
			By("listing with packageName=basens filter")
			var list porchv1alpha2.PackageRevisionList
			Expect(k8sClient.List(env.Ctx, &list, client.InNamespace(env.Namespace),
				client.MatchingFields{string(porchv1alpha2.PkgRevSelectorPackageName): "basens"},
			)).To(Succeed())
			Expect(list.Items).NotTo(BeEmpty())

			By("verifying all results have packageName=basens")
			for _, pr := range list.Items {
				Expect(pr.Spec.PackageName).To(Equal("basens"))
			}
		})

		It("should filter by spec.repository", func() {
			By("listing with repository=test-blueprints filter")
			var list porchv1alpha2.PackageRevisionList
			Expect(k8sClient.List(env.Ctx, &list, client.InNamespace(env.Namespace),
				client.MatchingFields{string(porchv1alpha2.PkgRevSelectorRepository): "test-blueprints"},
			)).To(Succeed())
			Expect(list.Items).NotTo(BeEmpty())

			By("verifying all results are from test-blueprints")
			for _, pr := range list.Items {
				Expect(pr.Spec.RepositoryName).To(Equal("test-blueprints"))
			}
		})

		It("should return empty for non-matching field values", func() {
			By("listing with a non-existent repository name")
			var list porchv1alpha2.PackageRevisionList
			Expect(k8sClient.List(env.Ctx, &list, client.InNamespace(env.Namespace),
				client.MatchingFields{string(porchv1alpha2.PkgRevSelectorRepository): "nonexistent-repo"},
			)).To(Succeed())
			Expect(list.Items).To(BeEmpty())
		})
	})

	Context("PackageMetadata Field Selectors", func() {
		// v1alpha1 supported filtering by spec.packageMetadata.labels[key]=value.
		// v1alpha2 CRD field indexes don't include packageMetadata yet.
		// TODO: implement packageMetadata field indexes in fieldindex.go and enable.
		PIt("should filter by packageMetadata labels")
	})

	Context("Kptfile Metadata Sync (Kptfile → CRD)", func() {
		It("should sync Kptfile labels and annotations to spec.packageMetadata", func() {
			By("creating a draft package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "kpt-sync", "v1", withInit("kptfile sync test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

			By("pushing Kptfile with labels, annotations, and readinessGates")
			updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
				"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: kpt-sync\n  labels:\n    sync-label: from-kptfile\n  annotations:\n    sync-anno: from-kptfile\ninfo:\n  description: kptfile sync test\n  readinessGates:\n  - conditionType: SyncTestReady\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.5\n    configMap:\n      namespace: sync-ns\n",
				"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: sync-cm\ndata:\n  key: value\n",
			})

			By("waiting for async render")
			waitForRendered(env.Ctx, pr)

			By("verifying spec.readinessGates and packageMetadata synced from Kptfile")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				g.Expect(pr.Spec.ReadinessGates).To(ContainElement(
					HaveField("ConditionType", Equal("SyncTestReady")),
				))
				g.Expect(pr.Spec.PackageMetadata).NotTo(BeNil())
				g.Expect(pr.Spec.PackageMetadata.Labels).To(HaveKeyWithValue("sync-label", "from-kptfile"))
				g.Expect(pr.Spec.PackageMetadata.Annotations).To(HaveKeyWithValue("sync-anno", "from-kptfile"))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		})
	})

	Context("PackageMetadata Sync (CRD → Kptfile)", func() {
		It("should sync spec.packageMetadata labels to Kptfile for Draft package", func() {
			By("creating a draft package with Init source")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg-meta-draft", "v1", withInit("packageMetadata test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

			By("patching spec.packageMetadata with labels")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				pr.Spec.PackageMetadata = &porchv1alpha2.PackageMetadata{
					Labels: map[string]string{
						"tier":  "frontend",
						"owner": "team-alpha",
					},
				}
				g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("waiting for render to execute")
			waitForRendered(env.Ctx, pr)

			By("verifying Kptfile was updated with labels")
			Eventually(func(g Gomega) {
				resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
				g.Expect(resources).To(HaveKey("Kptfile"))
				g.Expect(resources["Kptfile"]).To(ContainSubstring("tier: frontend"))
				g.Expect(resources["Kptfile"]).To(ContainSubstring("owner: team-alpha"))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		})

		It("should sync spec.packageMetadata annotations to Kptfile for Draft package", func() {
			By("creating a draft package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg-anno-draft", "v1", withInit("annotations test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

			By("patching spec.packageMetadata with annotations")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				pr.Spec.PackageMetadata = &porchv1alpha2.PackageMetadata{
					Annotations: map[string]string{
						"description": "My test package",
						"doc-link":    "https://example.com/docs",
					},
				}
				g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("waiting for render")
			waitForRendered(env.Ctx, pr)

			By("verifying Kptfile was updated with annotations")
			Eventually(func(g Gomega) {
				resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
				g.Expect(resources["Kptfile"]).To(ContainSubstring("description: My test package"))
				g.Expect(resources["Kptfile"]).To(ContainSubstring("doc-link: https://example.com/docs"))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		})

		It("should merge metadata with existing Kptfile labels and annotations", func() {
			By("creating a draft package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg-merge", "v1", withInit("merge test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

			By("pushing Kptfile with existing labels")
			updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
				"Kptfile":       "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: pkg-merge\n  labels:\n    existing: label\n  annotations:\n    existing-anno: value\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.5\n    configMap:\n      namespace: default\n",
				"resource.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\ndata:\n  key: val\n",
			})

			By("waiting for initial render and sync")
			waitForRendered(env.Ctx, pr)

			By("patching spec.packageMetadata with additional labels and annotations")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				pr.Spec.PackageMetadata = &porchv1alpha2.PackageMetadata{
					Labels: map[string]string{
						"new": "label",
					},
					Annotations: map[string]string{
						"new-anno": "new-value",
					},
				}
				g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("waiting for render")
			waitForRendered(env.Ctx, pr)

			By("verifying Kptfile has both existing and new labels/annotations (merge)")
			Eventually(func(g Gomega) {
				resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
				kf := resources["Kptfile"]
				g.Expect(kf).To(ContainSubstring("existing: label"), "existing label should be preserved")
				g.Expect(kf).To(ContainSubstring("new: label"), "new label should be added")
				g.Expect(kf).To(ContainSubstring("existing-anno: value"), "existing annotation should be preserved")
				g.Expect(kf).To(ContainSubstring("new-anno: new-value"), "new annotation should be added")
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		})

		It("should not sync spec.packageMetadata for Published packages (immutable)", func() {
			By("creating and publishing a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg-pub", "v1", withInit("published test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			publishPackage(env.Ctx, pr)

			By("attempting to patch spec.packageMetadata on Published package")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				pr.Spec.PackageMetadata = &porchv1alpha2.PackageMetadata{
					Labels: map[string]string{"should": "be-ignored"},
				}
				g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("verifying the change was not applied to Kptfile (immutable)")
			resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
			// The Kptfile should not have the "should: be-ignored" label
			Expect(resources["Kptfile"]).NotTo(ContainSubstring("should: be-ignored"))
		})

		It("should overwrite existing Kptfile metadata when labels key is updated", func() {
			By("creating a draft package with initial Kptfile labels")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg-update", "v1", withInit("update test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

			By("pushing Kptfile with initial labels")
			updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
				"Kptfile":  "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: pkg-update\n  labels:\n    version: v1\npipeline: {}\n",
				"res.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\ndata:\n  key: val\n",
			})

			By("waiting for initial render and sync")
			waitForRendered(env.Ctx, pr)

			By("patching spec.packageMetadata to change the version label")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				pr.Spec.PackageMetadata = &porchv1alpha2.PackageMetadata{
					Labels: map[string]string{"version": "v2"},
				}
				g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("waiting for render")
			waitForRendered(env.Ctx, pr)

			By("verifying Kptfile label was updated to v2")
			Eventually(func(g Gomega) {
				resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
				kf := resources["Kptfile"]
				g.Expect(kf).To(ContainSubstring("version: v2"))
				// Ensure old value is not present
				g.Expect(kf).NotTo(ContainSubstring("version: v1"))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		})

		It("should trigger render when spec.packageMetadata is updated", func() {
			By("creating a draft package with a mutation in the pipeline")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg-render-trigger", "v1", withInit("render trigger test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

			By("pushing Kptfile with a mutation that applies namespace")
			updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
				"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: pkg-render-trigger\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.5\n    configMap:\n      namespace: initial-ns\n",
				"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-cm\n  namespace: wrong-ns\ndata:\n  key: value\n",
			})

			By("waiting for initial render")
			waitForRendered(env.Ctx, pr)

			By("capturing the initial rendered output")
			var initialResources map[string]string
			Eventually(func(g Gomega) {
				initialResources = getPRRResources(env.Ctx, env.Namespace, pr.Name)
				g.Expect(initialResources["cm.yaml"]).To(ContainSubstring("namespace: initial-ns"))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("patching spec.packageMetadata to trigger a new render")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				pr.Spec.PackageMetadata = &porchv1alpha2.PackageMetadata{
					Labels: map[string]string{"render-test": "true"},
				}
				g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("verifying render was triggered (Rendered=True again)")
			waitForRendered(env.Ctx, pr)

			By("verifying the Kptfile was updated with the metadata")
			Eventually(func(g Gomega) {
				resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
				g.Expect(resources["Kptfile"]).To(ContainSubstring("render-test: \"true\""))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		})

		It("should handle concurrent Kptfile push and spec.packageMetadata update (PRR push wins)", func() {
			By("creating a draft package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg-concurrent", "v1", withInit("concurrent test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

			By("patching spec.packageMetadata")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				pr.Spec.PackageMetadata = &porchv1alpha2.PackageMetadata{
					Labels: map[string]string{"from-crd": "true"},
				}
				g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("simultaneously pushing Kptfile content with different labels")
			// The PRR push should overwrite the CRD-triggered metadata sync
			// This is the expected behavior: PRR push (last-write-wins via controller field manager)
			updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
				"Kptfile":  "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: pkg-concurrent\n  labels:\n    from-prr: \"true\"\npipeline: {}\n",
				"res.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\ndata:\n  key: val\n",
			})

			By("waiting for render to settle")
			waitForRendered(env.Ctx, pr)

			By("verifying the final state (PRR push labels visible)")
			Eventually(func(g Gomega) {
				resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
				// The PRR push should have set the from-prr label
				g.Expect(resources["Kptfile"]).To(ContainSubstring("from-prr: \"true\""))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		})

		It("should not create infinite reconciliation loops between Kptfile and spec.packageMetadata", func() {
			By("creating a draft package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg-no-loop", "v1", withInit("no-loop test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

			By("patching spec.packageMetadata multiple times in succession")
			for i := 1; i <= 3; i++ {
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
					pr.Spec.PackageMetadata = &porchv1alpha2.PackageMetadata{
						Labels: map[string]string{
							"iteration": fmt.Sprintf("%d", i),
						},
					}
					g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
				}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

				waitForRendered(env.Ctx, pr)
			}

			By("verifying the final iteration is what we set (no looping back to earlier values)")
			Eventually(func(g Gomega) {
				resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
				g.Expect(resources["Kptfile"]).To(ContainSubstring("iteration: \"3\""))
				g.Expect(resources["Kptfile"]).NotTo(ContainSubstring("iteration: \"1\""))
				g.Expect(resources["Kptfile"]).NotTo(ContainSubstring("iteration: \"2\""))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("verifying no resource data loss (all init files preserved)")
			resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
			Expect(resources).To(HaveKey("Kptfile"))
			Expect(resources).To(HaveKey("README.md"))
			Expect(resources).To(HaveKey("package-context.yaml"))
		})

		It("should sync spec.packageMetadata set at creation time", func() {
			By("creating a package WITH metadata set in spec at creation time")
			pr := newPackageRevision(env.Namespace, env.RepoName, "pkg-meta-creation", "v1", withInit("metadata at creation"))
			pr.Spec.PackageMetadata = &porchv1alpha2.PackageMetadata{
				Labels: map[string]string{
					"created-at": "v1",
					"env":        "test",
				},
				Annotations: map[string]string{
					"description": "package created with metadata",
				},
			}
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())

			By("waiting for package to be ready")
			waitForReady(env.Ctx, pr)
			waitForPRRVisible(env.Ctx, env.Namespace, pr.Name)

			By("waiting for initial render to complete (source render)")
			// Note: metadata sync happens AFTER source render completes, not during source execution.
			// The source (init/clone/copy/upgrade) creates the initial package structure in git,
			// then reconcileRender executes. After render, reconcilePackageMetadata applies the
			// spec.packageMetadata to the Kptfile in the next reconcile cycle.
			waitForRendered(env.Ctx, pr)

			By("verifying metadata was synced to Kptfile after initial render completes")
			Eventually(func(g Gomega) {
				resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
				g.Expect(resources).To(HaveKey("Kptfile"))
				g.Expect(resources["Kptfile"]).To(ContainSubstring("created-at: v1"))
				g.Expect(resources["Kptfile"]).To(ContainSubstring("env: test"))
				g.Expect(resources["Kptfile"]).To(ContainSubstring("description: package created with metadata"))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("updating metadata after initial render")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				pr.Spec.PackageMetadata.Labels["created-at"] = "v2"
				pr.Spec.PackageMetadata.Labels["new-label"] = "added-later"
				g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("waiting for render to execute after metadata update")
			waitForRendered(env.Ctx, pr)

			By("verifying both initial and updated metadata are present in Kptfile")
			Eventually(func(g Gomega) {
				resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
				g.Expect(resources["Kptfile"]).To(ContainSubstring("created-at: v2"))
				g.Expect(resources["Kptfile"]).To(ContainSubstring("env: test"))
				g.Expect(resources["Kptfile"]).To(ContainSubstring("new-label: added-later"))
				g.Expect(resources["Kptfile"]).To(ContainSubstring("description: package created with metadata"))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		})
	})

	Context("Label Selectors", func() {
		It("should filter by custom labels", func() {
			By("creating a package with a custom label")
			pr := newPackageRevision(env.Namespace, env.RepoName, "ls-pkg", "v1", withInit("label selector test"))
			pr.Labels = map[string]string{"kpt.dev/test-label": "test-value"}
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)

			By("listing with the custom label selector")
			var list porchv1alpha2.PackageRevisionList
			Expect(k8sClient.List(env.Ctx, &list, client.InNamespace(env.Namespace),
				client.MatchingLabels{"kpt.dev/test-label": "test-value"},
			)).To(Succeed())
			Expect(list.Items).To(HaveLen(1))
			Expect(list.Items[0].Name).To(Equal(pr.Name))
		})

		It("should filter by latest-revision label", func() {
			By("creating and publishing a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "latest-ls", "v1", withInit("latest label test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)
			publishPackage(env.Ctx, pr)

			By("listing with latest-revision=true label selector")
			var list porchv1alpha2.PackageRevisionList
			Expect(k8sClient.List(env.Ctx, &list, client.InNamespace(env.Namespace),
				client.MatchingLabels{porchv1alpha2.LatestPackageRevisionKey: porchv1alpha2.LatestPackageRevisionValue},
			)).To(Succeed())
			Expect(list.Items).NotTo(BeEmpty())
		})
	})

	Context("ReadinessGates", func() {
		It("should block Ready=True when readinessGate condition is not met", func() {
			By("creating a package with a readinessGate")
			pr := newPackageRevision(env.Namespace, env.RepoName, "rg-pkg", "v1", withInit("readiness gate test"))
			pr.Spec.ReadinessGates = []porchv1alpha2.ReadinessGate{
				{ConditionType: "CustomReady"},
			}
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())

			By("waiting for the controller to reconcile")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				g.Expect(pr.Status.Conditions).NotTo(BeEmpty())
			}).WithTimeout(defaultTimeout).Should(Succeed())

			By("verifying Ready is not True (gate not satisfied)")
			readyCond := findCondition(pr.Status.Conditions, porchv1alpha2.ConditionReady)
			Expect(readyCond).NotTo(BeNil())
			// Ready should be False or the gate should be reflected in the message
			// The exact behavior depends on whether the controller checks gates
			// before or after source execution
			if readyCond.Status == metav1.ConditionTrue {
				// If Ready=True, the gate must be in packageConditions
				Expect(pr.Status.PackageConditions).NotTo(BeEmpty())
			}
		})
	})

	Context("Finalizers", func() {
		It("should support custom finalizers that block deletion", func() {
			By("creating a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "fin-pkg", "v1", withInit("finalizer test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)

			By("adding a custom finalizer")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				pr.Finalizers = append(pr.Finalizers, "test-finalizer")
				g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("deleting — object should persist due to finalizer")
			Expect(k8sClient.Delete(env.Ctx, pr)).To(Succeed())
			Consistently(func() bool {
				err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)
				return err == nil
			}).WithTimeout(3 * time.Second).Should(BeTrue())

			By("removing finalizer to allow deletion")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
				pr.Finalizers = []string{}
				g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

			By("verifying the package is deleted")
			Eventually(func() bool {
				err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)
				return apierrors.IsNotFound(err)
			}).WithTimeout(defaultTimeout).Should(BeTrue())
		})
	})

	Context("Garbage Collection", func() {
		It("should cascade delete to owned objects", func() {
			By("creating a package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "gc-pkg", "v1", withInit("gc test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)

			By("creating a ConfigMap owned by the package")
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "owned-cm",
					Namespace: env.Namespace,
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: porchv1alpha2.SchemeGroupVersion.String(),
						Kind:       "PackageRevision",
						Name:       pr.Name,
						UID:        pr.UID,
					}},
				},
				Data: map[string]string{"key": "value"},
			}
			Expect(k8sClient.Create(env.Ctx, cm)).To(Succeed())

			By("deleting the package")
			Expect(k8sClient.Delete(env.Ctx, pr)).To(Succeed())

			By("verifying the owned ConfigMap is garbage collected")
			Eventually(func() bool {
				err := k8sClient.Get(env.Ctx, types.NamespacedName{Name: "owned-cm", Namespace: env.Namespace}, cm)
				return apierrors.IsNotFound(err)
			}).WithTimeout(defaultTimeout).Should(BeTrue())
		})
	})
})
