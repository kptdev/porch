---
title: "Private Registries"
type: docs
weight: 4
description: "Configure Function Runner access to private container registries"
---

{{% alert title="Note" color="primary" %}}
KPT functions and KRM functions are synonymous terms referring to the same containerized functions.
{{% /alert %}}

Configure the Function Runner to access private container registries for KRM functions.

## Use Cases

Private registries are commonly used for:
- **Enterprise environments** - Internal Harbor or JFrog registries
- **Cloud providers** - GitHub Container Registry (GHCR), AWS ECR, Azure ACR
- **Custom functions** - Organization-specific KRM functions

## Default Public Registries

By default, Function Runner uses public registries:
- `ghcr.io/kptdev/krm-functions-catalog` - GitHub Container Registry for KRM functions
- Other public registries as configured

## Private Registry Authentication

To use private container registries for KRM functions, configure authentication in the Function Runner.

### 1. Create Docker Configuration Secret

Create a secret using Docker configuration format:

{{% alert title="Note" color="primary" %}}
The secret must be in the same namespace as the function runner deployment. By default, this is the *porch-system* namespace.
{{% /alert %}}

```bash
kubectl create secret generic registry-auth-secret \
  --from-file=.dockerconfigjson=/path/to/your/config.json \
  --type=kubernetes.io/dockerconfigjson \
  --namespace=porch-system
```

Example `config.json` format:

```json
{
    "auths": {
        "https://index.docker.io/v1/": {
            "auth": "bXlfdXNlcm5hbWU6bXlfcGFzc3dvcmQ="
        },
        "ghcr.io": {
            "auth": "bXlfdXNlcm5hbWU6bXlfcGFzc3dvcmQ="
        }
    }
}
```

The `auth` value is base64 encoded `username:password`.

### 2. Mount Secret in Function Runner

Update the Function Runner deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: function-runner
  namespace: porch-system
spec:
  template:
    spec:
      containers:
      - name: function-runner
        args:
        - --enable-private-registries=true
        - --registry-auth-secret-path=/var/tmp/auth-secret/.dockerconfigjson
        - --registry-auth-secret-name=registry-auth-secret
        volumeMounts:
        - name: docker-config
          mountPath: /var/tmp/auth-secret
          readOnly: true
      volumes:
      - name: docker-config
        secret:
          secretName: registry-auth-secret
```

### 3. Configuration Arguments

Required Function Runner arguments:
- `--enable-private-registries=true` - Enable private registry functionality
- `--registry-auth-secret-path` - Path to mounted secret (default: `/var/tmp/auth-secret/.dockerconfigjson`)
- `--registry-auth-secret-name` - Name of the secret (default: `auth-secret`)

{{% alert title="Note" color="primary" %}}
Use dedicated subdirectories for mount paths to avoid overwriting directory permissions. For example, use `/var/tmp/auth-secret` instead of `/var/tmp`.
{{% /alert %}}

## How It Works

When configured, the Function Runner:
1. Replicates the registry secret to the `porch-fn-system` namespace
2. Uses it as an `imagePullSecret` for KRM function pods
3. Enables function pods to pull images from private registries

## TLS Configuration for Private Registries

For registries with custom TLS certificates:

### 1. Create TLS Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: registry-tls-secret
  namespace: porch-system
data:
  ca.crt: <base64-encoded-pem-certificate>
type: kubernetes.io/tls
```

{{% alert title="Note" color="primary" %}}
The certificate must be in PEM format and the key must be named one of the following:
- `ca.crt`
- `ca.pem`
- `cacert.pem`
- `ca-bundle.crt`
- `root.crt`
{{% /alert %}}

### 2. Mount TLS Secret

```yaml
spec:
  template:
    spec:
      containers:
      - name: function-runner
        args:
        - --enable-private-registries-tls=true
        - --tls-secret-path=/var/tmp/tls-secret/
        volumeMounts:
        - name: tls-registry-config
          mountPath: /var/tmp/tls-secret/
          readOnly: true
      volumes:
      - name: tls-registry-config
        secret:
          secretName: registry-tls-secret
```

### 3. TLS Configuration Arguments

Additional arguments for TLS:
- `--enable-private-registries-tls=true` - Enable TLS for private registries
- `--tls-secret-path` - Path to TLS certificate (default: `/var/tmp/tls-secret/`)

## TLS Connection Logic

When TLS is enabled, Function Runner attempts connection in this order:
1. Using the mounted TLS certificate
2. Using system intermediate certificates (for well-known CAs)
3. Without TLS as fallback
4. Returns error if all attempts fail

{{% alert title="Warning" color="warning" %}}
Ensure Kubernetes nodes are configured with the same TLS certificate information. The Function Runner can pull images, but KRM function pods need node-level certificate configuration to run successfully.
{{% /alert %}}

## Complete Example

Combining both authentication and TLS:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: function-runner
  namespace: porch-system
spec:
  template:
    spec:
      containers:
      - name: function-runner
        args:
        - --enable-private-registries=true
        - --registry-auth-secret-path=/var/tmp/auth-secret/.dockerconfigjson
        - --registry-auth-secret-name=registry-auth-secret
        - --enable-private-registries-tls=true
        - --tls-secret-path=/var/tmp/tls-secret/
        volumeMounts:
        - name: docker-config
          mountPath: /var/tmp/auth-secret
          readOnly: true
        - name: tls-registry-config
          mountPath: /var/tmp/tls-secret/
          readOnly: true
      volumes:
      - name: docker-config
        secret:
          secretName: registry-auth-secret
      - name: tls-registry-config
        secret:
          secretName: registry-tls-secret
```