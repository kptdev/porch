---
title: "Function Runner"
type: docs
weight: 5
description: |
  Separate gRPC service for executing KRM functions in isolated environments.
---

## What is the Function Runner?

The **Function Runner** is a standalone gRPC service that executes KRM (Kubernetes Resource Model) functions in isolated environments. It provides the runtime infrastructure for executing user-defined functions that mutate, validate, and generate Kubernetes configuration resources.

The Function Runner is responsible for:

- **Function Execution**: Running cached KRM function binaries over gRPC using `exec_path` supplied by the Engine. Arbitrary function images are evaluated by the **pod evaluator** in the Engine (porch-server and the PackageRevision controller).
- **Pod Lifecycle Management**, **Image Management**, and **Service Mesh Compatibility**: These still work as shown in the diagram below, but they now run in the Engine. See [Engine Function Evaluation]({{% relref "/docs/5_architecture_and_components/engine/functionality/function-evaluation.md" %}}), [Pod Lifecycle Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/pod-lifecycle-management.md" %}}), and [Image and Registry Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/image-registry-management.md" %}}).
- **Resource Isolation**: Cached binaries run in the Function Runner process; other functions run in Engine-managed pods.

## Role in the Architecture

The Function Runner sits as a separate service that the Task Handler (and the PackageRevision controller, when `FUNCTION_RUNNER_ADDRESS` is set) communicates with via gRPC. The pod evaluator, pod lifecycle, and image/registry boxes in this diagram now run **inside the Engine**; Function Runner itself hosts the gRPC server and the executable evaluator (`exec_path`).

```
┌─────────────────────────────────────────────────────────┐
│              Function Runner Service                    │
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
            gRPC Connection
                    │
                    ↓
         ┌──────────────────────┐
         │   Task Handler       │
         │   (in Porch)         │
         └──────────────────────┘
```

**Key architectural responsibilities:**

1. **Separate Service Deployment**: Runs independently from Porch server, enabling independent scaling, isolation, and separate failure domains
2. **Executable evaluation (fast path)**: Runs pre-cached function binaries using `exec_path` from the Engine. The Engine falls through to the in-process pod evaluator when no binary is cached
3. **gRPC Communication Protocol**: Exposes FunctionEvaluator service accepting serialized ResourceList (and `exec_path`) and returning transformed resources
4. **Pod-Based Execution Infrastructure**, **Image and Registry Integration**, and **Wrapper Server Pattern**: Now implemented in the Engine (see the Pod Lifecycle box in the diagram and the [Engine]({{% relref "/docs/5_architecture_and_components/engine" %}}) pages)

The Function Runner is instantiated as a separate deployment. Porch-server reaches it with `--function-runner`. The only runtime in this binary is `exec` (`--disable-runtimes` accepts `exec`).
