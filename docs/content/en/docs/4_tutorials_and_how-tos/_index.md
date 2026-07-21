---
title: "Guides"
type: docs
weight: 4
description: Step-by-step guides for using Porch to manage configuration packages
---

## Overview

This section provides practical, step-by-step guides for using Porch. Each guide walks you through a specific workflow with commands, expected output, and troubleshooting tips.

## Prerequisites

Before following these guides, ensure you have:

- Porch installed on a Kubernetes cluster - [Installation Guide]({{% relref "/docs/3_getting_started/installing-porch" %}})
- porchctl CLI installed - [CLI Installation Guide]({{% relref "/docs/3_getting_started/installing-porchctl" %}})
- kubectl configured to access your cluster
- Basic familiarity with Kubernetes concepts

## Guide Categories

### Working with Repositories

Learn how to connect Porch to Git repositories where packages are stored:

- [Registering Repositories]({{% relref "working_with_porch_repositories/repository-registration" %}}) - Connect Porch to Git repositories
- [Basic Repository Usage]({{% relref "working_with_porch_repositories/repository-basic-usage" %}}) - List and inspect repositories
- [Repository Synchronization]({{% relref "working_with_porch_repositories/repository-synchronization" %}}) - Keep Porch in sync with Git
- [Unregistering Repositories]({{% relref "working_with_porch_repositories/repository-unregistration" %}}) - Remove repositories from Porch

### Working with PackageRevisions (v1alpha1)

Learn how to create and manage configuration packages:

- [Creating Packages]({{% relref "working_with_package_revisions/creating-packages" %}}) - Initialize new packages from scratch
- [Cloning Packages]({{% relref "working_with_package_revisions/cloning-packages" %}}) - Clone upstream packages for customization
- [Copying Packages]({{% relref "working_with_package_revisions/copying-packages" %}}) - Create new revisions of existing packages
- [Inspecting Packages]({{% relref "working_with_package_revisions/inspecting-packages" %}}) - View package contents and metadata
- [Upgrading Packages]({{% relref "working_with_package_revisions/upgrading-packages" %}}) - Update downstream packages to new upstream versions
- [Working with Subpackages]({{% relref "working_with_package_revisions/working-with-subpackages" %}}) - Clone and upgrade independent subpackages within a package
- [Deleting Packages]({{% relref "working_with_package_revisions/deleting-packages" %}}) - Remove packages from repositories

### CRD-Based PackageRevisions (v1alpha2)

Use the CRD-based controller architecture for Kubernetes-native package management:

- [Working with CRD-Based PackageRevisions]({{% relref "working_with_crd_based_packagerevisions" %}}) - Enable the PR Controller and manage packages as native CRDs

## Recommended Learning Path

If you're new to Porch, follow this sequence:

1. **Start with repositories**: Register a Git repository to store your packages
2. **Create your first package**: Initialize a simple package and walk through the lifecycle (Draft → Proposed → Published)
3. **Clone and customize**: Clone an existing package and modify it for your needs
4. **Explore other operations**: Try copying, upgrading, and deleting packages

## Getting Help

Each guide includes:

- **Prerequisites** - What you need before starting
- **Step-by-step instructions** - Commands with explanations
- **Verification steps** - How to confirm success
- **Troubleshooting** - Common issues and solutions

If you encounter issues not covered in the guides, see:

- [Architecture and Components]({{% relref "/docs/5_architecture_and_components" %}}) for deeper technical details
- [CLI Reference]({{% relref "/docs/7_cli_api" %}}) for complete command documentation
