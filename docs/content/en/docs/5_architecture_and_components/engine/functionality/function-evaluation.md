---
title: "Function Evaluation"
type: docs
weight: 5
description: |
  Detailed architecture of function evaluation strategies and execution patterns.
---

## Overview

Function evaluation is hosted by the Engine (porch-server and the PackageRevision controller). This page moved from Function Runner with the pod evaluator. The Engine chains builtin Go functions, the Function Runner executable evaluator (via gRPC `exec_path`), and the in-process pod evaluator. The system uses a strategy pattern where different evaluators handle function execution in different ways (pod-based, executable, or chained), all conforming to a common interface.

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
- **ResourceList**: Serialized KRM resources as YAML bytes
- **ExecPath**: Absolute path of a cached function binary (gRPC field `exec_path`). Set by the Engine from the FunctionConfig store. If empty, the Function Runner executable evaluator returns NotFoundError so evaluation can fall through to the pod evaluator.

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

**Pod Evaluator:**
- Executes functions in Kubernetes pods
- Uses wrapper server for gRPC interface
- Manages pod cache with TTL-based expiration
- Handles service mesh compatibility via ClusterIP services

**Executable Evaluator:**
- Runs in the Function Runner gRPC service
- Engine looks up the binary in the FunctionConfig store and sends `exec_path` on the request
- Fast execution without pod overhead
- Empty or missing `exec_path` returns NotFoundError so the Engine can fall through to the pod evaluator

**Multi-Evaluator:**
- Chains multiple evaluators together
- Tries each evaluator in sequence
- Falls back on NotFoundError
- Returns first successful response

## Pod Evaluator

Executes functions in Kubernetes pods with caching and lifecycle management.

### Channel-Based Communication

The pod evaluator uses channels for communication with the pod cache manager:

**Communication pattern:**
- Request sent via channel to pod cache manager
- Blocks waiting for gRPC client from cache manager
- Cache manager handles pod creation/reuse
- Direct gRPC call to wrapper server in pod

**Channel characteristics:**
- **Buffered channel**: Size 1 to prevent blocking
- **One-way communication**: Request sent, client received
- **Synchronization point**: Blocks until client available
- **Error propagation**: Errors sent through same channel

**Benefits:**
- Decouples evaluation from pod management
- Single goroutine manages pod cache (no race conditions)
- Natural backpressure when pods unavailable
- Clean separation of concerns

### Client Connection Management

The pod cache manager maintains gRPC connections to function pods:

**Cache structure:**
- Map from image name to pod and gRPC client
- Includes pod object key for validation
- Stores gRPC client connection for reuse

**Connection validation:**
- Pod still exists (not deleted externally)
- Pod not in Failed state
- Service still exists for pod
- gRPC client target matches service URL
- Pod not being deleted (DeletionTimestamp nil)

**Failed pod handling:**
- Immediately delete failed pods
- Evict from cache
- Trigger new pod creation
- Prevents reusing broken pods

### Waitlist Mechanism

Prevents duplicate pod creation when multiple requests arrive for the same function:

**Waitlist pattern:**
- Multiple requests for same image queue up
- Single pod creation serves all waiters
- Batch notification when pod ready
- Prevents duplicate pod creation

**Error handling:**
- Pod creation errors sent to all waiters
- Waitlist cleared on error
- Each waiter receives error independently
- Allows retry on next request

### Function Execution

Once gRPC client acquired, function execution proceeds:

**Execution characteristics:**
- Synchronous gRPC call
- Context passed through for cancellation
- Timeout enforced by context deadline
- Stderr logged even on success
- Detailed logging on errors

## Executable Evaluator

Executes pre-cached function binaries locally for fast execution.

### exec_path resolution

The Engine resolves cached binaries before calling Function Runner. Function Runner does not map images itself.

**Resolution:**
- FunctionConfig store lookup by image (and optional tag constraint)
- If a binary exists under `--functions`, Engine sets `exec_path` on `EvaluateFunctionRequest`
- If no binary is cached, Engine does not call Function Runner and returns NotFoundError (fallback to pod evaluator)

**Function Runner checks:**
- Empty `exec_path` → NotFoundError
- `exec_path` must stay under the `--functions` directory (sandbox)
- Binary is executed with ResourceList on stdin

### Function Cache Lookup

**Lookup characteristics:**
- Engine: FunctionConfig store lookup by image name / tag
- Fast path when a binary is already on disk
- NotFoundError triggers fallback in the Engine multi-runtime
- Function Runner performs no image-to-path mapping

### Local Execution

Binary execution happens in-process:

**Execution characteristics:**
- Direct process execution (no container)
- ResourceList passed via stdin
- Output captured from stdout
- Stderr captured for logging
- Context-aware (respects cancellation)

**Performance benefits:**
- No pod startup latency
- No image pull time
- No Kubernetes API overhead
- Millisecond execution times
- Predictable performance

## Multi-Evaluator

Chains multiple evaluators with fallback logic.

### Evaluator Chaining

**Chaining characteristics:**
- Sequential evaluation (not parallel)
- First success wins
- Only NotFoundError triggers fallback
- Other errors returned immediately
- Preserves error semantics

**Typical chain (Engine multi-runtime):**
1. **Builtin runtime** (in-process Go functions)
2. **Function Runner executable evaluator** (gRPC, `exec_path` from FunctionConfig cache)
3. **Pod evaluator** (in-process in porch-server / PR controller)

### Fallback Strategy

**Fallback conditions:**
- Only on NotFoundError from evaluator
- Other errors (timeout, execution failure) don't trigger fallback
- Preserves error information from failed evaluator

**Fallback rationale:**
- NotFoundError indicates function not available in current evaluator
- Other errors indicate actual execution problems
- Fallback only makes sense for missing functions
- Prevents masking real errors

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

**Wrapper server responsibilities:**
- Accept gRPC EvaluateFunction requests
- Execute function entrypoint with ResourceList
- Capture stdout/stderr from function
- Parse structured results from output
- Return gRPC response with results
- Provide health check endpoint

### Entrypoint Wrapping

The wrapper server wraps the original function entrypoint:

**Entrypoint characteristics:**
- Original function entrypoint preserved
- Passed as arguments after separator
- Executed as subprocess
- Stdin/stdout/stderr captured

### Resource List Processing

**Processing characteristics:**
- ResourceList passed as raw bytes to stdin
- Function reads from stdin
- Function writes to stdout
- Standard KRM function protocol
- No modification of ResourceList format

### Structured Results Handling

The wrapper server parses structured results from function output:

**Structured results:**
- ResourceList contains results array
- Each result has severity, message, tags
- Exit code indicates overall success/failure
- Results provide detailed feedback

**Exit code semantics:**
- **0**: Function succeeded
- **Non-zero**: Function failed
- **Parse failure**: Wrapper server error
- **Execution error**: System error

## Execution Characteristics

### Synchronous Execution

**All evaluators execute synchronously:**
- Block until function completes
- No async callbacks or futures
- Simple request-response pattern
- Caller waits for result

**Benefits:**
- Simple programming model
- Easy error handling
- Predictable behavior
- No concurrency complexity

### Context Cancellation

**Context propagation:**
- Context passed through all layers
- Execution stops on cancellation
- Resources cleaned up
- Error returned to caller

### Timeout Handling

**Timeout sources:**
- Task Handler sets context deadline
- Function Runner respects deadline
- Evaluators check context
- Function execution cancelled on timeout

**Timeout behavior:**
- Execution stops immediately
- Partial results discarded
- Timeout error returned
- Resources cleaned up

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

**Retry mechanisms:**
- NotFoundError triggers fallback to next evaluator
- Transient errors may succeed on retry
- Waitlist allows retry on next request

**No retry scenarios:**
- Execution errors (function failed)
- Timeout errors (deadline exceeded)
- System errors (infrastructure problems)

## Performance Optimization

The evaluation system employs several performance strategies.

### Pod Reuse

**Reuse benefits:**
- Eliminates pod startup latency
- Avoids image pull time
- Reduces Kubernetes API load
- Improves response time

**Reuse mechanism:**
- Pod cache with TTL
- TTL extended on each use
- Garbage collection removes expired pods
- Failed pods immediately deleted

### Cache Warming

**Warming strategy:**
- Pre-create pods for frequently-used functions
- Configuration file specifies functions and TTLs
- Concurrent pod creation at startup
- Reduces first-request latency

**Warming benefits:**
- Pods ready before first request
- Predictable performance
- No cold start penalty
- Better user experience

### Concurrent Requests

**Concurrency handling:**
- Multiple requests can execute concurrently
- Each request gets own gRPC connection
- Pod cache manager coordinates access
- Round-robin load balancing across equal-load pods

**Concurrency characteristics:**
- Same function, same pod: Parallel execution supported
- Same function, different pods: Concurrent
- Different functions: Fully concurrent
- No artificial concurrency limits

**Load balancing:**
- Requests distributed to least-loaded pods
- Round-robin among pods with equal load
- Ensures even work distribution
- Prevents hotspotting on single pod

### Resource Limits

**Resource considerations:**
- Function pods have resource limits
- Limits prevent resource exhaustion
- Configurable via pod template
- Affects concurrent execution capacity

**Performance tuning:**
- Adjust pod TTL for reuse frequency
- Configure cache warming for hot functions
- Use executable evaluator for critical path
- Monitor pod resource usage
