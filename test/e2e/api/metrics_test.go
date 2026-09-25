// Copyright 2026 The kpt Authors
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

package api

import (
	"fmt"
	"slices"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/internal/telemetry"
	"github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/prometheus/common/model"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (t *PorchSuite) TestMetricsEndpoint() {
	porchServerShouldHaveRegexList := []string{
		"go_.*",
		"http_server_.*",
		"http_client_.*",
		"errors_total.*",
		"target_info.*",
		"promhttp_metric_handler_.*",
	}
	porchControllerShouldHaveRegexList := []string{
		"controller_.*",
		"go_.*",
	}
	porchFunctionRunnerShouldHaveRegexList := []string{
		"go_.*",
		"rpc_server_.*",
		// "rpc_client_.*", //There is no way to force both function runners to have at least one connection, so no metrics
	}
	porchWrapperServerShouldHaveRegexList := []string{
		"go_.*",
		"rpc_server_.*",
	}

	// Create a package revision and update it with a mutator.
	// This is needed to trigger a render and ensure that there is at least one wrapper-server instance.
	resources := t.setupFunctionTestPackage("git-fn-distroless", "test-fn-redis-bucket", "test-description", TestPackageSetupOptions{
		UpstreamRef: "redis-bucket/v1",
		UpstreamDir: "redis-bucket",
	})

	resources.Spec.Resources["configmap.yaml"] = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kptfile.kpt.dev
data:
  name: bucket-namespace
`

	t.AddMutator(resources, t.KrmFunctionsRegistry+"/"+setNamespaceImage, suiteutils.WithConfigPath("configmap.yaml"))
	t.UpdateF(resources)

	collectionResults, err := t.CollectMetricsFromPods()
	if err != nil {
		t.Fatalf("failed to collect metrics from pods: %v", err)
	}

	for _, regex := range porchServerShouldHaveRegexList {
		t.Assert().Regexp(regex, collectionResults.PorchServerMetrics, "porch server metrics should contain %q", regex)
	}

	for _, regex := range porchControllerShouldHaveRegexList {
		t.Assert().Regexp(regex, collectionResults.PorchControllerMetrics, "porch controller metrics should contain %q", regex)
	}

	for _, regex := range porchFunctionRunnerShouldHaveRegexList {
		t.Assert().Regexp(regex, collectionResults.PorchFunctionRunnerMetrics, "porch function runner metrics should contain %q", regex)
	}
	for _, regex := range porchWrapperServerShouldHaveRegexList {
		t.Assert().Regexp(regex, collectionResults.PorchWrapperServerMetrics, "porch wrapper server metrics should contain %q", regex)
	}
}

func (t *PorchSuite) TestAPIOperationDurationMetricExists() {
	expectedMetrics := []string{
		`porch_api_call_duration_seconds_bucket`,
		`porch_api_call_duration_seconds_count`,
		`porch_api_call_duration_seconds_sum`,
	}

	// Create and update a package revision
	resources := t.setupFunctionTestPackage("git-fn-distroless", "test-fn-redis-bucket", "test-description", TestPackageSetupOptions{
		UpstreamRef: "redis-bucket/v1",
		UpstreamDir: "redis-bucket",
	})

	resources.Spec.Resources["configmap.yaml"] = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kptfile.kpt.dev
data:
  name: bucket-namespace
`

	t.AddMutator(resources, t.KrmFunctionsRegistry+"/"+setNamespaceImage, suiteutils.WithConfigPath("configmap.yaml"))
	t.UpdateF(resources)

	collectionResults, err := t.CollectMetricsFromPods()
	t.Require().NoError(err, "failed to collect metrics from pods:")

	for _, metricName := range expectedMetrics {
		t.Assert().Regexp(metricName, collectionResults.PorchServerMetrics, "porch server metrics should contain %q", metricName)
	}
}

func (t *PorchSuite) TestAPIOperationDurationLabels() {
	const (
		repository  = "metrics-labels"
		packageName = "metrics-labels-package"
		initPackage = "throwaway-init"
		workspace   = "metrics-labels-workspace"
		countMetric = "porch_api_call_duration_seconds_count"
	)

	var key client.ObjectKey
	pr := &porchapi.PackageRevision{}
	resources := &porchapi.PackageRevisionResources{}

	setupOperation := func() *porchapi.PackageRevision {
		resources = t.setupFunctionTestPackage(repository, packageName, workspace, TestPackageSetupOptions{
			UpstreamRef: "redis-bucket/v1",
			UpstreamDir: "redis-bucket",
		})
		key = client.ObjectKey{Namespace: t.Namespace, Name: resources.Name}

		// The clone that produced the package revision above should be recorded as a
		// successful ClonePackageRevision. Fetching it is recorded as a successful GetPackageRevision.
		t.validateOperationDurationRecorded(countMetric, "GetPackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft,
			func() *porchapi.PackageRevision {
				t.GetF(key, pr)
				return pr
			})
		return pr
	}
	t.validateOperationDurationRecorded(countMetric, "ClonePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft,
		setupOperation)

	pushOperation := func() *porchapi.PackageRevision {
		resources.Spec.Resources["configmap.yaml"] = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kptfile.kpt.dev
data:
  name: bucket-namespace
`
		t.AddMutator(resources, t.KrmFunctionsRegistry+"/"+setNamespaceImage,
			suiteutils.WithConfigPath("configmap.yaml"))
		t.UpdateF(resources)
		t.GetF(key, pr)
		return pr
	}
	t.validateOperationDurationRecorded(countMetric, "UpdatePackageRevisionResources", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft,
		pushOperation)

	// Initialize a new package revision (results in a successful InitPackageRevision).
	initOperation := func() *porchapi.PackageRevision {
		t.CreatePackageDraftF(repository, initPackage, workspace)
		initPr := &porchapi.PackageRevision{}
		t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: fmt.Sprintf("%s.%s.%s", repository, initPackage, workspace)}, initPr)
		return initPr
	}
	t.validateOperationDurationRecorded(countMetric, "InitPackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft,
		initOperation)

	// Creating an already-cloned package revision fails, which is recorded as an
	// errored ClonePackageRevision.
	duplicateCloneAttempt := func() *porchapi.PackageRevision {
		t.Client.Create(t.GetContext(), pr)
		return pr
	}
	t.validateOperationDurationRecorded(countMetric, "ClonePackageRevision", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycleDraft,
		duplicateCloneAttempt)

	// Try to approve the package revision directly (results in error outcome and lifecycle_after == "Draft").
	directApproveAttempt := func() *porchapi.PackageRevision {
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
		t.UpdateApprovalL(pr)
		return pr
	}
	t.validateOperationDurationRecorded(countMetric, "UpdatePackageRevisionApproval", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycleDraft,
		directApproveAttempt)

	nonExistentProposeAttempt := func() *porchapi.PackageRevision {
		copy := pr.DeepCopy()
		copy.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
		copy.Name = "does.not.exist"
		copy.Spec.RepositoryName = "does"
		copy.Spec.PackageName = "not"
		copy.Spec.WorkspaceName = "exist"
		t.UpdateL(copy)
		return copy
	}
	t.validateOperationDurationRecorded(countMetric, "UpdatePackageRevision", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycle("UNKNOWN"),
		nonExistentProposeAttempt)

	// Propose the package revision (results in lifecycle_after == "Proposed").
	proposeOperation := func() *porchapi.PackageRevision {
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
		t.UpdateF(pr)
		return pr
	}
	t.validateOperationDurationRecorded(countMetric, "UpdatePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleProposed,
		proposeOperation)

	// Reject the proposed package revision (results in lifecycle_after == "Draft").
	rejectOperation := func() *porchapi.PackageRevision {
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDraft
		t.UpdateF(pr)
		return pr
	}
	t.validateOperationDurationRecorded(countMetric, "UpdatePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft,
		rejectOperation)

	nonExistentApproveAttempt := func() *porchapi.PackageRevision {
		copy := pr.DeepCopy()
		copy.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
		copy.Name = "does.not.exist"
		copy.Spec.RepositoryName = "does"
		copy.Spec.PackageName = "not"
		copy.Spec.WorkspaceName = "exist"
		t.UpdateApprovalE(copy)
		return copy
	}
	t.validateOperationDurationRecorded(countMetric, "UpdatePackageRevisionApproval", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycle("UNKNOWN"),
		nonExistentApproveAttempt)

	// Approve the package revision (results in lifecycle_after == "Published").
	// (re-propose it first to reverse the rejection earlier)
	approveOperation := func() *porchapi.PackageRevision {
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
		t.UpdateF(pr)
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
		pr = t.UpdateApprovalF(pr)
		return pr
	}
	t.validateOperationDurationRecorded(countMetric, "UpdatePackageRevisionApproval", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecyclePublished,
		approveOperation)

	// Edit/copy a package revision
	editOperation := func() *porchapi.PackageRevision {
		editPR := t.CreatePackageSkeleton(repository, packageName, workspace+"-2")
		editPR.Spec.Tasks = []porchapi.Task{
			{
				Type: porchapi.TaskTypeEdit,
				Edit: &porchapi.PackageEditTaskSpec{
					Source: &porchapi.PackageRevisionRef{
						Name: pr.Name,
					},
				},
			},
		}
		t.Client.Create(t.GetContext(), editPR)
		return editPR
	}
	t.validateOperationDurationRecorded(countMetric, "CopyPackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft,
		editOperation)

	// Try to delete the package revision directly (results in error outcome and lifecycle_after == "Published").
	directDeleteAttempt := func() *porchapi.PackageRevision {
		t.DeleteL(pr)
		return pr
	}
	t.validateOperationDurationRecorded(countMetric, "DeletePackageRevision", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecyclePublished,
		directDeleteAttempt)

	// Propose the package revision for deletion (results in lifecycle_after == "DeletionProposed").
	proposeDeleteOperation := func() *porchapi.PackageRevision {
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
		pr = t.UpdateApprovalF(pr)
		return pr
	}
	t.validateOperationDurationRecorded(countMetric, "UpdatePackageRevisionApproval", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDeletionProposed,
		proposeDeleteOperation)

	// Delete the package revision
	deleteOperation := func() *porchapi.PackageRevision {
		t.DeleteF(pr)
		return pr
	}
	t.validateOperationDurationRecorded(countMetric, "DeletePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycle(""),
		deleteOperation)

	nonExistentDeleteAttempt := func() *porchapi.PackageRevision {
		copy := pr.DeepCopy()
		copy.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
		copy.Name = "does.not.exist"
		copy.Spec.RepositoryName = "does"
		copy.Spec.PackageName = "not"
		copy.Spec.WorkspaceName = "exist"
		t.DeleteL(copy)
		return copy
	}
	t.validateOperationDurationRecorded(countMetric, "DeletePackageRevision", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycle("UNKNOWN"),
		nonExistentDeleteAttempt)

	nonExistentGetAttempt := func() *porchapi.PackageRevision {
		none := &porchapi.PackageRevision{}
		copyKey := client.ObjectKey{Namespace: t.Namespace, Name: "does.not.exist"}
		t.Reader.Get(t.GetContext(), copyKey, none)
		none.Spec.RepositoryName = "does"
		none.Spec.PackageName = "not"
		none.Spec.WorkspaceName = "exist"
		return none
	}
	t.validateOperationDurationRecorded(countMetric, "GetPackageRevision", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycle("UNKNOWN"),
		nonExistentGetAttempt)
}

// TestInFlightOperationsMetric verifies that the porch-server exports the
// porch_in_flight_api_operations metric, that it rises to 1 during operations,
// and that it settles back to 0 once operations cease. A non-zero value after
// this indicates a leaked in-flight operation (e.g. a decrement closure that
// was never properly invoked on an error path).
func (t *PorchSuite) TestInFlightOperationsMetric() {
	const (
		repository     = "inflight-metrics"
		packageName    = "test-fn-inflight"
		initPackage    = "inflight-init"
		workspace      = "inflight-workspace"
		inFlightMetric = "porch_in_flight_api_operations"
	)

	// Drive some completed API operations through the porch-server
	resources := t.setupFunctionTestPackage(repository, packageName, workspace,
		TestPackageSetupOptions{
			UpstreamRef: "redis-bucket/v1",
			UpstreamDir: "redis-bucket",
		})

	pr := &porchapi.PackageRevision{}

	testCases := []struct {
		spammer *spammer
		scraper *scraper
	}{
		{
			spammer: &spammer{
				func() {
					// get
					t.GetL(client.ObjectKey{Namespace: t.Namespace, Name: resources.Name}, pr)
				},
			},
			scraper: &scraper{
				metricName:            inFlightMetric,
				expectedOperationName: "GetPackageRevision",
				pr:                    pr,
			},
		},
		{
			spammer: &spammer{
				func() {
					// clone (create)
					t.Client.Create(t.GetContext(), pr)
				},
			},
			scraper: &scraper{
				metricName:            inFlightMetric,
				expectedOperationName: "ClonePackageRevision",
				pr:                    pr,
			},
		},
		{
			spammer: &spammer{
				func() {

					// push (update resources)
					resources.Spec.Resources["configmap.yaml"] = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kptfile.kpt.dev
data:
  namespace: bucket-namespace
`
					t.AddMutator(resources, t.KrmFunctionsRegistry+"/"+setNamespaceImage,
						suiteutils.WithConfigPath("configmap.yaml"))
					t.UpdateL(resources)
					// get
					t.GetL(client.ObjectKey{Namespace: t.Namespace, Name: resources.Name}, pr)
				},
			},
			scraper: &scraper{
				metricName:            inFlightMetric,
				expectedOperationName: "UpdatePackageRevisionResources",
				pr:                    pr,
			},
		},
		{
			spammer: &spammer{
				func() {
					// propose
					pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
					t.UpdateL(pr)
				},
			},
			scraper: &scraper{
				metricName:            inFlightMetric,
				expectedOperationName: "UpdatePackageRevision",
				pr:                    pr,
			},
		},
		{
			spammer: &spammer{
				func() {
					// approve
					pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
					t.UpdateApprovalL(pr)

					// propose-delete
					pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
					pr = t.UpdateApprovalL(pr)
				},
			},
			scraper: &scraper{
				metricName:            inFlightMetric,
				expectedOperationName: "UpdatePackageRevisionApproval",
				pr:                    pr,
			},
		},
		{
			spammer: &spammer{
				func() {
					// delete
					t.DeleteL(pr)
				},
			},
			scraper: &scraper{
				metricName:            inFlightMetric,
				expectedOperationName: "DeletePackageRevision",
				pr:                    pr,
			},
		},
	}

	timeout := 10 * time.Second

	for _, op := range testCases {
		done := make(chan bool)
		op.spammer.spam(t, done)

		go func(timeoutChan chan bool) {
			time.Sleep(timeout)
			select {
			case timeoutChan <- true:
				t.Error("timed out waiting to scrape in-flight metric with value 1 and attributes {namespace=%q, repository=%q, package=%q, workspace_name=%q, operation=%q}", t.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName, op.scraper.expectedOperationName)
			default:
			}
		}(done)

		op.scraper.waitToScrape(t, done)

		inFlightCountValue := op.scraper.results[slices.IndexFunc(op.scraper.results, func(m suiteutils.MetricResult) bool {
			return m.Attributes["namespace"] == model.LabelValue(t.Namespace) &&
				m.Attributes["repository"] == model.LabelValue(pr.Spec.RepositoryName) &&
				m.Attributes["package"] == model.LabelValue(pr.Spec.PackageName) &&
				m.Attributes["workspace_name"] == model.LabelValue(pr.Spec.WorkspaceName) &&
				m.Attributes["operation"] == model.LabelValue(op.scraper.expectedOperationName)
		})].Value
		t.Require().GreaterOrEqual(inFlightCountValue, float64(1))
	}

	samplesAfter := t.collectServerMetrics(inFlightMetric)

	// The metric must exist once at least one operation has run.
	t.Require().NotEmpty(samplesAfter, "porch server metrics should contain %q", inFlightMetric)

	// The series for the package revision we just exercised must be present,
	// confirming the expected label set (resource + operation + prKey attributes)
	// is recorded by TrackInFlightOperation.
	matched := slices.DeleteFunc(slices.Clone(samplesAfter), func(m suiteutils.MetricResult) bool {
		return !(m.Attributes["namespace"] == model.LabelValue(t.Namespace) &&
			m.Attributes["repository"] == model.LabelValue(pr.Spec.RepositoryName) &&
			m.Attributes["package"] == model.LabelValue(pr.Spec.PackageName) &&
			m.Attributes["workspace_name"] == model.LabelValue(pr.Spec.WorkspaceName))
	})
	t.Require().NotEmptyf(matched, "expected at least one %q series for {namespace=%q, repository=%q, package=%q, workspace_name=%q}",
		inFlightMetric, t.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName)

	// Every series for the package revision must have settled to 0
	for _, s := range matched {
		t.Assert().EqualValuesf(0, s.Value, "in-flight operation count for %q should settle to 0 once operations complete, got %v for operation=%q", inFlightMetric, s.Value, s.Attributes["operation"])
	}

	// Each of the expected operations must have an in-flight record at some point.
	expectedOperations := []string{"ClonePackageRevision", "GetPackageRevision", "UpdatePackageRevisionResources", "UpdatePackageRevision", "UpdatePackageRevisionApproval", "DeletePackageRevision"}
	for _, operation := range expectedOperations {
		t.Require().Truef(slices.ContainsFunc(matched, func(m suiteutils.MetricResult) bool {
			return m.Attributes["operation"] == model.LabelValue(operation)
		}), "expected an in-flight record for operation %s", operation)
	}
}

const dbCacheSkipMessage = "Package size metrics are only supported in DB cache deployments. If you already deployed Porch with the DB cache activated, set the DB_CACHE environment variable and re-run this test."

func (t *PorchSuite) TestPackageSizeMetricsExist() {
	if !t.UsingDBCache {
		t.T().Skip(dbCacheSkipMessage)
	}

	expectedMetrics := []string{
		`porch_package_size_bytes_bucket`,
		`porch_package_size_bytes_count`,
		`porch_package_size_bytes_sum`,
		`porch_package_size_bytes_total`,
	}

	// Create a new package revision to ensure metric creation in porch-server
	t.setupFunctionTestPackage("git-fn-distroless", "test-fn-redis-bucket", "test-description", TestPackageSetupOptions{
		UpstreamRef: "redis-bucket/v1",
		UpstreamDir: "redis-bucket",
	})

	// Sync some package revisions to ensure metric creation in porch-controllers
	t.RegisterGitRepositoryF(t.GetTestBlueprintsRepoURL(), suiteutils.TestBlueprintsRepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	collectionResults, err := t.CollectMetricsFromPods()
	t.Require().NoError(err, "failed to collect metrics from pods:")

	for _, metricName := range expectedMetrics {
		t.Assert().Regexp(metricName, collectionResults.PorchServerMetrics, "porch server metrics should contain %q", metricName)
		t.Assert().Regexp(metricName, collectionResults.PorchControllerMetrics, "porch controller metrics should contain %q", metricName)
	}
}

func (t *PorchSuite) TestPackageSizeMetricValues() {
	if !t.UsingDBCache {
		t.T().Skip(dbCacheSkipMessage)
	}

	// Create a new package via init, no task specified
	const (
		repository  = "metrics-values"
		packageName = "metrics-package"
		workspace   = "metrics-workspace"
		description = "empty-package description"

		expectedMetric = "porch_package_size_bytes_total"
	)

	// initialize a package
	resources := t.setupFunctionTestPackage(repository, packageName, workspace, TestPackageSetupOptions{
		UpstreamRef: "redis-bucket/v1",
		UpstreamDir: "redis-bucket",
	})
	resources.Spec.Resources["configmap.yaml"] = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kptfile.kpt.dev
data:
  name: bucket-namespace
`

	// push a resource change
	t.AddMutator(resources, t.KrmFunctionsRegistry+"/"+setNamespaceImage, suiteutils.WithConfigPath("configmap.yaml"))
	t.UpdateF(resources)

	pr := &porchapi.PackageRevision{}
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: resources.Name}, pr)

	t.validatePorchServerSizeMetric(pr, expectedMetric)

	// propose and approve
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	pr = t.UpdateApprovalF(pr)

	t.validatePorchServerSizeMetric(pr, expectedMetric)

	// propose-delete and delete
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	t.UpdateApprovalF(pr)
	t.DeleteE(pr)
	pr.Status.ResourcesSizeBytes = 0
	t.validatePorchServerSizeMetric(pr, expectedMetric)

	// register a repo to sync some package revisions and test metric creation in porch-controllers
	t.RegisterGitRepositoryF(t.GetTestBlueprintsRepoURL(), suiteutils.TestBlueprintsRepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	upstreamPr := &porchapi.PackageRevision{}
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: "test-blueprints.basens.v4"}, upstreamPr)
	t.validatePorchControllerSizeMetric(upstreamPr, expectedMetric)

	// delete the repo and wait for packages to be deleted to verify the metric is updated
	var repo configapi.Repository
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: suiteutils.TestBlueprintsRepoName}, &repo)
	t.DeleteE(&repo)
	t.WaitUntilRepositoryDeleted(suiteutils.TestBlueprintsRepoName, t.Namespace)
	t.WaitUntilAllPackagesDeleted(suiteutils.TestBlueprintsRepoName, t.Namespace)

	upstreamPr.Status.ResourcesSizeBytes = 0
	t.validatePorchControllerSizeMetric(upstreamPr, expectedMetric)
}

func (t *PorchSuite) validatePorchServerSizeMetric(pr *porchapi.PackageRevision, metricName string) {
	t.T().Helper()
	t.validateSizeMetric(pr, metricName, func(parsedResults *suiteutils.ParsedMetricsResults) map[string][]suiteutils.MetricResult {
		return parsedResults.PorchServerMetrics
	})
}

func (t *PorchSuite) validatePorchControllerSizeMetric(pr *porchapi.PackageRevision, metricName string) {
	t.T().Helper()
	t.validateSizeMetric(pr, metricName, func(parsedResults *suiteutils.ParsedMetricsResults) map[string][]suiteutils.MetricResult {
		return parsedResults.PorchControllerMetrics
	})
}

func (t *PorchSuite) validateSizeMetric(pr *porchapi.PackageRevision, metricName string, selectPodMetrics func(*suiteutils.ParsedMetricsResults) map[string][]suiteutils.MetricResult) {
	t.T().Helper()
	if t.UsingDBCache {
		collectionResults, err := t.CollectMetricsFromPods()
		t.Require().NoError(err, "failed to collect metrics from pods:")
		parsedResults, err := collectionResults.Parse()
		t.Require().NoError(err, "failed to parse collected metrics:")

		podParsedResults := selectPodMetrics(parsedResults)

		t.Assert().Contains(podParsedResults, metricName)

		metric := podParsedResults[metricName]
		metric = slices.DeleteFunc(metric, func(aMetric suiteutils.MetricResult) bool {
			return !(aMetric.Attributes["namespace"] == model.LabelValue(t.Namespace) &&
				aMetric.Attributes["repository"] == model.LabelValue(pr.Spec.RepositoryName) &&
				aMetric.Attributes["package"] == model.LabelValue(pr.Spec.PackageName) &&
				aMetric.Attributes["workspace_name"] == model.LabelValue(pr.Spec.WorkspaceName))
		})
		t.Require().Lenf(metric, 1, "Expected metrics to include exactly 1 %q entry with {namespace=%q, repository=%q, package=%q, workspace_name=%q}, but did not", metricName, t.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName)
		t.Assert().EqualValues(model.SampleValue(pr.Status.ResourcesSizeBytes), metric[0].Value)
	} else {
		t.Assert().EqualValues(0, pr.Status.ResourcesSizeBytes, "PackageRevision resources size should not be available in non-DB cache deployment")
	}

}

type spammer struct {
	fn func()
}

func (s *spammer) spam(t *PorchSuite, done chan bool) {
	go func(done chan bool) {
		for {
			select {
			case <-done:
				return
			default:
				s.fn()
			}
		}
	}(done)
}

type scraper struct {
	metricName, expectedOperationName string
	pr                                *porchapi.PackageRevision
	results                           []suiteutils.MetricResult
}

func (s *scraper) waitToScrape(t *PorchSuite, stopSpammer chan bool) {
	samples := t.collectServerMetrics(s.metricName)
	for {
		select {
		case <-stopSpammer:
			return
		default:
			switch {
			case slices.ContainsFunc(samples, func(m suiteutils.MetricResult) bool {
				found := m.Attributes["namespace"] == model.LabelValue(t.Namespace) &&
					m.Attributes["repository"] == model.LabelValue(s.pr.Spec.RepositoryName) &&
					m.Attributes["package"] == model.LabelValue(s.pr.Spec.PackageName) &&
					m.Attributes["workspace_name"] == model.LabelValue(s.pr.Spec.WorkspaceName) &&
					m.Attributes["operation"] == model.LabelValue(s.expectedOperationName) &&
					m.Value > 0
				return found
			}):
				stopSpammer <- true
				s.results = samples
				return
			default:
				samples = t.collectServerMetrics(s.metricName)
			}
		}
	}

}

// validateOperationDurationRecorded verifies that a Porch operation is reflected in
// the porch_api_call_duration metrics as a state transition: no matching record exists
// before the operation runs, and exactly one matching record exists after it runs.
//
// operationFn must perform the Porch operation under test and return the package
// revision the operation acted on; its key attributes are used to identify the metric
// series. expectedOutcome and expectedLifecycleAfter are the operation_outcome and
// lifecycle_after label values the resulting record is expected to carry.
func (t *PorchSuite) validateOperationDurationRecorded(metricName, operationName string, expectedOutcome telemetry.OperationOutcomeValue, expectedLifecycleAfter porchapi.PackageRevisionLifecycle, operationFn func() *porchapi.PackageRevision) {
	t.T().Helper()

	// 1. Snapshot metrics before the operation runs.
	beforeSamples := t.collectServerMetrics(metricName)

	// 2. Run the operation and capture the package revision it acted on.
	pr := operationFn()
	t.Require().NotNil(pr, "operationFn must return the package revision the operation acted on")

	// 3. Assert exactly 1 relevant record has been added in the course of the operation
	beforeCount := countOperationDurationSeries(beforeSamples, t.Namespace, pr, operationName, expectedOutcome, expectedLifecycleAfter)
	expectedAfterCount := beforeCount + 1

	afterSamples := t.collectServerMetrics(metricName)
	actualAfterCount := countOperationDurationSeries(afterSamples, t.Namespace, pr, operationName, expectedOutcome, expectedLifecycleAfter)
	t.Require().Equalf(expectedAfterCount, actualAfterCount, "expected an added %q record (for a total of %d) for {namespace=%q, repository=%q, package=%q, workspace_name=%q, operation=%q, operation_outcome=%q, lifecycle_after=%q} after the operation, found %d",
		metricName, expectedAfterCount, t.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName, operationName, expectedOutcome, expectedLifecycleAfter, actualAfterCount)
}

// collectServerMetrics collects and parses porch-server metrics, returning
// the samples for metricName (nil if metricName is not present).
func (t *PorchSuite) collectServerMetrics(metricName string) []suiteutils.MetricResult {
	t.T().Helper()

	collectionResults, err := t.CollectMetricsFromPods()
	t.Require().NoError(err, "failed to collect metrics from pods:")
	parsedResults, err := collectionResults.Parse()
	t.Require().NoError(err, "failed to parse collected metrics:")

	return parsedResults.PorchServerMetrics[metricName]
}

// countOperationDurationSeries returns the number of time series in samples that match
// the package-revision key attributes, the operation, and the distinguishing
// operation_outcome and lifecycle_after labels. Because every label that distinguishes
// one series from another is included in the match, the result is 0 or 1 and does not
// depend on the (non-deterministic) ordering of samples returned by Parse.
func countOperationDurationSeries(samples []suiteutils.MetricResult, namespace string, pr *porchapi.PackageRevision, operationName string, expectedOutcome telemetry.OperationOutcomeValue, expectedLifecycleAfter porchapi.PackageRevisionLifecycle) int {
	count := 0
	for _, s := range samples {
		if s.Attributes["namespace"] == model.LabelValue(namespace) &&
			s.Attributes["repository"] == model.LabelValue(pr.Spec.RepositoryName) &&
			s.Attributes["package"] == model.LabelValue(pr.Spec.PackageName) &&
			s.Attributes["workspace_name"] == model.LabelValue(pr.Spec.WorkspaceName) &&
			s.Attributes["operation"] == model.LabelValue(operationName) &&
			telemetry.OperationOutcomeValue(s.Attributes["operation_outcome"]) == expectedOutcome &&
			porchapi.PackageRevisionLifecycle(s.Attributes["lifecycle_after"]) == expectedLifecycleAfter {
			count++
		}
	}
	return count
}
