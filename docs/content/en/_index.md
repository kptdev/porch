---
title: Porch
description: Kubernetes-native package orchestration for KRM configuration packages
menu: {main: {weight: 50}}
toc_hide: true
---


<div class="row mt-5 mb-3">
    <div class="col-lg-6">
        <div class="lead">
{{< site_description >}}
        </div>
    </div>
    <div class="col-lg-6 text-center td-home-hero-logo">
        <img src="/images/kpt_stacked_color.svg" alt="kpt logo" style="max-width: 300px;">
    </div>
</div>

{{% blocks/section type="row" color="white"%}}

{{% blocks/feature icon="fas fa-download" title="Install" %}}
Get started by [installing](/docs/3_getting_started/) Porch.
{{% /blocks/feature %}}
{{% blocks/feature icon="fas fa-graduation-cap" title="Learn" %}}
Read the [Documentation](/docs/).
{{% /blocks/feature %}}
{{% blocks/feature icon="fas fa-info-circle" title="Concepts" %}}
Understand [core concepts](/docs/2_concepts/) like packages, repositories, and lifecycle.
{{% /blocks/feature %}}
{{% blocks/feature icon="fas fa-briefcase" title="Contribute" %}}
Porch is open source — [contribute on GitHub](https://github.com/kptdev/porch)
{{% /blocks/feature %}}

{{% /blocks/section %}}

# Key Concepts

{{% blocks/section type="row" color="white"%}}

{{% blocks/feature icon="fab fa-git-alt" title="GitOps Native" %}}
All package changes are committed to Git with full history. Works seamlessly with Config Sync, Flux, and other GitOps tools.
{{% /blocks/feature %}}
{{% blocks/feature icon="fas fa-check-circle" title="Approval Workflows" %}}
Packages move through lifecycle stages (Draft → Proposed → Published → DeletionProposed) with explicit approval gates.
{{% /blocks/feature %}}
{{% blocks/feature icon="fas fa-cube" title="Standard kpt Packages" %}}
Manages standard kpt packages with no vendor lock-in. Packages can be edited through Porch or directly in Git.
{{% /blocks/feature %}}
{{% blocks/feature icon="fas fa-code-branch" title="Package Cloning & Upgrades" %}}
Clone packages from upstream sources and automatically upgrade when new versions are published. Three-way merge handles local customizations.
{{% /blocks/feature %}}
{{% blocks/feature icon="fas fa-cogs" title="Function Execution" %}}
Apply KRM functions to transform and validate packages. Functions run in isolated containers with results tracked in package history.
{{% /blocks/feature %}}
{{% blocks/feature icon="fas fa-sync" title="Multi-Repository" %}}
Manage packages across multiple Git repositories from a single control plane. Controllers automate cross-repository operations.
{{% /blocks/feature %}}

{{% /blocks/section %}}

{{% blocks/section color="white" %}}

# Part of the kpt Project

Porch is maintained by the [kpt](https://kpt.dev) community and continues to evolve as a key component for configuration-as-data workflows.

{{% /blocks/section %}}

# Communication

{{% blocks/section type="row" color="white"%}}

{{% blocks/feature icon="fab fa-slack" title="Slack" %}}

Join us in the [#kpt](https://kubernetes.slack.com/archives/C0155NSPJSZ) channel in the [Kubernetes Slack](https://communityinviter.com/apps/kubernetes/community)!

{{% /blocks/feature %}}
{{% blocks/feature icon="fas fa-comments" title="Discussions" %}}

Join the discussions in the [kptdev/kpt](https://github.com/kptdev/kpt/discussions) repo.

{{% /blocks/feature %}}

{{% blocks/feature icon="fas fa-people-group" title="Community Meeting" %}}

Participate in our [community meetings](https://zoom-lfx.platform.linuxfoundation.org/meeting/98980817322?password=c09cdcc7-59c0-49c4-9802-ad4d50faafcd&invite=true)

{{% /blocks/feature %}}

{{% /blocks/section %}}

{{% blocks/lead color="white" %}}
kpt is a [Cloud Native Computing Foundation (CNCF)](https://www.cncf.io/) [Sandbox Project](https://www.cncf.io/sandbox-projects/)!

<img src="/images/cncf-color.svg" alt="CNCF logo" style="max-width: 600px;">

{{% /blocks/lead %}}
