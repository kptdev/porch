// Copyright 2025 The kpt Authors
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
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	GiteaClusterURL        = "http://gitea.gitea.svc.cluster.local:3000/porch/"
	GiteaUser              = "porch"
	GiteaPassword          = "secret"
	PorchTestRepoName      = "porch-test"
	TestBlueprintsRepoName = "test-blueprints"
	GiteaRepoAPi           = "http://localhost:3000/api/v1/repos/porch/" + PorchTestRepoName

	defaultGiteaLBIP = "172.18.255.200"
)

// getGiteaLBIP returns the Gitea LoadBalancer IP, preferring the GITEA_LB_IP env var.
// If not set, it polls the gitea-lb Service until the LoadBalancer IP is allocated
// (with a timeout). Falls back to the hardcoded default only if discovery times out.
func (t *TestSuite) getGiteaLBIP() string {
	if ip := os.Getenv("GITEA_LB_IP"); ip != "" {
		return ip
	}

	// Poll the service for up to 30 seconds waiting for MetalLB to assign an IP
	if t.Client != nil {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			svc := &corev1.Service{}
			err := t.Client.Get(context.Background(), client.ObjectKey{
				Namespace: "gitea",
				Name:      "gitea-lb",
			}, svc)
			if err == nil && len(svc.Status.LoadBalancer.Ingress) > 0 {
				if ip := svc.Status.LoadBalancer.Ingress[0].IP; ip != "" {
					return ip
				}
			}
			time.Sleep(1 * time.Second)
		}
	}

	return defaultGiteaLBIP
}

// getGiteaURL returns the appropriate Gitea URL based on whether Porch server and controller are running in cluster
func (t *TestSuite) getGiteaURL() string {
	// Both porch-server and controller need to reach Gitea
	// Use cluster URL only if BOTH are in-cluster
	if t.IsPorchServerInCluster() && t.IsRepoControllerInCluster() {
		return GiteaClusterURL
	}
	return "http://" + t.getGiteaLBIP() + ":3000/porch/"
}

// GetPorchTestRepoURL returns the dynamic PorchTestRepo URL
func (t *TestSuite) GetPorchTestRepoURL() string {
	return t.getGiteaURL() + PorchTestRepoName + ".git"
}

// GetTestBlueprintsRepoURL returns the dynamic TestBlueprintsRepo URL
func (t *TestSuite) GetTestBlueprintsRepoURL() string {
	return t.getGiteaURL() + TestBlueprintsRepoName + ".git"
}

// IsPorchTestRepo checks if a repository URL is specifically the porch-test repository
func IsPorchTestRepo(repo string) bool {
	return strings.Contains(repo, "porch-test")
}

// RecreateGiteaRepo recreates a Gitea repository to its initial state
func RecreateGiteaRepo(t *testing.T, repoName string) {
	t.Helper()

	// Skip cleanup only if test failed and KEEP_GITEA_ON_FAILURE is set in local development
	if t.Failed() && os.Getenv("KEEP_GITEA_ON_FAILURE") == "true" && os.Getenv("CI") == "" {
		t.Logf("Skipping gitea cleanup due to test failure (KEEP_GITEA_ON_FAILURE=true, local dev)")
		return
	}

	t.Logf("recreating gitea %s repository to initial state", repoName)

	// Delete the repository
	apiURL := "http://localhost:3000/api/v1/repos/" + GiteaUser + "/" + repoName
	req, _ := http.NewRequest("DELETE", apiURL, nil)
	req.SetBasicAuth(GiteaUser, GiteaPassword)
	if _, err := http.DefaultClient.Do(req); err != nil {
		t.Fatalf("Failed to delete gitea %s repository: %v", repoName, err)
	}

	// Recreate the repository
	body := `{"name": "` + repoName + `", "auto_init": true, "readme": "Default"}`
	req, _ = http.NewRequest("POST", "http://localhost:3000/api/v1/user/repos", strings.NewReader(body))
	req.SetBasicAuth(GiteaUser, GiteaPassword)
	req.Header.Set("Content-Type", "application/json")
	if _, err := http.DefaultClient.Do(req); err != nil {
		t.Fatalf("Failed to recreate gitea %s repository: %v", repoName, err)
	}
	t.Logf("Successfully recreated gitea %s repository", repoName)
}

// RecreateGiteaTestRepo recreates the porch-test repository to its initial state
func (t *TestSuite) RecreateGiteaTestRepo() {
	RecreateGiteaRepo(t.T(), PorchTestRepoName)
}
