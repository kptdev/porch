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
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"k8s.io/klog/v2"
)

const perfMeterName = "github.com/kptdev/porch"

var (
	perfOperationDuration           metric.Float64Histogram
	perfOperationCounter            metric.Float64Counter
	perfRepositoryCounter           metric.Float64Counter
	perfPackageCounter              metric.Float64Counter
	perfPackageRevisionCounter      metric.Float64Counter
	perfLifecycleTransitionDuration metric.Float64Histogram
	perfTestRunInfoGauge            metric.Float64Gauge
	perfActiveOperations            metric.Float64UpDownCounter
)

// InitPerfMetrics registers OpenTelemetry instruments used by performance tests.
// Call after telemetry.SetupOpenTelemetry so the meter provider is configured.
func InitPerfMetrics() (err error) {
	m := otel.Meter(perfMeterName)

	perfOperationDuration, err = m.Float64Histogram(
		"porch_perf_operation_duration_seconds",
		metric.WithDescription("Duration of Porch performance test operations in seconds"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120),
	)
	if err != nil {
		klog.Errorf("failed to create porch_perf_operation_duration_seconds: %v", err)
		return
	}

	perfOperationCounter, err = m.Float64Counter(
		"porch_perf_operations_total",
		metric.WithDescription("Total number of Porch performance test operations"),
	)
	if err != nil {
		klog.Errorf("failed to create porch_perf_operations_total: %v", err)
		return
	}

	perfRepositoryCounter, err = m.Float64Counter(
		"porch_perf_repositories_created_total",
		metric.WithDescription("Total number of repositories created in performance tests"),
	)
	if err != nil {
		klog.Errorf("failed to create porch_perf_repositories_created_total: %v", err)
		return
	}

	perfPackageCounter, err = m.Float64Counter(
		"porch_perf_packages_created_total",
		metric.WithDescription("Total number of packages created in performance tests"),
	)
	if err != nil {
		klog.Errorf("failed to create porch_perf_packages_created_total: %v", err)
		return
	}

	perfPackageRevisionCounter, err = m.Float64Counter(
		"porch_perf_package_revisions_total",
		metric.WithDescription("Total number of package revisions created in performance tests"),
	)
	if err != nil {
		klog.Errorf("failed to create porch_perf_package_revisions_total: %v", err)
		return
	}

	perfLifecycleTransitionDuration, err = m.Float64Histogram(
		"porch_perf_lifecycle_transition_duration_seconds",
		metric.WithDescription("Duration of package lifecycle transitions in seconds"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.1, 0.5, 1, 2, 5, 10, 30, 60),
	)
	if err != nil {
		klog.Errorf("failed to create porch_perf_lifecycle_transition_duration_seconds: %v", err)
		return
	}

	perfTestRunInfoGauge, err = m.Float64Gauge(
		"porch_perf_test_run_info",
		metric.WithDescription("Information about the current performance test run"),
	)
	if err != nil {
		klog.Errorf("failed to create porch_perf_test_run_info: %v", err)
		return
	}

	perfActiveOperations, err = m.Float64UpDownCounter(
		"porch_perf_active_operations",
		metric.WithDescription("Number of currently active operations"),
	)
	if err != nil {
		klog.Errorf("failed to create porch_perf_active_operations: %v", err)
		return
	}

	return nil
}

func RecordPerfMetric(operation, apiVersion, repoName, pkgName string, duration time.Duration, err error) {
	if perfOperationDuration == nil {
		klog.Warning("perfOperationDuration is nil - was InitPerfMetrics() called?")
		return
	}
	if perfOperationCounter == nil {
		klog.Warning("perfOperationCounter is nil - was InitPerfMetrics() called?")
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("api_version", apiVersion),
		attribute.String("repository", repoName),
		attribute.String("package", pkgName),
		attribute.String("status", perfStatusLabel(err)),
	)
	ctx := context.Background()
	perfOperationDuration.Record(ctx, duration.Seconds(), attrs)
	perfOperationCounter.Add(ctx, 1, attrs)
}

func RecordPerfLifecycleTransition(fromState, toState, apiVersion, repoName, pkgName string, duration time.Duration, err error) {
	if perfLifecycleTransitionDuration == nil {
		klog.Warning("perfLifecycleTransitionDuration is nil - was InitPerfMetrics() called?")
		return
	}
	perfLifecycleTransitionDuration.Record(context.Background(), duration.Seconds(),
		metric.WithAttributes(
			attribute.String("from_state", fromState),
			attribute.String("to_state", toState),
			attribute.String("api_version", apiVersion),
			attribute.String("repository", repoName),
			attribute.String("package", pkgName),
			attribute.String("status", perfStatusLabel(err)),
		),
	)
}

func RecordPerfPackageRevision(operation string, err error) {
	if perfPackageRevisionCounter == nil {
		klog.Warning("perfPackageRevisionCounter is nil - was InitPerfMetrics() called?")
		return
	}
	perfPackageRevisionCounter.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("operation", operation),
			attribute.String("status", perfStatusLabel(err)),
		),
	)
}

func SetPerfTestRunInfo(testName, namespace, apiVersion string, startTime time.Time) {
	if perfTestRunInfoGauge == nil {
		klog.Warning("perfTestRunInfoGauge is nil - was InitPerfMetrics() called?")
		return
	}
	perfTestRunInfoGauge.Record(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("test_name", testName),
			attribute.String("namespace", namespace),
			attribute.String("api_version", apiVersion),
			attribute.String("start_time", startTime.Format(time.RFC3339)),
		),
	)
}

func RecordPerfActiveOperation(operation, apiVersion string, delta float64) {
	if perfActiveOperations == nil {
		klog.Warning("perfActiveOperations is nil - was InitPerfMetrics() called?")
		return
	}
	perfActiveOperations.Add(context.Background(), delta,
		metric.WithAttributes(
			attribute.String("operation", operation),
			attribute.String("api_version", apiVersion),
		),
	)
}

func IncrementPerfRepositoryCounter() {
	if perfRepositoryCounter == nil {
		klog.Warning("perfRepositoryCounter is nil - was InitPerfMetrics() called?")
		return
	}
	perfRepositoryCounter.Add(context.Background(), 1)
}

func IncrementPerfPackageCounter() {
	if perfPackageCounter == nil {
		klog.Warning("perfPackageCounter is nil - was InitPerfMetrics() called?")
		return
	}
	perfPackageCounter.Add(context.Background(), 1)
}

func perfStatusLabel(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}
