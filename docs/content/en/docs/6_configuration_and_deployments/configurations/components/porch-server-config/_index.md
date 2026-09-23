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
- --cache-type=DB                            # Required for database cache (see also Core Server Arguments)
- --db-cache-driver=pgx                      # Database driver (pgx, mysql)
- --db-cache-data-source=connection-string   # Database connection string
- --db-push-drafts-to-git=false              # Push Draft/Proposed revisions to Git during repository sync
```

When using DB Cache with optional draft push mode, set `--db-push-drafts-to-git=true` on the Porch server and the matching `--repositories.push-drafts-to-git=true` on the repository controller. Both must be enabled together. See [Database Cache — Configurable Git Push Behavior]({{% relref "/docs/5_architecture_and_components/package-cache/db-cache.md#configurable-git-push-behavior" %}}) for behavior details.

**Example (DB cache with draft push mode):**

```yaml
spec:
  template:
    spec:
      containers:
      - name: porch-server
        args:
        - --cache-type=DB
        - --db-push-drafts-to-git=true
        env:
        # Database connection — see Environment Variables below and Cache Configuration
        - name: DB_DRIVER
          value: "pgx"
```

#### Function Runtime Arguments
```bash
args:
- --default-image-prefix=ghcr.io/kptdev/krm-functions-catalog  # Default function image prefix
```

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