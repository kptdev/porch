// Copyright 2022-2026 The kpt Authors
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

package e2e

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func IsPorchServerRunningInCluster(t *testing.T) bool {
	cmd := exec.Command("kubectl", "get", "--namespace=porch-system", "service", "api",
		"--output=jsonpath={.spec.selector}")

	var stderr bytes.Buffer
	var stdout bytes.Buffer

	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil || stderr.String() != "" {
		t.Fatalf("Error when getting porch api Service: %v: %s", err, stderr.String())
	}
	return stdout.String() != ""
}

func IsRepoControllerRunningInCluster(t *testing.T) bool {
	cmd := exec.Command("kubectl", "get", "--namespace=porch-system", "deployment", "porch-controllers",
		"--output=jsonpath={.spec.replicas}")

	var stderr bytes.Buffer
	var stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "NotFound") {
			return false
		}
		t.Fatalf("Error when getting porch-controllers Deployment: %v: %s", err, stderr.String())
	}
	return stdout.String() != "" && stdout.String() != "0"
}

func KubectlWaitForLoadBalancerIp(t *testing.T, namespace, name string) string {
	args := []string{"get", "service", "--namespace", namespace, name, "--output=jsonpath={.status.loadBalancer.ingress[0].ip}"}

	giveUp := time.Now().Add(1 * time.Minute)
	for {
		cmd := exec.Command("kubectl", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		ip := stdout.String()
		if err == nil && len(ip) > 0 { // LoadBalancer assigned an external IP
			t.Logf("LoadBalancer external IP: %s", ip)
			return ip
		}

		if time.Now().After(giveUp) {
			var msg string
			if err != nil {
				msg = err.Error()
			}
			t.Fatalf("LoadBalancer service %s/%s hasn't been assigned an external IP on time. Giving up: %s", namespace, name, msg)
		}

		time.Sleep(5 * time.Second)
	}
}

func KubectlWaitForRepoReady(t *testing.T, repoName, namespace string) {
	t.Logf("waiting for repo %s/%s to become Ready", namespace, repoName)
	args := []string{"get", "repository", repoName, "--namespace", namespace, "--output=jsonpath={.status.conditions[?(@.type=='Ready')].status}"}
	giveUp := time.Now().Add(2 * time.Minute)
	syncTriggered := false
	for {
		cmd := exec.Command("kubectl", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		t.Logf("running command %v", strings.Join(cmd.Args, " "))
		err := cmd.Run()
		ready := stdout.String()
		if err == nil && string(ready) == "True" {
			t.Logf("Repo %s/%s is Ready", namespace, repoName)
			return
		}
		if time.Now().After(giveUp) {
			var msg string
			if err != nil {
				msg = err.Error()
			}
			t.Fatalf("Repo %s/%s has not become Ready. Giving up: %s", namespace, repoName, msg)
		}
		// If not ready after 30s, force a sync to handle missed watch events during controller startup
		if !syncTriggered && time.Now().After(giveUp.Add(-90*time.Second)) {
			t.Logf("Triggering forced sync for repo %s/%s", namespace, repoName)
			KubectlTriggerRepoSync(t, repoName, namespace)
			syncTriggered = true
		}
		time.Sleep(2 * time.Second)
	}
}

// KubectlTriggerRepoSync annotates a repository to force the controller to reconcile it.
func KubectlTriggerRepoSync(t *testing.T, repoName, namespace string) {
	annotation := "config.porch.kpt.dev/run-once-at=" + time.Now().UTC().Format(time.RFC3339)
	cmd := exec.Command("kubectl", "annotate", "repository", repoName, "--namespace", namespace, annotation, "--overwrite")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Failed to trigger repo sync for %s/%s: %v\n%s", namespace, repoName, err, string(out))
	} else {
		t.Logf("Triggered repo sync for %s/%s", namespace, repoName)
	}
}

func KubectlCreateNamespace(t *testing.T, name string) {
	cmd := exec.Command("kubectl", "create", "namespace", name)
	t.Logf("running command %v", strings.Join(cmd.Args, " "))
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "AlreadyExists") {
		t.Fatalf("Failed to create namespace %q: %v\n%s", name, err, string(out))
	}
	t.Logf("output: %v", string(out))
}

func KubectlDeleteNamespace(t *testing.T, name string) {
	//Removing Finalizers from PackageRevs in the test NameSpace to avoid locking when deleting
	RemovePackagerevFinalizers(t, name)
	cmd := exec.Command("kubectl", "delete", "namespace", name, "--wait=true", "--timeout=2m")
	t.Logf("running command %v", strings.Join(cmd.Args, " "))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Failed to delete namespace %q: %v\n%s", name, err, string(out))
	}
	t.Logf("output: %v", string(out))
}

func RemovePackagerevFinalizers(t *testing.T, namespace string) {
	cmd := exec.Command("kubectl", "get", "packagerevs", "--namespace", namespace, "--output=jsonpath={.items[*].metadata.name}")
	var stderr bytes.Buffer
	var stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		t.Fatalf("Error when getting packagerevs from namespace: %v: %s", err, stderr.String())
	}

	packagerevs := reallySplit(stdout.String(), " ")
	if len(packagerevs) == 0 {
		t.Log("kubectl get packagerevs didn't return any objects - continue")
		return
	}
	t.Logf("Removing Finalizers from PackageRevs: %v", packagerevs)

	for _, pkgrev := range packagerevs {
		for range 3 {
			cmd := exec.Command("kubectl", "patch", "packagerev", pkgrev, "--type", "json", "--patch=[{\"op\": \"remove\", \"path\": \"/metadata/finalizers\"}]", "--namespace", namespace)
			out, err := cmd.CombinedOutput()
			if err == nil {
				break
			}
			if strings.Contains(string(out), "Operation cannot be fulfilled") {
				t.Logf("Conflict removing finalizers from %q, retrying...", pkgrev)
				continue
			}
			if strings.Contains(string(out), "NotFound") || strings.Contains(string(out), "missing value") {
				break
			}
			t.Errorf("Failed to remove Finalizer from %q: %v\n%s", pkgrev, err, string(out))
		}
	}
}

// RemovePackageRevisionFinalizers removes finalizers from v1alpha2 PackageRevision CRDs
// (packagerevisions.porch.kpt.dev) in the given namespace to unblock namespace deletion.
// First transitions Published packages to DeletionProposed (webhook requirement),
// then removes finalizers.
func RemovePackageRevisionFinalizers(t *testing.T, namespace string) {
	cmd := exec.Command("kubectl", "get", "packagerevisions.porch.kpt.dev", "--namespace", namespace, "--output=jsonpath={.items[*].metadata.name}")
	var stderr bytes.Buffer
	var stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		// If the CRD doesn't exist or namespace is gone, nothing to clean up
		if strings.Contains(stderr.String(), "NotFound") || strings.Contains(stderr.String(), "the server doesn't have a resource type") {
			t.Log("packagerevisions CRD not found - skipping finalizer removal")
			return
		}
		t.Fatalf("Error when getting packagerevisions from namespace: %v: %s", err, stderr.String())
	}

	packageRevisions := reallySplit(stdout.String(), " ")
	if len(packageRevisions) == 0 {
		t.Log("kubectl get packagerevisions didn't return any objects - continue")
		return
	}

	t.Log("First, transitioning Published packages to DeletionProposed")
	// First pass: transition Published packages to DeletionProposed
	for _, pr := range packageRevisions {
		cmd := exec.Command("kubectl", "get", "packagerevisions.porch.kpt.dev", pr, "--namespace", namespace, "--output=jsonpath={.spec.lifecycle}")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err == nil {
			lifecycle := stdout.String()
			if lifecycle == "Published" {
				patchCmd := exec.Command("kubectl", "patch", "packagerevisions.porch.kpt.dev", pr, "--type", "merge", "--patch={\"spec\":{\"lifecycle\":\"DeletionProposed\"}}", "--namespace", namespace)
				if out, err := patchCmd.CombinedOutput(); err != nil {
					t.Logf("Warning: failed to transition %q to DeletionProposed: %v\n%s", pr, err, string(out))
				}
			}
		}
	}
	
	t.Logf("Removing Finalizers from PackageRevisions: %v", packageRevisions)
	// Second pass: remove finalizers
	for _, pr := range packageRevisions {
		for range 3 {
			cmd := exec.Command("kubectl", "patch", "packagerevisions.porch.kpt.dev", pr, "--type", "json", "--patch=[{\"op\": \"remove\", \"path\": \"/metadata/finalizers\"}]", "--namespace", namespace)
			out, err := cmd.CombinedOutput()
			if err == nil {
				break
			}
			if strings.Contains(string(out), "Operation cannot be fulfilled") {
				t.Logf("Conflict removing finalizers from %q, retrying...", pr)
				continue
			}
			if strings.Contains(string(out), "NotFound") || strings.Contains(string(out), "missing value") {
				break
			}
			t.Errorf("Failed to remove Finalizer from %q: %v\n%s", pr, err, string(out))
		}
	}
}

// KubectlDeleteNamespaceV1Alpha2 removes v1alpha2 PackageRevision finalizers then deletes the namespace.
func KubectlDeleteNamespaceV1Alpha2(t *testing.T, name string) {
	RemovePackageRevisionFinalizers(t, name)
	cmd := exec.Command("kubectl", "delete", "namespace", name, "--wait=true", "--timeout=2m")
	t.Logf("running command %v", strings.Join(cmd.Args, " "))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Failed to delete namespace %q: %v\n%s", name, err, string(out))
	}
	t.Logf("output: %v", string(out))
}

// KubectlWaitForPackageRevisionReady polls a v1alpha2 PackageRevision until its Ready condition is True
// with observedGeneration matching the current generation, and its PackageRevisionResources are visible.
func KubectlWaitForPackageRevisionReady(t *testing.T, name, namespace string) {
	t.Logf("waiting for packagerevision %s/%s to become Ready", namespace, name)
	args := []string{"get", "packagerevisions.porch.kpt.dev", name, "--namespace", namespace,
		"--output=jsonpath={.metadata.generation},{.status.conditions[?(@.type=='Ready')].status},{.status.conditions[?(@.type=='Ready')].observedGeneration}"}
	giveUp := time.Now().Add(2 * time.Minute)
	for {
		cmd := exec.Command("kubectl", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		output := stdout.String()
		// Expect "<gen>,True,<observedGen>" where observedGen >= gen
		if err == nil {
			parts := strings.SplitN(output, ",", 3)
			if len(parts) == 3 && parts[1] == "True" && parts[0] != "" && parts[2] != "" {
				gen, genErr := strconv.ParseInt(parts[0], 10, 64)
				observedGen, obsErr := strconv.ParseInt(parts[2], 10, 64)
				if genErr == nil && obsErr == nil && observedGen >= gen {
					t.Logf("PackageRevision %s/%s is Ready (generation=%d, observedGeneration=%d)", namespace, name, gen, observedGen)
					break
				}
			}
		}
		if time.Now().After(giveUp) {
			var msg string
			if err != nil {
				msg = err.Error()
			}
			t.Fatalf("PackageRevision %s/%s has not become Ready. Giving up: %s (output: %s, stderr: %s)", namespace, name, msg, output, stderr.String())
		}
		time.Sleep(2 * time.Second)
	}

	// Wait for PRR to be visible (aggregated API may lag behind CRD status)
	t.Logf("waiting for packagerevisionresources %s/%s to be visible", namespace, name)
	prrArgs := []string{"get", "packagerevisionresources.porch.kpt.dev", name, "--namespace", namespace}
	for {
		cmd := exec.Command("kubectl", prrArgs...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			t.Logf("PackageRevisionResources %s/%s is visible", namespace, name)
			return
		}
		if time.Now().After(giveUp) {
			t.Fatalf("PackageRevisionResources %s/%s not visible. Giving up: %s", namespace, name, stderr.String())
		}
		time.Sleep(2 * time.Second)
	}
}

// KubectlWaitForPackageRevisionPublished polls a v1alpha2 PackageRevision until it is Published
// with a non-zero status.revision. This is needed when downstream operations depend on the
// revision number being set (e.g. upgrade resolution).
func KubectlWaitForPackageRevisionPublished(t *testing.T, name, namespace string) {
	t.Logf("waiting for packagerevision %s/%s to be Published with revision set", namespace, name)
	args := []string{"get", "packagerevisions.porch.kpt.dev", name, "--namespace", namespace,
		"--output=jsonpath={.spec.lifecycle},{.status.revision}"}
	giveUp := time.Now().Add(2 * time.Minute)
	for {
		cmd := exec.Command("kubectl", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		output := stdout.String()
		// Expect "Published,<N>" where N > 0
		if err == nil && strings.HasPrefix(output, "Published,") {
			parts := strings.SplitN(output, ",", 2)
			if len(parts) == 2 && parts[1] != "" {
				rev, parseErr := strconv.ParseInt(parts[1], 10, 64)
				if parseErr == nil && rev > 0 {
					t.Logf("PackageRevision %s/%s is Published with revision=%d", namespace, name, rev)
					return
				}
			}
		}
		if time.Now().After(giveUp) {
			var msg string
			if err != nil {
				msg = err.Error()
			}
			t.Fatalf("PackageRevision %s/%s has not become Published with revision. Giving up: %s (output: %s, stderr: %s)", namespace, name, msg, output, stderr.String())
		}
		time.Sleep(2 * time.Second)
	}
}

// KubectlWaitForPackageRevisionRendered polls a v1alpha2 PackageRevision until its Rendered condition is True.
// This is needed when pushing content with pipelines to ensure async render completes before propose/approve.
func KubectlWaitForPackageRevisionRendered(t *testing.T, name, namespace string) {
	t.Logf("waiting for packagerevision %s/%s to have Rendered=True", namespace, name)
	args := []string{"get", "packagerevisions.porch.kpt.dev", name, "--namespace", namespace,
		"--output=jsonpath={.status.conditions[?(@.type=='Rendered')].status}"}
	giveUp := time.Now().Add(2 * time.Minute)
	for {
		cmd := exec.Command("kubectl", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		output := strings.TrimSpace(stdout.String())
		if err == nil && output == "True" {
			t.Logf("PackageRevision %s/%s has Rendered=True", namespace, name)
			return
		}
		if time.Now().After(giveUp) {
			var msg string
			if err != nil {
				msg = err.Error()
			}
			t.Fatalf("PackageRevision %s/%s has not become Rendered. Giving up: %s (output: %s, stderr: %s)", namespace, name, msg, output, stderr.String())
		}
		time.Sleep(2 * time.Second)
	}
}

func reallySplit(s, sep string) []string {
	if len(s) == 0 {
		return []string{}
	}
	return strings.Split(s, sep)
}
