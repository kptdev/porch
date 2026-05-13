---
title: "Jaeger Tracing"
type: docs
weight: 3
description: "Configure distributed tracing with Jaeger for debugging and monitoring Porch Server operations"
---

Jaeger tracing provides distributed tracing capabilities for the Porch Server, allowing you to monitor and debug package operations. This is particularly useful for development and troubleshooting.

{{% alert title="Note" color="primary" %}}
Jaeger tracing is currently only supported for the Porch Server component. Function Runner and Porch Controllers do not support tracing.
{{% /alert %}}

## Overview

Porch Server supports OpenTelemetry tracing that can be exported to Jaeger for visualization. When enabled, Porch Server will generate traces for:

- Package operations (create, update, delete)
- Git repository interactions
- Function execution requests
- API requests and responses

## Deployment Setup

### Deploy Jaeger to Kubernetes

Porch includes a ready-to-use Jaeger deployment:

```bash
# Deploy Jaeger to your cluster
kubectl apply -f https://raw.githubusercontent.com/kptdev/porch/main/deployments/tracing/deployment.yaml
```

This creates:
- Jaeger all-in-one deployment in `porch-system` namespace
- OTLP service on port 4317 for trace ingestion
- HTTP service on port 16686 for the Jaeger UI

### Enable Tracing in Porch Server

Add the `OTEL` environment variable to the Porch Server deployment:

```bash
kubectl edit deployment -n porch-system porch-server
```

Add the environment variable:

```yaml
spec:
  template:
    spec:
      containers:
      - name: porch-server
        env:
        - name: OTEL
          value: otel://jaeger-oltp:4317
        - name: OTEL_SERVICE_NAME
          value: porch-server
```

### Access Jaeger UI

Set up port forwarding to access the Jaeger UI:

```bash
kubectl port-forward -n porch-system service/jaeger-http 16686
```

Open your browser to: http://localhost:16686

## Local Development Setup

For local development with Porch Server running outside Kubernetes:

### Install Jaeger Locally

1. Download Jaeger from the [official releases](https://www.jaegertracing.io/download/#binaries)
2. Extract and run the all-in-one binary:

```bash
cd jaeger
./jaeger-all-in-one
```

### Configure Local Porch Server

Set the `OTEL` environment variable:

**In VS Code (.vscode/launch.json):**
```json
{
  "env": {
    "OTEL": "otel://localhost:4317",
    "OTEL_SERVICE_NAME": "porch-server"
  }
}
```

**In shell:**
```bash
export OTEL="otel://localhost:4317"
export OTEL_SERVICE_NAME="porch-server"
```

## Environment Variables

Porch Server tracing configuration supports these environment variables:

| Variable | Description | Example |
|----------|-------------|----------|
| `OTEL` | OpenTelemetry endpoint URL | `otel://jaeger-oltp:4317` |
| `OTEL_SERVICE_NAME` | Service name in traces | `porch-server` |

## Using Jaeger UI

### Finding Traces

1. **Select Service**: Choose `porch-server` from the service dropdown
2. **Set Time Range**: Adjust the time range for your traces
3. **Search**: Click "Find Traces" to see available traces

### Trace Analysis

Each trace shows:
- **Operation Name**: The specific Porch operation (e.g., `CreatePackageRevision`)
- **Duration**: How long the operation took
- **Spans**: Individual steps within the operation
- **Tags**: Metadata about the operation (package name, repository, etc.)
- **Logs**: Detailed log messages from the operation

### Common Trace Patterns

**Package Creation:**
- Repository validation
- Git operations
- Function execution requests
- Resource creation

**Package Updates:**
- Diff calculation
- Function pipeline execution
- Git commit operations

## Troubleshooting

### No Traces Appearing

1. **Check Porch Server logs:**
```bash
kubectl logs -n porch-system deployment/porch-server
```

2. **Verify OTEL environment variable:**
```bash
kubectl get deployment -n porch-system porch-server -o yaml | grep -A5 env
```

3. **Check Jaeger connectivity:**
```bash
kubectl get svc -n porch-system jaeger-oltp
```

### Jaeger UI Not Accessible

1. **Verify port forwarding:**
```bash
kubectl get svc -n porch-system jaeger-http
```

2. **Check Jaeger pod status:**
```bash
kubectl get pods -n porch-system -l app=jaeger
```

### Performance Impact

- Tracing adds minimal overhead in production
- For high-volume environments, consider sampling rates
- Disable tracing by removing the `OTEL` environment variable

## Best Practices

- **Development**: Enable tracing for debugging complex package operations
- **Staging**: Use tracing to validate performance before production
- **Production**: Enable selectively or with sampling for critical debugging
- **Monitoring**: Use traces to identify bottlenecks in package operations

## Integration with Other Tools

Jaeger traces can be:
- Exported to other observability platforms
- Integrated with metrics and logging systems
- Used with performance monitoring tools
- Analyzed programmatically via Jaeger APIs