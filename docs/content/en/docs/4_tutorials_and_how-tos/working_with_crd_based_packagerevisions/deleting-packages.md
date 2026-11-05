---
title: "Deleting packages safely"
type: docs
weight: 4
description: "How Porch blocks deletion of a v1alpha2 PackageRevision that other packages depend on"
---

Porch stops you deleting a PackageRevision while another package in the same namespace is cloned from it or upgraded to it. This page covers how the check works, what the errors mean, and how to delete a multi-version package safely.

## What is blocked

A package is blocked when another PackageRevision in the same namespace references it by name, through:

- a clone (`spec.source.cloneFrom`), or
- an upgrade to it as the new upstream (`spec.source.upgrade`).

Copies (`copyFrom`) are not tracked. A copy is a new revision of the same package, not a dependency.

Subpackages cloned from a git URL are **not** protected yet. Deleting their upstream is still allowed.

## Where the check runs

The check runs in two places, and both fail closed:

- **DeletionProposed.** Moving a package to DeletionProposed is rejected while a dependent exists. The package never enters the state, so there is nothing to revert.
- **DELETE.** Deleting a package is rejected while a dependent exists. This covers packages that were never proposed, such as drafts.

The error lists up to 20 blocking packages and gives the total:

```text
cannot propose deletion: PackageRevision referenced by 2 downstream package(s): default/custom-a.v1, default/custom-b.v2
```

Remove or upgrade the dependent packages first, then propose the deletion again.

## Requirement: the webhook must be deployed

The check is an admission webhook. Without it, referenced packages can be deleted with no error. Confirm it is registered:

```bash
kubectl get validatingwebhookconfiguration packagerevision-validating-webhook-configuration
```

The webhook uses `failurePolicy: Fail`, so if it is registered but unreachable, every PackageRevision create, update, and delete is rejected until it recovers. The controller logs a message at startup if the webhook is missing or set to `Ignore`:

```bash
kubectl -n porch-system logs deployment/porch-controllers | grep "deletion guard"
```

## Deleting a multi-version package

A blueprint with several versions must be deleted version by version. If one version is blocked partway through, the earlier versions are already gone. To avoid that, do the deletion in two phases:

1. **Propose every version and the main package.** Each proposal that is rejected means that version still has dependents. If any proposal is rejected, move the versions already proposed back to Published and stop. Nothing has been deleted yet.
2. **Delete every version.** Start this phase only after every proposal succeeded.

Proposing deletion is not destructive, so phase 1 can be undone safely. Phase 2 can still fail if a new dependent is created between the two phases. Each delete is checked again, so the failure stops that version and no others.
