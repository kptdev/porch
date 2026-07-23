---
title: "Porch Controllers"
type: docs
weight: 2
description: "Configure the Porch controllers component"
---

The Porch controllers manage Repository synchronization, PackageVariants, and PackageVariantSets.

## Enabling Controllers

### Command Line Arguments

The controllers support these command line arguments:

```bash
args:
- --reconcilers=repositories,packagevariants,packagevariantsets  # Comma-separated list
# OR use --reconcilers=* to enable all controllers
```

### Repository Controller Configuration

The Repository Controller supports these additional flags:

```bash
args:
- --reconcilers=repositories
- --repositories.max-concurrent-reconciles=100
- --repositories.max-concurrent-syncs=50
- --repositories.health-check-frequency=5m
- --repositories.full-sync-frequency=1h
- --repositories.cache-type=CR  # or DB
```

**Configuration Parameters:**

| Parameter | Default | Description |
|-----------|---------|-------------|
| `max-concurrent-reconciles` | 100 | Parallel reconcile loops |
| `max-concurrent-syncs` | 50 | Parallel sync operations |
| `health-check-frequency` | 5m | Lightweight connectivity checks |
| `full-sync-frequency` | 1h | Complete repository sync |
| `cache-type` | CR | Cache implementation (CR or DB) - see [Cache Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/cache.md" %}}) |

**Cache Type:**

The `cache-type` parameter determines how package data is stored:
- **CR**: Custom Resources for metadata, in-memory caching (simpler, no database required)
- **DB**: PostgreSQL database for metadata and content (production-grade, scalable)

{{% alert title="Note" color="info" %}}
When using `--repositories.cache-type=DB`, you must also configure database connection settings via environment variables. See [Cache Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/cache.md" %}}) for complete setup instructions.
{{% /alert %}}

**Tuning Guidance:**

Adjust these parameters based on your deployment characteristics:

- **Concurrency settings** (`max-concurrent-reconciles`, `max-concurrent-syncs`):
  - Higher values increase throughput but consume more resources
  - Start with defaults and adjust based on observed CPU/memory usage
  - Monitor controller logs for reconciliation delays

- **Frequency settings** (`health-check-frequency`, `full-sync-frequency`):
  - More frequent checks detect issues faster but increase load
  - Less frequent checks reduce overhead but delay change detection
  - Balance based on your tolerance for sync lag vs resource usage

For detailed sync behavior and scheduling, see [Repository Sync Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/repository-sync.md" %}}).

### PackageRevision Controller Configuration

{{% alert title="Note" color="primary" %}}
The PackageRevision (PR) Controller is an opt-in feature that enables CRD-based PackageRevision management. See [Working with CRD-Based PackageRevisions]({{% relref "/docs/4_tutorials_and_how-tos/working_with_crd_based_packagerevisions" %}}) for a step-by-step guide.
{{% /alert %}}

The PR Controller is enabled by adding `packagerevisions` to the `--reconcilers` flag:

```bash
args:
- --reconcilers=repositories,packagerevisions
- --packagerevisions.max-concurrent-reconciles=50
- --packagerevisions.max-concurrent-renders=20
- --packagerevisions.render-requeue-delay=2s
- --packagerevisions.repo-operation-retry-attempts=3
- --packagerevisions.max-grpc-message-size=6291456
```

**Configuration Parameters:**

| Parameter | Default | Description |
|-----------|---------|-------------|
| `max-concurrent-reconciles` | 50 | Maximum parallel reconciles |
| `max-concurrent-renders` | 20 | Maximum parallel render operations |
| `render-requeue-delay` | 2s | Delay before requeue when render concurrency limit is reached |
| `repo-operation-retry-attempts` | 3 | Retry count for git operations on transient failures |
| `max-grpc-message-size` | 6MB | Maximum gRPC message size for function runner communication |

**Environment Variables:**

| Variable | Required | Description |
|----------|----------|-------------|
| `FUNCTION_RUNNER_ADDRESS` | For external functions | gRPC address of the function runner service. If unset, only builtin Go functions are available. |

**Prerequisites:**

The PR Controller requires:

- The Repository Controller to be running (provides the shared cache)
- The `PackageRevision` CRD (`porch.kpt.dev/v1alpha2`) to be installed in the cluster
- The function runner service to be reachable (if external KRM functions are used)

**Tuning Guidance:**

- **Render concurrency**: Controls how many packages can be rendered simultaneously. Rendering involves gRPC calls to the function runner, which is CPU and memory intensive. If the function runner is under-provisioned, reduce this limit. If you see frequent `RequeueAfter` from render throttling, increase it.

- **Reconcile concurrency**: Controls total parallel work. Source execution and lifecycle transitions are lightweight, so the bottleneck is usually rendering. A ratio of 2-3x reconciles to renders (e.g. 50 reconciles, 20 renders) works well for most clusters.

- **gRPC message size**: Increase this if packages contain large resource files (over 6MB total). This is uncommon for typical KRM packages.