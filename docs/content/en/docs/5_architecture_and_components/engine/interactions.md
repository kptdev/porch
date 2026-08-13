---
title: "Engine Interactions"
type: docs
weight: 4
description: |
  How the Engine interacts with other Porch components.
---

## Overall Interaction Overview

![Engine Interaction Architecture](/static/images/porch/engine-component-interaction.drawio.svg)

## Cache Integration

The Engine relies heavily on the Package Cache as its primary interface to repositories:

### Opening Repositories

All repository access goes through the cache:

```
Engine
     ↓
OpenRepository(repositorySpec)
     ↓
Package Cache
     ↓
Repository Adapter (Git)
```

**Process:**
1. Engine receives a Repository CR specification
2. Calls `cache.OpenRepository(ctx, repositorySpec)`
3. Cache returns a Repository interface implementation
4. Engine uses the repository abstraction for all operations

**Benefits of cache-mediated access:**
- **Caching**: Frequently accessed repositories remain in memory
- **Synchronization**: Cache handles background repository sync
- **Abstraction**: Engine doesn't need to know if it's Git or another backend
- **Connection pooling**: Cache manages repository connections efficiently

### Repository Operations

Once a repository is opened, the engine delegates all storage operations:

**Package Revision Operations:**
- `ListPackageRevisions`: Query package revisions with filtering
- `CreatePackageRevisionDraft`: Create mutable draft
- `UpdatePackageRevision`: Open existing revision as draft
- `ClosePackageRevisionDraft`: Commit draft to create immutable revision
- `DeletePackageRevision`: Remove package revision from storage

**Package Operations:**
- `ListPackages`: Query packages
- `CreatePackage`: Initialize new package
- `DeletePackage`: Remove package and all revisions

**Repository Metadata:**
- `Version`: Get repository version for change detection
- `Refresh`: Trigger repository sync
- `Close`: Clean up repository resources

The engine never directly manipulates Git repositories or storage - all operations go through the repository abstraction provided by the cache.

### Cache Invalidation

The engine does not directly invalidate cache entries. Instead, the cache monitors Repository CRs for changes, and background sync jobs refresh
repository state periodically. Repository operations (create, update, delete) trigger cache updates automatically. Engine operations are always
performed on the latest cached state.

## Task Handler Invocation

The Engine delegates all package transformation operations to the Task Handler:

### Task Execution During Creation

When creating a package revision:

```
Engine
     ↓
ApplyTask(draft, repositoryObj, packageRevision, packageConfig)
     ↓
Task Handler
     ↓
Task Mutations (init, clone, render, etc.)
```

**Process:**
1. Engine creates a draft package revision
2. Calls `taskHandler.ApplyTask()` with:
   - Draft to modify
   - Repository object for context
   - PackageRevision spec with task definition
   - Package configuration (path, name, etc.)
3. Task handler executes the task (init, clone, upgrade)
4. Task handler applies builtin functions (package context generation)
5. Returns modified draft or error

**Task types handled:**
- **Init**: Create new package with Kptfile
- **Clone**: Copy package from upstream
- **Upgrade**: Merge changes from new upstream version
- **Edit**: Create new revision from existing one

### Mutations During Updates

When updating a package revision:

```
Engine
     ↓
DoPRMutations(repoPR, oldObj, newObj, draft)
     ↓
Task Handler
     ↓
Apply new tasks to draft
```

**Process:**
1. Engine opens draft from existing package revision
2. Calls `taskHandler.DoPRMutations()` with old and new specs
3. Task handler compares task lists
4. Applies any new tasks to the draft
5. Returns modified draft

### Resource Mutations

When updating package resources directly:

```
Engine
     ↓
DoPRResourceMutations(pr, draft, oldRes, newRes)
     ↓
Task Handler
     ↓
Update resources + render
```

**Process:**
1. Engine opens draft from package revision
2. Calls `taskHandler.DoPRResourceMutations()` with resource changes
3. Task handler updates package resources
4. Task handler executes render task (runs function pipeline)
5. Returns render status with function results

The Engine provides the draft workspace. The Task handler modifies resources in the draft, after which the Engine commits
the draft. The Task handler has no direct repository access.

### Function Runtime Integration

The task handler uses function runtimes configured in the engine:

- **Builtin Runtime**: For built-in functions (set-namespace, etc.)
- **gRPC Runtime**: For external function runner service
- **Multi-Runtime**: Chains multiple runtimes together

The engine configures these runtimes during initialization and passes them to the task handler.

## Repository Access

The Engine accesses repositories through a layered abstraction:

### Repository Abstraction Layers

```
Engine
     ↓
Repository Interface (from cache)
     ↓
Repository Adapter (Git)
     ↓
External Repository (GitHub, GitLab, etc.)
```

### Repository Interface

The engine works with the `Repository` interface which provides Package Revision Management, Package Management,
and Repository Metadata.

**Package Revision Management:**
- Draft creation and closure
- Listing with filtering
- Deletion

**Package Management:**
- Package listing
- Package creation and deletion

**Repository Metadata:**
- Version tracking
- Refresh operations
- Resource cleanup

This interface is implemented by repository adapters (Git adapter) but the engine never calls adapter code directly.

### Draft Workflow

The engine uses a draft-based workflow for all modifications:

1. **Create/Open Draft**: Get mutable workspace
   - `CreatePackageRevisionDraft`: For new revisions
   - `UpdatePackageRevision`: For existing revisions

2. **Modify Draft**: Apply changes through task handler
   - Draft provides mutable access to package revision resources
   - Changes are isolated until committed

3. **Close Draft**: Commit changes
   - `ClosePackageRevisionDraft`: Persists to repository
   - Creates package revision
   - Generates Git commit or OCI layer

4. **Rollback**: On errors, draft is discarded
   - Ensures atomicity
   - No partial changes persisted

### Credential and Reference Resolution

The engine is configured with resolvers for repository access.

The Credential Resolver resolves authentication credentials for repositories. It supports basic auth,
bearer tokens, and GCP workload identity. It is passed to repository adapters through cache. The engine
does not handle credentials directly.

The Reference Resolver resolves package revision references (upstream package revisions), and it is
used during clone and upgrade operations. It is used to enable cross-repository package revision operations.

The User Info Provider provides authenticated user information, which is used for audit trails (PublishedBy field).
This info is extracted from Kubernetes API request context.

These resolvers are configured during engine initialization and passed to components that need them.

## Change Notification

The Engine notifies watchers of package revision changes:

### Watcher Manager Integration

```
Engine
     ↓
NotifyPackageRevisionChange(eventType, packageRevision)
     ↓
Watcher Manager
     ↓
API Server Watch Streams
```

**Notification events:**
- **Added**: New package revision created
- **Modified**: Package revision updated (metadata or lifecycle)
- **Deleted**: Package revision removed

Notifications are sent after successful package revision creation, after package revision updates (including lifecycle transitions),
and after metadata-only updates on published packages. Notifications are not sent on failures or during draft operations.

### Watch Stream Support

The watcher manager enables Kubernetes watch API support:

1. **Client subscribes**: API server calls `WatchPackageRevisions`
2. **Filter registration**: Watcher manager stores filter and callback
3. **Change notification**: Engine calls `NotifyPackageRevisionChange`
4. **Event delivery**: Watcher manager delivers to matching watchers
5. **Client receives**: Watch event sent to client

You can filter for repository name and namespace, package name and path, workspace name, lifecycle state, and
label selectors.

Only watchers with matching filters receive notifications.

### Watcher Lifecycle

Watchers are automatically cleaned up when client context is cancelled (connection closed) and when watcher callback
returns false (stop watching). The watcher manager periodically removes finished watchers. No manual cleanup is required
from engine.

The engine simply notifies changes and the watcher manager handles delivery and cleanup.
