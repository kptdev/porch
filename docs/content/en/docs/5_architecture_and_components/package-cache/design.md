---
title: "Cache Design"
type: docs
weight: 2
description: |
  Cache architecture and implementation patterns.
---

## Cache Interface

The Package Cache provides a unified interface that abstracts the underlying storage mechanism. Both the CR Cache and DB Cache implementations conform to this common interface, allowing the CaDEngine to work with either implementation without knowing which is in use.

**Repository Operations:** Open repository connections from Repository CR specifications, close repositories and clean up resources, retrieve list of all open repositories, get specific repository by key, update repository metadata, and check repository connectivity.

**Repository Interface Delegation:**

Once a repository is opened through the cache, the returned Repository interface provides:

**Package Revision Operations:** List package revisions with filtering (repository, package, workspace, lifecycle), create package revision drafts for modifications, update existing package revisions, finalize drafts to transition package revision lifecycle, and delete package revisions from repositories.

**Package Operations:** List packages with filtering by repository and path, create new packages in repositories, and delete packages and all their revisions.

**Repository Metadata:** Get repository version for change detection, refresh repository state from external sources, and close and clean up repository resources.

The interface design follows the Strategy Pattern, where different caching strategies (CR-based or database-backed) can be swapped without affecting the CaDEngine's operation. This abstraction enables deployment flexibility (choose cache based on scale requirements), testing with mock implementations, future cache implementations without CaDEngine changes, and consistent behavior regardless of storage backend.

## Factory Pattern

The Package Cache uses a Factory Pattern to instantiate the appropriate cache implementation based on configuration. This pattern decouples cache creation from cache usage, allowing the system to select the right implementation at runtime.

### Cache Factory Interface

Each cache implementation provides a factory that implements the CacheFactory interface. NewCache creates and initializes a cache instance with provided options.

### Cache Selection Mechanism

The cache selection process follows these steps:

1. **Singleton Check**: If a cache instance already exists, return it (singleton pattern)
2. **Type Selection**: Based on CacheType in options, instantiate CR Cache factory for `CRCacheType` or DB Cache factory for `DBCacheType`
3. **Factory Invocation**: Call factory's NewCache method with configuration options
4. **Singleton Storage**: Store created cache as global singleton
5. **Return Instance**: Return the cache instance for use

**Configuration Options:**

The factory receives CacheOptions containing:
- **CacheType**: Which cache implementation to use (CR or DB)
- **ExternalRepoOptions**: Configuration for repository adapters (credentials, resolvers)
- **RepoSyncFrequency**: How often to sync with external repositories
- **RepoPRChangeNotifier**: Watcher manager for change notifications
- **CoreClient**: Kubernetes client for CR operations
- **DBCacheOptions**: Database connection details (driver, data source) for DB cache

**Factory Benefits:** Encapsulation isolates cache creation logic in factories. Extensibility allows new cache types to be added by implementing CacheFactory. Selection is configuration-driven based on deployment configuration. Testability allows mock factories to be injected for testing.

## Cache Selection Strategy

Choosing between CR Cache and DB Cache depends on deployment requirements and scale:

### When to Use CR Cache

**Deployment characteristics:** Small to medium-scale deployments, hundreds of repositories, thousands of package revisions total, and a standard Kubernetes cluster with sufficient etcd capacity.

**Advantages:** CR Cache is Kubernetes-native (stores metadata as Custom Resources), has no external dependencies (only requires Kubernetes cluster), offers simpler operations (no database to manage or backup), has lower operational complexity (fewer moving parts), and provides immediate Git visibility (all package states exist in Git).

**Considerations:**
- In-memory caching requires re-fetch from Git on restart
- Memory usage grows with package count
- Selective cache invalidation on package deletion
- Git must be available for all draft operations

### When to Use DB Cache

**Deployment characteristics:** Large-scale deployments, hundreds to thousands of repositories, tens of thousands of package revisions, existing PostgreSQL infrastructure available, and a need for data persistence across restarts.

**Advantages:** DB Cache provides scalability (database handles large package counts efficiently), persistence (survives Porch server restarts without re-fetching), a lower memory footprint (no in-memory package caching), efficient deletion (targeted deletion without cache flush), draft isolation (Git only contains approved packages), and backup/recovery (standard database backup procedures).

**Considerations:**
- Requires external PostgreSQL database
- Higher operational complexity (database management)
- Database queries add latency compared to in-memory
- Additional infrastructure dependency

### Default Configuration

Porch deploys with CR Cache by default when no cache type is explicitly configured. This provides the simplest deployment experience for most users while allowing opt-in to DB Cache for larger deployments.

**Configuration method:** Set CacheType in Porch server startup options. CR Cache needs no additional configuration. DB Cache requires database driver and connection string.

### Migration Considerations

Switching between cache implementations requires a Porch server restart with new configuration. No data migration is needed (cache is rebuilt from Git repositories). Background sync repopulates cache from external repositories. There is a temporary performance impact during initial cache population.

## Key Architectural Differences

The two cache implementations differ fundamentally in how they interact with Git and store data:

| Aspect | CR Cache | DB Cache (Default) | DB Cache (--db-push-drafts-to-git) |
|--------|----------|----------|----------|
| **Draft Storage** | Git branches (immediate) | Database (deferred until publish) | Database + Git (immediate) |
| **Git Interaction** | Every lifecycle stage | Only on publish and sync | Every lifecycle stage |
| **Sync Scope** | All lifecycles | Published + DeletionProposed only | All lifecycles |
| **Persistence** | In-memory (re-fetch on restart) | Database (survives restart) | Database (survives restart) |
| **Git Availability** | Required for all operations | Required only for publish/sync | Required for all operations |
| **Memory Footprint** | Grows with package count | Minimal (database-backed) | Minimal (database-backed) |

**Note:** The DB Cache behavior can be configured with the `--db-push-drafts-to-git` flag. When set to `true`, the DB Cache mimics the CR Cache Git interaction timing while maintaining database persistence.

For detailed explanations of how these differences affect operations, see the individual implementation sections (CR Cache and DB Cache).
