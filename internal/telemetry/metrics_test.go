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
	"errors"
	"flag"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/kptdev/porch/api/porch"
	"github.com/kptdev/porch/api/porch/key"
	"github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/klog/v2"
)

const (
	durationMetric = "porch_api_call_duration_seconds"
	inFlightMetric = "porch_in_flight_api_operations"
)

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

	op := Operations.Update
	key, _ := key.PkgRevK8sName2Key("dummyNamespace", "dummyPkgrevName")
	recordControllerOperation(context.Background(), ResourcePackageRevision, op.AllCaps, op.TitleCase+"AsATest", time.Since(time.Now().Add(-time.Millisecond)), nil, "Draft", &key)

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

	testDuration, _ := time.ParseDuration("0.25s")
	recordDuration := time.Since(time.Now().Add(-testDuration))

	RecordAPIOperationDuration(context.Background(), ResourcePackageRevision, "GET", "GetPackageRevision", APIVersionV1Alpha1, recordDuration, nil, "Draft", nil)

	rm := collectMetricData(t, reader)
	assert.True(t, hasMetric(rm, "porch_api_call_duration_seconds"))
}

func TestRecordAPICallDuration_NilInstrument(t *testing.T) {
	setupMetricsTestMeterProvider(t)

	before := porchApiOpDurationSeconds
	porchApiOpDurationSeconds = nil
	t.Cleanup(func() { porchApiOpDurationSeconds = before })

	testDuration, _ := time.ParseDuration("0.5s")
	recordDuration := time.Since(time.Now().Add(-testDuration))

	assert.NotPanics(t, func() {

		RecordAPIOperationDuration(context.Background(), ResourcePackageRevision, "GET", "GetPackageRevision", APIVersionV1Alpha1, recordDuration, nil, "Draft", nil)
	})
}

func TestRecordRequestCount(t *testing.T) {
	reader := setupMetricsTestMeterProvider(t)

	ctx := request.WithUser(context.Background(), &user.DefaultInfo{Name: "test-user"})
	RecordServerRequestCount(ctx, ResourcePackageRevision, "LIST")

	rm := collectMetricData(t, reader)
	assert.True(t, hasMetric(rm, "porch_api_requests_by_user"))
}

func TestRecordRequestCount_UnknownUser(t *testing.T) {
	reader := setupMetricsTestMeterProvider(t)

	RecordServerRequestCount(context.Background(), ResourcePackageRevision, "LIST")

	rm := collectMetricData(t, reader)
	assert.True(t, hasMetric(rm, "porch_api_requests_by_user"))
}

func TestRecordRequestCount_NilInstrument(t *testing.T) {
	setupMetricsTestMeterProvider(t)

	before := requestsTotal
	requestsTotal = nil
	t.Cleanup(func() { requestsTotal = before })

	assert.NotPanics(t, func() {
		RecordServerRequestCount(context.Background(), ResourcePackageRevision, "LIST")
	})
}

func TestRecordControllerRequestCount(t *testing.T) {
	reader := setupMetricsTestMeterProvider(t)

	RecordControllerRequestCount(t.Context(), ResourcePackageRevision, "UPDATE")

	rm := collectMetricData(t, reader)
	assert.True(t, hasMetric(rm, "porch_api_requests_by_user"))
}

func TestRecordControllerRequestCount_NilInstrument(t *testing.T) {
	setupMetricsTestMeterProvider(t)

	before := requestsTotal
	requestsTotal = nil
	t.Cleanup(func() { requestsTotal = before })

	assert.NotPanics(t, func() {
		RecordControllerRequestCount(t.Context(), ResourcePackageRevision, "UPDATE")
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

	before := porchApiOpDurationSeconds
	porchApiOpDurationSeconds = nil
	t.Cleanup(func() { porchApiOpDurationSeconds = before })

	assert.NotPanics(t, func() {
		recordExternalRepoDuration(t.Context(), "fetch", 1.0)
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

type point struct {
	attrs map[string]string
	value float64
}

func attrsOf(kvs []attribute.KeyValue) map[string]string {
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		m[string(kv.Key)] = kv.Value.Emit()
	}
	return m
}

// pointsOf returns the data points of the named float64 histogram or int64 sum.
func pointsOf(t *testing.T, rm metricdata.ResourceMetrics, name string) []point {
	t.Helper()

	var points []point
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			switch data := m.Data.(type) {
			case metricdata.Histogram[float64]:
				for _, dp := range data.DataPoints {
					points = append(points, point{attrs: attrsOf(dp.Attributes.ToSlice()), value: float64(dp.Count)})
				}
			case metricdata.Sum[int64]:
				for _, dp := range data.DataPoints {
					points = append(points, point{attrs: attrsOf(dp.Attributes.ToSlice()), value: float64(dp.Value)})
				}
			default:
				require.Failf(t, "unexpected data type", "metric %q has type %T", name, m.Data)
			}
		}
	}
	return points
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// setAPIOperationsDetailed sets PORCH_API_OPERATIONS_DETAILED for the test. InitMetrics reads it,
// so call this before setupMetricsTestMeterProvider. The package flag is reset afterwards.
func setAPIOperationsDetailed(t *testing.T, value string) {
	t.Helper()
	t.Setenv(apiOpsDetailedEnv, value)
	t.Cleanup(func() { apiOpsDetailed = false })
}

func TestMeasureAPIOperation(t *testing.T) {
	prKey := key.PackageRevisionKey{
		PkgKey: key.PackageKey{
			RepoKey: key.RepositoryKey{Namespace: "ns", Name: "repo"},
			Path:    "cfg",
			Package: "pkg",
		},
		WorkspaceName: "ws",
		Revision:      1,
	}
	draft := porch.PackageRevisionLifecycle("Draft")
	proposed := porch.PackageRevisionLifecycle("Proposed")

	// The exact label sets are part of the cardinality contract: the aggregated sets must
	// stay free of per-package-revision attributes.
	aggregatedDuration := []string{"api_version", "operation", "operation_outcome", "resource", "verb"}
	detailedDuration := []string{"api_version", "lifecycle_after", "namespace", "operation", "operation_outcome", "package", "repository",
		"resource", "verb", "workspace_name"}
	aggregatedInFlight := []string{"api_version", "operation", "resource", "verb"}
	detailedInFlight := []string{"api_version", "desired_lifecycle", "namespace", "operation", "package", "repository", "resource", "verb",
		"workspace_name"}

	tests := []struct {
		name           string
		detailed       bool
		err            error
		nilErrPtr      bool
		lifecycleAfter func() porch.PackageRevisionLifecycle
		wantOutcome    string
		wantLifecycle  string // lifecycle_after; detailed mode only
	}{
		{
			name: "success reuses the desired lifecycle", detailed: true,
			wantOutcome: "success", wantLifecycle: "Draft",
		},
		{
			name: "lifecycleAfter overrides the desired lifecycle", detailed: true,
			lifecycleAfter: func() porch.PackageRevisionLifecycle { return proposed },
			wantOutcome:    "success", wantLifecycle: "Proposed",
		},
		{
			name: "an error assigned after the operation started is recorded", detailed: true,
			err:            errors.New("boom"),
			lifecycleAfter: func() porch.PackageRevisionLifecycle { return "" },
			wantOutcome:    "error", wantLifecycle: "",
		},
		{
			name: "a nil error pointer is a success", detailed: true, nilErrPtr: true,
			wantOutcome: "success", wantLifecycle: "Draft",
		},
		{
			name: "aggregated mode drops lifecycle and package revision attributes", detailed: false,
			wantOutcome: "success",
		},
		{
			name: "aggregated mode still distinguishes errors", detailed: false,
			err:         errors.New("boom"),
			wantOutcome: "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setAPIOperationsDetailed(t, strconv.FormatBool(tt.detailed))
			reader := setupMetricsTestMeterProvider(t)

			finish := MeasureAPIOperation(context.Background(), Operations.Create, ResourcePackageRevision, APIVersionV1Alpha1, draft, &prKey)

			// Between start and finish the operation is in flight and nothing has been timed.
			rm := collectMetricData(t, reader)
			inFlight := pointsOf(t, rm, inFlightMetric)
			require.Len(t, inFlight, 1)
			assert.EqualValues(t, 1, inFlight[0].value, "the operation must be in flight until finish is called")
			assert.Empty(t, pointsOf(t, rm, durationMetric), "no duration may be recorded before finish")

			// err is assigned after the operation started: finish must read it through the pointer.
			var err error
			err = tt.err
			errPtr := &err
			if tt.nilErrPtr {
				errPtr = nil
			}
			finish(errPtr, tt.lifecycleAfter)

			rm = collectMetricData(t, reader)

			durations := pointsOf(t, rm, durationMetric)
			require.Len(t, durations, 1)
			d := durations[0]
			assert.EqualValues(t, 1, d.value)
			assert.Equal(t, "CREATE", d.attrs["verb"])
			assert.Equal(t, "CreatePackageRevision", d.attrs["operation"])
			assert.Equal(t, ResourcePackageRevision, d.attrs["resource"])
			assert.Equal(t, APIVersionV1Alpha1, d.attrs["api_version"])
			assert.Equal(t, tt.wantOutcome, d.attrs["operation_outcome"])

			inFlight = pointsOf(t, rm, inFlightMetric)
			require.Len(t, inFlight, 1)
			assert.EqualValues(t, 0, inFlight[0].value, "finish must release the in-flight count")

			if !tt.detailed {
				assert.Equal(t, aggregatedDuration, sortedKeys(d.attrs))
				assert.Equal(t, aggregatedInFlight, sortedKeys(inFlight[0].attrs))
				return
			}
			assert.Equal(t, detailedDuration, sortedKeys(d.attrs))
			assert.Equal(t, tt.wantLifecycle, d.attrs["lifecycle_after"])
			assert.Equal(t, "ns", d.attrs["namespace"])
			assert.Equal(t, "repo", d.attrs["repository"])
			assert.Equal(t, "cfg/pkg", d.attrs["package"])
			assert.Equal(t, "ws", d.attrs["workspace_name"])

			assert.Equal(t, detailedInFlight, sortedKeys(inFlight[0].attrs))
			assert.Equal(t, "Draft", inFlight[0].attrs["desired_lifecycle"], "in-flight carries the desired, not the resulting, lifecycle")
		})
	}
}

func TestMeasureAPIOperation_NilInstruments(t *testing.T) {
	setupMetricsTestMeterProvider(t)

	durationBefore, inFlightBefore := porchApiOpDurationSeconds, porchInFlightApiOps
	porchApiOpDurationSeconds, porchInFlightApiOps = nil, nil
	t.Cleanup(func() { porchApiOpDurationSeconds, porchInFlightApiOps = durationBefore, inFlightBefore })

	assert.NotPanics(t, func() {
		var err error
		MeasureAPIOperation(context.Background(), Operations.Get, ResourcePackageRevision, APIVersionV1Alpha1, "", nil)(&err, nil)
	})
}

// With no package revision key the only per-request attribute available is the namespace
// of the request context, and it is exported in detailed mode only.
func TestTrackInFlightOperation_NamespaceFromContext(t *testing.T) {
	for _, detailed := range []bool{true, false} {
		t.Run("detailed="+strconv.FormatBool(detailed), func(t *testing.T) {
			setAPIOperationsDetailed(t, strconv.FormatBool(detailed))
			reader := setupMetricsTestMeterProvider(t)

			ctx := request.WithNamespace(context.Background(), "ctx-ns")
			op := Operations.List
			stop := TrackInFlightOperation(ctx, ResourcePackageRevision, op.AllCaps, op.TitleCase+ResourcePackageRevision, APIVersionV1Alpha1,
				"", nil)

			inFlight := pointsOf(t, collectMetricData(t, reader), inFlightMetric)
			require.Len(t, inFlight, 1)
			assert.EqualValues(t, 1, inFlight[0].value)
			assert.NotContains(t, inFlight[0].attrs, "repository")
			assert.NotContains(t, inFlight[0].attrs, "package")
			if detailed {
				assert.Equal(t, "ctx-ns", inFlight[0].attrs["namespace"])
				assert.Contains(t, inFlight[0].attrs, "desired_lifecycle")
			} else {
				assert.NotContains(t, inFlight[0].attrs, "namespace")
				assert.NotContains(t, inFlight[0].attrs, "desired_lifecycle")
			}

			stop()
			inFlight = pointsOf(t, collectMetricData(t, reader), inFlightMetric)
			require.Len(t, inFlight, 1)
			assert.EqualValues(t, 0, inFlight[0].value)
		})
	}
}

func TestTrackInFlightOperation_NilInstrument(t *testing.T) {
	setupMetricsTestMeterProvider(t)

	before := porchInFlightApiOps
	porchInFlightApiOps = nil
	t.Cleanup(func() { porchInFlightApiOps = before })

	assert.NotPanics(t, func() {
		stop := TrackInFlightOperation(context.Background(), ResourcePackageRevision, "GET", "GetPackageRevision", APIVersionV1Alpha1, "",
			nil)
		stop()
	})
}

func TestInitMetrics_APIOperationsDetailedEnv(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"false", false},
		{"0", false},
		{"yes", false}, // not understood by strconv.ParseBool, so treated as off
		{"garbage", false},
	}
	for _, tt := range tests {
		t.Run("value="+tt.value, func(t *testing.T) {
			setAPIOperationsDetailed(t, tt.value)
			setupMetricsTestMeterProvider(t)
			assert.Equal(t, tt.want, apiOpsDetailed)
		})
	}
}

func TestPerfStatusLabel(t *testing.T) {
	assert.Equal(t, OperationOutcomes.Success, perfStatusLabel(nil))
	assert.Equal(t, OperationOutcomes.Error, perfStatusLabel(errors.New("boom")))
}

func TestParsePkgRevK8sName2Key(t *testing.T) {
	// The revision number is not part of the object name, so it round-trips as 0.
	want := key.PackageRevisionKey{
		PkgKey: key.PackageKey{
			RepoKey: key.RepositoryKey{Namespace: "ns", Name: "repo"},
			Path:    "cfg",
			Package: "pkg",
		},
		WorkspaceName: "ws",
	}
	assert.Equal(t, want, ParsePkgRevK8sName2Key("ns", want.K8SName()))

	// A name that is not a valid URL yields the zero key rather than an error or a panic.
	assert.Equal(t, key.PackageRevisionKey{}, ParsePkgRevK8sName2Key("ns", "%zz"))
}

// recordControllerOperation records duration and request count for a v1alpha2 controller operation.
func recordControllerOperation(ctx context.Context, resource, verb, porchOperation string, duration time.Duration, err error, lifecycleAfter v1alpha2.PackageRevisionLifecycle, prKey *key.PackageRevisionKey) {
	RecordAPIOperationDuration(ctx, resource, verb, porchOperation, APIVersionV1Alpha2, duration, err, porch.PackageRevisionLifecycle(lifecycleAfter), prKey)
	RecordControllerRequestCount(ctx, resource, verb)
}
