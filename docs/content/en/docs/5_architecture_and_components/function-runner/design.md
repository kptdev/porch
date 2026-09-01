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

The Function Runner provides a unified interface that abstracts the underlying execution mechanism for KRM (Kubernetes Resource Model) functions. All evaluator implementations conform to this common interface, allowing the gRPC service to work with any evaluator without knowing which is in use.

**Evaluator Interface:**

The Evaluator interface defines a single operation:

- **EvaluateFunction**: Accepts context, image reference, and ResourceList; returns transformed ResourceList and logs

**Interface characteristics:**
- **Synchronous**: Blocks until function execution completes
- **Context-aware**: Respects cancellation and deadlines from gRPC context
- **Stateless**: No state maintained between calls
- **Error-typed**: Returns NotFoundError for missing functions to enable fallback

**Available Evaluators:**

- **Pod Evaluator**: Executes functions in Kubernetes pods with caching and lifecycle management
- **Executable Evaluator**: Runs pre-cached function binaries as local processes
- **Multi Evaluator**: Chains multiple evaluators with fallback logic

The interface design follows the **Strategy Pattern**, where different execution strategies (pod-based, executable, or chained) can be swapped without affecting the gRPC service layer. This abstraction enables:
- Deployment flexibility (choose evaluator based on performance requirements)
- Testing with mock implementations
- Future evaluator implementations without service changes
- Consistent behavior regardless of execution mechanism

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

**Pattern benefits:**
- Fast path for cached functions (executable evaluator)
- Fallback to pod evaluator for uncached functions
- Extensible to additional evaluator types
- Transparent to gRPC clients
- Graceful degradation on cache miss

## Evaluator Selection Strategy

Choosing between evaluators depends on deployment requirements and function characteristics:

### When to Use Pod Evaluator

**Deployment characteristics:**
- Need to support arbitrary function images, that is images that may not be known at the build time of Porch
- Functions require container isolation
- Security requirements mandate containerization
- No pre-caching infrastructure available

**Advantages:**
- **Universal compatibility**: Supports any function image
- **Container isolation**: Functions run in separate pods with resource limits
- **No pre-configuration**: Functions discovered dynamically
- **Security boundaries**: Kubernetes security contexts and network policies apply
- **Resource management**: Pod resource limits and quotas enforced

**Considerations:**
- Pod startup latency (typically 2-5 seconds for cached images)
- Kubernetes API overhead for pod and service creation
- Resource consumption (pods, services, endpoints)
- Image pull time for uncached images

### When to Use Executable Evaluator

**Deployment characteristics:**
- Small set of frequently-used functions
- Performance-critical function execution
- Functions available as standalone binaries
- Reduced Kubernetes API load desired

**Advantages:**
- **Fast execution**: Millisecond-level latency (no pod startup)
- **No Kubernetes overhead**: Direct process execution
- **Lower resource usage**: No pods or services created
- **Predictable performance**: No image pull or scheduling delays

**Considerations:**
- Requires pre-cached function binaries
- Limited to functions in configuration file
- No container isolation (functions run in runner process)
- Manual configuration and binary management needed

### When to Use Multi-Evaluator

**Deployment characteristics:**
- Mixed workload with frequent and infrequent functions
- Need both performance and flexibility
- Willing to manage executable cache for critical functions

**Advantages:**
- **Best of both**: Fast path for cached, fallback for uncached
- **Optimal performance**: Frequently-used functions execute quickly
- **Universal support**: All functions work via pod fallback
- **Graceful degradation**: Automatic fallback on cache miss

**Considerations:**
- Requires configuration for both evaluators
- Slightly more complex deployment
- Need to maintain executable cache for fast path

### Default Configuration

Porch-server deploys with the **pod evaluator by default** (`WRAPPER_SERVER_IMAGE` is required). Function Runner is the executable fast path.

**Configuration method:**
- Function Runner `--disable-runtimes` accepts `exec` only
- Pod evaluator: configured on porch-server / PackageRevision controller
- Executable evaluator: Engine looks up `exec_path` in the FunctionConfig store
- Multi-runtime: Engine chains builtin → Function Runner → pod evaluator

### Migration Considerations

Switching between evaluator configurations requires:
- Function Runner restart with new flags
- Executable evaluator requires configuration file with image-to-binary mappings
- Pod evaluator requires Kubernetes cluster access and RBAC permissions
- No data migration needed (evaluators are stateless)

## Key Architectural Differences

The evaluator implementations differ fundamentally in execution mechanism and performance characteristics:

| Aspect | Pod Evaluator | Executable Evaluator |
|--------|---------------|----------------------|
| **Execution Environment** | Kubernetes pods | Local processes |
| **Startup Latency** | Variable (pod creation, image pull, cluster speed) | Milliseconds (local process spawn) |
| **Function Discovery** | Dynamic (any image) | Static (configuration file) |
| **Isolation** | Container isolation | Process isolation |
| **Resource Management** | Kubernetes limits/quotas | OS process limits |
| **Kubernetes API Load** | High (pod/service CRUD) | None |
| **Configuration** | Minimal (cluster access) | Requires binary cache |
| **Caching Strategy** | Pod cache with TTL | No caching needed |

For detailed explanations of how these differences affect operations, see the individual functionality sections.

## Design Decisions

### Pod Caching Strategy

**Decision**: Use TTL-based pod caching with configurable expiration and garbage collection.

**Rationale:**
- Balances performance (reuse pods) with resource usage (clean up unused pods)
- Frequently-used functions stay cached (TTL extended on reuse)
- Infrequently-used functions cleaned up automatically
- Simple implementation compared to LRU or other eviction strategies

**Alternatives considered:**
- **No caching**: Too slow (pod startup on every call)
- **Permanent caching**: Resource accumulation without bounds
- **LRU eviction**: More complex, similar benefits to TTL

**Trade-offs:**
- TTL requires periodic garbage collection
- May recreate pods for functions with irregular usage patterns
- Configurable TTL allows tuning for specific workloads

### Pod Selection Strategy

**Decision**: Use round-robin distribution among pods with equal minimum load.

**Rationale:**
- Ensures even distribution of requests across equally-loaded pods
- Prevents all requests from going to the same pod when multiple pods have equal capacity
- Simple and predictable load balancing behavior
- Works well with parallel execution within pods

**Implementation:**
- Find the minimum waitlist length across all pods for a function
- Use round-robin to select among pods that have the minimum waitlist
- Maintains round-robin index per function to track position

**Alternatives considered:**
- **Pure least-loaded**: Can cause all requests to go to same pod when loads are equal
- **Random selection**: Less predictable distribution patterns
- **Weighted round-robin**: Unnecessary complexity for uniform pod capacities

**Trade-offs:**
- Slightly more complex than pure least-loaded selection
- Requires maintaining round-robin index per function
- Better load distribution justifies the minimal added complexity

### Parallel Execution Model

**Decision**: Allow parallel function evaluations within the same pod.

**Rationale:**
- Improves throughput by utilizing pod capacity fully
- Reduces wait times when multiple requests target the same function
- Simplifies code by removing serialization mutex
- Works well with round-robin pod selection for even load distribution

**Implementation:**
- Removed per-pod mutex that was serializing executions
- Concurrent executions tracked via atomic counter
- gRPC connection shared across parallel calls to same pod
- Context timeout prevents goroutine leaks

**Alternatives considered:**
- **Serial execution per pod**: Simpler but underutilizes pod capacity
- **Connection pooling**: More complex, unnecessary with gRPC multiplexing
- **Per-function concurrency limits**: Added complexity without clear benefit

**Trade-offs:**
- Functions must be safe for concurrent execution (standard KRM function requirement)
- Multiple evaluations share pod resources (CPU, memory)
- Better resource utilization justifies the concurrency model

### Service Mesh Compatibility

**Decision**: Use ClusterIP services as frontends for function pods.

**Rationale:**
- Service mesh sidecars require stable DNS names
- Services provide consistent endpoint for pod communication
- Enables service mesh features (mTLS, traffic management, observability)
- Standard Kubernetes pattern for pod communication

**Alternatives considered:**
- **Direct pod IP**: Incompatible with service mesh injection
- **Shared service**: Complex pod selection and routing
- **Headless service**: No load balancing or stable IP

**Trade-offs:**
- One service per pod increases resource usage
- Service mesh compatibility worth the cost for most deployments
- Can be disabled if service mesh not in use (future enhancement)

### Wrapper Server Pattern

**Decision**: Inject wrapper-server binary into function pods via init container.

**Rationale:**
- Provides consistent gRPC interface across all function images
- No modification to function images required
- Structured result handling and error reporting
- Health check support for pod readiness

**Alternatives considered:**
- **Modify function images**: Requires rebuilding all functions
- **Sidecar container**: More complex pod spec and communication
- **Direct function execution**: No structured results or health checks

**Trade-offs:**
- Init container adds slight startup delay
- Shared volume required between init and main containers
- Wrapper server must support all function entrypoint patterns

### Image Metadata Caching

**Decision**: Cache image digests and entrypoints in-memory for lifetime of runner.

**Rationale:**
- Avoids repeated registry API calls for same function
- Faster pod creation (no digest resolution delay)
- Reduced registry load and rate limiting issues
- Simple implementation (no expiration needed)

**Alternatives considered:**
- **No caching**: Repeated registry calls for every execution
- **Persistent cache**: Requires storage and invalidation logic
- **TTL-based cache**: Unnecessary complexity for immutable data

**Trade-offs:**
- Memory usage grows with number of unique function images
- No invalidation mechanism (requires runner restart for updates)
- Acceptable for typical deployments (hundreds of functions)

### Executable Evaluator Configuration

**Decision**: Use static configuration file mapping images to binary paths.

**Rationale:**
- Simple and explicit configuration
- No dynamic discovery complexity
- Clear mapping between function images and binaries
- Easy to audit and validate

**Alternatives considered:**
- **Directory scanning**: Implicit mapping, harder to debug
- **Database storage**: Unnecessary complexity
- **Dynamic download**: Security and caching concerns

**Trade-offs:**
- Requires manual configuration updates
- No automatic discovery of new binaries
- Configuration must be kept in sync with available binaries
