---
title: "Creating Packages"
type: docs
weight: 2
description: |
  Create, push content to, render, and publish a package using the CRD-based architecture.
---

## Overview

This guide walks through the full lifecycle of a PackageRevision managed by the PR Controller: creating a package, pushing content, observing rendering, and publishing.

All operations use standard `kubectl` commands against the `PackageRevision` CRD (`porch.kpt.dev/v1alpha2`).

## Create a Package (Init)

Create a new package by applying a PackageRevision with `spec.source.init`:

```yaml
apiVersion: porch.kpt.dev/v1alpha2
kind: PackageRevision
metadata:
  name: my-repo-my-package-v1
  namespace: default
spec:
  repository: my-repo
  packageName: my-package
  workspaceName: v1
  lifecycle: Draft
  source:
    init:
      description: "My first CRD-based package"
      keywords:
        - example
```

```bash
kubectl apply -f packagerevision.yaml
```

The CRD is created immediately in etcd. The PR Controller picks it up and executes the init source operation in the background.

## Watch for Ready

The controller reports progress via status conditions. Wait for the package to become ready:

```bash
kubectl wait packagerevision my-repo-my-package-v1 \
  --for=condition=Ready \
  --timeout=30s
```

Or inspect the conditions directly:

```bash
kubectl get packagerevision my-repo-my-package-v1 -o jsonpath='{.status.conditions}'
```

When both `Ready` and `Rendered` conditions are True, the package has been created and rendered successfully.

## Push Content

Edit the package contents through `PackageRevisionResources` (PRR). This is an aggregated API served by the Porch API Server:

```bash
# Get current resources
kubectl get packagerevisionresources my-repo-my-package-v1 -o yaml > prr.yaml

# Edit prr.yaml to add/modify resources in spec.resources
# Then apply
kubectl apply -f prr.yaml
```

After pushing content, the API Server patches the `porch.kpt.dev/render-request` annotation on the PackageRevision CRD. This triggers the PR Controller to render the updated content.

## Observe Rendering

After a content push, the Rendered condition temporarily transitions to `Unknown` (rendering in progress) and then back to True when complete:

```bash
kubectl get packagerevision my-repo-my-package-v1 \
  -o jsonpath='{.status.conditions[?(@.type=="Rendered")]}'
```

If rendering fails (e.g. a KRM function error), the Rendered condition is set to False with an error message in the `message` field.

## Publish

When you are satisfied with the package content, transition it to Published:

```bash
kubectl patch packagerevision my-repo-my-package-v1 \
  --type=merge \
  -p '{"spec": {"lifecycle": "Published"}}'
```

The controller transitions the package in Git (typically from a branch to a tag), assigns a revision number, and updates the `porch.kpt.dev/latest-revision` label.

```bash
kubectl get packagerevision my-repo-my-package-v1 \
  -o jsonpath='{.status.revision}'
```

## Create a New Revision (Copy)

To edit a published package, create a new revision by copying from the existing one:

```yaml
apiVersion: porch.kpt.dev/v1alpha2
kind: PackageRevision
metadata:
  name: my-repo-my-package-v2
  namespace: default
spec:
  repository: my-repo
  packageName: my-package
  workspaceName: v2
  lifecycle: Draft
  source:
    copyFrom:
      name: my-repo-my-package-v1
```

This creates a new draft workspace with the content of the published revision, ready for editing.

## Clone from Upstream

To create a downstream package from an upstream source:

```yaml
apiVersion: porch.kpt.dev/v1alpha2
kind: PackageRevision
metadata:
  name: my-repo-downstream-pkg-v1
  namespace: default
spec:
  repository: my-repo
  packageName: downstream-pkg
  workspaceName: v1
  lifecycle: Draft
  source:
    cloneFrom:
      upstreamRef:
        name: upstream-repo-upstream-pkg-v1
```

The controller clones the upstream content and sets up the Kptfile upstream/upstreamLock tracking.

## Delete a Package

Draft packages can be deleted directly:

```bash
kubectl delete packagerevision my-repo-my-package-v2
```

Published packages are protected by a finalizer. To delete a published package, first transition it to DeletionProposed:

```bash
kubectl patch packagerevision my-repo-my-package-v1 \
  --type=merge \
  -p '{"spec": {"lifecycle": "DeletionProposed"}}'

kubectl delete packagerevision my-repo-my-package-v1
```

## Using porchctl

All operations in this guide can also be performed using the `porchctl` CLI with the `--api-version=v1alpha2` flag. See the [User-Facing Differences]({{% relref "differences" %}}) page for CLI examples targeting the CRD-based architecture.

## Summary

{{% alert title="Note" color="warning" %}}
Validating admission webhooks for PackageRevision are not yet implemented. The operations below represent the intended usage patterns. The controller handles common mistakes defensively (e.g. blocking deletion of published packages via a finalizer), but some invalid states are not rejected at admission time.
{{% /alert %}}

| Operation | How |
|-----------|-----|
| Create (init) | `kubectl apply` with `spec.source.init` |
| Create (clone) | `kubectl apply` with `spec.source.cloneFrom` |
| Create (copy) | `kubectl apply` with `spec.source.copyFrom` |
| Push content | Edit and apply `PackageRevisionResources` |
| Check status | `kubectl get packagerevision -o jsonpath='{.status.conditions}'` |
| Publish | Patch `spec.lifecycle` to `Published` |
| Delete draft | `kubectl delete packagerevision` |
| Delete published | Patch lifecycle to `DeletionProposed`, then delete |
