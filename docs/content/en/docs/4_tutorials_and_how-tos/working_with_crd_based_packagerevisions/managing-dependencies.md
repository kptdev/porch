---
title: "Managing Package Dependencies"
type: docs
weight: 4
description: |
  Query dependencies between upstream and downstream packages, and safely
  delete packages that others depend on.
---

## Overview

When you clone an upstream package (for example one from a blueprint
repository) into a downstream package, Porch records the relationship. This
guide shows how to inspect those relationships in both directions and how to
check whether a package is safe to delete before you remove it.

These capabilities are available on the `porch.kpt.dev/v1alpha2` PackageRevision
CRD only, and operate within a single namespace. For the underlying mechanism,
see [Dependency Tracking]({{% relref "/docs/5_architecture_and_components/controllers/packagerevision-controller/functionality/dependency-tracking" %}}).

## Find what a package depends on (bottom-up)

Given a downstream package, list the upstream packages it was built from —
including any independent subpackages — with their exact version reference:

```bash
porchctl rpkg deps deployment-repo.my-app.v3 -n default
```

Example output:

```
deployment-repo.my-app.v3 depends on:
  - root: https://example.com/blueprints.git directory=base-pkg ref=base-pkg/v2 commit=1a2b3c4
  - subpackage components/networking: https://example.com/blueprints.git directory=net-pkg ref=net-pkg/v1 commit=9f8e7d6
```

`deps` requires no `--api-version` flag — it always uses the v1alpha2 API.
(Passing `--api-version=v1alpha1` is rejected, since the dependency data is a
v1alpha2-only feature.)

You can also read the same data directly from status with `kubectl`:

```bash
kubectl get packagerevision deployment-repo.my-app.v3 -n default \
  -o jsonpath='{.status.upstreamLock}{"\n"}{.status.subpackageUpstreams}{"\n"}'
```

## Find what depends on a package (top-down)

Given an upstream package, list every downstream package that references it —
whether as a top-level clone or as an embedded subpackage:

```bash
porchctl rpkg deps blueprint-repo.base-pkg.v2 --dependents -n default
```

Example output:

```
blueprint-repo.base-pkg.v2 is depended on by 2 package(s):
  - default/deployment-repo.my-app.v3
  - default/deployment-repo.other-app.v1
```

This is the query to use when an upstream package is outdated or flawed and you
need to identify every downstream package built on it.

## Check before deleting

Before deleting an upstream package, confirm nothing still depends on it:

```bash
porchctl rpkg deps blueprint-repo.base-pkg.v2 --can-delete -n default
```

If the package is unused, the command succeeds:

```
blueprint-repo.base-pkg.v2 can be deleted (no dependents)
```

If it is still in use, the command exits non-zero and names the blockers:

```
Error: blueprint-repo.base-pkg.v2 cannot be deleted: referenced by 1 downstream
package(s): default/deployment-repo.my-app.v3
```

### Deleting a package with multiple versions

An upstream package typically has several published versions plus a main
tracking revision, and deleting it means removing each version individually. To
avoid leaving the package half-deleted when one version turns out to be in use,
check every version up front and only proceed if all are clear:

```bash
for v in v1 v2 v3; do
  porchctl rpkg deps "blueprint-repo.base-pkg.$v" --can-delete -n default || exit 1
done
# all clear — now delete each version
```

You do not have to rely on this check alone: Porch enforces the same rule
server-side. Proposing deletion of a referenced package is rejected **before**
the package is transitioned to `DeletionProposed`, so a blocked version cannot
leave earlier versions permanently deleted. The pre-flight check simply lets you
catch the problem before starting.

## Notes

- **Same namespace.** Dependency queries and the deletion guard are scoped to
  the package's namespace.
- **Eventually consistent.** Dependency data is derived from package content and
  is updated shortly after a change. If a package's dependency list was capped
  (very large numbers of subpackages), `deps` prints a warning that results may
  be incomplete, and the deletion guard treats such a package as a possible
  dependent (fails closed).
