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
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	numRepos    = flag.Int("repos", 1, "Number of repositories to create")
	numPackages = flag.Int("packages", 5, "Number of packages per repository")
)

func createAndSetupRepo(t *testing.T, ctx context.Context, c client.Client, namespace, repoName string) []OperationMetrics {
	var metrics []OperationMetrics
	start := time.Now()

	// Create Gitea repo
	err := createGiteaRepo(repoName)
	duration := time.Since(start).Seconds()
	recordMetric("Create Gitea Repository", repoName, "", duration, err)
	metrics = append(metrics, OperationMetrics{
		Operation: "Create Gitea Repository",
		Duration:  time.Duration(duration * float64(time.Second)),
		Error:     err,
	})

	if err != nil {
		t.Logf("Warning: Failed to create Gitea repository: %v", err)
		return metrics
	}

	// Create Porch repo
	start = time.Now()
	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: namespace,
		},
		Spec: configapi.RepositorySpec{
			Type: "git",
			Git: &configapi.GitRepository{
				Repo:   fmt.Sprintf("%s/porch/%s", getGiteaBaseURL(), repoName),
				Branch: "main",
				SecretRef: configapi.SecretRef{
					Name: "gitea",
				},
				CreateBranch: true,
			},
		},
	}

	err = c.Create(ctx, repo)
	duration = time.Since(start).Seconds()
	recordMetric("Create Porch Repository", repoName, "", duration, err)
	metrics = append(metrics, OperationMetrics{
		Operation: "Create Porch Repository",
		Duration:  time.Duration(duration * float64(time.Second)),
		Error:     err,
	})

	if err == nil {
		repositoryCounter.Inc()
		start = time.Now()
		err = waitForPorchRepository(ctx, c, t, namespace, repoName, 60*time.Second)
		duration = time.Since(start).Seconds()
		recordMetric("Wait Repository Ready", repoName, "", duration, err)
		metrics = append(metrics, OperationMetrics{
			Operation: "Wait for Porch Repository Ready",
			Duration:  time.Duration(duration * float64(time.Second)),
			Error:     err,
		})
	}

	return metrics
}

func createAndTestPackage(t *testing.T, ctx context.Context, c client.Client, namespace, repoName, pkgName string) []OperationMetrics {
	var metrics []OperationMetrics
	start := time.Now()

	// Create new package
	newPkg := &porchapi.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchapi.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", repoName),
			Namespace:    namespace,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    pkgName,
			WorkspaceName:  "main",
			RepositoryName: repoName,
			Lifecycle:      porchapi.PackageRevisionLifecycleDraft,
			Tasks: []porchapi.Task{
				{
					Type: porchapi.TaskTypeInit,
					Init: &porchapi.PackageInitTaskSpec{
						Description: "Test package for Porch metrics",
						Keywords:    []string{"test", "metrics"},
						Site:        "https://kpt.dev",
					},
				},
			},
		},
	}

	err := c.Create(ctx, newPkg)
	duration := time.Since(start).Seconds()
	recordMetric("Create PackageRevision", repoName, pkgName, duration, err)
	metrics = append(metrics, OperationMetrics{
		Operation: "Create PackageRevision",
		Duration:  time.Duration(duration * float64(time.Second)),
		Error:     err,
	})

	if err == nil {
		packageCounter.Inc()
	}

	// Wait for package to initialize
	time.Sleep(5 * time.Second)
	debugPackageStatus(t, c, ctx, namespace, newPkg.Name)

	// First get the package
	var pkg porchapi.PackageRevision
	err = c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: newPkg.Name}, &pkg)
	if err == nil {
		// Start timing only the update operation
		start = time.Now()
		pkg.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
		err = c.Update(ctx, &pkg)
		duration = time.Since(start).Seconds()
		recordMetric("Update to Proposed", repoName, pkgName, duration, err)
		metrics = append(metrics, OperationMetrics{
			Operation: "Update to Proposed",
			Duration:  time.Duration(duration * float64(time.Second)),
			Error:     err,
		})

		if err == nil {
			// Wait for proposed state to settle
			time.Sleep(5 * time.Second)
			debugPackageStatus(t, c, ctx, namespace, pkg.Name)

			// Publish the package with approval
			start = time.Now()
			pkg.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
			err = c.SubResource("approval").Update(ctx, &pkg)
			duration = time.Since(start).Seconds()
			recordMetric("Update to Published", repoName, pkgName, duration, err)
			metrics = append(metrics, OperationMetrics{
				Operation: "Update to Published",
				Duration:  time.Duration(duration * float64(time.Second)),
				Error:     err,
			})

			if err == nil {
				// Verify final state
				time.Sleep(5 * time.Second)
				debugPackageStatus(t, c, ctx, namespace, pkg.Name)
			}
		}
	}

	// Delete package
	err = c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: pkg.Name}, &pkg)
	if err == nil {
		start = time.Now()
		pkg.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
		err = c.SubResource("approval").Update(ctx, &pkg)
		if err == nil {
			time.Sleep(2 * time.Second)
			err = c.Delete(ctx, &pkg)
		}
		duration = time.Since(start).Seconds()
		recordMetric("Delete PackageRevision", repoName, pkgName, duration, err)
		metrics = append(metrics, OperationMetrics{
			Operation: "Delete PackageRevision",
			Duration:  time.Duration(duration * float64(time.Second)),
			Error:     err,
		})
	}

	return metrics
}

func setupMonitoring(t *testing.T) error {
	// Create prometheus.yml
	promConfig := `
global:
  scrape_interval: 1s
  evaluation_interval: 1s

scrape_configs:
  - job_name: 'porch_metrics'
    static_configs:
      - targets: ['host.docker.internal:2113']
    scrape_interval: 1s
`
	if err := os.WriteFile("prometheus.yml", []byte(promConfig), 0644); err != nil {
		return fmt.Errorf("failed to create prometheus.yml: %v", err)
	}

	// Execute Docker commands
	cmds := []struct {
		name string
		cmd  string
		args []string
	}{
		{"network create", "docker", []string{"network", "create", "monitoring"}},
		{"stop prometheus", "docker", []string{"stop", "prometheus"}},
		{"remove prometheus", "docker", []string{"rm", "prometheus"}},
		{"run prometheus", "docker", []string{
			"run", "-d",
			"--name", "prometheus",
			"--network", "monitoring",
			"--add-host", "host.docker.internal:host-gateway",
			"-p", "9090:9090",
			"-v", fmt.Sprintf("%s/prometheus.yml:/etc/prometheus/prometheus.yml", getCurrentDir()),
			"prom/prometheus",
		}},
	}

	for _, cmd := range cmds {
		if err := exec.Command(cmd.cmd, cmd.args...).Run(); err != nil {
			t.Logf("Warning executing %s: %v", cmd.name, err)
		}
	}

	// Give Prometheus a moment to start up
	time.Sleep(2 * time.Second)

	return nil
}

func getCurrentDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

func cleanup(t *testing.T) {
	cmds := []struct {
		cmd  string
		args []string
	}{
		{"docker", []string{"stop", "prometheus"}},
		{"docker", []string{"rm", "prometheus"}},
		{"docker", []string{"network", "rm", "monitoring"}},
	}

	for _, cmd := range cmds {
		if err := exec.Command(cmd.cmd, cmd.args...).Run(); err != nil {
			t.Logf("Warning during cleanup: %v", err)
		}
	}

	os.Remove("prometheus.yml")
}

func setupGiteaSecret(t *testing.T) error {
	// Get the current file's directory
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("failed to get current file path")
	}
	dir := filepath.Dir(filename)

	// Read and apply the secret manifest
	secretManifest, err := os.ReadFile(filepath.Join(dir, "gitea-secret.yaml"))
	if err != nil {
		return fmt.Errorf("failed to read gitea secret manifest: %v", err)
	}

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = bytes.NewReader(secretManifest)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply gitea secret: %v\nOutput: %s", err, output)
	}

	return nil
}

func TestPorchScalePerformance(t *testing.T) {
	// Skip if not running E2E tests
	if os.Getenv("E2E") == "" {
		t.Skip("Skipping performance tests in non-E2E environment")
	}

	// Check if docker is available
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker not available, skipping performance tests")
	}

	// Setup Gitea secret
	if err := setupGiteaSecret(t); err != nil {
		t.Fatalf("Failed to setup Gitea secret: %v", err)
	}

	// Setup monitoring
	if err := setupMonitoring(t); err != nil {
		t.Fatalf("Failed to setup monitoring: %v", err)
	}
	defer cleanup(t)

	// Create a channel to handle interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start metrics server
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(":2113", nil); err != nil {
			t.Logf("Error starting metrics server: %v", err)
		}
	}()

	// Setup logger
	logger, err := NewTestLogger(t)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	flag.Parse()

	t.Logf("\nRunning test with %d repositories and %d packages per repository", *numRepos, *numPackages)

	// Setup clients
	cfg, err := config.GetConfig()
	if err != nil {
		t.Fatalf("Failed to get config: %v", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	namespace := "porch-demo"
	var allMetrics []TestMetrics

	// Test multiple repositories
	for i := 0; i < *numRepos; i++ {
		repoName := fmt.Sprintf("porch-metrics-test-%d", i)
		t.Logf("\n=== Testing Repository %d: %s ===", i+1, repoName)

		// Cleanup any existing resources first
		_ = deleteGiteaRepo(repoName)
		_ = c.Delete(ctx, &configapi.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      repoName,
				Namespace: namespace,
			},
		})
		time.Sleep(5 * time.Second) // Wait for cleanup

		repoMetrics := createAndSetupRepo(t, ctx, c, namespace, repoName)
		for _, m := range repoMetrics {
			recordMetric(m.Operation, repoName, "", m.Duration.Seconds(), m.Error)
		}
		printIterationResults(t, logger, i*(*numPackages), repoMetrics)

		// Test multiple packages per repository
		for j := 0; j < *numPackages; j++ {
			pkgName := fmt.Sprintf("test-package-%d", j)
			t.Logf("\n--- Testing Package %d: %s ---", j+1, pkgName)

			pkgMetrics := createAndTestPackage(t, ctx, c, namespace, repoName, pkgName)
			for _, m := range pkgMetrics {
				recordMetric(m.Operation, repoName, pkgName, m.Duration.Seconds(), m.Error)
			}
			printIterationResults(t, logger, (i*(*numPackages))+j+1, pkgMetrics)

			allMetrics = append(allMetrics, TestMetrics{
				RepoName: repoName,
				PkgName:  pkgName,
				Metrics:  append(repoMetrics, pkgMetrics...),
			})
		}

		// Cleanup repository
		start := time.Now()
		err := c.Delete(ctx, &configapi.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      repoName,
				Namespace: namespace,
			},
		})
		cleanupMetrics := []OperationMetrics{{
			Operation: "Delete Repository",
			Duration:  time.Since(start),
			Error:     err,
		}}
		printIterationResults(t, logger, (i+1)*(*numPackages), cleanupMetrics)
	}

	// Print consolidated results
	printTestResults(t, logger, allMetrics)

	// After all tests complete, print message and wait for interrupt
	t.Log("\nTests completed. Prometheus server is running at http://localhost:9090")
	t.Log("Press Ctrl+C to stop and cleanup...")

	// Wait for interrupt signal
	<-sigChan
	t.Log("\nReceived interrupt signal. Cleaning up...")
}

func printIterationResults(t *testing.T, logger *TestLogger, iteration int, metrics []OperationMetrics) {
	// Console output
	t.Logf("\n=== Iteration %d Results ===", iteration)
	t.Log("Operation                  Duration    Status")
	t.Log("--------------------------------------------------")

	// File output
	logger.LogResult("\n=== Iteration %d Results ===", iteration)
	logger.LogResult("Operation                  Duration    Status")
	logger.LogResult("--------------------------------------------------")

	for _, m := range metrics {
		status := "Success"
		if m.Error != nil {
			status = "Failed: " + m.Error.Error()
		}
		result := fmt.Sprintf("%-25s %-10v %s",
			m.Operation,
			m.Duration.Round(time.Millisecond),
			status)

		t.Log(result)
		logger.LogResult("%s", result)
	}
}

func printTestResults(t *testing.T, logger *TestLogger, allMetrics []TestMetrics) {
	header := "\n=== Consolidated Performance Test Results ==="
	t.Log(header)
	logger.LogResult("%s", header)

	subheader := "Operation                  Min         Max         Avg         Total"
	t.Log(subheader)
	logger.LogResult("%s", subheader)

	divider := "------------------------------------------------------------------------"
	t.Log(divider)
	logger.LogResult("%s", divider)

	stats := make(map[string]Stats)

	for _, m := range allMetrics {
		for _, metric := range m.Metrics {
			if metric.Error != nil {
				continue
			}
			s := stats[metric.Operation]
			if s.Count == 0 || metric.Duration < s.Min {
				s.Min = metric.Duration
			}
			if metric.Duration > s.Max {
				s.Max = metric.Duration
			}
			s.Total += metric.Duration
			s.Count++
			stats[metric.Operation] = s
		}
	}

	for op, stat := range stats {
		avg := stat.Total / time.Duration(stat.Count)
		result := fmt.Sprintf("%-25s %-11v %-11v %-11v %-11v",
			op,
			stat.Min.Round(time.Millisecond),
			stat.Max.Round(time.Millisecond),
			avg.Round(time.Millisecond),
			stat.Total.Round(time.Millisecond))

		t.Log(result)
		logger.LogResult("%s", result)
	}

	// Print errors if any
	hasErrors := false
	for _, m := range allMetrics {
		for _, metric := range m.Metrics {
			if metric.Error != nil {
				if !hasErrors {
					errHeader := "\n=== Errors Encountered ==="
					t.Log(errHeader)
					logger.LogResult("%s", errHeader)
					hasErrors = true
				}
				errMsg := fmt.Sprintf("Repository: %s, Package: %s, Operation: %s, Error: %v",
					m.RepoName, m.PkgName, metric.Operation, metric.Error)
				t.Log(errMsg)
				logger.LogResult("%s", errMsg)
			}
		}
	}
}

func TestMain(m *testing.M) {
	// Try to load kube config from standard locations
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
	}

	if _, err := os.Stat(kubeconfig); err == nil {
		os.Setenv("KUBERNETES_MASTER", "http://localhost:8080")
	}

	os.Exit(m.Run())
}
