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

package porch

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/bridges/prometheus"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
	"k8s.io/klog/v2"
	controllerruntimemetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Sets up OpenTelemetry with parameters
// from environment variables based on the
// opentelemetry.io/contrib/exporters/autoexport"
func SetupOpenTelemetry(ctx context.Context) error {
	setupTiming := time.Now()
	err := setupTracing(ctx)
	if err != nil {
		return err
	}
	err = setupMetrics(ctx)
	if err != nil {
		return err
	}
	http.DefaultTransport = otelhttp.NewTransport(http.DefaultTransport)
	http.DefaultClient.Transport = http.DefaultTransport
	klog.Infof("OpenTelemetry initialized in %s", time.Since(setupTiming))
	return nil

}

func setupTracing(ctx context.Context) error {
	exp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return fmt.Errorf("failed to create span exporter: %w", err)
	}
	tp := trace.NewTracerProvider(trace.WithBatcher(exp))
	go func() {
		<-ctx.Done()
		if err := tp.Shutdown(context.Background()); err != nil {
			panic(err)
		}
	}()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(autoprop.NewTextMapPropagator())

	return nil
}

func setupMetrics(ctx context.Context) error {
	autoexport.WithFallbackMetricProducer(func(ctx context.Context) (metric.Producer, error) {
		return prometheus.NewMetricProducer(
			prometheus.WithGatherer(controllerruntimemetrics.Registry),
		), nil
	})

	mr, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return fmt.Errorf("failed to create metric reader: %w", err)
	}
	go func() {
		<-ctx.Done()
		if err := mr.Shutdown(context.Background()); err != nil {
			panic(err)
		}
	}()

	mp := metric.NewMeterProvider(metric.WithReader(mr))
	otel.SetMeterProvider(mp)

	return nil
}
