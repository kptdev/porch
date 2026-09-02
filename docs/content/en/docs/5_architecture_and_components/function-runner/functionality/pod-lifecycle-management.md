---
title: "Pod Lifecycle Management"
type: docs
weight: 2
description: |
  Detailed architecture of pod cache management, lifecycle operations, and garbage collection.
---

## Overview

Pod lifecycle management orchestrates the complete lifecycle of function execution pods from creation through caching, reuse, and eventual garbage collection. The system maintains a pool of ready-to-use pods for frequently executed functions while automatically cleaning up unused pods based on time-to-live policies.

The architecture consists of two primary components:

**Pod Cache Manager** - Central coordinator running in a single goroutine that manages the pod cache, handles evaluation requests, processes pod readiness notifications, and performs periodic garbage collection. Uses channels for all communication to eliminate mutex synchronization.

**Pod Manager** - Handles low-level pod and service operations including creating pods from templates, retrieving existing pods, waiting for readiness, and managing ClusterIP services for service mesh compatibility.

Key design characteristics:

- **Single-threaded cache management** eliminates race conditions
- **Channel-based communication** provides clean separation between components
- **Service mesh compatibility** through ClusterIP services fronting each pod
- **Template-based pod creation** from a base PodTemplate / ServiceTemplate plus FunctionConfig templateOverrides
- **TTL-based lifecycle** with automatic garbage collection
- **Failed pod detection** with immediate deletion and cache eviction
- **Pod warming** capability for pre-creating pods at startup

## Pod Cache Manager

### Cache Manager Architecture

The Pod Cache Manager runs as a single goroutine managing all pod caching operations synchronously. This design eliminates mutex locks and prevents race conditions by ensuring all cache modifications happen sequentially.

The manager maintains two data structures:

**Cache Map** - Maps function image names to pod information and gRPC client connections. Each entry contains the pod namespace/name and an active gRPC client connection to the pod's wrapper server. When multiple pods serve the same function, the cache uses a round-robin index to distribute requests evenly across pods with equal load.

**Waitlist Map** - Maps function image names to lists of channels waiting for pod readiness. When multiple evaluation requests arrive for the same function before a pod is ready, they queue in the waitlist to prevent duplicate pod creation.

The manager responds to three event types through a select loop:

```
┌──────────────────────────────────────────────────────────┐
│              Pod Cache Manager (Single Goroutine)        │
│                                                          │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│  │   Request    │  │  Pod Ready   │  │  GC Ticker   │    │
│  │   Channel    │  │   Channel    │  │              │    │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘    │
│         │                 │                 │            │
│         ▼                 ▼                 ▼            │
│  ┌──────────────────────────────────────────────────┐    │
│  │           Select Loop (Synchronous)              │    │
│  └──────────────────────────────────────────────────┘    │
│         │                 │                 │            │
│         ▼                 ▼                 ▼            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│  │ Cache Lookup │  │ Notify       │  │ Scan & Delete│    │
│  │ + Waitlist   │  │ Waiters      │  │ Expired Pods │    │
│  └──────────────┘  └──────────────┘  └──────────────┘    │
│                                                          │
│  ┌─────────────────────────────────────────────────┐     │
│  │  Cache: map[image] → {pod, grpcClient}          │     │
│  │  Waitlists: map[image] → []chan                 │     │
│  └─────────────────────────────────────────────────┘     │
└──────────────────────────────────────────────────────────┘
```

Architectural benefits:

- **No mutex contention** - Single goroutine eliminates need for locks
- **Sequential consistency** - All operations happen in order without races
- **Simple reasoning** - No concurrent access patterns to reason about
- **Efficient batching** - Can notify multiple waiters simultaneously
- **Predictable behavior** - Deterministic event processing order

### Channel-Based Communication

The cache manager uses buffered channels for communication with evaluation request handlers and the pod manager.

**Request Channel** (buffer size 128) - Evaluation handlers send clientConnRequest messages containing the function image name and a response channel. The buffer allows burst handling without blocking handlers.

**Pod Ready Channel** (buffer size 128) - Pod manager sends imagePodAndGRPCClient messages when pods become ready or encounter errors. The buffer prevents pod manager goroutines from blocking during pod creation.

The response channel in each request is buffered with size 1 to prevent blocking when the cache manager sends the gRPC client back to the waiting handler.

### Waitlist Mechanism

Waitlists prevent duplicate pod creation when multiple evaluation requests arrive for the same function before a pod is ready.

**Waitlist lifecycle:**
- First request creates waitlist and triggers pod creation
- Subsequent requests add their response channels to existing waitlist
- When pod ready, all waiters notified simultaneously with gRPC client
- Waitlist deleted after notification
- Pod creation errors sent to all waiters

This mechanism ensures only one pod creation attempt per function image, even under high concurrent load.

## Pod Manager

### Pod Manager Responsibilities

The Pod Manager handles low-level Kubernetes operations for function pods and their frontend services, operating asynchronously in separate goroutines spawned by the cache manager.

**Core responsibilities:**

**Pod Lifecycle Operations** - Retrieve existing pods by label lookup, create new pods from templates, wait for pods to reach Running state with Ready condition, validate pod template versions, patch pod metadata with the image label and template-version annotation.

**Service Management** - Create ClusterIP services fronting each function pod, retrieve existing services, wait for service endpoints to become active, verify pod IP matches service endpoint IP.

**Image Metadata Operations** - Cache image digests and entrypoints, inspect container images to extract configuration, handle private registry authentication and TLS configuration, manage image pull secrets for function pods.

**Template System** - Load the `base-pod-template` PodTemplate and `base-service-template` ServiceTemplate (creating them from inline defaults if missing), merge FunctionConfig `templateOverrides`, and track the PodTemplate resourceVersion so pods are replaced when the base template changes.

### Pod Template System

The pod manager always starts from two cluster objects in the function-pod namespace: `base-pod-template` (`corev1.PodTemplate`) and `base-service-template` (`ServiceTemplate`).
If a get returns NotFound, the manager creates the object from the inline default compiled into the function-runner (the same spec as `deployments/porch/22-function-templates.yaml`).

After the function image, wrapper-server command, entrypoint args, and metadata annotations are patched onto a copy of the PodTemplate, `spec.podExecutor.templateOverrides` from the matching FunctionConfig is merged.
Overrides can set `serviceAccountName`, a pod `securityContext`, and resource / env / envFrom on the init container and the function container.

The PodTemplate `resourceVersion` is stored on the pod as `fn.kpt.dev/template-version`.
When the live template is newer, the next reuse deletes the old pod and creates a replacement.
See [Pod Templates]({{% relref "/docs/6_configuration_and_deployments/configurations/components/function-runner-config/pod-templates.md" %}}) for editing guidance.

### Container Configuration

The pod manager patches the function container in the template with function-specific configuration before creating the pod:

**Image** - Set to the requested function image name

**Command** - Set to wrapper-server binary path in shared volume

**Args** - Constructed as: --port 9446 --max-request-body-size <size> -- <original entrypoint>

The original entrypoint is extracted from the image metadata (either from image config's Entrypoint field or Cmd field as fallback). The wrapper server receives these args and executes the original entrypoint after setting up gRPC server.

**Volume Mounts** - Shared volume mounted at /wrapper-server-tools for accessing wrapper server binary

**Image Pull Secrets** - For private registry images, the registry auth secret name is appended to imagePullSecrets if not already present

### Pod Metadata Patching

Before creating a pod, the pod manager patches metadata fields for cache management and tracking.
It sets the `fn.kpt.dev/template-version` annotation to the PodTemplate `resourceVersion` (so a later template edit can force replacement) and the `fn.kpt.dev/image` label for lookup and service selectors.
The base template may already include `cluster-autoscaler.kubernetes.io/safe-to-evict: "true"`.
Idle-pod TTL is tracked in the function-runner process, not as a pod annotation.

## Service Management

### Service Mesh Compatibility

Each function pod has a dedicated ClusterIP service fronting it to provide stable DNS-based access and enable service mesh compatibility. Services are created independently of pods and persist across pod replacements.

**Service-Pod Mapping:**

Services map to pods through label selectors. The service selector matches the pod's image label, allowing pod replacement where new pods automatically join the service when labeled correctly, and providing stable addressing where the service DNS name remains constant across pod lifecycle.

**DNS-Based Access:**

The gRPC client connects to the service using its fully qualified DNS name rather than directly to the pod IP. DNS name format: `<service-name>.<namespace>.svc.cluster.local:9446`

The pod manager validates that the gRPC client's target matches the service DNS name when reusing cached connections. If the target doesn't match, the cache entry is evicted and a new pod/service is created.

**Endpoint Activation:**

Before returning a gRPC client, the pod manager waits for both the pod to be ready and the service endpoint to become active. This polling approach ensures the service is fully functional before evaluation requests are sent, prevents connection failures, detects misconfigurations, and provides service mesh compatibility by allowing time for sidecar injection and startup.

## Cache Operations

### Cache Validation

When an evaluation request arrives and a cached pod exists for the function image, the cache manager validates the cached entry before reusing it.

**Validation checks:**
1. **Pod exists** - Cached pod still exists in Kubernetes (not deleted externally)
2. **Not failed** - Pod status is not Failed (immediate deletion if failed)
3. **Not deleting** - Pod DeletionTimestamp is nil
4. **Service exists** - Frontend service still exists
5. **Target matches** - gRPC client target matches service DNS name

If any validation fails, the cache entry is evicted and a new pod creation is triggered.

### Cache Miss Handling

When no cached pod exists for a function image, the cache manager checks the waitlist to prevent duplicate pod creation. If a waitlist exists, the request is added to it. If no waitlist exists, a new waitlist is created, pod creation is triggered, and the request is added to the waitlist.

### Pod Reuse

Pod reuse provides significant performance benefits by eliminating pod startup time for subsequent evaluations.
Each reuse updates the in-memory `lastActivity` timestamp so the garbage collector keeps the pod alive.

**Reuse benefits:**
- Faster evaluation (no pod startup delay, typically 5-15 seconds saved)
- Reduced API load (fewer pod create/delete operations)
- Lower resource churn (stable pod population reduces scheduler load)
- Cost efficiency (amortize startup cost across multiple evaluations)

**TTL update on reuse:**

When a cached pod is reused, the cache manager records the current time as `lastActivity`.
That update is in memory and does not patch the pod.

## Pod Lifecycle Stages

### Pod Readiness

The pod manager polls the pod status to detect when it becomes ready for serving evaluation requests. Readiness requires both the pod phase to be "Running" and the Ready condition to be "True".

The polling loop checks these criteria every 100 milliseconds with a 60-second timeout. If the pod doesn't become ready within the timeout, an error is returned and sent to waiting evaluation requests.

**Health Probe Configuration:**

Pods include a readiness probe that executes grpc-health-probe to verify the wrapper server is accepting connections. The probe checks the gRPC health checking protocol implemented by the wrapper server. Kubernetes marks the pod ready only when the probe succeeds, ensuring the wrapper server is fully initialized.

### Pod Warming

Pod warming pre-creates function pods at startup so the first evaluation of a bundled function does not pay cold-start latency.
When `--warm-up-pod-cache` is true (the default), the cache manager walks every FunctionConfig in its store.
For each object that has a `podExecutor` with at least one tag, it starts one pod using the first prefix (or the default image prefix) and the first tag.

**Concurrent Creation:**

Warming creates those pods concurrently. Each function is processed in a separate goroutine with a 1-minute timeout per pod.
Using fixed names ensures only one pod is created per function even if multiple function-runner instances start simultaneously.

**Startup Optimization:**

Warming provides significant performance benefits for the first evaluation of each function. Without warming, first evaluation takes 5-15 seconds (pod creation + image pull + startup). With warming, first evaluation takes <100ms (pod already ready).

Warming is particularly valuable for high-traffic functions, latency-sensitive workflows, large images, and startup-heavy functions.

## Garbage Collection

### TTL-Based Expiration

Each cached pod has an in-memory `lastActivity` timestamp.
The garbage collector deletes a pod when it has no waiters and `time.Since(lastActivity)` exceeds the TTL for that image.

**TTL lifecycle:**
- **Pod Creation / first use** - `lastActivity` set to now
- **Pod Reuse** - `lastActivity` updated to now
- **GC Scan** - idle pods whose last activity is older than TTL are deleted

This keeps frequently used pods alive without writing TTL onto the pod object.

**TTL configuration:**
- Default TTL is the function-runner `--pod-ttl` flag (default 30 minutes)
- Per-function TTL comes from FunctionConfig `spec.podExecutor.timeToLive`
- Activity is refreshed in memory on each reuse

### GC Scan Process

The garbage collector runs periodically on a configurable interval (default 1 minute).
Each scan walks the in-memory pod cache and removes unhealthy pods plus idle pods that have exceeded their TTL.

The GC runs synchronously in the cache manager's select loop, ensuring no concurrent modifications to the cache during garbage collection.

**Scan operations:**
- For each cached function image, inspect its pods
- Delete immediately if the pod is Failed, missing, or its service is gone
- Evict if the gRPC target no longer matches the service DNS name
- If the pod is idle (empty waitlist) and `lastActivity` is older than TTL, delete the pod and service
- Drop empty cache entries

### Failed Pod Handling

Pods in Failed state are handled specially by the garbage collector with immediate deletion regardless of TTL. This prevents failed pods from consuming resources and blocking new pod creation.

Failed pods are also detected during cache validation when evaluation requests arrive. If a cached pod is found to be in Failed state, it is immediately deleted and evicted from the cache, triggering creation of a fresh pod.

**Failed pod scenarios:**
- Application crash (function container exits with non-zero code)
- OOM kill (container exceeds memory limit)
- Image pull backoff (repeated image pull failures)
- Liveness probe failure (container becomes unhealthy)

Immediate deletion ensures failed pods don't impact evaluation latency and resources are freed quickly.

### Pod and Service Deletion

When a pod expires or fails, both the pod and its frontend service are deleted. Deletion happens asynchronously in separate goroutines to avoid blocking the GC scan.

The service deletion ensures no orphaned services accumulate over time. The cache eviction ensures the cache manager won't try to reuse a deleted pod. Deletion is best-effort - if deletion fails (e.g., pod already deleted), errors are logged but don't prevent cache eviction or subsequent GC scans.

## Cache Consistency

### Validation Mechanisms

The cache maintains consistency through multiple validation mechanisms:

**Pod Validation** - Cached pods are validated before reuse to ensure they still exist, are not in Failed state, are not being deleted, and have matching service endpoints.

**Service Validation** - Services are validated to ensure they exist and have active endpoints matching the pod IP.

**Template Version Tracking** - Pod template versions are tracked to detect when templates change, triggering pod replacement on next use.

**Failed Pod Detection** - Failed pods are immediately detected and removed from cache during both validation and garbage collection.

### Cache Eviction

Cache entries are evicted in several scenarios:

**Validation Failure** - Pod doesn't exist, pod in Failed state, service doesn't exist, or target URL mismatch.

**Garbage Collection** - Pod expired based on TTL or pod in Failed state.

**Template Version Mismatch** - Pod has old template version when new template available.

Eviction triggers creation of a fresh pod on the next evaluation request for that function.

## Performance Optimization

### Pod Reuse Strategy

Pod reuse is the primary performance optimization, eliminating pod startup latency for subsequent evaluations. The cache manager extends the pod's TTL on each reuse, keeping frequently used pods alive indefinitely.

**Performance impact:**
- Eliminates 5-15 seconds of pod startup time per evaluation
- Reduces Kubernetes API load from pod create/delete operations
- Lowers resource churn and scheduler load
- Amortizes startup cost across multiple evaluations

### Cache Warming

Cache warming pre-creates pods at startup for FunctionConfig images that declare a `podExecutor`.
All such pods are created concurrently to minimize startup time.

**Warming benefits:**
- First evaluation served from ready pod (<100ms instead of 5-15 seconds)
- Predictable performance for high-traffic functions
- Reduced latency for latency-sensitive workflows
- Amortized image pull time for large images

### Concurrent Operations

The system supports concurrent operations efficiently:

**Concurrency characteristics:**
- Multiple evaluation requests can execute concurrently
- Each request gets own gRPC connection
- Pod cache manager coordinates access via channels
- Waitlist prevents duplicate pod creation
- Function evaluations can execute in parallel within the same pod

**Pod Selection Strategy:**

When multiple pods serve the same function, the cache manager selects the pod with the least load (smallest waitlist length). When multiple pods have equal load, the system uses round-robin distribution to ensure even load balancing across pods rather than always selecting the first available pod.

**Concurrency benefits:**
- Same function, same pod: Parallel (multiple evaluations execute concurrently)
- Same function, different pods: Concurrent
- Different functions: Fully concurrent
- No artificial concurrency limits

### Resource Management

Function pods have resource limits configured via the base PodTemplate and FunctionConfig `templateOverrides` to prevent resource exhaustion.
Limits affect concurrent execution capacity and should be tuned based on function requirements and cluster resources.

## Concurrency Model

### Single-Threaded Cache Management

The cache manager runs entirely in a single goroutine, processing events sequentially through a select loop. This design eliminates all concurrency concerns within the cache manager.

**Benefits:**
- **No race conditions** - Only one thread accesses cache and waitlist maps
- **No mutex overhead** - No lock acquisition or contention
- **Simple reasoning** - Sequential execution is easy to understand and debug
- **Deterministic behavior** - Events processed in arrival order
- **No deadlocks** - No lock ordering or circular dependencies

### Channel Synchronization

Channels provide the synchronization mechanism between the cache manager and external components. All communication uses channels rather than shared memory.

**Channel usage patterns:**

**Request Channel** (buffered, size 128) - Evaluation handlers send requests, cache manager receives and processes, buffer prevents handler blocking under load.

**Pod Ready Channel** (buffered, size 128) - Pod manager goroutines send ready notifications, cache manager receives and updates cache, buffer prevents pod manager blocking.

**Response Channels** (buffered, size 1) - Each request includes unique response channel, cache manager sends gRPC client or error, buffer prevents cache manager blocking.

Buffered channels enable asynchronous communication while maintaining backpressure under extreme load.

### Mutex-Free Cache Access

The cache and waitlist maps are accessed only by the cache manager goroutine, eliminating the need for mutex protection. This provides significant performance benefits including no lock contention, no false sharing, better CPU utilization, and simpler code.

Other goroutines (evaluation handlers, pod manager) never directly access the cache. They communicate only through channels, maintaining clean separation.

### Race Condition Prevention

The architecture prevents common race conditions through careful design:

**Duplicate pod creation** - Waitlist mechanism ensures only one creation attempt per image

**Cache invalidation** - Validation checks ensure cached pods are still valid before reuse

**Concurrent updates** - Single goroutine processes all updates sequentially

**TTL races** - TTL updates are asynchronous patches that don't affect cache state

**Deletion races** - Cache eviction happens before deletion, preventing use-after-delete

The combination of single-threaded cache management and channel-based communication eliminates the need for complex synchronization primitives while maintaining high performance and correctness.
