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

### Quick Start with Jaeger

Porch includes a ready-to-use Jaeger manifest for a quick OTLP-compatible trace backend. Apply it to your cluster:

```bash
kubectl apply -f - <<'EOF'
kind: ServiceAccount
apiVersion: v1
metadata:
  name: jaeger
  namespace: porch-system
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: jaeger
  namespace: porch-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: jaeger
  template:
    metadata:
      labels:
        app: jaeger
    spec:
      serviceAccountName: jaeger
      containers:
        - name: jaeger
          image: jaegertracing/all-in-one:latest
          imagePullPolicy: IfNotPresent
          resources:
            requests:
              memory: "1024Mi"
              cpu: "250m"
            limits:
              memory: "1024Mi"
---
apiVersion: v1
kind: Service
metadata:
  name: jaeger-otlp
  namespace: porch-system
spec:
  ports:
    - port: 4317
      protocol: TCP
      targetPort: 4317
  selector:
    app: jaeger
---
apiVersion: v1
kind: Service
metadata:
  name: jaeger-http
  namespace: porch-system
spec:
  ports:
    - port: 16686
      protocol: TCP
      targetPort: 16686
  selector:
    app: jaeger
EOF
```

Then enable trace export on Porch Server, Function Runner, and Porch Controllers:

```bash
kubectl set env deployment/porch-server -n porch-system \
  OTEL_TRACES_EXPORTER=otlp \
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://jaeger-otlp:4317 \
  OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=grpc

kubectl set env deployment/function-runner -n porch-system \
  OTEL_TRACES_EXPORTER=otlp \
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://jaeger-otlp:4317 \
  OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=grpc

kubectl set env deployment/porch-controllers -n porch-system \
  OTEL_TRACES_EXPORTER=otlp \
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://jaeger-otlp:4317 \
  OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=grpc
```

Access the Jaeger UI:

```bash
kubectl port-forward -n porch-system service/jaeger-http 16686
```

Open http://localhost:16686 and you should see `porch-server`, `porch-function-runner`, and `porch-controllers` in the service dropdown.

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
    value: "9464"
```

The metrics endpoint will be available at `http://<pod-ip>:9464/metrics`.

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
    prometheus.io/port: "9464"
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
      value: "9464"
    ports:
    - name: metrics
      containerPort: 9464
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
        prometheus.io/port: "9464"
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
          value: "9464"
        # OTLP for traces
        - name: OTEL_TRACES_EXPORTER
          value: "otlp"
        - name: OTEL_EXPORTER_OTLP_ENDPOINT
          value: "http://otel-collector.observability:4318"
        - name: OTEL_EXPORTER_OTLP_PROTOCOL
          value: "http/protobuf"
        ports:
        - name: metrics
          containerPort: 9464
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
        prometheus.io/port: "9464"
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
          value: "9464"
        # OTLP for traces
        - name: OTEL_TRACES_EXPORTER
          value: "otlp"
        - name: OTEL_EXPORTER_OTLP_ENDPOINT
          value: "http://otel-collector.observability:4318"
        - name: OTEL_EXPORTER_OTLP_PROTOCOL
          value: "http/protobuf"
        ports:
        - name: metrics
          containerPort: 9464
```

### Wrapper Server Configuration via Pod Templating

The wrapper-server component can be configured with OpenTelemetry settings through `base-pod-template` used by the Engine pod evaluator. Put the environment variables on the `function` container.

#### PodTemplate fragment with OpenTelemetry configuration

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
        prometheus.io/port: "9464"
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
              value: "9464"
            - name: OTEL_TRACES_EXPORTER
              value: "otlp"
            - name: OTEL_EXPORTER_OTLP_ENDPOINT
              value: "http://otel-collector.observability:4318"
            - name: OTEL_EXPORTER_OTLP_PROTOCOL
              value: "http/protobuf"
          ports:
            - name: metrics
              containerPort: 9464
              protocol: TCP
          volumeMounts:
            - name: wrapper-server-tools
              mountPath: /wrapper-server-tools
      volumes:
        - name: wrapper-server-tools
          emptyDir: {}
```

Apply this OpenTelemetry configuration by editing `base-pod-template` in `porch-fn-system` (see [Pod Templates]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-server-config/pod-templates" %}})). There is no `--function-pod-template` flag.

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


## Available Metrics

Porch records the following metrics via OpenTelemetry:

### Package Size Metrics

| Metric Name                      | Type      | Unit  | Description |
|----------------------------------|-----------|-------|-------------|
| `porch_package_size_bytes`       | Histogram | Bytes | File size of a package's resources expressed as a histogram |
| `porch_package_size_bytes_total` | Gauge     | Bytes | Total file size of a package's resources |

Package size metrics are recorded with the following attributes from the relevant package:

| Attribute        | Description |
|------------------|-------------|
| `namespace`      | Kubernetes namespace of the package revision |
| `repository`     | Name of the repository containing the package |
| `package`        | Path and name of the package |
| `workspace_name` | WorkspaceName of the package revision - short, unique description of the changes |

These metrics are recorded as part of every flow that updates package revision resources:
- Create package revision
- Delete package revision
- Discover/sync package revisions from a registered repository
- Delete package revisions on unregistering a repository
- Direct update of PackageRevisionResources (e.g. `rpkg push`)

**Prometheus metric names:**

When using the Prometheus exporter, these are made available under the metric names:
- `porch_package_size_bytes_bucket`
- `porch_package_size_bytes_count`
- `porch_package_size_bytes_sum`
- `porch_package_size_bytes_total`


## Troubleshooting

### Verify Metrics Endpoint

For Prometheus exporters, verify the metrics endpoint is accessible:

```bash
kubectl port-forward -n porch-system deployment/porch-server 9464:9464
curl http://localhost:9464/metrics
```

## Additional Resources

- [OpenTelemetry Autoexport Documentation](https://pkg.go.dev/go.opentelemetry.io/contrib/exporters/autoexport)
- [OpenTelemetry Environment Variables Specification](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/)

---

{{% alert title="Note" color="primary" %}}
The autoexport package automatically handles exporter lifecycle for traces and metrics, including graceful shutdown when the application context is cancelled. All environment variables documented here apply to all Porch components: porch-server, porch-controllers, function-runner, and wrapper-server.
{{% /alert %}}
