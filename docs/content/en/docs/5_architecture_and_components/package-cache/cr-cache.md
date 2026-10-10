---
title: "Custom Resource Cache"
type: docs
weight: 3
description: |
  CR-based cache implementation details.
---

## Overview

The **Custom Resource (CR) Cache** is the default cache implementation in Porch. It stores package metadata using Kubernetes Custom Resources while keeping repository data in memory. This implementation is designed for standard Porch deployments where Kubernetes `etcd` provides sufficient storage and performance.

**Key characteristics:** The CR Cache is the default implementation when no cache type is explicitly configured. It uses hybrid storage (an in-memory repository cache plus CR-based metadata storage) and is RDBMS-independent: no RDBMS such as PostgreSQL is required, because it leverages the Kubernetes API, `etcd`, and Git for persistence. It is suitable for small to medium deployments with moderate package counts and has no external dependencies beyond a Kubernetes cluster. It interacts with Git at every stage of the package revision lifecycle for persistence.

## Implementation Details

### Storage Architecture

The CR Cache uses a two-tier storage model:

```
In-Memory Layer
  │
  ├─ Repository connections
  ├─ Package revisions (cached)
  ├─ Package metadata (cached)
  └─ Repository version tracking
               ↓
Persistent Layer (Kubernetes)
  │
  └─ PackageRev CRs (metadata only)
```

**In-Memory Storage:** The in-memory layer holds repository instances and connections, cached package revisions from Git, package metadata (names, paths, revisions), the repository version used for change detection, and latest-revision tracking per package.

**Persistent Storage (PackageRev CRs):** PackageRev CRs persist labels and annotations, finalizers, owner references, deletion timestamps, and resource versions.

### Metadata Store

The CR Cache uses a **CRD-based metadata store** that manages `PackageRev` custom resources:

**PackageRev CR structure:** One PackageRev CR per PackageRevision, stored in same namespace as Repository CR, labeled with repository name for filtering, contains only Kubernetes metadata (no package content), and includes finalizer for cleanup coordination.

**Metadata operations:** The store creates a PackageRev CR when a package revision is published, retrieves metadata for a specific package revision, lists all PackageRev CRs for a repository, updates labels, annotations, finalizers, and owner references, and deletes a PackageRev CR (with optional finalizer clearing).

**Special labels:** `internal.porch.kpt.dev/repository` links PackageRev to Repository and is used for efficient filtering and cleanup.

**Finalizer handling:** `internal.porch.kpt.dev/packagerevision` prevents premature deletion, ensures coordination between PackageRevision and PackageRev CR, and is cleared only when all other finalizers are removed.

### Repository Caching

Each repository is wrapped in a `cachedRepository` that provides:

**Caching behavior:** On first access, the cache fetches all package revisions from Git; subsequent access returns cached data. The cache tracks the repository version to detect changes and only re-fetches when that version changes. A force refresh can bypass the cache when explicitly requested.

**Cache invalidation:** Invalidation is automatic on repository version change, with incremental updates on package revision changes.

**Concurrency control:** A per-repository mutex serializes cache updates, a read-write lock protects cache access, reads are lock-free when the cache is populated, and simultaneous refresh operations are prevented.

### Background Synchronization

The CR Cache creates a sync manager for each repository:

**Sync process:**
1. Periodically triggers repository refresh (configurable frequency)
2. Fetches latest package revisions from Git
3. Compares with cached revisions
4. Creates/updates/deletes PackageRev CRs as needed
5. Sends watch notifications for changes
6. Updates Repository CR condition with sync status

**Sync scope:** The sync covers all package revisions from Git across all lifecycles, including Draft, Proposed, Published, and DeletionProposed. This aligns with the pass-through approach (all states exist in Git) and ensures the cache reflects the complete Git repository state.

**Change detection:** Change detection compares package revision names between old and new states to detect additions, modifications, and deletions, checks resource versions to identify changes, and identifies new latest revisions.

**Notification flow:** Notifications fire for package revisions added in Git, modified in Git, or deleted from Git, and are sent before PackageRev CR creation to avoid race conditions.

### Latest Revision Tracking

The CR Cache computes the latest package revision:

**Identification logic:** Identification considers only Published package revisions, compares semantic versions when available (highest revision number wins), excludes draft and branch-tracking revisions, and is recomputed on every cache refresh.

**Latest revision label:** The `kpt.dev/latest-revision: "true"` label is added to the latest revision, used for filtering and queries, automatically updated when new revisions are published, and removed from the old latest when a new latest is identified. External modifications to this label are rejected by API strategy validation.

**Async notification:** When the latest revision is deleted, an async goroutine identifies the new latest, sends a Modified notification for that revision, and ensures clients see latest revision updates.

## Storage Mechanism

### Draft Package Handling

The CR Cache has a pass-through approach to draft packages:

**Draft lifecycle:** CreatePackageRevisionDraft passes directly to the Git repository adapter, so draft packages are immediately created as Git branches. All draft modifications go directly to Git: UpdatePackageRevision operations modify Git branches in real-time, and ClosePackageRevisionDraft commits and tags or finalizes the branch in Git.

**Git interaction pattern:** Draft creation creates a Git branch immediately, draft updates modify that branch on each update, and draft closure creates a Git tag or finalizes the branch. Every operation touches the external Git repository.

**Implications:**
- Draft work is immediately visible in Git repository
- Git repository reflects all draft states
- Network latency affects draft operations
- Git repository must be available for all draft operations

### PackageRev Custom Resource

The CR Cache stores metadata in `PackageRev` CRs:

**Resource structure:**
```yaml
apiVersion: internal.porch.kpt.dev/v1alpha1
kind: PackageRev
metadata:
  name: <repo>.<package>.<workspace>
  namespace: <repository-namespace>
  labels:
    internal.porch.kpt.dev/repository: <repo-name>
  finalizers:
    - internal.porch.kpt.dev/packagerevision
  ownerReferences:
    - apiVersion: porch.kpt.dev/v1alpha1
      kind: PackageRevision
      name: <same-as-metadata-name>
```

**Why PackageRev CRs?** PackageRev CRs separate metadata from package content, enable Kubernetes-native metadata management, support owner references and finalizers, allow label and annotation queries, and provide a resource version for optimistic locking.

### Memory Management

The CR Cache manages memory usage:

**Cache structure:** The cache is a map of repository keys to cached repositories, with a per-repository map of package revision keys to cached revisions, a per-repository map of package keys to cached packages, and a shared metadata store across all repositories.

**Memory characteristics:** Memory usage grows with the number of repositories and package revisions because full repository content is cached in memory. There is no automatic eviction (the cache persists until the repository is closed). Package deletion flushes the corresponding entries to free memory.

**Scalability considerations:**
- Suitable for hundreds of repositories
- Thousands of package revisions per repository
- Memory usage proportional to package count
- For larger deployments, consider using the DB Cache

### Repository Lifecycle

**Opening a repository:**
1. Check if repository already cached
2. If cached, return existing instance
3. If not cached, create repository adapter
4. Wrap adapter in cachedRepository
5. Start background sync manager
6. Store in cache map

**Closing a repository:**
1. Stop background sync manager
2. Delete all PackageRev CRs for repository
3. Send Delete notifications for all package revisions
4. Close underlying repository adapter
5. Remove from cache map

**Repository sharing:** Multiple Repository CRs can point to the same Git repository. The cache checks whether the repository is already open before closing it and only closes when the last Repository CR is deleted, which prevents premature connection closure.

### Cache Invalidation

The CR Cache uses selective invalidation for package revision deletion:

**Package revision deletion:** Deletion removes the specific package revision from the Git repository, drops only that revision from the in-memory cache, and deletes the corresponding PackageRev CR. Other cached package revisions remain unaffected, and the latest revision for the package is recomputed.

**Package deletion (all revisions):** Packages are deleted by removing all their revisions individually.

**Implications:**
- Efficient invalidation - only deleted revision removed from cache
- No full cache rebuild required
- No performance impact on other package revisions
- Memory freed immediately for deleted revision
