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
