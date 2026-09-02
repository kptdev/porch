---
title: "Function Configuration"
type: docs
weight: 1
description: "Configure KRM function execution with FunctionConfig resources"
---

Porch uses **FunctionConfig** custom resources to decide *how* a given function image is executed.
Each resource names a function (by image name and optional registry prefixes) and attaches one or more executors:
a Kubernetes pod, a local binary in the function-runner process, or an in-process Go call inside porch-server / porch-controllers.

At least one of `podExecutor`, `binaryExecutor`, or `goExecutor` must be set.
Porch ships a set of FunctionConfig objects in the `porch-fn-system` namespace as part of the default install; you can add your own or edit the shipped ones.

For how these resources are created during install, see [Installing Porch]({{% relref "/docs/3_getting_started/installing-porch.md" %}}) and [Catalog Deployment]({{% relref "/docs/6_configuration_and_deployments/deployments/catalog-deployment.md" %}}).

## How components consume FunctionConfig

The same CRD is watched independently by three processes.
Each process runs an embedded reconciler that copies matching FunctionConfig objects into its own in-memory store and records the generation it applied on the resource status:

The **porch-server** reconciler (`ReconcilerForServer`) feeds the builtin Go runtime used when the Engine executes functions in-process.
The **function-runner** reconciler (`ReconcilerForFunctionRunner`) feeds the executable evaluator (binary substitution) and the pod evaluator (TTL, parallelism, and template overrides).
The **porch-controllers** reconciler (`ReconcilerForController`) is started with the PackageRevision controller and feeds that controller's builtin runtime.
porch-controllers also pre-loads every FunctionConfig into the store at startup so a pod restart does not leave the cache empty until the informer catches up.

Each reconciler adds its own finalizer (`config.porch.kpt.dev/functionconfig-porch-server`, `...-function-runner`, `...-controller`)
so a FunctionConfig is not fully deleted until every component has dropped it from its store.
The function-runner only caches FunctionConfig objects in the namespace it uses for function pods (default `porch-fn-system`).

Status columns on `kubectl get functionconfigs` show which generation each component has applied.
When those values match `.metadata.generation` and `.status.error` is empty, the spec is live in that component.
Spec changes are picked up without restarting Porch; the reconcilers filter on generation so status-only updates do not retrigger work.

## Matching images to a FunctionConfig

`spec.image` is the **base name** of the function image, without a registry prefix or tag (for example `set-namespace`).
The FunctionConfig `metadata.name` should match `spec.image`.
If two FunctionConfig objects claim the same `spec.image` under different names, the second one is ignored.

`spec.prefixes` lists registry prefixes that should match.
An empty string in that list stands for the process default prefix
(`--default-image-prefix` on porch-server and function-runner, `DEFAULT_IMAGE_PREFIX` on porch-controllers; both default to `ghcr.io/kptdev/krm-functions-catalog`).
A function image is used with a given executor only when its registry prefix matches this list **and** its tag is listed on that executor.

When a Kptfile function specifies a version constraint (the `tag` field) rather than a concrete tag on the image,
the binary and Go executors pick the highest cached tag that satisfies the constraint.
An exact `image:tag` with an empty constraint is looked up as a literal tag.

## Executors

### Pod executor

`spec.podExecutor` configures function-runner pods for the matched tags.
`timeToLive` (default `30m`) is how long an idle pod is kept before garbage collection; the TTL is refreshed on each reuse.
`maxParallelExecutions` caps how many pods may run for this function (function-runner flag `--max-parallel-pods-per-function` is the fallback).
`preferredMaxQueueLength` is the waitlist length per pod (flag `--max-waitlist-length` is the fallback).

`templateOverrides` are merged onto the base `PodTemplate` when a pod is created.
They can set `serviceAccountName`, a pod `securityContext`, and resource / env / envFrom overrides on the init container and the function container.
The base templates themselves are documented in [Pod Templates]({{% relref "pod-templates" %}}).

If `--warm-up-pod-cache` is true (the default), the function-runner pre-creates one pod per FunctionConfig that has a `podExecutor` with at least one tag, using the first prefix and first tag.

### Binary executor

`spec.binaryExecutor` tells the function-runner executable evaluator to run a local binary instead of a pod for the listed tags.
`path` is either an absolute path or a path relative to the `--functions` directory (default `./functions`).
The binary is invoked with the ResourceList on stdin; stdout is the transformed ResourceList.

If the image is not in the binary cache, the executable evaluator returns `NotFoundError` and the multi-evaluator falls through to the pod evaluator.

### Go executor

`spec.goExecutor` tells porch-server and porch-controllers to run the function as an in-process Go `ResourceListProcessor` for the listed tags.
`id` is the key used in the builtin cache; if omitted, the FunctionConfig name is used.

Only three processors are compiled into Porch today: `apply-replacements`, `set-namespace`, and `starlark`.
A `goExecutor` on any other FunctionConfig is stored but has no processor to bind to.
The Engine tries the builtin runtime first and falls back to the function-runner over gRPC when the image is not in the Go cache.

## Example

The default install includes a FunctionConfig that uses all three executors for `set-namespace`:

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

With this spec, a pipeline step that asks for `set-namespace:v0.4.5` (or a constraint such as `v0.4` that selects `v0.4.5`) runs in-process.
`set-namespace:v0.4.2` runs as a binary in the function-runner. `set-namespace:v0.4.1` runs in a pod with a 30-minute TTL.

### Per-function pod resources

Use `templateOverrides` when a particular function needs more memory or a different service account than the base template:

```yaml
apiVersion: config.porch.kpt.dev/v1alpha1
kind: FunctionConfig
metadata:
  name: gatekeeper
  namespace: porch-fn-system
spec:
  image: gatekeeper
  prefixes:
    - ""
    - ghcr.io/kptdev/krm-functions-catalog
  podExecutor:
    tags:
      - v0.2.1
    timeToLive: 30m
    maxParallelExecutions: 3
    preferredMaxQueueLength: 2
    templateOverrides:
      container:
        resources:
          requests:
            memory: "512Mi"
            cpu: "500m"
          limits:
            memory: "1Gi"
            cpu: "1000m"
```

## Status

```yaml
status:
  apiServerObservedGeneration: 1
  functionRunnerObservedGeneration: 1
  controllerObservedGeneration: 1
  error: ""
```

`apiServerObservedGeneration`, `functionRunnerObservedGeneration`, and `controllerObservedGeneration` are the `.metadata.generation` each component last applied.
`error` is set when that component failed to apply the spec; it is cleared on the next successful reconcile.

## RBAC

The default Porch roles already grant the required verbs.
porch-server (aggregated-apiserver ClusterRole) and the function-runner (`porch-function-executor` Role in `porch-fn-system`)
can get, list, watch, and patch FunctionConfig objects and update `functionconfigs/status`.
The function-runner also has get/list/watch/create/update/patch on `podtemplates` and `servicetemplates` so it can read and, if missing, create the base templates.
