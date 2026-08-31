---
title: "OpenTelemetry Configuration"
type: docs
weight: 4
description: Configure OpenTelemetry metrics, traces, and pprof profiling for Porch components
---

## Overview

Porch supports OpenTelemetry observability through the [autoexport package](https://pkg.go.dev/go.opentelemetry.io/contrib/exporters/autoexport), which provides automatic configuration of metrics and traces exporters via environment variables. This enables seamless integration with various observability backends including OTLP collectors, Prometheus, and Jaeger.

All Porch components (porch-server, porch-controllers, function-runner, and wrapper-server) support OpenTelemetry configuration through standardized environment variables as defined by the [OpenTelemetry specification](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/).

Porch also exposes Go [pprof](https://pkg.go.dev/net/http/pprof) endpoints for continuous profiling. Grafana Alloy can scrape those endpoints into Pyroscope when the pod annotations and `PORCH_PPROF_PORT` environment variable described in [Pprof Configuration](#pprof-configuration) are set.

Default Kind deployments already export Prometheus metrics on port 9464 (`OTEL_METRICS_EXPORTER=prometheus`) and declare a container port named `pprof`. Trace export and the pprof HTTP server stay disabled until you set the corresponding environment variables.

{{% alert title="Note" color="primary" %}}
**Current Implementation Status**: Porch currently implements metrics and traces export. Logs export is not supported.
{{% /alert %}}

For a local Prometheus, Grafana, Jaeger, Pyroscope, and Grafana Alloy stack, see [Local Performance Monitoring Deployment]({{% relref "/docs/6_configuration_and_deployments/deployments/local-performance-monitoring-deployment" %}}). For load-test metrics emitted by the test process, see [Performance Tests]({{% relref "/docs/12_contributing/code-contribution/performance-tests" %}}).

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

To deploy Jaeger as part of the local monitoring stack (namespace `porch-monitoring`) instead of applying the manifest above, see [Local Performance Monitoring Deployment]({{% relref "/docs/6_configuration_and_deployments/deployments/local-performance-monitoring-deployment" %}}).

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

## Pprof Configuration

Porch components start a pprof HTTP server when `PORCH_PPROF_PORT` is set. Grafana Alloy uses `profiles.grafana.com/*` pod annotations to discover those endpoints and ship profiles to Pyroscope.

{{% alert title="Warning" color="warning" %}}
Enabling pprof turns on mutex and block profiling (`runtime.SetMutexProfileFraction(1)` and `runtime.SetBlockProfileRate(1)`). That adds overhead. Use it in development and performance-test clusters, not as a default in production, unless you have measured the cost.
{{% /alert %}}

Pprof applies to porch-server, function-runner, and porch-controllers. wrapper-server does not expose a pprof server.

### Environment Variable

| Variable | Description | Default |
|----------|-------------|---------|
| `PORCH_PPROF_PORT` | TCP port for the pprof HTTP server. If unset, the server does not start. | unset (disabled) |

Set it to match the named container port (typically `8080`):

```yaml
env:
  - name: PORCH_PPROF_PORT
    value: "8080"
```

### Container Port

The container must expose a port named `pprof` so Alloy can resolve `profiles.grafana.com/*/port_name: pprof`. Default Kind manifests already declare this port in `deployments/porch/3-porch-server.yaml`, `deployments/porch/2-function-runner.yaml`, and `deployments/porch/9-controllers.yaml`:

```yaml
ports:
  - containerPort: 9464
    name: metrics
    protocol: TCP
  - containerPort: 8080
    name: pprof
    protocol: TCP
```

### Pod Annotations for Pyroscope

Add these annotations on the pod template so Grafana Alloy scrapes the pprof port:

```yaml
metadata:
  annotations:
    profiles.grafana.com/service_name: "porch-server"   # or porch-function-runner / porch-controllers
    profiles.grafana.com/cpu.scrape: "true"
    profiles.grafana.com/cpu.port_name: "pprof"
    profiles.grafana.com/memory.scrape: "true"
    profiles.grafana.com/memory.port_name: "pprof"
    profiles.grafana.com/goroutine.scrape: "true"
    profiles.grafana.com/goroutine.port_name: "pprof"
    profiles.grafana.com/block.scrape: "true"
    profiles.grafana.com/block.port_name: "pprof"
    profiles.grafana.com/mutex.scrape: "true"
    profiles.grafana.com/mutex.port_name: "pprof"
```

| Annotation | Purpose |
|------------|---------|
| `profiles.grafana.com/service_name` | Service name in Pyroscope (`porch-server`, `porch-function-runner`, or `porch-controllers`) |
| `profiles.grafana.com/cpu.scrape` | Scrape CPU profiles (`/debug/pprof/profile`) |
| `profiles.grafana.com/memory.scrape` | Scrape heap profiles (`/debug/pprof/heap`) |
| `profiles.grafana.com/goroutine.scrape` | Scrape goroutine profiles (`/debug/pprof/goroutine`) |
| `profiles.grafana.com/block.scrape` | Scrape block profiles (`/debug/pprof/block`) |
| `profiles.grafana.com/mutex.scrape` | Scrape mutex profiles (`/debug/pprof/mutex`) |
| `profiles.grafana.com/*/port_name` | Named container port to scrape (`pprof`) |

### Profiles Available from the Pprof Server

| Endpoint | Profile | Scraped by Alloy |
|----------|---------|------------------|
| `/debug/pprof/profile` | CPU | yes (`cpu`) |
| `/debug/pprof/heap` | Heap memory | yes (`memory`) |
| `/debug/pprof/goroutine` | Goroutine stacks | yes (`goroutine`) |
| `/debug/pprof/block` | Blocking (off-CPU) | yes (`block`) |
| `/debug/pprof/mutex` | Mutex contention | yes (`mutex`) |
| `/debug/pprof/allocs` | Memory allocations | no (available via HTTP) |
| `/debug/pprof/threadcreate` | Thread creation | no (available via HTTP) |
| `/debug/pprof/trace` | Execution trace | no (available via HTTP) |
| `/debug/pprof/cmdline` | Process command line | no (available via HTTP) |
| `/debug/pprof/symbol` | Symbol lookup | no (available via HTTP) |
| `/debug/pprof/` | Index of all profiles | no (available via HTTP) |

Alloy writes the five scraped profile types into Pyroscope. View flame graphs in the Pyroscope UI (http://localhost:4040 when using the local stack) or the **Pyroscope – Porch profiling** Grafana dashboard. See [Local Performance Monitoring Deployment]({{% relref "/docs/6_configuration_and_deployments/deployments/local-performance-monitoring-deployment" %}}) for URLs and dashboards.

### Complete Pprof Example

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
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9464"
        prometheus.io/path: "/metrics"
        profiles.grafana.com/service_name: "porch-server"
        profiles.grafana.com/cpu.scrape: "true"
        profiles.grafana.com/cpu.port_name: "pprof"
        profiles.grafana.com/memory.scrape: "true"
        profiles.grafana.com/memory.port_name: "pprof"
        profiles.grafana.com/goroutine.scrape: "true"
        profiles.grafana.com/goroutine.port_name: "pprof"
        profiles.grafana.com/block.scrape: "true"
        profiles.grafana.com/block.port_name: "pprof"
        profiles.grafana.com/mutex.scrape: "true"
        profiles.grafana.com/mutex.port_name: "pprof"
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
        - name: PORCH_PPROF_PORT
          value: "8080"
        ports:
        - name: metrics
          containerPort: 9464
          protocol: TCP
        - name: pprof
          containerPort: 8080
          protocol: TCP
```

Repeat the same `PORCH_PPROF_PORT`, named `pprof` port, and `profiles.grafana.com/*` annotations on `function-runner` (service name `porch-function-runner`) and `porch-controllers` (service name `porch-controllers`).

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

Porch supports native Prometheus metrics export through an HTTP endpoint. This is the recommended approach for Kubernetes environments with Prometheus-based monitoring. Default Kind manifests already set these variables on porch-server, function-runner, and porch-controllers.

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

The local monitoring stack scrapes Porch Services on port 9464 directly rather than relying on these annotations. See [What Prometheus Scrapes]({{% relref "/docs/6_configuration_and_deployments/deployments/local-performance-monitoring-deployment#what-prometheus-scrapes" %}}).

## Complete Deployment Examples

The examples below combine Prometheus metrics, optional OTLP traces, Prometheus scrape annotations, and pprof/Pyroscope annotations. Adjust exporters to match your backend.

### Porch Server with Prometheus, OTLP traces, and pprof

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
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9464"
        prometheus.io/path: "/metrics"
        profiles.grafana.com/service_name: "porch-server"
        profiles.grafana.com/cpu.scrape: "true"
        profiles.grafana.com/cpu.port_name: "pprof"
        profiles.grafana.com/memory.scrape: "true"
        profiles.grafana.com/memory.port_name: "pprof"
        profiles.grafana.com/goroutine.scrape: "true"
        profiles.grafana.com/goroutine.port_name: "pprof"
        profiles.grafana.com/block.scrape: "true"
        profiles.grafana.com/block.port_name: "pprof"
        profiles.grafana.com/mutex.scrape: "true"
        profiles.grafana.com/mutex.port_name: "pprof"
    spec:
      containers:
      - name: porch-server
        image: porch-server:latest
        env:
        - name: OTEL_SERVICE_NAME
          value: "porch-server"
        - name: OTEL_METRICS_EXPORTER
          value: "prometheus"
        - name: OTEL_EXPORTER_PROMETHEUS_HOST
          value: "0.0.0.0"
        - name: OTEL_EXPORTER_PROMETHEUS_PORT
          value: "9464"
        - name: OTEL_TRACES_EXPORTER
          value: "otlp"
        - name: OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
          value: "http://otel-collector.observability:4318"
        - name: OTEL_EXPORTER_OTLP_TRACES_PROTOCOL
          value: "http/protobuf"
        - name: PORCH_PPROF_PORT
          value: "8080"
        ports:
        - name: metrics
          containerPort: 9464
          protocol: TCP
        - name: pprof
          containerPort: 8080
          protocol: TCP
```

### Porch Controllers with Prometheus Metrics, OTLP Traces, and pprof

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
        profiles.grafana.com/service_name: "porch-controllers"
        profiles.grafana.com/cpu.scrape: "true"
        profiles.grafana.com/cpu.port_name: "pprof"
        profiles.grafana.com/memory.scrape: "true"
        profiles.grafana.com/memory.port_name: "pprof"
        profiles.grafana.com/goroutine.scrape: "true"
        profiles.grafana.com/goroutine.port_name: "pprof"
        profiles.grafana.com/block.scrape: "true"
        profiles.grafana.com/block.port_name: "pprof"
        profiles.grafana.com/mutex.scrape: "true"
        profiles.grafana.com/mutex.port_name: "pprof"
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
        - name: PORCH_PPROF_PORT
          value: "8080"
        ports:
        - name: metrics
          containerPort: 9464
          protocol: TCP
        - name: pprof
          containerPort: 8080
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
        prometheus.io/path: "/metrics"
        profiles.grafana.com/service_name: "porch-function-runner"
        profiles.grafana.com/cpu.scrape: "true"
        profiles.grafana.com/cpu.port_name: "pprof"
        profiles.grafana.com/memory.scrape: "true"
        profiles.grafana.com/memory.port_name: "pprof"
        profiles.grafana.com/goroutine.scrape: "true"
        profiles.grafana.com/goroutine.port_name: "pprof"
        profiles.grafana.com/block.scrape: "true"
        profiles.grafana.com/block.port_name: "pprof"
        profiles.grafana.com/mutex.scrape: "true"
        profiles.grafana.com/mutex.port_name: "pprof"
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
        - name: PORCH_PPROF_PORT
          value: "8080"
        ports:
        - name: metrics
          containerPort: 9464
        - name: pprof
          containerPort: 8080
```

### Wrapper Server Configuration via Pod Templating

The wrapper-server component can be configured with OpenTelemetry settings through the pod templating mechanism used by the function runner. This is done by creating a ConfigMap with a pod template that includes the necessary environment variables.

wrapper-server does not expose pprof. Configure Prometheus metrics and optional OTLP traces only.

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

## Available Metrics

Porch records the following metrics via OpenTelemetry. They are emitted by porch-server and porch-controllers (and, where applicable, function-runner). View them on the **Porch API** and **Porch File-system Resources** Grafana dashboards when using the [local monitoring stack]({{% relref "/docs/6_configuration_and_deployments/deployments/local-performance-monitoring-deployment" %}}).

Load-test process metrics (`porch_perf_*`) are documented separately in [Performance Tests]({{% relref "/docs/12_contributing/code-contribution/performance-tests#performance-test-metrics" %}}).

### API Call Duration

| Metric Name | Type | Unit | Description |
|-------------|------|------|-------------|
| `porch_api_call_duration_seconds` | Histogram | seconds | Duration of Porch API calls and related operations |

| Attribute | Description |
|-----------|-------------|
| `resource` | Resource kind (see table below) |
| `verb` | Operation name (see table below) |
| `api_version` | `v1alpha1` or `v1alpha2`. Omitted for `ExternalRepo` operations, which are shared infrastructure |

Histogram buckets start at 0.001 seconds and double for 16 boundaries (up to 32.768 seconds).

### Request Counts

| Metric Name | Type | Unit | Description |
|-------------|------|------|-------------|
| `porch_api_requests_by_user` | Counter | — | Total number of requests, broken down by resource, operation, and user |

| Attribute | Description |
|-----------|-------------|
| `resource` | Resource kind (see table below) |
| `op` | Operation name (same values as `verb` on the duration histogram) |
| `user` | Kubernetes user from the request context. v1alpha2 controller operations use `packagerevision-controller` |
| `api_version` | `v1alpha1` or `v1alpha2`. Omitted for `ExternalRepo` operations |

### Resources and Operations

| `resource` | Component | `verb` / `op` values | `api_version` |
|------------|-----------|----------------------|---------------|
| `PackageRevision` | porch-server | `LIST`, `GET`, `CREATE`, `UPDATE`, `DELETE` | `v1alpha1` |
| `PackageRevision` | porch-controllers | `CREATE`, `UPDATE`, `DELETE` | `v1alpha2` |
| `PackageRevisionResources` | porch-server | `LIST`, `GET`, `UPDATE` | `v1alpha1` |
| `PackageRevisionResources` | porch-controllers | `UPDATE` | `v1alpha2` |
| `PackageRevisionApproval` | porch-server | `GET`, `UPDATE` | `v1alpha1` |
| `ExternalRepo` | porch-server (git) | `FETCH`, `PUSH` | omitted |

### Package Size Metrics

| Metric Name | Type | Unit | Description |
|-------------|------|------|-------------|
| `porch_package_size_bytes` | Histogram | Bytes | File size of a package's resources expressed as a histogram |
| `porch_package_size_bytes_total` | Gauge | Bytes | Total file size of a package's resources |

Package size metrics are recorded with the following attributes from the relevant package:

| Attribute | Description |
|-----------|-------------|
| `namespace` | Kubernetes namespace of the package revision |
| `repository` | Name of the repository containing the package |
| `package` | Path and name of the package |
| `workspace_name` | WorkspaceName of the package revision - short, unique description of the changes |

These metrics are recorded as part of every flow that updates package revision resources:

- Create package revision
- Delete package revision
- Discover/sync package revisions from a registered repository
- Delete package revisions on unregistering a repository
- Direct update of PackageRevisionResources (e.g. `rpkg push`)

Histogram buckets start at 0, then 1024 bytes, doubling for 21 further boundaries.

### Prometheus Metric Names

When using the Prometheus exporter, these are made available under the metric names:

**API call duration**

- `porch_api_call_duration_seconds_bucket`
- `porch_api_call_duration_seconds_count`
- `porch_api_call_duration_seconds_sum`

**Request counts**

- `porch_api_requests_by_user_total`

**Package size**

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

### Verify Pprof Endpoint

The pprof server listens only when `PORCH_PPROF_PORT` is set:

```bash
kubectl port-forward -n porch-system deployment/porch-server 8080:8080
curl http://localhost:8080/debug/pprof/
```

## Additional Resources

- [OpenTelemetry Autoexport Documentation](https://pkg.go.dev/go.opentelemetry.io/contrib/exporters/autoexport)
- [OpenTelemetry Environment Variables Specification](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/)
- [Go pprof](https://pkg.go.dev/net/http/pprof)
- [Grafana Alloy profiling annotations](https://grafana.com/docs/alloy/latest/collect/profiles-via-annotation/)

## See Also

- [Local Performance Monitoring Deployment]({{% relref "/docs/6_configuration_and_deployments/deployments/local-performance-monitoring-deployment" %}}) — local Prometheus, Grafana, Jaeger, Pyroscope, and Grafana Alloy
- [Performance Tests]({{% relref "/docs/12_contributing/code-contribution/performance-tests" %}}) — load tests and `porch_perf_*` metrics

---

{{% alert title="Note" color="primary" %}}
The autoexport package automatically handles exporter lifecycle for traces and metrics, including graceful shutdown when the application context is cancelled. All OpenTelemetry environment variables documented here apply to all Porch components: porch-server, porch-controllers, function-runner, and wrapper-server. `PORCH_PPROF_PORT` applies to porch-server, porch-controllers, and function-runner only.
{{% /alert %}}
