---
title: "Draft-Commit Workflow Orchestration"
type: docs
weight: 3
description: |
  Detailed architecture of the draft-commit workflow pattern and rollback mechanisms.
---

## Overview

The Engine orchestrates a draft-commit workflow for all package revision modifications. This pattern ensures atomicity - either all changes succeed and are persisted, or none are. The workflow uses mutable drafts for changes and immutable package revisions for storage.

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│          Draft-Commit Workflow Orchestration            │
│                                                         │
│  ┌──────────────────┐      ┌──────────────────┐         │
│  │   Draft Phase    │      │   Commit Phase   │         │
│  │                  │ ───> │                  │         │
│  │  • Open Draft    │      │  • Close Draft   │         │
│  │  • Apply Changes │      │  • Persist       │         │
│  │  • Validate      │      │  • Immutable     │         │
│  └──────────────────┘      └──────────────────┘         │
│           │                         │                   │
│           └────────┬────────────────┘                   │
│                    ↓                                    │
│          ┌──────────────────┐                           │
│          │     Rollback     │                           │
│          │    Mechanism     │                           │
│          │                  │                           │
│          │• Trigger On Error│                           │
│          │• Does Cleanup    │                           │
│          └──────────────────┘                           │
└─────────────────────────────────────────────────────────┘
```

## Draft-Commit Pattern

The Engine uses a two-phase workflow for all package revision modifications:

### Pattern Overview

```
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│   Request   │ ──────> │    Draft    │ ──────> │  Immutable  │
│             │         │   (Mutable) │         │   Package   │
│  Create or  │         │             │         │  Revision   │
│   Update    │         │  Changes    │         │             │
└─────────────┘         └─────────────┘         └─────────────┘
                              │
                              │ Error?
                              ↓
                        ┌─────────────┐
                        │  Rollback   │
                        │   Delete    │
                        │    Draft    │
                        └─────────────┘
```

**Two phases:**
1. **Draft Phase**: Mutable workspace where changes are applied
2. **Commit Phase**: Draft closed to create immutable package revision

**Key characteristics:**
- **Atomicity**: All changes succeed or all fail
- **Isolation**: Draft changes don't affect other revisions
- **Consistency**: Validation before commit
- **Durability**: Committed revisions are immutable

## Create Package Revision Workflow

The Engine orchestrates package revision creation through a draft-commit workflow:

### Creation Flow

```
CreatePackageRevision Request
        ↓
  Build Package Config
        ↓
  Validate Request
        ↓
  Open Repository
        ↓
  Create Draft ──────────────┐
        ↓                    │
  Setup Rollback Handler     │
        ↓                    │
  Apply Task                 │
        ↓                    │
  Error? ──Yes──> Rollback ──┘
        │
        No
        ↓
  Update Lifecycle
        ↓
  Error? ──Yes──> Rollback ──┘
        │
        No
        ↓
  Close Draft
        ↓
  Error? ──Yes──> Return Error (no rollback)
        │
        No
        ↓
  Return PackageRevision
```

**Process steps:**
1. **Build package config** from request and parent (if any)
2. **Validate request** (lifecycle, tasks, workspace name, etc.)
3. **Open repository** through cache
4. **Create draft** in repository (mutable workspace)
5. **Setup rollback handler** for error recovery
6. **Apply task** through task handler (init, clone, edit, upgrade)
7. **Update lifecycle** to requested state
8. **Close draft** to create immutable package revision
9. **Return** created package revision

### Draft Creation

```
Repository.CreatePackageRevisionDraft
        ↓
  Allocate Workspace
        ↓
  Initialize Resources
        ↓
  Return Draft Handle
```

**Draft characteristics:**
- **Mutable**: Can be modified before closing
- **Isolated**: Separate workspace from other revisions
- **Temporary**: Exists only during creation workflow
- **Lightweight**: No persistent storage until closed

**Draft operations:**
- UpdateResources: Modify package resources
- UpdateLifecycle: Change lifecycle state
- GetResources: Read current resources
- GetPackageRevision: Get package revision metadata

### Task Application

```
TaskHandler.ApplyTask
        ↓
  Execute Task Type
        ↓
  ┌────┴────┬────────┬─────────┐
  ↓         ↓        ↓         ↓
Init     Clone    Edit    Upgrade
  ↓         ↓        ↓         ↓
  └────┬────┴────────┴─────────┘
       ↓
  Apply Builtin Functions
       ↓
  Return Modified Draft
```

**Task execution:**
- Delegates to task handler for actual work
- Task handler modifies draft resources
- Builtin functions applied (package context, etc.)
- Draft returned with modifications

**Error handling:**
- Task errors trigger rollback
- Draft cleaned up on failure
- No partial package revisions created

### Draft Closure

```
Repository.ClosePackageRevisionDraft
        ↓
  Validate Draft State
        ↓
  Persist to Storage
        ↓
  Create Immutable PR
        ↓
  Return PackageRevision
```

**Closure process:**
- Draft validated before closure
- Resources persisted to repository (Git commit/tag)
- Immutable package revision created
- Draft workspace cleaned up

**Closure failure:**
- Error returned to caller
- No rollback attempted (would likely fail again)
- Draft may remain in repository
- Manual cleanup may be required

## Update Package Revision Workflow

The Engine orchestrates package revision updates through a draft-commit workflow:

### Update Flow

```
UpdatePackageRevision Request
        ↓
  Validate Resource Version
        ↓
  Check Current Lifecycle
        ↓
  Draft/Proposed? ──No──> Metadata Only Update
        │
       Yes
        ↓
  Open Repository
        ↓
  Update to Draft ────────────┐
        ↓                     │
  Apply Mutations             │
        ↓                     │
  Error? ──Yes──> Return Error (draft cleanup?)
        │
        No
        ↓
  Update Lifecycle
        ↓
  Error? ──Yes──> Return Error
        │
        No
        ↓
  Close Draft
        ↓
  Error? ──Yes──> Return Error
        │
        No
        ↓
  Update Metadata
        ↓
  Notify Watchers
        ↓
  Return Updated PR
```

**Process steps:**
1. **Validate resource version** (optimistic locking)
2. **Check current lifecycle** (determines update path)
3. **Open repository** through cache
4. **Update to draft** (opens existing revision as draft)
5. **Apply mutations** through task handler
6. **Update lifecycle** if changed
7. **Close draft** to persist changes
8. **Update metadata** (labels, annotations, finalizers)
9. **Notify watchers** of change
10. **Return** updated package revision

### Draft Opening from Existing Revision

```
Repository.UpdatePackageRevision
        ↓
  Load Existing Revision
        ↓
  Create Draft Workspace
        ↓
  Copy Resources to Draft
        ↓
  Return Draft Handle
```

In this case, the current package revision of the content is loaded and a mutable draft
workspace is created. Resources are copied to draft for modification, while the original revision is preserved (immutable).

**Draft operations:** The same as draft creation, meaning resources, lifecycle and metadata can be modified.
The changes are isolated until closed.

### Mutation Application

```
TaskHandler.DoPRMutations
        ↓
  Compare Old vs New Spec
        ↓
  Identify Changes
        ↓
  New Tasks? ──Yes──> Apply New Tasks
        │
        No
        ↓
  Return Modified Draft
```

**Mutation types:**
- **Task additions**: New tasks appended to task list
- **Lifecycle changes**: Handled separately (UpdateLifecycle)
- **Metadata changes**: Handled after draft closure

Old and new package revision specs are compared and the creation source (init/copy/clone/upgrade) task is applied.
This task is delegated to the task handler for execution after which a modified draft is returned.

### Metadata-Only Update Path

```
UpdatePackageRevision (Published/DeletionProposed)
        ↓
  Lifecycle Changed? ──Yes──> Update Lifecycle
        │
        No
        ↓
  Update Metadata
        ↓
  Notify Watchers
        ↓
  Return Updated PR
```

These are for Published and DeletionProposed package revisions, no draft-commit workflow is needed. This means direct metadata
update on immutable revision. Only labels, annotations, finalizers, owner references are updated.

Published packages are immutable, meaning that the content cannot change.  Metadata, however, does not affect package content.
This allows operational updates without content changes.

## Update Package Resources Workflow

The Engine orchestrates resource updates through a draft-commit workflow:

### Resource Update Flow

```
UpdatePackageResources Request
        ↓
  Get PackageRevision
        ↓
  Validate Resource Version
        ↓
  Check Lifecycle (Draft only)
        ↓
  Open Repository
        ↓
  Update to Draft
        ↓
  Apply Resource Mutations
        ↓
  Execute Render
        ↓
  Error? ──Yes──> Check `porch.kpt.dev/push-on-render-failure` annotation
        │                           ↓
        No                    "true"? ──Yes──> Close Draft + Return Error
        ↓                           │
        │                           No
        │                           ↓
        │                      Return Error (no push)
        ↓
  Close Draft (no lifecycle change)
        ↓
  Return PR + RenderStatus
```

**Process steps:**
1. **Get package revision** from repository object
2. **Validate resource version** (optimistic locking)
3. **Check lifecycle** (must be Draft)
4. **Open repository** through cache
5. **Update to draft** (opens existing revision)
6. **Apply resource mutations** through task handler
7. **Execute render** (run function pipeline)
8. **Check render result**:
   - Success: Close draft and return
   - Failure: Check `porch.kpt.dev/push-on-render-failure` annotation
     - If `"true"`: Close draft (persist resources) and return error
     - Otherwise: Return error without persisting
9. **Return** updated package revision and render status

### Resource Mutation

```
TaskHandler.DoPRResourceMutations
        ↓
  Update Package Resources
        ↓
  Execute Render Task
        ↓
  Run Function Pipeline
        ↓
  Return RenderStatus
```

Package resource content is updated directly and the render task is executed by running KRM functions. Once done,
the render status with function results is returned. This does not involve a lifecycle change, as the resources are updated in-place.

The render is executed by running a configured KRM function pipeline. These functions can validate, transform and generate resources.
The results are returned in a `RenderStatus`.

In case of render failure, the default behavior is for render errors to prevent draft finalization (no resources persisted).
With the `porch.kpt.dev/push-on-render-failure: "true"` annotation, the draft is closed even on render failure. The behavior of
partially-rendered resources can be further controlled via Kptfile annotations
(see [kpt documentation](https://kpt.dev/book/04-using-functions/#debugging-render-failures)). The error is always returned to
caller regardless of persistence behavior.

### Persisting Resources on Render Failure

The `porch.kpt.dev/push-on-render-failure` annotation enables saving work-in-progress packages even when the kpt function render pipeline fails:

**Annotation behavior:**

| PackageRevision Annotation | Kptfile Annotation | Render Result | Behavior |
|----------------------------|-------------------|---------------|----------|
| Not set | Not set | Success | Push rendered resources |
| Not set | Not set | Failure | No push, error returned |
| `"true"` | Not set | Success | Push rendered resources |
| `"true"` | Not set | Failure | Push unrendered resources, error returned |
| `"true"` | `"true"` | Failure | Push partially-rendered resources, error returned |
| `"false"` | Not set | Failure | No push, error returned |
| Not set | `"true"` | Failure | No push, error returned (kpt may have produced partial output internally) |

**How to use:**
```bash
# Add annotation to PackageRevision
kubectl annotate packagerevision <name> porch.kpt.dev/push-on-render-failure=true
```

{{% alert title="Note" color="primary" %}}
- Only applies to Draft PackageRevisions during resource updates (via `UpdatePackageResources`)
- Does not apply to package creation operations (init, clone, edit, copy)
- Error is always returned even when resources are persisted
- The behavior of partially-rendered resources can be further controlled via Kptfile annotations (see [kpt documentation](https://kpt.dev/book/04-using-functions/#debugging-render-failures))
- In rare cases (for example, internal errors during resource persistence), push may be prevented regardless of the annotation
{{% /alert %}}

## Rollback Mechanism

The Engine implements rollback to ensure atomicity on errors:

### Rollback Strategy

```
Operation Error
        ↓
  Rollback Handler Invoked
        ↓
  Close Draft (version=0)
        ↓
  Convert to PackageRevision
        ↓
  Delete PackageRevision
        ↓
  Success? ──No──> Log Warning
        │
       Yes
        ↓
  Return Original Error
```

**Rollback process:**
1. **Error detected** during operation
2. **Rollback handler invoked** automatically
3. **Close draft** with version=0 (converts to package revision)
4. **Delete package revision** from repository
5. **Log warning** if cleanup fails
6. **Return original error** to caller

### Rollback Handler Setup

**Rollback handler creation:**

When a draft is created, the engine sets up a rollback handler that will be invoked if any subsequent operation fails. The handler:

1. **Closes the draft** with a special version indicator (0) to convert it to a package revision
2. **Deletes the package revision** from the repository to clean up
3. **Logs warnings** if cleanup fails (best-effort cleanup)
4. **Captures references** to the draft and repository for later use

The rollback handler is a closure that captures the necessary context and can be invoked automatically when errors occur during the operation.
It captures draft and repository references and logs a warning if a cleanup fails. However, it is non-blocking, meaning it does not prevent error return.

### Rollback Triggers

**Rollback is invoked for:**
- Task application errors
- Lifecycle update errors
- Validation errors during operation
- Any error after draft creation

**Rollback is NOT invoked for:**
- Draft closure errors (would likely fail again)
- Errors before draft creation (nothing to clean up)
- Metadata update errors (draft already closed)

### Rollback Limitations

The rollback may fail if the repository connection is lost, due to permission errors,
if the draft is already closed, or if the repository is in an inconsistent state.

In case of failure, the warning is logged with error details, but the original operation error is still returned.
The draft may remain in the repository, which means that manual cleanup may be required. However, the repository
garbage collection may clean up eventually.

### Rollback vs Transaction

This is not classified as true transaction as there is no two-phase commit and no distributed
transaction support. It is best-effort cleanup only.

Transactions are not used as Git does not support them and repository operations are not transactional.
Additionally, rollback is a cleanup, not an undo.

Atomicity is guaranteed. Package revisions are either created/updated or not. There is no partial package
revisions visible to clients, as draft isolation prevents intermediate state visibility.

## Draft Lifecycle

Drafts have a specific lifecycle within the workflow:

### Draft States

```
┌─────────────┐
│   Created   │ ← CreatePackageRevisionDraft
└──────┬──────┘
       │
       ↓
┌─────────────┐
│  Modified   │ ← ApplyTask, UpdateLifecycle, UpdateResources
└──────┬──────┘
       │
       ↓
┌─────────────┐
│   Closed    │ ← ClosePackageRevisionDraft
└──────┬──────┘
       │
       ↓
┌─────────────┐
│  Immutable  │ ← PackageRevision created
│   Package   │
│  Revision   │
└─────────────┘
```

**Draft lifecycle:**
1. **Created**: Draft allocated with empty or copied resources
2. **Modified**: Changes applied through task handler
3. **Closed**: Draft persisted to repository
4. **Immutable**: Package revision created, draft cleaned up

### Draft Isolation

Isolation guarantees that draft changes are not visible to other operations, since the draft workspace is
separate from other revisions. This means, that multiple drafts can exist simultaneously, in different workspaces.
Draft closure is atomic (all or nothing).

**Concurrency:**
- Multiple drafts for different packages: Fully concurrent
- Multiple drafts for same package: Prevented by workspace name uniqueness
- Draft and read operations: Concurrent (reads don't see draft)

## Workflow Optimization

The Engine optimizes the draft-commit workflow:

### Optimization Strategies

During lazy draft creation, a draft is created only when needed. Drafts are not created for metadata-only updates,
or for read operations.

Early validation is performed before draft creation. It fails fast without expensive operations
and reduces rollback frequency.

Another optimization is efficient draft closure, which uses single repository operation,
atomic commit to Git and minimal overhead.

### Performance Characteristics

The draft creation cost is relatively lightweight. The workspace is allocated in the repository and
the resources are copied in case of updates.

Draft modification costs consist of in-memory operations. During modifications, the repository is not
accessed, but the task handler works.

The most expensive part of the workflow is the draft closure. It contains Git commit and tag creation,
as well as repository write operations.

Rollback costs only appear on error path. This consists of two repository operations, draft closure
and deletion.

## Error Handling

The Engine handles errors at each workflow stage:

### Error Categories

**Pre-draft errors:**
- Validation failures
- Repository access errors
- No rollback needed (no draft created)

**Draft-phase errors:**
- Task execution failures
- Lifecycle update failures
- Rollback invoked (draft cleanup)

**Closure errors:**
- Repository write failures
- Git operation failures
- No rollback (would likely fail again)

**Post-closure errors:**
- Metadata update failures
- Watcher notification failures
- Operation considered successful (package revision created)

### Error Recovery

**Client retry:**
- Optimistic locking errors: Re-read and retry
- Transient errors: Retry with backoff
- Validation errors: Fix input and retry

**Automatic recovery:**
- Rollback on draft-phase errors
- Repository garbage collection for orphaned drafts
- Cache refresh for stale data

**Manual recovery:**
- Rollback failures may require manual cleanup
- Repository inspection and repair
- Draft deletion through repository tools
