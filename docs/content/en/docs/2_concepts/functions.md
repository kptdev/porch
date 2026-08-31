---
title: "KRM Functions"
type: docs
weight: 7
description: |
  Understanding KRM functions in Porch: how functions transform and validate package resources.
---

## What are Functions in Porch?

**Functions** in Porch are [KRM (Kubernetes Resource Model) functions](https://github.com/kubernetes-sigs/kustomize/blob/master/cmd/config/docs/api-conventions/functions-spec.md) -
containerized programs that transform or validate Kubernetes resource manifests within a package's files. Functions are
declared in a package's Kptfile and executed by Porch when rendering the package.

Functions enable:
- Automated resource generation and modification
- Policy enforcement and validation
- Configuration customization without manual editing
- Repeatable, auditable transformations

For details on how to declare and configure functions in the Kptfile pipeline, see the [kpt functions documentation](https://kpt.dev/book/04-using-functions/).

## Function Execution in Porch

Porch executes functions through the **Engine** function runtime chain: builtin Go functions, the Function Runner (cached binaries via `exec_path`), then the in-process **pod evaluator**. Containerized functions run in Kubernetes pods managed by porch-server (and by the PackageRevision controller when `WRAPPER_SERVER_IMAGE` is set). Porch passes the package's resources to kpt, which passes the resources on as a [ResourceList](https://github.com/kubernetes-sigs/kustomize/blob/master/cmd/config/docs/api-conventions/functions-spec.md#resourcelist) to each function in the pipeline in turn. kpt executes the functions sequentially in the order declared in the Kptfile pipeline and passes the function results back to Porch, which stores them in the PackageRevisionResources's `status.renderStatus` field. Execution is triggered automatically following creation or clone of a package revision, update of a package revision, and when a package revision is proposed.

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
- Porch invokes kpt to execute functions via a function-runner component using containerized execution
- Functions automatically execute during package rendering on Draft package revisions
- Function results are stored in `status.renderStatus` of the PackageRevisionResources view of a package revision
- Published packages are immutable - functions don't re-execute after publication
- By default, render failures (including validation failures) block Draft package creation and package revision resource updates
- When updating resources on an existing Draft (e.g., `porchctl rpkg push`), the `porch.kpt.dev/push-on-render-failure: "true"` annotation allows persisting resources despite render failures
