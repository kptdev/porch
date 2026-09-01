---
title: "Pod Templates"
type: docs
weight: 2
description: "Customize function evaluator pod specifications using PodTemplate CRs"
---

The Engine pod evaluator (porch-server and the PackageRevision controller) customizes KRM function pods through Kubernetes `PodTemplate` and `ServiceTemplate` objects named `base-pod-template` and `base-service-template` in the function-pod namespace (`porch-fn-system`). This page moved from Function Runner with the pod evaluator.

## Overview

By default, the Engine uses an inline pod template and creates `base-pod-template` / `base-service-template` in `porch-fn-system` if they are missing. Default manifests ship these objects in `deployments/porch/22-function-templates.yaml`. Edit those CRs to customize resource limits, security contexts, node selectors, tolerations, and other pod-level settings.

The pod template system provides:
- **Resource customization** - Configure CPU/memory limits for function pods
- **Security hardening** - Apply security contexts and pod security standards
- **Scheduling control** - Add node selectors, affinity rules, and tolerations
- **Network policies** - Customize service specifications for service mesh integration
- **Volume management** - Add additional volumes and volume mounts

For architectural details on how pod templates are used in the pod lifecycle, see [Pod Lifecycle Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/pod-lifecycle-management.md" %}}).

## Template Contract

Any custom pod template must fulfill the following requirements:

1. **Function container** - Must contain a container named `function`
2. **Wrapper server entrypoint** - The `function` container's entrypoint must start the wrapper gRPC server
3. **Image replacement** - The `function` container's image can be set to any KRM function image without breaking the wrapper server entrypoint
4. **Entrypoint arguments** - The `function` container's args can be appended with entries from the function image's Dockerfile ENTRYPOINT

The Engine automatically patches the template with function-specific configuration (image, entrypoint, pull secrets, FunctionConfig TemplateOverrides) before creating pods.

## Enabling Pod Templates

Default Porch manifests already apply `base-pod-template` and `base-service-template` in `porch-fn-system` (`deployments/porch/22-function-templates.yaml`). porch-server and porch-controllers are bound to the `porch-function-executor` Role, which can get/create those objects. There is no `--function-pod-template` flag.

To customize, edit the `PodTemplate` in `porch-fn-system` (or replace the shipped YAML before deploy). Example:

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
          image: to-be-replaced
          command: 
            - /wrapper-server-tools/wrapper-server
          volumeMounts:
            - name: wrapper-server-tools
              mountPath: /wrapper-server-tools
      volumes:
        - name: wrapper-server-tools
          emptyDir: {}
```

The matching service frontend is `ServiceTemplate` `base-service-template` in the same namespace (see `deployments/porch/22-function-templates.yaml`).

The snippets below are pod spec fragments to merge into `base-pod-template`.

## Template Customization Examples

### Resource Limits

Add resource requests and limits to the function container:

```yaml
data:
  template: |
    apiVersion: v1
    kind: Pod
    spec:
      initContainers:
        - name: copy-wrapper-server
          image: ghcr.io/kptdev/porch-wrapper-server:latest
          command: [cp, -a, /home/nonroot/wrapper-server/., /wrapper-server-tools]
          volumeMounts:
            - name: wrapper-server-tools
              mountPath: /wrapper-server-tools
      containers:
        - name: function
          image: to-be-replaced
          command: [/wrapper-server-tools/wrapper-server]
          resources:
            requests:
              memory: "256Mi"
              cpu: "100m"
            limits:
              memory: "512Mi"
              cpu: "500m"
          volumeMounts:
            - name: wrapper-server-tools
              mountPath: /wrapper-server-tools
      volumes:
        - name: wrapper-server-tools
          emptyDir: {}
```

### Security Context

Apply security contexts for enhanced security:

```yaml
data:
  template: |
    apiVersion: v1
    kind: Pod
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      initContainers:
        - name: copy-wrapper-server
          image: ghcr.io/kptdev/porch-wrapper-server:latest
          command: [cp, -a, /home/nonroot/wrapper-server/., /wrapper-server-tools]
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: wrapper-server-tools
              mountPath: /wrapper-server-tools
      containers:
        - name: function
          image: to-be-replaced
          command: [/wrapper-server-tools/wrapper-server]
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: wrapper-server-tools
              mountPath: /wrapper-server-tools
      volumes:
        - name: wrapper-server-tools
          emptyDir: {}
```

### Node Scheduling

Add node selectors and tolerations:

```yaml
data:
  template: |
    apiVersion: v1
    kind: Pod
    spec:
      nodeSelector:
        workload-type: functions
      tolerations:
        - key: "functions"
          operator: "Equal"
          value: "true"
          effect: "NoSchedule"
      initContainers:
        - name: copy-wrapper-server
          image: ghcr.io/kptdev/porch-wrapper-server:latest
          command: [cp, -a, /home/nonroot/wrapper-server/., /wrapper-server-tools]
          volumeMounts:
            - name: wrapper-server-tools
              mountPath: /wrapper-server-tools
      containers:
        - name: function
          image: to-be-replaced
          command: [/wrapper-server-tools/wrapper-server]
          volumeMounts:
            - name: wrapper-server-tools
              mountPath: /wrapper-server-tools
      volumes:
        - name: wrapper-server-tools
          emptyDir: {}
```

## Template Versioning

The Engine tracks the PodTemplate's `ResourceVersion` to detect template changes. When the template is updated:

1. The Engine detects the new version on the next pod creation
2. Existing pods with the old template version continue running
3. When an old pod is reused, the Engine detects the version mismatch
4. The old pod is deleted and a new pod is created with the updated template

This ensures zero-downtime template updates while maintaining cache efficiency.

## Default Template

When `base-pod-template` is missing, the Engine creates it from this inline default template:

```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    cluster-autoscaler.kubernetes.io/safe-to-evict: "true"
spec:
  initContainers:
    - name: copy-wrapper-server
      image: ${WRAPPER_SERVER_IMAGE}
      command: [cp, -a, /home/nonroot/wrapper-server/., /wrapper-server-tools]
      volumeMounts:
        - name: wrapper-server-tools
          mountPath: /wrapper-server-tools
  containers:
    - name: function
      image: to-be-replaced
      command: [/wrapper-server-tools/wrapper-server]
      env:
        - name: OTEL_METRICS_EXPORTER
          value: prometheus
        - name: OTEL_TRACES_EXPORTER
          value: none
        - name: OTEL_EXPORTER_PROMETHEUS_HOST
          value: 0.0.0.0
      readinessProbe:
        exec:
          command:
            - /wrapper-server-tools/grpc-health-probe
            - -addr
            - localhost:9446
      volumeMounts:
        - name: wrapper-server-tools
          mountPath: /wrapper-server-tools
  volumes:
    - name: wrapper-server-tools
      emptyDir: {}
```

## Troubleshooting

### Template Validation Errors

If the Engine fails to parse the template:

```bash
kubectl logs -n porch-system deployment/porch-server | grep "unable to decode"
```

Common issues:
- Invalid YAML syntax in the PodTemplate
- Missing required fields (function container)
- Incorrect indentation

If the Engine cannot read the template:

```bash
kubectl logs -n porch-system deployment/porch-server | grep "PodTemplate"
```

Common issues:
- Invalid YAML syntax in the PodTemplate
- Missing required `function` container
- Incorrect indentation

### RBAC Permission Errors

If porch-server cannot read `base-pod-template`:

```bash
kubectl logs -n porch-system deployment/porch-server | grep "PodTemplate"
```

Verify the `porch-function-executor` RoleBinding includes the `porch-server` (and `porch-controllers`) ServiceAccount.

### Pod Creation Failures

If function pods fail to start with custom templates:

```bash
kubectl get pods -n porch-fn-system
kubectl describe pod -n porch-fn-system <pod-name>
```

Check for:
- Resource quota violations
- Image pull errors
- Security policy violations
- Node selector mismatches
