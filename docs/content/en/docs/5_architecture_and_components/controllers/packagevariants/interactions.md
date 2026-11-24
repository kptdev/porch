---
title: "Interactions with Porch APIs"
type: docs
weight: 4
description: |
  How the Porch controllers interact with Porch APIs.
---

## Overview

The Controllers are **clients** of the Porch API, not part of the Porch server. They run as a separate microservice, interacting
with Porch through standard Kubernetes client-go mechanisms. The controllers watch Porch API resources (PackageRevisions,
Repositories) and create/update/upgrade PackageRevisions (PackageVariant controller) and PackageVariants (PackageVariantSet
controller) through the Porch API to automate creation and management of package revisions on two levels of templating.
PackageVariants allow multiple downstream package revisions, each with its own defined customisations, to be spun off a
single upstream package revision; PackageVariantSets enable the same behaviour for PackageVariants themselves.

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│              Controller Manager                         │
│                                                         │
│     ┌──────────────────┐      ┌──────────────────┐      │
│     │ PackageVariant   │      │PackageVariantSet │      │
│     │   Controller     │      │   Controller     │      │
│     │                  │      │                  │      │
│     │  • Watch PRs     │      │  • Watch PRs     │      │
│     │  • Watch PVs     │      │  • Watch PVs     │      │
│     │  • Create PRs    │      │  • Watch PVSs    │      │
│     │  • Update Status │      │  • Create PVs    │      │
│     └──────────────────┘      │  • Update Status │      │
│              │                └──────────────────┘      │
│              │                         │                │
│              └───────────┬─────────────┘                │
│                          ↓                              │
│                 ┌──────────────────┐                    │
│                 │  Kubernetes      │                    │
│                 │  Client-Go       │                    │
│                 │                  │                    │
│                 │  • Informers     │                    │
│                 │  • Watches       │                    │
│                 │  • CRUD Ops      │                    │
│                 └──────────────────┘                    │
└─────────────────────────────────────────────────────────┘
                           ↓
                   Porch API Server
                           ↓
                   PackageRevisions
```

**Key architectural characteristics:**

1. **Separate Deployment**: Controllers run independently from Porch server and can be enabled/disabled using [the `--reconcilers` flag]({{% relref "../../../6_configuration_and_deployments/configurations/components/porch-controllers-config.md#command-line-arguments" %}}) at deployment time

2. **Standard Kubernetes Patterns**: Uses controller-runtime framework with standard watch/reconcile patterns

3. **Client-Only Access**: Controllers are pure API clients - no direct access to Porch internals, cache, or repository adapters

4. **Namespace-Scoped**: All watches and operations scoped to same namespace as controller resources

5. **Leader Election**: Currently disabled but supported for future high-availability deployments

## Watching Porch APIs

The controllers use Kubernetes watch mechanisms to detect changes in Porch resources and trigger reconciliation.

### Watch Configuration

**PackageVariant Controller watches:**

```
       Controller Manager
              ↓
        Set up Watches
              ↓
        ┌─────┴──────────┬──────────────────────┐
        ↓                ↓                      ↓
     Primary         Secondary              Secondary
      Watch            Watch                  Watch
        ↓                ↓                      ↓
  PackageVariant  PackageRevision  (future: injected resources)
```

**Watch setup:**
- **Primary watch**: PackageVariant CRs (main resource being reconciled)
- **Secondary watch**: PackageRevision CRs (triggers reconciliation when upstream changes)
- **Future watches**: Resources injected into PackageRevisions (for dynamic updates)

**PackageVariantSet Controller watches:**

```
      Controller Manager
              ↓
        Set up Watches
              ↓
        ┌─────┴──────────┬────────────────┐
        ↓                ↓                ↓
     Primary         Secondary        Secondary
      Watch            Watch            Watch
        ↓                ↓                ↓
PackageVariantSet  PackageVariant  PackageRevision
```

**Watch setup:**
- **Primary watch**: PackageVariantSet CRs (main resource being reconciled)
- **Secondary watch**: PackageVariant CRs (triggers reconciliation when child changes)
- **Secondary watch**: PackageRevision CRs (triggers reconciliation when upstream changes)

### Secondary Watch Mapping

**PackageVariant secondary watch:**

When a PackageRevision changes, the controller maps it to all PackageVariants in the namespace:

```
PackageRevision Change
        ↓
  Map Function
        ↓
  List All PackageVariants
        ↓
  In Same Namespace
        ↓
  Enqueue All
        ↓
  Each PV Reconciled
```

**Mapping rationale:**
- Simple implementation (no complex filtering)
- Ensures all PackageVariants see upstream changes
- Acceptable performance for typical scale (hundreds of resources)
- Trade-off: More reconciliations, simpler logic

**PackageVariantSet secondary watches:**

When a PackageVariant or PackageRevision changes, the controller maps it to all PackageVariantSets in the namespace:

```
PackageVariant/PackageRevision Change
        ↓
  Map Function
        ↓
  List All PackageVariantSets
        ↓
  In Same Namespace
        ↓
  Enqueue All
        ↓
  Each PVS Reconciled
```

**Mapping rationale:**
- Ensures PackageVariantSets see child PackageVariant changes
- Detects new upstream PackageRevisions for target expansion
- Broad watches ensure consistency
- May trigger unnecessary reconciliations but guarantees correctness

### Watch Event Types

**Event types processed:**
- **Added**: New resource created
- **Modified**: Existing resource updated
- **Deleted**: Resource removed (handled via finalizers)

**Event handling:**
- All events trigger reconciliation
- Reconciliation is idempotent (safe to call multiple times)
- Level-triggered (works from current state, not events)
- No event ordering guarantees needed

## PackageRevision Creation

The controllers create PackageRevisions through the Porch API to instantiate downstream packages.

### Creation Flow

**PackageVariant controller:**

```
Reconcile Loop
        ↓
  No Downstream Exists
        ↓
  Create PackageRevision
        ↓
  • ObjectMeta:
    - OwnerReferences: [PackageVariant UID]
    - Labels: from PackageVariant spec
    - Annotations: from PackageVariant spec
  • Spec:
    - PackageName: downstream package
    - RepositoryName: downstream repo
    - WorkspaceName: "packagevariant-N"
    - Tasks: [Clone{UpstreamRef}]
        ↓
  POST to Porch API
        ↓
  Porch Creates Draft
        ↓
  Return PackageRevision
        ↓
  Apply Mutations
```

**Creation parameters:**
- **OwnerReferences**: Establishes ownership for garbage collection
- **Labels/Annotations**: Propagated from PackageVariant spec
- **WorkspaceName**: Auto-generated with incremental numbering
- **Tasks**: Clone task references upstream PackageRevision by name

**PackageVariantSet controller:**

```
Reconcile Loop
        ↓
  Unroll Targets
        ↓
  For Each Target
        ↓
  Render PackageVariant Spec
        ↓
  Create PackageVariant
        ↓
  • ObjectMeta:
    - OwnerReferences: [PackageVariantSet UID]
    - Labels: {packagevariantset: PVS UID}
    - Finalizers: [config.porch.kpt.dev/packagevariants]
  • Spec: rendered from template
        ↓
  POST to Kubernetes API
        ↓
  PackageVariant Created
        ↓
  PackageVariant Controller
        ↓
  Creates PackageRevision
```

**Creation hierarchy:**
- PackageVariantSet creates PackageVariant CRs
- PackageVariant controller watches and creates PackageRevisions
- Two-level ownership chain: PVS → PV → PR

### Draft Management

The controller creates three types of drafts depending on the situation:

**Draft types:**
- **Clone Draft**: Initial package creation from upstream
- **Upgrade Draft**: Upstream version change (three-way merge)
- **Edit Draft**: Mutation changes only (copy and reapply)

**Draft workflow:**
- Controller creates PackageRevision with Draft lifecycle via Porch API
- Porch creates mutable workspace
- Controller applies mutations through PackageRevisionResources API
- Draft remains for human/automation approval
- Controller does NOT auto-publish (separation of concerns)

**For detailed draft creation flows and characteristics, see [PackageVariant Controller - Draft Management]({{% relref "packagevariant.md#draft-management" %}}).**

### Resource Mutations

**PackageRevisionResources updates:**

```
Fetch PackageRevisionResources
        ↓
  GET /apis/porch.kpt.dev/v1alpha1/
      namespaces/{ns}/
      packagerevisionresources/{name}
        ↓
  Modify Resources:
        ↓
  • Update package-context.yaml
  • Prepend functions to Kptfile
  • Inject configuration
        ↓
  Update PackageRevisionResources
        ↓
  PUT /apis/porch.kpt.dev/v1alpha1/
      namespaces/{ns}/
      packagerevisionresources/{name}
        ↓
  Porch Executes Render
        ↓
  Return RenderStatus
```

**Mutation operations:**
- **Package context**: Modify ConfigMap data field
- **KRM functions**: Prepend to Kptfile pipeline with naming pattern
- **Config injection**: Inject data from in-cluster resources

**Render execution:**
- Porch automatically executes render after resource update
- Runs function pipeline from Kptfile
- Returns RenderStatus with function results (`errorSummary`, `mutationSteps`, `validationSteps`)
- **Render failure handling**:
  - Default: Errors prevent draft closure (no resources persisted)
  - With `porch.kpt.dev/push-on-render-failure: "true"`: Draft closed even on failure (resources persisted)
  - Error always returned regardless of persistence

### Deletion Handling

**PackageRevision deletion:**

```
PackageVariant Deleted
        ↓
  Finalizer Cleanup
        ↓
  For Each Owned PR:
        ↓
  Check DeletionPolicy
        ↓
  ┌────┴────┬─────────┐
  ↓         ↓         ↓
Delete   Orphan   Published?
  ↓         ↓         ↓
DELETE   Remove   Set Lifecycle=
         Owner    DeletionProposed
         Ref
```

**Deletion policies:**
- **delete** (default): Remove owned PackageRevisions
- **orphan**: Remove owner reference, leave PackageRevision

**Published package handling:**
- Cannot delete Published packages directly
- Set Lifecycle to DeletionProposed
- Requires approval before actual deletion
- Maintains audit trail

## Status Updates

The controllers update status conditions to reflect reconciliation state.

### Status Updates

The controllers update status conditions to reflect reconciliation state:

**Condition types:**
- **Stalled**: Whether controller is making progress (validation passed, upstream found)
- **Ready**: Whether reconciliation succeeded

**Status update characteristics:**
- **Deferred**: Status updated at end of reconciliation (even on errors)
- **Subresource**: Uses status subresource for optimistic locking
- **Idempotent**: Safe to update multiple times
- **Conflict handling**: Retries on conflict with exponential backoff

**For detailed condition meanings and update flows, see:**
- [PackageVariant Controller - Condition Management]({{% relref "packagevariant.md#status-conditions" %}})
- [PackageVariantSet Controller - Condition Management]({{% relref "packagevariantset.md#status-conditions" %}})

## API Client Configuration

The controllers use standard Kubernetes client-go configuration.

### RBAC Permissions

**PackageVariant controller:**
- **PackageVariants**: get, list, watch, create, update, patch, delete
- **PackageVariants/status**: get, update, patch
- **PackageVariants/finalizers**: update
- **PackageRevisions**: create, delete, get, list, patch, update, watch
- **PackageRevisionResources**: create, delete, get, list, patch, update, watch
- **Repositories**: get, list, watch

**PackageVariantSet controller:**
- **PackageVariantSets**: get, list, watch, create, update, patch, delete
- **PackageVariantSets/status**: get, update, patch
- **PackageVariantSets/finalizers**: update
- **PackageVariants**: create, delete, get, list, patch, update, watch
- **All resources**: list (for ObjectSelector)
- **Repositories**: get, list, watch

**Common permissions:**
- **Leases**: get, list, watch, create, update, patch, delete (leader election)
- **Events**: create, patch (event recording)

### Error Handling

**API errors:**

```
API Operation
        ↓
  Error? ──No──> Continue
        │
       Yes
        ↓
  Check Error Type
        ↓
  ┌────┴────┬─────────┬─────────┐
  ↓         ↓         ↓         ↓
NotFound Conflict Transient Permanent
  ↓         ↓         ↓         ↓
Ignore   Retry    Requeue   Set Condition
         (auto)   (auto)    + Return
```

**Error handling strategies:**
- **NotFound**: Ignored (resource may have been deleted)
- **Conflict**: Automatic retry with exponential backoff
- **Transient**: Requeue with backoff (network errors, etc.)
- **Permanent**: Set error condition, don't requeue (validation errors)

**Retry behavior:**
- Controller-runtime handles automatic retry
- Exponential backoff for transient errors
- Maximum retry attempts configurable
- Failed reconciliations logged for debugging

## Integration Patterns

The controllers follow standard Kubernetes controller patterns.

### Owner References

**Ownership chain:**

```
PackageVariantSet
        ↓
  OwnerReference
        ↓
PackageVariant
        ↓
  OwnerReference
        ↓
PackageRevision
```

**Owner reference benefits:**
- **Garbage collection**: Kubernetes automatically deletes children when parent deleted
- **Cascading deletion**: Entire hierarchy cleaned up automatically
- **Ownership tracking**: Clear parent-child relationships
- **Finalizer coordination**: Ensures proper cleanup order

### Finalizers

**Finalizer usage:**

```
Resource Deletion
        ↓
  DeletionTimestamp Set
        ↓
  Finalizer Present? ──No──> Delete Immediately
        │
       Yes
        ↓
  Reconcile Cleanup
        ↓
  • Delete/Orphan Children
  • Wait for Approval
  • Clean Up Resources
        ↓
  Remove Finalizer
        ↓
  Update Resource
        ↓
  Kubernetes Deletes
```

**Finalizer purpose:**
- **PackageVariant**: Ensures proper deletion/orphaning of PackageRevisions
- **PackageVariantSet**: Ensures proper cleanup of PackageVariants
- **Coordination**: Prevents premature deletion before cleanup complete

### Label Selectors

**Label usage:**

```
PackageVariantSet Creates PackageVariant
        ↓
  Set Label:
    config.porch.kpt.dev/packagevariantset: {PVS UID}
        ↓
  List PackageVariants
        ↓
  Filter by Label
        ↓
  Only Owned PVs Returned
```

**Label benefits:**
- **Efficient querying**: Filter at API server level
- **Ownership tracking**: Identify owned resources
- **Cleanup**: Find all children for deletion
- **Reconciliation**: Only process owned resources
