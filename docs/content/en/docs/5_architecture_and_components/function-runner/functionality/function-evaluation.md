---
title: "Function Evaluation"
type: docs
weight: 1
description: |
  Detailed architecture of function evaluation strategies and execution patterns.
---

## Overview

Function evaluation is the core responsibility of the Function Runner for **cached binaries**. The pod evaluator sections below now run in the Engine; see [Engine Function Evaluation]({{% relref "/docs/5_architecture_and_components/engine/functionality/function-evaluation.md" %}}). The Engine sends `exec_path` on the gRPC request; Function Runner executes that binary or returns NotFoundError if `exec_path` is empty.

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│           Function Evaluation System                    │
│                                                         │
│  ┌──────────────────┐      ┌──────────────────┐         │
│  │   Evaluator      │      │   Execution      │         │
│  │   Interface      │ ───> │   Strategies     │         │
│  │                  │      │                  │         │
│  │  • Common        │      │  • Pod Evaluator │         │
│  │    Contract      │      │  • Exec Evaluator│         │
│  │  • Pluggable     │      │  • Multi Eval    │         │
│  └──────────────────┘      └──────────────────┘         │
│           │                         │                   │
│           └────────┬────────────────┘                   │
│                    ↓                                    │
│          ┌──────────────────┐                           │
│          │    Wrapper       │                           │
│          │    Server        │                           │
│          │                  │                           │
│          │  • gRPC Frontend │                           │
│          │  • Binary Exec   │                           │
│          │  • Result Parse  │                           │
│          └──────────────────┘                           │
└─────────────────────────────────────────────────────────┘
```

## Evaluator Interface

All evaluators implement a common interface that defines the contract for function execution.

### Interface Contract

**Single operation:**
- **EvaluateFunction**: Accepts context and request, returns response or error

**Request structure:**
- **Image**: Function container image identifier
- **Tag**: Optional version constraint. When set, evaluators resolve it against cached tags.
- **ResourceList**: Serialized KRM resources as YAML bytes

**Response structure:**
- **ResourceList**: Transformed KRM resources as YAML bytes
- **Log**: Function stderr output as bytes

**Contract characteristics:**
- **Synchronous**: Blocks until function execution completes
- **Context-aware**: Respects cancellation and deadlines from context
- **Stateless**: No state maintained between calls
- **Error-typed**: Returns NotFoundError for missing functions to enable fallback

### Evaluator Implementations

Three evaluator implementations provide different execution strategies:

**Pod Evaluator:** Executes functions in Kubernetes pods, using a wrapper server for the gRPC interface. It manages a pod cache with
TTL-based expiration, reads per-image TTL, waitlist, parallelism, and templateOverrides from FunctionConfig, and handles service mesh
compatibility via ClusterIP services.

**Executable Evaluator:** Executes local function binaries inside the function-runner process using `exec_path` from the Engine.
Image-to-binary mapping comes from FunctionConfig `binaryExecutor` (path + semver constraints), which gives fast execution without pod
overhead. An empty `exec_path` or a cache miss returns `NotFoundError` so the Engine can fall through to the pod evaluator.

**Multi-Evaluator:** Chains multiple evaluators together and tries each in sequence. It falls back on NotFoundError and returns the
first successful response.

## Pod Evaluator

Executes functions in Kubernetes pods with caching and lifecycle management.

### Channel-Based Communication

The pod evaluator uses channels for communication with the pod cache manager:

**Communication pattern:** A request is sent via channel to the pod cache manager, then the caller blocks waiting for a gRPC client from
the cache manager. The cache manager handles pod creation or reuse, and the evaluator makes a direct gRPC call to the wrapper server in
the pod.

**Channel characteristics:** Each request includes a response channel buffered with size 1 so the cache manager can send a response
without blocking. The caller sends the request on the request channel, then waits on the response channel for a client or an error.

**Benefits:** Evaluation is decoupled from pod management, and a single goroutine manages the pod cache (no race conditions). The design
provides natural backpressure when pods are unavailable and a clean separation of concerns.

### Client Connection Management

The pod cache manager maintains gRPC connections to function pods:

**Cache structure:** The cache is a map from image name to pod and gRPC client. It includes the pod object key for validation and stores
the gRPC client connection for reuse.

**Connection validation:** The manager checks that the pod still exists (not deleted externally), is not in Failed state, and is not
being deleted (DeletionTimestamp is nil). It also checks that the service for the pod still exists and that the gRPC client target
matches the service URL.

**Failed pod handling:** Failed pods are deleted immediately, evicted from the cache, and replaced by a new pod, which prevents reusing
broken pods.

### Waitlist Mechanism

Prevents duplicate pod creation when multiple requests arrive for the same function:

**Waitlist pattern:** Multiple requests for the same image queue up, and a single pod creation serves all waiters. When the pod is
ready, waiters are notified in a batch, which prevents duplicate pod creation.

**Error handling:** Pod creation errors are sent to all waiters, then the waitlist is cleared. Each waiter receives the error
independently, which allows a retry on the next request.

### Function Execution

Once gRPC client acquired, function execution proceeds. Execution is a synchronous gRPC call that passes the context through for
cancellation and enforces a timeout from the context deadline. Stderr is logged even on success, and errors get detailed logging.

## Executable Evaluator

Executes local function binaries inside the function-runner process for a fast path that skips pod startup.

### FunctionConfig-backed cache and exec_path

The executable evaluator does not read a YAML config file.
An embedded FunctionConfig reconciler watches FunctionConfig objects in the function-pod namespace and fills an in-memory store.
For each `spec.binaryExecutor`, the store records the binary path (absolute, or relative to `--functions`) against the listed semver constraints and `spec.prefixes`.

The Engine also looks up a cached binary and may set `exec_path` on the gRPC request.
Empty `exec_path` (and a store miss) returns `NotFoundError` so the Engine can fall through to the in-process pod evaluator.

When the evaluation request includes a version constraint (`Tag`), a concrete version is matched against those constraints; if `Tag` is itself a constraint, the store selects the highest FunctionConfig tag that parses as a version and satisfies it.
When `Tag` is empty, lookup uses the tag on the image reference against the same constraints.

Spec changes are applied on reconcile. The function-runner does not need to restart.

### Local Execution

Binary execution happens in-process:

**Execution characteristics:** The binary runs as a direct process (no container). The ResourceList is passed via stdin, stdout is
captured as output, and stderr is captured for logging. Execution is context-aware and respects cancellation.

**Performance benefits:** There is no pod startup latency, image pull time, or Kubernetes API overhead. Execution times are in
milliseconds, and performance is predictable.

## Multi-Evaluator

Chains multiple evaluators with fallback logic.

### Evaluator Chaining

Evaluators run sequentially, not in parallel, and the first success wins. Only NotFoundError triggers
fallback; other errors are returned immediately, which preserves error semantics.

**Typical chain:**
1. **Executable evaluator** (fast path)
2. **Pod evaluator** (fallback)

### Fallback Strategy

**Fallback conditions:** Fallback runs only on NotFoundError from an evaluator. Other errors (timeout, execution failure) do not trigger
fallback, so error information from the failed evaluator is preserved.

**Fallback rationale:** NotFoundError means the function is not available in the current evaluator, while other errors indicate actual
execution problems. Fallback therefore only makes sense for missing functions and does not mask real errors.

## Wrapper Server

Provides gRPC interface for function execution inside pods.

### Wrapper Server Architecture

```
┌─────────────────────────────────────────┐
│         Function Pod                    │
│                                         │
│  ┌──────────────────┐                   │
│  │  Init Container  │                   │
│  │                  │                   │
│  │  • Copy wrapper  │                   │
│  │    server binary │                   │
│  │  • To shared vol │                   │
│  └────────┬─────────┘                   │
│           ↓                             │
│  ┌──────────────────┐                   │
│  │ Main Container   │                   │
│  │                  │                   │
│  │  Entrypoint:     │                   │
│  │  wrapper-server  │                   │
│  │                  │                   │
│  │  Args:           │                   │
│  │  • --port 9446   │                   │
│  │  • --            │                   │
│  │  • [function     │                   │
│  │     entrypoint]  │                   │
│  └──────────────────┘                   │
└─────────────────────────────────────────┘
```

The wrapper server accepts gRPC EvaluateFunction requests, executes the function entrypoint with the ResourceList, and captures stdout
and stderr from the function. It parses structured results from the output, returns a gRPC response with those results, and provides a
health check endpoint.

### Entrypoint Wrapping

The wrapper server wraps the original function entrypoint. The original function entrypoint is preserved and passed as arguments after a
separator. It is executed as a subprocess, with stdin, stdout, and stderr captured.

### Resource List Processing

The ResourceList is passed as raw bytes to stdin; the function reads from stdin and writes to stdout. This is the standard KRM function
protocol, with no modification of the ResourceList format.

### Structured Results Handling

The wrapper server parses structured results from function output:

**Structured results:** The ResourceList contains a results array, and each result has a severity, message, and tags. The exit code
indicates overall success or failure, and the results provide detailed feedback.

**Exit code semantics:** Exit code 0 means the function succeeded; a non-zero code means it failed. Parse failure is a wrapper server
error, and an execution error is a system error.

## Execution Characteristics

### Synchronous Execution

All evaluators execute synchronously They block until the function completes, with no async callbacks or futures. The pattern is
simple request-response: the caller waits for the result.

The programming model is simple, with easy error handling and predictable behavior, and there is no concurrency complexity.

### Context Cancellation

The context is passed through all layers. On cancellation, execution stops, resources are cleaned up, and an error is returned to the
caller.

### Timeout Handling

**Timeout sources:** The Task Handler sets a context deadline that the Function Runner respects. Evaluators check the context, and
function execution is cancelled on timeout.

**Timeout behavior:** Execution stops immediately, partial results are discarded, a timeout error is returned, and resources are cleaned
up.

## Error Handling

The evaluation system handles errors at multiple levels.

### Error Types

**NotFoundError:**
- Function not available in evaluator
- Triggers fallback in multi-evaluator
- Distinguishable from execution errors
- Final NotFoundError if all evaluators fail

**Execution errors:**
- Function failed during execution
- Non-zero exit code
- Invalid output format
- Returned immediately without fallback

**Timeout errors:**
- Execution exceeded deadline
- Context cancellation triggered
- Partial results discarded
- Resources cleaned up

**System errors:**
- Infrastructure problems
- Pod creation failures
- Network issues
- Kubernetes API errors

### Error Propagation

**Error flow:**
- Function execution error captured
- Wrapper server formats error message
- Pod evaluator adds context
- Multi-evaluator checks error type
- Function runner returns to task handler
- Task handler includes in RenderStatus

### Error Recovery

**Retry mechanisms:** NotFoundError triggers fallback to the next evaluator. Transient errors may succeed on retry, and the waitlist
allows a retry on the next request.

**No retry scenarios:** Execution errors (function failed), timeout errors (deadline exceeded), and system errors (infrastructure
problems) are not retried.

## Performance Optimization

The evaluation system employs several performance strategies.

### Pod Reuse

**Reuse benefits:** Reuse eliminates pod startup latency, avoids image pull time, reduces Kubernetes API load, and improves response
time.

**Reuse mechanism:** Pods are cached with a TTL that is extended on each use. Garbage collection removes expired pods, and failed pods
are deleted immediately.

### Cache Warming

**Warming strategy:** Pods are pre-created for FunctionConfig objects that declare a `podExecutor` with at least one tag. The first
prefix and first tag are used to build the image name, and pods are created concurrently at startup, which reduces first-request latency.

**Warming benefits:** Pods are ready before the first request, so performance is predictable, there is no cold-start penalty, and the
user experience is better.

### Concurrent Requests

**Concurrency handling:** Multiple requests can execute concurrently, each with its own gRPC connection. The pod cache manager
coordinates access and uses round-robin load balancing across equal-load pods.

**Concurrency characteristics:** The same function can run in parallel on the same pod and concurrently on different pods. Different
functions are fully concurrent, with no artificial concurrency limits.

**Load balancing:** Requests go to the least-loaded pods, with round-robin among pods that have equal load. That keeps work evenly
distributed and prevents hotspotting on a single pod.

### Resource Limits

**Resource considerations:** Function pods have resource limits that prevent resource exhaustion. Limits are configurable via the base
PodTemplate and FunctionConfig `templateOverrides`, and they affect concurrent execution capacity.

**Performance tuning:** Adjust pod TTL for reuse frequency, configure cache warming for hot functions, use the executable evaluator for
the critical path, and monitor pod resource usage.