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
package metrics

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type PerformanceTests struct {
	PerfTestSuite
}

func TestPerf(t *testing.T) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
	}

	if _, err := os.Stat(kubeconfig); err == nil {
		_ = os.Setenv("KUBERNETES_MASTER", "http://localhost:8080")
	}

	suite.Run(t, &PerformanceTests{})
}

func (t *PerformanceTests) TestPorchScalePerformance() {
	if os.Getenv("LOAD_TEST") != "1" {
		t.T().Skipf("LOAD_TEST != 1: Skipping performance tests in non-load test environment")
	}

	// We never use error calculation in scale performance test
	errorCalculator := func(err error, errCount, numRevs int) bool {
		return false
	}

	testStartTime := time.Now()

	repoSemaphore := make(chan struct{}, t.testOptions.repoParallelism)
	var repoWg sync.WaitGroup

	for i := 0; i < t.testOptions.numRepos; i++ {
		if err := t.ctx.Err(); err != nil {
			break
		}
		repoSemaphore <- struct{}{}
		repoWg.Add(1)
		go func(repoIndex int) {
			defer repoWg.Done()
			defer func() { <-repoSemaphore }()
			t.processRepository(repoIndex, t.testOptions.numRevs, errorCalculator)
		}(i)
	}
	repoWg.Wait()
	lifecycleDuration := time.Since(testStartTime)
	if err := t.ctx.Err(); err != nil {
		t.T().Logf("Test interrupted: %v", err)
	}

	var deletionStartTime time.Time
	var deletionDuration time.Duration
	var deletedCount int

	if t.testOptions.enableDeletion && t.ctx.Err() == nil {
		t.deleteEnv(&deletionStartTime, &deletionDuration, &deletedCount)
	}

	t.printTestResults(t.testLogger)

	if t.testOptions.enableDeletion {
		t.T().Logf("Total duration for deletion operations: %v", deletionDuration)
		t.resultsLogger.LogToFile("Total duration for deletion operations: %v", deletionDuration)
	}
	t.logResults(lifecycleDuration, &deletedCount)
}

func (t *PerformanceTests) TestIncreasePRsPerformance() {
	maxPkgRevNum := math.MaxInt
	if os.Getenv("MAX_PR_TEST") != "1" {
		t.T().Skipf("MAX_PR_TEST != 1: Skipping performance tests in non-load test environment")
	}

	// TODO: Making more complex error calculation logic
	errorCalculator := func(err error, errCount, numRevs int) bool {
		if err != nil {
			t.T().Logf("\n--- Error Rate: %f", float64(errCount)/float64(numRevs))
			return float64(errCount)/float64(numRevs) >= t.testOptions.errorRate
		}
		return false
	}

	testStartTime := time.Now()

	repoIndex := 0

	t.processRepository(repoIndex, maxPkgRevNum, errorCalculator)

	lifecycleDuration := time.Since(testStartTime)
	if err := t.ctx.Err(); err != nil {
		t.T().Logf("Test interrupted: %v", err)
	}

	var deletionStartTime time.Time
	var deletionDuration time.Duration
	var deletedCount int

	if t.testOptions.enableDeletion && t.ctx.Err() == nil {
		t.deleteEnv(&deletionStartTime, &deletionDuration, &deletedCount)
	}

	t.printTestResults(t.testLogger)
	if t.testOptions.enableDeletion {
		t.T().Logf("Total duration for deletion operations: %v", deletionDuration)
		t.resultsLogger.LogToFile("Total duration for deletion operations: %v", deletionDuration)
	}
	t.logResults(lifecycleDuration, &deletedCount)
}

func (t *PerformanceTests) deleteEnv(deletionStartTime *time.Time, deletionDuration *time.Duration, deletedCount *int) {
	*deletionStartTime = time.Now()

	t.T().Log("\n=== Starting Deletion Operations ===")
	t.T().Logf("Deletion enabled: will delete all %d package revisions across %d repositories", t.testOptions.numRepos*t.testOptions.numPkgs*t.testOptions.numRevs, t.testOptions.numRepos)

	candidates, err := t.lifecycleDriver.ListPackageRevisionsForDeletion()
	if err != nil {
		t.T().Logf("failed to list package revisions for deletion: %v", err)
		return
	}

	t.T().Logf("found %d package revisions to delete", len(candidates))

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].RepoName != candidates[j].RepoName {
			return candidates[i].RepoName < candidates[j].RepoName
		}
		if candidates[i].PackageName != candidates[j].PackageName {
			return candidates[i].PackageName < candidates[j].PackageName
		}
		return candidates[i].RevisionNum > candidates[j].RevisionNum
	})

	*deletedCount = 0
	for _, pr := range candidates {
		if err := t.ctx.Err(); err != nil {
			t.T().Logf("Deletion interrupted: %v", err)
			break
		}

		t.T().Logf("Deleting package revision: %s (repo: %s, pkg: %s, revision: %d)",
			pr.Name, pr.RepoName, pr.PackageName, pr.RevisionNum)

		if err = t.lifecycleDriver.DeletePackageRevision(pr.RepoName, pr.PackageName, pr.Name, pr.RevisionNum); err == nil {
			proposeDel := t.metrics[pr.RepoName].pkgRevMetrics[pr.PackageName][pr.RevisionNum].Metrics[pkgRevProposeDeletion]
			del := t.metrics[pr.RepoName].pkgRevMetrics[pr.PackageName][pr.RevisionNum].Metrics[pkgRevDelete]
			t.resultsLogger.LogDeleted(pr.Name, proposeDel.Duration+del.Duration)
			*deletedCount++
		} else {
			t.T().Errorf("failed to delete package revision: %s (repo: %s, pkg: %s, revision: %d)", pr.Name, pr.RepoName, pr.PackageName, pr.RevisionNum)
		}
	}
	*deletionDuration = time.Since(*deletionStartTime)

	t.T().Logf("Completed deletion of %d package revisions", *deletedCount)
}

func (t *PerformanceTests) printTestResults(logger *TestLogger) {
	header := fmt.Sprintf("\n=== Consolidated Performance Test Results (%s) ===", t.testOptions.apiVersion)
	t.T().Log(header)
	logger.LogResult("%s", header)

	subheader := "Operation                              Min         Max         Avg         Total"
	t.T().Log(subheader)
	logger.LogResult("%s", subheader)

	divider := "------------------------------------------------------------------------------------"
	t.T().Log(divider)
	logger.LogResult("%s", divider)

	repoOperations := t.testOptions.apiVersion.RepoOperations()
	pkgRevOperations := t.testOptions.apiVersion.PkgRevOperations()
	allOperations := t.testOptions.apiVersion.AllOperations()

	operationStats := make(map[string]*Stats, len(allOperations))
	for _, op := range allOperations {
		operationStats[op] = &Stats{}
	}

	t.metricsMutex.RLock()
	for i := 0; i < t.testOptions.numRepos; i++ {
		repoName := fmt.Sprintf("%s-test-%d", t.testOptions.namespace, i)
		repoMetrics, exists := t.metrics[repoName]
		if !exists {
			continue
		}

		for key, repoOp := range repoMetrics.repoOps {
			if repoOp.Error != nil {
				continue
			}
			if stats, ok := operationStats[key]; ok {
				if stats.Count == 0 || repoOp.Duration < stats.Min {
					stats.Min = repoOp.Duration
				}
				if repoOp.Duration > stats.Max {
					stats.Max = repoOp.Duration
				}
				stats.Total += repoOp.Duration
				stats.Count++
			}
		}

		for j := 0; j < t.testOptions.numPkgs; j++ {
			pkgName := fmt.Sprintf("network-function-%d", j)
			pkgRevisions, exists := repoMetrics.pkgRevMetrics[pkgName]
			if !exists {
				continue
			}

			for k := 1; k <= t.testOptions.numRevs; k++ {
				pkgRevMetric, exists := pkgRevisions[k]
				if !exists {
					continue
				}

				for opKey, opMetric := range pkgRevMetric.Metrics {
					if opMetric.Error != nil {
						continue
					}
					if stats, ok := operationStats[opKey]; ok {
						if stats.Count == 0 || opMetric.Duration < stats.Min {
							stats.Min = opMetric.Duration
						}
						if opMetric.Duration > stats.Max {
							stats.Max = opMetric.Duration
						}
						stats.Total += opMetric.Duration
						stats.Count++
					}
				}
			}
		}
	}
	t.metricsMutex.RUnlock()

	t.metricsMutex.RLock()
	for i := 0; i < t.testOptions.numRepos; i++ {
		for _, opKey := range repoOperations {
			repoName := fmt.Sprintf("%s-test-%d", t.testOptions.namespace, i)
			repoMetrics, exists := t.metrics[repoName]
			if !exists {
				continue
			}

			repoOp, exists := repoMetrics.repoOps[opKey]
			if !exists || repoOp.Error != nil {
				continue
			}

			headingWithNum := fmt.Sprintf("%s  R%d", operationHeading(opKey), i)
			result := fmt.Sprintf("%-37s %-11v %-11v %-11v %-11v",
				headingWithNum,
				repoOp.Duration.Round(time.Millisecond),
				repoOp.Duration.Round(time.Millisecond),
				repoOp.Duration.Round(time.Millisecond),
				repoOp.Duration.Round(time.Millisecond))
			t.T().Log(result)
			logger.LogResult("%s", result)
		}
	}
	t.metricsMutex.RUnlock()

	for _, opKey := range pkgRevOperations {
		stats := operationStats[opKey]
		if stats.Count == 0 {
			continue
		}

		for k := 1; k <= t.testOptions.numRevs; k++ {
			revStats := &Stats{}
			revCount := 0

			t.metricsMutex.RLock()
			for i := 0; i < t.testOptions.numRepos; i++ {
				repoName := fmt.Sprintf("%s-test-%d", t.testOptions.namespace, i)
				repoMetrics, exists := t.metrics[repoName]
				if !exists {
					continue
				}

				for j := 0; j < t.testOptions.numPkgs; j++ {
					pkgName := fmt.Sprintf("network-function-%d", j)
					pkgRevisions, exists := repoMetrics.pkgRevMetrics[pkgName]
					if !exists {
						continue
					}

					pkgRevMetric, exists := pkgRevisions[k]
					if !exists {
						continue
					}

					opMetric, exists := pkgRevMetric.Metrics[opKey]
					if !exists || opMetric.Error != nil {
						continue
					}

					if revCount == 0 || opMetric.Duration < revStats.Min {
						revStats.Min = opMetric.Duration
					}
					if opMetric.Duration > revStats.Max {
						revStats.Max = opMetric.Duration
					}
					revStats.Total += opMetric.Duration
					revCount++
				}
			}
			t.metricsMutex.RUnlock()

			if revCount > 0 {
				avg := revStats.Total / time.Duration(revCount)
				headingWithRev := fmt.Sprintf("%s v%d", operationHeading(opKey), k)
				result := fmt.Sprintf("%-37s %-11v %-11v %-11v %-11v",
					headingWithRev,
					revStats.Min.Round(time.Millisecond),
					revStats.Max.Round(time.Millisecond),
					avg.Round(time.Millisecond),
					revStats.Total.Round(time.Millisecond))
				t.T().Log(result)
				logger.LogResult("%s", result)
			}
		}
	}

	hasErrors := false
	for i := 0; i < t.testOptions.numRepos; i++ {
		repoName := fmt.Sprintf("%s-test-%d", t.testOptions.namespace, i)
		testMetric, exists := t.metrics[repoName]
		if !exists {
			continue
		}

		for _, opMetric := range testMetric.repoOps {
			if opMetric.Error != nil {
				if !hasErrors {
					errHeader := "\n=== Errors Encountered ==="
					t.T().Log(errHeader)
					logger.LogResult("%s", errHeader)
					hasErrors = true
				}
				errMsg := fmt.Sprintf("Repository: %s, Operation: %s, Error: %v",
					repoName, opMetric.Operation, opMetric.Error)
				t.T().Log(errMsg)
				logger.LogResult("%s", errMsg)
			}
		}

		for j := 0; j < t.testOptions.numPkgs; j++ {
			pkgName := fmt.Sprintf("network-function-%d", j)
			pkgRevisions, exists := testMetric.pkgRevMetrics[pkgName]
			if !exists {
				continue
			}

			for k := 1; k <= t.testOptions.numRevs; k++ {
				pkgRevMetric, exists := pkgRevisions[k]
				if !exists {
					continue
				}

				for _, opMetric := range pkgRevMetric.Metrics {
					if opMetric.Error != nil {
						if !hasErrors {
							errHeader := "\n=== Errors Encountered ==="
							t.T().Log(errHeader)
							logger.LogResult("%s", errHeader)
							hasErrors = true
						}
						errMsg := fmt.Sprintf("Repository: %s, Package: %s, Revision: %d, Operation: %s, Error: %v",
							repoName, pkgRevMetric.pkgName, k, opMetric.Operation, opMetric.Error)
						t.T().Log(errMsg)
						logger.LogResult("%s", errMsg)
					}
				}
			}
		}
	}
}

func (t *PerformanceTests) logResults(lifecycleDuration time.Duration, deletedCount *int) {
	if err := t.testLogger.Sync(); err != nil {
		t.T().Logf("Warning: Failed to sync test logger: %v", err)
	}

	t.T().Logf("Total lifecycle duration for all operations: %v", lifecycleDuration)
	t.resultsLogger.LogToFile("Total lifecycle duration for all operations: %v", lifecycleDuration)

	t.T().Log("\nGenerating CSV results...")
	if err := t.generateCSVResults(); err != nil {
		t.T().Logf("Warning: Failed to generate CSV results: %v", err)
	} else {
		t.T().Logf("- CSV results saved to: %s", t.csvOptions.lifecycleCSV)
	}

	if err := t.generateDetailedOperationsCSV(); err != nil {
		t.T().Logf("Warning: Failed to generate detailed operations CSV: %v", err)
	} else {
		t.T().Logf("- Detailed operations CSV saved to: %s", t.csvOptions.operationsCSV)
	}

	if t.testOptions.enableDeletion && *deletedCount > 0 {
		if err := t.generateDeletionOperationsCSV(); err != nil {
			t.T().Logf("Warning: Failed to generate deletion operations CSV: %v", err)
		} else {
			t.T().Logf("- Deletion operations CSV saved to: %s", t.csvOptions.deletionCSV)
		}
	}

	t.T().Logf("- Raw results saved to: %s", t.logOptions.resultsFile)
	t.T().Logf("- Detailed log saved to: %s", t.logOptions.fullLogFile)

	if err := t.resultsLogger.Sync(); err != nil {
		t.T().Logf("Warning: Failed to sync results logger: %v", err)
	}
	if err := t.testLogger.Sync(); err != nil {
		t.T().Logf("Warning: Failed to sync test logger: %v", err)
	}

	t.T().Log("\nTests completed!")
}

func (t *PerformanceTests) processRepository(repoIndex, numRevs int, errorCalculator func(err error, errCount, numRevs int) bool) {
	if err := t.ctx.Err(); err != nil {
		return
	}

	repoName := fmt.Sprintf("%s-test-%d", t.testOptions.namespace, repoIndex)
	t.T().Logf("\n=== Creating Repository %d: %s ===", repoIndex+1, repoName)
	t.createAndSetupRepo(repoName)
	if err := t.ctx.Err(); err != nil {
		return
	}

	t.metricsMutex.RLock()
	for _, op := range t.metrics[repoName].repoOps {
		t.resultsLogger.LogToFile("%s: %s - %v (took %.3fs)", repoName, op.Operation, op.Error, op.Duration.Seconds())
	}
	t.metricsMutex.RUnlock()

	processPackage := func(pkgIndex int) {
		if err := t.ctx.Err(); err != nil {
			return
		}

		errCount := 0
		pkgName := fmt.Sprintf("network-function-%d", pkgIndex)
		t.T().Logf("\n--- Creating Package %s:%d ---", repoName, pkgIndex+1)

		t.metricsMutex.Lock()
		t.metrics[repoName].pkgRevMetrics[pkgName] = make(map[int]PackageRevisionMetrics)
		t.metricsMutex.Unlock()

		for k := 1; k <= numRevs; k++ {
			if err := t.ctx.Err(); err != nil {
				return
			}
			t.T().Logf("Creating revision %d/%d for package %s", k, t.testOptions.numRevs, pkgName)
			if pkgRevName, err := t.lifecycleDriver.DoLifecycle(repoName, pkgName, k); err == nil {
				t.metricsMutex.RLock()
				for _, op := range t.metrics[repoName].pkgRevMetrics[pkgName][k].Metrics {
					if op.Operation == fmt.Sprintf("%s:%d", pkgRevPublished, k) {
						t.resultsLogger.LogApproved(repoName, pkgName, k, pkgRevName, op.Duration)
					} else {
						t.resultsLogger.LogToFile("%s:%s:%d - %s (took %.3fs)", repoName, pkgName, k, op.Operation, op.Duration.Seconds())
					}
				}
				t.metricsMutex.RUnlock()
			} else {
				t.T().Logf("An error occured during the creation/update of the package revision %s: %s", pkgRevName, err)
				errCount++
				if errorCalculator(err, errCount, k) {
					break
				}
			}
		}
	}

	pkgSemaphore := make(chan struct{}, t.testOptions.packageParallelism)
	var pkgWg sync.WaitGroup

	for j := 0; j < t.testOptions.numPkgs; j++ {
		if err := t.ctx.Err(); err != nil {
			break
		}
		pkgSemaphore <- struct{}{}
		pkgWg.Add(1)
		go func(pkgIndex int) {
			defer pkgWg.Done()
			defer func() { <-pkgSemaphore }()
			processPackage(pkgIndex)
		}(j)
	}
	pkgWg.Wait()
}
