---
title: "Functionality"
type: docs
weight: 3
description: |
  Core functionality provided by the Porch API server.
---

## Overview

The Porch API Server provides the Kubernetes API interface for Porch resources. It implements custom REST storage backends that delegate to the Engine, enforces validation and admission policies through strategies, and manages real-time watch streams for clients.

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│              API Server Functionality                   │
│                                                         │
│  ┌──────────────────┐      ┌──────────────────┐         │
│  │   REST Storage   │      │   Strategies     │         │
│  │                  │ ───> │                  │         │
│  │  • CRUD Ops      │      │  • Validation    │         │
│  │  • Watch Streams │      │  • Admission     │         │
│  │  • Engine Deleg  │      │  • Table Conv    │         │
│  └──────────────────┘      └──────────────────┘         │
│           │                         │                   │
│           └────────┬────────────────┘                   │
│                    ↓                                    │
│          ┌──────────────────┐                           │
│          │   Background     │                           │
│          │   Operations     │                           │
│          │                  │                           │
│          │  • Cleanup       │                           │
│          └──────────────────┘                           │
└─────────────────────────────────────────────────────────┘
```

## REST Storage Implementation

See [Design]({{% relref "/docs/5_architecture_and_components/porch-apiserver/design.md#rest-storage-interface" %}}) for the design rationale behind custom REST storage.

The API Server implements custom REST storage for each Porch resource type:

### PackageRevision Storage

**CRUD operations:**
- **Create**: Validates spec, calls Engine to create package revision, returns created resource
- **Get**: Filters Engine package revision list by name, returns single resource
- **List**: Calls Engine to list package revisions with filters, returns resource list
- **Update**: Validates resource version, calls Engine to update package revision, returns updated resource
- **Delete**: Calls Engine to delete package revision, returns delete status

The Porch API server supports watches by delegating to the Engine cache. This system filters events based on defined watch criteria, delivers real-time change notifications, and automatically cleans up resources upon client disconnection.

The storage of PackageRevisions is also delegated to the Engine; there is no direct etcd storage. The Engine is responsible for managing package data in Git via its Cache, supports all standard Kubernetes operations, and fully implements the storage.Interface.

### PackageRevisionResources Storage

**Operations:**
- **Get**: Retrieves package content via Engine. Optional `file` query parameters select which files to return; omitting them returns the full package.
- **List**: Lists resources for package revisions
- **Update**: Updates package content via Engine. With `partial=true`, submitted files are merged into the existing package; without it, `spec.resources` replaces the whole package.

Package contents are handled by storing resources as a map where each filename is associated with its content. This design supports large package content, bypassing limitations that might be imposed by etcd. Furthermore, any updates to this content automatically trigger the execution of the render pipeline, and the system provides a RenderStatus along with the function results.

Selectors are parsed from the resource name in the request (`<name>?file=Kptfile` or `<name>?partial=true`) because the Kubernetes aggregated GET/UPDATE handlers receive the path name, not a separate query object. Quote the name in the shell so `?` is not treated as a glob.

**Get file filter:** Repeat `file` for each path (`?file=Kptfile&file=README.md`). The response `spec.resources` map contains only those files. An unknown path returns an error.

**Partial update:** Recognized `partial` values are `true`, `yes`, `1`, or empty (`?partial` or `?partial=true`). The Engine copies the current package, overwrites or adds the submitted files, then runs the usual render pipeline on the merged package. Omitted files stay in storage. Multiple `partial` values in one request are rejected.

PackageRevisionResources storage is handled such that operations are primarily read-only for most operations. Updates are exclusively permitted on Draft packages. Content is retrieved on-demand, meaning it is not cached within the API server, and all storage-related operations are delegated to the Engine.

### Package Storage

**Operations:**
- **Get**: Filters Engine package list by name
- **List**: Calls Engine to list packages with filters

Package aggregation serves to represent a complete package by encompassing all its revisions, tracking the latest version, and providing essential package-level metadata.

## Validation Strategies

See [Design]({{% relref "/docs/5_architecture_and_components/porch-apiserver/design.md#validation-strategy" %}}) for the design rationale behind validation strategies.

Strategies enforce validation rules before Engine operations:

### Create Validation

**Validation rules:** Includes ensuring required fields are present (such as package name and repository), adhering to lifecycle constraints (preventing creation of Published/DeletionProposed states), validating tasks (allowing a maximum of one task), confirming the correct workspace name format, and verifying package path validity.

**Validation process:** Validation is done for each strategy before any Engine operation. Specific field errors are returned for invalid specifications, effectively preventing invalid resources from reaching the Engine and providing clear error messages to clients.

### Update Validation

**Validation rules:** Includes requiring a resource version for optimistic locking, ensuring lifecycle transition validity, enforcing immutability constraints for Published packages, making tasks append-only (meaning tasks cannot be removed), and restricting metadata updates.

**Validation process:** Involves invoking strategy validation prior to any Engine update operation. This process meticulously compares old and new objects to confirm that all proposed changes are permissible, and it subsequently returns field errors for any updates that are deemed invalid.

### Status Validation

**Validation rules:** Status subresource updates are validated separately. This includes condition format, RenderStatus structure as well as DownstreamTargets.

**Validation process:** Involves calling `Strategy.ValidateStatusUpdate` for all status updates. This ensures that status updates do not inadvertently modify the specification, validates the overall status structure, and returns detailed field errors when an invalid status is detected.

## Admission Control

See [Design]({{% relref "/docs/5_architecture_and_components/porch-apiserver/design.md#admission-strategy" %}}) for the design rationale behind admission control.

Strategies apply admission policies and defaults:

### PrepareForCreate

**Operations:** Generating a name if one is not provided, setting the default lifecycle to Draft, initializing status conditions, setting the creation timestamp, and adding default labels/annotations.

**Admission process:** Called before validation, it modifies the resource in-place to ensure a consistent initial state and prepares the resource for the Engine.

### PrepareForUpdate

**Operations:** Validating the resource version, enforcing immutability rules, preserving status on specification updates, updating the modification timestamp, and merging labels/annotations.

**Admission process:** Called before validation, it checks the current versus desired state, enforces business rules, and prepares the resource for the Engine.

### Canonicalization

**Operations:** Normalizing resource representation, removing redundant fields, applying default values, and ensuring a consistent format.

**Canonicalization process:** Called after validation, it ensures a consistent storage format, simplifies comparison operations, and improves cache efficiency.

## Table Conversion

See [Design]({{% relref "/docs/5_architecture_and_components/porch-apiserver/design.md#table-conversion-strategy" %}}) for the design rationale behind table conversion.

Strategies convert resources to table format for kubectl:

### Column Definitions

**PackageRevision columns**
| Column Name     | Description                       |
|-----------------|-----------------------------------|
| Name            | Resource name                     |
| Package         | Package name                      |
| WorkspaceName   | Workspace identifier              |
| Revision        | Revision number                   |
| Lifecycle       | Current lifecycle state           |
| Repository      | Source repository                 |

The table format adheres to Kubernetes conventions, supports sorting and filtering, and provides human-readable output consistent with `kubectl` expectations.

### Conversion Process

**Conversion flow:** When `kubectl` requests a table format, the REST storage calls `Strategy.ConvertToTable`. This strategy then extracts the necessary column values and returns a `metav1.Table` with rows, which kubectl subsequently formats for display.

**Conversion characteristics:** This conversion process supports both list and individual resources, gracefully handles missing fields, provides consistent formatting, and enables various `kubectl` get commands.

## Watch Stream Management

See [Interactions]({{% relref "/docs/5_architecture_and_components/porch-apiserver/interactions.md#watch-stream-management" %}}) for how watch streams integrate with other components.

The API Server provides real-time watch streams:

### Watch Registration

**Registration process:** The registration process begins when a client sends a watch request that includes specific filters. The REST storage then calls the Engine cache to watch for package revisions, and the WatcherManager registers a watcher with these filters. Subsequently, a watch interface is returned to the client, allowing the client to receive events as they occur.

**Filter support:** The system offers robust filter support, including namespace filtering, label selectors, field selectors, and the ability to resume from a specific resource version.

### Event Delivery

**Event types:**
- **Added**: New package revision created
- **Modified**: Package revision updated
- **Deleted**: Package revision removed

Events are delivered in real-time, filtered based on watch criteria, and ordered per resource. However, delivery is best-effort, meaning network failures may result in dropped events.

### Watch Lifecycle

**Lifecycle stages:**
- **Registration**: Client subscribes with filters
- **Active**: Events delivered as changes occur
- **Disconnection**: Client closes connection or timeout
- **Cleanup**: WatcherManager removes watcher automatically

Cleanup triggers include the client disconnecting, cancelling context, watch timeout and error during event delivery.

## Background Operations

The API Server delegates certain background operations to separate controller components for better separation of concerns and scalability.

### Repository Synchronization

Repository synchronization is handled by the dedicated [Repository Controller]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller/_index.md" %}}), which runs as a separate component using the controller-runtime framework.

The Repository Controller manages:
- Repository resource lifecycle and reconciliation
- Periodic and on-demand repository synchronization
- Repository health checks and status updates
- Cache updates and invalidation
- Repository deletion and cleanup

See the [Repository Controller documentation]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller/_index.md" %}}) for details on sync configuration, scheduling, and implementation.

### Resource Cleanup

**Cleanup operations:** These operations include removing PackageRev CRs for deleted repositories, cleaning up orphaned cache entries, removing stale watch registrations, and garbage collecting expired resources.

**Cleanup triggers:** Various events can trigger these cleanup processes, including repository deletion, cache eviction, and watch disconnection. Periodic maintenance routines also initiate cleanup to ensure system health.

**Cleanup coordination:** Cleanup is coordinated by being triggered through lifecycle events and runs asynchronously. This approach ensures consistency across the system and effectively prevents resource leaks.

## Performance Optimization

The API Server employs several optimization strategies:

### List Operation Optimization

**Optimization techniques:** Concurrent repository listing with configurable maximum concurrency, per-repository timeouts to prevent slow repositories from blocking, early termination on context cancellation, and efficient filtering at the Engine level.

**Configuration:**
- MaxConcurrentLists: Maximum concurrent repository operations
- ListTimeoutPerRepository: Timeout per repository
- Prevents slow repositories from impacting overall performance

### Watch Stream Efficiency

**Efficiency mechanisms:** The WatcherManager provides efficient fan-out. Events are filtered at the source rather than being delivered and then filtered, automatic cleanup of inactive watchers, and eliminating polling overhead.

**Scalability:** The system supports many concurrent watchers with minimal overhead per watcher, ensures efficient event delivery, and scales effectively with the number of clients.

### Cache Integration

**Cache benefits:** The Engine caches repository data, which significantly reduces Git operations, leading to faster list operations and more consistent performance.

**Cache coordination:** Managed by the Repository Controller, which keeps the cache fresh through periodic synchronization. Watch notifications are used for cache updates, and automatic cache invalidation occurs transparently to API clients.

## Error Handling

See [Interactions]({{% relref "/docs/5_architecture_and_components/porch-apiserver/interactions.md#error-handling" %}}) for error handling across component boundaries.

The API Server handles errors at multiple levels:

### Validation Errors

**Error handling:** Strategy validation processes field errors, which are then converted into a 400 Bad Request response, including the specific field path and an informative error message, ensuring the client receives detailed error information.

**Error examples:**
- "spec.lifecycle: cannot create Published package"
- "spec.tasks: maximum one task allowed"
- "metadata.name: invalid format"

### Engine Errors

**Error handling:** The Engine returns typed errors, which the REST storage then translates into a Kubernetes status, providing an appropriate HTTP status code and detailed error information within the status message.

**Error types:**
- NotFound → 404 Not Found
- Conflict → 409 Conflict
- Validation → 400 Bad Request
- Internal → 500 Internal Server Error

### Watch Errors

**Error handling:** Registration errors are returned immediately upon occurrence. Should delivery errors arise, the watch stream is closed, an error event is sent to the client, and an automatic cleanup process is initiated.

**Error recovery:** For error recovery, clients are able to re-establish the watch and resume from the last known resource version. This mechanism ensures that there is no data loss during transient errors, allowing for graceful degradation of service.

## Concurrency Control

See [Interactions]({{% relref "/docs/5_architecture_and_components/porch-apiserver/interactions.md#concurrency-and-safety" %}}) for concurrency patterns across component interactions.

The API Server handles concurrent operations:

### Request Concurrency

**Concurrency characteristics:** Multiple clients can make requests concurrently, with each request processed independently. The Engine provides concurrency control, utilizing optimistic locking to prevent conflicts.

**Concurrency patterns:** Regarding concurrency patterns, read operations are fully concurrent, while write operations are serialized per package using an Engine mutex. Watch streams operate independently, and resource cleanup operations are also concurrent.

### Optimistic Locking

**Locking mechanism:** When clients initiate updates, they are required to provide a resource version. The API Server then validates this version before making a call to the Engine, which in turn compares the provided version with its current version. If a mismatch is detected, a conflict is returned, necessitating the client to re-read the resource and retry the operation. This is how the base Kubernetes apiserver works as well.

**Locking benefits:** This locking mechanism offers several benefits, including the prevention of lost updates and the elimination of the need for complex distributed locks. It scales effectively and aligns with standard Kubernetes patterns.

### Watch Stream Safety

Each watch stream operates independently with no shared state between watchers. Event delivery is thread-safe, and automatic cleanup mechanisms are in place to prevent leaks, collectively providing robust safety guarantees.
