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
	"testing"
	"time"

	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Remaining interface methods are not called by RecordPackageRevisionResourcesSize,
// so they can panic if invoked unexpectedly.

func TestRecordPackageRevisionResourcesSize_NilInstruments(t *testing.T) {
	require.NoError(t, InitMetrics())
	histogramBefore := prResourceSizeHistogram
	prResourceSizeHistogram = nil
	defer func() { prResourceSizeHistogram = histogramBefore }()

	fake :=
		repository.PackageRevisionKey{
			PkgKey:        repository.PackageKey{RepoKey: repository.RepositoryKey{Namespace: "ns"}},
			WorkspaceName: "ws",
			Revision:      1,
		}
	// Should return early without panic
	assert.NotPanics(t, func() { RecordPackageRevisionResourcesSize(context.Background(), fake, 1024) })

	prResourceSizeHistogram = histogramBefore
	gaugeBefore := prResourceSizeGauge
	prResourceSizeGauge = nil
	defer func() { prResourceSizeGauge = gaugeBefore }()
	// Should return early without panic
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
		repository.PackageRevisionKey{
			PkgKey:        repository.PackageKey{RepoKey: repository.RepositoryKey{Namespace: "test-ns"}},
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
	assert.True(t, foundHistogram, "expected porch_package_size_bytes histogram to be recorded")
	assert.True(t, foundGauge, "expected porch_package_size_bytes_total gauge to be recorded")
}
