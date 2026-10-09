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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const podEvaluatorMutatorImage = "ghcr.io/kptdev/krm-functions-catalog/set-annotations:v0.1.4"

var _ = Describe("PodEvaluator", Ordered, Label("content"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should render a non-builtin function via the controller pod evaluator", func() {
		if !allInCluster {
			Skip("pod evaluator test requires in-cluster porch-controllers")
		}

		By("verifying porch-controllers is configured to use porch-server's pod evaluator")
		Expect(controllerEnv(env.Ctx, "POD_EVALUATOR_ADDRESS")).NotTo(BeEmpty(),
			"porch-controllers must set POD_EVALUATOR_ADDRESS to porch-server's FunctionEvaluator")

		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "podeval-pkg", "v1", withInit("pod evaluator test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing a pipeline that requires the pod evaluator")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: podeval-pkg\npipeline:\n  mutators:\n  - image: " + podEvaluatorMutatorImage + "\n    configMap:\n      foo: bar\n",
			"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: podeval-cm\ndata:\n  key: value\n",
		})

		By("waiting for Rendered=True")
		waitForRendered(env.Ctx, pr)
		waitForReady(env.Ctx, pr)

		By("verifying the mutator annotated the ConfigMap")
		Eventually(func(g Gomega) {
			resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
			g.Expect(resources).To(HaveKey("cm.yaml"))
			g.Expect(resources["cm.yaml"]).To(ContainSubstring("foo: bar"))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("verifying a function pod was created in porch-fn-system")
		Eventually(func(g Gomega) {
			podList := &corev1.PodList{}
			g.Expect(k8sClient.List(env.Ctx, podList, client.InNamespace(fnNamespace))).To(Succeed())
			g.Expect(podsMatchingFunctionImage(podList, podEvaluatorMutatorImage)).NotTo(BeEmpty(),
				"expected a function pod whose image is %s", podEvaluatorMutatorImage)
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})
})

// controllerEnv returns the value of the named env var from the porch-controllers
// pod template. The workload is normally a Deployment, but the sharding PoC runs
// it as a StatefulSet (deployments/sharding/), so accept either.
func controllerEnv(ctx context.Context, name string) string {
	key := client.ObjectKey{Namespace: "porch-system", Name: "porch-controllers"}

	deploy := &appsv1.Deployment{}
	if err := k8sClient.Get(ctx, key, deploy); err == nil {
		return containersEnv(deploy.Spec.Template.Spec.Containers, name)
	}

	sts := &appsv1.StatefulSet{}
	if err := k8sClient.Get(ctx, key, sts); err == nil {
		return containersEnv(sts.Spec.Template.Spec.Containers, name)
	}

	return ""
}

func containersEnv(containers []corev1.Container, name string) string {
	for i := range containers {
		for _, envVar := range containers[i].Env {
			if envVar.Name == name {
				return envVar.Value
			}
		}
	}
	return ""
}

func podsMatchingFunctionImage(podList *corev1.PodList, image string) []corev1.Pod {
	var matches []corev1.Pod
	for i := range podList.Items {
		pod := podList.Items[i]
		for _, c := range pod.Spec.Containers {
			if c.Image == image || strings.Contains(c.Image, image) {
				matches = append(matches, pod)
				break
			}
		}
	}
	return matches
}
