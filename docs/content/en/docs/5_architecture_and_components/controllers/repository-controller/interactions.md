---
title: "Component Interactions"
type: docs
weight: 4
description: |
  How the Repository Controller interacts with other Porch components.
---

## Overview

The Repository Controller operates within the Porch ecosystem by watching Repository custom resources and interacting with the cache layer to keep package metadata synchronized. This page describes how the controller integrates with other Porch components.

### Architecture

```
┌────────────────────────────────────────────────────────┐
│              Repository Controller                     │
│                                                        │
│     ┌─────────────────────────────────────────┐        │
│     │  Reconcile Loop                         │        │
│     │                                         │        │
│     │  • Watch Repository CRs                 │        │
│     │  • Sync Decision Logic                  │        │
│     │  • Async Workers                        │        │
│     │  • Status Updates                       │        │
│     └─────────────────────────────────────────┘        │
│                          │                             │
│                          ↓                             │
│                 ┌──────────────────┐                   │
│                 │  Cache Interface │                   │
│                 │                  │                   │
│                 │  • OpenRepository│                   │
│                 │  • Refresh       │                   │
│                 │  • List Packages │                   │
│                 └──────────────────┘                   │
└────────────────────────────────────────────────────────┘
                           ↓
                   Kubernetes API
                           ↓
                   Repository CRs
                           ↓
                   Cache Layer
                   (CR/DB Cache)
```

## Kubernetes API Interactions

The controller watches Repository custom resources using standard Kubernetes controller patterns. When repositories are created, updated, or deleted, the controller receives events and reconciles the desired state with actual state.

When a Repository CR is deleted, the controller calls Close on the cache layer, which removes the repository from the cache map and closes the underlying git repository connection. Additionally, DB Cache deletes cached package data from the database, while CR Cache deletes PackageRev CRs for all package revisions.

All reconciliation is idempotent and level-triggered, meaning the controller can safely process the same event multiple times without causing issues. This design handles scenarios like controller restarts or missed events gracefully.

## Cache Layer Integration

The controller interacts with Porch's cache layer through a well-defined interface that abstracts the underlying storage mechanism. This abstraction allows the same controller code to work with both CR Cache and DB Cache configurations.

The cache interface handles the details of git operations and storage, allowing the controller to focus on synchronization logic. Whether using Custom Resources or a PostgreSQL database, the controller uses the same interface methods to validate connectivity, fetch content, and discover packages.

## v1alpha2 CRD Creation

In addition to populating the cache, the Repository Controller can create `PackageRevision` custom resources (CRDs) for each discovered package. This capability is controlled by:

1. The `CreateV1Alpha2Rpkg` feature flag (when enabled in configuration)
2. The `porch.kpt.dev/v1alpha2-migration: "true"` annotation on the Repository CR

When both conditions are met, the controller creates v1alpha2 PackageRevision CRDs in etcd. These CRDs are then reconciled by the PackageRevision Controller, which manages their full lifecycle.

Repositories without this annotation continue to use the v1alpha1 aggregated API. This allows gradual migration and coexistence of both models in the same cluster.

## Status Management

The controller maintains repository status using Server-Side Apply, which prevents conflicts when multiple components update the same resource. The controller acts as the `repository-controller` field manager and owns specific status fields like sync timestamps, package counts, and git commit hashes.

Status updates happen at key points: when syncs start, when they complete (successfully or with errors), and when health checks finish. The controller uses Kubernetes conditions to represent the current state and includes detailed information in status fields to support monitoring and troubleshooting.

### Status Update Flow

The controller updates repository status through the following steps:

1. **Sync operation completes** (health check or full sync)
2. **Determine status**: Success → `RepositoryStatusReady`, Failure → `RepositoryStatusError`
3. **Apply status update** using Server-Side Apply with field manager `repository-controller`
4. **Update status fields**:
   - `conditions` (RepositoryReady condition)
   - `lastFullSyncTime` (for full syncs only)
   - `nextFullSyncTime`
   - `packageCount` (for full syncs only)
   - `gitCommitHash` (for full syncs only)
   - `observedGeneration` / `observedRunOnceAt` (as needed)
5. **Persist to Kubernetes API**
