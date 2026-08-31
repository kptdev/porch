---
title: "Function Runner Interactions"
type: docs
weight: 4
description: |
  How the function runner integrates with Porch and external systems.
---

## Overview

The Function Runner is a **separate gRPC service** that interacts with multiple systems:
the Task Handler (via gRPC), Kubernetes API (for pod management, FunctionConfig objects, and pod/service templates),
container registries (for image metadata), and wrapper servers (for function execution).
It operates independently from the Porch server, enabling isolated function execution.

An embedded FunctionConfig reconciler watches FunctionConfig objects in the function-pod namespace and fills an in-memory store.
That store is how the executable evaluator resolves binaries and how the pod evaluator applies per-image TTL, parallelism, and template overrides.
Go execution is not handled here; it runs in porch-server / porch-controllers.
See [Function Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/components/function-runner-config/function-configuration.md" %}}).

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│              Function Runner Service                    │
│                                                         │
│  ┌──────────────┐      ┌──────────────┐      ┌──────┐   │
│  │ Task Handler │ ───> │    gRPC      │ ───> │ Eval │   │
│  │  (in Porch)  │      │   Server     │      │uators│   │
│  └──────────────┘      └──────────────┘      └──────┘   │
│         ↑                      │                │       │
│         │                      ↓                ↓       │
│         │              ┌──────────────┐      ┌──────┐   │
│         └──────────────│  Kubernetes  │      │Image │   │
│                        │     API      │      │Cache │   │
│                        └──────────────┘      └──────┘   │
│                               │                │        │
│                               ↓                ↓        │
│                        ┌──────────────┐    ┌─────────┐  │
│                        │ Function Pods│    │Registry │  │
│                        │  + Services  │    │  APIs   │  │
│                        └──────────────┘    └─────────┘  │
└─────────────────────────────────────────────────────────┘
```

## FunctionConfig reconciler

The function-runner starts a controller-runtime manager whose cache is limited to `--pod-namespace` (default `porch-fn-system`).
The FunctionConfig reconciler (`ReconcilerForFunctionRunner`) upserts each object into `FunctionConfigStore`, refreshes the binary cache when `binaryExecutor` is set, and writes `status.functionRunnerObservedGeneration`.
On delete it drops the store entry and removes its finalizer.

At evaluation time the executable evaluator looks up a binary by image name, prefix, and tag (or the best tag matching a version constraint).
A miss is `NotFoundError`, which the multi-evaluator treats as a signal to try the pod evaluator.
The pod evaluator reads `podExecutor` from the same store for TTL, waitlist length, max parallel pods, and `templateOverrides`.

The Engine's builtin Go runtime is a different reconciler instance, running inside porch-server (and porch-controllers).
Function-runner never executes Go processors.

## Task Handler Integration

The Function Runner integrates with the Task Handler through the gRPC Runtime:

### gRPC Runtime Pattern

```
Task Handler
        ↓
  gRPC Runtime (in Engine)
        ↓
  Persistent Connection
        ↓
Function Runner Service
        ↓
  Evaluator Selection
        ↓
  Function Execution
```

**Integration characteristics:**
- Task Handler uses gRPC Runtime to communicate with Function Runner
- Single persistent connection shared across all function executions
- Connection established at Porch startup, closed on shutdown
- Function Runner address configured via `--function-runner-address` flag

**Request-response flow:**
- Task Handler serializes ResourceList as YAML
- gRPC Runtime sends EvaluateFunctionRequest
- Function Runner selects appropriate evaluator (exec or pod)
- Response includes transformed ResourceList and function logs
- NotFoundError triggers fallback to next evaluator in multi-evaluator chain

### Render Pipeline Invocation

The Task Handler invokes Function Runner during render operations:

```
Task Handler
        ↓
  Execute Render Task
        ↓
  For Each Function in Kptfile:
        ↓
    Call EvaluateFunction
        ↓
    Function Runner Executes
        ↓
    Return Transformed Resources
        ↓
  Continue Pipeline
```

**Pipeline characteristics:**
- Sequential execution (one function at a time)
- Each function sees output of previous function
- First error stops pipeline
- Results aggregated into RenderStatus by Task Handler

### Message Size Configuration

Both Porch and Function Runner must agree on message size limits:

**Configuration synchronization:**
- Porch server: `--function-runner-grpc-max-message-size` flag
- Function Runner: `--max-request-body-size` flag
- Both must be set to same value (default: 6MB)
- Mismatched limits cause cryptic gRPC errors

**Why coordination matters:**
- Large packages with many resources exceed default gRPC limits
- ResourceList serialized as YAML can be large
- Must be coordinated during deployment

## Evaluator Execution Patterns

The Function Runner uses different execution patterns based on evaluator type.
Which evaluator succeeds for a given image is determined by the FunctionConfig store (binary tags vs everything else falling through to pods).

### Pod-Based Execution

```
gRPC Request
        ↓
  Pod Evaluator
        ↓
  Check Pod Cache
        ↓
  ┌────┴────┬─────────┐
  ↓         ↓         ↓
Cache    Cache     Cache
Hit      Miss      Invalid
  ↓         ↓         ↓
Reuse    Create    Recreate
Pod      Pod       Pod
  ↓         ↓         ↓
  └────┬────┴─────────┘
       ↓
  Connect via Service
       ↓
  Call Wrapper Server
       ↓
  Return Response
```

**Execution pattern:**
- Pod executor settings (TTL, parallelism, templateOverrides) come from the matching FunctionConfig
- Pod cache checked for existing pod (reuse if available)
- Pod selection uses round-robin among pods with minimum waitlist length
- Cache miss triggers pod creation with wrapper server
- ClusterIP service provides stable DNS-based access
- gRPC connection to wrapper server in pod
- Wrapper server executes function binary and returns results
- Multiple evaluations can execute in parallel on the same pod

**For detailed pod lifecycle, see [Pod Lifecycle Management]({{% relref "/docs/5_architecture_and_components/function-runner/functionality/pod-lifecycle-management.md" %}}).**

### Executable-Based Execution

```
gRPC Request
        ↓
  Executable Evaluator
        ↓
  Lookup in FunctionConfig store
        ↓
  Found? ──No──> Return NotFoundError
        │
       Yes
        ↓
  Execute Binary Locally
        ↓
  Return Response
```

**Execution pattern:**
- FunctionConfig `binaryExecutor` maps images to binary paths in the in-memory store
- Lookup by image name and tag (or highest tag matching a version constraint)
- Direct process execution with ResourceList input
- NotFoundError triggers fallback in multi-evaluator

**For detailed function evaluation, see [Function Evaluation]({{% relref "/docs/5_architecture_and_components/function-runner/functionality/function-evaluation.md" %}}).**

## Kubernetes API Integration

The Function Runner uses Kubernetes API for pod and service management:

### Resource Management Pattern

```
Pod Evaluator
        ↓
  Kubernetes Client
        ↓
  ┌──────┴──────┬────────────┬───────────────┐
  ↓             ↓            ↓               ↓
Pod Ops    Service Ops    Templates       Secrets
  ↓             ↓            ↓               ↓
Create/Get  Create/Get   PodTemplate       Auth
Delete      Delete       ServiceTemplate   Config
                         FunctionConfig
```

**Resource operations:**
- **Pods**: Create from template, get status, list by label, delete
- **Services**: Create ClusterIP frontend, get endpoints, delete
- **PodTemplate / ServiceTemplate**: Base spec for function pods (`base-pod-template`, `base-service-template`)
- **FunctionConfig**: Per-image executor settings, TTL, and templateOverrides
- **Secrets**: Access registry authentication and TLS certificates

**Template system:**
- Cluster `PodTemplate` and `ServiceTemplate` objects, created from inline defaults if missing
- FunctionConfig `templateOverrides` merged per image
- Template version tracking (PodTemplate resourceVersion) for pod replacement on changes

**For detailed pod management, see [Pod Lifecycle Management]({{% relref "/docs/5_architecture_and_components/function-runner/functionality/pod-lifecycle-management.md" %}}).**

### Service Mesh Compatibility

Each function pod has a dedicated ClusterIP service:

**Service frontend pattern:**
- ClusterIP service created for each pod
- Selector matches pod's image label
- Port 9446 targeting wrapper server
- Provides stable DNS name for service mesh sidecars
- Deleted when pod is garbage collected

**Benefits:**
- Service mesh sidecars require stable DNS names
- Services provide consistent endpoint for pod communication
- Enables service mesh features (mTLS, traffic management, observability)

## Container Registry Integration

The Function Runner integrates with container registries for image metadata and authentication:

### Image Metadata Caching

```
Pod Creation
        ↓
  Check Image Cache
        ↓
  Cache Hit? ──Yes──> Use Cached Metadata
        │
        No
        ↓
  Connect to Registry
        ↓
  Fetch Manifest + Config
        ↓
  Extract Digest + Entrypoint
        ↓
  Store in Cache
        ↓
  Return Metadata
```

**Caching pattern:**
- In-memory cache of image digests and entrypoints
- Avoids repeated registry API calls
- Faster pod creation (no digest resolution delay)
- Cache persists for Function Runner lifetime

**For detailed image management, see [Image and Registry Management]({{% relref "/docs/5_architecture_and_components/function-runner/functionality/image-registry-management.md" %}}).**

### Authentication and TLS

The Function Runner supports private registries with authentication:

**Authentication flow:**
- Default registry (ghcr.io/kptdev/krm-functions-catalog/) uses default keychain
- Custom registries use Docker config JSON format
- Auth secret mounted into Function Runner pod
- Image pull secrets created and attached to function pods

**TLS configuration:**
- Custom CA certificates for secure registry connections
- TLS secret contains ca.crt or ca.pem
- Custom transport created with TLS config
- Fallback to default transport on TLS errors

**Configuration:**
- `--registry-auth-secret-path`: Path to Docker config JSON
- `--enable-private-registries`: Enable private registry support
- `--tls-secret-path`: Path to TLS certificates

## Wrapper Server Integration

The wrapper server provides gRPC interface for function execution inside pods:

### Wrapper Server Pattern

```
Pod Evaluator
        ↓
  Connect to Service
        ↓
  Call EvaluateFunction
        ↓
Wrapper Server (in pod)
        ↓
  Execute Function Binary
        ↓
  • Stdin: ResourceList
  • Stdout: Transformed ResourceList
  • Stderr: Function logs
        ↓
  Parse Output
        ↓
  Return Response
```

**Integration pattern:**
- Init container copies wrapper-server binary to shared volume
- Main container executes wrapper-server as entrypoint
- Wrapper server receives gRPC requests on port 9446
- Executes original function entrypoint with ResourceList input
- Parses function output and returns structured results

**Communication protocol:**
- ResourceList exchanged as serialized YAML
- Standard kpt ResourceList format
- Function reads from stdin, writes to stdout
- Stderr captured for logging

**Error handling:**
- Non-zero exit codes indicate function failure
- Invalid output triggers parse errors
- Timeout errors if function exceeds deadline
- All errors propagated through gRPC to Task Handler

## Integration Patterns

The Function Runner follows standard integration patterns:

### Separation of Concerns

**Function Runner responsibilities:**
- gRPC service for function execution
- Evaluator selection and orchestration
- FunctionConfig cache for binary and pod executor settings
- Pod and service lifecycle management
- Image metadata caching
- Registry authentication

**Task Handler responsibilities:**
- Render pipeline orchestration
- Function sequencing
- Result aggregation
- RenderStatus creation

**Clear boundaries:**
- Function Runner doesn't know about package structure
- Task Handler doesn't know about pod management
- Integration through well-defined gRPC interface

### Stateless Service

**Stateless characteristics:**
- No persistent state between requests
- Pod cache is ephemeral (TTL-based)
- Image cache is in-memory only
- Evaluators are stateless

**Benefits:**
- Horizontal scaling possible
- Restart-safe (no state loss)
- Simple deployment model

### Asynchronous Operations

**Async patterns:**
- Pod creation happens in background goroutines
- Garbage collection runs periodically
- TTL updates are asynchronous patches
- Cache warming at startup is concurrent

**Synchronous patterns:**
- Function execution is synchronous (blocks until complete)
- gRPC requests are synchronous
- Pod readiness polling is synchronous

## Error Handling

The Function Runner handles errors at multiple integration points:

### gRPC Error Propagation

```
Function Execution Error
        ↓
Wrapper Server
        ↓
  gRPC Error
        ↓
Pod Evaluator
        ↓
  Propagate Error
        ↓
Task Handler
        ↓
  Include in RenderStatus
        ↓
Engine
        ↓
  Return to Client
```

**Error types:**
- **NotFoundError**: Function not available (triggers fallback)
- **Execution error**: Function failed (non-zero exit code)
- **Timeout error**: Execution exceeded deadline
- **System error**: Infrastructure problem (pod creation, registry access)

**Error handling strategy:**
- NotFoundError triggers fallback to next evaluator
- Other errors returned immediately without fallback
- Errors include context and details for debugging
- Task Handler aggregates errors into RenderStatus

### Retry and Fallback

**Retry mechanisms:**
- Registry authentication retries on 401
- Pod creation retries on transient errors
- Connection retries on network failures

**Fallback mechanisms:**
- Multi-evaluator fallback on NotFoundError
- TLS fallback on certificate errors
- Default keychain fallback for default registry

## Concurrency and Safety

The Function Runner handles concurrent operations safely:

### Concurrent Execution

**Concurrency characteristics:**
- Multiple function executions can run concurrently
- Multiple evaluations can run in parallel on the same pod
- Each request gets own gRPC connection to pod
- Pod cache manager coordinates access via channels
- Waitlist prevents duplicate pod creation

**Thread safety:**
- Pod cache manager runs in single goroutine (no races)
- Channel-based communication eliminates shared memory
- Image cache uses sync.Map for concurrent access
- Kubernetes client is thread-safe

### Resource Isolation

**Isolation guarantees:**
- Each function pod is isolated
- Functions execute in separate containers
- Resource limits enforced by Kubernetes
- No shared state between function executions

**For detailed concurrency patterns, see [Pod Lifecycle Management]({{% relref "/docs/5_architecture_and_components/function-runner/functionality/pod-lifecycle-management.md" %}}).**
