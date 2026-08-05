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
	"time"

	"github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	porchcontext "github.com/kptdev/porch/pkg/util/context"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"k8s.io/klog/v2"
)

const meterName = "github.com/kptdev/porch"

const (
	ControllerUser = "packagerevision-controller"

	ResourcePackageRevision          = "PackageRevision"
	ResourcePackageRevisionResources = "PackageRevisionResources"
	ResourceExternalRepo             = "ExternalRepo"

	apiCallDurationStartingBucket = 0.001
	apiCallDurationBucketCount    = 16

	packageSizeStartingBucket = 1024
	packageSizeBucketCount    = 21 // doubling boundaries after the initial zero bucket
)

var (
	APIVersionV1Alpha1 = v1alpha1.SchemeGroupVersion.Version
	APIVersionV1Alpha2 = v1alpha2.SchemeGroupVersion.Version

	apiCallDurationSeconds  metric.Float64Histogram
	requestsTotal           metric.Float64Counter
	prResourceSizeHistogram metric.Int64Histogram
	prResourceSizeGauge     metric.Int64Gauge
)

func InitMetrics() (err error) {
	m := otel.Meter(meterName)

	apiCallDurationSeconds, err = m.Float64Histogram(
		"porch_api_call_duration_seconds",
		metric.WithDescription("Duration of porch API calls in seconds."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(doublingBucketBoundaries(apiCallDurationStartingBucket, apiCallDurationBucketCount)...),
	)
	if err != nil {
		klog.Errorf("failed to create porch_api_call_duration_seconds: %v", err)
		return
	}

	requestsTotal, err = m.Float64Counter(
		"porch_api_requests_by_user",
		metric.WithDescription("Total number of requests tracked by BurstCounter, broken down by resource, operation, and user."),
	)
	if err != nil {
		klog.Errorf("failed to create porch_api_requests_by_user: %v", err)
		return
	}

	prResourceSizeHistogram, err = m.Int64Histogram(
		"porch_package_size_bytes",
		metric.WithUnit("By"),
		metric.WithDescription("Distribution of package revision resources' file size, in bytes"),
		metric.WithExplicitBucketBoundaries(packageSizeBucketBoundaries()...),
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
func RecordAPICallDuration(resource, verb, apiVersion string, durationSeconds float64) {
	if apiCallDurationSeconds == nil {
		klog.Warning("apiCallDurationSeconds is nil - was InitMetrics() called?")
		return
	}
	apiCallDurationSeconds.Record(context.Background(), durationSeconds,
		metric.WithAttributes(
			attribute.String("resource", resource),
			attribute.String("verb", verb),
			attribute.String("api_version", apiVersion),
		),
	)
}

func RecordRequestCount(ctx context.Context, resource, op, apiVersion string) {
	if requestsTotal == nil {
		klog.Warning("requestsTotal is nil - was InitMetrics() called?")
		return
	}
	recordRequestCount(resource, op, apiVersion, porchcontext.GetK8sUserName(ctx))
}

func RecordControllerRequestCount(resource, op, apiVersion string) {
	if requestsTotal == nil {
		klog.Warning("requestsTotal is nil - was InitMetrics() called?")
		return
	}
	recordRequestCount(resource, op, apiVersion, ControllerUser)
}

// RecordControllerOperation records duration and request count for a v1alpha2 controller operation.
func RecordControllerOperation(resource, verb string, start time.Time) {
	RecordAPICallDuration(resource, verb, APIVersionV1Alpha2, time.Since(start).Seconds())
	RecordControllerRequestCount(resource, verb, APIVersionV1Alpha2)
}

func recordRequestCount(resource, op, apiVersion, user string) {
	requestsTotal.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("resource", resource),
			attribute.String("op", op),
			attribute.String("user", user),
			attribute.String("api_version", apiVersion),
		),
	)
}

// External git operations are shared infrastructure and are not tagged with api_version.
func RecordExternalRepoOperation(ctx context.Context, op string, start time.Time) {
	recordExternalRepoDuration(op, time.Since(start).Seconds())
	RecordExternalRepoRequestCount(ctx, op)
}

func recordExternalRepoDuration(op string, durationSeconds float64) {
	if apiCallDurationSeconds == nil {
		klog.Warning("apiCallDurationSeconds is nil - was InitMetrics() called?")
		return
	}
	apiCallDurationSeconds.Record(context.Background(), durationSeconds,
		metric.WithAttributes(
			attribute.String("resource", ResourceExternalRepo),
			attribute.String("verb", op),
		),
	)
}

func RecordExternalRepoRequestCount(ctx context.Context, op string) {
	if requestsTotal == nil {
		klog.Warning("requestsTotal is nil - was InitMetrics() called?")
		return
	}
	requestsTotal.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("resource", ResourceExternalRepo),
			attribute.String("op", op),
			attribute.String("user", porchcontext.GetK8sUserName(ctx)),
		),
	)
}

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

func doublingBucketBoundaries(start float64, count int) []float64 {
	buckets := make([]float64, count)
	v := start
	for i := range buckets {
		buckets[i] = v
		v *= 2
	}
	return buckets
}

func packageSizeBucketBoundaries() []float64 {
	doubled := doublingBucketBoundaries(float64(packageSizeStartingBucket), packageSizeBucketCount)
	buckets := make([]float64, 1+len(doubled))
	buckets[0] = 0
	copy(buckets[1:], doubled)
	return buckets
}
