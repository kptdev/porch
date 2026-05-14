---
title: "OpenTelemetry Configuration"
type: docs
weight: 4
description: Configure OpenTelemetry metrics and traces export for Porch components
---

## Overview

Porch supports OpenTelemetry observability through the [autoexport package](https://pkg.go.dev/go.opentelemetry.io/contrib/exporters/autoexport), which provides automatic configuration of metrics and traces exporters via environment variables. This enables seamless integration with various observability backends including OTLP collectors, Prometheus, and Jaeger.

All Porch components (porch-server, porch-controllers, function-runner, and wrapper-server) support OpenTelemetry configuration through standardized environment variables as defined by the [OpenTelemetry specification](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/).

{{% alert title="Note" color="primary" %}}
**Current Implementation Status**: Porch currently implements metrics and traces export. Logs export is not supported.
{{% /alert %}}

## Traces Configuration

### OTLP Trace Export

Export traces to an OpenTelemetry Protocol (OTLP) collector using either HTTP or gRPC protocols.

#### HTTP Protocol

```yaml
env:
  - name: OTEL_TRACES_EXPORTER
    value: "otlp"
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: "http://otel-collector:4318"
  - name: OTEL_EXPORTER_OTLP_PROTOCOL
    value: "http/protobuf"
```

#### gRPC Protocol

```yaml
env:
  - name: OTEL_TRACES_EXPORTER
    value: "otlp"
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: "http://otel-collector:4317"
  - name: OTEL_EXPORTER_OTLP_PROTOCOL
    value: "grpc"
```

### Disable Traces

To disable trace export entirely:

```yaml
env:
  - name: OTEL_TRACES_EXPORTER
    value: "none"
```

### Trace Environment Variables

All environment variables apply to all Porch components: porch-server, porch-controllers, function-runner, and wrapper-server.

| Variable | Description | Default | Examples |
|----------|-------------|---------|----------|
| `OTEL_TRACES_EXPORTER` | Trace exporter type | `otlp` | `otlp`, `console`, `none` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP collector endpoint (applies to all signals) | - | `http://localhost:4318`, `https://otel-collector.example.com` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | Protocol for OTLP export (applies to all signals) | `http/protobuf` | `http/protobuf`, `grpc` |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | Traces-specific endpoint (overrides general endpoint) | - | `http://localhost:4318/v1/traces` |
| `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL` | Traces-specific protocol (overrides general protocol) | - | `http/protobuf`, `grpc` |

## Metrics Configuration

### OTLP Metrics Export

Export metrics to an OTLP collector using HTTP or gRPC protocols.

#### HTTP Protocol

```yaml
env:
  - name: OTEL_METRICS_EXPORTER
    value: "otlp"
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: "http://otel-collector:4318"
  - name: OTEL_EXPORTER_OTLP_PROTOCOL
    value: "http/protobuf"
```

#### gRPC Protocol

```yaml
env:
  - name: OTEL_METRICS_EXPORTER
    value: "otlp"
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: "http://otel-collector:4317"
  - name: OTEL_EXPORTER_OTLP_PROTOCOL
    value: "grpc"
```

### Prometheus Metrics Export

Porch supports native Prometheus metrics export through an HTTP endpoint. This is the recommended approach for Kubernetes environments with Prometheus-based monitoring.

#### Basic Prometheus Configuration

```yaml
env:
  - name: OTEL_METRICS_EXPORTER
    value: "prometheus"
  - name: OTEL_EXPORTER_PROMETHEUS_HOST
    value: "0.0.0.0"
  - name: OTEL_EXPORTER_PROMETHEUS_PORT
    value: "9090"
```

The metrics endpoint will be available at `http://<pod-ip>:9090/metrics`.

### Metrics Environment Variables

All environment variables apply to all Porch components: porch-server, porch-controllers, function-runner, and wrapper-server.

| Variable | Description | Default | Examples |
|----------|-------------|---------|----------|
| `OTEL_METRICS_EXPORTER` | Metrics exporter type | `otlp` | `otlp`, `prometheus`, `console`, `none` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP collector endpoint (applies to all signals) | - | `http://localhost:4318` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | Protocol for OTLP export (applies to all signals) | `http/protobuf` | `http/protobuf`, `grpc` |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | Metrics-specific endpoint (overrides general endpoint) | - | `http://localhost:4318/v1/metrics` |
| `OTEL_EXPORTER_OTLP_METRICS_PROTOCOL` | Metrics-specific protocol (overrides general protocol) | - | `http/protobuf`, `grpc` |
| `OTEL_EXPORTER_PROMETHEUS_HOST` | Prometheus endpoint host | `localhost` | `0.0.0.0`, `127.0.0.1` |
| `OTEL_EXPORTER_PROMETHEUS_PORT` | Prometheus endpoint port | `9464` | `9090`, `8080` |

## Prometheus Auto-Discovery


### Pod Annotations (Prometheus Kubernetes SD)

For Prometheus using Kubernetes service discovery with pod annotations:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: porch-server
  namespace: porch-system
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "9090"
    prometheus.io/path: "/metrics"
spec:
  containers:
  - name: porch-server
    image: porch-server:latest
    env:
    - name: OTEL_METRICS_EXPORTER
      value: "prometheus"
    - name: OTEL_EXPORTER_PROMETHEUS_HOST
      value: "0.0.0.0"
    - name: OTEL_EXPORTER_PROMETHEUS_PORT
      value: "9090"
    ports:
    - name: metrics
      containerPort: 9090
      protocol: TCP
```

## Complete Deployment Examples

### Porch Server with OTLP Export (All Signals)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: porch-server
  namespace: porch-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: porch-server
  template:
    metadata:
      labels:
        app: porch-server
    spec:
      containers:
      - name: porch-server
        image: porch-server:latest
        env:
        - name: OTEL_TRACES_EXPORTER
          value: "otlp"
        - name: OTEL_METRICS_EXPORTER
          value: "otlp"
        - name: OTEL_LOGS_EXPORTER
          value: "otlp"
        - name: OTEL_EXPORTER_OTLP_ENDPOINT
          value: "http://otel-collector.observability:4318"
        - name: OTEL_EXPORTER_OTLP_PROTOCOL
          value: "http/protobuf"
```

### Porch Controllers with Prometheus Metrics and OTLP Traces

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: porch-controllers
  namespace: porch-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: porch-controllers
  template:
    metadata:
      labels:
        app: porch-controllers
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9090"
        prometheus.io/path: "/metrics"
    spec:
      containers:
      - name: porch-controllers
        image: porch-controllers:latest
        env:
        # Prometheus for metrics
        - name: OTEL_METRICS_EXPORTER
          value: "prometheus"
        - name: OTEL_EXPORTER_PROMETHEUS_HOST
          value: "0.0.0.0"
        - name: OTEL_EXPORTER_PROMETHEUS_PORT
          value: "9090"
        # OTLP for traces
        - name: OTEL_TRACES_EXPORTER
          value: "otlp"
        - name: OTEL_EXPORTER_OTLP_ENDPOINT
          value: "http://otel-collector.observability:4318"
        - name: OTEL_EXPORTER_OTLP_PROTOCOL
          value: "http/protobuf"
        ports:
        - name: metrics
          containerPort: 9090
          protocol: TCP
```

### Function Runner with Mixed Configuration

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: function-runner
  namespace: porch-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: function-runner
  template:
    metadata:
      labels:
        app: function-runner
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9090"
    spec:
      containers:
      - name: function-runner
        image: function-runner:latest
        env:
        # Prometheus for metrics
        - name: OTEL_METRICS_EXPORTER
          value: "prometheus"
        - name: OTEL_EXPORTER_PROMETHEUS_HOST
          value: "0.0.0.0"
        - name: OTEL_EXPORTER_PROMETHEUS_PORT
          value: "9090"
        # OTLP for traces
        - name: OTEL_TRACES_EXPORTER
          value: "otlp"
        - name: OTEL_EXPORTER_OTLP_ENDPOINT
          value: "http://otel-collector.observability:4318"
        - name: OTEL_EXPORTER_OTLP_PROTOCOL
          value: "http/protobuf"
        ports:
        - name: metrics
          containerPort: 9090
```

### Wrapper Server Configuration via Pod Templating

The wrapper-server component can be configured with OpenTelemetry settings through the pod templating mechanism used by the function runner. This is done by creating a ConfigMap with a pod template that includes the necessary environment variables.

#### ConfigMap Pod Template with OpenTelemetry Configuration

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kpt-function-eval-pod-template
  namespace: porch-system
data:
  template: |
    apiVersion: v1
    kind: Pod
    metadata:
      annotations:
        cluster-autoscaler.kubernetes.io/safe-to-evict: "true"
        prometheus.io/scrape: "true"
        prometheus.io/port: "9090"
        prometheus.io/path: "/metrics"
    spec:
      initContainers:
        - name: copy-wrapper-server
          image: ghcr.io/kptdev/porch-wrapper-server:latest
          command: 
            - cp
            - -a
            - /home/nonroot/wrapper-server/.
            - /wrapper-server-tools
          volumeMounts:
            - name: wrapper-server-tools
              mountPath: /wrapper-server-tools
      containers:
        - name: function
          image: image-replaced-by-kpt-func-image
          command: 
            - /wrapper-server-tools/wrapper-server
          env:
            - name: OTEL_METRICS_EXPORTER
              value: "prometheus"
            - name: OTEL_EXPORTER_PROMETHEUS_HOST
              value: "0.0.0.0"
            - name: OTEL_EXPORTER_PROMETHEUS_PORT
              value: "9090"
            - name: OTEL_TRACES_EXPORTER
              value: "otlp"
            - name: OTEL_EXPORTER_OTLP_ENDPOINT
              value: "http://otel-collector.observability:4318"
            - name: OTEL_EXPORTER_OTLP_PROTOCOL
              value: "http/protobuf"
          ports:
            - name: metrics
              containerPort: 9090
              protocol: TCP
          volumeMounts:
            - name: wrapper-server-tools
              mountPath: /wrapper-server-tools
      volumes:
        - name: wrapper-server-tools
          emptyDir: {}
```

The function runner must be configured to use this template by specifying the `--function-pod-template` argument:

```yaml
command:
  - /server
  - --config=/config.yaml
  - --functions=/functions
  - --pod-namespace=porch-fn-system
  - --function-pod-template=kpt-function-eval-pod-template
```

## Context Propagation

Porch automatically configures context propagation using the [autoprop package](https://pkg.go.dev/go.opentelemetry.io/contrib/propagators/autoprop), which supports multiple propagation formats:

- W3C Trace Context (default)
- W3C Baggage
- B3 (Zipkin)
- Jaeger
- AWS X-Ray
- OpenTracing

The propagator is automatically selected based on the `OTEL_PROPAGATORS` environment variable. If not set, W3C Trace Context is used by default.

```yaml
env:
  - name: OTEL_PROPAGATORS
    value: "tracecontext,baggage,b3"
```

## HTTP Instrumentation

All Porch components automatically instrument HTTP clients and servers using [otelhttp](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp), providing:

- Automatic span creation for HTTP requests
- Request/response metrics
- Error tracking
- Distributed tracing across service boundaries

## Signal-Specific Endpoints

You can configure different endpoints for each signal type using signal-specific environment variables. These variables apply to all Porch components.

```yaml
env:
  # Base endpoint (used as fallback for all signals)
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: "http://otel-collector:4318"
  # Signal-specific endpoints (override base endpoint)
  - name: OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
    value: "http://jaeger-collector:4318/v1/traces"
  - name: OTEL_EXPORTER_OTLP_METRICS_ENDPOINT
    value: "http://prometheus-gateway:4318/v1/metrics"
```

This allows routing different telemetry signals to specialized backends.

## Troubleshooting

### Verify Metrics Endpoint

For Prometheus exporters, verify the metrics endpoint is accessible:

```bash
kubectl port-forward -n porch-system deployment/porch-server 9090:9090
curl http://localhost:9090/metrics
```

## Additional Resources

- [OpenTelemetry Autoexport Documentation](https://pkg.go.dev/go.opentelemetry.io/contrib/exporters/autoexport)
- [OpenTelemetry Environment Variables Specification](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/)


---

{{% alert title="Note" color="primary" %}}
The autoexport package automatically handles exporter lifecycle for traces and metrics, including graceful shutdown when the application context is cancelled. All environment variables documented here apply to all Porch components: porch-server, porch-controllers, function-runner, and wrapper-server.
{{% /alert %}}
