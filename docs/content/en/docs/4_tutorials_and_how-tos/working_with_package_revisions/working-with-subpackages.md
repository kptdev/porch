---
title: "Working with Subpackages"
type: docs
weight: 7
description: "A guide to cloning and upgrading independent subpackages within package revisions"
---

Independent subpackages allow you to compose a single package from multiple upstream sources, where each subpackage
can be cloned and upgraded independently. This guide walks through the complete workflow of adding an independent
subpackage to an existing package and then upgrading it when a new upstream version becomes available.

For the conceptual overview, see [Subpackages]({{% relref "/docs/2_concepts/subpackages" %}}).
For detailed command reference, see the [porchctl CLI guide]({{% relref "/docs/7_cli_api/porchctl.md" %}}).

## Key Concepts

- **Independent subpackage**: A kpt package nested in a subdirectory of a parent package that maintains its own
  upstream tracking via its `Kptfile`. See [the kpt package documentation](https://kpt.dev/book/03-packages) for
  a full description of independent subpackages
- **Parent package revision**: The Draft package revision into which subpackages are cloned or upgraded.
- **Subpackage directory**: The relative path within the parent package where the subpackage resides.

Unlike a regular clone (which creates a new package revision), a subpackage clone adds content into an existing
Draft package revision. Similarly, a subpackage upgrade modifies the parent package revision in-place rather than
creating a new one.

Note that **Subpackage directory** paths must follow the
[rules described on the subpackage page]({{% relref "/docs/2_concepts/subpackages/#subpackage-naming" %}}).

## Prerequisites

Before following this guide, ensure you have:

- A Kubernetes cluster running
- `porchctl` installed
- At least one repository registered with published upstream packages to clone from
- A Draft package revision that will serve as the parent package

{{% alert title="Important" color="warning" %}}
Subpackage operations require the parent package revision to be in **Draft** state.
{{% /alert %}}

 Subpackage operations may be performed on a freshly created draft before any other modifications are pushed to it or on
 a draft to which other modifications have already been pushed. Multiple subpackages may be cloned and upgraded on a draft
 one after another.

## End-to-End Example

This example demonstrates:
1. Creating a parent package
2. Cloning an upstream package into it as an independent subpackage
3. Publishing the parent package
4. Upgrading the subpackage when a new upstream version is available

### Step 1: Set Up Upstream Blueprints

First, ensure you have published upstream packages to clone from. In this example we assume two published
blueprint revisions exist:

```bash
$ porchctl rpkg get --namespace=porch-demo --name=networking
NAME                              PACKAGE       WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
blueprints.networking.main        networking    main            -1         false    Published   blueprints
blueprints.networking.v1          networking    v1              1          false    Published   blueprints
blueprints.networking.v2          networking    v2              2          true     Published   blueprints
```

### Step 2: Create a Parent Package Draft

Create a new package that will contain the subpackage:

```bash
# Initialize a parent package
porchctl rpkg init my-composed-app --namespace=porch-demo --repository=deployments --workspace=v1
```

Verify the draft was created:

```bash
$ porchctl rpkg get --namespace=porch-demo --name=my-composed-app
NAME                                 PACKAGE           WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
deployments.my-composed-app.v1       my-composed-app   v1              0          false    Draft       deployments
```

### Step 3: Clone a Subpackage into the Parent

Clone the upstream `networking` blueprint into a subdirectory of the parent package:

```bash
porchctl rpkg clone \
  blueprints.networking.v1 \
  deployments.my-composed-app.v1 \
  --subpackage-dir=components/networking \
  --namespace=porch-demo
```

This clones the contents of `blueprints.networking.v1` into the `components/networking` directory within the
parent package revision `deployments.my-composed-app.v1`.

Expected output:

```
subpackage cloned into directory "components/networking" in package revision "deployments.my-composed-app.v1"
```

You can verify by pulling the parent package locally:

```bash
porchctl rpkg pull deployments.my-composed-app.v1 ./my-composed-app --namespace=porch-demo
```

The directory structure will include:

```
my-composed-app/
├── Kptfile
├── package-context.yaml
└── components/
    └── networking/
        ├── Kptfile          # Subpackage's own Kptfile with upstream info
        └── *.yaml           # Subpackage resources
```

### Step 4: Publish the Parent Package

Once you're satisfied with the composed package, propose and approve it:

```bash
porchctl rpkg propose deployments.my-composed-app.v1 --namespace=porch-demo
porchctl rpkg approve deployments.my-composed-app.v1 --namespace=porch-demo
```

### Step 5: Upgrade the Subpackage

When a new upstream version (`blueprints.networking.v2`) becomes available, you can upgrade the subpackage.
First, create a new draft of the parent package:

```bash
porchctl rpkg copy deployments.my-composed-app.v1 --namespace=porch-demo --workspace=v2
```

Then upgrade the subpackage within the new draft:

```bash
porchctl rpkg upgrade deployments.my-composed-app.v2 \
  --subpackage-dir=components/networking \
  --revision=2 \
  --namespace=porch-demo
```

Expected output:

```
independent subpackage in directory "components/networking" in package "deployments.my-composed-app.v2" upgraded
```

The subpackage at `components/networking` is now upgraded to the contents of `blueprints.networking.v2`, merged
with any local customizations using the chosen strategy (default: `resource-merge`).

Finally, publish the upgraded parent:

```bash
porchctl rpkg propose deployments.my-composed-app.v2 --namespace=porch-demo
porchctl rpkg approve deployments.my-composed-app.v2 --namespace=porch-demo
```

## Subpackage Clone in Detail

### Usage

```bash
porchctl rpkg clone SOURCE_PACKAGE PARENT_PACKAGE_REVISION \
  --subpackage-dir=<path> \
  --namespace=<namespace>
```

When `--subpackage-dir` is specified:

- `SOURCE_PACKAGE` is the upstream package revision to clone (e.g., `blueprints.networking.v1`)
- `PARENT_PACKAGE_REVISION` (the `NAME` argument) is the existing Draft package revision that will receive the subpackage
- `--repository` and `--workspace` must **not** be specified
- The subdirectory must **not already exist** in the parent package

### Constraints

| Requirement | Reason |
|-------------|--------|
| Parent must be in Draft state | Subpackage clone modifies an existing package revision |
| Subdirectory must not exist | Prevents overwriting existing content |
| Path must be relative (no leading or trailing `/`, no `./` or `..`) | Ensures subpackage stays within the parent package tree |

## Subpackage Upgrade in Detail

### Usage

```bash
porchctl rpkg upgrade PARENT_PACKAGE_REVISION \
  --subpackage-dir=<path> \
  [--revision=<number>] \
  [--strategy=<strategy>] \
  --namespace=<namespace>
```

When `--subpackage-dir` is specified:

- `PARENT_PACKAGE_REVISION` is the Draft package revision containing the subpackage
- `--workspace` must **not** be specified (the upgrade modifies the parent in-place)
- The subdirectory **must already exist** and contain a valid `Kptfile` with upstream information
- `--revision` specifies the upstream revision to upgrade to (if omitted, upgrades to latest)
- `--strategy` controls the merge behaviour (default: `resource-merge`)

### How Porch Determines the Upstream

During a subpackage upgrade, Porch:

1. Reads the `Kptfile` at the specified subdirectory within the parent package
2. Extracts the upstream Git repository, package name, and current revision from the `Kptfile`
3. Finds the matching registered repository in Porch
4. Locates the old upstream package revision (current version) and the new upstream package revision (target version)
5. Performs a merge of the new upstream into the subpackage directory

### Constraints

| Requirement | Reason |
|-------------|--------|
| Parent must be in Draft state | Subpackage upgrade modifies an existing package revision |
| Subdirectory must exist with a valid Kptfile | The subpackage's upstream info is read from the Kptfile |
| Upstream repository must be registered in Porch | Porch needs to resolve package revisions for the merge |
| `--workspace` must not be set | The upgrade operates in-place on the parent, not creating a new package revision |

## Merge Strategies for Subpackage Upgrades

The same merge strategies available for regular upgrades apply to subpackage upgrades:

| Strategy | Behaviour |
|----------|-----------|
| `resource-merge` (default) | Structural 3-way merge preserving local customizations |
| `copy-merge` | Upstream files overwrite local; local-only files are preserved |
| `force-delete-replace` | Completely replaces subpackage contents with upstream |
| `fast-forward` | Fails if local modifications exist |

Example with a specific strategy:

```bash
porchctl rpkg upgrade deployments.my-composed-app.v2 \
  --subpackage-dir=components/networking \
  --revision=2 \
  --strategy=copy-merge \
  --namespace=porch-demo
```

## Troubleshooting

**Clone fails with "parent package must be in state draft"?**

- Ensure the target parent package revision is in Draft state
- If it's Published, create a new draft with `porchctl rpkg copy` first

**Clone fails with "invalid --subpackage-dir"?**

- The path must be a relative directory without leading `/`, `./`, or `..` segments
- Example valid paths: `components/networking`, `subpkgs/monitoring`, `infra`

**Upgrade fails with "could not find Kptfile for independent subpackage"?**

- Verify the subdirectory exists in the parent package and contains a `Kptfile`
- Pull the parent package locally to inspect: `porchctl rpkg pull <parent> ./dir --namespace=<ns>`

**Upgrade fails with "subpackage is not managed by kpt and cannot be upgraded"?**

- The subpackage's `Kptfile` does not have valid upstream Git information
- The subpackage may have been manually created rather than cloned via Porch

**Upgrade fails with "could not find repository"?**

- The upstream Git repository referenced in the subpackage's `Kptfile` is not registered in Porch
- Register the upstream repository with `porchctl repo register`

**"--workspace may not be specified on subpackage upgrades/clones"?**

- Remove the `--workspace` flag; subpackage operations modify the parent in-place

**"--repository may not be specified on subpackage clones"?**

- Remove the `--repository` flag; the target is the parent package revision, not a repository
