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

- **Function Execution**: Running KRM functions in isolated pods or as local executables
- **Pod Lifecycle Management**: Creating, caching, and garbage collecting function execution pods
- **Image Management**: Caching image metadata and handling private registry authentication
- **Service Mesh Compatibility**: Using ClusterIP services as frontends for function pods
- **Resource Isolation**: Ensuring functions execute in separate environments with controlled resources

## Role in the Architecture

The Function Runner sits as a separate service that the Task Handler communicates with via gRPC:

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
2. **Multiple Evaluator Strategies**: Supports pod-based execution (default), executable evaluation (fast path), and multi-evaluator chaining with fallback
3. **Pod-Based Execution Infrastructure**: Creates and manages function execution pods with TTL-based caching, garbage collection, and ClusterIP service frontends
4. **Image and Registry Integration**: Caches image metadata, supports private registries with authentication, and handles TLS certificates
5. **gRPC Communication Protocol**: Exposes FunctionEvaluator service accepting serialized ResourceList and returning transformed resources
6. **Wrapper Server Pattern**: Injects wrapper-server binary into function pods to provide gRPC interface and structured result handling

The Function Runner is instantiated as a separate deployment and configured with evaluator types (pod, executable, or both) through command-line flags.
