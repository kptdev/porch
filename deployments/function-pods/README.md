# Function pod templates

The function-runner builds evaluator pods from a `PodTemplate` named `base-pod-template` and a `ServiceTemplate` named `base-service-template` in `porch-fn-system`.
The default Porch install already deploys those objects (see `deployments/porch/22-function-templates.yaml`).
There is no `--function-pod-template` flag and no ConfigMap template.

To customize every function pod, edit the live objects or apply a replacement such as [deployment.yaml](deployment.yaml) from this directory:

```
kubectl apply -f deployment.yaml
```

Per-function CPU, memory, env, or service account belongs on FunctionConfig `spec.podExecutor.templateOverrides` instead.
Details are in the [Pod Templates](../../docs/content/en/docs/6_configuration_and_deployments/configurations/components/function-runner-config/pod-templates.md) documentation.

Existing function pods keep the previous template until they are reused or garbage-collected.
After a template change, the next evaluation for that image creates a replacement pod.
