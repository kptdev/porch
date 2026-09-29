---
title: "Subpackage Operations"
type: docs
weight: 2
description: |
  How the PR Controller handles independent subpackage clone and upgrade operations.
---

## Overview

Subpackage operations allow an independent subpackage to be cloned into, or upgraded within, an existing Draft package revision. Unlike source execution (which runs once at package creation time and is guarded by `status.creationSource`), subpackage operations can be applied repeatedly to the same package revision — one operation at a time — and are guarded by `status.lastSubpackageOperationHash`.

The operation is specified via `spec.subpackageOperation` on the PackageRevision CRD.

## Idempotency

Each time the controller reconciles a PackageRevision that has `spec.subpackageOperation` set, it computes a SHA-256 hash of the operation spec and compares it to `status.lastSubpackageOperationHash`. If they match, the operation has already been executed and is skipped. The hash is only written to status after the operation completes successfully, so a failure leaves the hash unset and the operation will be retried on the next reconcile.

## Clone

A subpackage clone copies an upstream package revision into a subdirectory of the parent Draft package revision.

```yaml
spec:
  subpackageOperation:
    subpackageDir: components/networking
    cloneFrom:
      upstreamRef:
        name: blueprints.networking.v1
```

The controller:

1. Fetches the upstream package content and updates its `Kptfile` upstream/upstreamLock fields
2. Rewrites `metadata.name` in the subpackage `Kptfile` to the dot-separated subpackage path (e.g. `components.networking`)
3. Merges the subpackage resources into the parent package under `subpackageDir/`
4. Creates a draft from the existing parent content, writes the merged resources, and closes the draft
5. Writes `status.lastSubpackageOperationHash` to mark the operation complete

The subdirectory must not already exist in the parent package.

## Upgrade

A subpackage upgrade performs a 3-way merge between the old upstream, new upstream, and current subpackage content.

```yaml
spec:
  subpackageOperation:
    subpackageDir: components/networking
    upgrade:
      oldUpstream:
        name: blueprints.networking.v1
      newUpstream:
        name: blueprints.networking.v2
      currentPackage:
        name: deployments.my-app.v2
      strategy: resource-merge
```

The controller:

1. Reads the old upstream, new upstream, and current package resources
2. Extracts the subpackage resources from the current package (stripping the `subpackageDir/` prefix)
3. Performs a 3-way merge using the specified strategy (default: `resource-merge`)
4. Updates the subpackage `Kptfile` upstream/upstreamLock to reference the new upstream
5. Rewrites the merged subpackage resources back under `subpackageDir/` in the parent package
6. Creates a draft, writes the result, and closes the draft
7. Writes `status.lastSubpackageOperationHash`

The same merge strategies available for regular package upgrades apply here. See [source-execution.md]({{< relref "source-execution.md" >}}) for strategy descriptions.

## Error Handling

Failures at any step call `setFailedConditionsAndLog`, which sets `Ready=False` and `Rendered=False` on the PackageRevision status and logs the error. The hash is not written on failure, so the operation will be retried on the next reconcile.

## Relationship to Source Execution

| Aspect | Source Execution | Subpackage Operation |
|--------|-----------------|----------------------|
| Trigger | `spec.source` | `spec.subpackageOperation` |
| Guard | `status.creationSource` | `status.lastSubpackageOperationHash` |
| Runs | Once per package lifetime | Once per distinct operation spec |
| Result | Creates initial package content | Modifies existing Draft package content |
