---
title: "Deployment Modes"
type: docs
weight: 6
draft: true
description: |
  Comparison of v1alpha1 (aggregated API) and v1alpha2 (CRD + controller) architectures.
---

## Overview

Porch supports two deployment modes for managing PackageRevisions. Both modes share the same underlying concepts — packages, lifecycle states, workspaces, upstream/downstream relationships — but differ in how the Kubernetes API is structured and where orchestration logic lives.

## v1alpha1: Aggregated API Mode

The original architecture. `PackageRevision` is served by the Porch API Server as an aggregated API resource with custom REST storage. When a client creates or updates a PackageRevision, the request flows synchronously through the API Server into the Engine, which orchestrates the operation against Git through the cache.

```
┌──────────────┐     ┌──────────────────┐     ┌─────────┐     ┌─────┐
│ kubectl      │────>│ Porch API Server │────>│ Engine  │────>│ Git │
│              │<────│ (aggregated API) │<────│         │<────│     │
└──────────────┘     └──────────────────┘     └─────────┘     └─────┘
```

The API Server is the single point of orchestration. It handles validation, lifecycle transitions, task execution, rendering, and watch streams — all within the request path. The Engine is the brain; the API Server is the interface.

PackageRevisions are not stored in etcd. The custom REST storage translates Kubernetes API semantics into Engine operations, and the Engine reads/writes Git through the cache. This means watches are custom (implemented via WatcherManager) rather than native Kubernetes watches.

## v1alpha2: CRD + Controller Mode

The newer architecture. `PackageRevision` is a standard Kubernetes CRD stored in etcd. A dedicated controller watches these CRDs and reconciles their desired state against Git asynchronously.

```
┌──────────────┐     ┌──────────────────┐
│ kubectl      │────>│ etcd (CRD)       │
│              │<────│ PackageRevision   │
└──────────────┘     └────────┬─────────┘
                              │ watch
                              ▼
                     ┌──────────────────┐     ┌───────────────┐     ┌─────┐
                     │ PR Controller    │────>│ Shared Cache  │────>│ Git │
                     └──────────────────┘     └───────────────┘     └─────┘
```

The CRD is the interface; the controller is the brain. Users express intent by writing to the CRD (set lifecycle, specify source), and the controller makes it happen in Git. Operations are eventually consistent — the controller reconciles on its own schedule rather than within the API request path.

Content access (`PackageRevisionResources`) still flows through the API Server and Engine, unchanged:

```
┌──────────────┐     ┌──────────────────┐     ┌─────────┐     ┌─────┐
│ kubectl      │────>│ Porch API Server │────>│ Engine  │────>│ Git │
└──────────────┘     └──────────────────┘     └─────────┘     └─────┘
```

The Engine's role narrows from full orchestration to content access only.

## Key Differences

**Storage model.** In v1alpha1, PackageRevision exists only in Git — the API Server synthesizes it on the fly. In v1alpha2, PackageRevision lives in etcd as a real CRD, with Git as the backing store for content. This gives you native Kubernetes features for free: field selectors, server-side filtering, standard watches, SSA, and standard RBAC without custom authorization logic.

**Execution model.** v1alpha1 is synchronous — a create request blocks until the package is written to Git and rendered. v1alpha2 is asynchronous — the CRD is created immediately in etcd, and the controller reconciles it in the background. Status conditions (Ready, Rendered) tell you when the work is done.

**Observability.** In v1alpha1, debugging requires reading API Server logs to understand what happened. In v1alpha2, the CRD's status conditions, events, and standard `kubectl describe` output show the current state and any errors. The controller's reconcile loop is visible through standard controller-runtime metrics.

**Scalability.** The v1alpha1 API Server is a single process handling all operations. In v1alpha2, the controller scales independently — you can tune concurrency, and the async model naturally handles bursts by queuing work rather than blocking requests.

**Engine role.** In v1alpha1, the Engine handles everything: lifecycle, tasks, rendering, content access, validation. In v1alpha2, the Engine handles only content access for PackageRevisionResources. Lifecycle, source execution, and rendering move to the PR controller.

## What's Shared

Both modes use the same Repository Controller for Git synchronization, the same function runner for KRM function evaluation, and the same PackageVariant/Set controllers for automation. The lifecycle model (Draft → Proposed → Published → DeletionProposed), workspace semantics, revision numbering, and upstream/downstream relationships are identical.

The Git storage format is also shared — branches for drafts, tags for published packages. A package created in one mode is visible in Git the same way as a package created in the other.

## Coexistence

Both modes can run in the same cluster. The v1alpha1 aggregated API and v1alpha2 CRD operate on different API resources and don't conflict. However, they manage separate sets of packages — a package created via v1alpha1 is not automatically visible as a v1alpha2 CRD. Migration tooling exists to move packages between modes.
