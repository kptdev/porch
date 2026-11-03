---
title: "Repositories"
type: docs
weight: 2
description: |
  Understanding repositories in Porch: connecting Porch to Git and OCI storage backends.
---

## What is a Repository in Porch?

A **Repository** is a Kubernetes resource that connects Porch to a Git repository where kpt packages are stored. When you
register a repository with Porch, you're creating a Repository resource that tells Porch:
- Where the storage backend is located (Git URL)
    - including a particular branch within the repository - the deployment branch
- How to authenticate (via references to a Kubernetes Secret containing the credentials)
- Whether it's a deployment repository (packages ready for deployment)
- How often to sync/refresh the package revision cache

Repositories can be accessed using the short name `repo` for convenience (e.g., `kubectl get repo`).

## Repository Types

Porch supports two storage backends:

### Git Repositories

Git repositories are the primary and fully-supported storage backend for Porch. Packages are stored as kpt packages in a Git branch, each in a directory with a Kptfile. When using the CR cache, Draft and Proposed package revisions live on temporary branches (for example, `drafts/my-package/v1` or `proposed/my-package/v1`), while Published package revisions are committed to the registered deployment branch. The full Git history is preserved for audit and rollback, and standard Git authentication (username/password, SSH keys, tokens) is used.

### OCI Repositories (Experimental)

OCI (Open Container Initiative) registries provide an alternative storage backend. Packages are stored as OCI artifacts (similar to container images). Support is experimental and may be unstable. This option is useful in environments that already use OCI registries.

{{% alert title="Warning" color="warning" %}}
OCI repository support is **experimental** and **may be unstable**. Git repositories are recommended for production use.
{{% /alert %}}

## Deployment vs Blueprint Repositories

Repositories can be designated as either deployment or blueprint repositories via the `spec.deployment` field. This field
is merely an indicator - Porch does not treat deployment repositories differently from blueprint repositories in any respect.

- **Blueprint repositories** (`deployment: false`) store reusable template packages that are meant to be cloned and customized. They are typically maintained by platform teams. Examples include shared infrastructure patterns and application templates.
- **Deployment repositories** (`deployment: true`) store deployment-ready packages: published packages that GitOps tools (such as Flux) can deploy. They typically target specific environments or clusters. Examples include prod-cluster-configs and dev-environment-packages.

## The Repository-Package Relationship

When a Repository resource is registered with Porch, Porch automatically conducts [package revision discovery]({{% relref "package-revisions#package-revision-discovery" %}}): it scans the storage backend for kpt packages (directories containing a Kptfile), creates PackageRevision resources for each discovered package, maintains a cache of package metadata for performance, and periodically syncs to detect new or updated packages. This means the Repository resource acts as the bridge between Porch's Kubernetes API and the actual Git repository where kpt package files are stored.

## Immutability

{{% alert title="Warning" color="warning" %}}
The following fields are **immutable** after repository creation and cannot be modified:
- `spec.type`
- `spec.git.branch`
- `spec.git.createBranch`
- `spec.git.directory`
- `spec.oci.registry`

To change these fields, you must delete and recreate the Repository resource.
{{% /alert %}}

This immutability ensures consistency and prevents accidental changes to the fundamental identity of the repository.

## Rehoming a Repository

Unlike the fields above, `spec.git.repo` (the Git URL) **is mutable**. Changing it lets you
*rehome* a repository: point an existing Repository resource at a different Git remote without
deleting and recreating it (for example, after migrating a repository to a new host).

Rehoming is validated **asynchronously during the next repository sync**, not at admission time.
This means the Kubernetes API always accepts the URL change immediately; whether the change is
*usable* is determined when the controller next syncs:

- The commit recorded in `status.gitCommitHash` (the branch HEAD as of the last successful sync)
  must still be present in the history of the configured branch on the new remote. It does **not**
  have to be the new remote's current HEAD — the branch is allowed to have moved forward. This
  "the recorded commit is reachable from the new branch HEAD" rule proves the new URL points at the
  same repository Porch was already tracking (for example, a mirror or fork), rather than an
  unrelated or divergent repository.
- If the recorded commit **is in** the new branch's history, the sync succeeds and the repository
  stays `Ready`.
- If the recorded commit is **not in** the new branch's history, the URL change still persists in
  the spec, but the sync fails and the repository reports `Ready=False` with `Reason=Error` and a
  message of the form `repository hash "..." registered on the repository is not present in the
  history of branch "..."`. The last-good `status.gitCommitHash` is left unchanged.
- To recover, set `spec.git.repo` to a URL whose configured branch still contains the recorded
  commit (for example, revert to the original URL). The spec change triggers an immediate re-sync,
  which clears the error and returns the repository to `Ready`.

See [Repository Rehoming Fails After URL Change]({{% relref "/docs/9_troubleshooting_and_faq/repository-sync#repository-rehoming-fails-after-url-change" %}})
for troubleshooting.

## Repository Status

The Repository resource maintains status information that reflects the current state of synchronization and the repository's health:

**Condition Fields:**
- `conditions`: Standard Kubernetes conditions indicating repository health and readiness

**Sync Tracking:**
- `lastFullSyncTime`: Timestamp of the last successful full sync
- `nextFullSyncTime`: Scheduled time for the next full sync
- `observedGeneration`: Generation of the spec that was last observed (used to detect spec changes)
- `observedRunOnceAt`: The `runOnceAt` value that was last processed (prevents duplicate one-time syncs)

**Repository Information:**
- `packageCount`: Number of packages discovered in the repository
- `gitCommitHash`: Current Git commit hash for Git repositories (empty for OCI repositories)

These status fields are updated by the Repository Controller and provide observability into sync operations. See the [Repository Controller documentation]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller" %}}) for details on how the controller manages repository synchronization.

### kubectl Output

When viewing repositories with `kubectl get repo`, you'll see several useful columns including Branch and Age:

```bash
$ kubectl get repo
NAME        TYPE   CONTENT      SYNC SCHEDULE   DEPLOYMENT   READY   ADDRESS                           BRANCH   AGE
my-repo     git    Package      0 */1 * * *     true         True    https://github.com/org/repo.git   main     5d
```

## Sync Configuration

The `spec.sync` field controls how and when Porch synchronizes with the repository:

**Periodic Synchronization:**
- `schedule`: A cron expression for scheduled syncs (e.g., `*/10 * * * *` to sync every 10 minutes)

**One-Time Synchronization:**
- `runOnceAt`: A timestamp to trigger a one-time cache sync outside the periodic schedule. This is tracked via `observedRunOnceAt` in the status to prevent redundant syncs.

See [Repository Sync Configuration]({{% relref "/docs/6_configuration_and_deployments/configurations/repository-sync" %}}) for detailed sync configuration options.

## Key Points

- **Repository** is a Kubernetes resource, not the Git/OCI storage itself
- It tells Porch where to find kpt packages and how to access them
- Git repositories are production-ready; OCI support is experimental
- Deployment repositories should contain packages that are ready for deployment
- Porch automatically discovers packages, parses their history, and exposes them as package revisions
