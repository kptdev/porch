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

package types

// Telemetry mirrors the environment variables read by internal/telemetry
// and the OpenTelemetry SDK variables set on every porch deployment.
// There is no existing configuration struct for these.
type Telemetry struct {
	// ServiceName is OTEL_SERVICE_NAME.
	ServiceName string
	// MetricsExporter is OTEL_METRICS_EXPORTER.
	MetricsExporter string
	// TracesExporter is OTEL_TRACES_EXPORTER.
	TracesExporter string
	// PrometheusHost is OTEL_EXPORTER_PROMETHEUS_HOST.
	PrometheusHost string
	// PrometheusPort is OTEL_EXPORTER_PROMETHEUS_PORT.
	PrometheusPort int
	// PprofPort is PORCH_PPROF_PORT. Zero leaves the pprof server disabled.
	PprofPort int
	// PackageSizeDetailed is PORCH_METRICS_PACKAGE_SIZE_DETAILED.
	PackageSizeDetailed bool
}
