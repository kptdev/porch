---
title: "Installation"
type: docs
weight: 3
description: "A set of guides for installing Porch prerequisites, the porchctl CLI, and deploying Porch components on a Kubernetes cluster." 
---

## Prerequisites

Before installing Porch, ensure you have:

1. **A Kubernetes cluster** - Any of the following:
   - Local cluster: [kind](https://kind.sigs.k8s.io/) (recommended for testing, **requires Docker**), [minikube](https://minikube.sigs.k8s.io/) (requires Docker or other driver), or similar
   - Cloud cluster: GKE, EKS, AKS, or any managed Kubernetes service
   - Bare-metal cluster with standard Kubernetes installation
   - Minimum version: Kubernetes {{< params "version_kube" >}}

2. **Required tools**:
   - [kubectl](https://kubernetes.io/docs/reference/kubectl/) - configured with access to your cluster ({{< params "version_kube" >}})
   - [kpt](https://kpt.dev/installation/kpt-cli/) - for deploying the Porch package ({{< params "version_kpt" >}})
   - [git](https://git-scm.com/) - for repository operations ({{< params "version_git" >}})
   - [Docker](https://www.docker.com/get-started/) - **if using kind or minikube with Docker driver** ({{< params "version_docker" >}})

3. **Network access**:
   - Internet connectivity to pull container images from GitHub Container Registry
   - Or pre-pulled images available in your cluster's container registry

4. **Optional tools** (only needed for building Porch from source or development):
   - [Go programming language](https://go.dev/) ({{< params "version_go" >}})

## Version Compatibility

The versions listed above are the latest tested versions. Before proceeding, review the [Compatibility & Requirements]({{% relref "/docs/3_getting_started/compatibility" %}}) page to:

- Verify your Kubernetes cluster version matches minimum requirements
- Check compatibility between Porch server and API module versions
- Confirm Go version support if building from source

## Next Steps

Once you have the prerequisites, see [Tutorials and How-Tos]({{% relref "/docs/4_tutorials_and_how-tos" %}}) to learn how to use Porch.
