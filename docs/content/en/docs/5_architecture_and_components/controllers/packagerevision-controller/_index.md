---
title: "PackageRevision Controller"
type: docs
weight: 2
draft: true
description: |
  Kubernetes controller for package revision lifecycle management (v1alpha2).
---

## Overview

The PackageRevision Controller manages the full lifecycle of package revisions as native Kubernetes CRDs. In the v1alpha1 architecture, the Porch API Server and Engine handle all operations synchronously within the request path. The PR controller takes a different approach — it watches `PackageRevision` CRDs in etcd and reconciles their desired state against Git asynchronously, following standard Kubernetes controller patterns.

This means users interact with package revisions the same way they interact with any other Kubernetes resource: create a CRD with the desired state, and the controller makes it so.

## How It Works

```
┌─────────────────────┐     ┌──────────────────────────────┐     ┌─────────────────┐
│ PackageRevision CRD │     │ PR Controller                │     │ Shared Cache    │
│ (etcd)              │────>│                              │────>│ (from Repo Ctr) │
│                     │     │ • Source execution           │     │                 │
│ • spec.source       │     │ • Render pipeline            │     │ • Git read/write│
│ • spec.lifecycle    │     │ • Lifecycle transitions      │     │ • Draft mgmt    │
│ • annotations       │     │ • Status updates (SSA)       │     │ • Content cache │
└─────────────────────┘     └──────────────────────────────┘     └─────────────────┘
                                        │
                                        ▼
                            ┌──────────────────────┐
                            │ Function Runner      │
                            │ (gRPC)               │
                            └──────────────────────┘
```

The controller does not manage repository connections or synchronization. That responsibility stays with the Repository Controller, which populates the shared cache. The PR controller reads from and writes to that cache — it never opens a Git connection directly.

## Reconciliation Pipeline

Each reconcile executes three phases in sequence. If any phase produces an error or requires a requeue, subsequent phases are skipped.

**Source execution** handles one-time package creation. When a user creates a PackageRevision with `spec.source` set (init, clone, copy, or upgrade), the controller executes that source operation to produce the initial package content in Git. Once `status.creationSource` is populated, this phase becomes a no-op on future reconciles.

**Rendering** runs the KRM function pipeline defined in the package's Kptfile. Two events trigger rendering: a content push via the PRR handler (signalled by the `porch.kpt.dev/render-request` annotation), or the completion of source execution. The controller reads resources from the cache, invokes kpt render through the function runner, and writes the results back.

**Lifecycle transition** compares the desired lifecycle in `spec.lifecycle` with the actual lifecycle in Git. If they differ, the controller transitions the package in Git. On publish, it assigns a revision number and updates the `latest-revision` label across all revisions of the same package.

## Relationship to Other Components

The PR controller sits alongside the Repository Controller in the controllers deployment. It depends on the shared cache that the Repository Controller creates and populates — this is enforced at startup by initializing the repo reconciler first and injecting its cache into the PR reconciler.

The Porch API Server and Engine continue to serve `PackageRevisionResources` for content access. When a user pushes content through PRR, the API Server writes to Git via the Engine and then patches the render-request annotation on the PackageRevision CRD. This annotation change triggers the PR controller to pick up the new content and render it.

PackageVariant and PackageVariantSet controllers create PackageRevision CRDs as part of their automation. The PR controller reconciles these like any other PackageRevision — it doesn't know or care who created the CRD.

## Enabling the Controller

The PR controller is enabled via the `--reconcilers` flag on the controllers deployment:

```
--reconcilers=packagerevisions
```

It requires the Repository Controller to be running (for the shared cache), the `PackageRevision` CRD to be installed, and the `FUNCTION_RUNNER_ADDRESS` environment variable to be set if external function evaluation is needed.

## Configuration

The controller exposes flags for tuning concurrency and retry behavior:

| Flag | Default | Description |
|------|---------|-------------|
| `packagerevisions.max-concurrent-reconciles` | 50 | Maximum parallel reconciles |
| `packagerevisions.max-concurrent-renders` | 20 | Maximum parallel render operations |
| `packagerevisions.render-requeue-delay` | 2s | Delay before requeue when render limit reached |
| `packagerevisions.repo-operation-retry-attempts` | 3 | Retry count for git operations |
| `packagerevisions.max-grpc-message-size` | 6MB | Max gRPC message size for fn-runner |
