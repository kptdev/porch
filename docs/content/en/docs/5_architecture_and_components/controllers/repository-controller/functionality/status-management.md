---
title: "Status Management"
type: docs
weight: 3
description: |
  Repository status fields, conditions, and timestamp tracking.
---

## Overview

The Repository controller maintains detailed status information to track sync operations, detect changes, and provide observability. The status includes both standard Kubernetes conditions and custom fields specific to repository synchronization.

## Status Fields

### Repository Information

The `packageCount` field shows how many package revisions were discovered during the last sync.

### Git Commit Tracking

For git repositories, the `gitCommitHash` field contains the commit hash of the configured branch, enabling GitOps workflows to track which version is currently synced. 

### Change Detection

The `observedGeneration` field tracks which version of the Repository spec the controller has processed. When the spec changes, the generation increments, and the controller detects this to trigger immediate action.

For one-time syncs, the `observedRunOnceAt` field prevents duplicate syncs when the `spec.sync.runOnceAt` field is set, updated, or cleared.

## Status Example

```yaml
status:
  conditions:
  - type: RepositoryReady
    status: "True"
    reason: Ready
    message: "Repository Ready"
    lastTransitionTime: "2025-01-30T11:00:00Z"
  lastFullSyncTime: "2025-01-30T11:00:00Z"
  nextFullSyncTime: "2025-01-30T12:00:00Z"
  observedGeneration: 5
  observedRunOnceAt: null
  packageCount: 42
  gitCommitHash: "abc123def456..."
```

## Status Transitions

The repository moves through different states as syncs execute:

- **Successful full sync**: `Ready` → `SyncInProgress` → `Ready`
- **Failed full sync**: `Ready` → `SyncInProgress` → `Error`
- **Successful health check**: `Ready` → `Ready` (no transition)
- **Failed health check**: `Ready` → `Error`
- **Recovery**: `Error` → `Ready` (triggers immediate full sync)

Health checks can detect connectivity failures and transition the repository to error state. When a repository recovers from error (health check succeeds after being in error), the controller immediately triggers a full sync to ensure the cache is up-to-date.

## Conditions

The status uses standard Kubernetes conditions with type `RepositoryReady`:

| Condition Status | Reason | Meaning |
|-----------------|--------|----------|
| `True` | `Ready` | Repository is healthy and synced |
| `False` | `Reconciling` | Sync operation in progress |
| `False` | `Error` | Sync failed, will retry |
| `Unknown` | - | Initializing (rare) |

The condition's `lastTransitionTime` only updates when the status actually changes. If a repository is already `Ready` and a health check succeeds, the timestamp doesn't change. This prevents timestamp churn and makes it easier to see when the last real state change occurred.

### PackageRevisionSyncComplete

Repositories enabled for v1alpha2 (annotated with `porch.kpt.dev/v1alpha2-migration: "true"`) also carry a `PackageRevisionSyncComplete` condition. It reports whether the last full sync brought every PackageRevision resource in line with the packages discovered in the repository.

This condition is independent of `Ready`. `Ready` reports whether the repository is reachable. A repository can be `Ready=True` while some of its PackageRevisions failed to sync.

| Condition Status | Reason | Meaning |
|-----------------|--------|----------|
| `True` | `SyncComplete` | Every PackageRevision was created, updated, or deleted successfully |
| `False` | `PartialSyncFailure` | One or more PackageRevisions failed to sync; the others were synced |
| `False` | `SyncFailed` | The PackageRevision sync did not run to completion, so nothing was synced |
| `False` | `V1Alpha2SyncDisabled` | The repository is annotated for v1alpha2, but the controller runs with `--repositories.create-v1alpha2-rpkg=false` |

For `PartialSyncFailure`, the message gives the number of failed PackageRevisions and lists the first few, sorted by name, with their errors. For example: `2 of 18 PackageRevisions failed to sync: <name>: <error>; <name>: <error>`.

The condition is updated only at the end of a full sync:

- When the full sync reaches the PackageRevision sync step, the condition is set to one of the values in the table above.
- When the repository is not annotated for v1alpha2, the condition is removed.
- Health checks, cold-start opens, sync-in-progress updates, and full syncs that fail before the PackageRevision sync step (for example, when git is unreachable) leave the condition unchanged. Those failures are reported by `Ready=False`.

A successful sync of an individual PackageRevision never clears the condition. It returns to `True` only after a full sync in which every PackageRevision synced. After fixing the cause of a failure, the condition stays `False` until the next scheduled full sync. To schedule a full sync sooner (by default one minute later), run `porchctl repo sync <repo-name>`.

Only the Repository controller writes this condition. The PackageRevision controller never changes it.

## Server-Side Apply

The controller uses Server-Side Apply with field manager `repository-controller` for all status updates. This prevents conflicts when multiple reconciles happen concurrently. The `meta.SetStatusCondition()` function preserves timestamps when the condition hasn't actually changed, avoiding unnecessary updates.

## Timestamp Fields

The controller tracks multiple timestamps for different purposes:

| Field | Purpose | Used For |
|-------|---------|----------|
| `lastFullSyncTime` | Last successful full sync | Sync scheduling, cron calculation |
| `nextFullSyncTime` | Next scheduled full sync | Observability, monitoring |
| `observedRunOnceAt` | Last observed runOnceAt value | Prevent duplicate one-time syncs |
| `condition.lastTransitionTime` | Last status change | Health checks, error retry, stale detection |

These timestamps serve different purposes. The `lastFullSyncTime` is used for calculating when the next full sync should happen based on frequency or cron schedule. The `nextFullSyncTime` makes this calculation explicit for observability. The `observedRunOnceAt` prevents duplicate syncs when one-time sync fields are modified. The condition's `lastTransitionTime` tracks any status change, including health checks and errors.

This separation matters because cron schedules calculate from the last full sync, not from the last health check or error. If health checks run every 5 minutes but full syncs happen every hour, the cron calculation uses the full sync timestamp.

When a repository has never been synced, `lastFullSyncTime` is nil, which the controller interprets as "never synced" and triggers an immediate sync.
