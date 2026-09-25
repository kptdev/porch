---
title: "Package Cache"
type: docs
weight: 4
description: |
  Caching layer for repository and package data.
---

## What is the Package Cache?

The **Package Cache** is the intermediary layer between the CaD (configuration as data) Engine and external Git repositories. It maintains in-memory or database-backed representations of repositories, packages, and package revisions to improve performance and reduce load on external repository systems.

The cache is responsible for:

- **Repository Management**: Opening, closing, and tracking repository connections
- **Data Caching**: Storing repository metadata, package revisions, and package revision resources
- **Repository Abstraction**: Providing a unified interface regardless of storage backend (Git)
- **Connection Pooling**: Managing connections to external repositories efficiently
- **Change Detection**: Tracking repository versions to detect when updates are needed

## Role in the Architecture

The Package Cache sits between the CaD Engine and Repository Adapters:

```
   CaDEngine
       ↓
  Package Cache
       ↓
    ┌──┴────┐
    ↓       ↓
CR Cache  DB Cache 
    ↓       ↓
Repository Adapter
        ↓
External Repositories (Git)
```

**Key architectural responsibilities:**

1. **Performance Optimization**: Reduces latency by caching repository data in memory or a database rather than fetching from Git on every request

2. **Repository Lifecycle Management**: Opens repositories on first access, maintains repository connections while in use, closes repositories when no longer needed, and handles repository sharing when multiple Repository CRs point to the same Git repository.

3. **Abstraction Layer**: Provides a consistent `Cache` interface with two implementations:
   - **CR Cache**: Caches package data in-memory, stores PackageRev CR metadata in Kubernetes
   - **DB Cache**: Stores all package data and metadata in a PostgreSQL database

4. **Repository Adapter Integration**: Creates repository adapter instances (Git adapter), wraps adapters with caching logic, delegates actual Git operations to adapters, and caches adapter responses.

5. **Change Notification**: Sends watch events (Added/Modified/Deleted) when package revisions change. Enables real-time watch streams for API clients through the CaDEngine's WatcherManager. And propagates changes from direct operations and background synchronizations to
all active watchers.

**Cache selection:**

The cache implementation is selected at Porch server startup based on configuration. **CR Cache** is the default implementation. It
stores metadata as Kubernetes resources And **DB Cache** is and alternative implementation for larger deployments. It stores metadata in
PostgreSQL.

Both implementations provide the same interface and functionality, differing only in storage mechanism and scalability characteristics.

**Singleton pattern:**

The cache is instantiated once during Porch server initialization and shared across all operations. This ensures consistent view of
repository state, efficient resource usage and centralized synchronization control.
