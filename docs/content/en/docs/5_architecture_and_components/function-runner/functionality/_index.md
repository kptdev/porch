---
title: "Function Runner Functionality"
type: docs
weight: 3
description: |
  Overview of function runner functionality and detailed documentation pages.
---

The Function Runner provides executable evaluation of cached KRM function binaries (`exec_path`). Pod lifecycle and image/registry management moved to the Engine; the pages below still describe those areas and now live under Engine.

## Functional Areas

### Function Evaluation

Executes KRM functions through pluggable evaluator strategies:
- **Evaluator Interface**: Common contract for all function execution strategies
- **Pod Evaluator**: Executes functions in Kubernetes pods with wrapper server integration
- **Executable Evaluator**: Runs pre-cached function binaries using `exec_path` from the Engine
- **Multi-Evaluator / Engine chain**: Engine tries builtin → Function Runner exec → pod evaluator
- **Request Channel Pattern**: Channel-based communication for pod cache coordination
- **Wrapper Server Integration**: gRPC wrapper injected into function pods for structured execution

For detailed architecture and process flows, see [Function Evaluation]({{% relref "/docs/5_architecture_and_components/function-runner/functionality/function-evaluation.md" %}}).

### Pod Lifecycle Management

Manages function execution pods with caching and garbage collection:
- **Pod Cache Manager**: Orchestrates pod lifecycle via channel-based communication
- **Pod Manager**: Handles pod and service CRUD operations
- **Pod Creation**: Template-based pod creation with init container for wrapper server injection
- **Service Management**: ClusterIP service frontends for service mesh compatibility
- **TTL-Based Caching**: Reuses pods with configurable expiration and extension on use
- **Garbage Collection**: Periodic cleanup of expired pods and failed pod handling
- **Pod Warming**: Pre-creates pods for frequently-used functions

For detailed architecture and process flows, see [Pod Lifecycle Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/pod-lifecycle-management.md" %}}).

### Image and Registry Management

Caches image metadata and handles private registry authentication:
- **Metadata Caching**: In-memory cache of image digests and entrypoints
- **Image Inspection**: Fetches manifests and configs from container registries
- **Private Registry Support**: Authentication using Docker config format
- **TLS Configuration**: Custom CA certificates for secure registry connections
- **Secret Management**: Creates and attaches image pull secrets to function pods
- **Registry Operations**: Handles manifest retrieval, authentication retry, and error handling

For detailed architecture and process flows, see [Image and Registry Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/image-registry-management.md" %}}).

## How They Work Together

```
┌─────────────────────────────────────────────────────────┐
│              Function Runner Service                    │
│                                                         │
│  ┌──────────────────┐      ┌──────────────────┐         │
│  │    Function      │      │      Pod         │         │
│  │   Evaluation     │ ───> │    Lifecycle     │         │
│  │                  │      │   Management     │         │
│  │  • Evaluator     │      │                  │         │
│  │    Selection     │      │  • Pod Cache     │         │
│  │  • Exec/Pod      │      │  • Pod Manager   │         │
│  │    Fallback      │      │  • GC/TTL        │         │
│  │  • Wrapper       │      │  • Service Mgmt  │         │
│  │    Server        │      │                  │         │
│  └──────────────────┘      └──────────────────┘         │
│           │                         │                   │
│           └────────┬────────────────┘                   │
│                    ↓                                    │
│          ┌──────────────────┐                           │
│          │     Image &      │                           │
│          │    Registry      │                           │
│          │   Management     │                           │
│          │                  │                           │
│          │  • Metadata      │                           │
│          │    Cache         │                           │
│          │  • Registry      │                           │
│          │    Auth          │                           │
│          │  • TLS Config    │                           │
│          │  • Pull Secrets  │                           │
│          └──────────────────┘                           │
└─────────────────────────────────────────────────────────┘
```

**Integration flow:**
1. **Function Evaluation** receives gRPC request from Task Handler
2. **Multi-Evaluator** tries executable evaluator first (fast path)
3. **If NotFound**, falls back to pod evaluator (container execution)
4. **Pod Lifecycle Management** checks pod cache for existing pod
5. **If cache miss**, creates new pod with wrapper server via Pod Manager
6. **Image & Registry Management** resolves image metadata and authentication
7. **Pod Manager** creates pod with image pull secrets and service frontend
8. **Pod Cache Manager** stores pod with TTL for reuse
9. **Function Evaluation** connects to pod via service and executes function
10. **Wrapper Server** executes function binary and returns structured results
11. **Garbage Collection** periodically removes expired pods from cache

Each functional area is documented in detail on its own page with architecture diagrams, process flows, and implementation specifics.
