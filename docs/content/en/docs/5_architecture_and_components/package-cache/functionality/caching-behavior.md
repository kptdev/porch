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

**Benefits:** Unused repositories have no upfront cost, memory is allocated only for accessed repositories, and the Porch server starts faster.

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

**Version tracking:** The repository version (Git commit SHA) is cached after each fetch and compared before serving data. If the version is unchanged, the Git fetch is skipped (cache hit); if it changed, the cache is refreshed (cache miss).

**Optimization:** This avoids expensive Git operations when the repository is unchanged, keeps the cache aligned with the current Git state, and balances freshness with performance.

### Force Refresh

**Explicit refresh:** Operations can request a force refresh that bypasses the version check, fetches immediately from Git, and updates the cache with the latest state. This is used when stale data is suspected or after errors.

**Refresh triggers:** A force refresh can come from a user-driven one-time sync using `porchctl repo sync` (which triggers the Repository Controller via Repository CR `spec.sync.runOnceAt` updates), from background sync operations orchestrated by the Repository Controller based on `spec.sync.schedule`, from version mismatch detection, or from recovery after sync errors.

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

**Data structures:** A package revisions map from PackageRevisionKey to PackageRevision, a packages map from PackageKey to Package, the last known Git commit SHA as the repository version, and a boolean latest-revision flag per package revision.

**Memory characteristics:** Memory grows with the number of package revisions because full repository content is cached in memory. There is no automatic eviction (the cache persists until the repository is closed). This is suitable for hundreds of repositories and thousands of revisions.

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

**Data structures:** Relational tables chain Repositories → Packages → Revisions → Resources, with foreign keys to enforce referential integrity, indexes to optimize queries on namespace, name, lifecycle, and latest, and JSON columns to store flexible metadata and specs.

**Memory characteristics:** The in-memory footprint is minimal because data is retrieved from the database on demand. This is suitable for thousands of repositories and tens of thousands of revisions, limited only by database capacity.

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

**Locking strategy:** A read-write mutex protects the cache maps. Read operations acquire a read lock (concurrent reads allowed), write operations acquire a write lock (exclusive access), and reads are lock-free when the cache is populated and the version is unchanged.

**DB Cache locking:** A per-repository mutex prevents simultaneous syncs, database transactions ensure atomic updates, and the TryLock pattern fails fast if an operation is already in progress.

## Cache Consistency

The cache maintains consistency with external Git repositories through multiple mechanisms. The [Repository Controller]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller" %}}) drives version checking and performs sync operations to keep the cache synchronized with repository state, while the cache layer provides the storage and comparison logic.

### Change Detection

The cache detects changes by comparing cached and external package revisions:

**Package Revision Comparison:**

1. Build map of existing cached package revisions by name
2. Build map of new package revisions from Git by name
3. Identify three categories: Added (in Git but not in cache — new package revisions), Modified (in both but with different resource versions), and Deleted (in cache but not in Git — removed from repository)

**Change notification:** Added, modified, and deleted package revisions trigger `watch.Added`, `watch.Modified`, and `watch.Deleted` events respectively.

**Sync Scope Differences:**

| Cache Type | Synced Lifecycles | Rationale |
|------------|-------------------|------------|
| **CR Cache** | All (Draft, Proposed, Published, DeletionProposed) | Pass-through approach - all states exist in Git |
| **DB Cache** | Published, DeletionProposed only | Database-first approach - drafts don't exist in Git |

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

**Rules:** Only Published package revisions are considered (highest revision number wins). Draft and branch-tracking revisions are excluded. The latest revision is recomputed during every sync and cache update.

**Latest revision label:** The `kpt.dev/latest-revision: "true"` label is added to the latest revision, used for filtering and queries, automatically updated when new revisions are published, and removed from the old latest when a new latest is identified.

**Async notification on deletion:** When the latest revision is deleted, an async goroutine identifies the new latest, sends a Modified notification for that revision, and ensures clients see latest revision updates without delay.

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

**Consistency mechanism:** The repository version is checked before operations, and the cache is refreshed when a mismatch is detected. This keeps the cache aligned with the current Git state and prevents serving stale data.

**Version update triggers:** The version is updated by background sync operations orchestrated by the Repository Controller, by explicit refresh requests, by package revision creation, update, or delete, and by repository reconnection after errors.

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

**Locking mechanism:** Package revisions include a Kubernetes resource version, and updates require a matching resource version. This prevents lost updates from concurrent modifications; the client must re-read and retry on conflict.

**Conflict resolution:** On conflict, the client re-reads the latest version, reapplies the changes, and retries the update with the new version.

### Metadata Synchronization

**CR Cache metadata:** PackageRev CRs store Kubernetes metadata (labels, annotations, finalizers) and are kept in sync with package revisions. Orphaned metadata is cleaned up during sync, and missing metadata is created during sync.

**DB Cache metadata:** Database records store metadata as JSON and update it atomically with package revisions. Foreign key constraints prevent orphaned records, and database transactions ensure consistency.

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

**Error handling strategy:** Sync errors are stored and reported in the Repository condition (managed by the Repository Controller). Failed syncs are retried on the next sync interval by the Repository Controller. The cache remains available with stale data during failures, and operations continue with a warning about staleness.

## Performance Optimization

The cache employs several strategies to optimize performance:

### Lock-Free Reads

**Read optimization:** The cache version is checked without a lock. If the version matches, data is served without Git access. A read lock is acquired only when accessing cache maps, and multiple concurrent reads are allowed.

**Performance impact:** This eliminates Git latency for cache hits, enables high read throughput, and scales with the number of concurrent clients.

### Lazy Loading

**Loading strategy:** Repositories are loaded on first access and package revisions are fetched on demand, so unused repositories have no upfront cost and memory is allocated incrementally.

**Benefits:** The Porch server starts faster, unused repositories have a lower memory footprint, and the design scales to large numbers of repositories.

### Efficient Data Structures

**Map-based lookups:** Lookups for package revisions and packages by key are O(1). Filtering uses map iteration, so no linear scans are required.

**Latest revision tracking:** The latest revision is pre-computed during sync and stored as a boolean flag for fast filtering. This avoids scanning all revisions to find the latest, and the flag is updated incrementally on changes.

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

**Benefits:** Foreground operations do not block on sync. The Repository Controller updates the cache asynchronously, clients are notified of changes via watch, freshness is balanced with responsiveness, and sync operations can scale independently.

### Database Query Optimization (DB Cache)

**Query strategies:** Indexes cover frequently queried columns (namespace, name, lifecycle, latest). SQL joins retrieve related data in a single query, filtering at the database level reduces data transfer, and resources are fetched separately only when needed.

**Performance characteristics:** Metadata queries are fast (indexed columns), filtering is efficient (database-level WHERE clauses), network overhead is reduced (a single query for related data), and the approach scales to large package counts.

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

**Cleanup process:** Closing stops the SyncManager (goroutines cancelled), deletes metadata resources (PackageRev CRs or DB records), sends delete notifications to watchers, closes the underlying repository adapter, and removes the cache entry from the map.
