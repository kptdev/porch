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
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configapi "github.com/nephio-project/porch/api/porchconfig/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const fnNamespace = "porch-fn-system"

var _ = Describe("FunctionConfig", Ordered, Label("content"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should add controller finalizer to FunctionConfig CRDs", func() {
		fc := &configapi.FunctionConfig{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
			Namespace: fnNamespace,
			Name:      "set-namespace",
		}, fc)).To(Succeed())

		Expect(fc.Finalizers).To(ContainElement(
			ContainSubstring("controller"),
		), "FunctionConfig should have the controller finalizer")
	})

	It("should set controllerObservedGeneration status", func() {
		fc := &configapi.FunctionConfig{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
			Namespace: fnNamespace,
			Name:      "set-namespace",
		}, fc)).To(Succeed())

		Expect(fc.Status.ControllerObservedGeneration).To(
			BeNumerically(">", 0),
			"controllerObservedGeneration should be set",
		)
	})

	It("should render using a dynamically-configured FunctionConfig tag", func() {
		By("patching set-namespace FunctionConfig with a custom tag")
		customTag := "v99.0.0"
		patch := []map[string]interface{}{
			{"op": "add", "path": "/spec/goExecutor/tags/-", "value": customTag},
		}
		patchBytes, err := json.Marshal(patch)
		Expect(err).NotTo(HaveOccurred())

		fc := &configapi.FunctionConfig{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: fnNamespace,
				Name:      "set-namespace",
			},
		}
		Expect(k8sClient.Patch(env.Ctx, fc, client.RawPatch(types.JSONPatchType, patchBytes))).To(Succeed())

		By("waiting for controller to reconcile the updated FunctionConfig")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(fc), fc)).To(Succeed())
			g.Expect(fc.Status.ControllerObservedGeneration).To(Equal(fc.Generation))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "fnconfig-dynamic", "v1", withInit("FunctionConfig dynamic tag test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing a pipeline referencing the custom tag")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: fnconfig-dynamic\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:" + customTag + "\n    configMap:\n      namespace: dynamic-tag-ns\n",
			"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: fnconfig-cm\ndata:\n  key: value\n",
		})

		By("waiting for successful render")
		waitForRendered(env.Ctx, pr)
		waitForReady(env.Ctx, pr)

		By("verifying the dynamically-configured function rendered correctly")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources["cm.yaml"]).To(ContainSubstring("namespace: dynamic-tag-ns"),
			"set-namespace:%s should have rendered — proves FunctionConfig dynamic wiring works", customTag)

		By("cleaning up: removing custom tag from FunctionConfig")
		restorePatch := []map[string]interface{}{
			{"op": "replace", "path": "/spec/goExecutor/tags", "value": []string{"v0.4.1", "v0.4"}},
		}
		restoreBytes, err := json.Marshal(restorePatch)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Patch(env.Ctx, fc, client.RawPatch(types.JSONPatchType, restoreBytes))).To(Succeed())
	})

	It("should remove tag from builtin runtime when FunctionConfig is updated", func() {
		By("adding a custom tag to set-namespace")
		ephemeralTag := "v88.0.0"
		addPatch := []map[string]interface{}{
			{"op": "add", "path": "/spec/goExecutor/tags/-", "value": ephemeralTag},
		}
		addBytes, err := json.Marshal(addPatch)
		Expect(err).NotTo(HaveOccurred())

		fc := &configapi.FunctionConfig{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: fnNamespace,
				Name:      "set-namespace",
			},
		}
		Expect(k8sClient.Patch(env.Ctx, fc, client.RawPatch(types.JSONPatchType, addBytes))).To(Succeed())

		By("waiting for controller to reconcile")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(fc), fc)).To(Succeed())
			g.Expect(fc.Status.ControllerObservedGeneration).To(Equal(fc.Generation))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("removing the custom tag")
		removePatch := []map[string]interface{}{
			{"op": "replace", "path": "/spec/goExecutor/tags", "value": []string{"v0.4.1", "v0.4"}},
		}
		removeBytes, err := json.Marshal(removePatch)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Patch(env.Ctx, fc, client.RawPatch(types.JSONPatchType, removeBytes))).To(Succeed())

		By("waiting for controller to reconcile the removal")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(fc), fc)).To(Succeed())
			g.Expect(fc.Status.ControllerObservedGeneration).To(Equal(fc.Generation))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("creating a package that references the removed tag")
		pr := newPackageRevision(env.Namespace, env.RepoName, "fnconfig-removed", "v1", withInit("FunctionConfig tag removal test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing a pipeline referencing the removed tag")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: fnconfig-removed\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:" + ephemeralTag + "\n    configMap:\n      namespace: should-not-render\n",
			"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: removed-cm\ndata:\n  key: value\n",
		})

		By("waiting for render to fail (tag no longer in builtin runtime)")
		waitForRenderFailed(env.Ctx, pr)
	})
})
