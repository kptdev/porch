---
title: "Upgrading Package Revisions"
type: docs
weight: 6
description: "A guide to upgrade package revisions using Porch and porchctl"
---

The package upgrade feature in Porch is a powerful mechanism for keeping deployed packages (downstream) up-to-date with their source blueprints (upstream). This guide walks through the entire workflow, from creating packages to performing an upgrade, with a special focus on the different upgrade scenarios and merge strategies.

For detailed command reference, see the [porchctl CLI guide]({{% relref "/docs/7_cli_api/porchctl.md" %}}).

## Key Concepts

To understand the upgrade process, it's essential to be familiar with the three states of a package during a merge operation:

*   **Original:** The state of the package when it was first cloned from the blueprint (e.g., `Blueprint v1`). This serves as the common ancestor for the merge.
*   **Upstream:** The new, updated version of the source blueprint (e.g., `Blueprint v2`). This contains the changes you want to incorporate.
*   **Local:** The current state of your deployment package, including any customizations you have applied since it was cloned.

The upgrade process combines changes from the **Upstream** blueprint with your **Local** customizations, using the **Original** version as a base to resolve differences.

## End-to-End Upgrade Example

This example demonstrates the complete process of creating, customizing, and upgrading a package.

### Step 1: Create a Base Blueprint Package (revision 1)

Create the initial revision of our blueprint. This will be the "upstream" source for our deployment package.

```bash
# Initialize a new package draft named 'blueprint' in the 'porch-test' repository
porchctl rpkg init blueprint --namespace=porch-demo --repository=porch-test --workspace=1

# Propose the draft for review
porchctl rpkg propose porch-test.blueprint.1 --namespace=porch-demo

# Approve and publish the package, making it available as revision 1
porchctl rpkg approve porch-test.blueprint.1 --namespace=porch-demo
```

![Step 1: Create Base Blueprint](/static/images/porch/upgrade-step1.drawio.svg)

PackageRevisions state after step 1:
```bash
$ porchctl rpkg get --namespace=porch-demo
NAME                        PACKAGE     WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.blueprint.main   blueprint   main            -1         false    Published   porch-test
porch-test.blueprint.1      blueprint   1               1          true     Published   porch-test
```

### Step 2: Create a New Blueprint Package Revision (revision 2)

Create a new revision of the blueprint to simulate an update. In this case, we add a new ConfigMap.

```bash
# Create a new draft (v2) by copying v1
porchctl rpkg copy porch-test.blueprint.1 --namespace=porch-demo --workspace=2

# Pull the contents of the new draft locally to make changes
porchctl rpkg pull porch-test.blueprint.2 --namespace=porch-demo ./tmp/blueprint-v2

# Add a new resource file to the package
kubectl create configmap test-cm --dry-run=client -o yaml > ./tmp/blueprint-v2/new-configmap.yaml

# Push the local changes back to the Porch draft
porchctl rpkg push porch-test.blueprint.2 --namespace=porch-demo ./tmp/blueprint-v2

# Propose and approve the new version
porchctl rpkg propose porch-test.blueprint.2 --namespace=porch-demo
porchctl rpkg approve porch-test.blueprint.2 --namespace=porch-demo
```
At this point, we have two published blueprint versions: `v1` (the original) and `v2` (with the new ConfigMap).

![Step 2: Create New Blueprint Revision](/static/images/porch/upgrade-step2.drawio.svg)

PackageRevisions state after step 2:
```bash
$ porchctl rpkg get --namespace=porch-demo
NAME                        PACKAGE     WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.blueprint.main   blueprint   main            -1         false    Published   porch-test
porch-test.blueprint.1      blueprint   1               1          false    Published   porch-test
porch-test.blueprint.2      blueprint   2               2          true     Published   porch-test
```

### Step 3: Clone Blueprint revision 1 into a Deployment Package

Clone the blueprint to create a "downstream" deployment package.

```bash
# Clone blueprint v1 to create a new deployment package
porchctl rpkg clone porch-test.blueprint.1 --namespace=porch-demo --repository=porch-test --workspace=1 deployment

# Pull the new deployment package locally to apply customizations
porchctl rpkg pull porch-test.deployment.1 --namespace=porch-demo ./tmp/deployment-v1

# Apply a local customization (e.g., add an annotation to the Kptfile)
kpt fn eval --image ghcr.io/kptdev/krm-functions-catalog/set-annotations:latest ./tmp/deployment-v1/Kptfile -- kpt.dev/annotation=true

# Push the local changes back to Porch
porchctl rpkg push porch-test.deployment.1 --namespace=porch-demo ./tmp/deployment-v1

# Propose and approve the deployment package
porchctl rpkg propose porch-test.deployment.1 --namespace=porch-demo
porchctl rpkg approve porch-test.deployment.1 --namespace=porch-demo
```

![Step 3: Clone Blueprint into Deployment Package](/static/images/porch/upgrade-step3.drawio.svg)

PackageRevisions state after step 3:
```bash
$ porchctl rpkg get --namespace=porch-demo
NAME                         PACKAGE      WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.blueprint.main    blueprint    main            -1         false    Published   porch-test
porch-test.blueprint.1       blueprint    1               1          false    Published   porch-test
porch-test.blueprint.2       blueprint    2               2          true     Published   porch-test
porch-test.deployment.main   deployment   main            -1         false    Published   porch-test
porch-test.deployment.1      deployment   1               1          true     Published   porch-test
```

### Step 4: Discover and Perform the Upgrade

Our deployment package is based on `blueprint.1`, but we know `blueprint.2` is available. We can discover and apply this upgrade.

```bash
# Discover available upgrades for packages cloned from 'upstream' repositories
porchctl rpkg upgrade --discover=upstream
# This will list 'porch-test.deployment.1' as having an available upgrade to revision 2.

# Upgrade the deployment package to revision 2 of its upstream blueprint
# This creates a new draft package: 'porch-test.deployment.2'
porchctl rpkg upgrade porch-test.deployment.1 --namespace=porch-demo --revision=2 --workspace=2

# Propose and approve the upgraded package
porchctl rpkg propose porch-test.deployment.2 --namespace=porch-demo
porchctl rpkg approve porch-test.deployment.2 --namespace=porch-demo
```

After approval, `porch-test.deployment.2` is the new, published deployment package. It now contains the `new-configmap.yaml` from the upstream `blueprint.2` and the local `kpt.dev/annotation=true` customization applied in Step 3.

![Step 4: Discover and Perform Upgrade](/static/images/porch/upgrade-step4.drawio.svg)

PackageRevisions state after step 4:
```bash
$ porchctl rpkg get --namespace=porch-demo
NAME                         PACKAGE      WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.blueprint.main    blueprint    main            -1         false    Published   porch-test
porch-test.blueprint.1       blueprint    1               1          false    Published   porch-test
porch-test.blueprint.2       blueprint    2               2          true     Published   porch-test
porch-test.deployment.main   deployment   main            -1         false    Published   porch-test
porch-test.deployment.1      deployment   1               1          false    Published   porch-test
porch-test.deployment.2      deployment   2               2          true     Published   porch-test
```

## Understanding Merge Strategies

![Package Upgrade Flow](/static/images/porch/upgrade.drawio.svg)

**Schema Explanation:**
The diagram above illustrates the package upgrade workflow in Porch:

1. **CLONE**: A deployment package (Deployment.v1) is initially cloned from a blueprint (Blueprint.v1) in the blueprints repository
2. **COPY**: The blueprint evolves to a new version (Blueprint.v2) with additional features or fixes
3. **UPGRADE**: The deployment package is upgraded to incorporate changes from the new blueprint version, creating Deployment.v2

The dashed line shows the relationship between the new blueprint version and the upgrade process, indicating that the upgrade "uses the new blueprint" as its source for changes.

The outcome of an upgrade depends on the changes made in the upstream blueprint and the local deployment package, combined with the chosen merge strategy. You can specify a strategy using the `--strategy` flag (e.g., `porchctl rpkg upgrade ... --strategy=copy-merge`).

### Merge Strategy Comparison

<table class="table" style="border: 2px solid var(--bs-body-color);">
  <thead>
    <tr>
      <th scope="col" style="border-bottom: 2px solid var(--bs-body-color);border-right: 2px solid var(--bs-body-color);"><strong>Scenario</strong></th>
      <th scope="col" style="border-bottom: 2px solid var(--bs-body-color);border-right: 2px solid var(--bs-body-color)">resource-merge (Default)</th>
      <th scope="col" style="border-bottom: 2px solid var(--bs-body-color);border-right: 2px solid var(--bs-body-color)">copy-merge</th>
      <th scope="col" style="border-bottom: 2px solid var(--bs-body-color);border-right: 2px solid var(--bs-body-color)">force-delete-replace</th>
      <th scope="col" style="border-bottom: 2px solid var(--bs-body-color);">fast-forward</th>
    </tr>
  </thead>
  <tbody>
    <tr>
      <th scope="row" style="border-right: 2px solid var(--bs-body-color);"><strong>File added in Upstream</strong></th>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">File is added to Local.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">File is added to Local.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">File is added to Local.</td>
      <td scope="row">Fails (Local must be unchanged).</td>
    </tr>
    <tr>
      <th scope="row" style="border-right: 2px solid var(--bs-body-color);"><strong>File modified in Upstream only</strong></th>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Changes are applied to Local.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Upstream file overwrites Local file.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Upstream file overwrites Local file.</td>
      <td scope="row">Fails (Local must be unchanged).</td>
    </tr>
    <tr>
      <th scope="row" style="border-right: 2px solid var(--bs-body-color);"><strong>File modified in Local only</strong></th>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Local changes are kept.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Upstream file overwrites Local file.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Local changes are discarded; Upstream version is used.</td>
      <td scope="row">Fails (Local must be unchanged).</td>
    </tr>
    <tr>
      <th scope="row" style="border-right: 2px solid var(--bs-body-color);"><strong>File modified in both (no conflict)</strong></th>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Both changes are merged.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Upstream file overwrites Local file.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Upstream file overwrites Local file.</td>
      <td scope="row">Fails (Local must be unchanged).</td>
    </tr>
    <tr>
      <th scope="row" style="border-right: 2px solid var(--bs-body-color);"><strong>File modified in both (conflict)</strong></th>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Merge autoconflic resolution: always choose the new upstream version.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Upstream file overwrites Local file.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Upstream file overwrites Local file.</td>
      <td scope="row">Fails (Local must be unchanged).</td>
    </tr>
    <tr>
      <th scope="row" style="border-right: 2px solid var(--bs-body-color);"><strong>File deleted in Upstream</strong></th>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">File is deleted from Local.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">File is deleted from Local.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">File is deleted from Local.</td>
      <td scope="row">Fails (Local must be unchanged).</td>
    </tr>
    <tr>
      <th scope="row" style="border-right: 2px solid var(--bs-body-color);"><strong>File deleted in Upstream but modified in Local</strong></th>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Local changes are kept (deletion is not applied).</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">File is deleted from Local.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">File is deleted from Local.</td>
      <td scope="row">Fails (Local must be unchanged).</td>
    </tr>
    <tr>
      <th scope="row" style="border-right: 2px solid var(--bs-body-color);"><strong>File added only in Local (never in Upstream)</strong></th>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">File is kept.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">File is kept.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">File is discarded.</td>
      <td scope="row">Fails (Local must be unchanged).</td>
    </tr>
    <tr>
      <th scope="row" style="border-right: 2px solid var(--bs-body-color);"><strong>Local package is unmodified</strong></th>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Upgrade succeeds.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Upgrade succeeds.</td>
      <td scope="row" style="border-right: 1px solid var(--bs-body-color);">Upgrade succeeds.</td>
      <td scope="row">Upgrade succeeds.</td>
    </tr>
  </tbody>
</table>

### Detailed Strategy Explanations

#### **resource-merge (Default)**
This is a structural 3-way merge designed for Kubernetes resources. It understands the structure of YAML files and attempts to intelligently merge changes from the upstream and local packages. This is the **recommended default strategy** for managing Kubernetes configuration. Use it when you want to preserve local customizations while incorporating upstream updates.

#### **copy-merge**
A file-level replacement strategy. For any file present in both local and upstream, the upstream version is used, overwriting local changes. Use copy-merge when you trust the upstream source more than local changes or when Porch cannot parse the file contents (e.g., non-KRM files).

Only files that were **added purely locally** (i.e., never existed in the original upstream the package was cloned from) are kept. This distinction matters when upstream deletes a file:

* A file that originally came from upstream and was later **modified locally** is still treated as upstream-owned. If upstream deletes it, it is **deleted from local**, and the local modifications are lost.
* A file that was **added locally** and never existed upstream is **kept**, even if upstream deletes the directory that now contains it. In that case, the containing directory is preserved too (only the upstream-owned files inside it are removed).

If you need to preserve local modifications to upstream-owned files across an upgrade, use the `resource-merge` strategy instead.

#### **force-delete-replace**
The most aggressive strategy. It completely discards the local package's contents and replaces them with the contents of the new upstream package. Used to completely reset a deployment package to a new blueprint version, abandoning all previous customizations.

#### **fast-forward**
A fail-fast safety check. The upgrade only succeeds if the local package has **zero modifications** compared to the original blueprint version it was cloned from. Use fast-forward to guarantee that you are only upgrading unmodified packages, preventing accidental overwrites of important local customizations.

## Practical examples: upgrade strategies in action

This section contains short, focused examples showing how each merge strategy behaves in realistic scenarios. Each example assumes you have a deployment package `porch-test.deployment.1` cloned from `porch-test.blueprint.1` and that `porch-test.blueprint.2` is available upstream.

### Example A — resource-merge (default)

Scenario: Upstream adds a new ConfigMap and local changes added an annotation to Kptfile. `resource-merge` should apply the upstream addition while preserving the local annotation.

```bash
# discover available upgrades
porchctl rpkg upgrade --discover=upstream
```

```bash
# perform upgrade using the default strategy
porchctl rpkg upgrade porch-test.deployment.1 --namespace=porch-demo --revision=2 --workspace=2
```

Outcome: A new draft `porch-test.deployment.2` is created containing both the new `ConfigMap` and the local annotation.

### Example B — copy-merge

Scenario: Upstream changes a file that the local package also modified, but you want the upstream version to win (file-level overwrite).

```bash
porchctl rpkg upgrade porch-test.deployment.1 --namespace=porch-demo --revision=2 --workspace=2 --strategy=copy-merge
```

Outcome: Files present in both upstream and local are replaced with the upstream copy. Files only present locally are preserved.

### Example C — force-delete-replace

Scenario: The blueprint has diverged substantially; you want to reset the deployment package to exactly match upstream v2.

```bash
porchctl rpkg upgrade porch-test.deployment.1 --namespace=porch-demo --revision=2 --workspace=2 --strategy=force-delete-replace
```

Outcome: The new draft contains only the upstream contents; local customizations are discarded.

### Example D — fast-forward

Scenario: You want to ensure upgrades are only applied to unmodified, pristine clones.

```bash
porchctl rpkg upgrade porch-test.deployment.1 --namespace=porch-demo --revision=2 --workspace=2 --strategy=fast-forward
```

Outcome: The upgrade succeeds only if `porch-test.deployment.1` has no local modifications compared to the original clone. If local changes exist, the command fails and reports the modifications that prevented a fast-forward.

## Reference

### Command Flags

The `porchctl rpkg upgrade` command has several key flags:

*   `--workspace=<name>`: (Mandatory) The name for the new workspace where the upgraded package draft will be created.
*   `--revision=<number>`: (Optional) The specific revision number of the upstream package to upgrade to. If not specified, Porch will automatically use the latest published revision.
*   `--strategy=<strategy>`: (Optional) The merge strategy to use. Defaults to `resource-merge`. Options are `resource-merge`, `copy-merge`, `force-delete-replace`, `fast-forward`.

For more details, run `porchctl rpkg upgrade --help`.

### Best Practices

For better organization and access control, keep blueprint packages and deployment packages in separate Git repositories.

Before upgrading, be certain which merge strategy fits your use case to avoid accidentally losing important local customizations. When in doubt, the default `resource-merge` is the safest and most intelligent option.

Do not upgrade packages' placeholder/main package revisions or attempt to use them as upstream for an upgrade - this is not supported. Select defined package revisions instead - see [Core Concepts]({{% relref "/docs/2_concepts#core-concepts" %}}) for the rules which identify a placeholder package revision.

### Cleanup

To remove the packages created in this guide, you must first propose them for deletion and then perform the final deletion.

```bash
# Clean up local temporary directory used in these examples
rm -rf ./tmp

# Propose all packages for deletion
porchctl rpkg propose-delete porch-test.blueprint.1 porch-test.blueprint.2 porch-test.deployment.1 porch-test.deployment.2 porch-test.blueprint.main porch-test.deployment.main --namespace=porch-demo

# Delete the packages
porchctl rpkg delete porch-test.blueprint.1 porch-test.blueprint.2 porch-test.deployment.1 porch-test.deployment.2 porch-test.blueprint.main porch-test.deployment.main --namespace=porch-demo
```
