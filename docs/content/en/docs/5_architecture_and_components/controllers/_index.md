---
title: "Porch Controllers"
type: docs
weight: 1
description: |
  Kubernetes controllers that automate Porch operations.
---

## Overview

Porch controllers are Kubernetes controllers that automate repository synchronization and package variant management. They run as a single deployment and can be individually enabled or disabled based on your needs.

## Architecture

All Porch controllers typically run together in a single pod and share the controller-runtime framework. They use standard Kubernetes controller patterns - watching resources, reconciling state, and requeuing when needed. You can enable or disable individual controllers using the `--reconcilers` flag, and they all act as clients of the Porch API server rather than being part of it.

While controllers share a pod by default, each controller can run in its own deployment if needed for scaling or isolation purposes, though this configuration is not covered in the documentation. See the [configuration guide]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-controllers-config.md" %}}) for standard deployment details.

## Available Controllers

### [Repository Controller]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller/_index.md" %}})

Synchronizes Repository custom resources with their backing Git repositories. The controller performs health checks and full syncs to keep package metadata current in Porch's cache layer.

### [PackageRevision Controller]({{% relref "/docs/5_architecture_and_components/controllers/packagerevision-controller/_index.md" %}})

Manages the full lifecycle of PackageRevisions as native Kubernetes CRDs. Handles package creation (init, clone, copy, upgrade), KRM function rendering, and lifecycle transitions. This is the core controller for the CRD-based architecture.

### [PackageVariant Controllers]({{% relref "/docs/5_architecture_and_components/controllers/packagevariants/_index.md" %}})

Automate creation and management of package variants through declarative configuration. Two controllers work together:

- **PackageVariant Controller**: Manages individual package variants with one-to-one upstream-to-downstream relationships
- **PackageVariantSet Controller**: Generates multiple PackageVariant resources based on target selectors for bulk operations
