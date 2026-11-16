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
	ServiceName string `env:"OTEL_SERVICE_NAME" usage:"OpenTelemetry service name"`
	// MetricsExporter is OTEL_METRICS_EXPORTER.
	MetricsExporter string `env:"OTEL_METRICS_EXPORTER" usage:"OpenTelemetry metrics exporter"`
	// TracesExporter is OTEL_TRACES_EXPORTER.
	TracesExporter string `env:"OTEL_TRACES_EXPORTER" usage:"OpenTelemetry traces exporter"`
	// PrometheusHost is OTEL_EXPORTER_PROMETHEUS_HOST.
	PrometheusHost string `env:"OTEL_EXPORTER_PROMETHEUS_HOST" usage:"Prometheus exporter host"`
	// PrometheusPort is OTEL_EXPORTER_PROMETHEUS_PORT.
	PrometheusPort int `env:"OTEL_EXPORTER_PROMETHEUS_PORT" usage:"Prometheus exporter port"`
	// PprofPort is PORCH_PPROF_PORT. Zero leaves the pprof server disabled.
	PprofPort int `env:"PORCH_PPROF_PORT" usage:"Port for the pprof server. Zero disables it"`
	// PackageSizeDetailed is PORCH_METRICS_PACKAGE_SIZE_DETAILED.
	PackageSizeDetailed bool `env:"PORCH_METRICS_PACKAGE_SIZE_DETAILED" usage:"Export detailed package size metrics"`
}

// LifecycleOptions controls lifecycle policy enforced by the API server and controllers.
// Database triggers only implement technical rules; they do not read this policy.
type LifecycleOptions struct {
	// AllowDirectPublicationFromDraft is copied into the reconciler's lifecycle.Options.
	AllowDirectPublicationFromDraft bool `usage:"If true, allow Draft → Published lifecycle transitions (direct publication without a Proposed step)."`
}
