### Function Pod Template

Function execution pods are created by the **Engine pod evaluator** (porch-server and, when enabled, porch-controllers), not Function Runner.

Default manifests install Kubernetes `PodTemplate` `base-pod-template` and `ServiceTemplate` `base-service-template` in `porch-fn-system`. See `deployments/porch/22-function-templates.yaml` and [Pod Templates](../../docs/content/en/docs/6_configuration_and_deployments/configurations/components/porch-server-config/pod-templates.md).

The ConfigMap-based `--function-pod-template` flow documented previously for Function Runner is no longer used.

### How to customize

Edit `base-pod-template` in `porch-fn-system` (or the YAML in `deployments/porch/22-function-templates.yaml` before deploy). porch-server and porch-controllers already have RBAC via the `porch-function-executor` Role.

This folder's `deployment.yaml` is a historical ConfigMap example and is not wired into current porch-server.
