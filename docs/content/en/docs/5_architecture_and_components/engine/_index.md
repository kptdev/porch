---
title: "Engine"
type: docs
weight: 3
description: |
  The CaD Engine that orchestrates package lifecycle operations.
---

## What is the Engine?

The **Engine** (also called the **CaD Engine** - Configuration as Data Engine) is the central orchestration component in Porch that manages the complete lifecycle of package revisions and packages. It acts as the coordination layer between the API server, package cache, repository adapters, and task execution pipeline.

The Engine is responsible for:

- **Package Revision Lifecycle Management**: Creating, updating, and deleting package revisions while enforcing lifecycle state transitions (Draft → Proposed → Published → DeletionProposed)
- **Task Orchestration**: Coordinating the execution of package tasks (init, clone, render, upgrade, edit) through the task handler
- **Repository Operations**: Opening repositories from the cache and delegating package operations to the appropriate repository adapter
- **Validation and Constraints**: Enforcing business rules like workspace name uniqueness, lifecycle constraints, and package path validation
- **Draft Management**: Managing the draft-commit workflow where changes are made to drafts and then closed to create immutable package revisions
- **Function Evaluation**: Running KRM functions through a multi-runtime (builtin, Function Runner exec via `exec_path`, in-process pod evaluator)
- **Change Notification**: Notifying watchers of package revision changes for real-time updates

## Role in the Architecture

The Engine sits between the Porch API Server and the lower-level components:

![Engine Architecture](/static/images/porch/porch-engine-architecture.drawio.svg)

**Key architectural responsibilities:**

1. **Abstraction Layer**: Provides a clean interface (CaDEngine) that hides the complexity of cache management, repository operations, and task execution from the API server

2. **Workflow Orchestration**: Implements the package revision workflow:
   - Create draft → Apply tasks → Update lifecycle → Close draft → Persist to repository
   - Handles rollback on errors to maintain consistency

3. **State Management**: Enforces package lifecycle state machine rules, ensuring packages can only transition through valid states

4. **Integration Point**: Connects multiple subsystems:
   - Uses the **Package Cache** to access repositories
   - Invokes the **Task Handler** to execute package operations
   - Notifies the **Watcher Manager** of changes for API watch streams
   - Delegates to **Repository Adapters** for storage operations

5. **Validation Gateway**: Validates all package operations before execution, including workspace name uniqueness, lifecycle constraints, and task-specific validations

6. **Function evaluation and pod lifecycle**: The Engine hosts the function runtime chain (builtin → Function Runner exec → pod evaluator). The following diagram is the former Function Runner architecture drawing; those boxes now run in-process in porch-server and the PackageRevision controller:

```
┌─────────────────────────────────────────────────────────┐
│     Engine — pod evaluator (porch-server / PR ctrl)     │
│                                                         │
│  ┌──────────────────┐      ┌──────────────────┐         │
│  │   gRPC Server    │      │   Evaluators     │         │
│  │                  │ ───> │                  │         │
│  │  • FunctionEval  │      │  • Pod Evaluator │         │
│  │    Service       │      │  • Exec Evaluator│         │
│  │  • Health Check  │      │  • Multi-Eval    │         │
│  └────────┬─────────┘      └────────┬─────────┘         │
│           │                         │                   │
│           └────────┬────────────────┘                   │
│                    ↓                                    │
│  ┌──────────────────┐      ┌──────────────────┐         │
│  │  Pod Lifecycle   │      │  Image & Registry│         │
│  │   Management     │      │   Management     │         │
│  │                  │      │                  │         │
│  │  • Pod Cache     │      │  • Metadata Cache│         │
│  │  • Pod Manager   │      │  • Auth & TLS    │         │
│  │  • GC & TTL      │      │  • Pull Secrets  │         │
│  └────────┬─────────┘      └────────┬─────────┘         │
│           │                         │                   │
│           └────────┬────────────────┘                   │
│                    ↓                                    │
│         ┌──────────────────────┐                        │
│         │   Kubernetes API     │                        │
│         │   & Registries       │                        │
│         └──────────────────────┘                        │
└─────────────────────────────────────────────────────────┘
                    ↑
                    │
            Task Handler / kpt Renderer
```

The Engine is instantiated once during Porch API server startup and configured with dependencies (cache, task handler, function runtimes, credential resolvers) through a functional options pattern. The same multi-runtime constructor is used by the PackageRevision controller for v1alpha2 renders.
