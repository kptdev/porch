---
title: "Architecture & Components"
type: docs
weight: 5
description: Porch's internal architecture and component design
---

This section provides detailed documentation of Porch's internal architecture, explaining how its components work together to manage KRM configuration packages.

## Overview

Porch has a modular architecture. Components are shared across two orchestration paths: the original aggregated API model and the newer CRD-based controller model. The CRD-based model is the active development focus.

## Core Components

### [Porch Controllers]({{% relref "controllers" %}})

Kubernetes controllers that manage package lifecycle and automate operations:
- **[PackageRevision Controller]({{% relref "controllers/packagerevision-controller" %}})**: Manages PackageRevision CRDs (`porch.kpt.dev/v1alpha2`) — creation, rendering, lifecycle transitions. The primary orchestration path for new deployments.
- **[Repository Controller]({{% relref "controllers/repository-controller" %}})**: Synchronizes Repository CRs with Git, populates the shared cache
- **[PackageVariant Controllers]({{% relref "controllers/packagevariants" %}})**: Automate package variant creation and management

### [Porch API Server]({{% relref "porch-apiserver" %}})

The Kubernetes aggregated API server that serves `porch.kpt.dev/v1alpha1`:
- PackageRevision, PackageRevisionResources (PRR), and Package resources
- Handles CRUD operations and watch requests for v1alpha1 clients
- Integrates with the Engine and Cache for orchestration and content access
- Serves PRR for both v1alpha1 and v1alpha2 workflows (content access)

### [Engine]({{% relref "engine" %}})

The Configuration as Data (CaD) Engine:
- In the v1alpha1 path: orchestrates full package lifecycle (creation, tasks, rendering, lifecycle transitions)
- In the v1alpha2 path: provides content read/write for PackageRevisionResources only
- Enforces validation rules and business constraints for v1alpha1 operations

### [Package Cache]({{% relref "package-cache" %}})

The shared caching layer between controllers/Engine and Git repositories:
- **CR Cache**: Stores metadata as Kubernetes custom resources
- **DB Cache**: Stores metadata in PostgreSQL for larger deployments
- Manages repository connections and Git interaction
- Shared by both the API Server/Engine and the PR Controller

### [Function Runner]({{% relref "function-runner" %}})

A standalone gRPC service for executing KRM functions:
- Runs functions in isolated containers or as builtin Go executables
- Manages pod lifecycle with caching and garbage collection
- Used by both the Engine (v1alpha1 renders) and the PR Controller (v1alpha2 renders)

## Data Paths

Porch supports two orchestration paths. Both use the same shared cache, function runner, and Git storage format.

### CRD-Based Controller Path (v1alpha2)

The recommended path for new deployments. PackageRevision is a native CRD in etcd, reconciled asynchronously by the PR Controller. This path requires the `porch.kpt.dev/v1alpha2-migration: "true"` annotation on the Repository CRD.

```
User (kubectl)
    │
    ▼
PackageRevision CRD (etcd)          PackageRevisionResources (PRR)
    │                                        │
    │ watch                                  │ aggregated API
    ▼                                        ▼
PR Controller ──────────────────────► Porch API Server / Engine
    │                                        │
    │ read/write                             │ read/write
    ▼                                        ▼
┌──────────────────────────────────────────────────────────┐
│                    Shared Cache                          │
│              (populated by Repo Controller)              │
└──────────────────────────────────────────────────────────┘
    │                         ▲
    │ git ops                 │ sync
    ▼                         │
┌─────────┐          ┌───────────────────┐
│   Git   │◄─────────│ Repository Ctr    │
└─────────┘          └───────────────────┘
```

- Users write desired state to the CRD; the PR Controller makes it real
- Operations are asynchronous — observe progress via status conditions
- PRR content access still flows through the API Server and Engine
- Standard Kubernetes RBAC, watches, field selectors
- Repository must be annotated with `porch.kpt.dev/v1alpha2-migration: "true"` to enable CRD creation

### Aggregated API Path (v1alpha1)

The original architecture. PackageRevision is served by the API Server with custom REST storage. Operations are synchronous within the request path.

```
User (kubectl / porchctl)
    │
    ▼
Porch API Server (aggregated API)
    │
    ▼
Engine (orchestration)
    │
    ▼
Shared Cache ──────► Git
```

- The API Server is the single orchestration point
- Requests block until the operation completes (write to Git, render)

Both paths coexist in the same cluster and share the same Git repositories

## Design Principles

**Separation of Concerns**: Each component has a well-defined responsibility. The cache owns Git interaction. The function runner owns function execution. Controllers own reconciliation logic.

**Shared Infrastructure**: Both orchestration paths share the cache, function runner, Git storage format, and lifecycle semantics. A package created via either path looks the same in Git.

**Extensibility**: Cache implementations (CR, DB) are interchangeable. Function evaluators support multiple execution strategies. Repository adapters abstract storage backends.
