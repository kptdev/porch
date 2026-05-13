---
title: Porch
description: Kubernetes-native package orchestration for KRM configuration packages
menu: {main: {weight: 10}}
---
{{< blocks/cover title="Porch" height="auto">}}
<a class="btn btn-lg btn-primary me-3 mb-4" href="/docs">
  Documentation <i class="fas fa-arrow-alt-circle-right ms-2"></i>
</a>
<a class="btn btn-lg btn-secondary me-3 mb-4" href="https://github.com/kptdev/porch">
  GitHub <i class="fab fa-github ms-2 "></i>
</a>
<a class="btn btn-lg btn-primary me-3 mb-4" href="/docs/3_getting_started/">
  Install Porch <i class="fas fa-download ms-2"></i>
</a>

<p class="lead mt-5">Kubernetes-native API for managing KRM configuration packages</p>

{{< /blocks/cover >}}

{{% blocks/lead color="primary" %}}
Porch is a Kubernetes extension apiserver that manages the lifecycle of KRM configuration packages in Git repositories.
It provides package operations through Kubernetes resources, enabling GitOps workflows with approval gates, automation, and collaboration.
{{% /blocks/lead %}}

{{% blocks/section type="row" %}}

{{% blocks/feature icon="fab fa-git-alt" title="GitOps Native" %}}
All package changes are committed to Git with full history. Works seamlessly with Config Sync, Flux, and other GitOps tools.
{{% /blocks/feature %}}

{{% blocks/feature icon="fas fa-check-circle" title="Approval Workflows" %}}
Packages move through lifecycle stages (Draft → Proposed → Published → DeletionProposed) with explicit approval gates to prevent accidental changes.
{{% /blocks/feature %}}

{{% blocks/feature icon="fas fa-cube" title="Standard kpt Packages" %}}
Manages standard kpt packages with no vendor lock-in. Packages can be edited through Porch or directly in Git.
{{% /blocks/feature %}}

{{% /blocks/section %}}

{{% blocks/section type="row" %}}

{{% blocks/feature icon="fas fa-code-branch" title="Package Cloning & Upgrades" %}}
Clone packages from upstream sources and automatically upgrade when new versions are published. Three-way merge handles local customizations.
{{% /blocks/feature %}}

{{% blocks/feature icon="fas fa-cogs" title="Function Execution" %}}
Apply krm functions to transform and validate packages. Functions run in isolated containers with results tracked in package history.
{{% /blocks/feature %}}

{{% blocks/feature icon="fas fa-sync" title="Multi-Repository" %}}
Manage packages across multiple Git repositories from a single control plane. Controllers automate cross-repository operations.
{{% /blocks/feature %}}

{{% /blocks/section %}}

{{% blocks/section color="primary" %}}

## Part of the kpt Project

Porch is maintained by the [kpt](htps://kpt.dev) community and continues to evolve as a key component for configuration-as-data workflows.

<a class="btn btn-lg btn-secondary me-3 mb-4" href="https://kpt.dev/">
  Learn about kpt <i class="fas fa-arrow-alt-circle-right ms-2"></i>
</a>

{{% /blocks/section %}}
