// Copyright 2026 The kpt and Nephio Authors
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

package metrics

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	//Porch server and function runner metrics
	apiCallDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "api_call_duration_seconds",
			Help:    "Duration of porch API calls in seconds.",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 16),
		},
		[]string{"resource", "verb"},
	)

	queueSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "porch_queue_size",
			Help:    "Observed size of various work queues in Porch.",
			Buckets: prometheus.LinearBuckets(0, 2, 50),
		},
		[]string{"queue"},
	)

	packageSizeBytes = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "porch_package_size_bytes",
			Help:    "Distribution of package revision resources' file size, in bytes",
			Buckets: append([]float64{0}, prometheus.ExponentialBuckets(1024, 2, 21)...),
		},
		[]string{"namespace", "repository", "package", "workspace_name"},
	)

	packageSizeBytesTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "porch_package_size_bytes_total",
			Help: "Total file size, in bytes, of a package revision's resources",
		},
		[]string{"namespace", "repository", "package", "workspace_name"},
	)

	//Porch performance test metrics
	operationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "porch_perf_operation_duration_seconds",
			Help:    "Duration of Porch performance test operations in seconds",
			Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120},
		},
		[]string{"operation", "repository", "package", "status"},
	)

	operationCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "porch_perf_operations_total",
			Help: "Total number of Porch performance test operations",
		},
		[]string{"operation", "repository", "package", "status"},
	)

	repositoryCounter = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "porch_perf_repositories_created_total",
			Help: "Total number of repositories created in performance tests",
		},
	)

	packageCounter = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "porch_perf_packages_created_total",
			Help: "Total number of packages created in performance tests",
		},
	)

	packageRevisionCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "porch_perf_package_revisions_total",
			Help: "Total number of package revisions created in performance tests",
		},
		[]string{"operation", "status"},
	)

	lifecycleTransitionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "porch_perf_lifecycle_transition_duration_seconds",
			Help:    "Duration of package lifecycle transitions in seconds",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
		},
		[]string{"from_state", "to_state", "repository", "package", "status"},
	)

	testRunInfo = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "porch_perf_test_run_info",
			Help: "Information about the current performance test run",
		},
		[]string{"test_name", "namespace", "start_time"},
	)

	activeOperations = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "porch_perf_active_operations",
			Help: "Number of currently active operations",
		},
		[]string{"operation"},
	)

	burstRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "porch_api_requests_by_user",
			Help: "Total number of requests tracked by BurstCounter, broken down by resource, operation, and user.",
		},
		[]string{"resource", "op", "user"},
	)

	shardMisconfigured = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "porch_shard_misconfigured",
			Help: "1 when this porch-server shard's --shard-count disagrees with StatefulSet.spec.replicas, else 0.",
		},
	)
)

type PrometheusMetricsServer struct {
	server *http.Server
	port   int
}

func NewPrometheusMetricsServer(port int) *PrometheusMetricsServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	return &PrometheusMetricsServer{
		server: server,
		port:   port,
	}
}

func (pms *PrometheusMetricsServer) Start() error {
	go func() {
		if err := pms.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Prometheus metrics server error: %v\n", err)
		}
	}()
	fmt.Printf("Prometheus metrics server started on port %d\n", pms.port)
	return nil
}

func (pms *PrometheusMetricsServer) StartInWG(wg *sync.WaitGroup) {
	wg.Go(func() {
		if err := pms.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Prometheus metrics server error: %v\n", err)
		}
	})
	fmt.Printf("Prometheus metrics server started on port %d\n", pms.port)
}

func (pms *PrometheusMetricsServer) Stop(ctx context.Context) error {
	fmt.Println("Shutting down Prometheus metrics server...")
	return pms.server.Shutdown(ctx)
}

func (pms *PrometheusMetricsServer) StopWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return pms.Stop(ctx)
}

// Porch server and function runner metric recording functions
func RecordAPICallDuration(resource, verb string, durationSeconds float64) {
	apiCallDurationSeconds.WithLabelValues(resource, verb).Observe(durationSeconds)
}

func RecordQueueSize(queue string, size float64) {
	queueSize.WithLabelValues(queue).Observe(size)
}

func RecordPackageRevisionResourcesSize(namespace, repoName, packageName, workspaceName string, resourcesSize int64) {
	packageSizeBytes.WithLabelValues(namespace, repoName, packageName, workspaceName).Observe(float64(resourcesSize))
	packageSizeBytesTotal.WithLabelValues(namespace, repoName, packageName, workspaceName).Set(float64(resourcesSize))
}

// Performance test metric recording functions
func PerfTestRecordMetric(operation, repoName, pkgName string, duration time.Duration, err error) {
	status := "success"
	if err != nil {
		status = "error"
	}

	operationDuration.WithLabelValues(operation, repoName, pkgName, status).Observe(duration.Seconds())
	operationCounter.WithLabelValues(operation, repoName, pkgName, status).Inc()
}

func PerfTestRecordLifecycleTransition(fromState, toState, repoName, pkgName string, duration time.Duration, err error) {
	status := "success"
	if err != nil {
		status = "error"
	}

	lifecycleTransitionDuration.WithLabelValues(fromState, toState, repoName, pkgName, status).Observe(duration.Seconds())
}

func PerfTestRecordPackageRevision(operation string, err error) {
	status := "success"
	if err != nil {
		status = "error"
	}
	packageRevisionCounter.WithLabelValues(operation, status).Inc()
}

func PerfTestSetTestRunInfo(testName, namespace string, startTime time.Time) {
	testRunInfo.WithLabelValues(testName, namespace, startTime.Format(time.RFC3339)).Set(1)
}

func PerfTestRecordActiveOperation(operation string, delta float64) {
	activeOperations.WithLabelValues(operation).Add(delta)
}

func PerfTestIncrementRepositoryCounter() {
	repositoryCounter.Inc()
}

func PerfTestIncrementPackageCounter() {
	packageCounter.Inc()
}

func RecordBurstRequestCount(resource, op, user string) {
	burstRequestsTotal.WithLabelValues(resource, op, user).Inc()
}

func SetShardMisconfigured(misconfigured bool) {
	if misconfigured {
		shardMisconfigured.Set(1)
		return
	}
	shardMisconfigured.Set(0)
}
