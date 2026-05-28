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

package telemetry

import (
	"context"
	"fmt"

	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"k8s.io/klog/v2"
)

const meterName = "github.com/nephio-project/porch"

var (
	prrResourceSizeHistogram metric.Float64Histogram
	prrResourceSizeGauge     metric.Int64Gauge
)

func InitMetrics() {
	m := otel.Meter(meterName)
	var err error

	prrResourceSizeHistogram, err = m.Float64Histogram(
		"porch_package_size_bytes",
		metric.WithDescription("File size, in bytes, of a package revision's resources"),
		metric.WithUnit("By"),
		metric.WithExplicitBucketBoundaries(0, 1024, 2048, 4096, 8192, 16384, 32768, 65536, 131072, 262144, 524288, 1048576, 2097152, 4194304, 8388608, 16777216, 33554432, 67108864, 134217728, 268435456, 536870912, 1073741824),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create porch_package_size_bytes histogram: %v", err))
	}

	prrResourceSizeGauge, err = m.Int64Gauge(
		"porch_package_size_bytes_total",
		metric.WithDescription("Total file size, in bytes, of a package revision's resources"),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create porch_package_size_bytes gauge: %v", err))
	}

}

// Porch server and function runner metric recording functions
func RecordPackageSizeUpdate(pkgRev repository.PackageRevision, newResourcesSize int64) {
	if prrResourceSizeHistogram == nil {
		klog.Warning("prrResourceSizeHistogram is nil")
		return
	}
	klog.Infof("Recording package resources size %dB for package %q", newResourcesSize, pkgRev.Key().RKey().Name+"/"+pkgRev.Key().PKey().Path+"/"+pkgRev.Key().PKey().Package)
	prrResourceSizeHistogram.Record(context.Background(), float64(newResourcesSize),
		metric.WithAttributes(
			attribute.String("namespace", pkgRev.KubeObjectNamespace()),
			attribute.String("repository", pkgRev.Key().RKey().Name),
			attribute.String("package", pkgRev.Key().PKey().Path+"/"+pkgRev.Key().PKey().Package),
		),
	)

	if prrResourceSizeGauge == nil {
		klog.Warning("prrResourceSizeGauge is nil")
		return
	}
	prrResourceSizeGauge.Record(context.Background(), newResourcesSize,
		metric.WithAttributes(
			attribute.String("namespace", pkgRev.KubeObjectNamespace()),
			attribute.String("repository", pkgRev.Key().RKey().Name),
			attribute.String("package", pkgRev.Key().PKey().Path+"/"+pkgRev.Key().PKey().Package)))
}
