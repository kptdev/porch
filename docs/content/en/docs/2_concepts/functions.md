---
title: "KRM Functions"
type: docs
weight: 7
description: |
  Understanding KRM functions in Porch: how functions transform and validate package resources.
---

## What are Functions in Porch?

**Functions** in Porch are [KRM (Kubernetes Resource Model) functions](https://github.com/kubernetes-sigs/kustomize/blob/master/cmd/config/docs/api-conventions/functions-spec.md) -
programs (usually containerized) that transform or validate Kubernetes resource manifests within a package's files. Functions are
declared in a package's Kptfile and executed by Porch when rendering the package.

Functions enable:
- Automated resource generation and modification
- Policy enforcement and validation
- Configuration customization without manual editing
- Repeatable, auditable transformations

For details on how to declare and configure functions in the Kptfile pipeline, see the [kpt functions documentation](https://kpt.dev/book/04-using-functions/).

## Function Configuration

Porch uses **FunctionConfig** custom resources to choose an executor for each function image and to supply executor-specific settings.
A FunctionConfig names the function image and optional registry prefixes, then attaches a pod executor, a binary executor, a Go executor, or any combination of the three.
Tags on each executor select which image versions use that path.

The default Porch install deploys FunctionConfig objects for common catalog functions into `porch-fn-system`.
porch-server, function-runner, and porch-controllers each run an embedded reconciler that copies those objects into an in-memory store used at evaluation time.

```yaml
apiVersion: config.porch.kpt.dev/v1alpha1
kind: FunctionConfig
metadata:
  name: set-namespace
  namespace: porch-fn-system
spec:
  image: set-namespace
  prefixes:
    - ""
    - ghcr.io/kptdev/krm-functions-catalog
  podExecutor:
    tags:
      - v0.4.1
    timeToLive: 30m
  binaryExecutor:
    tags:
      - v0.4.2
    path: set-namespace
  goExecutor:
    id: set-namespace
    tags:
      - v0.4
      - v0.4.5
```

The spec, status, and matching rules are documented in [Function Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/components/function-runner-config/function-configuration.md" %}}).

## Function Execution in Porch

Porch executes functions through the Engine's function runtime.
The builtin runtime (in porch-server and porch-controllers) handles images listed on a FunctionConfig `goExecutor`.
Everything else is sent over gRPC to the **function-runner**, which tries a local binary from `binaryExecutor` first and falls back to a Kubernetes pod from `podExecutor`.

The **pod executor** is the default path for arbitrary function images: the function-runner creates (or reuses) a pod, injects a wrapper gRPC server, and runs the function image in isolation.
TTL, parallelism, and pod-spec overrides come from the matching FunctionConfig.
The **binary executor** runs a pre-built binary inside the function-runner process, which avoids pod startup cost.
The **Go executor** calls a compiled-in `ResourceListProcessor` (today: apply-replacements, set-namespace, and starlark) with no extra process at all.

Regardless of executor, Porch passes the package's resources to [kpt](https://kpt.dev), which passes them on as a [ResourceList](https://github.com/kubernetes-sigs/kustomize/blob/master/cmd/config/docs/api-conventions/functions-spec.md#resourcelist) to each function in the pipeline in order.
kpt runs the functions sequentially and returns the results to Porch, which stores them in the PackageRevisionResources `status.renderStatus` field.
Rendering is triggered automatically after creating or cloning a package revision, after updating a package revision, and when a package revision is proposed.

## When Functions Execute

### Automatic rendering

This occurs when a Draft package revision is created (init, clone, or edit tasks), when package resources are modified by an update through the PackageRevisionResources API resource, or when a package revision is proposed.

### Manual rendering via render task

Users can add an explicit `render` task to force re-execution of the pipeline. Note that the `render` task is not persisted in the package revision's task list.

### Lifecycle constraints

Functions execute only on **Draft** package revisions. Proposed package revisions must be rejected back to **Draft** status to be eligible for rendering again. Published package revisions are immutable—their rendered state is frozen. Function results are preserved in the `status.renderStatus` of the PackageRevisionResources API resource across lifecycle transitions.

## Function Results in Porch

Function execution results are stored in the status of the PackageRevisionResources API resource:

```yaml
apiVersion: porch.kpt.dev/v1alpha1
kind: PackageRevisionResources
...
status:
  renderStatus:
    error: ""
    result:
      exitCode: 0
      items:
        - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.5
          exitCode: 0
        - image: ghcr.io/kptdev/krm-functions-catalog/kubeconform:v0.1.3
          exitCode: 1
          results:
            - message: "Invalid resource configuration"
              severity: error
```

The `renderStatus` field contains:
- Overall exit code (0 for success, non-zero for failure)
- Per-function results including exit codes and validation messages
- Error details if function execution failed

{{< alert title="Note" color="primary" >}}
By default, render failures (including validation failures) prevent Draft package revisions from being created and PackageRevisionResources from
being updated. However, when **updating resources on an existing Draft** (e.g. via `porchctl rpkg push`),
adding the `porch.kpt.dev/push-on-render-failure: "true"` annotation **to the PackageRevision** allows persisting resources even when rendering fails,
enabling iterative development on incomplete packages.
{{< /alert >}}


## Key Points

- Functions are standard KRM functions declared in the Kptfile pipeline (see [kpt functions docs](https://kpt.dev/book/04-using-functions/))
- Function execution is configured with FunctionConfig custom resources that select a pod, binary, or Go executor per image tag
- porch-server, function-runner, and porch-controllers each reconcile FunctionConfig objects into an in-memory store used at evaluation time
- Functions automatically execute during package rendering on Draft package revisions
- Function results are stored in `status.renderStatus` of the PackageRevisionResources view of a package revision
- Published packages are immutable - functions don't re-execute after publication
- By default, render failures (including validation failures) block Draft package creation and package revision resource updates
- When updating resources on an existing Draft (e.g., `porchctl rpkg push`), the `porch.kpt.dev/push-on-render-failure: "true"` annotation allows persisting resources despite render failures
