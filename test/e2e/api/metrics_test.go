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

	t.AddMutator(resources, setNamespaceImage, suiteutils.WithConfigPath("configmap.yaml"))
	t.UpdateF(resources)

	parsedResults := t.CollectMetricsFromPods()

	for _, regex := range porchServerShouldHaveRegexList {
		t.Assert().Regexp(regex, parsedResults.PorchServerMetrics, "porch server metrics should contain %q", regex)
	}

	for _, regex := range porchControllerShouldHaveRegexList {
		t.Assert().Regexp(regex, parsedResults.PorchControllerMetrics, "porch controller metrics should contain %q", regex)
	}

	for _, regex := range porchFunctionRunnerShouldHaveRegexList {
		t.Assert().Regexp(regex, parsedResults.PorchFunctionRunnerMetrics, "porch function runner metrics should contain %q", regex)
	}
	for _, regex := range porchWrapperServerShouldHaveRegexList {
		t.Assert().Regexp(regex, parsedResults.PorchWrapperServerMetrics, "porch wrapper server metrics should contain %q", regex)
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

	parsedResults := t.CollectMetricsFromPods()

	for _, metricName := range expectedMetrics {
		t.Assert().Regexp(metricName, parsedResults.PorchServerMetrics, "porch server metrics should contain %q", metricName)
	}
}

func (t *PorchSuite) TestAPIOperationDurationLabels() {
	if !t.ApiOpsDetailed {
		t.T().Log("PORCH_API_OPERATIONS_DETAILED is off: lifecycle_after, desired_lifecycle and per-package-revision attributes are not recorded, so checks involving them are skipped")
	}
	const (
		repository  = "metrics-labels"
		packageName = "metrics-labels-package"
		initPackage = "throwaway-init"
		workspace   = "metrics-labels-workspace"
	)
	var (
		createVerb = telemetry.Operations.Create.AllCaps
		getVerb    = telemetry.Operations.Get.AllCaps
		updateVerb = telemetry.Operations.Update.AllCaps
		deleteVerb = telemetry.Operations.Delete.AllCaps
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
		t.validateOperationDurationRecorded(getVerb, "GetPackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft,
			func() *porchapi.PackageRevision {
				t.GetF(key, pr)
				return pr
			})
		return pr
	}
	t.validateOperationDurationRecorded(createVerb, "ClonePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft,
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
	t.validateOperationDurationRecorded(updateVerb, "UpdatePackageRevisionResources", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft,
		pushOperation)

	// Initialize a new package revision (results in a successful InitPackageRevision).
	initOperation := func() *porchapi.PackageRevision {
		t.CreatePackageDraftF(repository, initPackage, workspace)
		initPr := &porchapi.PackageRevision{}
		t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: fmt.Sprintf("%s.%s.%s", repository, initPackage, workspace)}, initPr)
		return initPr
	}
	t.validateOperationDurationRecorded(createVerb, "InitPackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft,
		initOperation)

	// Creating an already-cloned package revision fails, which is recorded as an
	// errored ClonePackageRevision.
	duplicateCloneAttempt := func() *porchapi.PackageRevision {
		t.Client.Create(t.GetContext(), pr)
		return pr
	}
	t.validateOperationDurationRecorded(createVerb, "ClonePackageRevision", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycle(""),
		duplicateCloneAttempt)

	// Try to approve the package revision directly (results in error outcome and lifecycle_after == "Draft").
	directApproveAttempt := func() *porchapi.PackageRevision {
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
		t.UpdateApprovalL(pr)
		return pr
	}
	t.validateOperationDurationRecorded(updateVerb, "UpdatePackageRevisionApproval", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycleDraft,
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
	t.validateOperationDurationRecorded(updateVerb, "UpdatePackageRevision", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycle(""),
		nonExistentProposeAttempt)

	// Propose the package revision (results in lifecycle_after == "Proposed").
	proposeOperation := func() *porchapi.PackageRevision {
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
		t.UpdateF(pr)
		return pr
	}
	t.validateOperationDurationRecorded(updateVerb, "UpdatePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleProposed,
		proposeOperation)

	// Reject the proposed package revision (results in lifecycle_after == "Draft").
	rejectOperation := func() *porchapi.PackageRevision {
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDraft
		t.UpdateF(pr)
		return pr
	}
	t.validateOperationDurationRecorded(updateVerb, "UpdatePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft,
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
	t.validateOperationDurationRecorded(updateVerb, "UpdatePackageRevisionApproval", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycle(""),
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
	t.validateOperationDurationRecorded(updateVerb, "UpdatePackageRevisionApproval", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecyclePublished,
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
	t.validateOperationDurationRecorded(createVerb, "CopyPackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft,
		editOperation)

	// Try to delete the package revision directly (results in error outcome and lifecycle_after == "Published").
	directDeleteAttempt := func() *porchapi.PackageRevision {
		t.DeleteL(pr)
		return pr
	}
	t.validateOperationDurationRecorded(deleteVerb, "DeletePackageRevision", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecyclePublished,
		directDeleteAttempt)

	// Propose the package revision for deletion (results in lifecycle_after == "DeletionProposed").
	proposeDeleteOperation := func() *porchapi.PackageRevision {
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
		pr = t.UpdateApprovalF(pr)
		return pr
	}
	t.validateOperationDurationRecorded(updateVerb, "UpdatePackageRevisionApproval", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDeletionProposed,
		proposeDeleteOperation)

	// Delete the package revision
	deleteOperation := func() *porchapi.PackageRevision {
		t.DeleteF(pr)
		return pr
	}
	t.validateOperationDurationRecorded(deleteVerb, "DeletePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycle(""),
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
	t.validateOperationDurationRecorded(deleteVerb, "DeletePackageRevision", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycle(""),
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

	t.validateOperationDurationRecorded(getVerb, "GetPackageRevision", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycle(""),

		nonExistentGetAttempt)
}

// TestInFlightOperationsMetric verifies that the porch-server exports the
// porch_in_flight_api_operations metric, that it rises to 1 during operations,
// and that it settles back to 0 once operations cease. A non-zero value after
// this indicates a leaked in-flight operation (e.g. a decrement closure that
// was never properly invoked on an error path).
func (t *PorchSuite) TestInFlightOperationsMetric() {
	if !t.ApiOpsDetailed {
		t.T().Log("PORCH_API_OPERATIONS_DETAILED is off: lifecycle_after, desired_lifecycle and per-package-revision attributes are not recorded, so checks involving them are skipped")
	}
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
		stop := op.spammer.spam()
		found := op.scraper.waitToScrape(t, timeout)
		stop() // always stop the spammer, whether or not the metric was seen
		t.Require().Truef(found, "timed out after %s waiting to scrape in-flight metric with value > 0 and labels %v",
			timeout, t.inFlightLabels(pr, op.scraper.expectedOperationName))
	}

	expectedOperations := []string{"ClonePackageRevision", "GetPackageRevision", "UpdatePackageRevisionResources", "UpdatePackageRevision",
		"UpdatePackageRevisionApproval", "DeletePackageRevision"}

	// inScope picks the series this test is responsible for: the package revision's own series
	// when detailed, otherwise the series of the operations exercised above.
	inScope := func(m suiteutils.MetricResult) bool {
		if t.ApiOpsDetailed {
			return m.Attributes["namespace"] == model.LabelValue(t.Namespace) &&
				m.Attributes["repository"] == model.LabelValue(pr.Spec.RepositoryName) &&
				m.Attributes["package"] == model.LabelValue(pr.Spec.PackageName) &&
				m.Attributes["workspace_name"] == model.LabelValue(pr.Spec.WorkspaceName) &&
				slices.Contains(expectedOperations, string(m.Attributes["operation"]))
		}
		return slices.Contains(expectedOperations, string(m.Attributes["operation"]))
	}
	scoped := func() []suiteutils.MetricResult {
		return slices.DeleteFunc(slices.Clone(t.collectServerMetrics(inFlightMetric)),
			func(m suiteutils.MetricResult) bool { return !inScope(m) })
	}

	allZero := func() bool {
		return !slices.ContainsFunc(scoped(), func(m suiteutils.MetricResult) bool { return m.Value != 0 })
	}
	if !t.Assert().Eventually(allZero, 30*time.Second, time.Second, "in-flight counts for %q should settle to 0", inFlightMetric) {
		for _, s := range scoped() {
			if s.Value != 0 {
				t.Logf("operation=%q still has in-flight value %v", s.Attributes["operation"], s.Value)
			}
		}
	}

	matched := scoped()
	t.Require().NotEmptyf(matched, "expected in-flight series for %v", t.inFlightLabels(pr, ""))
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

	// The per-namespace histogram is emitted by default; the per-revision gauge is not.
	expectedHistogramSeries := []string{
		`porch_package_size_bytes_bucket`,
		`porch_package_size_bytes_count`,
		`porch_package_size_bytes_sum`,
	}
	const gaugeSeries = `porch_package_size_bytes_total`

	// Create a new package revision to ensure metric creation in porch-server
	t.setupFunctionTestPackage("git-fn-distroless", "test-fn-redis-bucket", "test-description", TestPackageSetupOptions{
		UpstreamRef: "redis-bucket/v1",
		UpstreamDir: "redis-bucket",
	})

	// Sync some package revisions to ensure metric creation in porch-controllers
	t.RegisterGitRepositoryF(t.GetTestBlueprintsRepoURL(), suiteutils.TestBlueprintsRepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	parsedResults := t.CollectMetricsFromPods()

	for _, metricName := range expectedHistogramSeries {
		t.Assert().Regexp(metricName, parsedResults.PorchServerMetrics, "porch server metrics should contain %q", metricName)
		t.Assert().Regexp(metricName, parsedResults.PorchControllerMetrics, "porch controller metrics should contain %q", metricName)
	}

	// The per-revision gauge must not be emitted in the default mode.
	t.Assert().NotRegexp(gaugeSeries, parsedResults.PorchServerMetrics, "porch server metrics should not contain per-revision gauge %q", gaugeSeries)
	t.Assert().NotRegexp(gaugeSeries, parsedResults.PorchControllerMetrics, "porch controller metrics should not contain per-revision gauge %q", gaugeSeries)
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
	t.AddMutator(resources, setNamespaceImage, suiteutils.WithConfigPath("configmap.yaml"))
	t.UpdateF(resources)

	pr := &porchapi.PackageRevision{}
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: resources.Name}, pr)

	// propose and approve
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	pr = t.UpdateApprovalF(pr)

	// Validate once the revision is published and its size is stable.
	t.validatePorchServerSizeMetric(pr)

	// propose-delete and delete
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	t.UpdateApprovalF(pr)
	t.DeleteE(pr)
	pr.Status.ResourcesSizeBytes = 0
	t.validatePorchServerSizeMetric(pr)

	// register a repo to sync some package revisions and test metric creation in porch-controllers
	t.RegisterGitRepositoryF(t.GetTestBlueprintsRepoURL(), suiteutils.TestBlueprintsRepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	upstreamPr := &porchapi.PackageRevision{}
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: "test-blueprints.basens.v4"}, upstreamPr)
	t.validatePorchControllerSizeMetric(upstreamPr)

	// delete the repo and wait for packages to be deleted to verify the metric is updated
	var repo configapi.Repository
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: suiteutils.TestBlueprintsRepoName}, &repo)
	t.DeleteE(&repo)
	t.WaitUntilRepositoryDeleted(suiteutils.TestBlueprintsRepoName, t.Namespace)
	t.WaitUntilAllPackagesDeleted(suiteutils.TestBlueprintsRepoName, t.Namespace)

	upstreamPr.Status.ResourcesSizeBytes = 0
	t.validatePorchControllerSizeMetric(upstreamPr)
}

func (t *PorchSuite) TestAPIOperationsDetailedAttributes() {
	t.setupFunctionTestPackage("git-fn-distroless", "test-fn-detailed-attrs", "ws",
		TestPackageSetupOptions{UpstreamRef: "redis-bucket/v1", UpstreamDir: "redis-bucket"})
	has := func(samples []suiteutils.MetricResult, l model.LabelName) bool {
		return slices.ContainsFunc(samples, func(s suiteutils.MetricResult) bool { _, ok := s.Attributes[l]; return ok })
	}
	for metric, lifecycleLabel := range map[string]model.LabelName{
		"porch_api_call_duration_seconds_count": "lifecycle_after",
		"porch_in_flight_api_operations":        "desired_lifecycle",
	} {
		samples := t.collectServerMetrics(metric)
		t.Require().NotEmpty(samples)
		for _, l := range []model.LabelName{"namespace", "repository", "package", "workspace_name", lifecycleLabel} {
			t.Assert().Equalf(t.ApiOpsDetailed, has(samples, l), "%s: label %q presence should match PORCH_API_OPERATIONS_DETAILED=%t", metric, l, t.ApiOpsDetailed)
		}
	}
}

func (t *PorchSuite) validatePorchServerSizeMetric(pr *porchapi.PackageRevision) {
	t.T().Helper()
	t.validateSizeMetric(pr, func(parsedResults *suiteutils.ParsedMetricsResults) map[string][]suiteutils.MetricResult {
		return parsedResults.PorchServerMetrics
	})
}

func (t *PorchSuite) validatePorchControllerSizeMetric(pr *porchapi.PackageRevision) {
	t.T().Helper()
	t.validateSizeMetric(pr, func(parsedResults *suiteutils.ParsedMetricsResults) map[string][]suiteutils.MetricResult {
		return parsedResults.PorchControllerMetrics
	})
}

func (t *PorchSuite) validateSizeMetric(pr *porchapi.PackageRevision, selectPodMetrics func(*suiteutils.ParsedMetricsResults) map[string][]suiteutils.MetricResult) {
	t.T().Helper()

	if !t.UsingDBCache {
		t.Assert().EqualValues(0, pr.Status.ResourcesSizeBytes, "PackageRevision resources size should not be available in non-DB cache deployment")
		return
	}

	parsedResults := t.CollectMetricsFromPods()

	t.validateNamespaceHistogram(pr, selectPodMetrics(parsedResults))
}

// validateNamespaceHistogram checks the per-namespace histogram. Exact
// per-revision values aren't recoverable from the aggregated histogram, and it
// is cumulative, so we assert directionally and skip the zero-size checkpoints.
func (t *PorchSuite) validateNamespaceHistogram(pr *porchapi.PackageRevision, podParsedResults map[string][]suiteutils.MetricResult) {
	t.T().Helper()

	if pr.Status.ResourcesSizeBytes == 0 {
		return
	}

	// The parser keys histogram samples under the family name; the _sum and
	// _count series are distinguished by the __name__ label.
	const (
		sumMetric   = "porch_package_size_bytes_sum"
		countMetric = "porch_package_size_bytes_count"
	)

	var namespaceSum, namespaceCount model.SampleValue

	for _, aMetric := range slices.Concat(podParsedResults[sumMetric], podParsedResults[countMetric]) {
		if aMetric.Attributes["namespace"] != model.LabelValue(t.Namespace) {
			continue
		}
		switch aMetric.Attributes[model.MetricNameLabel] {
		case sumMetric:
			namespaceSum += aMetric.Value
		case countMetric:
			namespaceCount += aMetric.Value
		}
	}

	t.Require().Greaterf(namespaceCount, model.SampleValue(0), "expected %q for namespace %q to have recorded at least one observation", countMetric, t.Namespace)
	t.Assert().GreaterOrEqualf(namespaceSum, model.SampleValue(pr.Status.ResourcesSizeBytes), "expected %q for namespace %q to be at least the revision size %d", sumMetric, t.Namespace, pr.Status.ResourcesSizeBytes)
}

type spammer struct {
	fn func()
}

// spam runs fn in a loop until the returned stop function is called. stop blocks until
// the loop has exited, so no call is still in flight when it returns.
func (s *spammer) spam() (stop func()) {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			default:
				s.fn()
			}
		}
	}()
	return func() { close(done); <-stopped }
}

type scraper struct {
	metricName, expectedOperationName string
	pr                                *porchapi.PackageRevision
	results                           []suiteutils.MetricResult
}

// waitToScrape polls until a matching series has a value above 0, and reports whether that
// happened before the timeout. The labels are rebuilt on every pass because s.pr is filled in
// by the spammer while this is running.
func (s *scraper) waitToScrape(t *PorchSuite, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		samples := t.collectServerMetrics(s.metricName)
		matching := suiteutils.FilterSamplesByLabel(samples, t.inFlightLabels(s.pr, s.expectedOperationName))
		if slices.ContainsFunc(matching, func(m suiteutils.MetricResult) bool { return m.Value > 0 }) {
			s.results = matching
			return true
		}
	}
	return false
}

// validateOperationDurationRecorded verifies that a Porch operation is reflected in
// the porch_api_call_duration metrics as a state transition.
// If per-package-revision operation metrics are enabled, verifies that no matching
// record exists before the operation runs and exactly one matching record exists
// after it runs. If not, verifies that the count of matching records has gone up
// by *at least* 1 after the operation.
//
// operationFn must perform the Porch operation under test and return the package
// revision the operation acted on; its key attributes are used to identify the metric
// series. expectedOutcome and expectedLifecycleAfter are the operation_outcome and
// lifecycle_after label values the resulting record is expected to carry.
func (t *PorchSuite) validateOperationDurationRecorded(httpVerb, operationName string, expectedOutcome telemetry.OperationOutcomeValue, expectedLifecycleAfter porchapi.PackageRevisionLifecycle, operationFn func() *porchapi.PackageRevision) {
	t.T().Helper()
	metricName := "porch_api_call_duration_seconds_count"

	// 1. Snapshot metrics before the operation runs.
	beforeSamples := t.collectServerMetrics(metricName)

	// 2. Run the operation and capture the package revision it acted on.
	pr := operationFn()
	t.Require().NotNil(pr, "operationFn must return the package revision the operation acted on")

	// 3. Assert exactly 1 relevant record has been added in the course of the operation
	beforeCount := t.countOperationDurationSeries(beforeSamples, httpVerb, operationName, t.Namespace, pr, expectedOutcome, expectedLifecycleAfter)
	expectedAfterCount := beforeCount + 1

	afterSamples := t.collectServerMetrics(metricName)
	actualAfterCount := t.countOperationDurationSeries(afterSamples, httpVerb, operationName, t.Namespace, pr, expectedOutcome, expectedLifecycleAfter)
	if t.ApiOpsDetailed {
		t.Require().Equalf(expectedAfterCount, actualAfterCount, "expected an added %q series (for a total of %d) for {namespace=%q, repository=%q, package=%q, workspace_name=%q, operation=%q, operation_outcome=%q, lifecycle_after=%q} after the operation, found %d",
			metricName, expectedAfterCount, t.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName, operationName, expectedOutcome, expectedLifecycleAfter, actualAfterCount)
	} else {
		t.Require().GreaterOrEqualf(actualAfterCount, expectedAfterCount, "expected %q count for {verb=%q, operation=%q,  operation_outcome=%q} to rise from %d to at least %d; found %d (other traffic may add more)",
			metricName, httpVerb, operationName, expectedOutcome, beforeCount, expectedAfterCount, actualAfterCount)
	}
}

// collectServerMetrics collects and parses porch-server metrics, returning
// the samples for metricName (nil if metricName is not present).
func (t *PorchSuite) collectServerMetrics(metricName string) []suiteutils.MetricResult {
	t.T().Helper()

	parsedResults := t.CollectMetricsFromPods()
	filteredResults := suiteutils.FilterSamplesByLabel(parsedResults.PorchServerMetrics[metricName],
		map[model.LabelName]string{"api_version": telemetry.APIVersionV1Alpha1},
	)
	return filteredResults
}

// countOperationDurationSeries returns a figure that goes up by exactly one when a matching
// Porch API operation is recorded. What is counted depends on how porch-server was installed
// (t.ApiOpsDetailed, from PORCH_API_OPERATIONS_DETAILED):
//
//   - Detailed: every package revision, namespace and lifecycle_after gets its own series, so
//     the samples are narrowed to this package revision and lifecycle, and the number of
//     matching series is returned. Each label combination identifies at most one series, so
//     the result is 0 or 1 and a newly recorded operation shows up as a new series. It does
//     not depend on the ordering of samples returned by Parse. Only the first occurrence
//     of a combination can be detected this way: a repeat of the same operation, outcome and
//     lifecycle on the same package revision adds no series.
//
//   - Default: the per-package-revision attributes and lifecycle_after are not exported, so
//     one series per verb, operation and outcome is shared by every package revision and
//     namespace. That series already exists once any such operation has run, so the sum of
//     the matching _count values is returned and a recorded operation shows up as an
//     increment. Other API traffic can add to the same series, so callers can only assert a
//     lower bound, and namespace, pr and expectedLifecycleAfter are ignored.
//
// In either mode the samples are first filtered by verb, operation, operation_outcome and
// api_version (v1alpha1), and samples must come from a *_count series of
// porch_api_call_duration_seconds (see validateOperationDurationRecorded).
func (t *PorchSuite) countOperationDurationSeries(samples []suiteutils.MetricResult, httpVerb, operationName, namespace string, pr *porchapi.PackageRevision, expectedOutcome telemetry.OperationOutcomeValue, expectedLifecycleAfter porchapi.PackageRevisionLifecycle) int {
	samples = suiteutils.FilterSamplesByLabel(samples,
		map[model.LabelName]string{
			"api_version":       telemetry.APIVersionV1Alpha1,
			"verb":              httpVerb,
			"operation":         operationName,
			"operation_outcome": string(expectedOutcome),
		})
	if t.ApiOpsDetailed {
		samples = suiteutils.FilterSamplesByLabel(samples,
			map[model.LabelName]string{
				"namespace":       namespace,
				"repository":      pr.Spec.RepositoryName,
				"package":         pr.Spec.PackageName,
				"workspace_name":  pr.Spec.WorkspaceName,
				"lifecycle_after": string(expectedLifecycleAfter),
			})
		// Series are specific to this package revision and lifecycle: the operation adds a new series.
		return len(samples)
	}

	// Aggregated across all package revisions: the series exists once any such operation has run,
	// so the operation shows up as an increment of its _count.
	var total int
	for _, s := range samples {
		total += int(s.Value)
	}
	return total
}

// inFlightLabels returns the labels identifying the in-flight series for operation. With
// PORCH_API_OPERATIONS_DETAILED off, series carry no package-revision attributes and are
// shared by every package revision, so the filter is by operation only.
func (t *PorchSuite) inFlightLabels(pr *porchapi.PackageRevision, operation string) map[model.LabelName]string {
	labels := map[model.LabelName]string{"operation": operation}
	if t.ApiOpsDetailed {
		labels["namespace"] = t.Namespace
		labels["repository"] = pr.Spec.RepositoryName
		labels["package"] = pr.Spec.PackageName
		labels["workspace_name"] = pr.Spec.WorkspaceName
	}
	return labels
}
