---
title: "Unregistering Repositories"
type: docs
weight: 6
description: "Unregistering repositories guide in Porch"
---

## Unregistering Repositories

When you no longer need Porch to manage packages from a repository, you can unregister it. This removes Porch's connection to the repository without affecting the underlying Git storage.

### Unregister a Repository

Remove a repository from Porch:

```bash
porchctl repo unregister porch-test --namespace default
```

This command removes the Repository resource from Kubernetes, it stops synchronizing packages from the repository, removes Porch's cached metadata for the repository but **does not delete** the underlying Git repository or its contents.

{{% alert title="Warning" color="warning" %}}
Unregistering a repository does not delete the underlying Git repository or its contents. It only removes Porch's connection to it.
{{% /alert %}}

**Example output:**

```bash
porch-test unregistered
```

**What happens to packages:**

- **Published packages in Git**: Remain in the Git repository and are preserved. If you re-register the same repository later, these packages will reappear when Porch synchronizes.
- **Draft/Proposed packages pushed to Git**: Also remain in Git and will reappear upon re-registration.
- **Unpushed work-in-progress packages**: Draft or Proposed revisions that have not yet been pushed to Git are removed and cannot be recovered. With DB Cache draft push mode enabled (`--db-push-drafts-to-git`), drafts are pushed during the repository sync. Ensure that the sync has run before unregistering if you need drafts preserved in Git.

---
