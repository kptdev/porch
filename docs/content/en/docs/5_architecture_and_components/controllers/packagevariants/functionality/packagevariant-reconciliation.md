---
title: "PackageVariant Reconciliation"
type: docs
weight: 1
description: |
  Detailed architecture of PackageVariant reconciliation flows and state management.
---

## Overview

The PackageVariant controller implements a continuous synchronization pattern between upstream and downstream packages. It monitors upstream PackageRevisions for changes, detects when downstream packages need updating, and creates appropriate drafts (clone, upgrade, or edit) to maintain synchronization while applying configured mutations.

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│          PackageVariant Reconciliation System           │
│                                                         │
│      ┌──────────────────┐     ┌──────────────────┐      │
│      │  State Machine   │     │   Upstream       │      │
│      │                  │ ──> │   Tracking       │      │
│      │  • No Downstream │     │                  │      │
│      │  • Upstream Chg  │     │  • UpstreamLock  │      │
│      │  • Mutation Chg  │     │  • Comparison    │      │
│      │  • Up-to-date    │     │  • Detection     │      │
│      └──────────────────┘     └──────────────────┘      │
│               │                         │               │
│               └───────────┬─────────────┘               │
│                           ↓                             │
│                 ┌──────────────────┐                    │
│                 │   Draft          │                    │
│                 │   Management     │                    │
│                 │                  │                    │
│                 │  • Clone         │                    │
│                 │  • Upgrade       │                    │
│                 │  • Edit          │                    │
│                 └──────────────────┘                    │
└─────────────────────────────────────────────────────────┘
```

## Reconciliation State Machine

The controller implements a state machine that determines actions based on current state:

### State Determination Flow

```
Reconcile Triggered
        ↓
  Get PackageVariant
        ↓
  List PackageRevisions
        ↓
  Validate Spec
        ↓
  Find Upstream PR
        ↓
  Get Downstream PRs
        ↓
  Downstream Exists? ──No──> State: NO_DOWNSTREAM
        │
       Yes
        ↓
  Check UpstreamLock
        ↓
  Up-to-date? ──No──> State: UPSTREAM_CHANGED
        │
       Yes
        ↓
  Calculate Draft Resources
        ↓
  Mutations Changed? ──Yes──> State: MUTATIONS_CHANGED
        │
        No
        ↓
  State: UP_TO_DATE
```

**State transitions:**
- **NO_DOWNSTREAM**: No downstream package exists → Create clone draft
- **UPSTREAM_CHANGED**: Upstream version changed → Create upgrade draft
- **MUTATIONS_CHANGED**: Mutations changed but upstream same → Create edit draft
- **UP_TO_DATE**: Everything synchronized → No action

### State Actions

**NO_DOWNSTREAM state:**
```
Create PackageRevision
        ↓
  • Tasks: [Clone{UpstreamRef}]
  • WorkspaceName: "packagevariant-1"
  • Lifecycle: Draft
  • OwnerReferences: [PackageVariant UID]
        ↓
  Fetch PackageRevisionResources
        ↓
  Apply Mutations
        ↓
  Update PackageRevisionResources
        ↓
  Return
```

**UPSTREAM_CHANGED state:**
```
Check Downstream Lifecycle
        ↓
  Published? ──No──> Error (can't upgrade draft)
        │
       Yes
        ↓
  Get Old Upstream (from UpstreamLock)
        ↓
  Get New Upstream (from spec)
        ↓
  Create PackageRevision
        ↓
  • Tasks: [Upgrade{
      OldUpstream, NewUpstream,
      LocalPackageRevision, Strategy
    }]
  • WorkspaceName: "packagevariant-N"
        ↓
  Porch Performs Three-Way Merge
        ↓
  Fetch PackageRevisionResources
        ↓
  Apply Mutations
        ↓
  Update PackageRevisionResources
        ↓
  Return
```

**MUTATIONS_CHANGED state:**
```
Check Downstream Lifecycle
        ↓
  Published? ──Yes──> Create Edit Draft
        │
        No (Draft/Proposed)
        ↓
  Update Existing Draft
        ↓
  Fetch PackageRevisionResources
        ↓
  Apply Mutations
        ↓
  Update PackageRevisionResources
        ↓
  Return
```

## Upstream Change Detection

The controller uses UpstreamLock comparison to detect when upstream packages change:

### UpstreamLock Structure

**Location and content:**
- Stored in: `PackageRevision.Status.UpstreamLock`
- Contains: Git reference pointing to upstream package revision
- Example refs: `refs/tags/v1`, `refs/tags/v2`, `refs/tags/packagevariant-3`
- Revision extraction: Parse number from ref path

### Comparison Algorithm

```
isUpToDate(pv, downstream)
        ↓
  UpstreamLock exists? ──No──> Consider up-to-date (warning logged)
        │
       Yes
        ↓
  Git ref exists? ──No──> Consider up-to-date (warning logged)
        │
       Yes
        ↓
  Git ref starts with "drafts"? ──Yes──> NOT up-to-date
        │
        No
        ↓
  Extract revision from Git ref
        ↓
  • Parse last segment after "/"
  • Convert to integer
        ↓
  Compare with pv.Spec.Upstream.Revision
        ↓
  Match? ──Yes──> Up-to-date
        │
        No
        ↓
  NOT up-to-date
```

**Comparison logic:**
1. **Missing UpstreamLock**: Assume up-to-date (log warning, avoid breaking)
2. **Missing Git ref**: Assume up-to-date (log warning, avoid breaking)
3. **Draft upstream**: Always needs update (target is always published)
4. **Revision mismatch**: Needs update (upstream version changed)
5. **Revision match**: Up-to-date (no action needed)

**Revision extraction:**
- Git ref format: `refs/tags/v{revision}` or `refs/tags/{workspace}`
- Extract last segment after final `/`
- Convert to integer using repository utility
- Compare with desired revision from PackageVariant spec

**Implications:**
- **Fast detection**: No need to fetch upstream content
- **Efficient**: Simple integer comparison
- **Automatic**: Detects new upstream versions without manual intervention
- **Graceful degradation**: Missing data doesn't break reconciliation

## Draft Creation Flows

The controller creates three types of drafts depending on the situation:

### Clone Draft Flow

**When used:**
- No downstream package exists
- Initial package creation from upstream

**Creation process:**
```
ensurePackageVariant()
        ↓
  No Existing Downstream
        ↓
  Create PackageRevision Object
        ↓
  • ObjectMeta:
    - Namespace: same as PackageVariant
    - OwnerReferences: [PackageVariant UID]
    - Labels: from PackageVariant.Spec.Labels
    - Annotations: from PackageVariant.Spec.Annotations
        ↓
  • Spec:
    - PackageName: downstream.Package
    - RepositoryName: downstream.Repo
    - WorkspaceName: newWorkspaceName()
    - Tasks: [Clone{UpstreamRef: upstream.Name}]
        ↓
  POST to Porch API
        ↓
  Porch Creates Draft Workspace
        ↓
  Porch Executes Clone Task
        ↓
  Controller Fetches PackageRevisionResources
        ↓
  Controller Applies Mutations
        ↓
  Controller Updates PackageRevisionResources
        ↓
  Draft Ready for Approval
```

**Workspace naming:**
- Prefix: `packagevariant-`
- Numbering: Incremental (1, 2, 3, ...)
- Algorithm: Scan existing PRs for same package/repo, find highest number, increment
- Scope: Per package/repository combination
- Ensures: Unique workspace names across all revisions

### Upgrade Draft Flow

**When used:**
- Downstream exists and is published
- Upstream version changed (UpstreamLock mismatch)

**Creation process:**
```
findAndUpdateExistingRevisions()
        ↓
  Downstream Not Up-to-date
        ↓
  Downstream Published? ──No──> Error
        │
       Yes
        ↓
  Extract Old Upstream Revision
        ↓
  • Get revision from UpstreamLock
  • Find published PR with that revision
        ↓
  Get New Upstream
        ↓
  • Use pv.Spec.Upstream
  • Find current upstream PR
        ↓
  Create PackageRevision Object
        ↓
  • Copy metadata from source
  • New WorkspaceName
  • Tasks: [Upgrade{
      OldUpstream: old PR name,
      NewUpstream: new PR name,
      LocalPackageRevision: current downstream name,
      Strategy: ResourceMerge
    }]
        ↓
  POST to Porch API
        ↓
  Porch Performs Three-Way Merge
        ↓
  • Base: OldUpstream
  • Theirs: NewUpstream
  • Ours: LocalPackageRevision
        ↓
  Controller Applies Mutations
        ↓
  Draft Ready for Approval
```

**Three-way merge:**
- **Base**: Old upstream version (common ancestor)
- **Theirs**: New upstream version (upstream changes)
- **Ours**: Current downstream (local changes)
- **Strategy**: ResourceMerge (field-level merge)
- **Result**: Merged package with both upstream and local changes

**Requirements:**
- Source must be published (cannot upgrade draft)
- Old upstream must be published and findable
- New upstream must exist
- All three package revisions must be accessible

### Edit Draft Flow

**When used:**
- Downstream exists and is published
- Mutations changed but upstream same
- Need to apply new mutations to published package

**Creation process:**
```
findAndUpdateExistingRevisions()
        ↓
  Calculate Draft Resources
        ↓
  Resources Changed? ──No──> Done
        │
       Yes
        ↓
  Downstream Published? ──No──> Update Existing Draft
        │
       Yes
        ↓
  Create PackageRevision Object
        ↓
  • Copy metadata from source
  • New WorkspaceName
  • Tasks: [Edit{Source: current PR name}]
        ↓
  POST to Porch API
        ↓
  Porch Copies Package
        ↓
  Controller Recalculates Mutations
        ↓
  Controller Applies New Mutations
        ↓
  Draft Ready for Approval
```

**Edit characteristics:**
- Copies published package to new draft
- Preserves upstream relationship
- Reapplies all mutations to new draft
- Used when only mutations changed (not upstream)

**Draft vs Published handling:**
- **Published source**: Create new edit draft
- **Draft/Proposed source**: Update existing draft in-place
- **Rationale**: Avoid creating multiple drafts for same package

## Adoption and Deletion

The controller manages ownership of downstream packages through policies:

### Adoption Flow

**AdoptionPolicy: adoptNone (default):**
```
Get Downstream PRs
        ↓
  For Each PR:
        ↓
    Has Our OwnerReference? ──No──> Skip
        │
       Yes
        ↓
    Process PR
```

**AdoptionPolicy: adoptExisting:**
```
Get Downstream PRs
        ↓
  For Each PR:
        ↓
    Matches Downstream Repo/Package? ──No──> Skip
        │
       Yes
        ↓
    Has Our OwnerReference? ──Yes──> Process PR
        │
        No
        ↓
    Adopt Package Revision
        ↓
    • Add our OwnerReference
    • Apply our Labels
    • Apply our Annotations
        ↓
    Update PR
        ↓
    Process PR
```

**Adoption process:**
- Check if PR matches downstream repo and package
- Add PackageVariant UID to OwnerReferences
- Merge PackageVariant labels into PR labels
- Merge PackageVariant annotations into PR annotations
- Update PR via Porch API

**Adoption rationale:**
- **adoptNone**: Safe default, prevents accidental takeover
- **adoptExisting**: Enables gradual migration to controller management
- **Use case**: Existing packages created manually, now want controller to manage

### Deletion Flow

**DeletionPolicy: delete (default):**
```
PackageVariant Deleted
        ↓
  DeletionTimestamp Set
        ↓
  For Each Owned PR:
        ↓
    Check Lifecycle
        ↓
    Draft/Proposed? ──Yes──> Delete Immediately
        │
        No (Published)
        ↓
    Set Lifecycle = DeletionProposed
        ↓
    Update PR
        ↓
    Wait for Approval
```

**DeletionPolicy: orphan:**
```
PackageVariant Deleted
        ↓
  DeletionTimestamp Set
        ↓
  For Each Owned PR:
        ↓
    Remove Our OwnerReference
        ↓
    Update PR
        ↓
    Leave Package in Place
```

**Deletion handling by lifecycle:**
- **Draft/Proposed**: Delete immediately (not yet approved)
- **Published**: Set to DeletionProposed (requires approval)
- **DeletionProposed**: No action (already proposed)

**Special case:**
- If DeletionProposed PR exists when PackageVariant deleted
- Must orphan it (remove OwnerReference)
- Otherwise it will be auto-deleted by Kubernetes garbage collection
- Allows approval process to complete

**Finalizer coordination:**
- Finalizer: `config.porch.kpt.dev/packagevariants`
- Added when PackageVariant created
- Prevents deletion until cleanup complete
- Removed after all owned PRs handled

## Error Handling

The controller handles errors at multiple stages:

### Validation Errors

```
Validate PackageVariant
        ↓
  Errors Found? ──Yes──> Set Conditions
        │                      ↓
        No                Stalled=True
        ↓                 Ready=False
  Continue                     ↓
                          Do NOT Requeue
```

**Validation failures:**
- Set Stalled condition to True with error message
- Set Ready condition to False
- Do NOT requeue (requires PackageVariant spec change)
- Error message includes all validation errors combined

**Validation checks:**
- Upstream field presence and completeness
- Downstream field presence and completeness
- AdoptionPolicy valid value
- DeletionPolicy valid value
- PackageContext reserved keys not used
- Injector names specified

### Upstream Not Found Errors

```
Find Upstream PR
        ↓
  Not Found? ──Yes──> Set Conditions
        │                   ↓
        No              Stalled=True
        ↓               Ready=False
  Continue                  ↓
                       Requeue (may appear)
```

**Upstream not found:**
- Set Stalled condition to True
- Set Ready condition to False
- Requeue (upstream may appear later)
- Watch on PackageRevisions will trigger reconciliation when upstream appears

### Reconciliation Errors

```
ensurePackageVariant()
        ↓
  Error? ──Yes──> Set Conditions
        │               ↓
        No          Ready=False
        ↓           Stalled=False
  Success               ↓
        ↓          Requeue (may be transient)
  Ready=True
  Stalled=False
```

**Reconciliation failures:**
- Set Ready condition to False with error message
- Keep Stalled condition as False (validation passed)
- Requeue (may be transient error)
- Examples: API errors, network issues, Porch unavailable

### Upgrade Constraint Errors

```
Create Upgrade Draft
        ↓
  Source Published? ──No──> Return Error
        │
       Yes
        ↓
  Old Upstream Published? ──No──> Return Error
        │
       Yes
        ↓
  Create Upgrade
```

**Upgrade constraints:**
- Source (current downstream) must be published
- Old upstream must be published and findable
- Cannot upgrade from draft (must publish first)
- Error returned to reconciliation loop
- Requeued for retry

### Condition Management

**Condition types:**

**Stalled condition:**
- **True**: Validation error or upstream not found (no progress possible)
- **False**: All validation passed, upstream found (can make progress)
- **Reasons**: "ValidationError" or "Valid"

**Ready condition:**
- **True**: Successfully ensured downstream package
- **False**: Error during reconciliation
- **Reasons**: "NoErrors" or "Error"

**Status update:**
- Deferred to end of reconciliation
- Updated even if reconciliation fails
- Provides visibility into controller state
- Used by users and automation to monitor progress

**DownstreamTargets tracking:**
- List of downstream PackageRevisions created/adopted
- Includes Name and RenderStatus (`errorSummary`, `mutationSteps`, `validationSteps`; the former `error` and `result` fields were removed)
- Updated after each reconciliation
- Preserved across reconciliations when possible
- Provides visibility into managed packages
