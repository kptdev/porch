---
title: "CLI / API Reference"
type: docs
weight: 7
description: Command-line and API reference documentation for Porch
---

## Overview

This section provides reference documentation for interacting with Porch through its command-line interface (CLI) or through the Kubernetes API.

## CLI Reference

The `porchctl` command-line tool provides commands for managing Porch repositories and packages. It communicates with the Porch API server to perform operations like:

- Registering and managing Git repositories
- Creating, cloning, and copying packages
- Managing package lifecycle from creation to deletion (Draft → Proposed → Published -> Deletion-Proposed)
- Pulling and pushing package content
- Upgrading downstream packages

**Quick Links:**

- [Installation]({{% relref "/docs/3_getting_started/installing-porchctl" %}})
- [repo commands]({{% relref "porchctl#repo" %}}) - Repository management
- [rpkg commands]({{% relref "porchctl#rpkg" %}}) - Package management

## API Reference

### Generate OpenAPI Specification

You can generate an OpenAPI specification for Porch's APIs and explore them interactively:

{{% alert title="Note" color="primary" %}}
The OpenAPI spec reflects your cluster's Porch installation. Make sure you're connected to a cluster with Porch installed when running the kubectl commands. You can follow the [Porch Install Guide]({{% relref "/docs/3_getting_started/_index.md" %}}) to deploy porch on a cluster
{{% /alert %}}

**1. Extract OpenAPI specs from your cluster:**

```bash
# Get config.porch.kpt.dev API spec
kubectl get --raw /openapi/v3/apis/config.porch.kpt.dev/v1alpha1 | jq '.' > config-porch-openapi.json

# Get porch.kpt.dev API spec
kubectl get --raw /openapi/v3/apis/porch.kpt.dev/v1alpha1 | jq '.' > porch-openapi.json

# Combine both specs into single file
jq -s '.[0] * .[1]' config-porch-openapi.json porch-openapi.json > combined-porch-openapi.json
```

**2. Explore the API interactively:**

Open [Swagger Editor](https://editor.swagger.io/) in your browser and import the `combined-porch-openapi.json` file to:

- Browse all API endpoints and operations
- View request/response schemas
- Test API calls directly from the browser
- Generate client code in various languages

### API Reference Guide

The complete field-level reference for Porch's Aggregated API Resources and Kubernetes Custom Resource Definitions (CRDs):

{{% alert title="Note" color="primary" %}}
The `porch.kpt.dev/v1alpha1` resources are Aggregated API Resources (not Custom Resources defined in a CRD) whilst the `config.porch.kpt.dev/v1alpha1` resources are Custom Resources.
{{% /alert %}}

The following lists provide a summary of the resources documented in the generated API and CRD guide:

**porch.kpt.dev/v1alpha1:**

- **PackageRevision** - Represents a specific revision of a package with metadata and lifecycle state
- **PackageRevisionResources** - Contains a list of the files in a package revision, there is one PackageRevisionResources resource for each PackageRevision resource. GET may include `?file=<path>` (repeatable) to return only those files. UPDATE may include `?partial=true` to merge submitted files instead of replacing the package. See [Reading Selected Package Files]({{% relref "/docs/4_tutorials_and_how-tos/working_with_package_revisions/inspecting-packages.md#reading-selected-package-files" %}}) and [Partial Package Content Updates]({{% relref "/docs/4_tutorials_and_how-tos/working_with_package_revisions/inspecting-packages.md#partial-package-content-updates" %}}).
- **PorchPackage** - Represents a package in a repository, with one or more PackageRevision resources in each package

**config.porch.kpt.dev/v1alpha1:**

- **Repository** - Connects Porch to Git repositories
- **PackageVariant** - Automates package cloning and updates
- **PackageVariantSet** - Manages sets of package variants

The API reference provides detailed information about:

- Field names, types, and descriptions
- Validation rules and constraints
- Nested object structures
- Enum values and options

**Quick Links:**

- [Aggregated API Resources]({{% relref "api-ref" %}})
- [Custom Resources]({{% relref "crd-ref" %}})

## When to Use CLI vs API

### Use porchctl CLI when:
- Performing interactive operations
- Quick one-off tasks
- Learning Porch workflows
- Debugging package issues
- Manual package management

### Use Kubernetes API (kubectl) when:
- Automating operations in scripts
- Integrating with CI/CD pipelines
- Building custom controllers
- Managing resources declaratively
- Applying GitOps patterns

### Using Either:
Most Porch operations can be performed with either `porchctl` or `kubectl`. For example:

**With porchctl:**
```bash
porchctl rpkg get --namespace default
```

**With kubectl:**
```bash
kubectl get packagerevisions -n default
```

Choose the tool that best fits your workflow and automation needs.
