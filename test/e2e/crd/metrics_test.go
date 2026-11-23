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

package crd

import (
	"context"
	"slices"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/internal/telemetry"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Metrics", Label("infra"), func() {
	It("should expose prometheus metrics from porch components", func() {
		if !allInCluster {
			Skip("metrics test requires all components in-cluster")
		}

		ctx := context.Background()

		By("finding porch-system pods")
		var pods corev1.PodList
		Expect(k8sClient.List(ctx, &pods, client.InNamespace("porch-system"))).To(Succeed())
		Expect(pods.Items).NotTo(BeEmpty())

		podsByPrefix := map[string]*corev1.Pod{}
		for i := range pods.Items {
			pod := &pods.Items[i]
			for _, prefix := range []string{"porch-server", "porch-controllers", "function-runner"} {
				if len(pod.Name) >= len(prefix) && pod.Name[:len(prefix)] == prefix {
					podsByPrefix[prefix] = pod
				}
			}
		}

		for _, prefix := range []string{"porch-controllers"} {
			pod, ok := podsByPrefix[prefix]
			if !ok {
				continue
			}
			By("checking metrics from " + prefix)
			resp, err := kubeClient.CoreV1().Pods("porch-system").
				ProxyGet("", pod.Name, "9464", "metrics", nil).
				DoRaw(ctx)
			Expect(err).NotTo(HaveOccurred())
			metrics := string(resp)
			Expect(metrics).To(ContainSubstring("http_client_"))
			Expect(metrics).To(ContainSubstring("target_info"))
		}
	})
})

var _ = Describe("API operation metrics", Ordered, Label("infra"), func() {
	var (
		env           *testEnv
		customTimeout = defaultTimeout / 6
	)
	const (
		bucketMetric = "porch_api_call_duration_seconds_bucket"
		countMetric  = "porch_api_call_duration_seconds_count"
		sumMetric    = "porch_api_call_duration_seconds_sum"
	)

	BeforeAll(func() {
		if !allInCluster {
			Skip("operation metrics test requires all components in-cluster")
		}
		env = sharedEnv()
	})

	It("exposes the porch_api_call_duration_seconds histogram in porch-controllers", func() {
		pr := newPackageRevision(env.Namespace, env.RepoName, "metrics-exists", "v1", withInit("porch_api_call_duration_seconds metrics exist"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		DeferCleanup(func() { deletePackage(env.Ctx, pr) })

		for _, metricName := range []string{
			bucketMetric, countMetric, sumMetric,
		} {
			Eventually(func() (string, error) {
				results, err := suiteutils.CollectMetricsFromPods(env.Ctx, kubeClient)
				if err != nil {
					return "", err
				}
				return results.PorchControllerMetrics, nil
			}).
				WithTimeout(customTimeout).WithPolling(defaultInterval).
				Should(MatchRegexp(metricName),
					"metrics from porch-controllers should contain %q", metricName)
		}
	})

	// Port of TestAPIOperationDurationLabels, reduced to the transitions the
	// v1alpha2 controller path actually records: the source reconcile records
	// operation=InitPackageRevision with lifecycle_after=Draft, and the lifecycle
	// transition reconcile records operation=UpdatePackageRevision with lifecycle_after
	// set to the resulting lifecycle. The api suite's synchronous porch-server REST
	// error/negative matrix is intentionally omitted because it does not exist in the
	// controller reconcile loop.
	It("records operation/lifecycle_after labels on controller duration series", func() {

		pr := newPackageRevision(env.Namespace, env.RepoName, "metrics-labels", "v1", withInit("metrics labels"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("verifying the source reconcile recorded InitPackageRevision/Draft/success")
		Eventually(func() int {
			return countControllerOperationSeries(collectControllerMetrics(env.Ctx, countMetric),
				env.Namespace, pr, telemetry.Operations.Create.AllCaps, "InitPackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft)
		}).WithTimeout(customTimeout).WithPolling(defaultInterval).
			Should(BeNumerically(">=", 1),
				"expected an InitPackageRevision/Draft success series on porch-controllers for {namespace=%q, repository=%q, package=%q, workspace_name=%q, operation=%q, operation_outcome=%q, lifecycle_after=%q}", env.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName, "InitPackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft)

		By("verifying the lifecycle reconcile's content read recorded GetPackageRevision/Draft/success")
		Eventually(func() int {
			return countControllerOperationSeries(collectControllerMetrics(env.Ctx, countMetric),
				env.Namespace, pr, telemetry.Operations.Get.AllCaps, "GetPackageRevision", telemetry.OperationOutcomes.Success,
				porchapi.PackageRevisionLifecycleDraft)
		}).WithTimeout(customTimeout).WithPolling(defaultInterval).
			Should(BeNumerically(">=", 1), "expected a GetPackageRevision/Draft success series on porch-controllers for {namespace=%q, repository=%q, package=%q, workspace_name=%q, operation=%q, operation_outcome=%q, lifecycle_after=%q}", env.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName, "GetPackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleDraft)

		By("proposing and approving the package revision")
		publishPackage(env.Ctx, pr)

		By("verifying the lifecycle transition reconcile recorded UpdatePackageRevision/Proposed/success")
		Eventually(func() int {
			return countControllerOperationSeries(collectControllerMetrics(env.Ctx, countMetric),
				env.Namespace, pr, telemetry.Operations.Update.AllCaps, "UpdatePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleProposed)
		},
		).WithTimeout(customTimeout).WithPolling(defaultInterval).
			Should(BeNumerically(">=", 1),
				"expected at least 1 UpdatePackageRevision/Proposed success series in porch-controllers metrics for {namespace=%q, repository=%q, package=%q, workspace_name=%q, operation=%q, operation_outcome=%q, lifecycle_after=%q}", env.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName, "UpdatePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycleProposed)

		By("verifying the lifecycle transition reconcile recorded UpdatePackageRevisionApproval/Published/success")
		Eventually(func() int {
			return countControllerOperationSeries(collectControllerMetrics(env.Ctx, countMetric),
				env.Namespace, pr, telemetry.Operations.Update.AllCaps, "UpdatePackageRevisionApproval", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecyclePublished)
		},
		).WithTimeout(customTimeout).WithPolling(defaultInterval).
			Should(BeNumerically(">=", 1),
				"expected at least 1 UpdatePackageRevision/Published success series in porch-controllers metrics for {namespace=%q, repository=%q, package=%q, workspace_name=%q, operation=%q, operation_outcome=%q, lifecycle_after=%q}", env.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName, "UpdatePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecyclePublished)

		deletePackage(env.Ctx, pr)
		waitForDeleted(env.Ctx, pr)
		By("verifying the delete recorded DeletePackageRevision/success with no lifecycle")
		Eventually(func() int {
			return countControllerOperationSeries(collectControllerMetrics(env.Ctx, "porch_api_call_duration_seconds_count"),
				env.Namespace, pr, telemetry.Operations.Delete.AllCaps, "DeletePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycle(""))
		}).WithTimeout(customTimeout).WithPolling(defaultInterval).
			Should(BeNumerically(">=", 1), "expected a DeletePackageRevision/success series on porch-controllers for {namespace=%q, repository=%q, package=%q, workspace_name=%q, operation=%q, operation_outcome=%q, lifecycle_after=%q}", env.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName, "DeletePackageRevision", telemetry.OperationOutcomes.Success, porchapi.PackageRevisionLifecycle(""))
	})

	It("records operation_outcome=error on the source and render reconcile paths", func() {

		By("copying from a non-existent source records CopyPackageRevision/error, with no lifecycle_after")
		badCopy := newPackageRevision(env.Namespace, env.RepoName, "metrics-err-src", "v1",
			withCopyFrom(crdName(env.RepoName, "metrics-err-src", "does-not-exist")))
		Expect(k8sClient.Create(env.Ctx, badCopy)).To(Succeed())

		waitForReadyFalse(env.Ctx, badCopy)
		Eventually(func() int {
			return countControllerOperationSeries(collectControllerMetrics(env.Ctx, countMetric),
				env.Namespace, badCopy, telemetry.Operations.Create.AllCaps, "CopyPackageRevision", telemetry.OperationOutcomes.Error, "")
		}).WithTimeout(customTimeout).WithPolling(defaultInterval).
			Should(BeNumerically(">=", 1), "expected a CopyPackageRevision/error series on porch-controllers for {namespace=%q, repository=%q, package=%q, workspace_name=%q, operation=%q, operation_outcome=%q, lifecycle_after=%q}", env.Namespace, badCopy.Spec.RepositoryName, badCopy.Spec.PackageName, badCopy.Spec.WorkspaceName, "CopyPackageRevision", telemetry.OperationOutcomes.Error, "")

		By("an unresolvable function image records UpdatePackageRevisionResources/error")
		badRender := newPackageRevision(env.Namespace, env.RepoName, "metrics-err-render", "v1", withInit("metrics render error"))
		Expect(k8sClient.Create(env.Ctx, badRender)).To(Succeed())
		waitForReady(env.Ctx, badRender)

		updatePRRResources(env.Ctx, env.Namespace, badRender.Name, map[string]string{
			"Kptfile": `
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: metrics-err-render
pipeline:
  mutators:
  - image: quay.io/invalid/nonexistent-fn:v0.0.1`,
			"cm.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: test-cm
  data:
    key: value
`,
		})

		waitForRenderFailed(env.Ctx, badRender)
		Eventually(func() int {
			return countControllerOperationSeries(collectControllerMetrics(env.Ctx, countMetric),
				env.Namespace, badRender, telemetry.Operations.Update.AllCaps, "UpdatePackageRevisionResources",
				telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycleDraft)
		}).WithTimeout(customTimeout).WithPolling(defaultInterval).
			Should(BeNumerically(">=", 1), "expected an UpdatePackageRevisionResources/error series on porch-controllers for for {namespace=%q, repository=%q, package=%q, workspace_name=%q, operation=%q, operation_outcome=%q, lifecycle_after=%q}", env.Namespace, badRender.Spec.RepositoryName, badRender.Spec.PackageName, badRender.Spec.WorkspaceName, "UpdatePackageRevisionResources", telemetry.OperationOutcomes.Error, porchapi.PackageRevisionLifecycleDraft)
	})

	// Port of TestInFlightOperationsMetric, asserting against
	// PorchControllerMetrics. The counter is incremented at the start of an operation
	// and decremented in a defer once it completes, so once reconciliation has quiesced
	// every series for the package revision must have settled to 0. A non-zero value
	// indicates a leaked in-flight operation (decrement closure not invoked).
	It("records porch_in_flight_api_operations for operations and settles it to 0 after each operation completes", func() {
		const inFlightMetric = "porch_in_flight_api_operations"

		pr := newPackageRevision(env.Namespace, env.RepoName, "metrics-inflight", "v1", withInit("metrics inflight"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		publishPackage(env.Ctx, pr)

		deletePackage(env.Ctx, pr)
		waitForDeleted(env.Ctx, pr)

		matched := make([]suiteutils.MetricResult, 0)
		Eventually(func(g Gomega) {
			samples := collectControllerMetrics(env.Ctx, inFlightMetric)

			for _, s := range samples {
				if s.Attributes["namespace"] == model.LabelValue(env.Namespace) &&
					s.Attributes["repository"] == model.LabelValue(pr.Spec.RepositoryName) &&
					s.Attributes["package"] == model.LabelValue(pr.Spec.PackageName) &&
					s.Attributes["workspace_name"] == model.LabelValue(pr.Spec.WorkspaceName) {
					matched = append(matched, s)
				}
			}

			g.Expect(matched).NotTo(BeEmpty(), "expected at least one in-flight series for the package revision")
			for _, s := range matched {
				Expect(float64(s.Value)).To(Or(BeNumerically("==", 0), BeNumerically("==", 1)),
					"in-flight operation count should be 0 or 1, got %v for operation=%q (package revision {namespace=%q, repository=%q, package=%q, workspace_name=%q})", s.Value, s.Attributes["operation"], env.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName)
			}

			expectedOperations := []string{"InitPackageRevision", "UpdatePackageRevisionResources", "UpdatePackageRevision", "UpdatePackageRevisionApproval", "DeletePackageRevision"}
			for _, operation := range expectedOperations {
				g.Expect(slices.ContainsFunc(matched, func(m suiteutils.MetricResult) bool {
					return m.Attributes["operation"] == model.LabelValue(operation)
				})).To(BeTrue(), "expected an in-flight record for operation %s (package revision {namespace=%q, repository=%q, package=%q, workspace_name=%q})", operation, env.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName)
			}
		}).WithTimeout(customTimeout).WithPolling(defaultInterval).Should(Succeed())

		samples := collectControllerMetrics(env.Ctx, inFlightMetric)

		matched = make([]suiteutils.MetricResult, 0)
		for _, s := range samples {
			if s.Attributes["namespace"] == model.LabelValue(env.Namespace) &&
				s.Attributes["repository"] == model.LabelValue(pr.Spec.RepositoryName) &&
				s.Attributes["package"] == model.LabelValue(pr.Spec.PackageName) &&
				s.Attributes["workspace_name"] == model.LabelValue(pr.Spec.WorkspaceName) {
				matched = append(matched, s)
			}
		}
		Expect(matched).NotTo(BeEmpty(), "expected at least one in-flight series for the package revision")
		for _, s := range matched {
			Expect(float64(s.Value)).To(BeNumerically("==", 0),
				"in-flight operation count should settle to 0, got %v for operation=%q (package revision {namespace=%q, repository=%q, package=%q, workspace_name=%q})", s.Value, s.Attributes["operation"], env.Namespace, pr.Spec.RepositoryName, pr.Spec.PackageName, pr.Spec.WorkspaceName)
		}
	})
})

// collectControllerMetrics collects and parses porch-server metrics, returning
// the samples for metricName (nil if metricName is not present).
func collectControllerMetrics(ctx context.Context, metricName string) []suiteutils.MetricResult {
	results, err := suiteutils.CollectMetricsFromPods(ctx, kubeClient)
	Expect(err).NotTo(HaveOccurred(), "failed to collect metrics from pods")

	parsed, err := results.Parse()
	Expect(err).NotTo(HaveOccurred(), "failed to parse collected metrics")

	filteredResults := suiteutils.FilterSamplesByLabel(parsed.PorchControllerMetrics[metricName],
		map[model.LabelName]string{"api_version": telemetry.APIVersionV1Alpha2},
	)
	return filteredResults
}

// countControllerOperationSeries returns the number of porch-controllers duration
// series in samples that match the package-revision key attributes, the operation, and
// the distinguishing operation_outcome and lifecycle_after labels. Because every label
// that distinguishes one series from another is included in the match, the result is 0
// or 1 and does not depend on the ordering of samples returned by Parse.
func countControllerOperationSeries(samples []suiteutils.MetricResult, namespace string, pr *porchapi.PackageRevision, httpVerb, operationName string, expectedOutcome telemetry.OperationOutcomeValue, expectedLifecycleAfter porchapi.PackageRevisionLifecycle) int {
	count := 0
	for _, s := range samples {
		if s.Attributes["namespace"] == model.LabelValue(namespace) &&
			s.Attributes["repository"] == model.LabelValue(pr.Spec.RepositoryName) &&
			s.Attributes["package"] == model.LabelValue(pr.Spec.PackageName) &&
			s.Attributes["workspace_name"] == model.LabelValue(pr.Spec.WorkspaceName) &&
			s.Attributes["verb"] == model.LabelValue(httpVerb) &&
			s.Attributes["operation"] == model.LabelValue(operationName) &&
			telemetry.OperationOutcomeValue(s.Attributes["operation_outcome"]) == expectedOutcome && ((expectedLifecycleAfter == "" && s.Attributes["lifecycle_after"] == "") ||
			porchapi.PackageRevisionLifecycle(s.Attributes["lifecycle_after"]) == expectedLifecycleAfter) {
			count++
		}
	}
	return count
}

// force metav1 import for compilation
var _ = metav1.Now
