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
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"k8s.io/klog/v2"
)

const meterName = "github.com/kptdev/porch"

const (
	ControllerUser = "packagerevision-controller"

	ResourcePackageRevision          = "PackageRevision"
	ResourcePackageRevisionApproval  = "PackageRevisionApproval"
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

	porchApiOpDurationSeconds metric.Float64Histogram
	porchInFlightApiOps       metric.Int64UpDownCounter
	requestsTotal             metric.Float64Counter
	prResourceSizeHistogram   metric.Int64Histogram
	prResourceSizeGauge       metric.Int64Gauge
)

func InitMetrics() (err error) {
	m := otel.Meter(meterName)

	porchApiOpDurationSeconds, err = m.Float64Histogram(
		"porch_api_call_duration_seconds",
		metric.WithDescription("Duration of Porch API operations in seconds."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(doublingBucketBoundaries(apiCallDurationStartingBucket, apiCallDurationBucketCount)...),
	)
	if err != nil {
		klog.Errorf("failed to create porch_api_call_duration_seconds: %v", err)
		return
	}

	porchInFlightApiOps, err = m.Int64UpDownCounter(
		"porch_in_flight_api_operations",
		metric.WithDescription("Number of in-flight (currently in progress) Porch API operations"),
	)
	if err != nil {
		klog.Errorf("failed to create porch_in_flight_api_operations: %v", err)
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
func RecordAPIOperationDuration(ctx context.Context, resource, verb, porchOperation, apiVersion string, duration time.Duration, err error, lifecycleAfter v1alpha1.PackageRevisionLifecycle, prKey *repository.PackageRevisionKey) {
	if porchApiOpDurationSeconds == nil {
		klog.Warning("apiCallDurationSeconds is nil - was InitMetrics() called?")
		return
	}

	seconds := duration.Seconds()

	attrSlice := []attribute.KeyValue{
		attribute.String("resource", resource),
		attribute.String("verb", verb),
		attribute.String("api_version", apiVersion),
		attribute.String("operation", porchOperation),
		attribute.String("operation_outcome", string(perfStatusLabel(err))),
		attribute.String("lifecycle_after", string(lifecycleAfter)),
	}
	attrSlice = append(attrSlice, attributesFromPrKey(prKey).ToSlice()...)
	attributes := attribute.NewSet(attrSlice...)

	if klog.V(3).Enabled() {
		klog.Infof(
			"Recording %f seconds duration for Porch API operation with attributes %v",
			seconds, attributes.MarshalLog())
	}

	porchApiOpDurationSeconds.Record(ctx, seconds,
		metric.WithAttributeSet(attributes),
	)
}

func TrackInFlightOperation(ctx context.Context, resource, verb, porchOperation, apiVersion string, initialLifecycle v1alpha1.PackageRevisionLifecycle, prKey *repository.PackageRevisionKey) func() {
	if porchInFlightApiOps == nil {
		klog.Warning("porchInFlightApiOps is nil - was InitMetrics() called?")
		return func() {}
	}

	attrSlice := []attribute.KeyValue{
		attribute.String("resource", resource),
		attribute.String("verb", verb),
		attribute.String("operation", porchOperation),
		attribute.String("api_version", apiVersion),
		attribute.String("initial_lifecycle", string(initialLifecycle)),
	}
	attrSlice = append(attrSlice, (attributesFromPrKey(prKey).ToSlice())...)
	attributes := attribute.NewSet(attrSlice...)

	if klog.V(3).Enabled() {
		klog.Infof(
			"Tracking START of in-flight Porch API operation with attributes %v",
			attributes.MarshalLog())
	}
	porchInFlightApiOps.Add(ctx, 1,
		metric.WithAttributeSet(attributes),
	)

	return func() {
		if klog.V(3).Enabled() {
			klog.Infof(
				"Tracking END of in-flight Porch API operation with attributes %v",
				attributes.MarshalLog())
		}
		porchInFlightApiOps.Add(context.Background(), -1,
			metric.WithAttributeSet(attributes),
		)
	}
}

func TrackInFlightControllerOperation(ctx context.Context, resource, verb, porchOperation string, initialLifecycle v1alpha2.PackageRevisionLifecycle, prKey *repository.PackageRevisionKey) func() {
	return TrackInFlightOperation(ctx, resource, verb, porchOperation, APIVersionV1Alpha2, v1alpha1.PackageRevisionLifecycle(initialLifecycle), prKey)
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
func RecordControllerOperation(ctx context.Context, resource, verb, porchOperation string, duration time.Duration, err error, lifecycleAfter v1alpha2.PackageRevisionLifecycle, prKey *repository.PackageRevisionKey) {
	RecordAPIOperationDuration(ctx, resource, verb, porchOperation, APIVersionV1Alpha2, duration, err, v1alpha1.PackageRevisionLifecycle(lifecycleAfter), prKey)
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
	if porchApiOpDurationSeconds == nil {
		klog.Warning("apiCallDurationSeconds is nil - was InitMetrics() called?")
		return
	}
	porchApiOpDurationSeconds.Record(context.Background(), durationSeconds,
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

	if prResourceSizeHistogram == nil {
		klog.Warning("prResourceSizeHistogram is nil - was InitMetrics() called?")
		return
	}

	attributes := *attributesFromPrKey(&prKey)

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

func perfStatusLabel(err error) OperationOutcomeValue {
	if err != nil {
		return OperationOutcomes.Error
	}
	return OperationOutcomes.Success
}

func attributesFromPrKey(prKey *repository.PackageRevisionKey) *attribute.Set {
	if prKey == nil {
		return attribute.EmptySet()
	}
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
	return &attributes
}

type OperationOutcomeValue string

var OperationOutcomes = struct {
	Success, Error OperationOutcomeValue
}{
	Success: "success",
	Error:   "error",
}

type operationVerbs struct {
	AllCaps, TitleCase string
}

func ParseOperation(baseString string) operationVerbs {
	return operationVerbs{
		AllCaps:   cases.Upper(language.English).String(baseString),
		TitleCase: cases.Title(language.English).String(baseString),
	}
}

var Operations = struct {
	List, Get, Create, Update, Delete operationVerbs
}{
	List:   ParseOperation("list"),
	Get:    ParseOperation("get"),
	Create: ParseOperation("create"),
	Update: ParseOperation("update"),
	Delete: ParseOperation("delete"),
}
