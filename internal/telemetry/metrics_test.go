package telemetry

import (
	"context"
	"testing"

	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type fakePackageRevision struct {
	repository.PackageRevision
	key       repository.PackageRevisionKey
	namespace string
}

func (f *fakePackageRevision) KubeObjectNamespace() string        { return f.namespace }
func (f *fakePackageRevision) Key() repository.PackageRevisionKey { return f.key }

// Remaining interface methods are not called by RecordPackageSizeUpdate,
// so they can panic if invoked unexpectedly.

func TestRecordPackageSizeUpdate_NilInstruments(t *testing.T) {
	InitMetrics()
	histogramBefore := prrResourceSizeHistogram
	prrResourceSizeHistogram = nil
	defer func() { prrResourceSizeHistogram = histogramBefore }()

	fake := &fakePackageRevision{
		namespace: "ns",
		key: repository.PackageRevisionKey{
			WorkspaceName: "ws",
			Revision:      1,
		},
	}
	// Should return early without panic
	assert.NotPanics(t, func() { RecordPackageSizeUpdate(fake, 1024) })

	prrResourceSizeHistogram = histogramBefore
	gaugeBefore := prrResourceSizeGauge
	prrResourceSizeGauge = nil
	defer func() { prrResourceSizeGauge = gaugeBefore }()
	// Should return early without panic
	assert.NotPanics(t, func() { RecordPackageSizeUpdate(fake, 1024) })
}

func TestRecordPackageSizeUpdate_RecordsMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	defer mp.Shutdown(context.Background())

	InitMetrics()

	fake := &fakePackageRevision{
		namespace: "test-ns",
		key: repository.PackageRevisionKey{
			WorkspaceName: "ws",
			Revision:      1,
		},
	}

	RecordPackageSizeUpdate(fake, 4096)

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
