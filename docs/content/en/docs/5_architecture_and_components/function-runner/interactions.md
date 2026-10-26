---
title: "Function Runner Interactions"
type: docs
weight: 4
description: |
  How the function runner integrates with Porch and external systems.
---

## Overview

The Function Runner is a **separate gRPC service** that interacts with the Task Handler (via gRPC) to run cached function binaries.
Kubernetes API, image cache, and function-pod boxes in the diagram now run in the Engine; Function Runner still serves EvaluateFunction for cached binaries using `exec_path`.

Function Runner no longer watches FunctionConfig objects. The Engine resolves `binaryExecutor` entries from its own FunctionConfig store and sends the selected `exec_path` over gRPC.
Go execution is not handled here; it runs in porch-server / porch-controllers.
Pod TTL, parallelism, and template overrides are applied by the Engine pod evaluator, not by this process.
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

At evaluation time the executable evaluator looks up a binary by image name, prefix, and tag against FunctionConfig semver constraints.
When the request includes a Kptfile version constraint, a concrete version is matched against those constraints; otherwise the store uses the highest FunctionConfig tag that parses as a version and satisfies the constraint.
A miss is `NotFoundError`, which the multi-evaluator treats as a signal to try the in-process pod evaluator.
The Engine pod evaluator reads `podExecutor` from porch-server's FunctionConfig store for TTL, waitlist length, max parallel pods, and `templateOverrides`.

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

The Task Handler uses gRPC Runtime to communicate with the Function Runner over a single persistent
connection shared across all function executions. That connection is established at Porch startup and closed on shutdown. The Function
Runner address is configured via `--function-runner` on porch-server (`FUNCTION_RUNNER_ADDRESS` on the PackageRevision controller). The
Engine looks up `exec_path` in the FunctionConfig store; the Function Runner executes that binary or returns NotFoundError.

**Request-response flow:**
- Task Handler serializes ResourceList as YAML
- gRPC Runtime sends EvaluateFunctionRequest
- Function Runner executes the binary at `exec_path` (or returns NotFoundError if it is empty)
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

Functions run sequentially, one at a time, and each function sees the output of the previous one. The first error stops the pipeline,
and results are aggregated into RenderStatus by the Task Handler.

### Message Size Configuration

Both Porch and Function Runner must agree on message size limits:

**Configuration synchronization:**
- Porch server: `--function-runner-grpc-max-message-size` flag
- Function Runner: `--max-request-body-size` flag
- Both must be set to same value (default: 6MB)
- Mismatched limits cause cryptic gRPC errors

Coordination matters because Large packages with many resources exceed default gRPC limits, because a ResourceList serialized as YAML
can be large. The limit must be coordinated during deployment.

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

For detailed pod lifecycle, see [Pod Lifecycle Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/pod-lifecycle-management.md" %}}).

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

For detailed function evaluation, see [Function Evaluation]({{% relref "/docs/5_architecture_and_components/function-runner/functionality/function-evaluation.md" %}}).

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

For detailed pod management, see [Pod Lifecycle Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/pod-lifecycle-management.md" %}}).

### Service Mesh Compatibility

Each function pod has a dedicated ClusterIP service:

**Service frontend pattern:**
- ClusterIP service created for each pod
- Selector matches pod's image label
- Port 9446 targeting wrapper server
- Provides stable DNS name for service mesh sidecars
- Deleted when pod is garbage collected

**Benefits:** Service mesh sidecars require stable DNS names, and services provide a consistent endpoint for pod communication. That
enables service mesh features (mTLS, traffic management, observability).

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

For detailed image management, see [Image and Registry Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/image-registry-management.md" %}}).

### Authentication and TLS

The Function Runner supports private registries with authentication:

**Authentication flow:**
- Default registry (ghcr.io/kptdev/krm-functions-catalog/) uses default keychain
- Custom registries use Docker config JSON format
- Auth secret mounted into Function Runner pod
- Image pull secrets created and attached to function pods

**TLS configuration:** Custom CA certificates secure registry connections. The TLS secret contains `ca.crt` or `ca.pem`, and a custom
transport is created with that TLS config, with fallback to the default transport on TLS errors.

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

**Communication protocol:** The ResourceList is exchanged as serialized YAML in the standard kpt ResourceList format. The function reads
from stdin and writes to stdout, and stderr is captured for logging.

**Error handling:** A non-zero exit code indicates function failure, and invalid output triggers parse errors. If the function exceeds
its deadline, a timeout error is raised. All errors are propagated through gRPC to the Task Handler.

## Integration Patterns

The Function Runner follows standard integration patterns:

### Separation of Concerns

**Function Runner responsibilities:** The Function Runner provides the gRPC service for function execution, including evaluator
selection and orchestration. It caches FunctionConfig settings for binary and pod executors, manages pod and service lifecycle, caches
image metadata, and handles registry authentication.

**Task Handler responsibilities:** The Task Handler orchestrates the render pipeline, sequences functions, aggregates results, and
creates RenderStatus.

**Clear boundaries:** The Function Runner does not know about package structure, and the Task Handler does not know about pod
management. They integrate through a well-defined gRPC interface.

### Stateless Service

**Stateless characteristics:** There is no persistent state between requests. The pod cache is ephemeral (TTL-based), the image cache is
in-memory only, and evaluators are stateless.

**Benefits:** Horizontal scaling is possible, restarts are safe (no state loss), and the deployment model is simple.

### Asynchronous Operations

**Async patterns:** Pod creation runs in background goroutines, garbage collection runs periodically, and TTL updates are asynchronous
patches. Cache warming at startup is concurrent.

**Synchronous patterns:** Function execution is synchronous (it blocks until complete), as are gRPC requests and pod readiness polling.

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

**Error handling strategy:** NotFoundError triggers fallback to the next evaluator; other errors are returned immediately without
fallback. Errors include context and details for debugging, and the Task Handler aggregates them into RenderStatus.

### Retry and Fallback

**Retry mechanisms:** Registry authentication retries on 401, pod creation retries on transient errors, and connections retry on network
failures.

**Fallback mechanisms:** The multi-evaluator falls back on NotFoundError, TLS falls back on certificate errors, and the default keychain
is used as fallback for the default registry.

## Concurrency and Safety

The Function Runner handles concurrent operations safely:

### Concurrent Execution

**Concurrency characteristics:** Multiple function executions can run concurrently, including in parallel on the same pod. Each request
gets its own gRPC connection to the pod. The pod cache manager coordinates access via channels, and a waitlist prevents duplicate pod
creation.

**Thread safety:** The pod cache manager runs in a single goroutine (no races), and channel-based communication eliminates shared
memory. The image cache uses `sync.Map` for concurrent access, and the Kubernetes client is thread-safe.

### Resource Isolation

Each function pod is isolated, and functions execute in separate containers. Kubernetes enforces resource limits, and there is no shared
state between function executions.

For detailed concurrency patterns, see [Pod Lifecycle Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/pod-lifecycle-management.md" %}}).
