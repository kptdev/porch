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
[the kpt package documentation](https://kpt.dev/book/03-packages) for a description of dependent and independent subpackages.

## What is an Independent Subpackage?

An **independent subpackage** maintains its own upstream source and can be independently upgraded. Unlike regular package contents
and dependent subpackages that are managed as a single unit, independent subpackages retain upstream tracking information in their
`Kptfile`, enabling them to be upgraded separately from the parent package.

## What is a Dependent Subpackage?

A **dependent subpackage** does not maintain its own upstream source and cannot be independently upgraded.  The subpackage contents
are managed as a single unit with the regular package contents.

## Why Use Independent Subpackages?

Independent subpackages enable **composition** of packages from multiple upstream sources. A single parent package can
contain multiple independently-versioned components, each tracking a different upstream package. This is useful when a deployment
package must combine resources from several blueprint packages, different components within a package need to be upgraded on
different schedules or you want to assemble a complex package from reusable building blocks without creating separate package
revisions for each component.

## How Subpackages Work

### Cloning a Subpackage

To add an independent subpackage to an existing package, you clone an upstream package into a subdirectory of a **Draft**
parent package revision. The clone operation:

1. Copies the upstream package contents into the specified subdirectory
2. Preserves the upstream's `Kptfile`
3. Updates the `metadata.name` of the subpackage
4. Adds the origin information of the subpackage to the `Kptfile` of the cloned subpackage

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
2. Merges the new upstream version into the subpackage directory employing the specified strategy, using the same mechanism
as a regular package revision upgrade
3. Updates the `metadata.name` of the subpackage
4. Updates the origin information of the subpackage in the `Kptfile` of the cloned subpackage

```bash
porchctl rpkg upgrade deployment.my-app.v2 \
  --subpackage-dir=components/networking \
  --revision=3
```
If the `revision` parameter is omitted, the subpackage is upgraded to the latest available published revision of its upstream
source.

Subpackage operations may be performed on a freshly created draft before any other modifications are pushed to it or on
a draft to which other modifications have already been pushed. Multiple subpackages may be cloned and upgraded on a draft
one after another before it is proposed and approved.


## Constraints

- The parent package revision must be in **Draft** state for both clone and upgrade operations
- The `--subpackage-dir` path must be a valid relative path (no leading `/`, `./`, `.`, or `..` segments) and comply with the subpackage directory naming rules described in the [subpackage naming](#subpackage-naming) section below.
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

## Subpackage Naming

When Porch clones or upgrades a subpackage it names the subpackage (sets `metadata.name`) based on the `--subpackage-dir` parameter value (`subpackageDir` on the API).
It creates a Kubernetes-compliant DNS subdomain name and inserts it in the `metadata.name` field of the Kptfile.

Porch converts any “/“ characters in the `--subpackage-dir` or `subpackageDir` value into ‘.’ characters to create a
[valid Kubernetes DNS Subdomain name](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/). This creates a
unique name for the subpackage in the package. This means that `--subpackage-dir` and `subpackageDir` values are restricted to the rules for subdomain
names in `--subpackage-dir` and `subpackageDir` values.

- No more than 253 characters (after replacing "/" with ".")
- Only lowercase alphanumeric characters, "-", "/" ("/" is converted to ".")
- Must start and end with an alphanumeric character (letter or digit)

This [Kubernetes validation IsDNS1123Subdomain() function](https://github.com/kubernetes/apimachinery/blob/master/pkg/util/validation/validation.go)
is used to check the value once "/" characters are replaced with "." characters.

So the following `subpackageDir` values result in the following `metadata.name` values in the Kptfile:

| subpackageDir                            | metadata.name                            |
|------------------------------------------|------------------------------------------|
| subpackage                               | subpackage                               |
| ran/subpackage                           | ran.subpackage                           |
| ran/south/southeast/region-1a/subpackage | ran.south.southeast.region-1a.subpackage |
| 1subpackage                              | 1subpackage                              |
| 1subpckage2/3subpackage4/5subpackage6    | 1subpckage2.3subpackage4.5subpackage6    |
| Subpackage                               | error (Uppercase character)              |
| sub_package                              | error ("_" illegal)                      |
| ran\subpackage                           | error ("\\" illegal)                     |

## Key Points

- Independent subpackages enable composing a package from multiple upstream sources
- Each independent subpackage maintains its own upstream tracking via its `Kptfile`
- Subpackage operations (clone and upgrade) modify the parent package revision in-place
- The parent package must be in Draft state for subpackage operations
- Subpackages can be upgraded independently, on their own schedule
