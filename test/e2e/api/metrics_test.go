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
	"slices"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
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

const dbCacheSkipMessage = "Package size metrics are only supported in DB cache deployments. If you already deployed Porch with the DB cache activated, set the DB_CACHE environment variable and re-run this test."

func (t *PorchSuite) TestPackageSizeMetric() {
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
