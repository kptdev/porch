---
title: "Caching Behavior"
type: docs
weight: 2
description: |
  Detailed architecture of cache population, structure, and consistency mechanisms.
---

## Overview

The Package Cache optimizes performance by storing repository data in memory (CR Cache) or database (DB Cache) to avoid redundant Git operations. The caching system uses lazy loading, version-based refresh, and concurrency control to balance performance with data freshness.

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Caching System                       │
│                                                         │
│  ┌──────────────┐      ┌──────────────┐      ┌──────┐   │
│  │   Cache      │      │   Version    │      │ Git  │   │
│  │  Population  │ ───> │   Tracking   │ ───> │ Repo │   │
│  │              │      │              │      │      │   │
│  │ • Lazy Load  │      │ • Compare    │      │      │   │
│  │ • Refresh    │      │ • Refresh    │      │      │   │
│  └──────────────┘      └──────────────┘      └──────┘   │
│         │                      │                        │
│         └──────────┬───────────┘                        │
│                    ↓                                    │
│         ┌──────────────────┐                            │
│         │  Cache Structure │                            │
│         │                  │                            │
│         │  • Maps          │                            │
│         │  • Mutex         │                            │
│         │  • Consistency   │                            │
│         └──────────────────┘                            │
└─────────────────────────────────────────────────────────┘
```

## Cache Population

The cache uses lazy loading and version-based refresh to minimize Git operations. While the cache provides the data storage and access interfaces, the [Repository Controller]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller" %}}) orchestrates background sync operations that populate and refresh the cache on configurable schedules.

### Initial Population

```
CaDEngine Request
        ↓
  OpenRepository
        ↓
   Cache Empty? ──No──> Return Cached Data
        │
       Yes
        ↓
  Fetch from Git
        ↓
  Build Cache Maps
        ↓
  Store in Cache
        ↓
  Return Data
```

**Process:**
1. **Repository opened** on first access from CaDEngine
2. **Cache initially empty** (lazy loading strategy)
3. **First operation triggers fetch** from Git repository
4. **All package revisions loaded** into cache
5. **Subsequent operations served** from cached data

**Benefits:**
- No upfront cost for unused repositories
- Memory allocated only for accessed repositories
- Faster startup time for Porch server

### Version-Based Refresh

```
Operation Request
        ↓
  Check Cache Version
        ↓
  Fetch Git Version
        ↓
  Versions Match? ──Yes──> Serve from Cache
        │
        No
        ↓
  Fetch from Git
        ↓
  Update Cache
        ↓
  Update Version
        ↓
  Serve Data
```

**Version tracking:**
- Repository version (Git commit SHA) cached after each fetch
- Version compared before serving data
- If version unchanged, skip Git fetch (cache hit)
- If version changed, refresh cache (cache miss)

**Optimization:**
- Avoids expensive Git operations when repository unchanged
- Ensures cache reflects current Git state
- Balances freshness with performance

### Force Refresh

**Explicit refresh:**
- Operations can request force refresh (bypass version check)
- Triggers immediate fetch from Git
- Updates cache with latest state
- Used when stale data suspected or after errors

**Refresh triggers:**
- User-driven one-time sync using `porchctl repo sync` (which triggers the Repository Controller via Repository CR `spec.sync.runOnceAt` updates)
- Background sync operations orchestrated by the Repository Controller based on `spec.sync.schedule`
- Version mismatch detection
- Recovery from sync errors

## Cache Structure

The cache maintains structured data for fast lookups and efficient operations:

### CR Cache Structure

```
Cached Repository
    │
    ├─ Repository Metadata
    │   ├─ Key (namespace, name)
    │   ├─ Spec (Repository CR)
    │   └─ Last Version (Git SHA)
    │
    ├─ Package Revisions Map
    │   └─ PackageRevisionKey → CachedPackageRevision
    │       ├─ PackageRevision object
    │       ├─ Metadata store reference
    │       └─ isLatestRevision flag
    │
    ├─ Packages Map
    │   └─ PackageKey → CachedPackage
    │       ├─ Package object
    │       └─ Latest revision reference
    │
    └─ Concurrency Control
        └─ Read-Write Mutex
```

**Data structures:**
- **Package revisions map**: PackageRevisionKey → PackageRevision
- **Packages map**: PackageKey → Package
- **Repository version**: Last known Git commit SHA
- **Latest revision flags**: Boolean per package revision

**Memory characteristics:**
- Grows with number of package revisions
- Full repository content cached in memory
- No automatic eviction (persists until repository closed)
- Suitable for hundreds of repositories, thousands of revisions

### DB Cache Structure

```
PostgreSQL Database
    │
    ├─ repositories table
    │   └─ Repository metadata (JSON)
    │
    ├─ packages table
    │   └─ Package metadata (JSON)
    │
    ├─ package_revisions table
    │   ├─ Metadata (JSON)
    │   ├─ Lifecycle (column)
    │   └─ Latest flag (boolean)
    │
    └─ package_revision_resources table
        └─ KRM resources (JSON)
```

**Data structures:**
- **Relational tables**: Repositories → Packages → Revisions → Resources
- **Foreign keys**: Enforce referential integrity
- **Indexes**: Optimize queries on namespace, name, lifecycle, latest
- **JSON columns**: Store flexible metadata and specs

**Memory characteristics:**
- Minimal in-memory footprint
- Data retrieved from database on demand
- Suitable for thousands of repositories, tens of thousands of revisions
- Limited only by database capacity

### Concurrency Control

**CR Cache locking:**
```
Read Operation          Write Operation
      ↓                       ↓
  RLock()                 Lock()
      ↓                       ↓
  Read Data              Modify Data
      ↓                       ↓
  RUnlock()              Unlock()
```

**Locking strategy:**
- **Read-write mutex** protects cache maps
- **Read operations** acquire read lock (concurrent reads allowed)
- **Write operations** acquire write lock (exclusive access)
- **Lock-free reads** when cache populated and version unchanged

**DB Cache locking:**
- **Per-repository mutex** prevents simultaneous syncs
- **Database transactions** ensure atomic updates
- **TryLock pattern** fails fast if operation already in progress

## Cache Consistency

The cache maintains consistency with external Git repositories through multiple mechanisms. The [Repository Controller]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller" %}}) drives version checking and performs sync operations to keep the cache synchronized with repository state, while the cache layer provides the storage and comparison logic.

### Change Detection

The cache detects changes by comparing cached and external package revisions:

**Package Revision Comparison:**

1. Build map of existing cached package revisions by name
2. Build map of new package revisions from Git by name
3. Identify three categories:
   - **Added**: In Git but not in cache (new package revisions)
   - **Modified**: In both but with different resource versions
   - **Deleted**: In cache but not in Git (removed from repository)

**Change notification:**
- Added package revisions trigger `watch.Added` events
- Modified package revisions trigger `watch.Modified` events
- Deleted package revisions trigger `watch.Deleted` events

**Sync Scope Differences:**

| Cache Type | Synced Lifecycles | Rationale |
|------------|-------------------|------------|
| **CR Cache** | All (Draft, Proposed, Published, DeletionProposed) | Pass-through approach - all states exist in Git |
| **DB Cache (default)** | Published, DeletionProposed only | Database-first approach - drafts don't exist in Git |
| **DB Cache (`--db-push-drafts-to-git`)** | All (Draft, Proposed, Published, DeletionProposed) | Database Sync compares all states and pushes Draft/Proposed changes to Git during the sync |

See [Database Cache — Configurable Git Push Behavior]({{% relref "/docs/5_architecture_and_components/package-cache/db-cache.md#configurable-git-push-behavior" %}}) for draft push mode details.

### Latest Revision Tracking

The cache automatically identifies and tracks the latest package revision for each package:

**Identification Logic:**

```
All Package Revisions
        ↓
Filter Published Only
        ↓
Compare Revision Numbers
        ↓
Highest Number = Latest
        ↓
Set Latest Flag/Label
```

**Rules:**
- Only Published package revisions considered
- Highest revision number wins
- Draft and branch-tracking revisions excluded
- Recomputed during every sync and cache update

**Latest revision label:**
- `kpt.dev/latest-revision: "true"` added to latest revision
- Used for filtering and queries
- Automatically updated when new revisions published
- Removed from old latest when new latest identified

**Async notification on deletion:**
- When latest revision deleted, async goroutine identifies new latest
- Sends Modified notification for new latest revision
- Ensures clients see latest revision updates without delay

### Version-Based Consistency

```
Cache State              Git Repository
    ↓                          ↓
Version: abc123          Version: abc123
    ↓                          ↓
    └────── Compare ───────────┘
              ↓
         Match Found
              ↓
      Serve from Cache
      (No Git Access)
```

**Consistency mechanism:**
- Repository version checked before operations
- Cache refreshed when version mismatch detected
- Ensures cache reflects current Git state
- Prevents serving stale data

**Version update triggers:**
- Background sync operations orchestrated by the Repository Controller
- Explicit refresh requests
- Package revision creation/update/delete
- Repository reconnection after errors

### Optimistic Locking

```
Client Update Request
        ↓
  Resource Version: v1
        ↓
  Cache Check
        ↓
  Current Version: v1? ──No──> Conflict Error
        │
       Yes
        ↓
  Apply Update
        ↓
  Increment Version: v2
        ↓
  Return Success
```

**Locking mechanism:**
- Package revisions include Kubernetes resource version
- Updates require matching resource version
- Prevents lost updates from concurrent modifications
- Client must re-read and retry on conflict

**Conflict resolution:**
- Client receives conflict error
- Client re-reads latest version
- Client reapplies changes
- Client retries update with new version

### Metadata Synchronization

**CR Cache metadata:**
- PackageRev CRs store Kubernetes metadata (labels, annotations, finalizers)
- Metadata kept in sync with package revisions
- Orphaned metadata cleaned up during sync
- Missing metadata created during sync

**DB Cache metadata:**
- Database records store metadata as JSON
- Metadata updated atomically with package revisions
- Foreign key constraints prevent orphaned records
- Database transactions ensure consistency

### Error Handling

**Sync error behavior:**
```
Sync Operation
      ↓
   Error? ──No──> Update Cache
      │
     Yes
      ↓
  Log Error
      ↓
  Update Condition
      ↓
  Keep Stale Cache
      ↓
  Retry Next Cycle
```

**Error handling strategy:**
- Sync errors stored and reported in Repository condition (managed by Repository Controller)
- Failed syncs retried on next sync interval by the Repository Controller
- Cache remains available with stale data during failures
- Operations continue with warning about staleness

## Performance Optimization

The cache employs several strategies to optimize performance:

### Lock-Free Reads

**Read optimization:**
- Cache version checked without lock
- If version matches, serve data without Git access
- Read lock acquired only when accessing cache maps
- Multiple concurrent reads allowed

**Performance impact:**
- Eliminates Git latency for cache hits
- Enables high read throughput
- Scales with number of concurrent clients

### Lazy Loading

**Loading strategy:**
- Repositories loaded on first access
- Package revisions fetched on demand
- No upfront cost for unused repositories
- Memory allocated incrementally

**Benefits:**
- Faster Porch server startup
- Lower memory footprint for unused repositories
- Scales to large numbers of repositories

### Efficient Data Structures

**Map-based lookups:**
- O(1) lookup time for package revisions by key
- O(1) lookup time for packages by key
- Efficient filtering using map iteration
- No linear scans required

**Latest revision tracking:**
- Pre-computed during sync
- Boolean flag for fast filtering
- Avoids scanning all revisions to find latest
- Updated incrementally on changes

### Background Sync

The [Repository Controller]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller" %}}) performs asynchronous synchronization on configurable schedules (via `spec.sync.schedule`), using the cache as the data store. This separation allows the cache to serve foreground operations without blocking while the controller independently manages repository polling and updates.

**Async synchronization:**
```
Foreground Operations    Repository Controller
        ↓                       ↓
  Serve from Cache        Periodic Sync
        ↓                       ↓
  No Blocking            Update Cache
        ↓                       ↓
  Fast Response          Notify Changes
```

**Benefits:**
- Operations don't block on sync
- Cache updated asynchronously by the Repository Controller
- Clients notified of changes via watch
- Balances freshness with responsiveness
- Independent scaling of sync operations

### Database Query Optimization (DB Cache)

**Query strategies:**
- Indexes on frequently queried columns (namespace, name, lifecycle, latest)
- SQL joins to retrieve related data in single query
- Filtering at database level reduces data transfer
- Resources fetched separately only when needed

**Performance characteristics:**
- Fast metadata queries (indexed columns)
- Efficient filtering (database-level WHERE clauses)
- Reduced network overhead (single query for related data)
- Scalable to large package counts

## Cache Lifecycle

### Repository Opening

```
OpenRepository Request
        ↓
  Check if Cached
        ↓
  Already Open? ──Yes──> Return Cached
        │
        No
        ↓
  Create Adapter
        ↓
  Wrap in Cache
        ↓
  Start SyncManager
        ↓
  Store in Cache
        ↓
  Return Repository
```

### Repository Closing

```
CloseRepository Request
        ↓
  Stop SyncManager
        ↓
  Delete Metadata
        ↓
  Send Delete Events
        ↓
  Close Adapter
        ↓
  Remove from Cache
        ↓
  Complete
```

**Cleanup process:**
- SyncManager stopped (goroutines cancelled)
- Metadata resources deleted (PackageRev CRs or DB records)
- Delete notifications sent to watchers
- Underlying repository adapter closed
- Cache entry removed from map
