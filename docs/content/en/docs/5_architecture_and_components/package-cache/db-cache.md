---
title: "Database Cache"
type: docs
weight: 4
description: |
  Database-based cache implementation details.
---

## Overview

The **Database (DB) Cache** is an alternative cache implementation in Porch designed for larger deployments. It stores both package metadata and repository data in a PostgreSQL database rather than in memory or Kubernetes Custom Resources. This implementation provides better scalability and persistence characteristics for production environments with high package counts.

**Key characteristics:** The DB Cache is the alternative implementation, used when it is explicitly configured with database connection details. All repository, package, and package revision data is stored in PostgreSQL, which requires an external PostgreSQL database instance. It is suitable for large deployments with thousands of packages and package revisions, and it survives Porch server restarts without re-fetching from Git. By default, it interacts with Git only during approval, publish, and sync operations. The `--db-push-drafts-to-git` flag can be set to push drafts as well.

## Implementation Details

### Storage Architecture

The DB Cache uses a database-centric storage model:

```
PostgreSQL Database
  │
  ├─ repositories table
  │   └─ Repository metadata
  │
  ├─ packages table
  │   └─ Package metadata
  │
  ├─ package_revisions table
  │   └─ Package revision metadata
  │
  └─ package_revision_resources table
      └─ Package resources (KRM YAML)
```

**Database Storage:** The database stores repository connections and metadata, package metadata (names, paths), package revision metadata (lifecycle, tasks, upstream locks, package resources size), package resources (full KRM resource content), and timestamps and user tracking for all entities.

**No In-Memory Cache:** All data is retrieved from the database on demand, with no in-memory caching of package revisions. Responsiveness depends on database query performance and on PostgreSQL query optimization and indexing.

### Database Handler

The DB Cache uses a singleton database handler that manages the PostgreSQL connection:

**Connection management:** A single database connection is shared across all repositories. It is opened during cache initialization, with connection pooling handled by the PostgreSQL driver. A ping check on connection open verifies connectivity, and the connection is closed when the cache is shut down.

**Configuration:** The handler uses the PostgreSQL driver (pgx) and a DataSource connection string (host, port, database, credentials), configured via CacheOptions at Porch server startup.

**Singleton pattern:** There is one DBHandler instance per Porch server. GetDB() returns that singleton, OpenDB() creates it if it is not already open, and CloseDB() closes the connection and clears the singleton.

### Repository Storage

Each repository is stored in the `repositories` table with metadata stored as JSON:

**Storage approach:** Repository metadata (namespace, name, spec) is persisted in the database as JSON for flexibility. Timestamps track the last update and user, and a deployment flag indicates the repository type.

**Repository lifecycle:**
1. OpenRepository checks if repository exists in database
2. If not found, creates external repository adapter
3. Writes repository metadata to database
4. Starts background sync manager
5. CloseRepository stops sync and deletes all cached packages
6. Removes repository row from database

### Package and Package Revision Storage

The DB Cache uses a relational model with four tables:

**Relational structure:** Repositories relate to packages one-to-many, packages to package revisions one-to-many, and package revisions to resources one-to-one. Foreign key relationships enforce referential integrity.

**Key storage characteristics:** Composite primary keys (namespace, name) match Kubernetes naming. Metadata and specs are stored as JSON for flexibility, while lifecycle state is a dedicated column for efficient filtering. A boolean flag tracks the latest revision for query performance, and resources live in a separate table to reduce row size.

### Background Synchronization

The DB Cache includes a sync manager for each repository:

**Sync process:**
1. Periodically triggers repository sync (configurable frequency)
2. Fetches cached package revisions from database (lifecycle states depend on configuration)
3. Fetches external package revisions from Git
4. Compares cached vs external package revisions
5. Deletes package revisions only in cache (removed from Git)
6. Caches package revisions only in external repo (new in Git)
7. Updates Repository CR condition with sync status

**Sync scope:** In default mode, sync covers only Published and DeletionProposed package revisions. Draft and Proposed revisions are excluded, which aligns with the database-first approach (drafts don't exist in Git) and reduces sync overhead by ignoring work-in-progress packages. With `--db-push-drafts-to-git=true`, sync covers all package revisions including Draft and Proposed, so all lifecycle states stay synchronized with Git. This is similar to CR Cache behavior, with higher sync overhead but complete Git synchronization.

**Version tracking:** The cache stores the external repository version (Git commit SHA) and only re-fetches from Git if that version changed. If the version is unchanged, it reuses the last external package revision map, which reduces Git operations during frequent syncs.

**Change detection:** Change detection compares package revision keys between cached and external sets and classifies each key as cached-only, both, or external-only. Cached-only revisions were deleted from Git and are removed from the database; external-only revisions are new in Git and are written to the database; revisions present in both are already synchronized and need no action.

**Sync statistics:** The sync tracks counts of cached-only, both, and external-only revisions, logs duration and statistics, and reports errors to the Repository CR condition.

**Concurrency control:** A per-repository mutex prevents simultaneous syncs. The TryLock pattern fails fast if a sync is already in progress, which prevents database contention and duplicate work.

### Latest Revision Tracking

The DB Cache tracks the latest package revision:

**Identification logic:** The latest revision is determined during sync, considering only Published package revisions (highest revision number wins), and stored as a boolean flag in the `package_revisions.latest` column.

**Database flag:** `latest=TRUE` is set on the latest revision and `latest=FALSE` on all others. The flag is updated during sync when new revisions are added, which enables efficient queries for latest revisions.

**Query optimization:** Queries can filter by `latest=TRUE` in a SQL WHERE clause, which avoids scanning all revisions to find the latest and improves performance for latest-revision queries.

## Key Design Decisions

### Relational Database Model

**Why a relational model:** A relational model enforces referential integrity through foreign keys, prevents orphaned packages or package revisions, enables efficient joins to retrieve related data, and supports complex filtering at the database level.

**Composite primary keys:** All tables use (k8s_name_space, k8s_name) as the primary key, which matches the Kubernetes resource naming convention and enables multi-tenancy with namespace isolation.

### JSON Storage Strategy

**Why JSON for metadata:** JSON metadata allows a flexible schema without database migrations, stores arbitrary Kubernetes metadata (labels, annotations, and so on), and simplifies storage of complex nested structures. The trade-off is less efficient queries on JSON fields.

**What's stored as JSON:** JSON storage covers repository, package, and package revision metadata, package specs and tasks, upstream locks (external package revision IDs), and full KRM resource content.

### Separate Resources Table

**Why separate resources:** Package resources can be large (multiple KRM YAML files). Storing them separately reduces row size in the package_revisions table, improves query performance when resources are not needed, and allows fetching metadata without loading full content.

### Latest Revision Flag

**Why a boolean flag:** The flag is pre-computed during sync for performance, enabling fast filtering with `WHERE latest=TRUE` and avoiding a scan of all revisions to find the latest. The trade-off is that the flag must be maintained during updates.

### Query Patterns

**SQL joins for related data:** A single query retrieves a package revision with repository context. Joins avoid multiple round-trips to the database, filtering at the database level reduces data transfer, and resources are fetched separately only when needed.

## Storage Mechanism

### Draft Package Handling

The DB Cache has a database-first approach to draft packages by default, but this behavior is configurable:

**Default behavior (database-first):** CreatePackageRevisionDraft creates the package revision in the database only, so draft packages are stored entirely in PostgreSQL. All draft modifications update the database, not Git: UpdatePackageRevision operations modify database records, and ClosePackageRevisionDraft sums up package file size based on the state of resources at that point and saves to the database without Git interaction.

**Default Git interaction pattern:** Draft creation and draft updates have no Git interaction (database only). The Proposed → Published transition pushes to the Git repository, and background sync pulls published packages from Git.

**Default implications:**
- Draft work isolated in database until approval
- Git repository only contains approved/published packages
- Lower network latency for draft operations
- Git repository can be temporarily unavailable during draft work
- Drafts survive Porch server restarts (persisted in database)

**Default publish workflow:**
1. Draft created and modified in database
2. Lifecycle transitions: Draft → Proposed (database only)
3. Lifecycle transitions: Proposed → Published (triggers Git push)
4. Package revision pushed to Git with assigned revision number
5. External package revision ID stored in database
6. Placeholder package revision created for latest tracking

## Configurable Git Push Behavior

The DB Cache supports a configurable Git push mode via the `--db-push-drafts-to-git` flag that changes when package revisions are pushed to Git.

### Configuration

**Flag:** `--db-push-drafts-to-git`
**Type:** Boolean
**Default:** `false`
**Location:** Porch server startup flag

**Example deployment configuration:**
```yaml
spec:
  containers:
  - name: porch-server
    args:
    - --db-push-drafts-to-git=true  # Enable draft push mode
```

### Behavior When Enabled

When `--db-push-drafts-to-git=true`, the DB Cache mimics the CR Cache timing for Git pushes:

**Git interaction pattern:** Draft creation pushes to Git immediately, and each draft or proposed update is also pushed to Git. The published transition pushes the final state, and background sync covers all lifecycle states (Draft, Proposed, Published).

**Implications:**
- Draft and proposed revisions exist in both database and Git
- Git repository contains work-in-progress packages
- Each modification triggers a Git push operation
- Git repository must be available during draft operations
- Behavior similar to CR Cache
- Higher network overhead due to frequent Git operations

**Modified workflow:**
1. Draft created in database AND pushed to Git
2. Each draft update saved to database AND pushed to Git
3. Lifecycle transitions: Draft → Proposed (database + Git push)
4. Lifecycle transitions: Proposed → Published (database + Git push)
5. All states synchronized to Git throughout the lifecycle

### When to Use Each Mode

**Use default mode (`--db-push-drafts-to-git=false`) when:**
- Want to minimize Git operations during development
- Git repository should only contain approved packages
- Network latency to Git is a concern
- Want to allow draft work when Git is temporarily unavailable
- Prefer database-first workflow

**Use draft push mode (`--db-push-drafts-to-git=true`) when:**
- Need Git to reflect all package states for external tooling
- Want CR Cache-like behavior with database persistence
- Git repository serves as primary source of truth
- Need external visibility into draft/proposed packages

### Cache Invalidation

The DB Cache uses targeted deletion for cache invalidation:

**Package deletion:** Deletion removes the specific package and all its revisions from the database, with no cache flush required. Database foreign key constraints ensure referential integrity and automatically prevent orphaned records.

**Implications:**
- Efficient deletion without affecting other packages
- No need to rebuild cache after deletion
- Database handles cleanup automatically
- Minimal overhead for targeted deletion

### Data Persistence

The DB Cache provides true persistence:

**Porch server restart:** All repository, package, and package revision data survives a restart, so there is no need to re-fetch from Git on startup. Repositories automatically reconnect on first access, and background sync resumes after reconnection.

**Database backup:** Standard PostgreSQL backup procedures apply, including point-in-time recovery and disaster recovery through database restore, with no dependency on Git availability for recovery.

**Data consistency:** Database transactions ensure atomic updates. Foreign key constraints prevent orphaned records and maintain referential integrity automatically, and a rollback on error prevents partial updates.

### Memory Management

The DB Cache has minimal memory footprint:

**Memory characteristics:** There is no in-memory caching of package revisions; only active repository connections are held in memory, with the database connection pool managed by the driver. Memory usage is independent of package count.

**Scalability:**
- Suitable for thousands of repositories
- Tens of thousands of package revisions
- Limited only by database capacity
- Horizontal scaling via database replication

**Trade-offs:**
- Lower memory usage than CR Cache
- Higher latency due to database queries
- Requires external PostgreSQL instance
- Additional operational complexity

### When to Use DB Cache

**Use DB Cache when:**
- Managing hundreds of repositories
- Thousands of package revisions per repository
- Memory constraints on Porch server
- Need for data persistence across restarts
- Existing PostgreSQL infrastructure available
- Backup and disaster recovery requirements

**Use CR Cache when:**
- Small to medium deployments
- Prefer Kubernetes-native storage
- No external database dependencies desired
- Lower operational complexity preferred
- etcd capacity sufficient for package metadata
