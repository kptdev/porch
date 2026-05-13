// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package pods

import (
	"time"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/test/e2e/suiteutils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func RestartPorchServer(t *suiteutils.MultiClusterTestSuite) {
	t.T().Helper()

	t.Logf("Restarting porch-server microservice")

	if err := t.KubeClient.CoreV1().Pods("porch-system").
		DeleteCollection(t.GetContext(),
			metav1.DeleteOptions{GracePeriodSeconds: new(int64)},
			metav1.ListOptions{LabelSelector: "app=porch-server"}); err != nil {
		t.Fatalf("error restarting porch-server: error deleting porch-server pod: %w", err)
	}
	watcher, err := t.KubeClient.CoreV1().Pods("porch-system").
		Watch(t.GetContext(), metav1.ListOptions{
			LabelSelector: "app=porch-server",
		})
	if err != nil {
		t.Logf("error after restarting porch-server: error watching recreated porch-server pod: %w", err)
	}

	t.Logf("Waiting for porch-server pod...")
	for event := range watcher.ResultChan() {
		p, ok := event.Object.(*corev1.Pod)
		if !ok {
			t.Logf("unexpected type")
		}
		if len(p.Status.ContainerStatuses) > 0 && p.Status.ContainerStatuses[0].Ready == true {
			watcher.Stop()
			t.Logf("porch-server pod back to Ready")
			break
		}
		t.Logf("Still waiting for porch-server pod...")
	}

	t.Logf("porch-server pod ready")

	var repos configapi.RepositoryList
	t.ListF(&repos, client.InNamespace(t.Namespace))
	t.WaitUntilMultipleRepositoriesReady(repos.Items)
	time.Sleep(1 * time.Minute)
}

func RestartAllPorchPods(t *suiteutils.MultiClusterTestSuite) {
	t.T().Helper()

	t.Logf("Restarting all pods in porch-system namespace")

	if err := t.KubeClient.CoreV1().Pods("porch-system").DeleteCollection(t.GetContext(),
		metav1.DeleteOptions{GracePeriodSeconds: new(int64)},
		metav1.ListOptions{}); err != nil {
		t.Fatalf("error deleting pods to restart")
	}

	watcher, err := t.KubeClient.CoreV1().Pods("porch-system").
		Watch(t.GetContext(), metav1.ListOptions{
			LabelSelector: "app=porch-server",
		})
	if err != nil {
		t.Logf("error after restarting pods: error watching recreated porch-server pod: %w", err)
	}

	t.Logf("Waiting for porch-server pod as most important to test case...")
	for event := range watcher.ResultChan() {
		p, ok := event.Object.(*corev1.Pod)
		if !ok {
			t.Logf("Got unexpected type watching porch-server pod")
		}
		if len(p.Status.ContainerStatuses) > 0 && p.Status.ContainerStatuses[0].Ready == true {
			watcher.Stop()
			t.Logf("porch-server pod back to Ready")
			break
		}
		t.Logf("Still waiting for porch-server pod...")
	}
}
