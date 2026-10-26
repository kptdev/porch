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
	"os"
	"strconv"
	"time"

	"github.com/kptdev/porch/api/porch/key"
	"github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/api/porch/v1alpha2"
	porchcontext "github.com/kptdev/porch/pkg/util/context"
	"github.com/kptdev/porch/pkg/util/datasize"
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

	packageSizeStartingBucket = datasize.KB
	packageSizeBucketCount    = 21 // doubling boundaries after the initial zero bucket

	// packageSizeDetailedEnv, when set to a truthy value (1/true), makes the
	// per-package-revision gauge carry high-cardinality labels (repository,
	// package, workspace_name). It is OFF by default because those labels add
	// one time series per package revision, which scales linearly with the
	// number of revisions and, on OTel SDKs with a cardinality limit, silently
	// collapses excess revisions into an overflow bucket. Enable only for
	// targeted debugging on small repositories.
	packageSizeDetailedEnv = "PORCH_METRICS_PACKAGE_SIZE_DETAILED"
)

var (
	APIVersionV1Alpha1 = v1alpha1.SchemeGroupVersion.Version
	APIVersionV1Alpha2 = v1alpha2.SchemeGroupVersion.Version

	apiCallDurationSeconds  metric.Float64Histogram
	requestsTotal           metric.Float64Counter
	prResourceSizeHistogram metric.Int64Histogram
	prResourceSizeGauge     metric.Int64Gauge

	// packageSizeDetailed is read once at InitMetrics time from
	// PORCH_METRICS_PACKAGE_SIZE_DETAILED and controls whether the per-revision
	// gauge emits high-cardinality labels. See packageSizeDetailedEnv.
	packageSizeDetailed bool
)

func InitMetrics() (err error) {
	m := otel.Meter(meterName)

	packageSizeDetailed = parseBoolEnv(os.Getenv(packageSizeDetailedEnv))
	if packageSizeDetailed {
		klog.Warningf(
			"%s is enabled: porch_package_size_bytes_total will carry per-package-revision labels "+
				"(repository, package, workspace_name). This adds one time series per revision and can "+
				"produce very high metric cardinality on large repositories.", packageSizeDetailedEnv)
	}

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
		metric.WithDescription("Distribution of package revision resource sizes, in bytes, aggregated per namespace"),
		metric.WithExplicitBucketBoundaries(packageSizeBucketBoundaries()...),
	)
	if err != nil {
		klog.Errorf("failed to create porch_package_size_bytes histogram: %v", err)
		return
	}

	prResourceSizeGauge, err = m.Int64Gauge(
		"porch_package_size_bytes_total",
		metric.WithUnit("By"),
		metric.WithDescription("Current total resource size, in bytes, of package revisions. Aggregated per namespace and repository by default; set "+packageSizeDetailedEnv+"=true to break down per package revision."),
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

// RecordPackageRevisionResourcesSize records the resource size of a single
// package revision.
//
// To keep metric cardinality bounded, the labels are deliberately limited:
//   - The histogram (porch_package_size_bytes) is aggregated per namespace only,
//     so it stays a meaningful distribution with at most one series set per
//     namespace, regardless of the number of package revisions.
//   - The gauge (porch_package_size_bytes_total) is aggregated per namespace and
//     repository by default. Only when PORCH_METRICS_PACKAGE_SIZE_DETAILED is
//     enabled does it additionally carry the high-cardinality package and
//     workspace_name labels, producing one series per package revision.
func RecordPackageRevisionResourcesSize(ctx context.Context, prKey key.PackageRevisionKey, resourcesSize int64) {
	namespace := prKey.RKey().Namespace
	repositoryName := prKey.RKey().Name

	if prResourceSizeHistogram == nil {
		klog.Warning("prResourceSizeHistogram is nil - was InitMetrics() called?")
		return
	}

	// Histogram: distribution of sizes, aggregated per namespace only.
	histAttrs := attribute.NewSet(
		attribute.String("namespace", namespace),
	)

	if klog.V(3).Enabled() {
		klog.Infof(
			"Recording package resources size %dB for repository %q/%q (detailed=%t)",
			resourcesSize, namespace, repositoryName, packageSizeDetailed)
	}

	prResourceSizeHistogram.Record(ctx, resourcesSize, metric.WithAttributeSet(histAttrs))

	// The per-revision gauge is only emitted in detailed mode. A gauge records a
	// last-write-wins value per attribute set, which is only meaningful when the
	// attribute set uniquely identifies a single package revision. Aggregating it
	// to namespace+repository would make concurrent revisions overwrite each
	// other's value (not a sum), so in the default low-cardinality mode we rely
	// on the per-namespace histogram (which exposes _sum and _count) instead and
	// skip the gauge entirely.
	if !packageSizeDetailed {
		return
	}

	if prResourceSizeGauge == nil {
		klog.Warning("prResourceSizeGauge is nil - was InitMetrics() called?")
		return
	}

	prPath := ""
	if prKey.PKey().Path != "" {
		prPath = prKey.PKey().Path + "/"
	}
	gaugeAttrs := attribute.NewSet(
		attribute.String("namespace", namespace),
		attribute.String("repository", repositoryName),
		attribute.String("package", prPath+prKey.PKey().Package),
		attribute.String("workspace_name", prKey.WorkspaceName),
	)
	prResourceSizeGauge.Record(ctx, resourcesSize, metric.WithAttributeSet(gaugeAttrs))
}

// parseBoolEnv parses a boolean-ish environment variable value. Empty or
// unparseable values are treated as false.
func parseBoolEnv(v string) bool {
	// strconv.ParseBool returns an error (and false) for "" and any
	// unparseable value, which is exactly the fallback we want.
	b, _ := strconv.ParseBool(v)
	return b
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
