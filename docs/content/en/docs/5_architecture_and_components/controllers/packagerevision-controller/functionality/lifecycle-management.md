---
title: "Lifecycle Management"
type: docs
weight: 3
description: |
  Lifecycle transitions, deletion gating, and revision numbering.
---

## Overview

The lifecycle phase compares the desired lifecycle in `spec.lifecycle` with the actual state in the cache. If they differ, the controller transitions the package accordingly. The `spec.lifecycle` field is client-owned: the controller reads it but never writes it.

## Lifecycle States

| State | Meaning |
|-------|---------|
| Draft | Package is being edited. Stored as a Git branch. |
| Proposed | Package is proposed for review (optional intermediate state). |
| Published | Package is immutable. Stored as a Git tag. |
| DeletionProposed | Package is marked for deletion. Required before deleting a published package. |

## State Transitions

```
    ┌─────────┐
    │  Draft  │◄──────────┐
    └────┬────┘           │
         │ propose        │ reject
         ▼                │
    ┌─────────────┐       │
    │  Proposed   ├───────┘
    └────┬────────┘
         │ approve
         ▼
    ┌──────────────┐
    │  Published   │
    └────┬─────────┘
         │ propose-delete
         ▼
    ┌──────────────────┐
    │ DeletionProposed │
    └────┬─────┬───────┘
         │     │
    approve   reject
    deletion   │
         │     ▼
         │  Published
         ▼
      [Deleted]
```

**Valid transitions:**
- Draft → Proposed (propose)
- Proposed → Published (approve)
- Proposed → Draft (reject)
- Published → DeletionProposed (propose-delete)
- DeletionProposed → Deleted (approve deletion)
- DeletionProposed → Published (reject deletion)

**Invalid transitions:**
- Draft → Published (must go through Proposed)
- Published → Draft (must create new Draft revision)

## Publish

On publish, the controller:

1. Calls `UpdateLifecycle` on the shared cache (moves from branch to tag in Git)
2. Assigns a revision number (incremented from the highest existing revision for that package)
3. Updates the `porch.kpt.dev/latest-revision` label across all revisions of the same package
4. Records `publishedBy` and `publishedAt` in status

## Latest-Revision Labels

The controller maintains a `porch.kpt.dev/latest-revision` label on all PackageRevisions. The published revision with the highest revision number gets `"true"`; all others get `"false"`. This enables efficient queries like "give me the latest published version of package X" without listing and sorting all revisions.

Labels are updated on two events: when a package is published (the new revision becomes latest), and when a published package is deleted (the previous revision becomes latest again).

## Deletion Gating

Published packages cannot be deleted directly. This safety mechanism prevents accidental destruction of immutable packages. The controller enforces it through a finalizer.

When a user deletes the CRD, Kubernetes sets `deletionTimestamp` but the finalizer prevents actual removal. The controller checks:

- **Published + Repository exists**: The controller does nothing. The object stays in Terminating state until the user transitions it to DeletionProposed.
- **DeletionProposed (or any non-Published state)**: The controller cleans up Git refs and removes the finalizer, allowing Kubernetes to complete the deletion.
- **Repository deleted** (via Kubernetes [garbage collection](https://kubernetes.io/docs/concepts/architecture/garbage-collection/) cascade): The controller allows deletion regardless of lifecycle. There is no point protecting packages whose repository is gone.

## OwnerReference

Each PackageRevision gets an ownerReference pointing to its Repository CRD. This serves two purposes:

1. Enables Kubernetes garbage collection (deleting a Repository cascades to all its packages)
2. Allows the controller to detect GC cascade during deletion gating

The ownerReference is set on first reconcile if not already present, in the same patch that adds the finalizer.
