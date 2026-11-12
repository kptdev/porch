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

The admission webhook does not check history. It does check for directory conflicts. For
Repositories with the same URL and branch, the webhook applies these rules:

- Identical directories conflict only within the same namespace. Across namespaces they are
  allowed.
- A root directory (`/`) conflicts with any other directory, in any namespace.
- Nested directories (one path inside another) conflict in any namespace.
- Disjoint directories never conflict.

History compatibility is checked during the next sync. For each repository:

- The commit recorded in `status.gitCommitHash` must still be reachable from the head of
  the configured branch on the new remote. The branch may have moved forward.
- If it is, the sync succeeds, `status.observedGeneration` catches up to `metadata.generation`,
  and the repository is `Ready=True`.
- If it is not, the sync fails with a "not present in the history" error. The repository is
  `Ready=False` and `status.gitCommitHash` keeps its last good value.

## Repositories and Git URLs

The relationship between Porch repositories and Git repositories is one-to-many:

- **One Repository resource points at one Git URL.** `spec.git.repo` holds a single URL, and
  `spec.git.branch` and `spec.git.directory` select a branch and a directory within it.
- **One Git URL can back many Repository resources.** Several Repository resources can
  reference the same URL and branch, provided their directories do not clash under the rules
  above.

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
3. Resume normal operations only after every repository has `observedGeneration` equal to
   `generation` and `Ready=True` on the new URL.

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

     Compare the generations. `metadata.generation` is the spec version that the
     rehome changed. `status.observedGeneration` is the spec version the last sync
     completed for:

     ```bash
     kubectl get repository <repo-name> -n <namespace> \
       -o jsonpath='generation={.metadata.generation} observedGeneration={.status.observedGeneration}{"\n"}'
     ```

     The rehome sync is finished only when `observedGeneration` equals `generation` and
     `Ready` is `True`. Until then, `Ready=True` can still describe the old URL. This is
     most likely when `U1` and `U2` have the same head, because the recorded hash then
     matches either URL. `gitCommitHash` should be the head of the branch on `U2`.
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
   resources. This works only if the recorded commit is reachable from `U1`'s branch. See
   [Reverting a rehome](#reverting-a-rehome).

5. **Remove the old URL.** Once every Repository resource in the group has
   `observedGeneration` equal to `generation` and `Ready=True` on `U2`,
   and package operations have been verified, `U1` is no longer referenced by Porch. Porch
   does not delete anything from `U1`. Retiring that remote is up to you.

## Reverting a rehome

Reverting is governed by the same history check as a rehome. Setting `spec.git.repo` back to
`U1` is accepted if `status.gitCommitHash` is reachable from the head of `U1`'s configured
branch. Whether the rehome sync succeeded does not matter.

- **The rehome sync failed.** `status.gitCommitHash` still holds the last good commit. If `U1`
  still contains it, the revert succeeds.
- **The rehome sync succeeded.** `status.gitCommitHash` holds the commit from `U2`. The revert
  succeeds only if `U1` also contains that commit. This is always the case when both URLs have the
  same head, and can be the case when `U1` has not moved on. If `U1` does not contain it, the
  revert is rejected. Fix the problem on `U2` or rehome again to a remote that contains the
  recorded commit.

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

The porchctl paths below match on these URLs, so each can miss an upstream after a rehome:

- `porchctl rpkg upgrade --subpackage-dir` for a subpackage whose Kptfile has no
  `upstream.ref`. This covers packages cloned before the reference was recorded, and clones
  from a raw Git URL.
- `porchctl rpkg upgrade` of a package created by an edit, which finds its upstream through
  `status.upstreamLock`.
- `porchctl rpkg upgrade` of a v1alpha2 package cloned from a raw Git URL, which finds its
  upstream through `status.upstreamLock`.
- `porchctl rpkg upgrade --discover upstream` and `--discover downstream` in v1alpha1. These
  match `status.upstreamLock` on URL for every package, including name-based clones. A stale
  URL can therefore hide available updates. v1alpha2 discovery matches by name, except for raw
  Git URL clones.

Everything else uses names. Upgrading a main package created by a clone or an upgrade uses
names only, in both API versions.

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
