---
title: "Porch Server"
type: docs
weight: 1
description: "Configure the Porch API server component"
---

The Porch server is the main API server component that handles package operations and Git repository interactions.

## Configuration Options

### Command Line Arguments

#### Core Server Arguments
```bash
args:
- --cache-directory=/cache/porch              # Directory for repository and package caches
- --cache-type=CR                             # Cache type: CR (Custom Resource) or DB (Database)
- --function-runner=function-runner:9445      # Function runner gRPC service address
- --max-request-body-size=6291456             # Max request body size in bytes (6MB)
- --standalone-debug-mode=false               # Local debugging mode (dev only)
```

#### Repository Management Arguments
```bash
args:
- --repo-sync-frequency=10m                  # Repository sync frequency
- --repo-operation-retry-attempts=3          # Retry attempts for repo operations
- --retryable-git-errors=pattern1,pattern2   # Additional retryable git error patterns
- --list-timeout-per-repo=20s                # Timeout per repository list request
- --max-parallel-repo-lists=10               # Max concurrent repository lists
- --use-user-cabundle=false                  # Enable custom CA bundle for Git TLS
```

#### Database Cache Arguments
```bash
args:
- --db-cache-driver=pgx                      # Database driver (pgx, mysql)
- --db-cache-data-source=connection-string   # Database connection string
```

#### Function Runtime Arguments
```bash
args:
- --function-runner=function-runner:9445      # Function Runner gRPC address (exec fast path)
- --default-image-prefix=ghcr.io/kptdev/krm-functions-catalog  # Default function image prefix
- --functions=/home/nonroot/functions         # On-disk FunctionConfig binary cache
- --pod-namespace=porch-fn-system             # Namespace for function pods and FunctionConfigs
```

#### Pod Evaluator Arguments

Porch-server **requires** `WRAPPER_SERVER_IMAGE` and will not start without it. These flags configure the in-process pod evaluator:

```bash
args:
- --warm-up-pod-cache=true         # Pre-create pods from the warm-up config (default: true)
- --pod-ttl=30m                    # Pod TTL before GC (default: 30m)
- --scan-interval=1m               # GC scan interval (default: 1m)
- --max-waitlist-length=1          # Max waiters per pod (deployment default; flag default is 2)
- --max-parallel-pods-per-function=2  # Max parallel pods per function image
- --enable-private-registries=false
- --registry-auth-secret-path=/var/tmp/config-secret/.dockerconfigjson
- --registry-auth-secret-name=auth-secret
- --enable-private-registries-tls=false
- --tls-secret-path=/var/tmp/tls-secret/
```

```bash
env:
- name: WRAPPER_SERVER_IMAGE
  value: "ghcr.io/kptdev/porch-wrapper-server:latest"  # Required
```

For function pod specs see [Pod Templates]({{% relref "pod-templates" %}}). For registry auth see [Private Registries]({{% relref "private-registries-config" %}}).

### Environment Variables

#### Database Configuration (when using DB cache)
```bash
env:
- name: DB_DRIVER
  value: "pgx"                    # Database driver
- name: DB_HOST
  value: "postgresql.example.com" # Database host
- name: DB_PORT
  value: "5432"                   # Database port
- name: DB_NAME
  value: "porch"                  # Database name
- name: DB_USER
  value: "porch_user"             # Database user
- name: DB_PASSWORD
  value: "your_password"          # Database password
- name: DB_SSL_MODE
  value: "disable"                # SSL mode (optional)
```

## Git Repository Authentication

For detailed Git repository authentication configuration, see [Git Authentication]({{% relref "git-authentication" %}}) subsection.

## Webhook Certificate Management

For cert-manager integration with Porch webhooks, see [Cert Manager Webhooks]({{% relref "cert-manager-webhooks" %}}) subsection.

## Distributed Tracing

For tracing and metrics configuration, see [OpenTelemetry Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/opentelemetry" %}}).

## Resource Limits

```yaml
resources:
  requests:
    memory: "256Mi"
    cpu: "100m"
  limits:
    memory: "512Mi"
    cpu: "500m"
```

## Health Checks

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 30
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /readyz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5
```