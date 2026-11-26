---
title: "Deleting Package Revisions"
type: docs
weight: 7
description: "A step by step guide to deleting package revisions in Porch"
---

## Tutorial Overview

You will learn how to:

1. Delete Draft and Proposed PackageRevisions directly
2. Propose Published PackageRevisions for deletion
3. Approve or reject deletion proposals
4. Understand the deletion workflow and safety mechanisms

{{% alert title="Note" color="primary" %}}
This tutorial assumes a porch repository is initialized with the "porch-test" name.
Replace any "porch-test" value with your repository's name in the commands below.
{{% /alert %}}

---

## Understanding PackageRevision Deletion

PackageRevision deletion in Porch follows different workflows depending on the lifecycle state:

**Direct Deletion:**

- **Draft** and **Proposed** PackageRevisions can be deleted immediately
- No approval process required
- Permanently removes the PackageRevision and its Git branch

**Deletion Proposal Workflow:**

- **Published** PackageRevisions require a two-step deletion process
- First propose deletion, then approve the proposal
- Provides safety mechanism to prevent accidental deletion of production packages

**Branch-Tracking PackageRevisions:**

- When you publish a PackageRevision, Porch automatically creates a "main" branch-tracking PackageRevision
- These have revision `-1` and workspace name `main`
- They track the current state of the package on the main Git branch
- **Important**: These are managed automatically by Porch and should not be directly modified
- The only user interaction should be deletion after all regular PackageRevisions of **that specific package** are deleted

---

## Step 1: Create Test PackageRevisions

Let's create some test PackageRevisions to demonstrate the deletion workflows:

```bash
# Create a Draft PackageRevision
porchctl rpkg init test-draft-package \
  --namespace=default \
  --repository=porch-test \
  --workspace=draft-v1 \
  --description="Test package for deletion"

# Create a Proposed PackageRevision
porchctl rpkg init test-proposed-package \
  --namespace=default \
  --repository=porch-test \
  --workspace=proposed-v1 \
  --description="Test package for deletion"

porchctl rpkg propose porch-test.test-proposed-package.proposed-v1 --namespace=default

# Create a Published PackageRevision
porchctl rpkg init test-published-package \
  --namespace=default \
  --repository=porch-test \
  --workspace=published-v1 \
  --description="Test package for deletion"

porchctl rpkg propose porch-test.test-published-package.published-v1 --namespace=default
porchctl rpkg approve porch-test.test-published-package.published-v1 --namespace=default
```

Verify the PackageRevisions were created:

```bash
porchctl rpkg get --namespace=default
```

You should see an output similar to:

```bash
NAME                                             PACKAGE                  WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.test-draft-package.draft-v1           test-draft-package       draft-v1        0          false    Draft       porch-test
porch-test.test-proposed-package.proposed-v1     test-proposed-package    proposed-v1     0          false    Proposed    porch-test
porch-test.test-published-package.main           test-published-package   main            -1         false    Published   porch-test
porch-test.test-published-package.published-v1   test-published-package   published-v1    1          true     Published   porch-test
```

---

## Step 2: Delete Draft PackageRevisions

Draft PackageRevisions can be deleted immediately without any approval process:

```bash
porchctl rpkg del porch-test.test-draft-package.draft-v1 --namespace=default
```

This command immediately removes the Draft PackageRevision and deletes the corresponding Git branch (`draft/draft-v1`). No approval or confirmation is required.

Verify the deletion:

```bash
porchctl rpkg get --namespace=default
```

The Draft PackageRevision should no longer appear in the list:

```bash
NAME                                             PACKAGE                  WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.test-proposed-package.proposed-v1     test-proposed-package    proposed-v1     0          false    Proposed    porch-test
porch-test.test-published-package.main           test-published-package   main            -1         false    Published   porch-test
porch-test.test-published-package.published-v1   test-published-package   published-v1    1          true     Published   porch-test
```

---

## Step 3: Delete Proposed PackageRevisions

Proposed PackageRevisions can also be deleted directly:

```bash
porchctl rpkg del porch-test.test-proposed-package.proposed-v1 --namespace=default
```

This command immediately removes the Proposed PackageRevision and deletes the corresponding Git branch (`proposed/proposed-v1`). No approval process is required.

Verify the deletion:

```bash
porchctl rpkg get --namespace=default
```

The Proposed PackageRevision should no longer appear in the list:

```bash
NAME                                             PACKAGE                  WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.test-published-package.main           test-published-package   main            -1         false    Published   porch-test
porch-test.test-published-package.published-v1   test-published-package   published-v1    1          true     Published   porch-test
```

---

## Step 4: Propose Published PackageRevision for Deletion

Published PackageRevisions cannot be deleted directly. You must first propose them for deletion:

```bash
porchctl rpkg propose-delete porch-test.test-published-package.published-v1 --namespace=default
```

This command changes the PackageRevision lifecycle from `Published` to `DeletionProposed` and signals that its deletion is up for review.
Requires approval before actual deletion occurs.

Verify the state change:

```bash
porchctl rpkg get porch-test.test-published-package.published-v1 --namespace=default
```

The lifecycle should now show `DeletionProposed`:

```bash
NAME                                             PACKAGE                  WORKSPACENAME   REVISION   LATEST   LIFECYCLE         REPOSITORY
porch-test.test-published-package.main           test-published-package   main            -1         false    Published         porch-test
porch-test.test-published-package.published-v1   test-published-package   published-v1    1          true     DeletionProposed  porch-test
```

---

## Step 5a: Approve Deletion Proposal

If you want to proceed with the deletion, approve the deletion proposal:

```bash
porchctl rpkg del porch-test.test-published-package.published-v1 --namespace=default
```

{{% alert title="Warning" color="Warning" %}}
This command permanently deletes the PackageRevision, removing the Git tag and any associated branches. This cannot be undone once completed.
{{% /alert %}}

Verify the deletion:

```bash
porchctl rpkg get --namespace=default
```

The published PackageRevision should no longer exist, but the main branch-tracking PackageRevision remains:

```bash
NAME                                     PACKAGE                  WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.test-published-package.main   test-published-package   main            -1         false    Published   porch-test
```

---

## Step 5b: Reject Deletion Proposal (Alternative)

If you decide not to delete the PackageRevision, you can reject the deletion proposal:

```bash
# First, let's create another Published PackageRevision for this example
porchctl rpkg init test-reject-delete \
  --namespace=default \
  --repository=porch-test \
  --workspace=reject-v1 \
  --description="Test package for rejection"

porchctl rpkg propose porch-test.test-reject-delete.reject-v1 --namespace=default
porchctl rpkg approve porch-test.test-reject-delete.reject-v1 --namespace=default

# Propose it for deletion
porchctl rpkg propose-delete porch-test.test-reject-delete.reject-v1 --namespace=default

# Now reject the deletion proposal
porchctl rpkg reject porch-test.test-reject-delete.reject-v1 --namespace=default
```

These commands change lifecycle from `DeletionProposed` back to `Published`, which means that the PackageRevision returns to normal published state. The revision can be used again normally.

Verify the state change:

```bash
porchctl rpkg get porch-test.test-reject-delete.reject-v1 --namespace=default
```

The lifecycle should be back to `Published`:

```bash
NAME                                      PACKAGE            WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.test-reject-delete.reject-v1   test-reject-delete reject-v1       1          true     Published   porch-test
```

---

## Batch Deletion Operations

You can delete multiple PackageRevisions with a single command:

**Delete multiple Draft/Proposed PackageRevisions:**

```bash
porchctl rpkg del package1 package2 package3 --namespace=default
```

**Propose multiple Published PackageRevisions for deletion:**

```bash
porchctl rpkg propose-delete package1 package2 package3 --namespace=default
```

**Approve multiple deletion proposals:**

```bash
porchctl rpkg del package1 package2 package3 --namespace=default
```

---

## Deletion Workflow Summary

The complete deletion workflow depends on the PackageRevision lifecycle state:

```mermaid
graph TD
    A[PackageRevision] --> B{Lifecycle State?}
    B -->|Draft| C[porchctl rpkg del]
    B -->|Proposed| C
    B -->|Published| D[porchctl rpkg propose-delete]
    C --> E[Immediately Deleted]
    D --> F[DeletionProposed State]
    F --> G{Decision?}
    G -->|Approve| H[porchctl rpkg del]
    G -->|Reject| I[porchctl rpkg reject]
    H --> E
    I --> J[Back to Published]
```

---

## Safety Considerations

**Published PackageRevision Protection**

The two-step deletion process prevents accidental removal: deletion proposals can be reviewed before approval, and if a proposal is rejected the PackageRevision returns to the **Published** state.

**Git Repository Impact**

Draft/Proposed deletions remove Git branches as well, and published deletions remove Git tags and references. Deletion is permanent and cannot be undone.

**Upstream Reference Considerations**

Upstream PackageRevisions cannot be proposed for deletion or deleted while downstream PackageRevisions use them. Delete the downstream PackageRevisions first, then delete the upstream. The error message names the blocking downstream PackageRevisions. See [Deletion Protection]({{% relref "upstream-downstream#deletion-protection" %}}) for what counts as downstream. Also consider the impact on deployed workloads.

---

## Troubleshooting

**Cannot delete Published PackageRevision directly:**

```bash
Error: cannot delete published package revision directly, use propose-delete first
```

- Use `porchctl rpkg propose-delete` first, then `porchctl rpkg del`

**PackageRevision not found:**

- Verify the exact PackageRevision name with the `porchctl rpkg get --namespace=default` command
- Check that you are using the correct namespace
- Ensure the PackageRevision has not already been deleted

**Permission denied:**

- Check RBAC permissions with the `kubectl auth can-i delete packagerevisions -n default` command
- Verify your service account has proper deletion roles

**Cannot delete upstream PackageRevision:**

```bash
Error: errors:
  packagerevisions.porch.kpt.dev "blueprints.vendor.v1" is forbidden: PackageRevision referenced by 2 downstream package(s): default/deployments.app.v1, default/deployments.site.v1 (subpackage network). To release it, delete the dependents, or remove or upgrade the subpackage in a Draft dependent, then retry
```

- `porchctl rpkg propose-delete` and `porchctl rpkg del` both return this error
- At most 20 downstream PackageRevisions are listed. "more than 20" means there are more.
- See "Upstream Reference Considerations" in Safety Considerations above
- Delete the listed downstream PackageRevisions, then retry

**Deletion proposal stuck:**

- Check the PackageRevision status with the `porchctl rpkg get <name> -o yaml` command
- Look for conditions that might prevent deletion
- Ensure no other processes are modifying the PackageRevision

---

## Complete Cleanup

After deleting PackageRevisions, you may notice "main" branch-tracking PackageRevisions still exist. These are automatically created by Porch when packages are published and must be deleted separately.

{{% alert title="Warning" color="warning" %}}
Main branch-tracking PackageRevisions (with workspace name "main" and revision "-1") are managed automatically by Porch. Do not modify, propose, approve, or otherwise interact with them except for deletion after all regular PackageRevisions of that specific package have been removed.
{{% /alert %}}

Check for remaining PackageRevisions:

```bash
porchctl rpkg get --namespace=default
```

You might see an output like:

```bash
NAME                                     PACKAGE                  WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.test-published-package.main   test-published-package   main            -1         false    Published   porch-test
porch-test.test-reject-delete.main       test-reject-delete       main            -1         false    Published   porch-test
```

Delete the main branch-tracking PackageRevisions:

```bash
# Propose deletion of main branch PackageRevisions
porchctl rpkg propose-delete porch-test.test-published-package.main --namespace=default
porchctl rpkg propose-delete porch-test.test-reject-delete.main --namespace=default

# Approve the deletions
porchctl rpkg del porch-test.test-published-package.main --namespace=default
porchctl rpkg del porch-test.test-reject-delete.main --namespace=default
```

Verify complete cleanup:

```bash
porchctl rpkg get --namespace=default
```

All test PackageRevisions should now be removed.

---
