---
title: "PackageVariant Controller"
type: docs
weight: 2
description: |
  Controller for managing PackageVariant resources.
---

## Overview

The **PackageVariant Controller** is a Kubernetes controller that manages individual package variants - maintaining a one-to-one relationship between an upstream package and a downstream package. It continuously synchronizes downstream PackageRevisions with upstream changes and applies configured mutations.

**Key characteristics:**

- **Reconciliation-based**: Uses standard Kubernetes controller-runtime reconciliation loop
- **Upstream tracking**: Monitors upstream PackageRevisions for changes via UpstreamLock comparison
- **Draft-driven workflow**: All changes create draft PackageRevisions that require approval
- **Policy-based ownership**: Configurable adoption and deletion policies for existing packages
- **Mutation application**: Applies package context, pipeline functions, and config injection
- **Finalizer-based cleanup**: Ensures proper cleanup of owned PackageRevisions on deletion

## Implementation Details

### Reconciliation State Machine

The PackageVariant controller implements a state machine that determines actions based on the current state of upstream and downstream packages:

```
PackageVariant Created
        ↓
  Validate Spec
        ↓
  Find Upstream PR
        ↓
  Downstream Exists? ──No──> Create Clone Draft
        │                           ↓
       Yes                    Apply Mutations
        ↓                           ↓
  Check UpstreamLock               Return
        ↓
  Up-to-date? ──Yes──> Check Mutations
        │                     ↓
        No              Changed? ──No──> Done
        ↓                     │
  Create Upgrade Draft        Yes
        ↓                     ↓
  Apply Mutations       Create Edit Draft
        ↓                     ↓
  Return              Apply Mutations
                            ↓
                          Return
```

**State transitions:**
- **No downstream**: Create clone draft from upstream
- **Upstream changed**: Create upgrade draft (three-way merge)
- **Mutations changed**: Create edit draft from published downstream
- **Up-to-date**: No action needed

### Upstream Tracking Mechanism

**UpstreamLock comparison logic:**

The controller determines if a downstream package is up-to-date by examining the UpstreamLock stored in the downstream PackageRevision's status. This lock contains a Git reference that points to the upstream package revision currently used.

**Comparison process:**

```
Check Downstream UpstreamLock
        ↓
  UpstreamLock exists? ──No──> Consider up-to-date
        │
       Yes
        ↓
  Git ref exists? ──No──> Consider up-to-date
        │
       Yes
        ↓
  Git ref starts with "drafts"? ──Yes──> Need update
        │
        No
        ↓
  Extract revision number from Git ref
        ↓
  Compare with desired revision
        ↓
  Match? ──Yes──> Up-to-date
        │
        No
        ↓
  Trigger upgrade draft
```

**UpstreamLock structure:**
- Stored in PackageRevision.Status.UpstreamLock
- Contains Git ref (e.g., "refs/tags/v1", "refs/tags/v2")
- Revision number extracted from ref path
- Enables automatic version tracking

**Implications:**
- Controller doesn't need to fetch upstream content to detect changes
- Git ref comparison is fast and efficient
- Supports automatic upgrades when upstream publishes new versions

### Draft Management

**Three draft types:**

1. **Clone Draft** - Initial package creation:
```
Create PackageRevision
    ↓
Spec.Tasks = [Clone]
    ↓
Clone.Upstream.UpstreamRef = upstream PR name
    ↓
WorkspaceName = "packagevariant-1"
    ↓
Lifecycle = Draft
```

2. **Upgrade Draft** - Upstream version change:
```
Create PackageRevision
    ↓
Spec.Tasks = [Upgrade]
    ↓
Upgrade.OldUpstream = old upstream PR
Upgrade.NewUpstream = new upstream PR
Upgrade.LocalPackageRevision = current downstream PR
Upgrade.Strategy = ResourceMerge
    ↓
WorkspaceName = "packagevariant-2"
    ↓
Lifecycle = Draft
```

3. **Edit Draft** - Mutation changes only:
```
Create PackageRevision
    ↓
Spec.Tasks = [Edit]
    ↓
Edit.Source = current downstream PR
    ↓
WorkspaceName = "packagevariant-3"
    ↓
Lifecycle = Draft
```

**Workspace naming:**
- Prefix: "packagevariant-"
- Incremental numbering: packagevariant-1, packagevariant-2, etc.
- Scoped per package/repository combination
- Ensures unique workspace names

**Draft workflow:**
1. Controller creates draft PackageRevision via Porch API
2. Porch creates mutable workspace
3. Controller fetches PackageRevisionResources
4. Controller applies mutations (package context, pipeline, injections)
5. Controller updates PackageRevisionResources
6. Draft remains for human/automation approval
7. Controller does NOT auto-publish (separation of concerns)

### Adoption and Deletion Policies

**Adoption Policy:**

```
AdoptionPolicy: adoptNone (default)
    ↓
Only manage self-created packages
    ↓
Check OwnerReference.UID == PackageVariant.UID
    ↓
Ignore packages without our UID

AdoptionPolicy: adoptExisting
    ↓
Take ownership of matching packages
    ↓
Find packages matching downstream repo/package
    ↓
Add our OwnerReference
    ↓
Apply our labels/annotations
```

**Deletion Policy:**

```
DeletionPolicy: delete (default)
    ↓
PackageVariant deleted
    ↓
For each owned PackageRevision:
    ↓
  Draft/Proposed? ──Yes──> Delete immediately
        │
        No (Published)
        ↓
  Set Lifecycle = DeletionProposed
        ↓
  Wait for approval

DeletionPolicy: orphan
    ↓
PackageVariant deleted
    ↓
For each owned PackageRevision:
    ↓
  Remove our OwnerReference
        ↓
  Leave package in place
```

**Policy rationale:**
- **adoptNone**: Safe default, prevents accidental takeover
- **adoptExisting**: Enables gradual migration to controller management
- **delete**: Ensures cleanup of generated packages
- **orphan**: Preserves packages when controller no longer needed

## Reconciliation Overview

The PackageVariant controller implements a continuous synchronization pattern between upstream and downstream packages. The reconciliation process follows standard Kubernetes controller patterns with validation, upstream discovery, and package management.

**Key reconciliation characteristics:**
- **Idempotent**: Can be called multiple times safely
- **Level-triggered**: Works from current state, not events
- **Status updates**: Deferred to ensure they happen even on errors
- **Requeue on errors**: Transient errors trigger automatic retry

**For detailed reconciliation flows, state machine, validation logic, and error handling, see [PackageVariant Reconciliation]({{% relref "functionality/packagevariant-reconciliation.md" %}}).**

## Mutation Application Overview

The controller applies three types of mutations to downstream packages:

**1. Package Context Injection:**
- Modifies package-context.yaml ConfigMap
- Adds/updates/removes keys based on PackageVariant spec
- Reserved keys ("name", "package-path") auto-generated

**2. KRM Function Injection:**
- Prepends functions to Kptfile pipeline
- Functions named: `PackageVariant.{pvName}.{funcName}.{position}`
- Ensures PackageVariant functions run before user functions

**3. Config Injection:**
- Injects data from in-cluster Kubernetes resources
- Enables cluster-specific configuration
- Dynamic configuration based on cluster state

**For detailed mutation application flows, ordering, and change detection, see [Mutation Application]({{% relref "functionality/mutation-application.md" %}}).**

## Status Conditions

**Condition types:**

1. **Stalled** - Whether PackageVariant is making progress:
   - Status=True: Validation error or upstream not found
   - Status=False: All validation passed, upstream found
   - Reason: "ValidationError" or "Valid"

2. **Ready** - Whether reconciliation succeeded:
   - Status=True: Successfully ensured downstream package
   - Status=False: Error during reconciliation
   - Reason: "NoErrors" or "Error"

**DownstreamTargets tracking:**
- List of downstream PackageRevisions created/adopted
- Includes Name and RenderStatus (`errorSummary`, `mutationSteps`, `validationSteps`; the former `error` and `result` fields were removed)
- Updated after each reconciliation
- Provides visibility into managed packages
