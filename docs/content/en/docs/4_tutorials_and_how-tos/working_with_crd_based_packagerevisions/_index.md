---
title: "Working with CRD-Based PackageRevisions"
type: docs
weight: 3
description: |
  How to use the CRD-based PackageRevision model with the PR Controller.
---

## Prerequisites

Before enabling the CRD-based architecture, ensure you have:

- **Porch** deployed in your cluster (with support for the `Packagerevisions` reconciler)
- **kubectl** configured to communicate with the cluster
- **Repository Controller** running (this is part of the default Porch deployment)
- **Function runner** deployed and reachable (for KRM function rendering)

## What is the CRD-based architecture?

Porch supports a controller-based architecture where PackageRevisions are managed as native Kubernetes CRDs (API version `porch.kpt.dev/v1alpha2`). This replaces the synchronous aggregated API model with an asynchronous controller that reconciles desired state against Git in the background.

For now, this is an opt-in feature. You enable it by adding the `Packagerevisions` reconciler to the controllers deployment. The aggregated API and CRD-based architectures can coexist in the same cluster.

## Why use the CRD-based architecture?

- **Kubernetes-native**: PackageRevisions behave like any other CRD. Use familiar tools and patterns.
- **Asynchronous**: Operations do not block the API request. Create a package and watch status conditions for progress.
- **Observable**: Ready and Rendered conditions, events, and `kubectl describe` show exactly what is happening.
- **Scalable**: The controller handles bursts by queuing work. Concurrency is tunable.
- **SSA-friendly**: Field ownership is explicit. Multiple actors can safely write to the same resource.

## Install the PackageRevision CRD

The `PackageRevision` CRD (`porch.kpt.dev/v1alpha2`) must be installed in the cluster. If you deployed Porch using the standard manifests, the CRD is included. Verify it exists:

```bash
kubectl get crd packagerevisions.porch.kpt.dev
```

Expected output:

```
NAME                            CREATED AT
packagerevisions.porch.kpt.dev  2025-01-15T10:00:00Z
```

## Enable the PR Controller

Add `packagerevisions` to the `--reconcilers` flag on the controllers deployment. If you are already running the repository controller, the flag becomes:

```bash
--reconcilers=repositories,packagerevisions
```

You can patch an existing deployment:

```bash
kubectl -n porch-system patch deployment porch-controllers \
  --type='json' \
  -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/args", "value": ["--reconcilers=repositories,packagerevisions"]}]'
```

## Annotate Repositories for v1alpha2

Repositories must be annotated to opt into CRD-based management. Without this annotation, the repository controller will not create v1alpha2 PackageRevision CRDs, and the API server will continue to handle the repository via the v1alpha1 path.

Add the annotation to each repository you want managed by the PR Controller:

```bash
kubectl annotate repository my-repo porch.kpt.dev/v1alpha2-migration=true
```

Or include it in the Repository manifest:

```yaml
apiVersion: config.porch.kpt.dev/v1alpha1
kind: Repository
metadata:
  name: my-repo
  namespace: default
  annotations:
    porch.kpt.dev/v1alpha2-migration: "true"
spec:
  type: git
  git:
    repo: https://github.com/example/my-repo.git
    branch: main
```

Once annotated:
- The repository controller discovers packages and creates v1alpha2 PackageRevision CRDs for them
- The API server rejects v1alpha1 PackageRevision operations on this repository (returning "use the v1alpha2 API")
- PRR updates use the async render path (patching the render-request annotation instead of rendering synchronously)

## Verify the Controller is Running

Check that the controllers pod restarted and the PR Controller is active:

```bash
kubectl -n porch-system logs deployment/porch-controllers | grep "packagerevisions"
```

You should see log lines indicating the reconciler has started:

```
"Starting workers" controller="packagerevisions" worker count=50
```

## Verify Function Runner

If you plan to use external KRM functions (container-based), confirm the function runner is reachable:

```bash
kubectl -n porch-system get pods -l app=function-runner
```

The `FUNCTION_RUNNER_ADDRESS` environment variable must be set on the controllers deployment. The default Porch manifests already configure this.

## Next Steps

- [Creating packages]({{% relref "/docs/4_tutorials_and_how-tos/working_with_crd_based_packagerevisions/creating-packages" %}}): create, render, and publish a package using the PR Controller
- [User-facing differences]({{% relref "/docs/4_tutorials_and_how-tos/working_with_crd_based_packagerevisions/differences" %}}): what changes compared to the aggregated API model
