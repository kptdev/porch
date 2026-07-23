---
title: "User-Facing Differences"
type: docs
weight: 3
description: |
  What changes for users when moving from the aggregated API to the CRD-based architecture.
---

## Overview

This page describes the user-visible differences between the aggregated API architecture and the CRD-based controller architecture. If you have been using the aggregated API (`porch.kpt.dev/v1alpha1`), this is what changes in your workflow.

## API Group and Version

| | Aggregated API | CRD-based |
|---|----------|----------|
| **API resource** | Aggregated API (custom REST storage) | Native CRD in etcd |
| **apiVersion** | `porch.kpt.dev/v1alpha1` | `porch.kpt.dev/v1alpha2` |
| **PackageRevisionResources** | Same (`porch.kpt.dev/v1alpha1`) | Same (unchanged) |

The two API versions are independent. There is no conversion webhook between them. A PackageRevision created via the aggregated API is not automatically visible as a CRD (and vice versa).

## Synchronous vs Asynchronous

The most significant user-facing change is the execution model:

**Aggregated API**: A `kubectl apply` that creates a PackageRevision blocks until the package is created in Git and rendered. When the command returns, the package is ready.

{{% alert title="⚠️ Major Operational Difference" color="warning" %}}
**CRD-based**: `kubectl apply` returns **immediately** after the CRD is written to etcd. **The PR Controller reconciles in the background asynchronously.** You must check status conditions to observe progress — the command returning does NOT mean the operation is complete.
{{% /alert %}}

You observe progress through status conditions:

```bash
# Wait for the package to be ready
kubectl wait packagerevision my-pkg --for=condition=Ready --timeout=30s
```

This means you need to check conditions before assuming work is complete. The tradeoff is that the API never blocks or times out on long operations (large renders, slow Git repos).

## Status Conditions

The CRD-based architecture uses standard `metav1.Condition` fields on the PackageRevision status:

| Condition | State | Meaning |
|-----------|-------|---------|
| `Ready` | `True` | The package is in the desired lifecycle state and healthy |
| `Ready` | `False` | An error prevented the package from reaching the desired state |
| `Rendered` | `True` | The KRM function pipeline has been executed on the current content |
| `Rendered` | `False` | Rendering failed (e.g., KRM function error) |
| `Rendered` | `Unknown` | Rendering is in progress |

These conditions are the primary way to observe controller progress. They include `observedGeneration`, `lastTransitionTime`, and `message` fields for debugging.

The aggregated API does not have these conditions because operations are synchronous.

## Package Creation

The **aggregated API** uses an imperative "tasks" model in the spec:

```yaml
spec:
  tasks:
    - type: init
      init:
        description: "..."
```

The **CRD-based architecture** uses a declarative `source` field:

```yaml
spec:
  source:
    init:
      description: "..."
```

The source is executed once. After creation, `status.creationSource` records what was done and the source field is not re-processed.

## Revision Number

| | Aggregated API | CRD-based |
|---|----------|----------|
| **Location** | `spec.revision` | `status.revision` |

In the CRD-based architecture, the revision number is controller-assigned on publish and lives in status (since it is an observed value, not user intent).

## Lifecycle Field

The `spec.lifecycle` field is the same in both architectures (Draft, Proposed, Published, DeletionProposed). The difference is who acts on it:

- **Aggregated API**: The API Server processes the transition synchronously in the request handler.
- **CRD-based**: The PR Controller processes it asynchronously during reconciliation.

## Content Access (PRR)

`PackageRevisionResources` **remains an aggregated API** served by the Porch API Server — it is the **only component that does not become a native CRD** in the v1alpha2 architecture. You read and write package content the same way regardless of which architecture manages the PackageRevision.

This design choice allows content access to bypass etcd's object size limits (which PRR can exceed), while keeping the PackageRevision metadata as a native CRD.

## RBAC

**Aggregated API**: Uses custom authorization logic implemented in the API Server. Requires custom RBAC configuration.

**CRD-based**: Uses standard Kubernetes RBAC. You can grant access to PackageRevisions using normal ClusterRole/RoleBinding resources:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: packagerevision-editor
rules:
  - apiGroups: ["porch.kpt.dev"]
    resources: ["packagerevisions"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

## CLI (porchctl)

The `porchctl` CLI supports both architectures. Use the `--api-version=v1alpha2` flag for CRD-based operations:

```bash
porchctl rpkg get --api-version=v1alpha2
porchctl rpkg init my-package --api-version=v1alpha2 --repository=my-repo --workspace=v1
```

## What Stays the Same

- PackageRevisionResources (PRR) for content access
- Repository CRD registration and sync
- KRM function rendering pipeline
- PackageVariant/PackageVariantSet automation
- Git storage format (branches for drafts, tags for published)
- Lifecycle states and their semantics
- Workspace naming conventions
