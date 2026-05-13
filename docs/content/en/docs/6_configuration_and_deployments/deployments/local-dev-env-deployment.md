---
title: "Local Development Environment"
type: docs
weight: 3
description: "Set up a local development environment for Porch using Kind"
---

This guide provides instructions for setting up a local development environment using Kind (Kubernetes in Docker) for developing, testing, and exploring Porch.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) - For running containers and Kind cluster
- [kubectl](https://kubernetes.io/docs/tasks/tools/install-kubectl/) - Kubernetes command-line tool
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) - Local Kubernetes clusters using Docker

## Setup

### 1. Create Kind Cluster

From the Porch repository root directory:

```bash
./scripts/setup-dev-env.sh
```

This script:
- Creates a Kind cluster named `porch-test`
- Installs MetalLB load balancer
- Deploys Gitea Git server
- Generates PKI resources for testing
- Builds the `porchctl` CLI binary

### 2. Deploy Porch

Choose your cache backend:

**Option A: CR Cache (Default)**
```bash
make run-in-kind
```

**Option B: Database Cache**
```bash
make run-in-kind-db-cache
```

## Verification

### Check Pod Status

```bash
kubectl get pods -n porch-system
```

Expected output:
```
NAME                                 READY   STATUS    RESTARTS   AGE
function-runner-xxx-xxx              1/1     Running   0          2m
porch-controllers-xxx-xxx            1/1     Running   0          2m
porch-server-xxx-xxx                 1/1     Running   0          2m
```

### Verify API Resources

```bash
kubectl api-resources | grep porch
```

### Test porchctl CLI

```bash
# Add to PATH (optional)
export PATH="$(pwd)/.build:$PATH"

# Test CLI
porchctl version
```

## Access Services

### Gitea Git Server

- **URL**: http://localhost:3000
- **Username**: `porch`
- **Password**: `secret`

### Porch API

```bash
# Port forward to access Porch API
kubectl port-forward -n porch-system svc/api 8080:8080
```

## Clean Up

To restart from scratch:

```bash
kind delete cluster --name porch-test
./scripts/setup-dev-env.sh
```

## Next Steps

- Follow the [Getting Started tutorial]({{% relref "/docs/3_getting_started" %}}) to create your first packages
- See [Development Process]({{% relref "/docs/12_contributing" %}}) for contributing guidelines
