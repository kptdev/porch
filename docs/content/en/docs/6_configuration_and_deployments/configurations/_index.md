---
title: "Configurations"
type: docs
weight: 1
description: "Configure Porch features and integrations"
---

This section covers configuring Porch's various features and integrations after deployment.

## Component Configuration

### [Components]({{% relref "components" %}})
Configure individual Porch components:
- [Porch Server]({{% relref "components/porch-server-config" %}}) - API server configuration
  - [Git Authentication]({{% relref "components/porch-server-config/git-authentication" %}}) - Git repository authentication
  - [Cert Manager Webhooks]({{% relref "components/porch-server-config/cert-manager-webhooks" %}}) - Webhook certificate management
- [Porch Controllers]({{% relref "components/porch-controllers-config" %}}) - Repository, PackageRevision, and variant controller settings
- [Function Runner]({{% relref "components/function-runner-config" %}}) - Function execution environment
  - [Private Registries]({{% relref "components/function-runner-config/private-registries-config" %}}) - Container registry authentication

### OTEL Metrics & Tracing

[OpenTelemetry]({{% relref "/docs/6_configuration_and_deployments/configurations/opentelemetry" %}}) - Tracing and metrics configuration

## Core Configuration Options

### [Cache]({{% relref "cache" %}})
Porch supports two caching mechanisms:
- **CR Cache** (default) - Uses Kubernetes Custom Resources
- **Database Cache** - Uses PostgreSQL for improved performance

## Advanced Integrations

### [Repository Synchronization]({{% relref "repository-sync" %}})
Configure Git repository synchronization with ConfigSync or other GitOps tools.

## Configuration Best Practices

- Start with default CR cache for simplicity
- Configure private registries only if using private KRM functions in Function Runner
- Enable tracing in development environments for debugging
- Use cert-manager for production TLS certificate management
- Set appropriate resource limits for each component
