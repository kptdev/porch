// Copyright 2025-2026 The kpt Authors
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
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	gitserver "github.com/kptdev/porch/test/git/pkg"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	TestGitServerImage = "test-git-server"
)

func (t *TestSuite) CreateGitRepo() GitConfig {
	// Deploy Git server via k8s client.
	t.Logf("creating git server in cluster")
	if t.IsTestRunnerInCluster() {
		return t.createInClusterGitServer(!t.IsPorchServerInCluster())
	} else {
		return createLocalGitServer(t.T())
	}
}

func (t *TestSuite) createInClusterGitServer(exposeByLoadBalancer bool) GitConfig {
	// Determine git-server image name. Use the same container registry and tag as the Porch server,
	// replacing base image name with `git-server`. TODO: Make configurable?

	var porch appsv1.Deployment
	t.GetF(client.ObjectKey{
		Namespace: "porch-system",
		Name:      "function-runner",
	}, &porch)

	gitImage := InferGitServerImage(porch.Spec.Template.Spec.Containers[0].Image)

	var deploymentKey = client.ObjectKey{
		Namespace: t.Namespace,
		Name:      "git-server",
	}
	var replicas int32 = 1
	var selector = map[string]string{
		"git-server": strings.ReplaceAll(t.Name(), "/", "_"),
	}
	t.CreateF(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentKey.Name,
			Namespace: deploymentKey.Namespace,
			Annotations: map[string]string{
				"kpt.dev/porch-test": t.Name(),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: selector,
			},
			Template: coreapi.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector,
				},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							Name:  "git-server",
							Image: gitImage,
							Args:  []string{},
							Ports: []coreapi.ContainerPort{
								{
									ContainerPort: 8080,
									Protocol:      coreapi.ProtocolTCP,
								},
							},
							ImagePullPolicy: coreapi.PullIfNotPresent,
						},
					},
				},
			},
		},
	})

	t.Cleanup(func() {
		t.DeleteE(&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deploymentKey.Name,
				Namespace: deploymentKey.Namespace,
			},
		})
	})

	t.Cleanup(func() {
		t.DumpLogsForDeploymentE(deploymentKey)
	})

	serviceKey := client.ObjectKey{
		Namespace: t.Namespace,
		Name:      "git-server-service",
	}
	service := coreapi.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceKey.Name,
			Namespace: serviceKey.Namespace,
			Annotations: map[string]string{
				"kpt.dev/porch-test": t.Name(),
			},
		},
		Spec: coreapi.ServiceSpec{
			Ports: []coreapi.ServicePort{
				{
					Protocol: coreapi.ProtocolTCP,
					Port:     8080,
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: 8080,
					},
				},
			},
			Selector: selector,
		},
	}
	if exposeByLoadBalancer {
		service.Spec.Type = coreapi.ServiceTypeLoadBalancer
	}
	t.CreateF(&service)

	t.Cleanup(func() {
		t.DeleteE(&coreapi.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      serviceKey.Name,
				Namespace: serviceKey.Namespace,
			},
		})
	})

	t.Logf("Waiting for git-server to start ...")

	// Wait a minute for git server to start up.
	giveUp := time.Now().Add(time.Minute)

	for {
		time.Sleep(1 * time.Second)

		var deployment appsv1.Deployment
		t.GetF(deploymentKey, &deployment)

		ready := true
		if ready && deployment.Generation != deployment.Status.ObservedGeneration {
			t.Logf("waiting for ObservedGeneration %v to match Generation %v", deployment.Status.ObservedGeneration, deployment.Generation)
			ready = false
		}
		if ready && deployment.Status.UpdatedReplicas < deployment.Status.Replicas {
			t.Logf("waiting for UpdatedReplicas %d to match Replicas %d", deployment.Status.UpdatedReplicas, deployment.Status.Replicas)
			ready = false
		}
		if ready && deployment.Status.AvailableReplicas < deployment.Status.Replicas {
			t.Logf("waiting for AvailableReplicas %d to match Replicas %d", deployment.Status.AvailableReplicas, deployment.Status.Replicas)
			ready = false
		}

		if ready {
			ready = false // Until we've seen Available condition
			for _, condition := range deployment.Status.Conditions {
				if condition.Type == "Available" {
					ready = true
					if condition.Status != "True" {
						t.Logf("waiting for status.condition %v", condition)
						ready = false
					}
				}
			}
		}

		if ready {
			break
		}

		if time.Now().After(giveUp) {
			t.Fatalf("git server failed to start: %s", &deployment)
			return GitConfig{}
		}
	}

	t.Logf("git server is up")

	t.Logf("Waiting for git-server-service to be ready ...")

	// Check the Endpoint and Service resources for readiness
	gitUrl := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", serviceKey.Name, serviceKey.Namespace)
	giveUp = time.Now().Add(time.Minute)
	for {
		time.Sleep(1 * time.Second)
		if time.Now().After(giveUp) {
			t.Fatalf("git-server-service not ready on time")
			return GitConfig{}
		}

		var endpoint coreapi.Endpoints
		err := t.Client.Get(t.GetContext(), serviceKey, &endpoint)
		if err != nil || !endpointIsReady(&endpoint) {
			t.Logf("waiting for Endpoint to be ready: %+v", endpoint)
			continue
		}
		if exposeByLoadBalancer {
			var svc coreapi.Service
			// if svc is empty we will just continue
			_ = t.Client.Get(t.GetContext(), serviceKey, &svc)
			if len(svc.Status.LoadBalancer.Ingress) == 0 || svc.Status.LoadBalancer.Ingress[0].IP == "" {
				t.Logf("waiting for LoadBalancer to be assigned: %+v", svc)
				continue
			}
			t.Logf("LoadBalancer IP was assigned to git-server-service: %s", svc.Status.LoadBalancer.Ingress[0].IP)
			gitUrl = fmt.Sprintf("http://%s:8080", svc.Status.LoadBalancer.Ingress[0].IP)
		}

		t.Log("git-server-service is ready")
		break
	}

	return GitConfig{
		Repo:      gitUrl,
		Branch:    "main",
		Directory: "/",
	}
}

func createLocalGitServer(t *testing.T) GitConfig {
	tmp, err := os.MkdirTemp("", "porch-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory for Git repository: %v", err)
		return GitConfig{}
	}

	t.Cleanup(func() {
		if err := os.RemoveAll(tmp); err != nil {
			t.Errorf("Failed to delete Git temp directory %q: %v", tmp, err)
		}
	})

	var gitRepoOptions []gitserver.GitRepoOption
	repos := gitserver.NewDynamicRepos(tmp, gitRepoOptions)

	server, err := gitserver.NewGitServer(repos)
	if err != nil {
		t.Fatalf("Failed to start git server: %v", err)
		return GitConfig{}
	}

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	addressChannel := make(chan net.Addr)

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := server.ListenAndServe(ctx, "127.0.0.1:0", addressChannel)
		if err != nil {
			if err == http.ErrServerClosed {
				t.Log("Git server shut down successfully")
			} else {
				t.Errorf("Git server exited with error: %v", err)
			}
		}
	}()

	// Wait for server to start up
	address, ok := <-addressChannel
	if !ok {
		t.Errorf("Server failed to start")
		return GitConfig{}
	}

	return GitConfig{
		Repo:      fmt.Sprintf("http://%s", address),
		Branch:    "main",
		Directory: "/",
	}
}

func InferGitServerImage(porchImage string) string {
	slash := strings.LastIndex(porchImage, "/")
	repo := porchImage[:slash+1]
	image := porchImage[slash+1:]
	colon := strings.LastIndex(image, ":")
	tag := image[colon+1:]

	return repo + TestGitServerImage + ":" + tag
}

func endpointIsReady(endpoints *coreapi.Endpoints) bool {
	if len(endpoints.Subsets) == 0 {
		return false
	}
	for _, s := range endpoints.Subsets {
		if len(s.Addresses) == 0 {
			return false
		}
		for _, a := range s.Addresses {
			if a.IP == "" {
				return false
			}
		}
	}
	return true
}
