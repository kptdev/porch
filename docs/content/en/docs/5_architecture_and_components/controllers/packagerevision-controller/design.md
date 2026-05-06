---
title: "Design"
type: docs
weight: 2
draft: true
description: |
  Internal design and architecture of the PackageRevision Controller.
---

## Controller Structure

The PR controller is a standard controller-runtime reconciler. Its internal structure mirrors the reconciliation pipeline — each concern is handled by a dedicated sub-reconciler that returns early if its work is not needed:

```
PackageRevisionReconciler
├── reconcileFinalizer()    — Finalizer + ownerReference management, deletion gating
├── reconcileSource()       — One-time package creation (init/clone/copy/upgrade)
├── reconcileRender()       — KRM function pipeline execution
└── reconcileLifecycle()    — Git lifecycle transitions, revision numbering
```

## CRD as Intent, Git as Content

The fundamental design decision is the separation of intent from content. The `PackageRevision` CRD in etcd is the source of truth for **what the user wants** — which lifecycle state the package should be in, how it was created, whether rendering is requested. Git is the source of truth for **what the package contains** — the actual KRM resource files.

The controller bridges these two stores. A user sets `spec.lifecycle: Published` on the CRD; the controller transitions the package in Git to published state and updates `status` to reflect the result. This is standard Kubernetes controller semantics — spec is desired state, status is observed state.

## Shared Cache

The controller does not open Git repositories directly. All Git interaction goes through the `ContentCache` interface, which is backed by the Repository Controller's shared cache. This design centralizes repository connection management, credential handling, and cache invalidation in a single component.

The cache provides six operations that cover the controller's needs:

- **GetPackageContent** — read package state and files from the cache
- **CreateNewDraft** — open a new draft for writing initial content
- **CreateDraftFromExisting** — open an existing package for modification (used by render)
- **CloseDraft** — commit a draft to Git
- **UpdateLifecycle** — transition a package's lifecycle state in Git
- **DeletePackage** — remove git refs (branches/tags) for a package

The controller never needs to know whether the underlying cache is CR-based or DB-based. It works identically with either implementation.

## Server-Side Apply for Status

All status updates use Server-Side Apply with distinct field managers to avoid ownership conflicts. This is important because multiple actors write to the same PackageRevision — the Repository Controller sets initial values during discovery, and the PR controller takes over during reconciliation.

Three field managers partition the status fields:

**packagerev-controller** owns the core status: Ready condition, observedGeneration, revision number, publishedBy/At timestamps, upstream and self locks, and creationSource.

**packagerev-controller-render** owns the render tracking fields: Rendered condition, renderingPrrResourceVersion, and observedPrrResourceVersion. Separating these prevents a lifecycle status update from accidentally clearing render state.

**packagerev-controller-kptfile** owns fields synced from the Kptfile after rendering: readinessGates, packageMetadata, and packageConditions. These are written to the CRD spec and status so that external controllers can read Kptfile-derived data without parsing package content.

## Concurrency-Limited Rendering

Rendering calls the function runner via gRPC, which is resource-intensive. Rather than allowing all 50 concurrent reconciles to render simultaneously, the controller uses a channel-based semaphore to bound concurrent renders to a configurable limit (default 20).

When the semaphore is full, the reconcile doesn't block — it returns a `RequeueAfter` result and tries again after a short delay. This keeps the controller responsive and prevents it from overwhelming the function runner or exhausting gRPC connections.

## Stale Render Detection

A race exists between rendering and content pushes. While the controller is rendering (which may take seconds), the user might push new content through PRR, changing the render-request annotation. If the controller wrote back the now-stale render results, the user's latest content would be overwritten.

To handle this, after rendering completes the controller re-reads the PackageRevision directly from etcd (bypassing the informer cache) and compares the current annotation value with the one that triggered the render. If they differ, the render results are discarded and the reconcile requeues to pick up the newer content.

## Deletion Gating

Published packages cannot be deleted directly. This is a safety mechanism — deleting a published package from Git is destructive and irreversible. The controller enforces this through a finalizer:

When a user deletes the CRD, Kubernetes sets `deletionTimestamp` but the finalizer prevents actual removal. The controller checks the package's lifecycle:

- If the package is Published and its owner Repository still exists, the controller does nothing. The object stays in Terminating state until the user first transitions it to DeletionProposed.
- If the package is DeletionProposed (or any non-Published state), the controller cleans up Git refs and removes the finalizer, allowing Kubernetes to complete the deletion.
- If the owner Repository has been deleted (Kubernetes GC cascade), the controller allows deletion regardless of lifecycle — there's no point protecting packages whose repository is gone.

## OwnerReference to Repository

Each PackageRevision gets an ownerReference pointing to its Repository CRD. This serves two purposes: it enables Kubernetes garbage collection (deleting a Repository cascades to all its packages), and it allows the controller to detect GC cascade during deletion gating.

The ownerReference is set on first reconcile if not already present, in the same patch that adds the finalizer.

## Source Execution

Source execution is idempotent — it only runs once per PackageRevision. The guard is `status.creationSource`: if it's already set, the source phase is skipped entirely.

**Init** creates a brand new package by generating a Kptfile with the specified metadata (name, description, keywords). No external dependencies.

**Clone** copies content from an upstream package. Two modes are supported: cloning from a registered PackageRevision (by name reference) or from a raw Git URL. In both cases, the Kptfile's upstream and upstreamLock fields are set to track the source.

**Copy** creates a new revision from an existing published revision of the same package in the same repository. This is the mechanism for "edit an existing package" — copy the latest published revision into a new draft workspace.

**Upgrade** performs a 3-way merge between the old upstream, new upstream, and current local package. It supports multiple merge strategies (resource-merge, fast-forward, force-delete-replace, copy-merge). After merging, the Kptfile upstream/upstreamLock are updated to point at the new upstream.

After any source execution, the controller creates a draft in the cache, writes the resources, closes the draft (committing to Git), and requeues to trigger rendering.

## Latest-Revision Labels

The controller maintains a `porch.kpt.dev/latest-revision` label on all PackageRevisions. The published revision with the highest revision number gets `"true"`; all others get `"false"`. This label enables efficient queries like "give me the latest published version of package X" without listing and sorting all revisions.

Labels are updated on two events: when a package is published (the new revision becomes latest), and when a published package is deleted (the previous revision becomes latest again).
