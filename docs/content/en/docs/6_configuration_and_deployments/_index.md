---
title: "Configuration & Deployments"
type: docs
weight: 6
description: "Configuring and deploying Porch in different environments"
---

This section covers deploying Porch in various environments and configuring its features for optimal operation.

## 1. Choose Your Deployment

Start by selecting the appropriate deployment method for your environment:

- **[Local Development]({{% relref "deployments/local-dev-env-deployment" %}})** - Kind-based setup for development and testing
- **[Local Performance Monitoring]({{% relref "deployments/local-performance-monitoring-deployment" %}})** - Prometheus, Grafana, Grafana Alloy, Pyroscope and Jaeger for kind local clusters
- **[Catalog Deployment]({{% relref "deployments/catalog-deployment" %}})** - Production deployment with pre/post-deployment configuration planning

## 2. Configure Components (Optional)

After deployment, configure individual components as needed:

- **[Component Configurations]({{% relref "configurations/components" %}})** - Porch Server, Function Runner, and Controllers
