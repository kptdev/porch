---
title: "Components"
type: docs
weight: 1
description: "Configure individual Porch components"
---

Configure each Porch component individually for optimal performance and security. All configurations are optional - Porch works with default settings.

## Core Components

### [Porch Server]({{% relref "porch-server-config" %}})
The main API server that handles package operations and Git repository interactions:
- [Git Authentication]({{% relref "porch-server-config/git-authentication" %}}) - Repository authentication methods

### [Porch Controllers]({{% relref "porch-controllers-config" %}})
Manage the lifecycle of Repositories, PackageRevisions, PackageVariants, and PackageVariantSets:
- [Webhooks]({{% relref "porch-webhooks" %}}) - Validation webhooks for PackageRevision and Repository resources
  - [Certificate Management]({{% relref "porch-webhooks/cert-manager-webhooks" %}}) - TLS certificate setup
  - [Validation Rules]({{% relref "porch-webhooks/validation-rules" %}}) - Detailed validation rules

### [Function Runner]({{% relref "function-runner-config" %}})
Executes KRM functions in isolated containers:
- [Private Registry Access]({{% relref "function-runner-config/private-registries-config" %}}) - Container registry authentication