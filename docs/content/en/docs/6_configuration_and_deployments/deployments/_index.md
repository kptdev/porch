---
title: "Deployments"
type: docs
weight: 2
description: "Deploy Porch in different environments"
---

This section covers deploying Porch in various environments, from local development to production clusters.

## Deployment Methods

### [Local Development Environment]({{% relref "local-dev-env-deployment" %}})
Set up Porch locally using Kind for development and testing. This includes:
- Automated Kind cluster setup
- Built-in Gitea for Git repositories
- Choice between CR cache or database cache
- Pre-configured development tools

### [Local Performance Monitoring Deployment]({{% relref "local-performance-monitoring-deployment" %}})
Deploy Prometheus, Grafana, Grafana Alloy, Pyroscope, and Jaeger against a local Kind cluster:
- Base deployment for Porch API, Resource Usage and PostgreSQL metrics visualized in Grafana dashboards
- Optional Jaeger deployment for distributed traces
- Optional Pyroscope and Grafana Alloy deployment for Go CPU, memory, goroutine, block, and mutex profiling

### [Catalog Deployment]({{% relref "catalog-deployment" %}})
Deploy Porch in production environments using the Nephio catalog:
- Production-ready configuration
- Integration with existing Kubernetes clusters
- Scalable deployment options

## Choosing Your Deployment

- **Use Local Development** when:
  - Developing or testing Porch functionality
  - Learning Porch concepts
  - Contributing to Porch development

- **Use Local Performance Monitoring** when:
  - Measuring API latency, resource usage, or package sizes
  - Running [performance tests]({{% relref "/docs/12_contributing/code-contribution/performance-tests" %}})
  - Inspecting traces or Go profiles locally

- **Use Catalog Deployment** when:
  - Deploying to production environments
  - Integrating with existing infrastructure
  - Requiring enterprise-grade reliability