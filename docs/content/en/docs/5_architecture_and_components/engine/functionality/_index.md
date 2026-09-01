---
title: "Engine Functionality"
type: docs
weight: 3
description: |
  Overview of CaDEngine functionality and detailed documentation pages.
---

The Engine provides five core functional areas that work together to manage the complete lifecycle of package revisions:

## Functional Areas

### Lifecycle Management

Enforces package lifecycle state machine and business rules through:
- **State Transitions**: Draft → Proposed → Published → DeletionProposed
- **State-Based Constraints**: Different mutability rules per lifecycle state
- **Validation**: Creation, update, transition, and resource update validation
- **Audit Tracking**: PublishedBy, PublishedAt, tasks history, and resource versions

For detailed architecture and process flows, see [Lifecycle Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/lifecycle-management.md" %}}).

### Validation & Business Rules

Validates operations and enforces business rules to ensure correctness through:
- **Lifecycle Validation**: Cannot create Published/DeletionProposed packages
- **Task Validation**: Maximum one task during creation, defaults to init
- **Workspace Name Uniqueness**: Prevents duplicate workspace names per package
- **Package Name Validation**: Prevents invalid package names
- **Clone Validation**: Clone can only create new packages, not new revisions
- **Upgrade Validation**: Source revisions must be published
- **Package Path Overlap**: Prevents nested package structures
- **Optimistic Locking**: Resource version checking for concurrent modification prevention

For detailed architecture and process flows, see [Validation & Business Rules]({{% relref "/docs/5_architecture_and_components/engine/functionality/validation-business-rules.md" %}}).

### Draft-Commit Workflow Orchestration

Orchestrates the draft-commit pattern for all package revision modifications through:
- **Draft Phase**: Package revisions are mutable and changes are applied
- **Commit Phase**: Draft closed to create immutable package revision
- **Rollback Mechanism**: Automatic cleanup on errors to ensure atomicity
- **Atomicity**: Either all changes succeed or all fail
- **Isolation**: Draft changes don't affect other revisions

For detailed architecture and process flows, see [Draft-Commit Workflow Orchestration]({{% relref "/docs/5_architecture_and_components/engine/functionality/draft-commit-orchestration.md" %}}).

### Task Coordination

Coordinates task execution by delegating to the Task Handler through:
- **ApplyTask**: Execute task during package revision creation (init, clone, edit, upgrade)
- **DoPRMutations**: Apply mutations during package revision update
- **DoPRResourceMutations**: Apply resource mutations and execute render
- **Function Runtime Integration**: Builtin, Function Runner (exec via `exec_path`), and pod evaluator
- **Error Handling**: Task errors trigger rollback and cleanup

For detailed architecture and process flows, see [Task Coordination]({{% relref "/docs/5_architecture_and_components/engine/functionality/task-coordination.md" %}}).

### Function Evaluation

Owns the function runtime chain used by both the v1alpha1 Task Handler and the v1alpha2 PackageRevision controller renderer:
- **Multi-runtime**: builtin → Function Runner (cached binaries via `exec_path`) → pod evaluator
- **Pod Evaluator**: In-process pod cache, wrapper-server gRPC, TTL/GC
- **Image and registry**: Digest/entrypoint cache and private registry auth for function pods

For the runtime chain, see [Function Evaluation]({{% relref "/docs/5_architecture_and_components/engine/functionality/function-evaluation.md" %}}). For pods, see [Pod Lifecycle Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/pod-lifecycle-management.md" %}}). For registries, see [Image and Registry Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/image-registry-management.md" %}}).

## How They Work Together

```
┌─────────────────────────────────────────────────────────┐
│                      Engine                             │
│                                                         │
│  ┌──────────────────┐      ┌──────────────────┐         │
│  │   Validation &   │      │   Lifecycle      │         │
│  │  Business Rules  │ ───> │   Management     │         │
│  │                  │      │                  │         │
│  │  • Lifecycle     │      │  • Transitions   │         │
│  │  • Tasks         │      │  • Constraints   │         │
│  │  • Workspace     │      │  • Enforcement   │         │
│  │  • Clone/Upgrade │      │                  │         │
│  │  • Path Overlap  │      │                  │         │
│  │  • Opt. Locking  │      │                  │         │
│  └──────────────────┘      └──────────────────┘         │
│           │                         │                   │
│           └────────┬────────────────┘                   │
│                    ↓                                    │
│  ┌──────────────────┐      ┌──────────────────┐         │
│  │  Draft-Commit    │      │      Task        │         │
│  │  Orchestration   │ ───> │  Coordination    │         │
│  │                  │      │                  │         │
│  │  • Open Draft    │      │  • ApplyTask     │         │
│  │  • Apply Changes │      │  • DoPRMutations │         │
│  │  • Close Draft   │      │  • DoPRResource  │         │
│  │  • Rollback      │      │    Mutations     │         │
│  └──────────────────┘      └──────────────────┘         │
└─────────────────────────────────────────────────────────┘
```

**Integration flow:**
1. **Validation & Business Rules** validate operations before execution
2. **Lifecycle Management** enforces state machine rules and constraints
3. **Draft-Commit Orchestration** manages the mutable draft workflow with rollback
4. **Task Coordination** delegates to task handler for package transformations
5. **Function Evaluation** runs the Kptfile pipeline (builtin, Function Runner exec, pod evaluator)

Each functional area is documented in detail on its own page with architecture diagrams, process flows, and implementation specifics.

## What the Engine Does NOT Do

The Engine does **not** implement package/package revision CRUD operations - it delegates to repository adapters:

**Delegation operations:**
- **ListPackageRevisions**: Opens repo through cache → delegates to `repo.ListPackageRevisions`
- **ListPackages**: Opens repo through cache → delegates to `repo.ListPackages`
- **CreatePackage**: Opens repo through cache → delegates to `repo.CreatePackage`
- **DeletePackage**: Opens repo through cache → delegates to `repo.DeletePackage`
- **DeletePackageRevision**: Opens repo through cache → delegates to `repo.DeletePackageRevision`

These are thin wrappers that open the repository through the cache and delegate to repository adapters. The actual storage operations (Git commits, tags, branches) are handled by repository adapters, not the Engine.

The Engine's real work is **orchestration, validation, lifecycle enforcement, task coordination, and function evaluation** - not storage operations.
