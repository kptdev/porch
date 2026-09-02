---
title: "Function Runner"
type: docs
weight: 3
description: "Configure the Function Runner component"
---

{{% alert title="Note" color="primary" %}}
KPT functions and KRM functions are synonymous terms referring to the same containerized functions.
{{% /alert %}}

The Function Runner executes KRM functions in a secure, isolated environment.

## Configuration Options

### Command Line Arguments

#### Generic Arguments
```bash
args:
- --port=9445                    # Server port (default: 9445)
- --disable-runtimes=exec,pod     # Disable specific runtimes (exec, pod)
- --log-level=2                   # Log verbosity level 0-5 (default: 2)
```

#### Exec Runtime Arguments
```bash
args:
- --functions=./functions         # Path to cached functions (default: ./functions)
- --config=./config.yaml          # Path to exec runtime config file (default: ./config.yaml)
```

#### Pod Runtime Arguments
```bash
args:
- --pod-cache-config=/pod-cache-config/pod-cache-config.yaml  # Pod cache config file path
- --warm-up-pod-cache=true         # Warm up pod cache on startup (default: true)
- --pod-namespace=porch-fn-system  # Namespace for KRM function pods (default: porch-fn-system)
- --pod-ttl=30m                    # Pod TTL before GC (default: 30m)
- --scan-interval=1m               # GC scan interval (default: 1m)
- --function-pod-template=         # ConfigMap with pod specification
- --max-request-body-size=6291456  # Max gRPC message size in bytes (default: 6MB)
- --max-waitlist-length            # Maximum waitlist length per pod
- --max-parallel-pods-per-function # Maximum parallel pods per function
```

#### Private Registry Arguments
```bash
args:
- --enable-private-registries=false              # Enable private registry support
- --registry-auth-secret-path=/var/tmp/config-secret/.dockerconfigjson  # Registry auth secret path
- --registry-auth-secret-name=auth-secret        # Registry auth secret name
- --enable-private-registries-tls=false          # Enable TLS for private registries
- --tls-secret-path=/var/tmp/tls-secret/         # TLS secret path
```

### Environment Variables

```bash
env:
- name: WRAPPER_SERVER_IMAGE
  value: "<wrapper-server-image>"  # Required for pod runtime
```

## Advanced Configuration

### Pod Templates

Customize function evaluator pod specifications using ConfigMap templates:

```bash
args:
- --function-pod-template=kpt-function-eval-pod-template  # ConfigMap name
```

For detailed pod template configuration, see [Pod Templates]({{% relref "pod-templates" %}}) documentation.

## Runtime Configuration

### Exec Runtime

The exec runtime runs functions as local executables:

```bash
args:
- --functions=./functions         # Directory containing cached function executables
- --config=./config.yaml          # Configuration file for exec runtime
```

### Pod Runtime

The pod runtime runs functions as Kubernetes pods:

```bash
args:
- --pod-namespace=porch-fn-system # Namespace for function pods
- --pod-ttl=30m                   # How long pods live before cleanup
- --scan-interval=1m              # How often to scan for expired pods
- --warm-up-pod-cache=true        # Pre-deploy common function pods
```

### Disabling Runtimes

To disable specific runtimes:

```bash
args:
- --disable-runtimes=exec         # Disable exec runtime only
- --disable-runtimes=pod          # Disable pod runtime only
- --disable-runtimes=exec,pod     # Disable both runtimes
```

## Resource Limits

```bash
resources:
  requests:
    memory: "512Mi"
    cpu: "200m"
  limits:
    memory: "1Gi"
    cpu: "1000m"
```

## Health Checks

```bash
livenessProbe:
  grpc:
    port: 9445
  initialDelaySeconds: 30
  periodSeconds: 10

readinessProbe:
  grpc:
    port: 9445
  initialDelaySeconds: 5
  periodSeconds: 5
```

## Complete Example

Complete Function Runner deployment configuration:

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
    spec:
      containers:
      - name: function-runner
        image: function-runner:latest
        args:
        - --port=9445
        - --log-level=2
        - --pod-namespace=porch-fn-system
        - --pod-ttl=30m
        - --scan-interval=1m
        - --warm-up-pod-cache=true
        - --max-request-body-size=6291456
        env:
        - name: WRAPPER_SERVER_IMAGE
          value: "wrapper-server:latest"
        ports:
        - containerPort: 9445
          protocol: TCP
        resources:
          requests:
            memory: "512Mi"
            cpu: "200m"
          limits:
            memory: "1Gi"
            cpu: "1000m"
        livenessProbe:
          grpc:
            port: 9445
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          grpc:
            port: 9445
          initialDelaySeconds: 5
          periodSeconds: 5
```

## Observability

For tracing, metrics, and pprof configuration, see [OpenTelemetry Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/opentelemetry" %}}). For a local Prometheus, Grafana, Jaeger, Pyroscope, and Grafana Alloy stack, see [Local Performance Monitoring Deployment]({{% relref "/docs/6_configuration_and_deployments/deployments/local-performance-monitoring-deployment" %}}).

{{% alert title="Note" color="primary" %}}
For advanced configuration options:
- [Pod Templates]({{% relref "pod-templates" %}}) - Customize function pod specifications
- [Private Registries]({{% relref "private-registries-config" %}}) - Configure private registry access
{{% /alert %}}