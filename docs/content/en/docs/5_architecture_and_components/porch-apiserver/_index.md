---
title: "Porch API Server"
type: docs
weight: 2
description: |
  The Kubernetes extension API server that provides Porch's API resources.
---

## What is the Porch API Server?

The **Porch API Server** is a Kubernetes extension API server that exposes Porch's aggregated API resources (PackageRevision, PackageRevisionResources, Package) and CRDs (Repository, PackageRev) through the Kubernetes API. It acts as the entry point for client interactions with Porch, translating Kubernetes API requests into operations on the underlying Engine and Cache.

> The PackageVariant and PackageVariantSet CRDs are not controlled by the Porch API server. They are controlled by the [Porch Controllers]({{% relref "/docs/5_architecture_and_components/controllers/_index.md" %}})

The Porch API Server is responsible for:

- **API Resource Registration**: Registering Porch's Kubernetes resources with the Kubernetes API aggregation layer
- **Request Handling**: Processing CRUD operations (Create, Read, Update, Delete) and Watch requests for those resources
- **REST Storage Implementation**: Interacting with the Porch [Cad Engine]({{% relref "/docs/5_architecture_and_components/engine/_index.md" %}}) to provide storage backends for each API resource type
- **Validation and Admission**: Enforcing API validation rules and admission policies through strategies
- **Authentication and Authorization**: Integrating with Kubernetes RBAC for access control
- **Watch Stream Management**: Supporting real-time watch streams for resource changes
- **Background Operations**: Running maintenance tasks such as resource cleanup

## Role in the Architecture

The Porch API Server sits at the top of the Porch architecture, serving as the interface between Kubernetes clients and Porch's internal components:

```
┌────────────────────────────────────────────────────────────┐
│              Porch API Server                              │
│                                                            │
│  ┌──────────────────┐      ┌────────────────────────────┐  │
│  │   API Groups     │      │   REST Storage             │  │          
│  │                  │ ───> │   (Aggregated)             │  │          
│  │  • porch.kpt.dev │      │  • PackageRevision         │  │        
│  │                  │      │  • PackageRevisionResources│  │         
│  │                  │      │  • Package                 │  │          
│  └──────────────────┘      └────────────────────────────┘  │
│           │                         │                      │
│           │                         │                      │
│  ┌──────────────────┐               │                      │
│  │   CRDs (etcd)    │               │                      │
│  │                  │               │                      │
│  │  • Repository    │               │                      │
│  │  • PackageRev    │               │                      │
│  └──────────────────┘               │                      │
│           │                         │                      │
│           └────────┬────────────────┘                      │
│                    ↓                                       │
│  ┌──────────────────┐      ┌──────────────────┐            │
│  │   Strategies     │      │   Background     │            │
│  │                  │      │   Jobs           │            │
│  │  • Validation    │      │                  │            │
│  │  • Admission     │      │  • Cleanup       │            │
│  │  • Table Conv    │      │                  │            │
│  └──────────────────┘      └──────────────────┘            │
└────────────────────────────────────────────────────────────┘
                    ↓
         ┌──────────────────────┐
         │   CaD Engine         │
         │   (Orchestration)    │
         └──────────────────────┘
                    ↓
         ┌──────────────────────┐
         │   Package Cache      │
         │   (Repository Data)  │
         └──────────────────────┘
```

**Key architectural responsibilities:**

1. **Kubernetes API Integration**: Extends Kubernetes API with Porch-specific resources using API aggregation
2. **Request Translation**: Converts Kubernetes API requests into Engine operations (CreatePackageRevision, UpdatePackageRevision, etc.)
3. **Storage Abstraction**: Implements Kubernetes storage interface (REST storage) that delegates to Engine rather than etcd
4. **Strategy Pattern**: Uses Kubernetes strategy pattern for validation, admission control, and table conversion
5. **Watch Support**: Provides real-time watch streams by integrating with Engine's WatcherManager
6. **Background Processing**: Runs periodic tasks for resource cleanup
7. **Multi-Tenancy**: Enforces namespace isolation and RBAC policies for secure multi-tenant operation

{{% alert title="Note" color="primary" %}}
Repository synchronization and lifecycle management are handled by the separate [Repository Controller]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller/_index.md" %}}), not by the API Server. This separation improves scalability and allows the API Server to focus on serving API requests.
{{% /alert %}}

The API Server is instantiated once during Porch startup and configured with Engine, Cache, and client connections through dependency injection.
