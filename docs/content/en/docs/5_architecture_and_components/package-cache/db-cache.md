---
title: "Database Cache"
type: docs
weight: 4
description: |
  Database-based cache implementation details.
---

## Overview

The **Database (DB) Cache** is an alternative cache implementation in Porch designed for larger deployments. It stores both package metadata and repository data in a PostgreSQL database rather than in memory or Kubernetes Custom Resources. This implementation provides better scalability and persistence characteristics for production environments with high package counts.

**Key characteristics:**

- **Alternative implementation**: Used when explicitly configured with database connection details
- **Database-backed storage**: All repository, package, and package revision data stored in PostgreSQL
- **External dependency**: Requires PostgreSQL database instance
- **Suitable for**: Large deployments with thousands of packages and package revisions
- **Better persistence**: Survives Porch server restarts without re-fetching from Git
- **Git interaction**: By default, interacts with Git only during approval/publish and sync operations. Configurable with `--db-push-drafts-to-git` flag to push drafts

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

**Database Storage:**
- Repository connections and metadata
- Package metadata (names, paths)
- Package revision metadata (lifecycle, tasks, upstream locks, package resources size)
- Package resources (full KRM resource content)
- Timestamps and user tracking for all entities

**No In-Memory Cache:**
- All data retrieved from database on demand
- No in-memory caching of package revisions
- Database query performance critical for responsiveness
- Relies on PostgreSQL query optimization and indexing

### Database Handler

The DB Cache uses a **singleton database handler** that manages the PostgreSQL connection:

**Connection management:**
- Single database connection shared across all repositories
- Connection opened during cache initialization
- Connection pooling handled by PostgreSQL driver
- Ping check on connection open to verify connectivity
- Connection closed when cache is shut down

**Configuration:**
- Driver: PostgreSQL driver (pgx)
- DataSource: Connection string with host, port, database, credentials
- Configured via CacheOptions at Porch server startup

**Singleton pattern:**
- One DBHandler instance per Porch server
- GetDB() returns the singleton instance
- OpenDB() creates the singleton if not already open
- CloseDB() closes connection and clears singleton

### Repository Storage

Each repository is stored in the `repositories` table with metadata stored as JSON:

**Storage approach:**
- Repository metadata (namespace, name, spec) persisted in database
- Metadata stored as JSON for flexibility
- Timestamps track last update and user
- Deployment flag indicates repository type

**Repository lifecycle:**
1. OpenRepository checks if repository exists in database
2. If not found, creates external repository adapter
3. Writes repository metadata to database
4. Starts background sync manager
5. CloseRepository stops sync and deletes all cached packages
6. Removes repository row from database

### Package and Package Revision Storage

The DB Cache uses a **relational model** with four tables:

**Relational structure:**
- Repositories → Packages (one-to-many)
- Packages → Package Revisions (one-to-many)
- Package Revisions → Resources (one-to-one)
- Foreign key relationships enforce referential integrity

**Key storage characteristics:**
- Composite primary keys (namespace, name) match Kubernetes naming
- Metadata and specs stored as JSON for flexibility
- Lifecycle state stored as dedicated column for efficient filtering
- Latest revision tracked with boolean flag for query performance
- Resources stored in separate table to reduce row size

### Background Synchronization

The DB Cache includes a **sync manager** for each repository:

**Sync process:**
1. Periodically triggers repository sync (configurable frequency)
2. Fetches cached package revisions from database (lifecycle states depend on configuration)
3. Fetches external package revisions from Git
4. Compares cached vs external package revisions
5. Deletes package revisions only in cache (removed from Git)
6. Caches package revisions only in external repo (new in Git)
7. Updates Repository CR condition with sync status

**Sync scope:**
- **Default mode**: Only syncs Published and DeletionProposed package revisions
  - Draft and Proposed revisions excluded from sync
  - Aligns with database-first approach (drafts don't exist in Git)
  - Reduces sync overhead by ignoring work-in-progress packages
- **With `--db-push-drafts-to-git=true`**: Syncs all package revisions including Draft and Proposed
  - All lifecycle states synchronized with Git
  - Similar behavior to CR Cache
  - Higher sync overhead but complete Git synchronization

**Version tracking:**
- Caches external repository version (Git commit SHA)
- Only re-fetches from Git if version changed
- Reuses last external package revision map if version unchanged
- Reduces Git operations during frequent syncs

**Change detection:**
- Compares package revision keys between cached and external
- Identifies: cached-only, both, external-only
- Cached-only: Deleted from Git, remove from database
- External-only: New in Git, write to database
- Both: Already synchronized, no action needed

**Sync statistics:**
- Tracks count of cached-only, both, external-only
- Logs sync duration and statistics
- Reports sync errors to Repository CR condition

**Concurrency control:**
- Per-repository mutex prevents simultaneous syncs
- TryLock pattern: fails fast if sync already in progress
- Prevents database contention and duplicate work

### Latest Revision Tracking

The DB Cache tracks the latest package revision:

**Identification logic:**
- Latest revision determined during sync
- Only considers Published package revisions
- Highest revision number wins
- Stored as boolean flag in `package_revisions.latest` column

**Database flag:**
- `latest=TRUE` set on latest revision
- `latest=FALSE` on all other revisions
- Updated during sync when new revisions added
- Enables efficient queries for latest revisions

**Query optimization:**
- Can filter by `latest=TRUE` in SQL WHERE clause
- Avoids scanning all revisions to find latest
- Improves performance for latest revision queries

## Key Design Decisions

### Relational Database Model

**Why a relational model:**
- Enforces referential integrity through foreign keys
- Prevents orphaned packages or package revisions
- Enables efficient joins to retrieve related data
- Supports complex filtering at database level

**Composite primary keys:**
- All tables use (k8s_name_space, k8s_name) as primary key
- Matches Kubernetes resource naming convention
- Enables multi-tenancy with namespace isolation

### JSON Storage Strategy

**Why JSON for metadata:**
- Flexible schema without database migrations
- Stores arbitrary Kubernetes metadata (labels, annotations, etc.)
- Simplifies storage of complex nested structures
- Trade-off: Less efficient queries on JSON fields

**What's stored as JSON:**
- Repository, package, and package revision metadata
- Package specs and tasks
- Upstream locks (external package revision IDs)
- Full KRM resource content

### Separate Resources Table

**Why separate resources:**
- Package resources can be large (multiple KRM YAML files)
- Reduces row size in package_revisions table
- Improves query performance when resources not needed
- Allows fetching metadata without loading full content

### Latest Revision Flag

**Why a boolean flag:**
- Pre-computed during sync for performance
- Enables fast filtering: `WHERE latest=TRUE`
- Avoids scanning all revisions to find latest
- Trade-off: Must be maintained during updates

### Query Patterns

**SQL joins for related data:**
- Single query retrieves package revision with repository context
- Joins avoid multiple round-trips to database
- Filtering at database level reduces data transfer
- Resources fetched separately only when needed

## Storage Mechanism

### Draft Package Handling

The DB Cache has a **database-first approach** to draft packages by default, but this behavior is configurable:

**Default behavior (database-first):**
- CreatePackageRevisionDraft creates package revision in database only
- Draft packages stored entirely in PostgreSQL
- All draft modifications update database, not Git
- UpdatePackageRevision operations modify database records
- ClosePackageRevisionDraft:
  - sums up package file size based on state of resources at this point
  - saves to database without Git interaction

**Default Git interaction pattern:**
- **Draft creation**: No Git interaction (database only)
- **Draft updates**: No Git interaction (database only)
- **Proposed → Published transition**: Pushes to Git repository
- **Background sync**: Pulls published packages from Git

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

The DB Cache supports a **configurable Git push mode** via the `--db-push-drafts-to-git` flag that changes when package revisions are pushed to Git.

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

**Git interaction pattern:**
- **Draft creation**: Pushes to Git immediately
- **Draft updates**: Pushes each update to Git
- **Proposed updates**: Pushes to Git
- **Published transition**: Pushes final state to Git
- **Background sync**: Syncs all lifecycle states (Draft, Proposed, Published)

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

The DB Cache uses **targeted deletion** for cache invalidation:

**Package deletion:**
- Deletes specific package and all its revisions from database
- No cache flush required
- Database foreign key constraints ensure referential integrity
- Orphaned records automatically prevented by database

**Implications:**
- Efficient deletion without affecting other packages
- No need to rebuild cache after deletion
- Database handles cleanup automatically
- Minimal overhead for targeted deletion

### Data Persistence

The DB Cache provides true persistence:

**Porch server restart:**
- All repository, package, and package revision data survives restart
- No need to re-fetch from Git on startup
- Repositories automatically reconnect on first access
- Background sync resumes after reconnection

**Database backup:**
- Standard PostgreSQL backup procedures apply
- Point-in-time recovery possible
- Disaster recovery through database restore
- No dependency on Git availability for recovery

**Data consistency:**
- Database transactions ensure atomic updates
- Foreign key constraints prevent orphaned records
- Referential integrity maintained automatically
- Rollback on error prevents partial updates

### Memory Management

The DB Cache has minimal memory footprint:

**Memory characteristics:**
- No in-memory caching of package revisions
- Only active repository connections in memory
- Database connection pool managed by driver
- Memory usage independent of package count

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
