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

`PackageRevisionResources` **remains an aggregated API** served by the Porch API Server — it is the **only component that does not become a native CRD** in the v1alpha2 architecture. You read and write package content the same way regardless of which architecture manages the PackageRevision, including filtered GET (`?file=<path>`) and partial UPDATE (`?partial=true`). See [Reading Selected Package Files]({{% relref "/docs/4_tutorials_and_how-tos/working_with_package_revisions/inspecting-packages.md#reading-selected-package-files" %}}) and [Partial Package Content Updates]({{% relref "/docs/4_tutorials_and_how-tos/working_with_package_revisions/inspecting-packages.md#partial-package-content-updates" %}}).

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

## Filtering by PackageMetadata Labels

The **aggregated API** supports filtering by `spec.packageMetadata.labels` directly via field selectors in its custom REST storage.

The **CRD-based architecture** cannot use CRD field selectors for nested map fields (Kubernetes limitation). Instead, the PR Controller mirrors Kptfile labels to the PackageRevision object's `metadata.labels` with the prefix `porch.kpt.dev/kptfile-label__`. Slashes in label keys are escaped as `__`.

This enables standard Kubernetes label selectors:

```bash
# Filter by packageMetadata label
kubectl get packagerevisions -n default --selector 'porch.kpt.dev/kptfile-label__env=prod'

# Label key with slash (app.example.com/name) → escaped as double-underscore
kubectl get packagerevisions -n default --selector 'porch.kpt.dev/kptfile-label__app.example.com__name=myapp'

# Combine selectors
kubectl get packagerevisions -n default \
  --selector 'porch.kpt.dev/kptfile-label__env=prod,porch.kpt.dev/kptfile-label__tier=backend'
```

Only labels are mirrored (not annotations). This is by design: Kubernetes label selectors (used for filtering via `kubectl -l`) only work on labels, not annotations. Since the purpose of mirroring is to enable filtering, annotations are not included.

The mirroring happens automatically after each render cycle (Draft packages only; Proposed and Published labels are frozen at publication time).

### System Labels

The Repository Controller automatically manages two system labels on every PackageRevision:
- `porch.kpt.dev/repository: <repo-name>` — identifies the owning repository
- `porch.kpt.dev/latest-revision: "true"` — marks the latest revision of each package

These labels enable reliable filtering:

```bash
# All packages in a repository
kubectl get packagerevisions -l porch.kpt.dev/repository=my-repo

# Only the latest revision of each package
kubectl get packagerevisions -l porch.kpt.dev/latest-revision=true
```

## Migration from v1alpha1

When you annotate a Repository with `porch.kpt.dev/v1alpha2-migration=true`, the Repository Controller automatically creates v1alpha2 PackageRevision CRDs for all packages it discovers (Published, Proposed, and Draft packages) during repository sync. Git package content is preserved, but the annotation also stops v1alpha1 PackageRevision access for that repository without waiting for CRD creation to complete. Update clients and automation to use v1alpha2 and wait for the corresponding CRDs to appear before resuming operations.

### Data Preservation

| Item | Preserved? | Details |
|------|-----------|---------|
| ObjectMeta.labels (user-added) | ✅ YES | Extracted from v1alpha1 and seeded to v1alpha2 |
| ObjectMeta.annotations (user-added) | ✅ YES | Extracted from v1alpha1 and seeded to v1alpha2 |
| spec.packageMetadata | ✅ YES | Read fresh from Kptfile during migration; always matches source |
| System labels (v1alpha1) | ❌ REPLACED | Filtered out; v1alpha2 system labels created fresh |

### Filtering Continuity

User-added ObjectMeta labels from v1alpha1 are preserved, so existing filtering queries continue to work after migration:

```bash
# This query still works after migration (if labels were migrated from v1alpha1)
kubectl get packagerevisions -l team=platform
```

However, for reliable, auditable filtering in v1alpha2, use the Kptfile label mirror feature described above instead of manual ObjectMeta labels.

### What Changes

After migration, PackageRevision resources:
- Become native Kubernetes CRDs (not aggregated API objects)
- Get automatic Kptfile label mirroring (if labels exist in Kptfile)
- Get v1alpha2 system labels (`porch.kpt.dev/repository`, `porch.kpt.dev/latest-revision`) — system labels from v1alpha1 are filtered and replaced during this process
- Follow the asynchronous reconciliation model (see [Synchronous vs Asynchronous](#synchronous-vs-asynchronous) above)
