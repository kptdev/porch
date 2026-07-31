---
title: "Rendering"
type: docs
weight: 2
description: |
  KRM function pipeline execution, concurrency control, and stale render detection.
---

## Overview

Rendering runs the KRM function pipeline defined in the package's Kptfile. The controller reads resources from the shared cache, invokes kpt render through the function runner (gRPC), and writes the rendered output back to the cache.

## Triggers

Two events can trigger rendering:

1. **Content push via PRR**: When a user edits `PackageRevisionResources`, the API Server patches the `porch.kpt.dev/render-request` annotation on the PackageRevision CRD. The controller's predicate filter detects this and triggers a reconcile.
2. **Source execution completion**: After init, clone, copy, or upgrade produces initial content (including a generated or copied Kptfile), the controller immediately proceeds to render. For init with an empty Kptfile, rendering may produce no additional output.

The controller tracks the annotation value in `status.observedPrrResourceVersion` to avoid re-rendering the same content.

## Concurrency Limiting

Rendering is resource-intensive (gRPC calls to the function runner, CPU for function execution). The controller uses a channel-based semaphore to bound concurrent renders to a configurable limit (default 20), independent of the overall reconcile concurrency (default 50).

When the semaphore is full, the reconcile does not block. It returns a `RequeueAfter` result and tries again after a configurable delay (default 2s). This keeps the controller responsive and prevents it from overwhelming the function runner.

## Stale Render Detection

A race exists between rendering and content pushes. While the controller is rendering (which may take seconds), the user might push new content, changing the render-request annotation. Writing back stale results would overwrite the user's latest content.

To handle this, after rendering completes the controller re-reads the PackageRevision directly from etcd (bypassing the informer cache) and compares the current annotation value with the one that triggered the render. If they differ, the render results are discarded and the reconcile requeues to pick up the newer content.

## Status Reporting

The `Rendered` condition reflects the current state:

| Status | Meaning |
|--------|---------|
| True | Rendering succeeded for the current content |
| False | Rendering failed (error message in `message` field) |
| Unknown | Rendering is in progress |

The controller does not retry failed renders automatically. Once a render fails, the observed version is recorded so the same content will not re-trigger. A new content push (which updates the render-request annotation) is required to trigger another render attempt.
