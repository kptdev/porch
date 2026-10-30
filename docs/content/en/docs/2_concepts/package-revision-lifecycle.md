---
title: "Package Revision Lifecycle"
type: docs
weight: 5
description: |
  Understanding package revision lifecycle stages and workflow in Porch.
---

## What is Package Revision Lifecycle?

The **lifecycle** of a package revision refers to its current stage in Porch's orchestration process. A package revision
moves through different stages from creation to publication, with approval gates between stages.

## Lifecycle Stages

### Draft

A package revision in **Draft** stage is work in progress. It is being actively edited and not ready for deployment. If
it is being cached by the [CR cache]({{% relref "../5_architecture_and_components/package-cache/cr-cache.md" %}}),
its contents are stored in a temporary branch in the Git repository (e.g., `drafts/package-name/workspace`). If it is
being cached by the [DB cache]({{% relref "../5_architecture_and_components/package-cache/db-cache.md" %}}),
its contents are only stored in the database and do not appear in Git at all.

Users have complete freedom in the orchestration operations they can perform on a Draft package revision. They can modify
the package contents, update the `Kptfile`, and add or remove KRM functions in the `Kptfile`'s pipeline. They can modify
the package revision as many times as they wish, and even delete it at the cost of losing all their changes up to that point.

### Proposed

A package revision in **Proposed** stage is marked as being ready for review. It is the usual hop between Draft
and Published, allowing it to be reviewed before finalization, approval for deployment, and publication. Proposed is
optional only when direct publication from Draft is enabled. A package revision
in Proposed stage can be pulled for review, but cannot be pushed - to edit its package contents again, it must first be
rejected back to Draft stage. However, as with Draft stage, a package revision in Proposed stage can be deleted.

If the package revision is being cached by the CR cache, it is still stored in a temporary branch in Git  (e.g.,
`proposed/package-name/workspace`). If it is being cached by the DB cache, it is only persisted to the database.

### Published

A package revision in Published stage is finalized and reviewed. Porch has assigned it a revision number (> `0`) and committed
it to the deployment branch of its Git repository (usually `main`). A Published package revision is ready for deployment
and can be picked up and deployed by a GitOps framework such as Flux. A Published package revision is immutable - to continue
developing its contents, it can be either copied to create another package revision of the same package, or cloned into
the first package revision of an entirely new package. It cannot be directly deleted, but must first be moved to DeletionProposed
stage.

Pre-existing package revisions discovered through [package revision discovery]({{% relref "package-revisions#package-revision-discovery" %}})
are also permanently in Published stage.

### DeletionProposed

A package revision in DeletionProposed stage is marked for deletion, and is awaiting approval for deletion to be carried
out. It still exists in its Git repository until the deletion is approved by permanently deleting the package revision.
If it is being cached by the CR cache, its contents are also duplicated in a temporary branch in the Git repository
(e.g., `deletionProposed/package-name/workspace`).

The DeletionProposed stage exists to ensure a package revision in Published stage cannot be deleted unless the deletion is
approved. This restriction makes sense because, for example, if a package revision is deployed using a GitOps framework,
we do not want the package revision to disappear without being absolutely sure it should be deleted. Similarly, if a package
revision is an upstream package revision of other package revisions, we do not want to delete the parent of those package
revisions, as that would mean the child package revisions could never be upgraded.

## Lifecycle Workflow

The diagram below shows the typical flow of a package revision through its lifecycle stages:

![Package Lifecycle Workflow](/static/images/porch/lifecycle-flowchart.drawio.svg)

**Normal workflow:**
1. **Draft** → User creates or edits a package revision
2. **Proposed** → User proposes changes for review
3. **Review** → Team reviews the changes
4. **Published** → Changes are approved and committed

This is the default path. Draft → Published (skipping Proposed) is off unless `--allow-direct-publication-from-draft` is enabled on porch-server and `--packagerevisions.allow-direct-publication-from-draft` on porch-controllers.

**Rejection workflow:**
- If changes are rejected during review, the package revision returns to Draft stage
- User makes required modifications
- Process repeats until approval

**Deletion workflow:**
- Published package revision → DeletionProposed
- Review → If approved, package revision is deleted
- If rejected, the package revision returns to Published stage

## State Transitions

Valid state transitions:
- Draft → Proposed
- Proposed → Published (on approval)
- Proposed → Draft (on rejection)
- Published → DeletionProposed
- DeletionProposed → Deleted (on approval)
- DeletionProposed → Published (on rejection)
- Draft → Published, only when `--allow-direct-publication-from-draft` is enabled on **porch-server** and `--packagerevisions.allow-direct-publication-from-draft` is enabled on **porch-controllers** (both default to `false`; both must match)

Invalid transitions:
- Draft → Published when those flags are off (the default; propose first)
- Published → Draft (must create new Draft)

## Key Points

- Lifecycle stages control when a package revision can be modified or deployed
- Proposed and DeletionProposed are approval gates; Proposed can be skipped only when both allow-direct-publication-from-draft flags are true
- Only Draft package revisions can be modified
- Published package revisions are immutable and deployment-ready
- The default workflow helps ensure changes are reviewed and approved before being finalized
