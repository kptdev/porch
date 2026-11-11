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
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestNewPrometheusMetricsServerSetsReadHeaderTimeout(t *testing.T) {
	const port = 0

	pms := NewPrometheusMetricsServer(port)

	require.Equal(t, 10*time.Second, pms.server.ReadHeaderTimeout)
}

func TestRecordPackageRevisionResourcesSizeRecordsHistogramAndGauge(t *testing.T) {
	// given a package revision whose resources total 4096 bytes
	ns := t.Name()
	const size int64 = 4096

	// when the size is recorded
	RecordPackageRevisionResourcesSize(ns, "repo", "pkg", "ws", size)

	// then the histogram and gauge expose that size for the package labels
	require.Equal(t, float64(size), testutil.ToFloat64(packageSizeBytesTotal.WithLabelValues(ns, "repo", "pkg", "ws")))
	count, sum := histogramSample(t, packageSizeBytes, ns, "repo", "pkg", "ws")
	require.Equal(t, uint64(1), count)
	require.Equal(t, float64(size), sum)
}

func TestRecordPackageRevisionResourcesSizeRecordsNestedPackageLabel(t *testing.T) {
	// given a nested package label as produced by the DB cache
	ns := t.Name()

	// when the size is recorded
	RecordPackageRevisionResourcesSize(ns, "repo", "team/app/pkg", "ws", 1024)

	// then the gauge is labeled with the path-prefixed package name
	require.Equal(t, 1024.0, testutil.ToFloat64(packageSizeBytesTotal.WithLabelValues(ns, "repo", "team/app/pkg", "ws")))
}

func TestRecordPackageRevisionResourcesSizeRecordsZeroOnDelete(t *testing.T) {
	// given a package revision that has been deleted
	ns := t.Name()

	// when a zero size is recorded
	RecordPackageRevisionResourcesSize(ns, "repo", "pkg", "ws", 0)

	// then the gauge is cleared and the histogram still counts the observation
	require.Equal(t, 0.0, testutil.ToFloat64(packageSizeBytesTotal.WithLabelValues(ns, "repo", "pkg", "ws")))
	count, sum := histogramSample(t, packageSizeBytes, ns, "repo", "pkg", "ws")
	require.Equal(t, uint64(1), count)
	require.Equal(t, 0.0, sum)
}

func TestPrometheusMetricsServerServesMetricsAndStops(t *testing.T) {
	port := freeTCPPort(t)
	pms := NewPrometheusMetricsServer(port)

	require.NoError(t, pms.Start())

	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", port))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond)
	require.NoError(t, pms.StopWithTimeout(time.Second))
}

func TestPrometheusMetricsServerStartInWGServesMetricsAndStops(t *testing.T) {
	port := freeTCPPort(t)
	pms := NewPrometheusMetricsServer(port)
	var wg sync.WaitGroup

	pms.StartInWG(&wg)

	require.Eventually(t, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", port))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond)
	require.NoError(t, pms.Stop(t.Context()))
	wg.Wait()
}

func TestRecordAPICallDurationObservesHistogram(t *testing.T) {
	resource, verb := t.Name(), "GET"

	RecordAPICallDuration(resource, verb, 0.25)

	count, sum := histogramSample(t, apiCallDurationSeconds, resource, verb)
	require.Equal(t, uint64(1), count)
	require.Equal(t, 0.25, sum)
}

func TestRecordQueueSizeObservesHistogram(t *testing.T) {
	queue := t.Name()

	RecordQueueSize(queue, 7)

	count, sum := histogramSample(t, queueSize, queue)
	require.Equal(t, uint64(1), count)
	require.Equal(t, 7.0, sum)
}

func TestPerfTestRecordMetricRecordsSuccessAndError(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		err            error
		expectedStatus string
	}{
		"success": {expectedStatus: "success"},
		"error":   {err: fmt.Errorf("boom"), expectedStatus: "error"},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			operation, repo, pkg := t.Name(), "repo", "pkg"

			PerfTestRecordMetric(operation, repo, pkg, 150*time.Millisecond, tc.err)

			require.Equal(t, 1.0, testutil.ToFloat64(operationCounter.WithLabelValues(operation, repo, pkg, tc.expectedStatus)))
			count, _ := histogramSample(t, operationDuration, operation, repo, pkg, tc.expectedStatus)
			require.Equal(t, uint64(1), count)
		})
	}
}

func TestPerfTestRecordLifecycleTransitionRecordsStatus(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		err            error
		expectedStatus string
	}{
		"success": {expectedStatus: "success"},
		"error":   {err: fmt.Errorf("transition failed"), expectedStatus: "error"},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fromState, toState := "Draft", t.Name()
			repo, pkg := "repo", "pkg"

			PerfTestRecordLifecycleTransition(fromState, toState, repo, pkg, time.Second, tc.err)

			count, _ := histogramSample(t, lifecycleTransitionDuration, fromState, toState, repo, pkg, tc.expectedStatus)
			require.Equal(t, uint64(1), count)
		})
	}
}

func TestPerfTestRecordPackageRevisionIncrementsCounter(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		err            error
		expectedStatus string
	}{
		"success": {expectedStatus: "success"},
		"error":   {err: fmt.Errorf("create failed"), expectedStatus: "error"},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			operation := t.Name()

			PerfTestRecordPackageRevision(operation, tc.err)

			require.Equal(t, 1.0, testutil.ToFloat64(packageRevisionCounter.WithLabelValues(operation, tc.expectedStatus)))
		})
	}
}

func TestPerfTestSetTestRunInfoSetsGauge(t *testing.T) {
	testName, namespace := t.Name(), "perf-ns"
	startTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	PerfTestSetTestRunInfo(testName, namespace, startTime)

	require.Equal(t, 1.0, testutil.ToFloat64(testRunInfo.WithLabelValues(testName, namespace, startTime.Format(time.RFC3339))))
}

func TestPerfTestRecordActiveOperationAddsDelta(t *testing.T) {
	operation := t.Name()

	PerfTestRecordActiveOperation(operation, 2)

	require.Equal(t, 2.0, testutil.ToFloat64(activeOperations.WithLabelValues(operation)))
}

func TestPerfTestIncrementRepositoryCounterIncrements(t *testing.T) {
	before := testutil.ToFloat64(repositoryCounter)

	PerfTestIncrementRepositoryCounter()

	require.Equal(t, before+1, testutil.ToFloat64(repositoryCounter))
}

func TestPerfTestIncrementPackageCounterIncrements(t *testing.T) {
	before := testutil.ToFloat64(packageCounter)

	PerfTestIncrementPackageCounter()

	require.Equal(t, before+1, testutil.ToFloat64(packageCounter))
}

func TestRecordBurstRequestCountIncrementsCounter(t *testing.T) {
	resource, op, user := t.Name(), "update", "alice"

	RecordBurstRequestCount(resource, op, user)

	require.Equal(t, 1.0, testutil.ToFloat64(burstRequestsTotal.WithLabelValues(resource, op, user)))
}

func TestSetShardMisconfiguredTogglesGauge(t *testing.T) {
	testCases := map[string]struct {
		misconfigured bool
		expected      float64
	}{
		"misconfigured": {misconfigured: true, expected: 1},
		"configured":    {misconfigured: false, expected: 0},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			SetShardMisconfigured(tc.misconfigured)

			require.Equal(t, tc.expected, testutil.ToFloat64(shardMisconfigured))
		})
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func histogramSample(t *testing.T, vec *prometheus.HistogramVec, labelValues ...string) (uint64, float64) {
	t.Helper()
	observer := vec.WithLabelValues(labelValues...)
	metric, ok := observer.(prometheus.Metric)
	require.True(t, ok)

	pb := &dto.Metric{}
	require.NoError(t, metric.Write(pb))
	require.NotNil(t, pb.Histogram)
	return pb.Histogram.GetSampleCount(), pb.Histogram.GetSampleSum()
}
