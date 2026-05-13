// Copyright 2022-2025 The kpt Authors
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

package suiteutils

import (
	"bytes"
	"io"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (t *TestSuite) DumpLogsForDeploymentE(deploymentKey client.ObjectKey) {
	t.T().Helper()
	t.dumpLogsForDeployment(deploymentKey, t.Errorf)
}

func (t *TestSuite) hasOwner(child, parent runtime.Object) bool {
	t.T().Helper()
	childAccessor, err := meta.Accessor(child)
	if err != nil {
		t.Fatalf("could not get accessor for %T: %v", child, err)
	}
	parentAccessor, err := meta.Accessor(parent)
	if err != nil {
		t.Fatalf("could not get accessor for %T: %v", parent, err)
	}

	for _, ownerRef := range childAccessor.GetOwnerReferences() {
		// Kind is not always populated, UID should suffice (as it's globally unique)
		if ownerRef.UID != parentAccessor.GetUID() {
			continue
		}
		return true
	}

	return false
}

func (t *TestSuite) dumpLogsForDeployment(deploymentKey client.ObjectKey, eh ErrorHandler) {
	t.T().Helper()
	deployment, err := t.KubeClient.AppsV1().Deployments(deploymentKey.Namespace).Get(t.GetContext(), deploymentKey.Name, metav1.GetOptions{})
	if err != nil {
		eh("failed to get deployemnt %v: %v", deploymentKey, err)
	}

	replicaSets, err := t.KubeClient.AppsV1().ReplicaSets(deployment.Namespace).List(t.GetContext(), metav1.ListOptions{})
	if err != nil {
		eh("failed to list replicasets: %v", err)
	}

	for i := range replicaSets.Items {
		replicaSet := &replicaSets.Items[i]
		if !t.hasOwner(replicaSet, deployment) {
			continue
		}
		t.dumpLogsForReplicaSet(replicaSet, eh)
	}
}

func (t *TestSuite) dumpLogsForReplicaSet(replicaSet *appsv1.ReplicaSet, eh ErrorHandler) {
	t.T().Helper()
	pods, err := t.KubeClient.CoreV1().Pods(replicaSet.Namespace).List(t.GetContext(), metav1.ListOptions{})
	if err != nil {
		eh("failed to list pods: %v", err)
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		if !t.hasOwner(pod, replicaSet) {
			continue
		}
		t.dumpLogsForPod(pod, eh)
	}
}

func (t *TestSuite) dumpLogsForPod(pod *corev1.Pod, eh ErrorHandler) {
	t.T().Helper()
	for _, container := range pod.Spec.Containers {
		podKey := client.ObjectKey{
			Namespace: pod.Namespace,
			Name:      pod.Name,
		}
		t.dumpLogsForPodContainer(podKey, container.Name, eh)
	}
}

func (t *TestSuite) dumpLogsForPodContainer(podKey client.ObjectKey, containerName string, eh ErrorHandler) {
	t.T().Helper()
	req := t.KubeClient.CoreV1().Pods(podKey.Namespace).GetLogs(podKey.Name, &corev1.PodLogOptions{Container: containerName})
	podLogs, err := req.Stream(t.GetContext())
	if err != nil {
		eh("failed to open pod logs %v %s: %v", podKey, containerName, err)
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	if _, err = io.Copy(buf, podLogs); err != nil {
		eh("failed to copy pod logs %v %s: %v", podKey, containerName, err)
	}

	t.Logf("Logs from pod %v, container %s: %s", podKey, containerName, buf.String())
}
