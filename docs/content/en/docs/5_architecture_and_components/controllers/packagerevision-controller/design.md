---
title: "Design"
type: docs
weight: 2
description: |
  Internal design and architecture of the PackageRevision Controller.
---

## Controller Structure

The PR Controller is a standard [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) reconciler. Its internal structure mirrors the reconciliation pipeline, with each concern handled by a dedicated sub-reconciler that returns early if its work is not needed:

```
PackageRevisionReconciler
├── reconcileFinalizer()    — Finalizer + ownerReference management, deletion gating
├── reconcileSource()       — One-time package creation (init/clone/copy/upgrade)
├── reconcileRender()       — KRM function pipeline execution
└── reconcileLifecycle()    — Git lifecycle transitions, revision numbering
```

For detailed behaviour of each phase, see the [Functionality]({{% relref "functionality" %}}) section.

## CRD as Intent, Cache as Content

The fundamental design decision is the separation of intent from content. The `PackageRevision` CRD in etcd is the source of truth for **what the user wants**: which lifecycle state the package should be in, how it was created, whether rendering is requested. The shared cache (backed by Git) is the source of truth for **what the package contains**: the actual KRM resource files.

The controller bridges these two stores. When you set `spec.lifecycle: Published` on the CRD, the controller transitions the package in the cache to published state and updates `status` to reflect the result. This is standard Kubernetes controller semantics: spec is desired state, status is observed state.

## Shared Cache

The controller does not open Git repositories directly. All Git interaction goes through the `ContentCache` interface, which is backed by the Repository Controller's shared cache. This design centralizes repository connection management, credential handling, and cache invalidation in a single component.

The cache provides six operations that cover the controller's needs:

- **GetPackageContent**: read package state and files from the cache
- **CreateNewDraft**: open a new draft for writing initial content
- **CreateDraftFromExisting**: open an existing package for modification (used by render)
- **CloseDraft**: commit a draft to Git
- **UpdateLifecycle**: transition a package's lifecycle state
- **DeletePackage**: remove git refs (branches/tags) for a package

The controller never needs to know whether the underlying cache is CR-based or DB-based. It works identically with either implementation.

## Content in the PRRStore

For v1alpha2 repositories, package content is kept in the PRRStore, a content store in the same PostgreSQL database as the DB cache. It is separate from the v1alpha1 DB cache data, which v1alpha2 repositories do not write. Git remains the store for published revisions.

With `push-drafts-to-git=false`, a Draft or Proposed revision is never pushed, so Git discovery cannot see it. The store marks such a revision as store-only, and the repository lists marked revisions alongside those from Git. The marker is not a copy of `spec.lifecycle`, which stays with the CRD. It is set when an unpushed draft is closed and cleared when the revision is published. Renders and PRR pushes leave it alone.

Deleting a Repository deletes all of its PRRStore content, including markers.

## Server-Side Apply for Status

All status updates use Server-Side Apply (SSA) with distinct field managers to avoid ownership conflicts. This is important because multiple actors write to the same PackageRevision: the Repository Controller sets initial values during discovery, and the PR Controller takes over during reconciliation.

Three field managers partition the status fields:

**packagerev-controller** owns the core status: Ready condition, observedGeneration, revision number, publishedBy/At timestamps, upstream and self locks, and creationSource.

**packagerev-controller-render** owns the render tracking fields: Rendered condition, renderingPrrResourceVersion, and observedPrrResourceVersion. Separating these prevents a lifecycle status update from accidentally clearing render state.

**packagerev-controller-kptfile** owns fields synced from the Kptfile after rendering: readinessGates, packageMetadata, and packageConditions. These are written to the CRD spec and status so that external controllers can read Kptfile-derived data without parsing package content.
