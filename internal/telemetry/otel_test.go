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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	otlpmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	otlptraces "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

const (
	ENV_OTEL_METRICS_EXPORTER   = "OTEL_METRICS_EXPORTER"
	METRICS_EXPORTER_PROMETHEUS = "prometheus"
	METRICS_EXPORTER_OTLP       = "otlp"

	ENV_OTEL_TRACES_EXPORTER     = "OTEL_TRACES_EXPORTER"
	DEFAULT_OTEL_TRACES_EXPORTER = "none"

	ENV_OTEL_EXPORTER_PROMETHEUS_HOST = "OTEL_EXPORTER_PROMETHEUS_HOST"
	ENV_OTEL_EXPORTER_PROMETHEUS_PORT = "OTEL_EXPORTER_PROMETHEUS_PORT"
	ENV_OTEL_EXPORTER_OTLP_ENDPOINT   = "OTEL_EXPORTER_OTLP_ENDPOINT"
	ENV_OTEL_EXPORTER_OTLP_PROTOCOL   = "OTEL_EXPORTER_OTLP_PROTOCOL"
)

func TestPrometheusHTTPServer(t *testing.T) {
	// Find a free port
	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := lis.Addr().(*net.TCPAddr).Port
	lis.Close()

	t.Setenv(ENV_OTEL_METRICS_EXPORTER, METRICS_EXPORTER_PROMETHEUS)
	t.Setenv(ENV_OTEL_TRACES_EXPORTER, DEFAULT_OTEL_TRACES_EXPORTER)
	t.Setenv(ENV_OTEL_EXPORTER_PROMETHEUS_HOST, "0.0.0.0")
	t.Setenv(ENV_OTEL_EXPORTER_PROMETHEUS_PORT, fmt.Sprintf("%d", port))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	res, err := SetupOpenTelemetry(ctx)
	require.NoError(t, err)
	defer res.ShutdownWithTimeout(5 * time.Second)

	// Verify the HTTP server is serving metrics
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/metrics", port))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "target_info")
}

func TestPrometheusHTTPServerInvalidPort(t *testing.T) {
	t.Setenv(ENV_OTEL_METRICS_EXPORTER, METRICS_EXPORTER_PROMETHEUS)
	t.Setenv(ENV_OTEL_TRACES_EXPORTER, DEFAULT_OTEL_TRACES_EXPORTER)
	t.Setenv(ENV_OTEL_EXPORTER_PROMETHEUS_HOST, "0.0.0.0")
	t.Setenv(ENV_OTEL_EXPORTER_PROMETHEUS_PORT, "not-a-number")

	ctx := context.Background()
	_, err := SetupOpenTelemetry(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestOtelMetricsPushHTTP(t *testing.T) {
	requestWaitChannel := make(chan struct{})

	ts := httptest.NewServer(&mockHTTPMetricsServer{t: t, ch: requestWaitChannel})
	defer ts.Close()

	t.Setenv(ENV_OTEL_METRICS_EXPORTER, METRICS_EXPORTER_OTLP)
	t.Setenv(ENV_OTEL_TRACES_EXPORTER, DEFAULT_OTEL_TRACES_EXPORTER)
	t.Setenv(ENV_OTEL_EXPORTER_OTLP_ENDPOINT, ts.URL)
	t.Setenv(ENV_OTEL_EXPORTER_OTLP_PROTOCOL, "http/protobuf")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	res, err := SetupOpenTelemetry(ctx)
	require.NoError(t, err)

	// Shutdown flushes the periodic reader, which triggers the export
	require.NoError(t, res.ShutdownWithTimeout(5*time.Second))
	<-requestWaitChannel
}

func TestOtelTracesPushHTTP(t *testing.T) {
	requestWaitChannel := make(chan struct{})

	ts := httptest.NewServer(&mockHTTPTraceServer{t: t, ch: requestWaitChannel})
	defer ts.Close()

	t.Setenv(ENV_OTEL_TRACES_EXPORTER, METRICS_EXPORTER_OTLP)
	t.Setenv(ENV_OTEL_METRICS_EXPORTER, DEFAULT_OTEL_TRACES_EXPORTER)
	t.Setenv(ENV_OTEL_EXPORTER_OTLP_ENDPOINT, ts.URL)
	t.Setenv(ENV_OTEL_EXPORTER_OTLP_PROTOCOL, "http/protobuf")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	res, err := SetupOpenTelemetry(ctx)
	require.NoError(t, err)

	// Create a span to trigger trace export
	tracer := otel.Tracer("test")
	_, span := tracer.Start(ctx, "test-span")
	span.End()

	// Shutdown flushes the batch span processor
	require.NoError(t, res.ShutdownWithTimeout(5*time.Second))
	<-requestWaitChannel
}
func TestSetupOpenTelemetryPrometheusEndpoint(t *testing.T) {
	t.Setenv(ENV_OTEL_METRICS_EXPORTER, METRICS_EXPORTER_PROMETHEUS)
	t.Setenv(ENV_OTEL_TRACES_EXPORTER, DEFAULT_OTEL_TRACES_EXPORTER)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	res, err := SetupOpenTelemetry(ctx)
	require.NoError(t, err)
	defer res.ShutdownWithTimeout(5 * time.Second)

	// Verify that metrics are accessible via the OTel meter provider
	meter := otel.Meter("test")
	counter, err := meter.Float64Counter("test_counter")
	require.NoError(t, err)
	counter.Add(ctx, 1)
}

func TestOtelMetricsPushGRPC(t *testing.T) {
	requestWaitChannel := make(chan struct{})

	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer lis.Close()

	s := grpc.NewServer()
	otlpmetrics.RegisterMetricsServiceServer(s, &mockMetricsServer{t: t, ch: requestWaitChannel})

	go func() {
		if err := s.Serve(lis); err != nil {
			t.Errorf("Failed to serve: %v", err)
		}
	}()
	defer s.Stop()

	t.Setenv(ENV_OTEL_METRICS_EXPORTER, METRICS_EXPORTER_OTLP)
	t.Setenv(ENV_OTEL_TRACES_EXPORTER, DEFAULT_OTEL_TRACES_EXPORTER)
	t.Setenv(ENV_OTEL_EXPORTER_OTLP_ENDPOINT, fmt.Sprintf("http://localhost:%d", lis.Addr().(*net.TCPAddr).Port))
	t.Setenv(ENV_OTEL_EXPORTER_OTLP_PROTOCOL, "grpc")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	res, err := SetupOpenTelemetry(ctx)
	require.NoError(t, err)

	require.NoError(t, res.ShutdownWithTimeout(5*time.Second))
	<-requestWaitChannel
}

func TestOtelTracesPushGRPC(t *testing.T) {
	requestWaitChannel := make(chan struct{})

	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer lis.Close()

	s := grpc.NewServer()
	otlptraces.RegisterTraceServiceServer(s, &mockTraceServer{t: t, ch: requestWaitChannel})

	go func() {
		if err := s.Serve(lis); err != nil {
			t.Errorf("Failed to serve: %v", err)
		}
	}()
	defer s.Stop()

	t.Setenv(ENV_OTEL_TRACES_EXPORTER, METRICS_EXPORTER_OTLP)
	t.Setenv(ENV_OTEL_METRICS_EXPORTER, "none")
	t.Setenv(ENV_OTEL_EXPORTER_OTLP_ENDPOINT, fmt.Sprintf("http://localhost:%d", lis.Addr().(*net.TCPAddr).Port))
	t.Setenv(ENV_OTEL_EXPORTER_OTLP_PROTOCOL, "grpc")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	res, err := SetupOpenTelemetry(ctx)
	require.NoError(t, err)

	// Create a span to trigger trace export
	tracer := otel.Tracer("test")
	_, span := tracer.Start(ctx, "test-span")
	span.End()

	require.NoError(t, res.ShutdownWithTimeout(5*time.Second))
	<-requestWaitChannel
}

type mockMetricsServer struct {
	otlpmetrics.UnimplementedMetricsServiceServer
	t  *testing.T
	ch chan struct{}
}

func (m *mockMetricsServer) Export(ctx context.Context, req *otlpmetrics.ExportMetricsServiceRequest) (*otlpmetrics.ExportMetricsServiceResponse, error) {
	assert.NotEmpty(m.t, req.GetResourceMetrics())
	close(m.ch)
	return &otlpmetrics.ExportMetricsServiceResponse{}, nil
}

type mockTraceServer struct {
	otlptraces.UnimplementedTraceServiceServer
	t  *testing.T
	ch chan struct{}
}

func (m *mockTraceServer) Export(ctx context.Context, req *otlptraces.ExportTraceServiceRequest) (*otlptraces.ExportTraceServiceResponse, error) {
	assert.NotEmpty(m.t, req.GetResourceSpans())
	close(m.ch)
	return &otlptraces.ExportTraceServiceResponse{}, nil
}

type mockHTTPMetricsServer struct {
	t  *testing.T
	ch chan struct{}
}

func (m *mockHTTPMetricsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	require.NoError(m.t, err)
	r.Body.Close()

	req := &otlpmetrics.ExportMetricsServiceRequest{}
	proto.Unmarshal(body, req)

	assert.NotEmpty(m.t, req.GetResourceMetrics())
	close(m.ch)
}

type mockHTTPTraceServer struct {
	t  *testing.T
	ch chan struct{}
}

func (m *mockHTTPTraceServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	require.NoError(m.t, err)
	r.Body.Close()

	req := &otlptraces.ExportTraceServiceRequest{}
	proto.Unmarshal(body, req)

	assert.NotEmpty(m.t, req.GetResourceSpans())
	close(m.ch)
}
