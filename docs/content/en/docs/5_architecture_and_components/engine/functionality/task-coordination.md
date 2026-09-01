---
title: "Task Coordination"
type: docs
weight: 4
description: |
  Detailed architecture of task handler integration and coordination.
---

## Overview

The Engine coordinates task execution by delegating to the Task Handler. Tasks represent operations that transform package content (init, clone, edit, upgrade, render). The Engine orchestrates when and how tasks are executed, while the Task Handler implements the actual transformations.

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│              Task Coordination System                   │
│                                                         │
│  ┌──────────────────┐      ┌──────────────────┐         │
│  │      Engine      │      │  Task Handler    │         │
│  │  Orchestration   │ ───> │  Execution       │         │
│  │                  │      │                  │         │
│  │  • When to Run   │      │  • Init          │         │
│  │  • Draft Mgmt    │      │  • Clone         │         │
│  │  • Error Handle  │      │  • Edit          │         │
│  └──────────────────┘      │  • Upgrade       │         │
│           │                │  • Render        │         │
│           │                └──────────────────┘         │
│           ↓                         │                   │
│  ┌──────────────────┐               ↓                   │
│  │   Function       │      ┌──────────────────┐         │
│  │   Runtime        │ <─── │  Builtin Funcs   │         │
│  │                  │      │                  │         │
│  │  • gRPC          │      │  • set-namespace │         │
│  │  • Builtin       │      │  • ensure-context│         │
│  └──────────────────┘      └──────────────────┘         │
└─────────────────────────────────────────────────────────┘
```

## Task Handler Integration

The Engine integrates with the Task Handler through three main operations:

### Integration Points

```
   Engine                     Task Handler
     ↓                              ↓
CreatePackageRevision ──────> ApplyTask
     ↓                              ↓
UpdatePackageRevision ──────> DoPRMutations
     ↓                              ↓
UpdatePackageResources ─────> DoPRResourceMutations
```

**Three integration points:**
1. **ApplyTask**: Execute task during package revision creation
2. **DoPRMutations**: Apply mutations during package revision update
3. **DoPRResourceMutations**: Apply resource mutations during resource update

The Engine is responsible for determining when to invoke the task handler, providing draft workspace for modifications,
handling errors and rollback, and managing lifecycle transitions.

The Task Handler is responsible for executing task transformations, modifying draft resources, applying builtin functions,
and returning results or errors.

## ApplyTask - Creation Task Execution

The Engine invokes ApplyTask during package revision creation:

### ApplyTask Flow

```
CreatePackageRevision
        ↓
  Create Draft
        ↓
  ApplyTask(draft, repo, pr, config)
        ↓
  Task Handler
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
        ↓
  Engine
        ↓
  Update Lifecycle
        ↓
  Close Draft
```

**Process:**
1. **Engine creates draft** (mutable workspace)
2. **Engine invokes ApplyTask** with:
   - Draft to modify
   - Repository object for context
   - PackageRevision spec with task definition
   - Package configuration (path, name, etc.)
3. **Task Handler executes task** based on type
4. **Task Handler applies builtin functions** (package context generation)
5. **Task Handler returns** modified draft or error
6. **Engine updates lifecycle** if successful
7. **Engine closes draft** to create package revision

### ApplyTask Parameters

Draft is a mutable workspace for modifications. It provides UpdateResources, GetResources methods, and it
is isolated from other revisions.

Repository Object is the Repository Custom Resource specification. It is used for context (repository name, namespace).
This parameter is passed to task implementations.

PackageRevision contains task specification in `Spec.Tasks`. The first task in the list is executed. Task type
determines which implementation runs.

Package Config contains package path, name, and workspace. This is an upstream reference for clone/upgrade.
Contains additional metadata.

### Task Execution

Task type determines which implementation runs (init, clone, edit, upgrade). Task handler modifies draft resources based on task logic,
and builtin functions are applied after task execution. The modified drafts are returned to Engine.

| Task type | Action |
|-----------|--------|
| init | Initialize new, blank package |
| clone | Copy package from an existing, separate upstream |
| edit | Create new revision within an existing package |
| upgrade | Merge changes from a new version of the upstream |

**Handback to Engine:**
- **Success**: Returns modified draft
- **Error**: Returns error, triggers rollback

## DoPRMutations - Update Mutations

The Engine invokes DoPRMutations during package revision updates:

### DoPRMutations Flow

```
UpdatePackageRevision
        ↓
  Open Draft from Existing
        ↓
  DoPRMutations(repoPR, oldObj, newObj, draft)
        ↓
  Task Handler
        ↓
  Compare Task Lists
        ↓
  New Tasks? ──No──> Return Draft
        │
       Yes
        ↓
  Apply New Tasks
        ↓
  Return Modified Draft
        ↓
  Engine
        ↓
  Update Lifecycle
        ↓
  Close Draft
```

**Process:**
1. **Engine opens draft** from existing package revision
2. **Engine invokes DoPRMutations** with:
   - Repository package revision (for context)
   - Old PackageRevision spec
   - New PackageRevision spec
   - Draft to modify
3. **Task Handler compares** old and new task lists
4. **Task Handler applies** any new tasks
5. **Task Handler returns** modified draft or error
6. **Engine updates lifecycle** if changed
7. **Engine closes draft** to persist changes

### DoPRMutations Parameters

Repository PackageRevision is the current package revision from the repository. It provides context for mutations,
but is not directly modified, the draft is.

Old PackageRevision is the previous PackageRevision spec. It is used for comparison, as it identifies what changed.

New PackageRevision is the desired PackageRevision spec. It contains new tasks to apply to reach the target state.

Draft is a mutable workspace for modifications. It already contains the current package content and it is modified by
new tasks.

### Task Comparison

```
Compare Task Lists
        ↓
  Old Task: [init, clone]
  New Task: [init, clone, render]
        ↓
  Identify New Task: [render]
        ↓
  Apply New Task
```

**Comparison logic:** The `tasks` list comprises a single, immutable task that identifies the source of the package.
It has remained a list for backwards compatibility but will be replaced in the future.

**Task application:** The task is executed when the revision is created. In case of an error, the process stops and returns.

## DoPRResourceMutations - Resource Updates

The Engine invokes DoPRResourceMutations during resource updates:

### DoPRResourceMutations Flow

```
UpdatePackageResources
        ↓
  Open Draft from Existing
        ↓
  DoPRResourceMutations(pr, draft, oldRes, newRes)
        ↓
  Task Handler
        ↓
  Update Package Resources
        ↓
  Execute Render Task
        ↓
  Run Function Pipeline
        ↓
  Return RenderStatus
        ↓
  Engine
        ↓
  Close Draft (no lifecycle change)
```

**Process:**
1. **Engine opens draft** from existing package revision
2. **Engine invokes DoPRResourceMutations** with:
   - Package revision (for context)
   - Draft to modify
   - Old PackageRevisionResources
   - New PackageRevisionResources
3. **Task Handler updates** package resources in draft
4. **Task Handler executes render** (runs function pipeline)
5. **Task Handler returns** RenderStatus with function results
6. **Engine closes draft** without lifecycle change

### DoPRResourceMutations Parameters

PackageRevision is the current package revision from the repository, which provides context for mutations.
It contains the function pipeline configuration.

Draft is a mutable workspace for modifications. The resources are updated directly and it is modified by
resource mutations (including the render task).

Old PackageRevisionResources is the previous resource content, which is used for comparison (not currently used).
It has an audit trail.

New PackageRevisionResources is the desired resource content, which is applied to the draft. It is the target state.

### Render Execution

The Task Handler updates package resources in draft and executes render task, which means running a function pipeline.
Once done, RenderStatus is returned with function results.

**Handback to Engine:**
- **RenderStatus**: Contains function execution results
  - Result: Overall success/failure
  - Error: Error message if failed
  - Exit code: Function exit codes
  - Function details: Per-function results
- **Modified draft**: Resources updated by render task

## Task Handler Configuration

The Engine configures the Task Handler during initialization:

### Task Handler Setup

```
NewCaDEngine(opts...)
        ↓
  Create cadEngine
        ↓
  taskHandler = GetDefaultTaskHandler()
        ↓
  Apply Options:
        ↓
    • Function Runtimes
    • Credential Resolvers
    • Reference Resolvers
        ↓
  Return Configured Engine
```

**Configuration options:**
- **Function runtimes**: Builtin, gRPC, or multi-runtime
- **Credential resolver**: For accessing upstream packages
- **Reference resolver**: For resolving package references
- **User info provider**: For audit trails

### Function Runtime Configuration

**Runtime types:**
- **Builtin Runtime**: For built-in functions (set-namespace, etc.)
- **gRPC Runtime**: For Function Runner cached binaries (`exec_path`)
- **Pod evaluator**: In-process when `WRAPPER_SERVER_IMAGE` is set
- **Multi-Runtime**: Chains builtin → Function Runner → pod evaluator

Runtime selection is configured at porch-server startup (`--function-runner`, `WRAPPER_SERVER_IMAGE`, pod-evaluator flags) and passed to the task handler. The PackageRevision controller builds the same chain from `FUNCTION_RUNNER_ADDRESS` and `WRAPPER_SERVER_IMAGE`.

For details on function runtime implementations, see [Function Evaluation]({{% relref "/docs/5_architecture_and_components/engine/functionality/function-evaluation.md" %}}) and [Function Runner Design]({{% relref "/docs/5_architecture_and_components/function-runner/design.md" %}}).

## Error Handling

The Engine handles task execution errors:

### Task Error Flow

```
ApplyTask/DoPRMutations/DoPRResourceMutations
        ↓
  Task Handler Executes
        ↓
  Error? ──No──> Return Success
        │
       Yes
        ↓
  Return Error
        ↓
  Engine
        ↓
  Rollback (if creation)
        ↓
  Return Error to Client
```

**Error handling:**
- Task errors returned to Engine
- Engine triggers rollback (for creation)
- Error propagated to client
- No partial package revisions created

### Error Types

**Task execution errors:**
- Invalid task configuration
- Upstream package not found
- Merge conflicts (upgrade)
- Function execution failures

**Function errors:**
- Function image not found
- Function execution timeout
- Function validation failures
- Function runtime errors

**Resource errors:**
- Invalid YAML syntax
- Missing required fields
- Schema validation failures

### Error Recovery

One option is client retry, which means fixing task configuration, resolving upstream references, fixing resource
syntax, and then retrying the operation.

Automatic recovery includes rollback on creation errors, as well as draft cleanup. No manual intervention needed

## Task Coordination Patterns

The Engine uses specific patterns for task coordination:

### Task Execution

```
Task List: [init/clone/edit/upgrade]
        ↓
  Execute [init/clone/edit/upgrade]
        ↓
  Success? ──No──> Return Error
        │
       Yes
        ↓
  Return Success
```

The one task from the task list is applied and executed. The first error stops execution.

### Task List Pattern

```
Create: [init]
        ↓
Update: [clone/edit/upgrade]
        ↓
Update: [render]
```

A single persistent task which indicates [init/clone/edit/upgrade] method. The task history shows package origin.

### Draft Isolation

```
Package Revision A          Package Revision B
        ↓                           ↓
    Draft A                     Draft B
        ↓                           ↓
  Task Execution            Task Execution
        ↓                           ↓
   Independent                 Independent
```

Isolation guarantees that each draft has a separate context, that task execution does not affect other drafts, and
that there is no shared state between tasks. With draft isolation, concurrent task execution is possible on different packages.

## Task Handler Interface

The Engine interacts with Task Handler through a defined interface:

### Interface Methods

**ApplyTask:**
- Accepts: context, draft workspace, repository object, package revision spec, package configuration
- Returns: error on failure
- Purpose: Execute task during package revision creation

**DoPRMutations:**
- Accepts: context, repository package revision, old spec, new spec, draft workspace
- Returns: error on failure
- Purpose: Apply mutations during package revision update

**DoPRResourceMutations:**
- Accepts: context, package revision, draft workspace, old resources, new resources
- Returns: render status and error
- Purpose: Apply resource mutations and execute render task

### Interface Characteristics

The interface is context-aware. All methods accept context for cancellation. Timeout and deadline is supported.
Tracing and logging context is propagated as well.

It is draft-based, meaning that all methods work with draft workspaces and no direct repository modification
is performed. Includes isolation and atomicity.

Errors indicate task failure. The Engine handles rollback and gives clear error messages for debugging.

## Task Coordination Benefits

The task coordination pattern provides several benefits:

### Separation of Concerns

The Engine orchestrates the workflow, manages drafts and lifecycle, handles errors and rollback, and
enforces business rules.

The Task Handler implements task logic, transforms package content, executes functions, and
returns results.

The benefit of this separation is that both component have clear responsibilities. Testing and maintenance are easier, and
pluggable task implementations can be applied. Additionally, evolution of the components is independent.

### Extensibility

**New task types:**
- Implement in task handler
- Engine unchanged
- Register with task handler
- Available for use

**New function runtimes:**
- Implement runtime interface
- Configure at startup
- Task handler uses new runtime
- No CaDEngine changes

### Testability

You can test the engine with mock task handler. Orchestration logic, error handling, and rollback mechanism can be tested.

You can test the Task Handler with mock draft interface. Task implementations and function execution can be tested. This is
independent of CaDEngine.
