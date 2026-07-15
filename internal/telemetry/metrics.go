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

package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/klog/v2"
)

const meterName = "github.com/kptdev/porch"

var (
	apiCallDurationSeconds  metric.Float64Histogram
	RequestsTotal           metric.Float64Counter
	prResourceSizeHistogram metric.Int64Histogram
	prResourceSizeGauge     metric.Int64Gauge

	// Performance tests related vars
	perfOperationDuration           metric.Float64Histogram
	perfOperationCounter            metric.Float64Counter
	perfRepositoryCounter           metric.Float64Counter
	perfPackageCounter              metric.Float64Counter
	perfPackageRevisionCounter      metric.Float64Counter
	perfLifecycleTransitionDuration metric.Float64Histogram
	perfTestRunInfoGauge            metric.Float64Gauge
	perfActiveOperations            metric.Float64UpDownCounter
)

func InitMetrics() (err error) {
	m := otel.Meter(meterName)

	apiCallDurationSeconds, err = m.Float64Histogram(
		"porch_api_call_duration_seconds",
		metric.WithDescription("Duration of porch API calls in seconds."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(
			0.001, 0.002, 0.004, 0.008, 0.016, 0.032, 0.064, 0.128,
			0.256, 0.512, 1.024, 2.048, 4.096, 8.192, 16.384, 32.768,
		),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create porch_api_call_duration_seconds: %v", err))
	}

	RequestsTotal, err = m.Float64Counter(
		"porch_api_requests_by_user",
		metric.WithDescription("Total number of requests tracked by BurstCounter, broken down by resource, operation, and user."),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create porch_api_requests_by_user: %v", err))
	}

	prResourceSizeHistogram, err = m.Int64Histogram(
		"porch_package_size_bytes",
		metric.WithUnit("By"),
		metric.WithDescription("Distribution of package revision resources' file size, in bytes"),
		metric.WithExplicitBucketBoundaries(0, 1024, 2048, 4096, 8192, 16384, 32768, 65536, 131072, 262144, 524288, 1048576, 2097152, 4194304, 8388608, 16777216, 33554432, 67108864, 134217728, 268435456, 536870912, 1073741824),
	)
	if err != nil {
		klog.Errorf("failed to create porch_package_size_bytes histogram: %v", err)
		return
	}

	prResourceSizeGauge, err = m.Int64Gauge(
		"porch_package_size_bytes_total",
		metric.WithUnit("By"),
		metric.WithDescription("Total file size, in bytes, of a package revision's resources"),
	)
	if err != nil {
		klog.Errorf("failed to create porch_package_size_bytes gauge: %v", err)
		return
	}

	// Performance test related metrics
	perfOperationDuration, err = m.Float64Histogram(
		"porch_perf_operation_duration_seconds",
		metric.WithDescription("Duration of Porch performance test operations in seconds"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create porch_perf_operation_duration_seconds: %v", err))
	}

	perfOperationCounter, err = m.Float64Counter(
		"porch_perf_operations_total",
		metric.WithDescription("Total number of Porch performance test operations"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create porch_perf_operations_total: %v", err))
	}

	perfRepositoryCounter, err = m.Float64Counter(
		"porch_perf_repositories_created_total",
		metric.WithDescription("Total number of repositories created in performance tests"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create porch_perf_repositories_created_total: %v", err))
	}

	perfPackageCounter, err = m.Float64Counter(
		"porch_perf_packages_created_total",
		metric.WithDescription("Total number of packages created in performance tests"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create porch_perf_packages_created_total: %v", err))
	}

	perfPackageRevisionCounter, err = m.Float64Counter(
		"porch_perf_package_revisions_total",
		metric.WithDescription("Total number of package revisions created in performance tests"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create porch_perf_package_revisions_total: %v", err))
	}

	perfLifecycleTransitionDuration, err = m.Float64Histogram(
		"porch_perf_lifecycle_transition_duration_seconds",
		metric.WithDescription("Duration of package lifecycle transitions in seconds"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.1, 0.5, 1, 2, 5, 10, 30, 60),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create porch_perf_lifecycle_transition_duration_seconds: %v", err))
	}

	perfTestRunInfoGauge, err = m.Float64Gauge(
		"porch_perf_test_run_info",
		metric.WithDescription("Information about the current performance test run"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create porch_perf_test_run_info: %v", err))
	}

	perfActiveOperations, err = m.Float64UpDownCounter(
		"porch_perf_active_operations",
		metric.WithDescription("Number of currently active operations"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create porch_perf_active_operations: %v", err))
	}

	return nil
}

// Porch server and function runner metric recording functions
func RecordAPICallDuration(resource, verb string, durationSeconds float64) {
	if apiCallDurationSeconds == nil {
		return
	}
	apiCallDurationSeconds.Record(context.Background(), durationSeconds,
		metric.WithAttributes(
			attribute.String("resource", resource),
			attribute.String("verb", verb),
		),
	)
}

func RecordRequestCount(ctx context.Context, resource, op string) {
	if RequestsTotal == nil {
		return
	}
	user := getK8sUserName(ctx)
	RequestsTotal.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("resource", resource),
			attribute.String("op", op),
			attribute.String("user", user),
		),
	)
}

func RecordPackageRevisionResourcesSize(ctx context.Context, prKey repository.PackageRevisionKey, resourcesSize int64) {
	prPath := func() string {
		if prKey.PKey().Path != "" {
			return prKey.PKey().Path + "/"
		}
		return ""
	}()
	attributes := attribute.NewSet(
		attribute.String("namespace", prKey.RKey().Namespace),
		attribute.String("repository", prKey.RKey().Name),
		attribute.String("package", prPath+prKey.PKey().Package),
		attribute.String("workspace_name", prKey.WorkspaceName),
	)

	if prResourceSizeHistogram == nil {
		klog.Warning("prResourceSizeHistogram is nil - was InitMetrics() called?")
		return
	}

	if klog.V(3).Enabled() {
		klog.Infof(
			"Recording package resources size %dB for package revision with attributes %v",
			resourcesSize, attributes.MarshalLog())
	}

	prResourceSizeHistogram.Record(ctx, resourcesSize, metric.WithAttributeSet(attributes))

	if prResourceSizeGauge == nil {
		klog.Warning("prResourceSizeGauge is nil - was InitMetrics() called?")
		return
	}
	prResourceSizeGauge.Record(ctx, resourcesSize, metric.WithAttributeSet(attributes))
}

// Performance test metric recording functions
func PerfTestRecordMetric(operation, repoName, pkgName string, duration time.Duration, err error) {
	attrs := metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("repository", repoName),
		attribute.String("package", pkgName),
		attribute.String("status", statusLabel(err)),
	)
	ctx := context.Background()
	perfOperationDuration.Record(ctx, duration.Seconds(), attrs)
	perfOperationCounter.Add(ctx, 1, attrs)
}

func PerfTestRecordLifecycleTransition(fromState, toState, repoName, pkgName string, duration time.Duration, err error) {
	perfLifecycleTransitionDuration.Record(context.Background(), duration.Seconds(),
		metric.WithAttributes(
			attribute.String("from_state", fromState),
			attribute.String("to_state", toState),
			attribute.String("repository", repoName),
			attribute.String("package", pkgName),
			attribute.String("status", statusLabel(err)),
		),
	)
}

func PerfTestRecordPackageRevision(operation string, err error) {
	perfPackageRevisionCounter.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("operation", operation),
			attribute.String("status", statusLabel(err)),
		),
	)
}

func PerfTestSetTestRunInfo(testName, namespace string, startTime time.Time) {
	perfTestRunInfoGauge.Record(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("test_name", testName),
			attribute.String("namespace", namespace),
			attribute.String("start_time", startTime.Format(time.RFC3339)),
		),
	)
}

func PerfTestRecordActiveOperation(operation string, delta float64) {
	perfActiveOperations.Add(context.Background(), delta,
		metric.WithAttributes(attribute.String("operation", operation)),
	)
}

func PerfTestIncrementRepositoryCounter() {
	perfRepositoryCounter.Add(context.Background(), 1)
}

func PerfTestIncrementPackageCounter() {
	perfPackageCounter.Add(context.Background(), 1)
}

func statusLabel(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

func getK8sUserName(ctx context.Context) string {
	if user, ok := request.UserFrom(ctx); ok {
		return user.GetName()
	}
	return "<UNKNOWN>"
}
