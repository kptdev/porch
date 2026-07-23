---
title: "Repository Controller"
type: docs
weight: 1
description: |
  Kubernetes controller for repository synchronization and lifecycle management.
---

## Overview

The Repository Controller manages the synchronization of [Porch repositories]({{% relref "/docs/2_concepts/repositories.md" %}}) with their corresponding external Git repositories. It continuously monitors repositories and keeps their [package]({{% relref "/docs/2_concepts/package.md" %}}) content up-to-date through periodic synchronization.

The controller handles several key responsibilities:

- Runs synchronization on configurable schedules (frequency, cron, or one-time)
  - Lightweight health checks to detect connectivity issues quickly
  - Full syncs to fetch content and discover packages
- Maintains repository status with sync timestamps, package counts, and git commit hashes
- Implements smart retry logic with error-type-specific intervals
- Controls concurrency to prevent resource exhaustion

## How It Works

The controller operates as a standard [Kubernetes controller](https://kubernetes.io/docs/concepts/architecture/controller/), watching Repository custom resources and reconciling their desired state with actual state:

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│ Repository      │    │ Controller       │    │ Cache Layer     │
│ CRD             │───>│ Reconcile Loop   │───>│ (CR/DB Cache)   │
│                 │    │                  │    │                 │
│ • Git config    │    │ • Sync decision  │    │ • Package data  │
│ • Sync schedule │    │ • Async workers  │    │ • Git cache     │
│ • Credentials   │    │ • Status update  │    │ • Database      │
│ • Annotations   │    │ • CRD creation   │    │                 │
└─────────────────┘    └──────────────────┘    └─────────────────┘
```

The controller uses a dual sync strategy to balance responsiveness with efficiency. Health checks run frequently to detect problems quickly, while full syncs run less often to fetch content and discover packages. This approach minimizes unnecessary git operations while maintaining up-to-date repository state.

When repositories encounter errors, the controller automatically retries with intervals tailored to the error type. The controller also detects stale syncs and automatically recovers.

### Repository Annotation for CRD-Based Management

The Repository Controller supports two discovery modes controlled by the `porch.kpt.dev/v1alpha2-migration` annotation:

- **Without annotation** (default): The repository controller syncs the repository and populates the cache. The aggregated API (v1alpha1) serves packages from this repository.
- **With annotation** (`porch.kpt.dev/v1alpha2-migration: "true"`): In addition to syncing, the controller creates `PackageRevision` CRDs (v1alpha2) for each discovered package in the repository. The PackageRevision Controller then manages these CRDs asynchronously.

For details on using the CRD-based architecture, see the [Working with CRD-Based PackageRevisions tutorial]({{% relref "/docs/4_tutorials_and_how-tos/working_with_crd_based_packagerevisions" %}}).

## Key Features

**Intelligent Sync Scheduling**: The controller prioritizes operations based on urgency, ensuring one-time syncs and spec changes execute immediately while routine operations happen on schedule.

**Flexible Configuration**: Repositories can use frequency-based scheduling, cron expressions, or one-time syncs to control when synchronization happens.

**Production-Grade Reliability**: Automatic retry with smart backoff, stale sync detection, and concurrent operation limiting ensure reliable operation at scale.

**Rich Status Information**: The controller maintains detailed status to support monitoring and troubleshooting.

## Configuration

For cache configuration, see [Cache Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/cache.md" %}}).

For controller-specific settings, see [Repository Controller Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-controllers-config.md#repository-controller-configuration" %}}).
