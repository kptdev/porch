---
title: "PackageRevision Controller"
type: docs
weight: 1
description: |
  Kubernetes controller for package revision lifecycle management (CRD-based architecture).
---

## Overview

The PackageRevision (PR) Controller manages the full lifecycle of package revisions as native Kubernetes CRDs. It is the core component of the CRD-based controller architecture, which replaces the synchronous aggregated API model with an asynchronous, Kubernetes-native controller model.

In the aggregated API architecture, the Porch API Server and Engine handle all operations synchronously within the request path. The PR Controller takes a different approach: it watches `PackageRevision` CRDs (API version `porch.kpt.dev/v1alpha2`) in etcd and reconciles their desired state against the shared cache asynchronously, following standard [Kubernetes controller patterns](https://kubernetes.io/docs/concepts/architecture/controller/).

Users interact with package revisions the same way they interact with any other Kubernetes resource: create a CRD with the desired state, and the controller makes it so.

## Functionality

The PR Controller is responsible for:

- **Package creation**: executing source operations (init, clone, copy, upgrade) to produce initial package content
- **Rendering**: running KRM function pipelines defined in the package's Kptfile after content changes
- **Lifecycle management**: transitioning packages between Draft, Proposed, Published, and DeletionProposed states
- **Revision numbering**: assigning and tracking revision numbers on publish, maintaining latest-revision labels
- **Deletion gating**: preventing accidental deletion of published packages via a finalizer
- **Status reporting**: surfacing Ready and Rendered conditions so users can observe progress via `kubectl`

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

The controller does not manage repository connections or synchronization. That responsibility stays with the Repository Controller, which populates the shared cache. The PR Controller reads from and writes to that cache; it never opens a Git connection directly.

## Reconciliation Pipeline

Each reconcile executes four phases in sequence. The reconcile itself is triggered asynchronously (decoupled from the API request), but within a single reconcile run the phases are ordered. If any phase produces an error or requires a requeue, subsequent phases are skipped.

**Finalizer and owner reference management** ensures proper deletion gating. Published packages are protected by a finalizer to prevent accidental deletion. Ownerships to the Repository CRD enable Kubernetes garbage collection to cascade deletion of packages when repositories are deleted.

**Source execution** handles one-time package creation. When a user creates a PackageRevision with `spec.source` set (init, clone, copy, or upgrade), the controller executes that source operation to produce the initial package content in the shared cache. Once `status.creationSource` is populated, this phase becomes a no-op on future reconciles.

**Rendering** runs the KRM function pipeline defined in the package's Kptfile. Two events can trigger rendering: a content push via the PRR handler (signalled by the `porch.kpt.dev/render-request` annotation), or the completion of source execution. The controller reads resources from the cache, invokes kpt render through the function runner, and writes the results back.

**Lifecycle transition** compares the desired lifecycle in `spec.lifecycle` with the actual lifecycle in the cache. If they differ, the controller transitions the package accordingly. On publish, it assigns a revision number and updates the `latest-revision` label across all revisions of the same package.

## Relationship to Other Components

The PR Controller sits alongside the Repository Controller in the controllers deployment. It depends on the shared cache that the Repository Controller creates and populates. This is enforced at startup by initializing the repo reconciler first and injecting its cache into the PR reconciler.

The Porch API Server and Engine continue to serve `PackageRevisionResources` (PRR) for content access. When a user pushes content through PRR, the API Server writes to the shared cache via the Engine and then patches the render-request annotation on the PackageRevision CRD. This annotation change triggers the PR Controller to pick up the new content and render it.

PackageVariant and PackageVariantSet controllers create PackageRevision CRDs as part of their automation. The PR Controller reconciles these like any other PackageRevision; it does not know or care who created the CRD.

## Enabling the Controller

The PR Controller is enabled via the `--reconcilers` flag on the controllers deployment:

```
--reconcilers=repositories,packagerevisions
```

Make sure the Repository Controller is also running (it populates the shared cache), the `PackageRevision` CRD is installed, and the `FUNCTION_RUNNER_ADDRESS` environment variable is set if external function evaluation is needed.

**Repository Annotation**: For the PR Controller to reconcile packages in a repository, the repository must be annotated with `porch.kpt.dev/v1alpha2-migration: "true"`. Without this annotation, the Repository Controller does not create v1alpha2 PackageRevision CRDs. See the [Working with CRD-Based PackageRevisions tutorial]({{% relref "/docs/4_tutorials_and_how-tos/working_with_crd_based_packagerevisions" %}}) for setup instructions.

For detailed configuration options (concurrency tuning, retry behaviour, gRPC limits), see the [Porch Controllers configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-controllers-config" %}}).
