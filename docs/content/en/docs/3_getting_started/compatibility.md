---
title: "Compatibility & Requirements"
type: docs
weight: 1
description: "Version compatibility matrices for Porch server, API module, and dependencies."
---

{{% alert color="info" title="Documentation Note" %}}
This documentation covers features from the **latest development branch** ({{< params "version_porch_server_dev" >}} + api {{< params "version_porch_api_dev" >}}). For production deployments, use the **stable versions** listed below.
{{% /alert %}}

## Porch Server & API Module Compatibility

The Porch API module (`github.com/kptdev/porch/api`) is versioned independently from the Porch server. Use this matrix to ensure version compatibility when integrating with or using Porch:

### Stable (Production Ready)

| Porch Server | Recommended API Module | Release Date | Status |
|---|---|---|---|
| [v1.6.0](https://github.com/nephio-project/porch/releases/tag/v1.6.0) | v1.0.0 | 2026-06-19 | Stable |
| [v1.6.1](https://github.com/nephio-project/porch/releases/tag/v1.6.1) | v1.0.0 | 2026-07-10 | Stable |
| [v1.6.2](https://github.com/nephio-project/porch/releases/tag/v1.6.2) | v1.0.1 | 2026-07-25 | Stable |
| [v{{< params "version_porch_server" >}}](https://github.com/nephio-project/porch/releases/tag/v{{< params "version_porch_server" >}}) | **{{< params "version_porch_api" >}}** (recommended) | 2026-07-30 | **Latest Stable** |

### In Development (Main Branch)

| Porch Server | Recommended API Module | Expected Release | Status |
|---|---|---|---|
| [v{{< params "version_porch_server_dev" >}}](https://github.com/nephio-project/porch/releases/tag/v{{< params "version_porch_server_dev" >}}) | {{< params "version_porch_api_dev" >}} | TBD | Pre-release |

*These versions are from the main development branch and are documented here. For production use, stick with the stable versions above.*

### API Module Release History

| API Version | Release Date | Status | Key Changes |
|---|---|---|---|
| [v1.0.0](https://github.com/nephio-project/porch/releases/tag/api/v1.0.0) | 2026-06-23 | Stable | Initial API module separation |
| [v1.0.1](https://github.com/nephio-project/porch/releases/tag/api/v1.0.1) | 2026-07-13 | **Latest Stable** | Modernize Go idioms and remove outdated patterns |
| [v1.0.2](https://github.com/nephio-project/porch/releases/tag/api/v1.0.2) | 2026-09-25 | Pre-release | Bump kpt api version in porch api |
| [v1.0.3](https://github.com/nephio-project/porch/releases/tag/api/v1.0.3) | 2026-09-29 | Pre-release | Workaround fix for package name in kpt render |

**Breaking Changes:** None. All API module versions v1.0.x are compatible patch releases with no breaking changes.

## Supported Infrastructure Versions

| Component | Minimum Version | Latest Tested | Notes |
|---|---|---|---|
| Kubernetes | {{< params "version_kube_min" >}} | {{< params "version_kube_latest" >}} | Required for aggregated API server features |
| Go (for building from source) | {{< params "version_go_min_stable" >}} | {{< params "version_go" >}} | Required only for development |
| Docker | — | {{< params "version_docker" >}} | Required for kind/minikube with Docker driver |
| kpt CLI | {{< params "version_kpt_min" >}} | {{< params "version_kpt" >}} | Required for deploying Porch packages |
| kubectl | {{< params "version_kube_min" >}} | {{< params "version_kube_latest" >}} | Must match or be newer than cluster version |
| git | {{< params "version_git_min" >}} | {{< params "version_git" >}} | Required for Git repository operations |

{{% alert color="primary" title="Note" %}}
The "Latest Tested" versions above are the versions Porch is regularly tested against. Porch may work with other versions outside this range, but compatibility is not guaranteed.
{{% /alert %}}

## Go Version Support for Development

If building Porch from source:

| Porch Version | Minimum Go | Maximum Go | Recommended Go |
|---|---|---|---|
| v1.6.0 - {{< params "version_porch_server" >}} | {{< params "version_go_min_stable" >}} | {{< params "version_go_max_stable" >}} | {{< params "version_go_max_stable" >}} |
| {{< params "version_porch_server_dev" >}} (main) | {{< params "version_go_min_dev" >}} | {{< params "version_go_max_dev" >}}+ | {{< params "version_go" >}} |

## Kubernetes Cluster Support

Porch is tested and verified to run on:

- **Local clusters**: kind (tested with {{< params "version_kind" >}}) running Kubernetes {{< params "version_kube" >}}
- **Managed Kubernetes services**: Likely compatible with GKE, EKS, AKS (but not regularly tested)

{{% alert color="warning" title="Compatibility Note" %}}
Porch's primary test environment is kind with Kubernetes {{< params "version_kube" >}}. While Porch should work on other Kubernetes distributions and versions meeting the minimum version requirement ({{< params "version_kube_min" >}}+), compatibility outside the tested environment is not formally verified. If you encounter issues on a different platform or version, please report them.
{{% /alert %}}

## Cache Backend Compatibility

Porch supports two cache backends for storing package metadata:

| Cache Backend | Storage | Porch Version Support | Backup Recovery |
|---|---|---|---|
| **CR Cache** | Kubernetes custom resources (etcd) | v1.0+; safe across Porch versions | Restore from Kubernetes backup; version-agnostic |
| **DB Cache** | PostgreSQL database | v1.6+; single version only | Restore only into same Porch version |

{{% alert color="warning" title="DB Cache Backup Requirement" %}}
When using the DB Cache backend, PostgreSQL backups **must be restored into a Porch installation of the same version**. Cross-version restoration is not supported and will cause data corruption.
{{% /alert %}}

## Migration Path: v1alpha1 to v1alpha2

Porch is transitioning from an aggregated API model (v1alpha1) to a CRD-based controller model (v1alpha2). The aggregated API server remains for serving PackageRevisionResources content.

| Component | Status | Timeline |
|---|---|---|
| v1alpha1 API (aggregated) | Supported | Kept for backward compatibility; no EOL date announced |
| v1alpha2 CRD-based PackageRevisions | In development | Progressive rollout; per-repo opt-in via annotation |

**Migration is optional and per-repository.** You can opt-in to v1alpha2 when ready by adding an annotation to your Repository. For details, see [Working with CRD-Based PackageRevisions]({{% relref "/docs/4_tutorials_and_how-tos/working_with_crd_based_packagerevisions" %}}).
