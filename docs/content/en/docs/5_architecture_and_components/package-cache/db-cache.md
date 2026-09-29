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
- **Git interaction**: By default, interacts with Git only during publish and sync (pull). With `--db-push-drafts-to-git`, draft and proposed revisions are also pushed to Git during the sync

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
- **Default mode (`--db-push-drafts-to-git=false`)**: Compares only Published and DeletionProposed package revisions
  - Draft and Proposed revisions are excluded from sync comparison
  - Aligns with database-first approach (drafts don't exist in Git)
  - Reduces sync overhead by ignoring work-in-progress packages
- **Draft push mode (`--db-push-drafts-to-git=true`)**: Compares all lifecycle states including Draft and Proposed
  - Sync detects draft/proposed revisions that need to be pushed to Git
  - Pushes are queued during sync, not on every database update
  - Higher sync overhead but keeps Git aligned with database state over time

**Version tracking:**
- Caches external repository version (Git commit SHA)
- Only re-fetches from Git if version changed
- Reuses last external package revision map if version unchanged
- Reduces Git operations during frequent syncs

**Change detection:**
- Compares package revision keys between cached and external
- Identifies: cached-only, both and external-only
- **Cached-only**: If the PR is removed from Git, then it is deleted from the database (except unpushed Draft/Proposed when draft push mode is enabled; those are queued for Git push instead)
- **External-only**: If the PR is new in Git, it is written to the database
- **Both**: If the PR is already present in both, then no pull action is needed. In draft push mode, Draft/Proposed revisions whose database content changed since the last push are queued for Git push

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

For optional draft push mode (`--db-push-drafts-to-git`), see the configuration section below. Draft create and update operations remain database-only. Git is updated during background sync.

## Configurable Git Push Behavior

The DB Cache supports a **configurable draft push mode** via the `--db-push-drafts-to-git` flag. When enabled, Draft and Proposed package revisions are pushed to Git during background sync rather than on every database update.

### Configuration

| Component | Flag | Default |
|-----------|------|---------|
| Porch server | `--db-push-drafts-to-git` | `false` |
| Repository controller | `--repositories.push-drafts-to-git` | `false` |

Both flags must be set to `true` when using DB Cache with draft push mode. The repository controller passes the setting to the cache layer used during sync.

For deployment examples, see [Porch Server configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-server-config" %}}) and [Repository Controller configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/components/porch-controllers-config" %}}).

### Database-First Write Path

Regardless of the flag setting, draft work follows the same database-first write path:

- **Draft creation**: Stored in PostgreSQL only
- **Draft updates**: Stored in PostgreSQL only
- **Proposed updates**: Stored in PostgreSQL only
- **Lifecycle transitions** (Draft → Proposed): Stored in PostgreSQL only

Git is not contacted during create or update operations. This keeps draft editing fast and allows draft work when Git is temporarily unavailable.

### Behavior When Draft Push Mode Is Enabled

When `--db-push-drafts-to-git=true`, background sync pushes Draft and Proposed revisions to Git:

**During sync for cached-only Draft/Proposed PRs present only in database, not in Git:**
- Sync queues a Git push instead of treating the revision as stale cache data to delete
- Push runs asynchronously after sync identifies the revision

**During sync for Draft/Proposed PRs present in both database and Git:**
- Sync compares the revision's `updated` timestamp against the last successful push marker
- If database content changed since the last push, sync queues a Git push
- If already up to date, no push is performed

**Push execution:**
- Each queued push re-reads the revision from the database before writing to Git
- Skips push if the revision was deleted, published, or unchanged since sync detected the need
- Records push markers on success so subsequent syncs can skip redundant pushes
- If the revision is modified during an in-flight push, markers are not recorded and the next sync retries with fresh data

**Publish:**
- Published transitions still push to Git immediately (same as default mode)
- If the draft branch already exists in Git from a prior sync push, publish updates that branch instead of creating a new one

**Delete:**
- Deleting a Draft or Proposed revision also removes the corresponding Git branch when draft push mode is enabled

**Sync scope:**
- Sync compares all lifecycle states (Draft, Proposed, Published, DeletionProposed)
- Git eventually reflects draft/proposed state, bounded by sync frequency

### When to Use Each Mode

**Use default mode (`--db-push-drafts-to-git=false`) when:**
- Git repository should only contain approved/published packages
- Want to minimize Git operations
- External tooling does not need visibility into draft/proposed work
- Prefer the simplest database-first workflow

**Use draft push mode (`--db-push-drafts-to-git=true`) when:**
- External Git tooling or backup processes need draft/proposed branches in Git
- Git should reflect work-in-progress packages, with sync frequency acceptable as the update latency
- Unpushed draft/proposed packages must survive repository re-registration (see [Repository Unregistration]({{% relref "/docs/4_tutorials_and_how-tos/working_with_porch_repositories/repository-unregistration" %}}))

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
