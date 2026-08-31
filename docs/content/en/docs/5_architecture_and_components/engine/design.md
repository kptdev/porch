---
title: "Engine Design"
type: docs
weight: 2
description: |
  Architecture and design patterns of the CaD Engine.
---

## CaD Engine Architecture

The Engine follows a **layered architecture** with clear separation of concerns:

```
┌─────────────────────────────────────────┐
│         CaDEngine Interface             │  ← Public API
├─────────────────────────────────────────┤
│         cadEngine Implementation        │  ← Orchestration Logic
├─────────────────────────────────────────┤
│  ┌──────────┬──────────┬─────────────┐  │
│  │  Cache   │   Task   │   Watcher   │  │  ← Dependencies
│  │          │  Handler │   Manager   │  │
│  └──────────┴──────────┴─────────────┘  │
└─────────────────────────────────────────┘
```

**Key architectural patterns:**

### Dependency Injection via Functional Options

The engine is constructed using a **functional options pattern** that allows flexible configuration of dependencies:

- Cache implementation (CR-based or DB-based)
- Task handler for executing package operations
- Function runtimes (builtin, Function Runner exec, in-process pod evaluator)
- Credential and reference resolvers
- Watcher manager for change notifications
- User info provider for audit trails

This pattern enables testing with mock implementations and supports different deployment configurations without changing the CaDEngine code.

### Draft-Commit Workflow

The engine implements a **draft-commit pattern** for all package revision modifications:

1. **Open Draft**: Create a mutable draft from an existing package revision or start fresh
2. **Apply Changes**: Execute tasks or resource mutations on the draft
3. **Validate**: Ensure changes meet lifecycle and business rule constraints
4. **Commit**: Close the draft to create an immutable package revision
5. **Rollback**: If any step fails, clean up the draft to maintain consistency

This pattern ensures atomicity - either all changes succeed and are persisted, or none are.

### Repository Abstraction

The engine does not directly interact with Git repositories. Instead, it opens repositories through the **cache layer** and works with
repository abstractions that hide storage implementation details. It also delegates all storage operations to repository adapters.
Additionally, it maintains separation between business logic and storage mechanisms.

### Concurrency Model

The Engine handles concurrent operations through multiple mechanisms:

**Package-Level Locking:**

The engine uses **per-package mutexes** to prevent concurrent modifications to the same package:

- Mutex key is based on: `namespace-repository-package-workspace`
- Acquired using `TryLock()` - fails fast if lock unavailable
- Returns conflict error if another operation is in progress
- Automatically released after operation completes (success or failure)
- Prevents race conditions during package creation and updates

**Optimistic Locking:**

For update operations, the engine uses **Kubernetes resource versions**. The client must provide current resource version in update
request. Then, the engine compares provided version with actual version and returns a conflict error if the versions do not match. This
forces the client to re-read and retry with the latest version. This way lost updates are prevented when multiple clients modify the same
package.

**Draft Isolation:**

Drafts provide natural concurrency isolation. Each draft is a separate workspace in the repository. This means that multiple drafts can
exist for different workspaces simultaneously and drafts do not interfere with each other until closed. Closing a draft is an atomic
operation.

**Read Operations:**

List and Get operations are **lock-free**, they read from cached repository state. No locking is required for queries. You may see
slightly stale data during cache refresh but eventually it will be consistent with the repository state.

**Concurrency characteristics:**
- **Single package**: Only one write operation at a time (mutex protected)
- **Different packages**: Fully concurrent operations
- **Read operations**: Always concurrent, no blocking
- **Failed lock acquisition**: Immediate conflict error, no waiting

## Interface Design

The Engine exposes a single interface (`CaDEngine`) with operations grouped by resource type:

### PackageRevision Operations

- **ListPackageRevisions**: Query package revisions with filtering
- **CreatePackageRevision**: Create new package revisions with task execution
- **UpdatePackageRevision**: Modify draft/proposed revisions or transition lifecycle states
- **DeletePackageRevision**: Remove package revisions from repositories
- **UpdatePackageResources**: Modify the resource contents of draft package revisions

### Package Operations

- **ListPackages**: Query packages across repositories

### Supporting Operations

- **ObjectCache**: Access the watcher manager for real-time change notifications
- **OpenRepository**: Internal helper to access repositories through the cache

**Interface characteristics**

All operations are **context-aware** for cancellation and tracing. Operations accept **API objects** (porchapi types) and return
**repository abstractions**. The interface is **synchronous**, which means that operations complete before returning. Additionally, errors
are returned directly rather than stored in status fields.

## Package Lifecycle State Machine

The Engine enforces a strict state machine for package revision lifecycle:

![Package Lifecycle Workflow](/static/images/porch/lifecycle-flowchart.drawio.svg)

### State Transition Rules

#### Draft State

This is the starting state of a package when created and the default when no lifecycle is specified.
It allows full modifications of tasks, resources, metadata or lifecycle.
Packages in this state can transition to either Proposed or Published.

#### Proposed State

Packages in this state are immutable, meaning that no resource or task modifications are allowed to them. This state indicates that
the package revision is ready for review. From the proposed state, packages can transition to either Draft (for rework) or Published.

#### Published State

Packages in this state are immutable, meaning that no resource or task modifications are allowed to them. Only metadata (labels,
annotations) and lifecycle can be updated. From the published state, packages can transition to DeletionProposed. The published state
represents the deployable, production-ready state.

#### DeletionProposed State

This state marks the package revision for deletion. For lifecycle checks, packages in these state are considered "published". This is the
final state before actual deletion and it is used to signal intent to remove the package while maintaining audit trail.

### Lifecycle Enforcement

The engine enforces lifecycle rules at multiple points:

1. **Creation Validation**: Prevents creating package revisions directly in Published or DeletionProposed states
2. **Update Validation**: Checks current lifecycle before allowing modifications
3. **Resource Update Validation**: Only allows resource changes on Draft package revisions
4. **Transition Validation**: Ensures requested lifecycle transitions are valid
5. **Optimistic Locking**: Uses resource versions to prevent concurrent modification conflicts

### Special Behaviors

- **Published package revisions** are treated as immutable snapshots - their content cannot change
- **DeletionProposed** package revisions are still considered "published" for dependency checks
- **Draft** package revisions can be freely modified until published
- **Proposed -> Published** Lifecycle transitions are one-way - package revisions cannot revert from Published to Draft
