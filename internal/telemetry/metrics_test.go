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
	"flag"
	"sort"
	"testing"
	"time"

	"github.com/kptdev/porch/api/porch/key"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/klog/v2"
)

// Remaining interface methods are not called by RecordPackageRevisionResourcesSize,
// so they can panic if invoked unexpectedly.

func TestRecordPackageRevisionResourcesSize_NilInstruments(t *testing.T) {
	require.NoError(t, InitMetrics())
	histogramBefore := prResourceSizeHistogram
	prResourceSizeHistogram = nil
	defer func() { prResourceSizeHistogram = histogramBefore }()

	fake :=
		key.PackageRevisionKey{
			PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "ns"}},
			WorkspaceName: "ws",
			Revision:      1,
		}
	// Should return early without panic
	assert.NotPanics(t, func() { RecordPackageRevisionResourcesSize(context.Background(), fake, 1024) })

	prResourceSizeHistogram = histogramBefore
	gaugeBefore := prResourceSizeGauge
	prResourceSizeGauge = nil
	defer func() { prResourceSizeGauge = gaugeBefore }()
	// The gauge is only touched in detailed mode; enable it so the nil-gauge
	// guard is actually exercised. Should still return early without panic.
	packageSizeDetailed = true
	defer func() { packageSizeDetailed = false }()
	assert.NotPanics(t, func() { RecordPackageRevisionResourcesSize(context.Background(), fake, 1024) })
}

func TestRecordAPICallDuration_IncludesAPIVersion(t *testing.T) {
	previousMp := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	defer func() {
		otel.SetMeterProvider(previousMp)
		mp.Shutdown(context.Background())
	}()

	require.NoError(t, InitMetrics())

	RecordControllerOperation(ResourcePackageRevision, "UPDATE", time.Now().Add(-time.Millisecond))

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	var foundDuration, foundRequest bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "porch_api_call_duration_seconds":
				foundDuration = true
			case "porch_api_requests_by_user":
				foundRequest = true
			}
		}
	}
	assert.True(t, foundDuration, "expected porch_api_call_duration_seconds to be recorded")
	assert.True(t, foundRequest, "expected porch_api_requests_by_user to be recorded")
}

func TestRecordPackageRevisionResourcesSize_RecordsMetrics(t *testing.T) {
	previousMp := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	defer func() {
		otel.SetMeterProvider(previousMp)
		mp.Shutdown(context.Background())
	}()

	require.NoError(t, InitMetrics())

	fake :=
		key.PackageRevisionKey{
			PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "test-ns"}},
			WorkspaceName: "ws",
			Revision:      1,
		}

	RecordPackageRevisionResourcesSize(context.Background(), fake, 4096)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	var foundHistogram, foundGauge bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "porch_package_size_bytes" {
				foundHistogram = true
			}
			if m.Name == "porch_package_size_bytes_total" {
				foundGauge = true
			}
		}
	}
	// By default (detailed mode off) the histogram is always recorded, but the
	// per-revision gauge is intentionally skipped to keep cardinality bounded.
	assert.True(t, foundHistogram, "expected porch_package_size_bytes histogram to be recorded")
	assert.False(t, foundGauge, "expected porch_package_size_bytes_total gauge to be skipped when detailed mode is off")

	// Enforce the low-cardinality contract directly: the histogram must carry
	// exactly the namespace attribute. If someone reintroduces the per-revision
	// labels (repository, package, workspace_name) this assertion fails even
	// though the metric is still "present".
	assert.Equal(t, []string{"namespace"}, histogramAttributeKeys(t, rm, "porch_package_size_bytes"),
		"porch_package_size_bytes histogram must be aggregated per namespace only")
}

func TestRecordPackageRevisionResourcesSize_Detailed(t *testing.T) {
	t.Setenv(packageSizeDetailedEnv, "true")

	previousMp := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	defer func() {
		otel.SetMeterProvider(previousMp)
		mp.Shutdown(context.Background())
	}()

	require.NoError(t, InitMetrics())
	t.Cleanup(func() { packageSizeDetailed = false })

	fake := key.PackageRevisionKey{
		PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "test-ns", Name: "repo"}, Package: "pkg"},
		WorkspaceName: "ws",
		Revision:      1,
	}

	RecordPackageRevisionResourcesSize(context.Background(), fake, 4096)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	var foundHistogram, foundGauge bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "porch_package_size_bytes" {
				foundHistogram = true
			}
			if m.Name == "porch_package_size_bytes_total" {
				foundGauge = true
			}
		}
	}
	assert.True(t, foundHistogram, "expected porch_package_size_bytes histogram to be recorded in detailed mode")
	assert.True(t, foundGauge, "expected porch_package_size_bytes_total gauge to be recorded in detailed mode")
}

func setupMetricsTestMeterProvider(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	previousMp := otel.GetMeterProvider()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		otel.SetMeterProvider(previousMp)
		mp.Shutdown(context.Background())
	})

	require.NoError(t, InitMetrics())
	return reader
}

func collectMetricData(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	return rm
}

func hasMetric(rm metricdata.ResourceMetrics, name string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return true
			}
		}
	}
	return false
}

// histogramAttributeKeys returns the sorted attribute keys present on the data
// points of the named Int64 histogram. It is used to assert the exact label
// set a metric carries, which guards the low-cardinality contract: a metric
// must not quietly gain high-cardinality labels (repository, package,
// workspace_name) without failing a test.
func histogramAttributeKeys(t *testing.T, rm metricdata.ResourceMetrics, name string) []string {
	t.Helper()

	keySet := map[string]struct{}{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[int64])
			require.Truef(t, ok, "metric %q is not an Int64 histogram", name)
			for _, dp := range hist.DataPoints {
				for _, kv := range dp.Attributes.ToSlice() {
					keySet[string(kv.Key)] = struct{}{}
				}
			}
		}
	}

	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestRecordAPICallDuration(t *testing.T) {
	reader := setupMetricsTestMeterProvider(t)

	RecordAPICallDuration(ResourcePackageRevision, "GET", APIVersionV1Alpha1, 0.25)

	rm := collectMetricData(t, reader)
	assert.True(t, hasMetric(rm, "porch_api_call_duration_seconds"))
}

func TestRecordAPICallDuration_NilInstrument(t *testing.T) {
	setupMetricsTestMeterProvider(t)

	before := apiCallDurationSeconds
	apiCallDurationSeconds = nil
	t.Cleanup(func() { apiCallDurationSeconds = before })

	assert.NotPanics(t, func() {
		RecordAPICallDuration(ResourcePackageRevision, "GET", APIVersionV1Alpha1, 0.5)
	})
}

func TestRecordRequestCount(t *testing.T) {
	reader := setupMetricsTestMeterProvider(t)

	ctx := request.WithUser(context.Background(), &user.DefaultInfo{Name: "test-user"})
	RecordRequestCount(ctx, ResourcePackageRevision, "LIST", APIVersionV1Alpha1)

	rm := collectMetricData(t, reader)
	assert.True(t, hasMetric(rm, "porch_api_requests_by_user"))
}

func TestRecordRequestCount_UnknownUser(t *testing.T) {
	reader := setupMetricsTestMeterProvider(t)

	RecordRequestCount(context.Background(), ResourcePackageRevision, "LIST", APIVersionV1Alpha1)

	rm := collectMetricData(t, reader)
	assert.True(t, hasMetric(rm, "porch_api_requests_by_user"))
}

func TestRecordRequestCount_NilInstrument(t *testing.T) {
	setupMetricsTestMeterProvider(t)

	before := requestsTotal
	requestsTotal = nil
	t.Cleanup(func() { requestsTotal = before })

	assert.NotPanics(t, func() {
		RecordRequestCount(context.Background(), ResourcePackageRevision, "LIST", APIVersionV1Alpha1)
	})
}

func TestRecordControllerRequestCount(t *testing.T) {
	reader := setupMetricsTestMeterProvider(t)

	RecordControllerRequestCount(ResourcePackageRevision, "UPDATE", APIVersionV1Alpha2)

	rm := collectMetricData(t, reader)
	assert.True(t, hasMetric(rm, "porch_api_requests_by_user"))
}

func TestRecordControllerRequestCount_NilInstrument(t *testing.T) {
	setupMetricsTestMeterProvider(t)

	before := requestsTotal
	requestsTotal = nil
	t.Cleanup(func() { requestsTotal = before })

	assert.NotPanics(t, func() {
		RecordControllerRequestCount(ResourcePackageRevision, "UPDATE", APIVersionV1Alpha2)
	})
}

func TestRecordExternalRepoOperation(t *testing.T) {
	reader := setupMetricsTestMeterProvider(t)

	ctx := request.WithUser(context.Background(), &user.DefaultInfo{Name: "git-user"})
	RecordExternalRepoOperation(ctx, "clone", time.Now().Add(-50*time.Millisecond))

	rm := collectMetricData(t, reader)
	assert.True(t, hasMetric(rm, "porch_api_call_duration_seconds"))
	assert.True(t, hasMetric(rm, "porch_api_requests_by_user"))
}

func TestRecordExternalRepoDuration_NilInstrument(t *testing.T) {
	setupMetricsTestMeterProvider(t)

	before := apiCallDurationSeconds
	apiCallDurationSeconds = nil
	t.Cleanup(func() { apiCallDurationSeconds = before })

	assert.NotPanics(t, func() {
		recordExternalRepoDuration("fetch", 1.0)
	})
}

func TestRecordExternalRepoRequestCount(t *testing.T) {
	reader := setupMetricsTestMeterProvider(t)

	ctx := request.WithUser(context.Background(), &user.DefaultInfo{Name: "git-user"})
	RecordExternalRepoRequestCount(ctx, "push")

	rm := collectMetricData(t, reader)
	assert.True(t, hasMetric(rm, "porch_api_requests_by_user"))
}

func TestRecordExternalRepoRequestCount_NilInstrument(t *testing.T) {
	setupMetricsTestMeterProvider(t)

	before := requestsTotal
	requestsTotal = nil
	t.Cleanup(func() { requestsTotal = before })

	ctx := request.WithUser(context.Background(), &user.DefaultInfo{Name: "git-user"})
	assert.NotPanics(t, func() {
		RecordExternalRepoRequestCount(ctx, "push")
	})
}

func TestRecordPackageRevisionResourcesSize_WithPath(t *testing.T) {
	// Path only affects the detailed per-revision gauge's "package" label, so
	// exercise detailed mode here.
	t.Setenv(packageSizeDetailedEnv, "true")
	reader := setupMetricsTestMeterProvider(t)
	t.Cleanup(func() { packageSizeDetailed = false })

	fake := key.PackageRevisionKey{
		PkgKey: key.PackageKey{
			RepoKey: key.RepositoryKey{Namespace: "test-ns", Name: "repo"},
			Path:    "configs",
			Package: "my-pkg",
		},
		WorkspaceName: "ws",
		Revision:      1,
	}

	RecordPackageRevisionResourcesSize(context.Background(), fake, 2048)

	rm := collectMetricData(t, reader)
	assert.True(t, hasMetric(rm, "porch_package_size_bytes"))
	assert.True(t, hasMetric(rm, "porch_package_size_bytes_total"))
}

func TestRecordPackageRevisionResourcesSize_VerboseLogging(t *testing.T) {
	reader := setupMetricsTestMeterProvider(t)
	klog.InitFlags(nil)
	require.NoError(t, flag.Set("v", "3"))

	fake := key.PackageRevisionKey{
		PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "test-ns"}},
		WorkspaceName: "ws",
		Revision:      1,
	}

	assert.NotPanics(t, func() {
		RecordPackageRevisionResourcesSize(context.Background(), fake, 512)
	})

	rm := collectMetricData(t, reader)
	assert.True(t, hasMetric(rm, "porch_package_size_bytes"))
}
