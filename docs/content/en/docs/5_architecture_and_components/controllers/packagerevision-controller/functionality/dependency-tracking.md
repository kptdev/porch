---
title: "Dependency Tracking"
type: docs
weight: 5
description: |
  How the PR Controller projects upstream/downstream package dependencies and
  enforces pre-deletion safety.
---

## Overview

When a package is cloned from an upstream repository, it records that reference
in its `Kptfile` `upstream`/`upstreamLock` fields. A package may also contain
nested [subpackages]({{< relref "subpackage-operations.md" >}}) (independent
directories with their own Kptfiles), each with their own upstream reference.
The PR Controller projects all of these direct references (root package + all
nested subpackages) onto the PackageRevision `status`, making them queryable
without reading package content.

The validating webhook uses this projection to block deletion of a package that
other packages still depend on, ensuring data integrity.

This feature is available on the `porch.kpt.dev/v1alpha2` PackageRevision CRD
only. It operates within a single namespace and tracks **direct dependencies**
(the immediate upstream of a package and all its nested subpackages), not
transitive chains.

## What the controller projects

During reconciliation, after a package's content is available, the controller
reads the rendered resources it already has in hand and populates three
`status` fields:

| Field | Meaning |
|-------|---------|
| `status.upstreamLock` | The resolved git locator of the package's **root** upstream (existing field). |
| `status.subpackageUpstreams` | One entry per nested subpackage `Kptfile` that has a resolved `upstreamLock`, each carrying the subpackage `path` and the upstream git locator. |
| `status.upstreamKeys` | A flattened, de-duplicated set of canonical keys (`repo\|directory\|ref`) derived from `upstreamLock` and every `subpackageUpstreams` entry. Used for reverse dependency lookups. |
| `status.dependencyTruncated` | `true` when the number of discovered subpackage upstreams exceeded the cap (`MaxSubpackageUpstreams`, currently 100) and the list was capped. |

The upstream key intentionally excludes the git commit, so a query by
ref/tag (for example `v2`) matches regardless of the exact resolved commit.
The full locator (including commit) is preserved in `status.upstreamLock` and
`status.subpackageUpstreams` for exact-reference use.

### Consistency and cost

The projection is derived from package content and is **eventually consistent**:
it is recomputed on the reconcile that follows a content change, and written
only when it differs from the current value. All the dependency status fields
are applied together in a single Server-Side Apply, so they are never partially
updated. Computing the projection adds no extra reads — it reuses the rendered
resource map already loaded during reconciliation.

A malformed nested `Kptfile` does not fail the whole projection — the other
subpackages are still recorded — but it marks the projection incomplete
(`dependencyTruncated=true`). Because the projection backs a safety guard, an
incomplete projection is treated as a possible dependency (the delete guard
fails closed) rather than silently assuming the package is dependency-free.

## Querying dependencies

Both directions are answered from `status`, without reading package content:

- **Bottom-up** — given a package, read `status.upstreamLock` and
  `status.subpackageUpstreams` to see the upstream packages (and versions) it
  depends on.
- **Top-down** — given an upstream package, list the packages whose
  `status.upstreamKeys` contain that package's key. This is a namespace-scoped
  lookup.

The [`porchctl rpkg deps`]({{% relref "/docs/7_cli_api/porchctl#rpkg-deps" %}})
command wraps both directions.

## Pre-deletion safety

The validating webhook blocks removal of an upstream package that other packages
depend on, so that deleting an in-use package cannot leave downstream packages
dangling.

Two checks share the same reverse lookup:

- **On the `DeletionProposed` transition** — proposing deletion of a package
  that is still referenced is rejected. Checking here, *before* the destructive
  step, prevents a multi-version delete from leaving a package partially
  deleted when a later version turns out to be in use.
- **On `DELETE`** — deletion is rejected with an error listing the blocking
  downstream packages (capped for readability), so operators know what to
  remove first.

Both checks match dependents two ways: by name (a top-level clone/copy/upgrade
source) and by git locator (a subpackage reference). A package whose dependency
projection is truncated is treated as a possible dependent (the check fails
closed), so an incomplete projection can never permit an unsafe delete.

## Scope and limitations

- **Same namespace.** The reverse lookup and the delete guard are namespace
  scoped. A dependent in a different namespace is not tracked — and, because
  subpackage upstream references resolve within a namespace, such a
  cross-namespace dependency cannot be formed through the normal reference path
  in the first place.
- **Direct dependencies only.** The controller records each package's immediate
  upstream (the root package and all nested subpackages within it). It does not
  walk transitive chains. For example, if package A clones B, and B clones C,
  the system records that A depends on B and B depends on C as separate edges.
  To answer "what does A transitively depend on?", you reconstruct the chain by
  following these direct edges across packages. This design keeps dependency
  storage compact and allows each package to own its own references.
