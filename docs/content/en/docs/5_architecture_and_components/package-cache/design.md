---
title: "Cache Design"
type: docs
weight: 2
description: |
  Cache architecture and implementation patterns.
---

## Cache Interface

The Package Cache provides a unified interface that abstracts the underlying storage mechanism. Both the CR Cache and DB Cache implementations conform to this common interface, allowing the CaDEngine to work with either implementation without knowing which is in use.

**Repository Operations:**
- Open repository connections from Repository CR specifications
- Close repositories and clean up resources
- Retrieve list of all open repositories
- Get specific repository by key
- Update repository metadata
- Check repository connectivity

**Repository Interface Delegation:**

Once a repository is opened through the cache, the returned Repository interface provides:

**Package Revision Operations:**
- List package revisions with filtering (repository, package, workspace, lifecycle)
- Create package revision drafts for modifications
- Update existing package revisions
- Finalize drafts to transition package revision lifecycle
- Delete package revisions from repositories

**Package Operations:**
- List packages with filtering by repository and path
- Create new packages in repositories
- Delete packages and all their revisions

**Repository Metadata:**
- Get repository version for change detection
- Refresh repository state from external sources
- Close and clean up repository resources

The interface design follows the **Strategy Pattern**, where different caching strategies (CR-based or database-backed) can be swapped without affecting the CaDEngine's operation. This abstraction enables:
- Deployment flexibility (choose cache based on scale requirements)
- Testing with mock implementations
- Future cache implementations without CaDEngine changes
- Consistent behavior regardless of storage backend

## Factory Pattern

The Package Cache uses a **Factory Pattern** to instantiate the appropriate cache implementation based on configuration. This pattern decouples cache creation from cache usage, allowing the system to select the right implementation at runtime.

### Cache Factory Interface

Each cache implementation provides a factory that implements the CacheFactory interface:
- **NewCache**: Creates and initializes a cache instance with provided options

### Cache Selection Mechanism

The cache selection process follows these steps:

1. **Singleton Check**: If a cache instance already exists, return it (singleton pattern)
2. **Type Selection**: Based on CacheType in options:
   - `CRCacheType`: Instantiate CR Cache factory
   - `DBCacheType`: Instantiate DB Cache factory
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

**Factory Benefits:**
- **Encapsulation**: Cache creation logic isolated in factories
- **Extensibility**: New cache types can be added by implementing CacheFactory
- **Configuration-driven**: Cache selection based on deployment configuration
- **Testability**: Mock factories can be injected for testing

## Cache Selection Strategy

Choosing between CR Cache and DB Cache depends on deployment requirements and scale:

### When to Use CR Cache

**Deployment characteristics:**
- Small to medium-scale deployments
- Hundreds of repositories
- Thousands of package revisions total
- Standard Kubernetes cluster with sufficient etcd capacity

**Advantages:**
- **Kubernetes-native**: Stores metadata as Custom Resources
- **No external dependencies**: Only requires Kubernetes cluster
- **Simpler operations**: No database to manage or backup
- **Lower operational complexity**: Fewer moving parts
- **Immediate Git visibility**: All package states exist in Git

**Considerations:**
- In-memory caching requires re-fetch from Git on restart
- Memory usage grows with package count
- Selective cache invalidation on package deletion
- Git must be available for all draft operations

### When to Use DB Cache

**Deployment characteristics:**
- Large-scale deployments
- Hundreds to thousands of repositories
- Tens of thousands of package revisions
- Existing PostgreSQL infrastructure available
- Need for data persistence across restarts

**Advantages:**
- **Scalability**: Database handles large package counts efficiently
- **Persistence**: Survives Porch server restarts without re-fetching
- **Lower memory footprint**: No in-memory package caching
- **Efficient deletion**: Targeted deletion without cache flush
- **Draft isolation** (default): Git only contains approved packages; optional draft push mode pushes drafts during the repository sync
- **Backup/recovery**: Standard database backup procedures

**Considerations:**
- Requires external PostgreSQL database
- Higher operational complexity (database management)
- Database queries add latency compared to in-memory
- Additional infrastructure dependency

### Default Configuration

Porch deploys with **CR Cache by default** when no cache type is explicitly configured. This provides the simplest deployment experience for most users while allowing opt-in to DB Cache for larger deployments.

**Configuration method:**
- Set CacheType in Porch server startup options
- CR Cache: No additional configuration needed
- DB Cache: Requires database driver and connection string

### Migration Considerations

Switching between cache implementations requires:
- Porch server restart with new configuration
- No data migration needed (cache is rebuilt from Git repositories)
- Background sync repopulates cache from external repositories
- Temporary performance impact during initial cache population

## Key Architectural Differences

The two cache implementations differ fundamentally in how they interact with Git and store data:

| Aspect | CR Cache | DB Cache (Default) | DB Cache (--db-push-drafts-to-git) |
|--------|----------|----------|----------|
| **Draft Storage** | Git branches (immediate) | Database only | Database (Git updated during sync) |
| **Git Interaction** | Every lifecycle stage | Publish and sync (pull) | Publish and sync (pull and push) |
| **Sync Scope** | All lifecycles | Published + DeletionProposed only | All lifecycles |
| **Persistence** | In-memory (re-fetch on restart) | Database (survives restart) | Database (survives restart) |
| **Git Availability for Draft Edits** | Required for all operations | Required only for publish/sync | Required for all operations if running the repository sync can be guaranteed |
| **Memory Footprint** | Grows with package count | Minimal (database-backed) | Minimal (database-backed) |

**Note:** With `--db-push-drafts-to-git=true`, Draft and Proposed revisions are pushed to Git during the repository sync when the database content has changed. Draft create and update operations remain database-only; Git visibility for drafts is bounded by sync frequency.

For detailed explanations of how these differences affect operations, see the individual implementation sections (CR Cache and DB Cache).
