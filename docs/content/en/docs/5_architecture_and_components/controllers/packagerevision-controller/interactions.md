---
title: "Interactions"
type: docs
weight: 3
description: |
  How the PackageRevision Controller interacts with other Porch components.
---

## Component Interaction Overview

```
                    ┌─────────────────────────────────────────────────────────┐
                    │                    User / GitOps                        │
                    └───────────┬─────────────────────────────┬───────────────┘
                                │ kubectl apply               │ kubectl get PRR
                                ▼                             ▼
┌───────────────────────────────────────────┐   ┌─────────────────────────────┐
│         PackageRevision CRD (etcd)        │   │   Porch API Server          │
│                                           │   │                             │
│  • spec.source (init/clone/copy/upgrade)  │   │  • PackageRevisionResources │
│  • spec.lifecycle (Draft/Published/...)   │   │  • Engine (content access)  │
│  • annotations (render-request)           │   │                             │
└───────────────────┬───────────────────────┘   └──────────────┬──────────────┘
                    │ watch                                    │ read/write
                    ▼                                          ▼
┌───────────────────────────────────────────────────────────────────────────────┐
│                          Shared Cache (ContentCache)                          │
│                                                                               │
│  GetPackageContent · CreateNewDraft · CreateDraftFromExisting                 │
│  CloseDraft · UpdateLifecycle · DeletePackage                                 │
│                                                                               │
└───────────────────────────────────────────────────────────────────────────────┘
        ▲ populated by                              │ git operations
        │                                           ▼
┌───────────────────────────┐           ┌───────────────────────────┐
│   Repository Controller   │           │   Git Repository          │
└───────────────────────────┘           └───────────────────────────┘
```

## Repository Controller

The PR Controller and Repository Controller never communicate directly. Their coupling is entirely through the shared cache: the Repository Controller creates it, populates it by syncing Git repositories on schedule, and the PR Controller reads from and writes to it.

Startup ordering is enforced in `controllers/main.go`: the repo reconciler initializes first and its cache reference is injected into the PR reconciler before setup. If the Repository Controller is not enabled, the PR Controller cannot start (it requires a non-nil cache).

At runtime, the Repository Controller's sync loop keeps the cache fresh with the latest Git state. The PR Controller's writes (creating drafts, closing them, transitioning lifecycle) go through the same cache and are immediately visible to subsequent reads. There is no separate notification channel between the two controllers; the cache is the shared state.

If the Repository Controller stops running, the PR Controller continues to operate on whatever the cache already holds. No new repositories will be synced and external Git changes will not be picked up, but in-flight operations complete normally.

## Porch API Server and Engine

In the CRD-based architecture, the API Server and Engine serve a narrower role than in the aggregated API model. They handle `PackageRevisionResources` (PRR), the aggregated API that provides read/write access to package file contents. Lifecycle management, source execution, and rendering are handled by the PR Controller instead.

The interaction between the API Server and the PR Controller is event-driven through an annotation. When a user pushes content via PRR:

1. The API Server writes the new content to the shared cache through the Engine.
2. The API Server patches the `porch.kpt.dev/render-request` annotation on the PackageRevision CRD with the PRR's resourceVersion.
3. The PR Controller's predicate filter detects the annotation change and triggers a reconcile.
4. The controller reads the updated content from the cache, renders it, and writes the results back.

This handoff means the API Server does not need to know how rendering works; it just signals that new content is available. The PR Controller does not need to know how content was written; it just reads whatever is in the cache.

## Function Runner

The PR Controller calls the function runner during the render phase. The function runner is a standalone gRPC service that executes KRM functions, both builtin Go functions compiled into the binary and external functions running in containers.

The controller creates a `kptRenderer` during initialization. This is an internal component that wraps kpt's render library and is configured with the function runner's gRPC address and runner options (image prefix, allowed/disallowed registries, etc.). During render, the controller writes package resources to an in-memory filesystem, invokes the renderer (which calls functions through the gRPC runtime), and reads the results back.

Concurrency is bounded by the `max-concurrent-renders` setting. If the function runner is unavailable, renders fail and the Rendered condition is set to False with the error message. The controller does not retry failed renders automatically; it waits for the next trigger (annotation change or manual requeue).

If `FUNCTION_RUNNER_ADDRESS` is not set, only builtin Go functions are available. External container-based functions will fail.

## PackageVariant and PackageVariantSet Controllers

These controllers create PackageRevision CRDs as part of their automation workflows. When a PackageVariantSet detects a new upstream revision, it creates downstream PackageRevision CRDs with `spec.source.cloneFrom` set. The PR Controller then reconciles these exactly like user-created packages: executing the clone, rendering, and managing lifecycle.

There is no special coordination between the PV controllers and the PR Controller. The CRD in etcd is the interface contract. The PV controllers write the desired state; the PR Controller makes it real.

## Kubernetes Integration

**Garbage Collection (GC)**: Each PackageRevision has an ownerReference to its Repository CRD. When a Repository is deleted, Kubernetes [garbage collection](https://kubernetes.io/docs/concepts/architecture/garbage-collection/) cascades deletion to all owned PackageRevisions. The PR Controller detects this scenario (owner repo gone) and allows deletion of Published packages that would normally be blocked by the finalizer.

**Field Selectors**: The controller registers field indexes on the PackageRevision CRD for efficient server-side filtering. This enables queries like "list all published packages in repository X" without client-side filtering. The indexes cover `spec.repository`, `spec.packageName`, `spec.workspaceName`, `spec.lifecycle`, and `status.revision`.

**Predicates**: The controller uses two event filters to avoid unnecessary reconciles. `GenerationChangedPredicate` skips reconciles when only metadata (labels, annotations other than render-request) changes. A custom `renderRequestChanged` predicate fires specifically when the render-request annotation changes, ensuring content pushes trigger rendering even though they do not bump generation.

## Data Flow: Creating a Package

A user creates a PackageRevision CRD with `spec.source.init` and `spec.lifecycle: Draft`. The CRD lands in etcd and the controller's informer picks it up.

The controller runs `reconcileSource`, which calls `initPackage` to generate a Kptfile in memory. It then opens a draft in the cache, writes the resources, and closes the draft (committing to Git). Status is updated with `creationSource: init` and the reconcile requeues.

On the next reconcile, source is skipped (already done). The controller runs `reconcileRender`, reads the resources from the cache, invokes kpt render, and writes the rendered output back. The Rendered condition is set to True.

Finally, `reconcileLifecycle` checks that `spec.lifecycle` matches the cache state. Both are Draft, so Ready is set to True and the reconcile completes.

When the user later patches `spec.lifecycle` to Published, the controller calls `UpdateLifecycle` on the cache, which transitions the package (typically moving from a branch to a tag). A revision number is assigned, latest-revision labels are updated, and Ready remains True.

## Data Flow: Pushing Content and Rendering

A user edits package content through `PackageRevisionResources`. The API Server writes the new content to the cache via the Engine, then patches the render-request annotation on the PackageRevision CRD.

The annotation change triggers a reconcile. Source is skipped (already done). The controller enters `reconcileRender`, sees that the annotation value differs from `status.observedPrrResourceVersion`, and proceeds to render.

It reads the updated resources from the cache, acquires the render semaphore, and calls kpt render. After rendering completes, it re-reads the CRD from etcd to check for staleness. If the annotation has not changed during rendering, it writes the rendered resources back to the cache and updates the Rendered condition to True. The observedPrrResourceVersion is set to the annotation value, preventing the same content from being rendered again.
