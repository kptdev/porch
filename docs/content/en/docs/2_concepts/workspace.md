---
title: "Workspace"
type: docs
weight: 4
description: |
  Understanding workspaces: how Porch isolates concurrent work on packages.
---

{{% alert title="Warning" color="warning" %}}

**Workspace is merely a unique ID for a package revision!**

The content of this page will shortly be updated to reflect this fact and moved into the PackageRevision page!

{{% /alert %}}

## What is a Workspace?

A **workspace** is a named, isolated environment for working on a package revision in Porch. Each Draft or Proposed
package revision has a unique workspace name that identifies the specific set of changes being made to a package.

Workspaces enable multiple users to work on different changes to the same package simultaneously, isolate work-in-progress changes from published package revisions, provide clear identification of what changes a package revision contains, and support tracking of package revision history and lineage.

## How Workspaces Work

When you create a new Draft package revision, you specify a workspace name. That workspace name **must be unique** within a package (no two package revisions of the same package can share it), **identifies the changes** contained in that package revision, **maps to Git branches** (for Git repositories) in the format `drafts/<package-path>/<workspace-name>`, and **persists through lifecycle** transitions (Draft → Proposed → Published, or Draft → Published when direct publication is enabled).

## Workspaces and Lifecycle

Workspaces persist throughout a package revision's lifecycle stages (Draft, Proposed, and Published). The workspace name
remains constant, providing continuity as the package revision moves through the stages. For details on lifecycle stages
and transitions, see [Package Lifecycle]({{% relref "/docs/2_concepts/package-revision-lifecycle" %}}).

## Workspace Names

Workspace names should be:
- **Descriptive**: Indicate what changes the package revision contains (e.g., `add-monitoring`, `update-v2`, `fix-rbac`)
- **Unique**: No other package revision of the same package can use the same workspace name
- **Valid**: Follow [Kubernetes naming conventions](https://kubernetes.io/docs/concepts/overview/working-with-objects/names#dns-label-names)
  (lowercase alphanumeric, hyphens allowed)

Common patterns:
- Feature-based: `add-feature-x`, `enable-tls`
- Version-based: `v1`, `v2`, `v3` (often auto-generated)
  {{% alert title="Not a revision number!" color="warning" %}}

  If a version-based workspace name is used, **do not confuse it** with [revision numbers](#workspace-and-revision-numbers)!

  {{% /alert %}}
- User-based: `alice-changes`, `bob-update`
- Purpose-based: `security-patch`, `config-update`

## Workspaces and Git

When Porch is installed with the [CR cache]({{% relref "../5_architecture_and_components/package-cache/cr-cache" %}}),
workspace names form part of the branches in Git:

**Draft package revisions**: Stored in branches named `drafts/<package-path>/<workspace-name>`
- Example: `drafts/my-app/add-monitoring`

**Proposed package revisions**: Stored in branches named `proposed/<package-path>/<workspace-name>`
- Example: `proposed/my-app/add-monitoring`

**Published package revisions**: Committed to the `main` branch and tagged
- The workspace name is preserved in the PackageRevision API resource's metadata and he Git branch is deleted after publication.

This Git mapping provides native Git workflow integration, branch-based isolation, standard Git operations (diff, merge, rebase) on package changes, and an audit trail through Git history.

## Multiple Workspaces on the Same Package

Multiple users can work on the same package simultaneously by using different workspaces to create multiple Draft package
revisions:

```
Package: my-app

Workspace: add-monitoring (Draft)
  - User Alice adding Prometheus configs

Workspace: update-dependencies (Draft)
  - User Bob updating container images

Workspace: v3 (Published)
  - Current production version
```

Each workspace is isolated. Changes in one workspace do not affect package contents until published.

## Workspace and Revision Numbers

Workspaces and revision numbers serve different purposes:

**Workspace name**: Identifies work-in-progress changes (Draft/Proposed)
- Temporary identifier
- User-defined
- Describes the changes

**Revision number**: Identifies published versions
- Permanent identifier
- Auto-assigned by Porch (`1`, `2`, `3`, ...)
- Indicates version sequence

When a package revision is published, it receives a revision number, but the workspace name is preserved for historical
tracking.

## Key Points

- Workspace = arbitrary ID to make a package revision completely unique
- Each Draft/Proposed package revision has a unique workspace name
- Workspace names must be unique within a package
- Workspaces map to Git branches (drafts/ or proposed/ prefixes)
- Multiple workspaces enable concurrent work on the same package
- Workspace names are preserved after publication for tracking
- Workspace names should be descriptive and follow naming conventions
- Workspace names must not be confused with revision numbers
