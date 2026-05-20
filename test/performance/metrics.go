// Copyright 2024, 2026 The kpt Authors
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
package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(porchapi.AddToScheme(scheme))
	utilruntime.Must(configapi.AddToScheme(scheme))
}

const defaultGiteaLBIP = "172.18.255.200"

var (
	giteaBaseURLOnce sync.Once
	giteaBaseURLVal  string
)

// getGiteaBaseURL returns the Gitea base URL. It prefers the GITEA_LB_IP env var,
// then attempts to discover the IP from the gitea-lb Service in the cluster,
// and falls back to the hardcoded default only if both are unavailable.
func getGiteaBaseURL() string {
	giteaBaseURLOnce.Do(func() {
		ip := os.Getenv("GITEA_LB_IP")
		if ip == "" {
			ip = discoverGiteaLBIP()
		}
		if ip == "" {
			ip = defaultGiteaLBIP
		}
		giteaBaseURLVal = "http://" + ip + ":3000"
	})
	return giteaBaseURLVal
}

// discoverGiteaLBIP attempts to read the gitea-lb Service LoadBalancer IP from the cluster.
func discoverGiteaLBIP() string {
	cfg, err := config.GetConfig()
	if err != nil {
		return ""
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return ""
	}
	svc := &corev1.Service{}
	err = c.Get(context.Background(), client.ObjectKey{Namespace: "gitea", Name: "gitea-lb"}, svc)
	if err != nil {
		return ""
	}
	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		return svc.Status.LoadBalancer.Ingress[0].IP
	}
	return ""
}

func createGiteaRepo(repoName string) error {
	giteaURL := getGiteaBaseURL() + "/api/v1/user/repos"
	payload := map[string]interface{}{
		"name":        repoName,
		"description": "Test repository for Porch metrics",
		"private":     false,
		"auto_init":   true,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %v", err)
	}

	req, err := http.NewRequest("POST", giteaURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("porch", "secret")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create repo: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to create repo, status: %d", resp.StatusCode)
	}

	return nil
}

func debugPackageStatus(t *testing.T, c client.Client, ctx context.Context, namespace, name string) {
	var pkg porchapi.PackageRevision
	err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &pkg)
	if err != nil {
		t.Logf("Error getting package: %v", err)
		return
	}

	t.Logf("\nPackage Status Details:")
	t.Logf("  Name: %s", pkg.Name)
	t.Logf("  LifecycleState: %s", pkg.Spec.Lifecycle)
	t.Logf("  WorkspaceName: %s", pkg.Spec.WorkspaceName)
	t.Logf("  Revision: %v", pkg.Spec.Revision)
	t.Logf("  Published: %v", pkg.Status.PublishedAt)
	t.Logf("  Tasks:")
	for i, task := range pkg.Spec.Tasks {
		t.Logf("    %d. Type: %s", i+1, task.Type)
		if task.Type == porchapi.TaskTypeInit && task.Init != nil {
			t.Logf("       Description: %s", task.Init.Description)
			t.Logf("       Keywords: %v", task.Init.Keywords)
		}
	}
	t.Logf("  Conditions:")
	for _, cond := range pkg.Status.Conditions {
		t.Logf("    - Type: %s", cond.Type)
		t.Logf("      Status: %s", cond.Status)
		t.Logf("      Message: %s", cond.Message)
		t.Logf("      Reason: %s", cond.Reason)
	}
}

// ... existing imports and code ...

func deleteGiteaRepo(repoName string) error {
	giteaURL := fmt.Sprintf("%s/api/v1/repos/porch/%s", getGiteaBaseURL(), repoName)

	req, err := http.NewRequest("DELETE", giteaURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create delete request: %v", err)
	}

	req.SetBasicAuth("porch", "secret")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete repo: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("failed to delete repo, status: %d", resp.StatusCode)
	}

	return nil
}
func waitForPorchRepository(ctx context.Context, c client.Client, t *testing.T, namespace, name string, timeout time.Duration) error {
	start := time.Now()
	for {
		if time.Since(start) > timeout {
			return fmt.Errorf("timeout waiting for repository to be ready")
		}

		var repo configapi.Repository
		err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &repo)
		if err != nil {
			return err
		}

		t.Logf("\nRepository conditions at %v:", time.Since(start))
		t.Logf("Spec: %+v", repo.Spec)
		t.Logf("Status: %+v", repo.Status)

		ready := false
		for _, cond := range repo.Status.Conditions {
			t.Logf("  - Type: %s, Status: %s, Message: %s",
				cond.Type, cond.Status, cond.Message)
			if cond.Type == "Ready" && cond.Status == "True" {
				ready = true
				break
			}
		}

		if ready {
			return nil
		}

		time.Sleep(2 * time.Second)
	}
}
