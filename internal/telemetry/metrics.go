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

	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"k8s.io/klog/v2"
)

const meterName = "github.com/kptdev/porch"

var (
	prResourceSizeHistogram metric.Int64Histogram
	prResourceSizeGauge     metric.Int64Gauge
)

func InitMetrics() (err error) {
	m := otel.Meter(meterName)

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

	return nil
}

// Porch server and function runner metric recording functions
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
