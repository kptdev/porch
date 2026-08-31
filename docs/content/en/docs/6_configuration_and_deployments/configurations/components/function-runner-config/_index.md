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
Binary vs pod selection and most per-function settings come from [FunctionConfig]({{% relref "function-configuration" %}}) resources, not from a static config file.
The flags below are process-wide defaults; a matching FunctionConfig overrides TTL, waitlist length, and parallelism for that image.
Go execution is declared on the same CRD but runs in porch-server and porch-controllers, not in this process.

## Configuration Options

### Command Line Arguments

#### Generic Arguments
```bash
args:
- --port=9445                     # Server port (default: 9445)
- --disable-runtimes=exec,pod     # Disable specific runtimes (exec, pod)
- --log-level=2                   # Log verbosity level 0-5 (default: 2)
- --default-image-prefix=ghcr.io/kptdev/krm-functions-catalog  # Prefix for unqualified function names
```

#### Exec Runtime Arguments
```bash
args:
- --functions=./functions         # Directory of cached function binaries (default: ./functions)
```

Binary-to-image mappings come from FunctionConfig `binaryExecutor` entries.
`--functions` is the directory used when `spec.binaryExecutor.path` is relative.

#### Pod Runtime Arguments
```bash
args:
- --warm-up-pod-cache=true            # Pre-create pods for FunctionConfig podExecutor images (default: true)
- --pod-namespace=porch-fn-system     # Namespace for KRM function pods (default: porch-fn-system)
- --pod-ttl=30m                       # Default pod TTL before GC (default: 30m)
- --scan-interval=1m                  # GC scan interval (default: 1m)
- --max-request-body-size=6291456     # Max gRPC message size in bytes (default: 6MB)
- --max-waitlist-length=2             # Default waitlist length per pod (default: 2)
- --max-parallel-pods-per-function=1  # Default max pods per function (default: 1)
- --max-grpc-retries=2                # Retries on gRPC Unavailable (default: 2)
```

`--pod-ttl`, `--max-waitlist-length`, and `--max-parallel-pods-per-function` are fallbacks used when the matching FunctionConfig does not set `timeToLive`, `preferredMaxQueueLength`, or `maxParallelExecutions`.

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

## FunctionConfig and templates

Per-function executor choice, tags, binary paths, Go ids, pod TTL, and template overrides are declared on FunctionConfig objects in `porch-fn-system`.
The function-runner runs an embedded reconciler that watches those objects and updates its in-memory store without a process restart.

The pod evaluator builds function pods from the `base-pod-template` `PodTemplate` and `base-service-template` `ServiceTemplate` in the pod namespace, then applies `spec.podExecutor.templateOverrides`.
There is no `--function-pod-template` flag and no ConfigMap template.

See [Function Configuration]({{% relref "function-configuration" %}}) and [Pod Templates]({{% relref "pod-templates" %}}).

## Runtime Configuration

### Exec Runtime

The exec runtime runs functions as local binaries listed on a FunctionConfig `binaryExecutor`.
`--functions` is only the directory that relative `path` values are resolved against.

```bash
args:
- --functions=./functions
```

### Pod Runtime

The pod runtime runs functions as Kubernetes pods.
Cache warming walks FunctionConfig objects that have a `podExecutor` and pre-creates one pod for the first prefix/tag of each.

```bash
args:
- --pod-namespace=porch-fn-system # Namespace for function pods
- --pod-ttl=30m                   # How long pods live before cleanup
- --scan-interval=1m              # How often to scan for expired pods
- --warm-up-pod-cache=true        # Pre-deploy common function pods
```

### Disabling Runtimes

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

{{% alert title="Note" color="primary" %}}
For advanced configuration options:
- [Function Configuration]({{% relref "function-configuration" %}}) - Executor selection and per-function settings
- [Pod Templates]({{% relref "pod-templates" %}}) - Base PodTemplate / ServiceTemplate and templateOverrides
- [Private Registries]({{% relref "private-registries-config" %}}) - Configure private registry access
{{% /alert %}}
