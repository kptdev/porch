---
title: "Rehoming Repositories"
type: docs
weight: 6
description: "How to point Porch repositories at a Git repository whose URL has changed"
---

## What rehoming is

Rehoming is changing the Git URL of a Git repository that Porch already uses, so that
the Porch Repository resources now point at the new URL. The contents are the same
repository, but the address has changed, for example because it moved to a new host.

In Porch this is a change to `spec.git.repo` on a Repository resource. The field is
mutable. Porch does not recreate the Repository or its packages. It treats the new URL as
the same repository, provided the history still contains the commit Porch last synced.

The admission webhook does not check history. It does check for conflicts. A new URL,
together with the branch and directory, must not clash with another Repository in the same
namespace. History compatibility is checked during the next sync. For each repository:

- The commit recorded in `status.gitCommitHash` must still be reachable from the head of
  the configured branch on the new remote. The branch may have moved forward.
- If it is, the sync succeeds and the repository is `Ready=True`.
- If it is not, the sync fails with a "not present in the history" error. The repository is
  `Ready=False` and `status.gitCommitHash` keeps its last good value.

## Repositories and Git URLs

The relationship between Porch repositories and Git repositories is one-to-many:

- **One Repository resource points at one Git URL.** `spec.git.repo` holds a single URL, and
  `spec.git.branch` and `spec.git.directory` select a branch and a directory within it.
- **One Git URL can back many Repository resources.** Several Repository resources can
  reference the same URL. The admission webhook rejects overlapping locations only within a
  namespace. The same URL and branch can be registered in several namespaces, and in one
  namespace with disjoint directories.

This matters because Porch locks Git access by URL, not by Repository resource. See the next
section.

## Locking and why rehomes must be grouped

Porch keeps one local clone per Git URL. All Repository resources that use that URL share the
clone and one lock, across namespaces. Repositories on different URLs have separate clones
and locks. Porch has no record that two URLs are the same repository.

This has a consequence for rehoming. Suppose two Repository resources, `A` and `B`, both point at
URL `U1`. If only `A` moves to `U2`:

- `A` now writes to `U2` under the `U2` lock.
- `B` still writes to `U1` under the `U1` lock.
- Writes from `A` and `B` to the same underlying repository are no longer serialised. Porch's
  locking no longer protects them.

To avoid this, every Repository resource that uses the old URL must be rehomed to the new URL
together, during the same maintenance window.

Rehoming several different old URLs in the same window is fine. Each group of repositories
sharing one old URL must move together to a single new URL.

## Maintenance window

Porch cannot guarantee safe writes across a URL change, so run rehoming inside a maintenance
window:

1. Stop all create, update and delete operations on every package revision in every Repository
   that uses the URL being changed. This includes proposing, approving, publishing and deleting.
2. Rehome all of those Repository resources, and wait for each sync to finish (see below).
3. Resume normal operations only after every repository is `Ready=True` on the new URL.

## Rehoming steps

Assume the old URL is `U1` and the new URL is `U2`. Repeat these steps for each group of
Repository resources that share `U1`.

1. **Make the new URL addressable.** Make sure `U2` is reachable from the cluster and that the
   credentials in the repository's secret work against it. Confirm the branch exists and
   contains the commit Porch last synced. For each repository, check its last recorded commit:

   ```bash
   kubectl get repository <repo-name> -n <namespace> -o jsonpath='{.status.gitCommitHash}'; echo
   ```

2. **Rehome the Repository resources.** Update `spec.git.repo` on each one:

   ```bash
   kubectl patch repository <repo-name> -n <namespace> --type merge \
     -p '{"spec":{"git":{"repo":"U2"}}}'
   ```

   The change is accepted immediately. It also triggers a sync (see below).

3. **Observe the sync.** The rehome causes a full sync of each repository. Check that it
   completes:

   - Repository status:

     ```bash
     kubectl get repository <repo-name> -n <namespace> \
       -o jsonpath='{range .status.conditions[?(@.type=="Ready")]}{.status}{" "}{.reason}{" "}{.message}{"\n"}{end}'
     kubectl get repository <repo-name> -n <namespace> -o jsonpath='{.status.gitCommitHash}'; echo
     ```

     `Ready` should be `True`. `gitCommitHash` should be the head of the branch on `U2`.
   - Controller log. A successful sync logs `Repository full sync completed successfully`.
     A history failure logs `Repository history validation failed`:

     ```bash
     kubectl logs -n porch-system deployment/porch-controllers | grep -i "<repo-name>"
     ```

   For a general description of syncs and how to trigger one manually, see
   [Synchronizing Repositories]({{% relref "/docs/4_tutorials_and_how-tos/working_with_porch_repositories/repository-synchronization" %}}).

4. **Fix and repeat.** If a repository is not `Ready=True`, fix the cause on `U2`, such as a
   missing branch, missing commit or bad credentials. Then repeat step 3. A spec change
   triggers another sync. You can also trigger one with `porchctl repo sync <repo-name> --namespace <namespace>`.

   If you need to abandon the rehome, set `spec.git.repo` back to `U1` on the affected
   resources. This works only while the rehome sync is failing. See
   [Reverting a rehome](#reverting-a-rehome).

5. **Remove the old URL.** Once every Repository resource in the group is `Ready=True` on `U2`,
   and package operations have been verified, `U1` is no longer referenced by Porch. Porch
   does not delete anything from `U1`. Retiring that remote is up to you.

## Reverting a rehome

Reverting depends on whether the rehome sync succeeded:

- **The rehome sync failed.** `status.gitCommitHash` still holds the last good commit, so
  setting `spec.git.repo` back to `U1` passes the history check and the repository becomes
  `Ready=True` again.
- **The rehome sync succeeded.** `status.gitCommitHash` now holds the commit from `U2`. Reverting
  to `U1` is rejected unless `U1` also contains that commit. In that case, do not revert.
  Fix the problem on `U2` or rehome again to a remote that contains the recorded commit.

## What goes stale after a rehome

After a rehome, these references still contain the old URL. This is expected and is not
rewritten:

- **`upstream` and `upstreamLock` in Kptfiles** of main packages and subpackages.
- **`status.upstreamLock` and `status.selfLock`** on package revisions created before the rehome.

Porch does not rewrite these references. Published package revisions are immutable: Porch
only allows updates to draft and proposed packages. Fixing the references would therefore mean
changing every affected package revision, which is not possible for published ones. Even for
drafts, it would mean pulling, updating and pushing the Kptfile of every affected package across
all repositories, which is too costly to do as part of a rehome.

Porch uses the Kptfile and status URLs only in these porchctl fallback paths. Each one
matches on the URL and fails after a rehome:

- Upgrading a subpackage whose Kptfile has no `upstream.ref` (packages cloned before the
  reference was recorded, and clones from a raw Git URL).
- Finding the upstream of a package created by an edit, where the upstream is found through
  `status.upstreamLock`.
- Finding the upstream of a package cloned from a raw Git URL in v1alpha2.

Everything else uses names, not URLs. Upgrading a main package created by a clone or an
upgrade uses names only. Upgrading a package created by an edit falls back to matching
`status.upstreamLock` on URL.

## How Porch resolves packages after a rehome

- **Clones and upgrades reference packages by Kubernetes name.** The upstream reference in
  a main package's `spec` (`UpstreamRef`), and the subpackage Kptfile's `upstream.ref`, hold the
  name of a PackageRevision. Porch looks that up by name in the same namespace.
- **Each PackageRevision points at its Repository by name.** Porch uses the repository name,
  not the URL, to find the Repository resource. The repository's current `spec.git.repo` is then
  used to open the Git clone.
- **The URL in use is the current one, once the sync has run.** Porch reopens the repository
  with the new URL when the spec changes, and the rehome sync runs at that point. Until that
  sync has completed, the cached clone can still point at the old URL. This is why writes must
  stay paused during the maintenance window.
