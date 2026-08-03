---
title: "Source Execution"
type: docs
weight: 1
description: |
  How the PR Controller creates initial package content.
---

## Overview

Source execution handles one-time package creation. When a user creates a PackageRevision with `spec.source` set, the controller executes the specified operation to produce initial content in the shared cache. This phase is idempotent: the guard is `status.creationSource`; if it is already set, source execution is skipped entirely.

After any source execution, the controller creates a draft in the cache, writes the resources, closes the draft (committing to Git), and requeues to trigger rendering.

## Init

Creates a brand new package by generating a Kptfile with the specified metadata (name, description, keywords). No external dependencies.

```yaml
spec:
  source:
    init:
      description: "Edge router configuration"
      keywords:
        - router
        - edge
```

## Clone

Copies content from an upstream package. Two modes are supported:

**From a registered PackageRevision** (by upstream ref):

```yaml
spec:
  source:
    cloneFrom:
      upstreamRef:
        name: upstream-repo-base-pkg-v1
```

**From a raw Git URL:**

```yaml
spec:
  source:
    cloneFrom:
      type: git
      git:
        repo: https://github.com/example/blueprints.git
        ref: basens/v1
        directory: /basens
        secretRef:
          name: my-repo-auth
```

In both cases, the Kptfile's `upstream` and `upstreamLock` fields are set to track the source, enabling future upgrades.

## Copy

Creates a new revision from an existing published revision of the same package in the same repository. This is the mechanism for "edit an existing package": copy the latest published revision into a new draft workspace.

```yaml
spec:
  source:
    copyFrom:
      name: my-repo-my-package-v1
```

## Upgrade

Performs a 3-way merge between the old upstream, new upstream, and current local package. Supports multiple merge strategies:

| Strategy | Behaviour |
|----------|-----------|
| `resource-merge` | Field-level merge respecting KRM semantics (default) |
| `fast-forward` | Only succeeds if local has no changes from old upstream |
| `force-delete-replace` | Replaces local content entirely with new upstream |
| `copy-merge` | Copies new upstream, preserving local-only files |

After merging, the Kptfile `upstream`/`upstreamLock` fields are updated to reference the new upstream version.
