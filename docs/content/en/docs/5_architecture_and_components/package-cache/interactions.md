---
title: "Cache Interactions"
type: docs
weight: 6
description: |
  How the cache integrates with repositories and adapters.
---

## Overview

The Package Cache acts as an intermediary layer that coordinates between the CaDEngine, Repository Adapters, and external Git repositories. It provides caching, synchronization, and change notification services while maintaining a clean separation of concerns.

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│                  Package Cache Layer                    │
│                                                         │
│  ┌──────────────┐      ┌──────────────┐      ┌──────┐   │
│  │ CaDEngine    │ ───> │    Cache     │ ───> │ Repo │   │
│  │   Requests   │      │  Operations  │      │Adapt │   │
│  │              │      │              │      │ ers  │   │
│  └──────────────┘      └──────────────┘      └──────┘   │
│         ↑                      │                │       │
│         │                      ↓                ↓       │
│         │              ┌──────────────┐      ┌──────┐   │
│         └──────────────│   Watcher    │      │ Git  │   │
│                        │  Notifier    │      │Repos │   │
│                        └──────────────┘      └──────┘   │
└─────────────────────────────────────────────────────────┘
```

## Repository Adapter Integration

The cache wraps repository adapters to provide caching and synchronization services:

### Adapter Wrapping Pattern

```
Cache OpenRepository
        ↓
  Check if Cached
        ↓
   Not Cached? ──Yes──> Create External Repo
        │                      ↓
        │              externalrepo.CreateRepositoryImpl
        │                      ↓
        │              Repository Adapter
        │                      ↓
        │              Wrap in cachedRepository
        │                      ↓
        │              Start SyncManager
        │                      ↓
        └────────────> Store in Cache Map
                               ↓
                        Return Repository
```

**Process:**
1. **Cache receives** OpenRepository request with Repository CR spec
2. **Check cache map** for existing repository instance
3. **If not cached**, create an external repository adapter by calling externalrepo.CreateRepositoryImpl with the repository spec. A factory pattern selects a Git or OCI adapter based on type, and the adapter is initialized with credentials and configuration
4. **Wrap adapter** in cachedRepository (CR Cache) or dbRepository (DB Cache)
5. **Start SyncManager** for background synchronization
6. **Store in cache map** keyed by namespace/name
7. **Return wrapped repository** to CaDEngine

**Adapter delegation:**

The cached repository delegates operations to the underlying adapter:

```
Cached Repository Method
        ↓
  Check Cache State
        ↓
  Cache Valid? ──Yes──> Return Cached Data
        │
        No
        ↓
  Call Adapter Method
        ↓
  Adapter Executes
        ↓
  Update Cache
        ↓
  Return Result
```

**Delegated operations:** CreatePackageRevisionDraft and UpdatePackageRevision pass through to the adapter. ClosePackageRevisionDraft is closed by the adapter, after which the cache updates. DeletePackageRevision is deleted by the adapter, after which the cache invalidates. ListPackageRevisions is listed by the adapter and stored in the cache. Version is provided by the adapter as a Git SHA that the cache tracks. Refresh re-fetches via the adapter and rebuilds the cache.

### Credential and Configuration Flow

```
CaDEngine
     ↓
Cache Options
     ↓
ExternalRepoOptions
     ↓
Repository Adapter
     ↓
Git/OCI Operations
```

**Configuration passed through cache:** The cache passes through a credential resolver for Git/OCI authentication, a reference resolver for upstream package references, a user info provider for audit, a metadata store for CR Cache metadata (CR Cache only), and a database handler for the PostgreSQL connection (DB Cache only).

**Cache doesn't handle:** The cache does not handle authentication to external repositories, Git operations (clone, fetch, push), OCI registry operations, or package content parsing.

### Repository Lifecycle Management

**Opening repositories:**

```
OpenRepository(spec)
        ↓
  Generate Key
        ↓
  Check Cache Map
        ↓
  Found? ──Yes──> Return Cached
        │
        No
        ↓
  Create Adapter
        ↓
  Wrap & Store
        ↓
  Return New
```

**Closing repositories:**

```
CloseRepository(key)
        ↓
  Stop SyncManager
        ↓
  Delete Metadata
        ↓
  Send Delete Events
        ↓
  Close Adapter
        ↓
  Remove from Map
```

**Repository sharing:** Multiple Repository CRs can reference the same Git repository. The cache checks whether the repository is already open before closing it and only closes the adapter when the last Repository CR is deleted, which prevents premature connection closure.

## CaDEngine Access

The CaDEngine accesses the cache through a clean interface:

### Cache Interface Operations

```
CaDEngine
     ↓
Cache.OpenRepository(spec)
     ↓
Repository Interface
     ↓
  ┌──────┴──────┬──────────┬─────────┐
  ↓             ↓          ↓         ↓
List        Create      Update    Delete
Packages    Draft       Draft     Package
```

**CaDEngine workflow:**

1. **Open repository** through cache
2. **Receive repository interface** (cached wrapper)
3. **Call repository methods** (ListPackageRevisions, CreatePackageRevisionDraft, etc.)
4. **Cache handles** caching, synchronization, notifications
5. **CaDEngine unaware** of caching implementation details

### Request Flow Examples

**List package revisions:**

```
CaDEngine
     ↓
ListPackageRevisions(filter)
     ↓
Cached Repository
     ↓
Check Cache Version
     ↓
Version Match? ──Yes──> Return Cached
     │
     No
     ↓
Fetch from Adapter
     ↓
Update Cache
     ↓
Return Results
```

**Create package revision:**

```
CaDEngine
     ↓
CreatePackageRevisionDraft
     ↓
Cached Repository
     ↓
Pass to Adapter
     ↓
Adapter Creates Draft
     ↓
Return Draft
     ↓
CaDEngine Modifies
     ↓
ClosePackageRevisionDraft
     ↓
Adapter Commits
     ↓
Cache Updates
     ↓
Notify Watchers
```

**Update package revision:**

```
CaDEngine
     ↓
UpdatePackageRevision
     ↓
Cached Repository
     ↓
Unwrap Cached PR
     ↓
Pass to Adapter
     ↓
Adapter Opens Draft
     ↓
Return Draft
     ↓
CaDEngine Modifies
     ↓
ClosePackageRevisionDraft
     ↓
Cache Updates
     ↓
Notify Watchers
```

### Cache Transparency

The cache is transparent to the CaDEngine:

**CaDEngine perspective:** The CaDEngine calls repository interface methods and receives repository objects. It is unaware of the caching layer, does not know which cache implementation is in use (CR vs DB), and does not manage synchronization.

**Cache responsibilities:** The cache intercepts repository operations, manages the caching strategy, provides data storage and access interfaces, sends change notifications, and maintains consistency with Git.

## Background Synchronization

Repository synchronization is orchestrated by the Repository Controller, a separate component that manages Repository custom resources using the controller-runtime framework. The Repository Controller watches Repository resources for spec changes and reconciles them, performs periodic syncs based on configured cron schedules, and handles one-time sync requests via `spec.sync.runOnceAt`. It detects changes (added, modified, or deleted package revisions), updates the cache through standard cache interfaces, and updates Repository CR status conditions with sync results and package metadata.

The cache provides data storage and access interfaces that the Repository Controller uses to store and retrieve package revision data. The controller drives sync operations, while the cache maintains the data layer.

**Manual sync:** Use `porchctl repo sync <repository-name> -n <namespace>` for an immediate sync, or set `spec.sync.runOnceAt` in the Repository CR to a future timestamp.

For details on the Repository Controller's reconciliation logic and sync scheduling, see the [Repository Controller documentation]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller/_index.md" %}}). For the cache's role in synchronization, see [Repository Synchronization]({{% relref "/docs/5_architecture_and_components/package-cache/functionality/repository-synchronization.md" %}}).

## Watcher Notification Integration

The cache notifies watchers of package revision changes:

### Notification Flow

```
Cache Operation
        ↓
Package Changed
        ↓
NotifyPackageRevisionChange
        ↓
Watcher Manager
        ↓
Filter Matching
        ↓
Deliver to Watchers
        ↓
API Server Watch
        ↓
Client Receives Event
```

**Notification triggers:** ClosePackageRevisionDraft triggers an Added event for a new package revision, UpdatePackageRevision a Modified event, and DeletePackageRevision a Deleted event. Background sync emits Added, Modified, or Deleted events for Git changes, and closing a repository emits Deleted events for all package revisions.

**Notification timing:** Notifications are sent after the cache update completes, before the metadata store update (to avoid race conditions), regardless of metadata store errors, and synchronously from cache operations.

### Watch Event Delivery

**Event structure:** Each event includes a type (Added, Modified, or Deleted), the full package revision object with metadata, and a timestamp of when the event occurred.

**Delivery guarantees:** Delivery is at-least-once (events may be delivered multiple times), ordered for the same package revision, filtered so only matching watchers receive events, and best-effort (network failures may drop events).

**Client watch lifecycle:**
1. **Client subscribes** via API server
2. **Watcher registered** with filter
3. **Cache sends** notifications
4. **Watcher delivers** to client
5. **Client receives** watch events
6. **Client disconnects** or cancels
7. **Watcher cleaned up** automatically
