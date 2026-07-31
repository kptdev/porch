---
title: "Overview"
type: docs
weight: -1
description: >
  Porch is a Kubernetes-based system for managing the lifecycle of KRM configuration packages stored in Git repositories. It exposes package operations as Kubernetes resources, enabling GitOps workflows with approval gates, automation, and collaboration.
---

Porch (Package Orchestration Server) is part of the [kpt project](https://github.com/kptdev/kpt). The name "Porch" is short for "Package ORCHestration."

## Why you need Porch and what it can do

Managing configuration packages across multiple repositories and teams is challenging. You need to clone repositories, edit YAML files, run KRM functions, handle merge conflicts, and push changes - all while maintaining consistency, applying governance policies, and coordinating with GitOps deployment tools.

What if you could manage packages through a Kubernetes API instead? That's where Porch comes in.

Porch provides you with a Kubernetes-native way to manage [KRM configuration packages](https://kpt.dev/book/03-packages/). It handles Git operations, applies transformations through [KRM functions](https://kpt.dev/book/04-using-functions/), enforces approval workflows, and keeps everything synchronized - all through standard Kubernetes resources.

Porch provides you with:

* **Kubernetes-native package management** - Manage packages through Kubernetes resources (PackageRevision, Repository) instead of direct Git operations. Use Porch's `porchctl` CLI utility kubectl or alternatively client-go, or any Kubernetes tooling.

* **Approval workflows** - Packages move through lifecycle stages (Draft → Proposed → Published → DeletionProposed) with explicit approval gates. Prevent accidental publication of unreviewed changes.

* **Automatic package discovery** - Register a Git repository once, and Porch automatically discovers all packages within it. No manual inventory management.

* **Function execution** - Apply KRM functions to transform and validate packages. Functions run in isolated containers with results tracked in package history.

* **Package cloning and upgrades** - Clone packages from upstream sources and automatically upgrade them when new upstream versions are published. Three-way merge handles local customizations.

* **GitOps integration** - All changes are committed to Git with full history. Works seamlessly with [Flux](https://fluxcd.io/), [Config Sync](https://docs.cloud.google.com/kubernetes-engine/config-sync/docs/how-to/installing-config-sync) and other GitOps deployment tools.

* **Multi-repository orchestration** - Manage packages across multiple Git repositories from a single control plane. Controllers can automate cross-repository operations.

* **Collaboration and governance** - Multiple users and automation can work on packages concurrently. Draft revisions provide workspace isolation before publication.

* **Repository synchronization** - Porch detects changes made directly in Git (outside Porch) and synchronizes its cache. Supports Porch-managed packages.

{{% alert title="Note" color="primary" %}}
Making manual changes to the packages in Git without using Porch as the orchestrator is risky and not recommended.
{{% /alert %}}

* **Standard kpt packages** - Packages remain standard kpt packages. No vendor lock-in or Porch specific DSL "code" in kpt packages.

## What Porch is not

Porch is not a deployment tool. It manages packages in repositories but does not deploy them to clusters. Deployment is handled by GitOps tools such as Flux, ArgoCD, or Config Sync. Porch prepares and publishes packages to Git; those tools consume them from Git and deploy to clusters.

Porch also does not replace kpt. It uses kpt internally for package operations and function execution, acting as “kpt-as-a-service” by exposing a Kubernetes API on top of kpt’s capabilities.

Porch does not deploy packages. It manages package content and lifecycle in repositories. Deployment to clusters is done by separate GitOps tools that watch those repositories. Packages can be edited through Porch. While it is possible to manage packages directly in Git as well, doing so without using Porch as the orchestrator is risky and not recommended.

Porch does not provide a package registry. It works with your existing Git repositories to manage packages, rather than acting as a separate package storage system. It also does not enforce a specific repository structure; it works with standard kpt package layouts, and you keep control of how your repositories are organized.

## How Porch fits in the ecosystem

Porch is a key component in configuration-as-data workflows:

1. **Package authoring**: Users create and edit packages through Porch's Kubernetes API
2. **Package transformation**: KRM functions validate and transform package contents
3. **Package publication**: Approved packages are published to deployment repositories
4. **Package deployment**: The GitOps deployment system (Flux etc) deploys published packages to clusters
5. **Package automation**: Controllers (PackageVariant, PackageVariantSet) automate package operations

Porch sits between package authors and deployment tools, providing the orchestration layer that was previously handled by manual Git operations or custom scripts.

## Architecture

Porch consists of three main deployable components.

The **Porch Server** is a Kubernetes aggregated apiserver that serves the `porch.kpt.dev/v1alpha1` API PackageRevision, PackageRevisionResources, and Package resources. It includes the Engine (orchestration logic), the Cache (repository content), and Repository Adapters that abstract Git backends. As the architecture evolves toward the CRD-based model, the server will remain, and continue to serve PackageRevisionResources (PRR) package file content that can exceed etcd size limits and provides the v1alpha1 API for existing PackageRevisionResources (PRR) clients.

The **Function Runner** is a separate gRPC service that runs KRM functions in containers. It can execute both functions provided by Porch and externally developed function images.

The **Controllers** are a set of Kubernetes controllers that manage package lifecycle and automate operations:

- [**PackageRevision Controller**]({{% relref "/docs/5_architecture_and_components/controllers/packagerevision-controller" %}}) — manages PackageRevision custom resources (`porch.kpt.dev/v1alpha2`), handling package creation, rendering, and lifecycle transitions.
- [**Repository Controller**]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller" %}}) — synchronizes Repository custom resources with their backing Git repositories.
- [**PackageVariant Controllers**]({{% relref "/docs/5_architecture_and_components/controllers/packagevariants" %}}) — automate creation and management of package variants through declarative configuration.

The **Cache** is a storage backend used to cache repository content for performance. Porch supports a CR-based cache backed by Kubernetes custom resources, or a PostgreSQL-based cache for larger deployments.

{{% alert title="Architecture Direction" color="info" %}}
Porch is transitioning from an aggregated API model (where the API server orchestrates all operations) to a CRD-based controller model (where native Kubernetes CRDs and controllers handle package lifecycle). The aggregated API server remains for serving PackageRevisionResources content.
{{% /alert %}}
