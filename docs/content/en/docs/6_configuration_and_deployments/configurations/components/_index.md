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
- [Cert Manager Webhooks]({{% relref "porch-server-config/cert-manager-webhooks" %}}) - Webhook certificate management

### [Porch Controllers]({{% relref "porch-controllers-config" %}})
Manage the lifecycle of Repositories, PackageRevisions, PackageVariants, and PackageVariantSets.

### [Function Runner]({{% relref "function-runner-config" %}})
Executes KRM functions in isolated containers:
- [Private Registry Access]({{% relref "function-runner-config/private-registries-config" %}}) - Container registry authentication