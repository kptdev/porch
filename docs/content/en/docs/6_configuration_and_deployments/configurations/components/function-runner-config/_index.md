---
title: "Function Runner"
type: docs
weight: 3
description: "Configure the Function Runner component"
---

The Function Runner executes cached KRM function binaries over gRPC. Pod runtime flags moved to [Porch Server]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-server-config" %}}) with the pod evaluator.

{{% alert title="Note" color="primary" %}}
KPT functions and KRM functions are synonymous terms referring to the same containerized functions.
{{% /alert %}}

Binary vs pod selection and most per-function settings come from [FunctionConfig]({{% relref "function-configuration" %}}) resources, not from a static config file.
The Engine looks up a cached binary in its FunctionConfig store and sends `exec_path` on the gRPC request.
Function Runner executes that binary and returns `NotFoundError` when `exec_path` is empty so the Engine can fall through to the in-process pod evaluator.
Go execution is declared on the same CRD but runs in porch-server and porch-controllers, not in this process.

## Configuration Options

### Command Line Arguments

#### Generic Arguments
```bash
args:
- --port=9445                                                  # Server port (default: 9445)
- --disable-runtimes=exec                                      # Disable the exec runtime (the only runtime in this binary)
- --log-level=2                                                # Log verbosity level 0-5 (default: 2)
- --default-image-prefix=ghcr.io/kptdev/krm-functions-catalog  # Prefix for unqualified function names
```

#### Exec Runtime Arguments
```bash
args:
- --functions=./functions  # Directory of cached function binaries (default: ./functions)
```

Binary-to-image mappings come from FunctionConfig `binaryExecutor` entries.
`--functions` is the directory used when `spec.binaryExecutor.path` is relative.
The Engine may also send `exec_path` on the gRPC request. Function Runner does not read a `--config` image-to-binary mapping file.

#### Pod Runtime Arguments

These flags now belong to **porch-server**. See [Porch Server]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-server-config" %}}). They moved with the pod evaluator:
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

These flags now belong to **porch-server**. See [Private Registries]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-server-config/private-registries-config" %}}).
```bash
args:
- --enable-private-registries=false                                     # Enable private registry support
- --registry-auth-secret-path=/var/tmp/config-secret/.dockerconfigjson  # Registry auth secret path
- --registry-auth-secret-name=auth-secret                               # Registry auth secret name
- --enable-private-registries-tls=false                                 # Enable TLS for private registries
- --tls-secret-path=/var/tmp/tls-secret/                                # TLS secret path
```

### Environment Variables

`WRAPPER_SERVER_IMAGE` is required on **porch-server**. The PackageRevision controller uses porch-server's FunctionEvaluator gRPC (`POD_EVALUATOR_ADDRESS`); do not start a second in-process evaluator on controllers.

```bash
env:
- name: WRAPPER_SERVER_IMAGE
  value: "<wrapper-server-image>"  # Required for the Engine pod evaluator
```

## FunctionConfig and templates

Per-function executor choice, tags, binary paths, Go ids, pod TTL, and template overrides are declared on FunctionConfig objects in `porch-fn-system`.
The function-runner runs an embedded reconciler that watches those objects and updates its in-memory binary store without a process restart.

The Engine pod evaluator builds function pods from the `base-pod-template` `PodTemplate` and `base-service-template` `ServiceTemplate` in the pod namespace, then applies `spec.podExecutor.templateOverrides`.
There is no `--function-pod-template` flag and no ConfigMap template.

See [Function Configuration]({{% relref "function-configuration" %}}) and [Pod Templates]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-server-config/pod-templates" %}}).

## Runtime Configuration

### Exec Runtime

The exec runtime runs functions as local binaries listed on a FunctionConfig `binaryExecutor`.
`--functions` is only the directory that relative `path` values are resolved against.

```bash
args:
- --functions=/home/nonroot/functions         # Directory containing cached function executables
```

The Engine supplies `exec_path`; Function Runner does not use `--config`.

### Pod Runtime

The pod runtime runs in the Engine (porch-server). See [Porch Server]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-server-config" %}}).

### Disabling Runtimes

To disable the exec runtime:

```bash
args:
- --disable-runtimes=exec         # Disable exec runtime
```

`--disable-runtimes=pod` is not valid; the pod evaluator is not in this binary.

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
        - --functions=/home/nonroot/functions
        - --max-request-body-size=6291456
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
- [Pod Templates]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-server-config/pod-templates" %}}) - Customize function pod specifications
- [Private Registries]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-server-config/private-registries-config" %}}) - Configure private registry access
{{% /alert %}}
