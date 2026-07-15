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
	"net/http"
	"os"
	"strconv"
	"time"

	prombridge "go.opentelemetry.io/contrib/bridges/prometheus"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"
	controllerruntimemetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	otelHostEnv     = "OTEL_EXPORTER_PROMETHEUS_HOST"
	otelHostDefault = "0.0.0.0"
	otelPortEnv     = "OTEL_EXPORTER_PROMETHEUS_PORT"
	otelPortDefault = "9464"
)

// OTelResources holds all OpenTelemetry resources that need lifecycle management.
// Use Shutdown() to cleanly release all resources.
type OTelResources struct {
	metricsServer  *http.Server
	metricsPort    int
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *trace.TracerProvider
	profiling      *Profiling
}

// Shutdown gracefully shuts down all OpenTelemetry resources.
func (r *OTelResources) Shutdown(ctx context.Context) error {
	shutdownTiming := time.Now()
	var errs []error
	if r.metricsServer != nil {
		if err := r.metricsServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("metrics server shutdown: %w", err))
		}
	}
	if r.meterProvider != nil {
		if err := r.meterProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter provider shutdown: %w", err))
		}
	}
	if r.tracerProvider != nil {
		if err := r.tracerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer provider shutdown: %w", err))
		}
	}
	if r.profiling != nil {
		r.profiling.Stop()
	}
	if len(errs) > 0 {
		return fmt.Errorf("otel shutdown errors: %v", errs)
	}
	klog.Infof("OpenTelemetry shut down in %s", time.Since(shutdownTiming))
	return nil
}

// ShutdownWithTimeout is a convenience wrapper around Shutdown with a timeout.
func (r *OTelResources) ShutdownWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return r.Shutdown(ctx)
}

// SetupOpenTelemetry is the single entry point for all OpenTelemetry setup.
// It configures tracing, metrics (including the Prometheus HTTP server if
// OTEL_EXPORTER_PROMETHEUS_HOST and OTEL_EXPORTER_PROMETHEUS_PORT are set),
// and initializes all Porch metric instruments. Returns OTelResources
// for lifecycle management.
func SetupOpenTelemetry(ctx context.Context) (*OTelResources, error) {
	setupTiming := time.Now()
	res := &OTelResources{}

	// Setup tracing
	if err := setupTracing(ctx, res); err != nil {
		return nil, err
	}

	// Setup metrics provider
	if err := setupMetrics(ctx, res); err != nil {
		return nil, err
	}

	// Initialize all Porch metric instruments
	if err := InitMetrics(); err != nil {
		return nil, fmt.Errorf("failed to initialize Porch metrics: %w", err)
	}

	// Start the Prometheus metrics HTTP server if port is configured
	if err := startMetricsServerIfConfigured(res); err != nil {
		return nil, err
	}

	prof := &Profiling{}
	prof.Start()
	res.profiling = prof

	http.DefaultTransport = otelhttp.NewTransport(http.DefaultTransport)
	http.DefaultClient.Transport = http.DefaultTransport
	klog.Infof("OpenTelemetry initialized in %s", time.Since(setupTiming))
	return res, nil
}

func setupTracing(ctx context.Context, res *OTelResources) error {
	exp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return fmt.Errorf("failed to create span exporter: %w", err)
	}
	tp := trace.NewTracerProvider(trace.WithBatcher(exp))
	res.tracerProvider = tp
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(autoprop.NewTextMapPropagator())
	return nil
}

func setupMetrics(ctx context.Context, res *OTelResources) error {
	exporter := os.Getenv("OTEL_METRICS_EXPORTER")

	autoexport.WithFallbackMetricProducer(func(ctx context.Context) (sdkmetric.Producer, error) {
		return prombridge.NewMetricProducer(
			prombridge.WithGatherer(controllerruntimemetrics.Registry),
		), nil
	})

	readers := []sdkmetric.Option{}
	if exporter == "prometheus" {
		if os.Getenv(otelHostEnv) == "" {
			if err := os.Setenv(otelHostEnv, otelHostDefault); err != nil {
				return err
			}
		}
		if os.Getenv(otelPortEnv) == "" {
			if err := os.Setenv(otelPortEnv, otelPortDefault); err != nil {
				return err
			}
		}

		// Only create the Prometheus exporter when we intend to expose a scrape
		// endpoint, to avoid writing OTel metrics into the default Prometheus
		// registry when pushing via OTLP.
		promExp, err := otelprometheus.New(
			otelprometheus.WithRegisterer(prometheus.DefaultRegisterer),
		)
		if err != nil {
			return fmt.Errorf("failed to create prometheus exporter: %w", err)
		}
		readers = append(readers, sdkmetric.WithReader(promExp))
	} else {
		autoMr, err := autoexport.NewMetricReader(ctx)
		if err != nil {
			return fmt.Errorf("failed to create metric reader: %w", err)
		}
		readers = append(readers, sdkmetric.WithReader(autoMr))
	}

	mp := sdkmetric.NewMeterProvider(readers...)
	res.meterProvider = mp
	otel.SetMeterProvider(mp)

	return nil
}

func startMetricsServerIfConfigured(res *OTelResources) error {
	hostStr := os.Getenv(otelHostEnv)
	if hostStr == "" {
		return nil
	}
	portStr := os.Getenv(otelPortEnv)
	if portStr == "" {
		return nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("invalid %s value %q: %w", otelPortEnv, portStr, err)
	}
	if port <= 0 {
		return nil
	}

	gatherers := prometheus.Gatherers{
		prometheus.DefaultGatherer,
		controllerruntimemetrics.Registry,
		legacyregistry.DefaultGatherer,
	}
	handler := promhttp.HandlerFor(gatherers, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	})

	mux := http.NewServeMux()
	mux.Handle("/metrics", handler)

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", hostStr, port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	res.metricsServer = srv
	res.metricsPort = port

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.Errorf("OTel metrics server error: %v", err)
		}
	}()
	klog.Infof("OTel metrics server started on port %d", port)

	return nil
}
