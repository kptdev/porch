---
title: "Pod Templates"
type: docs
weight: 2
description: "Customize function evaluator pod specifications using ConfigMap templates"
---

The Function Runner supports customizing the pod specifications used for KRM function evaluation through ConfigMap-based templates. This allows you to configure resource limits, security contexts, node selectors, tolerations, and other pod-level settings for function execution pods.

## Overview

By default, the Function Runner uses an inline pod template with sensible defaults. For advanced use cases requiring customization, you can provide a ConfigMap containing custom pod and service templates. The Function Runner will use these templates when creating function evaluator pods.

The pod template system provides:
- **Resource customization** - Configure CPU/memory limits for function pods
- **Security hardening** - Apply security contexts and pod security standards
- **Scheduling control** - Add node selectors, affinity rules, and tolerations
- **Network policies** - Customize service specifications for service mesh integration
- **Volume management** - Add additional volumes and volume mounts

For architectural details on how pod templates are used in the pod lifecycle, see [Pod Lifecycle Management]({{% relref "/docs/5_architecture_and_components/function-runner/functionality/pod-lifecycle-management.md" %}}).

## Template Contract

Any custom pod template must fulfill the following requirements:

1. **Function container** - Must contain a container named `function`
2. **Wrapper server entrypoint** - The `function` container's entrypoint must start the wrapper gRPC server
3. **Image replacement** - The `function` container's image can be set to any KRM function image without breaking the wrapper server entrypoint
4. **Entrypoint arguments** - The `function` container's args can be appended with entries from the function image's Dockerfile ENTRYPOINT

The Function Runner automatically patches the template with function-specific configuration before creating pods.

## Enabling Pod Templates

### Step 1: Configure RBAC

The Function Runner requires read access to the pod template ConfigMap. Create a Role and RoleBinding in the Function Runner's namespace:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: porch-fn-runner-configmap-reader
  namespace: porch-system
rules:
  - apiGroups: [""]
    resources: ["configmaps"]
    resourceNames: ["kpt-function-eval-pod-template"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: porch-fn-runner-configmap-reader
  namespace: porch-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: porch-fn-runner-configmap-reader
subjects:
  - kind: ServiceAccount
    name: porch-fn-runner
    namespace: porch-system
```

### Step 2: Configure Function Runner

Add the `--function-pod-template` argument to the Function Runner deployment, specifying the ConfigMap name:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: function-runner
  namespace: porch-system
spec:
  template:
    spec:
      serviceAccountName: porch-fn-runner
      containers:
        - name: function-runner
          image: ghcr.io/kptdev/porch-function-runner:latest
          args:
            - --port=9445
            - --pod-namespace=porch-fn-system
            - --function-pod-template=kpt-function-eval-pod-template
          env:
            - name: WRAPPER_SERVER_IMAGE
              value: ghcr.io/kptdev/porch-wrapper-server:latest
```

### Step 3: Create the ConfigMap

Create a ConfigMap in the same namespace where the Function Runner is deployed (typically `porch-system`). The ConfigMap must contain two keys:

- `template` - Pod specification in YAML format
- `serviceTemplate` - Service specification in YAML format

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kpt-function-eval-pod-template
  namespace: porch-system
data:
  template: |
    apiVersion: v1
    kind: Pod
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
  serviceTemplate: |
    apiVersion: v1
    kind: Service
    spec:
      type: ClusterIP
      ports:
      - port: 9446
        protocol: TCP
        targetPort: 9446
      selector:
        fn.kpt.dev/image: to-be-replaced
```

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

The Function Runner tracks the ConfigMap's `ResourceVersion` to detect template changes. When the ConfigMap is updated:

1. The Function Runner detects the new version on the next pod creation
2. Existing pods with the old template version continue running
3. When an old pod is reused, the Function Runner detects the version mismatch
4. The old pod is deleted and a new pod is created with the updated template

This ensures zero-downtime template updates while maintaining cache efficiency.

## Default Template

When no ConfigMap is specified, the Function Runner uses this inline default template:

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

If the Function Runner fails to parse the template:

```bash
kubectl logs -n porch-system deployment/function-runner | grep "unable to decode"
```

Common issues:
- Invalid YAML syntax in the ConfigMap
- Missing required `function` container
- Incorrect indentation

### RBAC Permission Errors

If the Function Runner cannot read the ConfigMap:

```bash
kubectl logs -n porch-system deployment/function-runner | grep "Could not get Configmap"
```

Verify the Role and RoleBinding are correctly configured and the ServiceAccount name matches.

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
