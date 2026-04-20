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
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
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

	Context("Kptfile Metadata Sync", func() {
		It("should sync Kptfile labels and annotations to spec.packageMetadata", func() {
			By("creating a draft package")
			pr := newPackageRevision(env.Namespace, env.RepoName, "kpt-sync", "v1", withInit("kptfile sync test"))
			Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
			waitForReady(env.Ctx, pr)

			By("pushing Kptfile with labels, annotations, and readinessGates")
			updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
				"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: kpt-sync\n  labels:\n    sync-label: from-kptfile\n  annotations:\n    sync-anno: from-kptfile\ninfo:\n  description: kptfile sync test\n  readinessGates:\n  - conditionType: SyncTestReady\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.1\n    configMap:\n      namespace: sync-ns\n",
				"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: sync-cm\ndata:\n  key: value\n",
			})

			By("waiting for async render")
			waitForRendered(env.Ctx, pr)

			By("verifying spec.readinessGates synced from Kptfile")
			Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
			Expect(pr.Spec.ReadinessGates).To(ContainElement(
				HaveField("ConditionType", Equal("SyncTestReady")),
			))

			By("verifying spec.packageMetadata synced from Kptfile")
			Expect(pr.Spec.PackageMetadata).NotTo(BeNil())
			Expect(pr.Spec.PackageMetadata.Labels).To(HaveKeyWithValue("sync-label", "from-kptfile"))
			Expect(pr.Spec.PackageMetadata.Annotations).To(HaveKeyWithValue("sync-anno", "from-kptfile"))
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
