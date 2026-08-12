---
title: "Validation & Business Rules"
type: docs
weight: 2
description: |
  Detailed architecture of validation and business rule enforcement in the Engine.
---

## Overview

The Engine enforces validation and business rules to ensure package revisions are created and modified correctly. These rules prevent invalid operations, enforce naming constraints, and maintain referential integrity across packages and revisions.

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│            Validation & Business Rules                  │
│                                                         │
│  ┌──────────────────┐      ┌──────────────────┐         │
│  │   Pre-Operation  │      │   Constraint     │         │
│  │   Validation     │ ───> │   Enforcement    │         │
│  │                  │      │                  │         │
│  │  • Lifecycle     │      │  • Uniqueness    │         │
│  │  • Tasks         │      │  • Path Overlap  │         │
│  │  • Resources     │      │  • Clone Rules   │         │
│  └──────────────────┘      └──────────────────┘         │
│           │                         │                   │
│           └────────┬────────────────┘                   │
│                    ↓                                    │
│          ┌──────────────────┐                           │
│          │   Optimistic     │                           │
│          │    Locking       │                           │
│          │                  │                           │
│          │  • Resource Ver  │                           │
│          │  • Conflict Det  │                           │
│          └──────────────────┘                           │
└─────────────────────────────────────────────────────────┘
```

## Lifecycle Validation

The Engine validates lifecycle values during package revision creation and updates:

### Creation Lifecycle Validation

```
CreatePackageRevision
        ↓
  Check Lifecycle Value
        ↓
  Empty? ──Yes──> Default to Draft
        │
        No
        ↓
  Draft/Proposed? ──Yes──> Allow
        │
        No
        ↓
  Published/DeletionProposed? ──Yes──> Reject
        │
        No
        ↓
  Invalid Value? ──Yes──> Reject
```

**Validation rules:**
- **Empty lifecycle**: Defaults to Draft (most common case)
- **Draft allowed**: New package revisions can be created as Draft
- **Proposed allowed**: New package revisions can be created as Proposed
- **Published forbidden**: Cannot create package revisions directly in Published state
- **DeletionProposed forbidden**: Cannot create package revisions in DeletionProposed state
- **Invalid values**: Any other lifecycle value is rejected

**Error messages:**
- "cannot create a package revision with lifecycle value 'Final'" (for Published/DeletionProposed)
- "unsupported lifecycle value: {value}" (for invalid values)

**Rationale:**

Packages must progress through the Draft/Proposed states before being published. This prevents bypassing the review/approval
workflows and ensures all packages have a draft history. Additionally, it ensures that the packages have a successful render
of their kpt pipeline, meaning that failed render packages cannot progress further.

### Update Lifecycle Validation

```
UpdatePackageRevision
        ↓
  Check Current Lifecycle
        ↓
  Draft/Proposed? ──Yes──> Allow Full Update
        │
        No
        ↓
  Published/DeletionProposed? ──Yes──> Metadata Only
        │
        No
        ↓
  Invalid Value? ──Yes──> Reject
```

**Validation rules:**
- **Draft**: Full updates allowed (tasks, resources, lifecycle, metadata)
- **Proposed/Published/DeletionProposed**: Only metadata and lifecycle updates allowed
- **Invalid current lifecycle**: Operation rejected

**Error messages:**
- "invalid original lifecycle value: {value}"
- "invalid desired lifecycle value: {value}"

**Rationale:**

The work-in-progress draft packages are _mutable_, whilst the published ones are _immutable_, meaning that their content cannot be changed.
The lifecycle transitions must follow state machine rules (see [Lifecycle Management]({{% relref "/docs/5_architecture_and_components/engine/functionality/lifecycle-management.md" %}})).

## Task Validation

The Engine validates tasks during package revision creation:

### Task Count Validation

```
CreatePackageRevision
        ↓
  Check Task Count
        ↓
  Count > 1? ──Yes──> Reject
        │
        No
        ↓
  Count == 0? ──Yes──> Default to Init
        │
        No
        ↓
  Count == 1? ──Yes──> Validate Task Type
```

**Validation rules:**
- **Maximum one task**: Only one task allowed during creation
- **Default task**: If no task specified, defaults to `init` task
- **Task type validation**: Task type must be valid (init, clone, edit, upgrade)

**Error messages:**
- "task list must not contain more than one task"

**Rationale:**

Only allowing one operation simplifies the creation workflow. However, you can add multiple tasks later with updates. The default init
task provides a sensible starting point.

### Task Type Validation

| Task type | Action |
|-----------|--------|
| init | Initialize new, blank package |
| clone | Copy package from an existing, separate upstream |
| edit | Create new revision within an existing package |
| upgrade | Merge changes from a new version of the upstream |

**Task-specific validation:**

Each task type has additional validation rules and the validation is delegated to task-specific validators.
For more information, see the sections below.

## Workspace Name Uniqueness

The Engine enforces workspace name uniqueness within a package:

### Uniqueness Check

```
CreatePackageRevision
        ↓
  List Existing Revisions
        ↓
  For Each Revision:
        ↓
    Same Package? ──No──> Continue
        │
       Yes
        ↓
    Same Workspace? ──Yes──> Reject
        │
        No
        ↓
  Continue
        ↓
  Allow Creation
```

**Validation process:**
1. **List all revisions** for the same package in the repository
2. **Check workspace names** of existing revisions
3. **Reject if duplicate** workspace name found
4. **Allow if unique** workspace name

**Error message:**
- "package revision workspaceNames must be unique; package revision with name {name} in repo {repo} with workspaceName {workspace} already exists"

**Rationale:**

The workspace name is used to generate the Kubernetes object name, which must be unique within the namespace. This
prevents naming conflicts and confusion.

### Workspace Name Validation

The workspace name must be a valid RFC 1123 label name. The repository and package is combined to create the object name.

Format: `{repo}.{path}.{package}.{workspace}`

## Clone Task Validation

The Engine validates clone tasks to prevent creating invalid packages:

```
Clone Task Validation
        ↓
  List Existing Revisions
        ↓
  For Each Revision:
        ↓
    Same Package? ──Yes──> Reject
        │
        No
        ↓
  Continue
        ↓
  Resolve Repository Containing Proposed Upstream
        ↓
  Upstream is Placeholder? ──Yes──> Reject
        │
        No
        ↓
  Allow Clone
```

### Clone Constraint Check: Package Uniqueness In Repository

The package name must be unique in the repository to avoid creating duplicate packages.

**Validation process:**
1. **List all revisions** in the repository
2. **Check for existing package** with same name
3. **Reject if package exists** (clone can only create new packages)
4. **Allow if package doesn't exist**

**Error message:**
- "`clone` cannot create a new revision for package {package} that already exists in repo {repo}; make subsequent revisions using `copy`"

**Rationale:**

Cloning is for creating **new** packages from upstream, which is why existing packages should use `edit`, otherwise called `copy`, for new revisions.
This prevents accidental overwriting of existing packages.

### Clone Constraint Check: Exclude Placeholder Package Revision

The upstream package revision cannot be a placeholder package revision (identified by `Revision == -1` and `WorkspaceName` matching the Git repository's branch)

**Validation process:**
1. **Resolve repository details** for the upstream package revision
2. **Check if placeholder** (revision == -1 and WorkspaceName matches repo Git branch)
3. **Reject if placeholder** package revision
4. **Allow if not placeholder**

**Error message:**
- "upstream revision may not be the placeholder package revision {repo}/{name}"

**Rationale:**

Placeholder package revisions represent the main/branch-HEAD state. This means that they are not fixed revisions,
making them unsuitable for cloning. These operations would fail in later lifecycle stages.

### Clone vs Edit/Copy

Use **clone** when creating a new package revision from upstream source, when the package does not exist in target repository,
or when the package is brought into the repository for the first time.

Use **edit/copy** when creating a new revision of an existing package, when the package already exists in the repository,
or when you are iterating on an existing package.

## Edit Task Validation

The Engine validates edit tasks to ensure source revisions meet requirements:

```
Edit Task Validation
        ↓
  Fetch Source Revision
        ↓
  Check Same Package? ──No──> Reject
        │
       Yes
        ↓
  Check if Placeholder? ──Yes──> Reject
        │
        No
        ↓
  Check Published? ──No──> Reject
        │
       Yes
        ↓
  Allow Edit
```

### Edit Constraint Check: Source Package Revision Validation

The new package revision must be from the same package as the source package revision since we are iterating on the existing package.

**Validation process:**
1. **Fetch source revision** from specified reference
2. **Verify same package** (same repository and package name)
3. **Check if published** (only published revisions can be edited)
4. **Allow if all checks pass**

**Error messages:**
- "source revision must be from same package {repo}/{package}"
- "source revision must be published"

**Rationale:**

Edit creates new revisions from existing packages in the same repository. Placeholder package revisions represent
unstable main branch state. This means, that only published revisions provide stable source for editing.

### Edit Constraint Check: Exclude Placeholder Package Revision

The source package revision cannot be a placeholder package revision (identified by `Revision == -1` and `WorkspaceName` matching the Git repository's branch)


**Validation process:**
1. **Fetch source revision** from specified reference
2. **Resolve repository details** for the source package revision
3. **Check if placeholder** (Revision == -1 and WorkspaceName matches repo Git branch)
4. **Reject if placeholder** package revision
5. **Allow if not placeholder**

**Error message:**
- "source revision may not be the placeholder package revision {repo}/{name}"

**Rationale:**

Placeholder package revisions are not fixed revisions, so editing from unstable main/branch-HEAD state is not supported.
This prevents creating revisions from non-deterministic sources.

## Upgrade Task Validation

The Engine validates upgrade tasks to ensure the three source revisions meet requirements:

### Upgrade Source Validation

```
Upgrade Task Validation
        ↓
  Extract Source Revisions
        ↓
  • OldUpstream
  • NewUpstream
  • LocalPackageRevision
        ↓
  For Each Source:
        ↓
    Find Revision
        ↓
    Published? ──No──> Reject
        │
       Yes
        ↓
  Continue
        ↓
  Resolve Repository Containing Proposed New Upstream
        ↓
  Upstream is Placeholder? ──Yes──> Reject
        │
        No
        ↓
  Resolve Repository Containing Local Revision
        ↓
  Local Revision is Placeholder? ──Yes──> Reject
        │
        No
        ↓
  Allow Upgrade
```

**Validation process:**
1. **Extract source revision references** from upgrade task spec:
   - OldUpstream: Previous upstream version
   - NewUpstream: New upstream version to upgrade to
   - LocalPackageRevision: Current local package revision
2. **Find each source revision** in repository
3. **Check lifecycle state** of each source
4. **Reject if any source not published**
5. **Allow if all sources published**

**Error message:**
- "all source PackageRevisions of upgrade task must be published, {name} is not"

**Rationale:**

Upgrade performs a three-way merge (old upstream, new upstream, local). Source revisions must be stable (published) for reliable merge.
This prevents upgrading from unstable draft versions.

### Upgrade Source Requirements

| Required sources     | Description                              |
|----------------------|------------------------------------------|
| OldUpstream          | The package's current upstream version   |
| NewUpstream          | The upstream version to which to upgrade |
| LocalPackageRevision | The local package revision to upgrade    |

All sources must be in published lifecycle state, accessible in the repository and valid package revisions.

### Placeholder Package Revision Check

Neither the target upstream package revision (the new revision being upgraded to) nor the package revision specified for upgrade can be a placeholder package revision (identified by `Revision == -1` and `WorkspaceName` matching the repository's Git branch)

**Validation process:**
1. **Fetch target upstream revision** (NewUpstream)
2. **Resolve repository details** for the target upstream package revision
3. **Check if placeholder** (revision == -1 and workspaceName matches repo Git branch)
4. **Reject if target upstream is placeholder**
5. **Fetch local package revision** to be upgraded
6. **Resolve repository details** for the local package revision
7. **Check if placeholder** (revision == -1 and workspaceName matches repo Git branch)
8. **Reject if local revision is placeholder**
9. **Allow if neither is placeholder**

**Error messages:**
- "target upstream revision may not be the placeholder package revision {repo}/{name}"
- "the placeholder package revision {repo}/{name} may not be upgraded"

**Rationale:**

Upgrade performs a three-way merge requiring stable source revisions, however, placeholder package revisions represent unstable main branch state.
This means, that using placeholder revisions would produce non-deterministic upgrade results. This prevents upgrading to non-fixed revision states.
Validation on clone and edit operations precludes the possibility of old upstream being a placeholder.


## Package Path Overlap Validation

The Engine validates package paths to prevent nested packages:

### Path Overlap Check

```
CreatePackageRevision (init/clone)
        ↓
  Check if Package Creation
        ↓
  Yes? ──No──> Skip Check
        │
       Yes
        ↓
  List All Revisions
        ↓
  Check Path Overlaps
        ↓
  Overlapping? ──Yes──> Reject
        │
        No
        ↓
  Allow Creation
```

**Validation process:**
1. **Determine if package revision creation** (init or clone task)
2. **List all package revisions** in repository
3. **Check for path overlaps** with new package path
4. **Reject if overlap detected**
5. **Allow if no overlap**

**Path overlap rules:**

Package path cannot be the parent or a child of an existing package. This prevents ambiguous package boundaries.

**Example overlaps (rejected):**
- New: `networking/vpc`, Existing: `networking/vpc/subnets` (parent)
- New: `networking/vpc/subnets`, Existing: `networking/vpc` (child)

**Valid paths (allowed):**
- New: `networking/vpc`, Existing: `networking/firewall` (siblings)
- New: `apps/frontend`, Existing: `apps/backend` (siblings)

**Rationale:**

This prevents nested package structures and maintains clear package boundaries. Additionally, this way confusion
about package ownership are avoided.

## Optimistic Locking

The Engine uses optimistic locking to prevent concurrent modification conflicts:

### Resource Version Check

```
Update Request
        ↓
  Extract Resource Version
        ↓
  Empty? ──Yes──> Reject
        │
        No
        ↓
  Compare with Current
        ↓
  Match? ──Yes──> Proceed
        │
        No
        ↓
  Return Conflict Error
```

**Validation process:**
1. **Extract resource version** from update request
2. **Reject if empty** (resource version required)
3. **Compare with current version** in repository
4. **Proceed if match** (no concurrent modification)
5. **Return conflict if mismatch** (concurrent modification detected)

**Error message:**
- "the object has been modified; please apply your changes to the latest version and try again"

**HTTP status:**
- 409 Conflict (when resource version mismatch)

### Optimistic Locking Flow

```
Client A                      Engine                    Client B
   ↓                             ↓                          ↓
Read PR (v1)                     ↓                      Read PR (v1)
   ↓                             ↓                          ↓
Modify Locally                   ↓                   Modify Locally
   ↓                             ↓                          ↓
Update (v1)  ──────────> Check Version (v1 == v1)           ↓
   ↓                             ↓                          ↓
   ↓                        Apply Update                    ↓
   ↓                             ↓                          ↓
   ↓                        Increment to v2                 ↓
   ↓                             ↓                          ↓
Success (v2) <────────────  Return Success                  ↓
   ↓                             ↓                          ↓
   ↓                             ↓                   Update (v1) ───>
   ↓                             ↓                          ↓
   ↓                        Check Version (v1 != v2)        ↓
   ↓                             ↓                          ↓
   ↓                        Return Conflict ─────────> Conflict!
   ↓                             ↓                          ↓
   ↓                             ↓                      Re-read (v2)
   ↓                             ↓                          ↓
   ↓                             ↓                   Reapply Changes
   ↓                             ↓                          ↓
   ↓                             ↓                   Retry Update (v2)
```

**Conflict resolution:**
1. **Client receives conflict error**
2. **Client re-reads latest version**
3. **Client reapplies changes** to latest version
4. **Client retries update** with new resource version

### Resource Version Management

**Resource version characteristics:**

- Managed by Kubernetes API server
- Opaque string (typically integer)
- Incremented on each update
- Used for optimistic concurrency control

**When resource version checked:**
- UpdatePackageRevision operations
- UpdatePackageResources operations
- Any operation that modifies package revision

**When resource version NOT checked:**
- CreatePackageRevision (new object, no version)
- DeletePackageRevision (deletion doesn't need version check)
- ListPackageRevisions (read-only operation)

## Validation Timing

The Engine performs validation at specific points in the operation lifecycle:

### Pre-Operation Validation

```
API Request
     ↓
Parse Request
     ↓
Validate Inputs
     ↓
  Valid? ──No──> Return Error (400 Bad Request)
     │
    Yes
     ↓
Open Repository
     ↓
Execute Operation
```

**Validation before operation:**
- Lifecycle values
- Task count and types
- Resource version presence
- Input format and structure

**Benefits:**

The benefits of this are that it is fails fast (before expensive operations),
gives clear error messages, and has no side effects on failure.

### Mid-Operation Validation

```
Operation Started
     ↓
Open Draft
     ↓
Validate Constraints
     ↓
  Valid? ──No──> Rollback + Return Error
     │
    Yes
     ↓
Apply Changes
     ↓
Close Draft
```

**Validation during operation:**
- Workspace name uniqueness
- Clone constraints
- Upgrade source states
- Package path overlaps

**Benefits:**

The benefits of this are that it has access to the repository state,
can check against existing data and has rollback mechanism available.

### Post-Operation Validation

**Validation after operation:**
- Currently minimal post-operation validation
- Repository adapters may perform additional checks
- Cache consistency checks

## Error Handling

The Engine returns specific errors for validation failures:

### Error Types

**Bad Request (400):**
- Invalid lifecycle values
- Invalid task types
- Missing required fields
- Malformed input

**Conflict (409):**
- Resource version mismatch (optimistic locking)
- Workspace name conflicts
- Package path overlaps

**Unprocessable Entity (422):**
- Clone task on existing package
- Upgrade task with unpublished sources
- Business rule violations

### Error Messages

**Error message format:**

The type of failure is clearly described with context given (package name, repository, etc.). Suggestion for resolution when applicable is also given.

**Examples:**
- "cannot create a package revision with lifecycle value 'Final'"
- "package revision workspaceNames must be unique; package revision with name {name} in repo {repo} with workspaceName {workspace} already exists"
- "`clone` cannot create a new revision for package {package} that already exists in repo {repo}; make subsequent revisions using `copy`"
- "all source PackageRevisions of upgrade task must be published, {name} is not"

## Validation Extension Points

The Engine's validation system can be extended:

### Future Validation Rules

**Potential additions:**
- Package naming conventions
- Resource size limits
- Dependency validation
- Security policy enforcement
- Custom admission webhooks

**Extension mechanisms:**
- Validation plugins
- Webhook integration
- Policy engine integration
- Custom validators

### Validation Configuration

Currently validation rules are hardcoded in the Engine. No configuration mechanism is available.

Future possibilities include configurable validation rules, repository-specific policies, organization-wide policies and validation rule versioning.
