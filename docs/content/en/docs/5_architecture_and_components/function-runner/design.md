---
title: "Function Runner Design"
type: docs
weight: 2
description: |
  Evaluator patterns and architectural decisions.
---

## Architectural Context

The Function Runner is **one of several function execution mechanisms** in Porch. The Task Handler uses a **Function Runtime** interface that can be implemented by:

```
Task Handler (in Porch)
        ↓
   Function Runtime
        ↓
  ┌─────┴─────┬──────────┐
  ↓           ↓          ↓
Builtin      gRPC      Multi
Runtime     Runtime   Runtime
  ↓           ↓          ↓
In-Process  Function   Chains
Go Code     Runner     Runtimes
           (External)
              ↓
    ┌─────────┴─────────┬──────────┐
    ↓                   ↓          ↓
  Pod               Executable   Multi
  Evaluator         Evaluator  Evaluator
    ↓                   ↓          ↓
  Function           Cached     Fallback
   Pods             Binaries    Chain
```

**Function Runtime implementations:**

1. **Builtin Runtime** (in Engine, not Function Runner): Executes specific functions in-process within the Porch server as compiled Go code (apply-replacements, set-namespace, starlark)
2. **gRPC Runtime** (in Engine): Calls the external Function Runner service via gRPC
3. **Multi Runtime** (in Engine): Chains builtin, gRPC Function Runner (exec), and pod evaluator with NotFound fallback

This documentation focuses on the **Function Runner service** (the gRPC-based external service), not the builtin runtime which is part of the Engine component. The pod evaluator now runs in the Engine; Function Runner only hosts the executable evaluator. The Engine looks up a cached binary and sets gRPC `exec_path`; empty `exec_path` is treated as NotFound.

## Evaluator Interface

The Function Runner provides a unified interface that abstracts the underlying execution mechanism for KRM (Kubernetes Resource Model)
functions. All evaluator implementations conform to this common interface, allowing the gRPC service to work with any evaluator without
knowing which is in use.

The Evaluator interface defines a single operation, EvaluateFunction, which accepts a context, an image reference, and a ResourceList,
and returns a transformed ResourceList and logs.

The interface is synchronous (it blocks until function execution completes), context-aware (it respects cancellation and deadlines from
the gRPC context), and stateless (no state is maintained between calls). It is error-typed: it returns NotFoundError for missing
functions to enable fallback.

Available evaluators include a Pod Evaluator that executes functions in Kubernetes pods with caching and lifecycle management, an
Executable Evaluator that runs pre-cached function binaries as local processes, and a Multi Evaluator that chains multiple evaluators
with fallback logic.

The interface design follows the Strategy Pattern, where different execution strategies (pod-based, executable, or chained) can be
swapped without affecting the gRPC service layer. This abstraction enables deployment flexibility (choose evaluator based on performance
requirements), testing with mock implementations, future evaluator implementations without service changes, and consistent behavior
regardless of execution mechanism.

## Multi-Evaluator Pattern

The Function Runner uses a **Chain-of-Responsibility Pattern** through the MultiEvaluator to provide fast-path execution with fallback.

### Chaining Mechanism

The multi-evaluator tries evaluators in sequence until one succeeds:

```
  gRPC Request
        ↓
  MultiEvaluator
        ↓
  Try Evaluator 1
        ↓
  Success? ──Yes──> Return Response
        │
        No
        ↓
  NotFound? ──Yes──> Try Evaluator 2
        │
        No
        ↓
  Return Error
```

**Chaining rules:**
- Evaluators tried in configured order (typically: executable, then pod)
- First successful response returned immediately
- NotFoundError triggers fallback to next evaluator
- Other errors (timeout, execution failure) returned immediately without fallback
- Final NotFoundError returned if all evaluators fail

### Runtime Configuration

The server supports dynamic evaluator selection through command-line flags:

**Configuration mechanism:**
1. Engine starts with builtin, optional Function Runner exec, and pod evaluator
2. Function Runner `--disable-runtimes` can disable `exec` only
3. Engine hosts the pod evaluator when `WRAPPER_SERVER_IMAGE` is set
4. Engine wraps runtimes in a multi-runtime chain (builtin → exec → pod)

**Configuration examples:**
- Function Runner `--disable-runtimes=exec`: Engine uses builtin + pod evaluator
- No Function Runner address: Engine uses builtin + pod evaluator
- Default porch-server: Function Runner exec fast path, then pod evaluator

(`--disable-runtimes=pod` was a Function Runner flag; the pod evaluator is not in that binary.)

**Pattern benefits:** The pattern provides a fast path for cached functions (executable evaluator), falls back to the pod evaluator for
uncached functions, and is extensible to additional evaluator types. It is transparent to gRPC clients and degrades gracefully on a
cache miss.

## Evaluator Selection Strategy

Choosing between evaluators depends on deployment requirements and function characteristics:

### When to Use Pod Evaluator

**Deployment characteristics:** Use this evaluator when you need to support arbitrary function images, including images that may not be
known at Porch build time, when functions require container isolation, when security requirements mandate containerization, and when no
pre-caching infrastructure is available.

**Advantages:** It supports any function image, runs functions in separate pods with resource limits, and discovers functions
dynamically with no pre-configuration. Kubernetes security contexts and network policies apply, and pod resource limits and quotas are
enforced.

**Considerations:** Pod startup latency is typically 2-5 seconds for cached images. Kubernetes API overhead for pod and service
creation, resource consumption (pods, services, endpoints), and image pull time for uncached images also apply.

### When to Use Executable Evaluator

**Deployment characteristics:** Use this evaluator for a small set of frequently used functions when performance-critical execution
matters, when the functions are available as standalone binaries, and when reduced Kubernetes API load is desired.

**Advantages:** Execution is millisecond-level (no pod startup) with no Kubernetes overhead, because functions run as direct processes.
Resource usage is lower (no pods or services are created), and performance is predictable with no image pull or scheduling delays.

**Considerations:** It requires pre-cached function binaries and is limited to functions listed on a FunctionConfig `binaryExecutor`.
There is no container isolation (functions run in the runner process), and manual configuration and binary management are needed.

### When to Use Multi-Evaluator

**Deployment characteristics:** Use this evaluator for mixed workloads with frequent and infrequent functions when you need both
performance and flexibility, and you are willing to manage an executable cache for critical functions.

**Advantages:** Cached functions take a fast path, with fallback for uncached ones, so frequently used functions execute quickly while
all functions still work via pod fallback. On a cache miss, fallback is automatic.

**Considerations:** Both evaluators must be configured, which makes deployment slightly more complex. The executable cache for the fast
path must be maintained.

### Default Configuration

Porch-server deploys with the **pod evaluator by default** (`WRAPPER_SERVER_IMAGE` is required). Function Runner is the executable fast path.

**Configuration method:**
- Function Runner `--disable-runtimes` accepts `exec` only
- Pod evaluator: configured on porch-server (`WRAPPER_SERVER_IMAGE` is required). The PackageRevision controller calls it over FunctionEvaluator gRPC (`POD_EVALUATOR_ADDRESS`).
- Executable evaluator: FunctionConfig `binaryExecutor` entries and binaries under `--functions`; the Engine sends `exec_path`
- Multi-runtime: Engine chains builtin → Function Runner exec → pod evaluator

### Migration Considerations

Switching between evaluator configurations requires:
- Function Runner restart with new flags
- Executable evaluator requires FunctionConfig `binaryExecutor` entries and binaries under `--functions`
- Pod evaluator requires Kubernetes cluster access and RBAC permissions
- No data migration needed (evaluators are stateless)

## Key Architectural Differences

The evaluator implementations differ fundamentally in execution mechanism and performance characteristics:

| Aspect | Pod Evaluator | Executable Evaluator |
|--------|---------------|----------------------|
| **Execution Environment** | Kubernetes pods | Local processes |
| **Startup Latency** | Variable (pod creation, image pull, cluster speed) | Milliseconds (local process spawn) |
| **Function Discovery** | Dynamic (any image) | Static (FunctionConfig binaryExecutor) |
| **Isolation** | Container isolation | Process isolation |
| **Resource Management** | Kubernetes limits/quotas | OS process limits |
| **Kubernetes API Load** | High (pod/service CRUD) | None |
| **Configuration** | Minimal (cluster access) | Requires binary cache |
| **Caching Strategy** | Pod cache with TTL | No caching needed |

For detailed explanations of how these differences affect operations, see the individual functionality sections.

## Design Decisions

### Pod Caching Strategy

**Decision**: Use TTL-based pod caching with configurable expiration and garbage collection.

**Rationale:** The strategy balances performance (reuse pods) with resource usage (clean up unused pods). Frequently used functions stay
cached, with the TTL extended on reuse, while infrequently used functions are cleaned up automatically. The implementation is simpler
than LRU or other eviction strategies.

**Alternatives considered:** No caching is too slow (pod startup on every call). Permanent caching accumulates resources without bounds.
LRU eviction is more complex and offers similar benefits to TTL.

**Trade-offs:** TTL requires periodic garbage collection and may recreate pods for functions with irregular usage patterns. A
configurable TTL allows tuning for specific workloads.

### Pod Selection Strategy

**Decision**: Use round-robin distribution among pods with equal minimum load.

**Rationale:** Round-robin keeps requests evenly distributed across equally loaded pods and prevents all requests from going to the same
pod when multiple pods have equal capacity. The load balancing behavior is simple and predictable, and it works well with parallel
execution within pods.

**Implementation:** The selector finds the minimum waitlist length across all pods for a function, then uses round-robin to choose among
pods that have that minimum waitlist. A round-robin index per function tracks position.

**Alternatives considered:** Pure least-loaded selection can send all requests to the same pod when loads are equal. Random selection
produces less predictable distribution patterns. Weighted round-robin adds unnecessary complexity for uniform pod capacities.

**Trade-offs:** Selection is slightly more complex than pure least-loaded and requires maintaining a round-robin index per function.
Better load distribution justifies the minimal added complexity.

### Parallel Execution Model

**Decision**: Allow parallel function evaluations within the same pod.

**Rationale:** Parallel execution improves throughput by using pod capacity fully and reduces wait times when multiple requests target
the same function. Removing the serialization mutex also simplifies the code. The model works well with round-robin pod selection for
even load distribution.

**Implementation:** The per-pod mutex that serialized executions was removed. Concurrent executions are tracked with an atomic counter,
the gRPC connection is shared across parallel calls to the same pod, and a context timeout prevents goroutine leaks.

**Alternatives considered:** Serial execution per pod is simpler but underutilizes pod capacity. Connection pooling is more complex and
unnecessary with gRPC multiplexing. Per-function concurrency limits add complexity without a clear benefit.

**Trade-offs:** Functions must be safe for concurrent execution (a standard KRM function requirement), and multiple evaluations share
pod resources (CPU, memory). Better resource utilization justifies the concurrency model.

### Service Mesh Compatibility

**Decision**: Use ClusterIP services as frontends for function pods.

**Rationale:** Service mesh sidecars require stable DNS names, and services provide a consistent endpoint for pod communication. That
enables service mesh features (mTLS, traffic management, observability) and follows the standard Kubernetes pattern for pod
communication.

**Alternatives considered:** Direct pod IPs are incompatible with service mesh injection. A shared service makes pod selection and
routing more complex. A headless service provides no load balancing or stable IP.

**Trade-offs:** One service per pod increases resource usage. Service mesh compatibility is worth that cost for most deployments.
Services can be disabled if a service mesh is not in use (future enhancement).

### Wrapper Server Pattern

**Decision**: Inject wrapper-server binary into function pods via init container.

**Rationale:** The wrapper provides a consistent gRPC interface across all function images without modifying those images. It also
supplies structured result handling, error reporting, and health check support for pod readiness.

**Alternatives considered:** Modifying function images would require rebuilding all functions. A sidecar container makes the pod spec
and communication more complex. Direct function execution provides no structured results or health checks.

**Trade-offs:** The init container adds a slight startup delay, and a shared volume is required between the init and main containers.
The wrapper server must support all function entrypoint patterns.

### Image Metadata Caching

**Decision**: Cache image digests and entrypoints in-memory for lifetime of runner.

**Rationale:** Caching avoids repeated registry API calls for the same function, so pod creation is faster (no digest resolution delay).
It also reduces registry load and rate-limiting issues. The implementation is simple because no expiration is needed.

**Alternatives considered:** With no caching, every execution repeats registry calls. A persistent cache requires storage and
invalidation logic. A TTL-based cache adds unnecessary complexity for immutable data.

**Trade-offs:** Memory usage grows with the number of unique function images, and there is no invalidation mechanism (a runner restart
is required for updates). That is acceptable for typical deployments (hundreds of functions).

### Executable Evaluator Configuration

**Decision**: Use FunctionConfig custom resources to map images to binary paths, watched by an embedded reconciler.

**Rationale:** The same CRD configures pod, binary, and Go executors, and spec changes apply without restarting the function-runner.
Prefix and tag matching is shared with the builtin runtime, and the mapping is easy to audit with `kubectl get functionconfigs`.

**Alternatives considered:** A static YAML config file required a process restart and drifted from pod-executor settings. Directory
scanning uses implicit mapping and is harder to debug. Dynamic download raises security and caching concerns.

**Trade-offs:** Binaries still have to be present under `--functions` (or an absolute path). Duplicate `spec.image` values on different
FunctionConfig objects are ignored. Only images listed on `binaryExecutor` take the fast path; everything else falls back to pods.
