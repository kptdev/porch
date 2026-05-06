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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Metrics", Label("infra"), func() {
	It("should expose prometheus metrics from porch components", func() {
		if !allInCluster {
			Skip("metrics test requires all components in-cluster")
		}

		ctx := context.Background()
		kubeClient, err := kubernetes.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())

		By("finding porch-system pods")
		var pods corev1.PodList
		Expect(k8sClient.List(ctx, &pods, client.InNamespace("porch-system"))).To(Succeed())
		Expect(pods.Items).NotTo(BeEmpty())

		podsByPrefix := map[string]*corev1.Pod{}
		for i := range pods.Items {
			pod := &pods.Items[i]
			for _, prefix := range []string{"porch-server", "porch-controllers", "function-runner"} {
				if len(pod.Name) >= len(prefix) && pod.Name[:len(prefix)] == prefix {
					podsByPrefix[prefix] = pod
				}
			}
		}

		for _, prefix := range []string{"porch-controllers"} {
			pod, ok := podsByPrefix[prefix]
			if !ok {
				continue
			}
			By("checking metrics from " + prefix)
			resp, err := kubeClient.CoreV1().Pods("porch-system").
				ProxyGet("", pod.Name, "9464", "metrics", nil).
				DoRaw(ctx)
			Expect(err).NotTo(HaveOccurred())
			metrics := string(resp)
			Expect(metrics).To(ContainSubstring("http_client_"))
			Expect(metrics).To(ContainSubstring("target_info"))
		}
	})
})

// force metav1 import for compilation
var _ = metav1.Now
