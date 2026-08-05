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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
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

// GetGiteaURL returns the appropriate Gitea URL based on whether Porch server is running in cluster
func (t *TestSuite) GetGiteaURL() string {
	if t.IsPorchServerInCluster() {
		return t.GiteaUrl + "/" + t.GiteaUser + "/"
	}
	return "http://localhost:3000/porch/"
}

func (t *TestSuite) GetGiteaApiURL() string {
	if t.GiteaUrl == GiteaClusterURL {
		return "http://localhost:3000"
	}
	return t.GiteaUrl
}

func DraftGitBranchName(packageName, workspaceName string) string {
	return fmt.Sprintf("drafts/%s/%s", packageName, workspaceName)
}

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

func (t *TestSuite) CreateGiteaRepo(repoName string) string {
	t.T().Helper()
	repoURL := t.CreateGiteaRepoNoCleanup(repoName)
	t.Cleanup(func() {
		t.DeleteGiteaRepo(repoName)
	})
	return repoURL
}

func (t *TestSuite) CreateGiteaRepoNoCleanup(repoName string) string {
	t.T().Helper()

	body := fmt.Sprintf(`{"name":%q,"auto_init":true,"readme":"Default"}`, repoName)
	req, _ := http.NewRequest("POST", t.GetGiteaApiURL()+"/api/v1/user/repos", strings.NewReader(body))
	req.SetBasicAuth(t.GiteaUser, t.GiteaPassword)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("CreateGiteaRepoNoCleanup: request failed for %q: %v", repoName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("CreateGiteaRepoNoCleanup: unexpected status %d creating repo %q", resp.StatusCode, repoName)
	}
	t.Logf("CreateGiteaRepoNoCleanup: created repo %q", repoName)

	return t.GetGiteaURL() + repoName + ".git"
}

// DeleteGiteaRepo deletes a Gitea repository owned by t.GiteaUser.
func (t *TestSuite) DeleteGiteaRepo(repoName string) {
	t.T().Helper()

	req, _ := http.NewRequest("DELETE",
		t.GetGiteaApiURL()+"/api/v1/repos/"+t.GiteaUser+"/"+repoName, nil)
	req.SetBasicAuth(t.GiteaUser, t.GiteaPassword)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("DeleteGiteaRepo: request failed for %q: %v", repoName, err)
		return
	}
	defer resp.Body.Close()

	t.Logf("DeleteGiteaRepo: deleted repo %q (status %d)", repoName, resp.StatusCode)
}

// BuildGitRepoObject returns a new configapi.Repository pointing at repoURL using the given secret.
// This is useful when managing two registrations of the same repo within a single test.
func (t *TestSuite) BuildGitRepoObject(repoName, repoURL, secretName string) client.Object {
	return &configapi.Repository{
		TypeMeta: metav1.TypeMeta{
			Kind:       configapi.TypeRepository.Kind,
			APIVersion: configapi.GroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: t.Namespace,
		},
		Spec: configapi.RepositorySpec{
			Description: "Porch Test Repository",
			Type:        configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				Repo:   repoURL,
				Branch: "main",
				SecretRef: configapi.SecretRef{
					Name: secretName,
				},
			},
		},
	}
}

// GiteaCommitFileToBranch creates a new file on the given branch via the Gitea Contents API.
func (t *TestSuite) GiteaCommitFileToBranch(repoName, branchName, filePath, fileContent, commitMsg string) {
	t.T().Helper()

	encodedContent := base64.StdEncoding.EncodeToString([]byte(fileContent))
	body := fmt.Sprintf(`{"message":%q,"content":%q,"branch":%q}`, commitMsg, encodedContent, branchName)
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/contents/%s",
		t.GetGiteaApiURL(), t.GiteaUser, repoName, filePath)
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("GiteaCommitFileToBranch: failed to build request: %v", err)
	}
	req.SetBasicAuth(t.GiteaUser, t.GiteaPassword)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GiteaCommitFileToBranch: request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("GiteaCommitFileToBranch: unexpected status %d committing %q to branch %q in repo %q",
			resp.StatusCode, filePath, branchName, repoName)
	}
	t.Logf("GiteaCommitFileToBranch: committed %q to branch %q in repo %q", filePath, branchName, repoName)
}

// GiteaGetBranchLatestCommitSHA returns the latest commit SHA on the given branch.
func (t *TestSuite) GiteaGetBranchLatestCommitSHA(repoName, branchName string) string {
	t.T().Helper()

	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/branches/%s",
		t.GetGiteaApiURL(), t.GiteaUser, repoName, branchName)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("GiteaGetBranchLatestCommitSHA: failed to build request: %v", err)
	}
	req.SetBasicAuth(t.GiteaUser, t.GiteaPassword)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GiteaGetBranchLatestCommitSHA: request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GiteaGetBranchLatestCommitSHA: unexpected status %d for branch %q in repo %q",
			resp.StatusCode, branchName, repoName)
	}

	var info struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("GiteaGetBranchLatestCommitSHA: failed to decode response: %v", err)
	}
	return info.Commit.ID
}

// WaitUntilGiteaBranchExists polls until the named branch appears in the Gitea repository.
func (t *TestSuite) WaitUntilGiteaBranchExists(repoName, branchName string, timeout time.Duration) {
	t.T().Helper()

	err := wait.PollUntilContextTimeout(context.Background(), time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		return t.GiteaBranchExists(repoName, branchName), nil
	})
	if err != nil {
		t.Fatalf("WaitUntilGiteaBranchExists: branch %q in repo %q did not appear within %v",
			branchName, repoName, timeout)
	}
}

// GiteaBranchExists returns true when the named branch exists in the given Gitea
func (t *TestSuite) GiteaBranchExists(repoName, branchName string) bool {
	t.T().Helper()

	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/branches/%s",
		t.GetGiteaApiURL(), t.GiteaUser, repoName, branchName)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Logf("GiteaBranchExists: failed to build request: %v", err)
		return false
	}

	req.SetBasicAuth(t.GiteaUser, t.GiteaPassword)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("GiteaBranchExists: request failed: %v", err)
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// WaitUntilGiteaBranchHasNewCommit polls until the branch's latest commit SHA differs from
// oldCommitSHA and returns the new SHA.
func (t *TestSuite) WaitUntilGiteaBranchHasNewCommit(repoName, branchName, oldCommitSHA string, timeout time.Duration) string {
	t.T().Helper()

	var newSHA string
	err := wait.PollUntilContextTimeout(context.Background(), time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		url := fmt.Sprintf("%s/api/v1/repos/%s/%s/branches/%s",
			t.GetGiteaApiURL(), t.GiteaUser, repoName, branchName)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return false, nil
		}
		req.SetBasicAuth(t.GiteaUser, t.GiteaPassword)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false, nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false, nil
		}
		var info struct {
			Commit struct {
				ID string `json:"id"`
			} `json:"commit"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return false, nil
		}
		if info.Commit.ID != "" && info.Commit.ID != oldCommitSHA {
			newSHA = info.Commit.ID
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("WaitUntilGiteaBranchHasNewCommit: branch %q in repo %q did not get a new commit (was %q) within %v",
			branchName, repoName, oldCommitSHA, timeout)
	}
	return newSHA
}

func (t *TestSuite) GiteaRepoTagCount(repoName string) int {
	t.T().Helper()

	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/tags", t.GetGiteaApiURL(), t.GiteaUser, repoName)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Logf("GiteaRepoTagCount: failed to build request: %v", err)
		return -1
	}
	req.SetBasicAuth(t.GiteaUser, t.GiteaPassword)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("GiteaRepoTagCount: request failed: %v", err)
		return -1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Logf("GiteaRepoTagCount: unexpected status %d for repo %q", resp.StatusCode, repoName)
		return -1
	}

	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		t.Logf("GiteaRepoTagCount: failed to decode response: %v", err)
		return -1
	}

	return len(tags)
}

// SetGiteaRepoArchived archives or un-archives a Gitea repository owned by
func (t *TestSuite) SetGiteaRepoArchived(repoName string, archived bool) {
	t.T().Helper()

	archivedStr := "false"
	if archived {
		archivedStr = "true"
	}

	body := fmt.Sprintf(`{"archived":%s}`, archivedStr)
	req, _ := http.NewRequest("PATCH",
		t.GetGiteaApiURL()+"/api/v1/repos/"+t.GiteaUser+"/"+repoName,
		strings.NewReader(body))
	req.SetBasicAuth(t.GiteaUser, t.GiteaPassword)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SetGiteaRepoArchived: request failed for repo %q: %v", repoName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SetGiteaRepoArchived: unexpected status %d for repo %q archived=%v",
			resp.StatusCode, repoName, archived)
	}

	t.Logf("SetGiteaRepoArchived: repo %q archived=%v", repoName, archived)
}
