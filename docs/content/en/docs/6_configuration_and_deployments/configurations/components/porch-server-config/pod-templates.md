---
title: "Pod Templates"
type: docs
weight: 2
description: "Customize function evaluator pods with PodTemplate, ServiceTemplate, and FunctionConfig overrides"
---

The Engine pod evaluator (porch-server and the PackageRevision controller) builds function pods from the **PodTemplate**/**ServiceTemplate** objects `base-pod-template` and `base-service-template` in the function-pod namespace (default `porch-fn-system`), plus per-function overrides from the matching [FunctionConfig]({{% relref "/docs/6_configuration_and_deployments/configurations/components/function-runner-config/function-configuration" %}}).
There is no `--function-pod-template` flag and no ConfigMap template. This page moved from Function Runner with the pod evaluator.

The template names and the creation behavior are configurable via porch-server flags (`--base-pod-template-name`, `--base-service-template-name`, `--skip-template-creation`, `--template-wait-timeout`). See [Template discovery and creation](#template-discovery-and-creation) below.

For how those templates are used during pod creation, see [Pod Lifecycle Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/pod-lifecycle-management.md" %}}).

## How templates are applied

On pod creation the Engine:

1. Gets the base PodTemplate (`corev1.PodTemplate`, named `base-pod-template` by default) and base ServiceTemplate (`config.porch.kpt.dev/v1alpha1` ServiceTemplate, named `base-service-template` by default) from the function-pod namespace (`--pod-namespace`, default `porch-fn-system`). By default, if either is missing it creates it from the inline default shipped in the binary. The names and the creation behavior can be changed with flags — see [Template discovery and creation](#template-discovery-and-creation).
2. Patches the function container with the requested image, the wrapper-server command, the original image entrypoint as arguments, and any image-pull secret required for private registries.
3. Patches pod metadata: `fn.kpt.dev/image` label and `fn.kpt.dev/template-version` set to the PodTemplate `resourceVersion`.
4. Merges `spec.podExecutor.templateOverrides` from the FunctionConfig for that image, if any.
5. Creates the pod and a ClusterIP Service from the ServiceTemplate.

Cluster-wide defaults (node selectors, extra volumes, security context that every function should inherit) belong on the base PodTemplate. Per-function CPU/memory, env, or service account belong on `templateOverrides`.

## Template contract

Any custom `base-pod-template` must keep a container named `function`.
That container's command must start the wrapper gRPC server. The Engine replaces the image and appends the original function entrypoint to `args`.
An init container named `copy-wrapper-server` is expected as the first init container when `templateOverrides.initContainer` is used, because overrides are merged by index.

The Engine patches the template before creating pods. Leave the function image as a placeholder. It is always replaced.

## Default templates

The default install deploys these objects (trimmed from `deployments/porch/22-function-templates.yaml`):

```yaml
apiVersion: v1
kind: PodTemplate
metadata:
  name: base-pod-template
  namespace: porch-fn-system
template:
  metadata:
    annotations:
      cluster-autoscaler.kubernetes.io/safe-to-evict: "true"
  spec:
    initContainers:
      - name: copy-wrapper-server
        image: ghcr.io/kptdev/porch-wrapper-server:latest
        command:
          - cp
          - -a
          - /home/nonroot/wrapper-server/.
          - /wrapper-server-tools
        volumeMounts:
          - name: wrapper-server-tools
            mountPath: /wrapper-server-tools
    containers:
      - name: function
        image: image-replaced-by-kpt-func-image
        command:
          - /wrapper-server-tools/wrapper-server
        env:
          - name: OTEL_METRICS_EXPORTER
            value: prometheus
          - name: OTEL_TRACES_EXPORTER
            value: none
          - name: OTEL_EXPORTER_PROMETHEUS_HOST
            value: 0.0.0.0
        readinessProbe:
          exec:
            command: ["/wrapper-server-tools/grpc-health-probe", "-addr", "localhost:9446"]
        volumeMounts:
          - name: wrapper-server-tools
            mountPath: /wrapper-server-tools
    volumes:
      - name: wrapper-server-tools
        emptyDir: {}
---
apiVersion: config.porch.kpt.dev/v1alpha1
kind: ServiceTemplate
metadata:
  name: base-service-template
  namespace: porch-fn-system
template:
  spec:
    ports:
      - port: 9446
        protocol: TCP
        targetPort: 9446
        name: server
    selector:
      fn.kpt.dev/image: to-be-replaced
    type: ClusterIP
```

By default, if you delete them, porch-server recreates them from its inline defaults the next time it needs a pod.
Edits you make to the live objects are used for subsequent pod creates.
This auto-creation can be disabled (see [Template discovery and creation](#template-discovery-and-creation)) when the templates are managed externally, for example by Helm.

## Template discovery and creation

By default the pod evaluator looks up the templates by the fixed names `base-pod-template` and `base-service-template` and creates them from the inline defaults when they are missing. Four porch-server flags change this behavior:

| Flag | Default | Effect |
| --- | --- | --- |
| `--base-pod-template-name` | `base-pod-template` | Name of the PodTemplate to look up in the function-pod namespace. |
| `--base-service-template-name` | `base-service-template` | Name of the ServiceTemplate to look up in the function-pod namespace. |
| `--skip-template-creation` | `false` | When `true`, the pod evaluator never creates the inline-default templates. If a template is missing it returns an error, so porch-server fails fast rather than silently falling back to the inline default. |
| `--template-wait-timeout` | `0` (disabled) | When greater than `0`, the pod evaluator polls for a missing template until it appears or this timeout elapses, before either creating it (default) or, with `--skip-template-creation`, failing. |

### Externally managed templates (e.g. Helm)

If a custom PodTemplate/ServiceTemplate is applied by Helm or another tool, the function-runner may start before that template is applied. With the default behavior it would create the inline default first, and operators then have to work around the ordering with Helm hook weights or init containers.

To make ordering explicit instead:

- Set `--skip-template-creation=true` so porch-server never creates the inline default and fails fast if the expected template is absent. Combine this with a readiness/retry strategy at the deployment layer, or
- Set `--template-wait-timeout` to a non-zero duration so porch-server waits for the externally managed template to appear. For example `--template-wait-timeout=2m` waits up to two minutes.

The two flags can be combined: wait up to the timeout for the template, then fail fast (rather than create a default) if it still has not appeared.

Point the evaluator at non-default names with `--base-pod-template-name` / `--base-service-template-name` when your tooling creates templates under different names.

## Customizing the base PodTemplate

Edit `base-pod-template` in `porch-fn-system` to change every function pod.
Typical additions are resource requests and limits on the `function` container, a pod `securityContext`, `nodeSelector` / `tolerations`, and extra volumes.

```yaml
kubectl edit podtemplate base-pod-template -n porch-fn-system
```

Example resource limits on the function container:

```yaml
        resources:
          requests:
            memory: "256Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
```

Example pod security context:

```yaml
    securityContext:
      runAsNonRoot: true
      runAsUser: 65532
      fsGroup: 65532
      seccompProfile:
        type: RuntimeDefault
```

## Per-function overrides

FunctionConfig `spec.podExecutor.templateOverrides` is merged after the base template is patched.
Supported fields are `serviceAccountName`, pod `securityContext`, and `resources` / `env` / `envFrom` on the init container and the function container.
Scheduling fields such as `nodeSelector` are not part of `templateOverrides`; put those on the base PodTemplate.

```yaml
apiVersion: config.porch.kpt.dev/v1alpha1
kind: FunctionConfig
metadata:
  name: gatekeeper
  namespace: porch-fn-system
spec:
  image: gatekeeper
  podExecutor:
    tags:
      - v0.2.1
    templateOverrides:
      serviceAccountName: function-sa
      container:
        resources:
          limits:
            memory: "1Gi"
            cpu: "1000m"
```

## Template versioning

The Engine records the PodTemplate `resourceVersion` on each pod as `fn.kpt.dev/template-version`.
On the next reuse, a mismatch against the current template causes the old pod to be deleted and a new one created.
Existing pods keep serving until they are reused or garbage-collected, so template edits do not immediately disrupt in-flight evaluations.

## RBAC

The default `porch-function-executor` Role in `porch-fn-system` already allows get/list/watch/create/update/patch on `podtemplates` and `servicetemplates`.
porch-server and porch-controllers are bound to that Role. No extra Role is required for the base templates.

## Troubleshooting

If pods fail to start after a template edit:

```bash
kubectl get pods -n porch-fn-system
kubectl describe pod -n porch-fn-system <pod-name>
kubectl logs -n porch-system deployment/porch-server
```

Common causes are invalid YAML on the PodTemplate, a missing `function` container, resource-quota or image-pull failures, and security-policy or node-selector mismatches. If porch-server logs that it cannot get `base-pod-template` or `base-service-template`, check that the RoleBinding for `porch-function-executor` includes the `porch-server` (and `porch-controllers`) ServiceAccount.
