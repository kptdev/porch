---
title: "Subpackages"
type: docs
weight: 7
description: |
  Understanding independent subpackages: nested packages within a parent package that maintain their own upstream relationship.
---

## What are Subpackages?

A **subpackage** is a kpt package nested within a parent package at a specific subdirectory. Subpackages have a `Kptfile` that can
be used to apply specific mutations or validations to the contents of the subpackage. See
[the kpt package documentation](https://kpt.dev/book/03-packages) for a full description of dependent and independent subpackages.

## What is an Independent Subpackage?

An **independent subpackage** maintains its own upstream source and can be independently upgraded. Unlike regular package contents
and dependent subpackages that are managed as a single unit, independent subpackages retain upstream tracking information in their
`Kptfile`, enabling them to be upgraded separately from the parent package.

## What is a Dependent Subpackage?

A **dependent subpackage** does not maintain its own upstream source and cannot be independently upgraded.  The subpackage contents
is managed as a single unit with the regular package contents.

## Why Use Independent Subpackages?

Independent subpackages enable **composition** of packages from multiple upstream sources. A single parent package can
contain multiple independently-versioned components, each tracking a different upstream package. This is useful when a deployment package must combine resources from several blueprint packages,different components within a package need to be upgraded on different schedules or you want to assemble a complex package from reusable building blocks without creating separate package revisions for each component.

## How Subpackages Work

### Cloning a Subpackage

To add an independent subpackage to an existing package, you clone an upstream package into a subdirectory of a **Draft**
parent package revision. The clone operation:

1. Copies the upstream package contents into the specified subdirectory
2. Preserves the upstream's `Kptfile`
3. Adds the origin information of the subpackage to the `Kptfile` of the cloned subpackage

```bash
porchctl rpkg clone upstream-repo.blueprint.v1 deployment.my-app.v2 \
  --subpackage-dir=components/networking \
  --namespace=default
```

In this example, the `blueprint` package from `upstream-repo` is cloned into the `components/networking` directory
of the draft parent package revision `deployment.my-app.v2`.

### Upgrading a Subpackage

When a new version of the upstream package is published, the independent subpackage can be upgraded independently of
the parent package. The upgrade operation:

1. Reads the subpackage's `Kptfile` to determine its current upstream source
2. Merges the new upstream version into the subpackage directory using the specified strategy using the same mechanism as is used in the upgrade of a regular PR
3. Updates the origin information of the subpackage in the `Kptfile` of the cloned subpackage

```bash
porchctl rpkg upgrade deployment.my-app.v2 \
  --subpackage-dir=components/networking \
  --revision=3
```
If the `revision` parameter is omitted, the subpackage is upgraded to the latest available published revision of its upstream
source.

Subpackage operations may be performed on a freshly created draft before any other modifications are pushed to it or on
a draft to which other modifications have already been pushed. Muitiple subpackages may be cloned and upgraded on a draft
one after another before it is proposed and approved.


## Constraints

- The parent package revision must be in **Draft** state for both clone and upgrade operations
- The `--subpackage-dir` path must be a valid relative path (no leading `/`, `./`, or `..` segments)
- For clone: the subdirectory must **not already exist** in the package
- For upgrade: the subdirectory **must already exist** and contain a valid `Kptfile` with upstream information
- `--workspace` and `--repository` must not be specified when using `--subpackage-dir`

## Subpackages vs Regular Clone

| Aspect | Regular Clone | Subpackage Clone |
|--------|---------------|------------------|
| Result | Creates a new package revision | Adds content to an existing draft package revision |
| Target | New package in a repository | Subdirectory within a parent package |
| Upgrade | Creates a new package revision | Modifies the parent package revision in-place |
| Tracking | PackageRevision tracks upstream | Subpackage's Kptfile tracks upstream |

## Key Points

- Independent subpackages enable composing a package from multiple upstream sources
- Each independent subpackage maintains its own upstream tracking via its `Kptfile`
- Subpackage operations (clone and upgrade) modify the parent package revision in-place
- The parent package must be in Draft state for subpackage operations
- Subpackages can be upgraded independently, on their own schedule
