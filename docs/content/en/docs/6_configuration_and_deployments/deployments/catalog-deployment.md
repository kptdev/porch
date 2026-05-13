---
title: "Catalog Deployment"
type: docs
weight: 2
description: "Deploy Porch using the Nephio catalog for production environments"
---

This guide covers deploying Porch in production environments using the [Nephio catalog](https://github.com/nephio-project/catalog/tree/main/nephio/core/porch).

## Configuration Planning

Before deploying Porch, determine which features you need.

### Cache Mode Selection

Choose your cache backend based on deployment scale and requirements:

- **CR Cache** (default): Development and small deployments (<100 repositories)
- **DB Cache**: Production deployments requiring scale and reliability

{{% alert title="Important" color="warning" %}}
If using **DB Cache**, you must configure database settings for **both** Porch Server and Repository Controller before deployment. See [Cache Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/cache" %}}) for complete setup instructions including database initialization.
{{% /alert %}}

### Optional Pre-deployment Configuration

These **optional** features must be configured **before** deployment if you need them:

#### Porch Server
- [Cert-Manager Webhooks]({{% relref "../configurations/components/porch-server-config/cert-manager-webhooks" %}}) - Enable cert-manager webhook integration (requires deployment env vars)
- [Jaeger Tracing]({{% relref "../configurations/components/porch-server-config/jaeger-tracing" %}}) - Enable distributed tracing (requires deployment env vars)
- [Git Custom TLS]({{% relref "../configurations/components/porch-server-config/git-authentication#3-httpstls-configuration" %}}) - Enable custom TLS certificates for Git repositories (requires `--use-git-cabundle=true` arg)

#### Function Runner
- [Private Registries]({{% relref "../configurations/components/function-runner-config/private-registries-config" %}}) - Configure private container registries (requires deployment args and volume mounts)

### Post-deployment Configuration

These features can be configured **after** deployment:

- [Git Authentication]({{% relref "../configurations/components/porch-server-config/git-authentication" %}}) - Configure Porch Server authentication for private Git repositories

{{% alert title="Note" color="primary" %}}
[Repository Sync]({{% relref "../configurations/repository-sync" %}}) configuration is currently located in the system configuration section but should be moved to a more logical location as it's about configuring individual Repository resources, not system-wide settings.
{{% /alert %}}

## Prerequisites

- Kubernetes cluster (v1.25+)
- [kubectl](https://kubernetes.io/docs/tasks/tools/install-kubectl/) configured for your cluster
- [kpt](https://kpt.dev/installation/) CLI tool
- Cluster admin permissions

## Installation Steps

### 1. Get the Porch Package

```bash
kpt pkg get https://github.com/nephio-project/catalog/tree/main/nephio/core/porch
```

### 2. Customize Configuration (Optional)

If you need any pre-deployment features from the [Configuration Planning](#configuration-planning) section above, modify the package now:

```bash
cd porch/

# Example: Configure database cache for Porch Server
kpt fn eval --image gcr.io/kpt-fn/set-annotations:v0.1 -- \
  annotations='cache-type=DB'

# Review your changes
kpt pkg tree
```

### 3. Render and Apply

```bash
# Render the package with any customizations
kpt fn render porch

# Initialize the package for lifecycle management
kpt live init porch

# Apply to your cluster
kpt live apply porch
```

## Verification

### Check Pod Status

Verify all Porch components are running:

```bash
kubectl get pods -n porch-system
```

Expected output:
```
NAME                                 READY   STATUS    RESTARTS   AGE
function-runner-xxx-xxx              1/1     Running   0          2m
function-runner-xxx-xxx              1/1     Running   0          2m
porch-controllers-xxx-xxx            1/1     Running   0          2m
porch-server-xxx-xxx                 1/1     Running   0          2m
```

### Verify API Resources

Confirm Porch CRDs are registered:

```bash
kubectl api-resources | grep porch
```


## Troubleshooting

### Common Issues

**Pods not starting:**
```bash
kubectl describe pods -n porch-system
kubectl logs -n porch-system -l app=porch-server
```

**CRDs not registered:**
```bash
kubectl get crd | grep porch
```

### Getting Help

For additional support:
- Check the [Porch GitHub issues](https://github.com/kptdev/porch/issues)
- Join the [kpt community](https://kpt.dev/)
