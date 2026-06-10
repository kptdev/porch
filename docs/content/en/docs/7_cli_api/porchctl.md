---
title: "CLI Reference"
type: docs
weight: 1
description: Command-line reference for porchctl
---

## Overview

`porchctl` is the command-line interface for interacting with Porch. It communicates with a Kubernetes API server that has Porch installed as an aggregated API server, allowing you to manage repository registrations and packages within those repositories.

## Installation

See [Installing porchctl]({{% relref "/docs/3_getting_started/installing-porchctl" %}}) for installation instructions.

## Global Flags

These flags are available for all porchctl commands:

| Flag | Description | Default |
|------|-------------|---------|
| `--kubeconfig string` | Path to kubeconfig file. Only required if out-of-cluster. Can also be set via `KUBECONFIG` environment variable. | |
| `--log-flush-frequency duration` | Maximum seconds between log flushes | `5s` |
| `--truncate-output` | Enable output truncation | `true` |
| `-v, --v Level` | Log level verbosity | |
| `-h, --help` | Help for porchctl | |

## Commands

- [repo](#repo) - Manage package repositories
- [rpkg](#rpkg) - Manage packages
- [completion](#completion) - Generate shell autocompletion
- [version](#version) - Print version information

---

## repo

Manage package repositories.

**Aliases:** `repo`, `repository`

### Subcommands

- [repo reg](#repo-reg) - Register a package repository
- [repo get](#repo-get) - List registered repositories
- [repo sync](#repo-sync) - Schedule one-time repository sync
- [repo unreg](#repo-unreg) - Unregister a repository

### Common Flags

These flags are available for all `repo` subcommands:

| Flag | Description |
|------|-------------|
| `--as-uid string` | UID to impersonate for the operation |
| `--cluster string` | Name of the kubeconfig cluster to use |
| `--context string` | Name of the kubeconfig context to use |
| `--disable-compression` | Opt-out of response compression |
| `-n, --namespace string` | Namespace scope for this request |
| `-s, --server string` | Address and port of Kubernetes API server |
| `--tls-server-name string` | Server name for certificate validation |
| `--user string` | Name of the kubeconfig user to use |

---

### repo reg

Register a package repository.

**Aliases:** `reg`, `register`

**Usage:**
```bash
porchctl repo reg REPOSITORY [flags]
```

**Arguments:**

- `REPOSITORY` - URI for the repository. Can be a Git repository URL.

**Flags:**

| Flag | Description | Default |
|------|-------------|---------|
| `--name string` | Repository name. If unspecified, uses last segment of URL | |
| `--description string` | Brief description of the repository | |
| `--branch string` | Git branch where finalized packages are committed | `main` |
| `--create-branch` | Create the package branch if it doesn't exist | `false` |
| `--directory string` | Directory within repository containing packages | `/` |
| `--deployment` | Mark as deployment repository (packages are deployment-ready) | `false` |
| `--sync-schedule string` | Cron expression for periodic sync (e.g., `*/10 * * * *`) | |
| `--repo-basic-username string` | Username for basic authentication | |
| `--repo-basic-password string` | Password/token for basic authentication | |
| `--repo-workload-identity` | Use workload identity for authentication | `false` |

**Examples:**

```bash
# Register a Git repository with auto-generated name
porchctl repo register https://github.com/platkrm/demo-blueprints.git --namespace=default

# Register a deployment repository with custom name
porchctl repo register https://github.com/platkrm/blueprints-deployment.git \
  --name=foo --deployment --namespace=bar

# Register with sync schedule (every 10 minutes)
porchctl repo register https://github.com/platkrm/blueprints-deployment.git \
  --name=foo --deployment --namespace=bar --sync-schedule="*/10 * * * *"
```

---

### repo get

List registered repositories.

**Aliases:** `get`, `ls`, `list`

**Usage:**
```bash
porchctl repo get [REPOSITORY_NAME] [flags]
```

**Arguments:**

- `REPOSITORY_NAME` - (Optional) Name of specific repository to show. If omitted, shows all repositories.

**Flags:**

| Flag | Description |
|------|-------------|
| `-A, --all-namespaces` | List repositories across all namespaces |
| `--allow-missing-template-keys` | Ignore template errors for missing fields | `true` |
| `-L, --label-columns strings` | Labels to display as columns |
| `--no-headers` | Don't print headers in output |
| `-o, --output string` | Output format: json, yaml, wide, custom-columns, etc. |
| `--show-kind` | Show resource type |
| `--show-labels` | Show all labels |
| `--show-managed-fields` | Keep managedFields in JSON/YAML output |
| `--sort-by string` | Sort by field (JSONPath expression) |
| `--template string` | Template for go-template output |

**Examples:**

```bash
# List all repositories in default namespace
porchctl repo get --namespace default

# Show specific repository
porchctl repo get foo --namespace bar

# List repositories across all namespaces
porchctl repo get -A
```

---

### repo sync

Schedule a one-time sync for registered repositories.

**Aliases:** `sync`, `reconcile`

**Usage:**
```bash
porchctl repo sync [REPOSITORY_NAME] [flags]
```

**Arguments:**

- `REPOSITORY_NAME` - (Optional) Name(s) of repositories to sync. Use `--all` to sync all repositories.

**Flags:**

| Flag | Description |
|------|-------------|
| `--all` | Sync all repositories in the namespace |
| `-A, --all-namespaces` | List repositories across all namespaces |
| `--run-once string` | Schedule sync using duration (e.g., `10m`, `2h30m`) or RFC3339 timestamp. Minimum: 1 minute | `1m` |
| `-o, --output string` | Output format |

**Examples:**

```bash
# Sync all repositories after 1 minute (default)
porchctl repo sync --all --namespace default

# Sync specific repository after 1 minute
porchctl repo sync foo --namespace default

# Sync after 10 minutes
porchctl repo sync foo --namespace bar --run-once=10m

# Sync at specific time
porchctl repo sync foo1 foo2 --namespace bar --run-once=2025-09-16T14:00:00Z
```

---

### repo unreg

Unregister a repository.

**Aliases:** `unreg`, `unregister`

**Usage:**
```bash
porchctl repo unreg REPOSITORY [flags]
```

**Arguments:**

- `REPOSITORY` - Name of the repository to unregister.

**Flags:**

| Flag | Description | Default |
|------|-------------|---------|
| `--keep-auth-secret` | Keep the auth secret when unregistering | `false` |

**Examples:**

```bash
# Unregister repository and delete auth secret
porchctl repo unreg registered-repository --namespace=default

# Unregister but keep auth secret
porchctl repo unreg registered-repository --namespace=default --keep-auth-secret
```

---

## rpkg

Manage packages and package revisions.

**Aliases:** `rpkg`, `rpackage`

### Subcommands

- [rpkg init](#rpkg-init) - Initialize new package
- [rpkg clone](#rpkg-clone) - Clone existing package
- [rpkg copy](#rpkg-copy) - Create new revision from existing package
- [rpkg get](#rpkg-get) - List package revisions
- [rpkg pull](#rpkg-pull) - Pull package content locally
- [rpkg push](#rpkg-push) - Push local content to package
- [rpkg propose](#rpkg-propose) - Propose package for publication
- [rpkg approve](#rpkg-approve) - Approve proposed package
- [rpkg reject](#rpkg-reject) - Reject proposed package
- [rpkg del](#rpkg-del) - Delete package revision
- [rpkg propose-delete](#rpkg-propose-delete) - Propose deletion of published package
- [rpkg upgrade](#rpkg-upgrade) - Upgrade downstream package to newer upstream

### Common Flags

These flags are available for all `rpkg` subcommands:

| Flag | Description |
|------|-------------|
| `--as-uid string` | UID to impersonate for the operation |
| `--cluster string` | Name of the kubeconfig cluster to use |
| `--context string` | Name of the kubeconfig context to use |
| `--disable-compression` | Opt-out of response compression |
| `--kubeconfig string` | Path to kubeconfig file |
| `-n, --namespace string` | Namespace scope for this request |
| `-s, --server string` | Address and port of Kubernetes API server |
| `--tls-server-name string` | Server name for certificate validation |
| `--user string` | Name of the kubeconfig user to use |
| `-v, --v Level` | Log level verbosity |

---

### rpkg init

Initialize a new package revision in a repository.

**Usage:**
```bash
porchctl rpkg init PACKAGE_NAME [flags]
```

**Arguments:**

- `PACKAGE_NAME` - Name of the new package.

**Flags:**

| Flag | Description | Default |
|------|-------------|---------|
| `--repository string` | Repository where package will be created | (required) |
| `--workspace string` | Workspace name for the package | (required) |
| `--description string` | Short description of the package | `"sample description"` |
| `--keywords strings` | List of keywords for the package | |
| `--site string` | Link to page with package information | |

**Examples:**

```bash
# Create new package
porchctl rpkg init example-package-name \
  --repository=example-repository \
  --workspace=example-workspace \
  --namespace=example-namespace
```

---

### rpkg clone

Create a clone of an existing package revision.

**Usage:**
```bash
porchctl rpkg clone SOURCE_PACKAGE NAME [flags]
```

**Arguments:**

- `SOURCE_PACKAGE` - Source package to clone. Can be:
  - Git: `https://git-repository.git/package-name`
  - Package: `example-repo.example-package-name.example-workspace`
- `NAME` - Name of the new package.

**Flags:**

| Flag | Description | Default |
|------|-------------|---------|
| `--repository string` | Downstream repository for cloned package | (required unless `--subpackage-dir` is set) |
| `--workspace string` | Workspace name for new package | `v1` (must not be set with `--subpackage-dir`) |
| `--directory string` | Directory within upstream repository (Git only) | |
| `--ref string` | Branch, tag, or SHA in upstream repository (Git only) | |
| `--strategy string` | Update strategy: `resource-merge`, `fast-forward`, `force-delete-replace`, `copy-merge` | `resource-merge` |
| `--secret-ref string` | Secret name for basic auth (Git only) | |
| `--subpackage-dir string` | Directory path into which the upstream package will be cloned as an independent subpackage. When set, `NAME` refers to the parent package revision (which must be in Draft state), and `--repository`/`--workspace` must not be specified. | |

**Examples:**

```bash
# Clone from existing package
porchctl rpkg clone example-repo.example-package-name.example-workspace example-package-name-2 \
  --repository=example-repo-2 \
  --workspace=example-workspace-2 \
  --strategy=force-delete-replace

# Clone from Git repository
porchctl rpkg clone https://github.com/repo/blueprint.git example-downstream-package \
  --repository=blueprint \
  --ref=base/v0 \
  --namespace=default \
  --directory=base

# Clone as an independent subpackage into a draft parent package
porchctl rpkg clone upstream-repo.blueprint.v1 deployment.parent-package.v2 \
  --subpackage-dir=path/to/subpkg \
  --namespace=default
```

---

### rpkg copy

Create a new package revision from an existing one.

**Aliases:** `copy`, `edit`

**Usage:**
```bash
porchctl rpkg copy SOURCE_PACKAGE [flags]
```

**Arguments:**

- `SOURCE_PACKAGE` - Kubernetes name of the source package revision.

**Flags:**

| Flag | Description |
|------|-------------|
| `--workspace string` | Workspace name for the copy | (required) |

**Examples:**

```bash
# Create new revision with different workspace
porchctl rpkg copy example-repo.example-package-name.example-workspace \
  --workspace=example-workspace-2 \
  --namespace=example-namespace
```

---

### rpkg get

List package revisions in registered repositories.

**Aliases:** `get`, `list`

**Usage:**
```bash
porchctl rpkg get [PACKAGE] [flags]
```

**Arguments:**

- `PACKAGE` - (Optional) Kubernetes name of specific package revision. If omitted, shows all package revisions.

**Flags:**

| Flag | Description | Default |
|------|-------------|---------|
| `--name string` | Filter by package name (substring match) | |
| `--revision int` | Filter by revision number | `-2` (all) |
| `--workspace string` | Filter by workspace name | |
| `--show-kptfile` | Display the root Kptfile of the specified package revision. Requires exactly one package revision name. Cannot be combined with `--name`, `--revision`, `--workspace`, or `-A`. | |
| `-A, --all-namespaces` | List across all namespaces | |
| `-o, --output string` | Output format | |
| `--no-headers` | Don't print headers | |
| `--show-labels` | Show all labels | |

**Examples:**

```bash
# Get specific package revision
porchctl rpkg get example-repo.example-package-name.example-workspace --namespace=example-namespace

# Get by package name
porchctl rpkg get --name=example-package-name --namespace=example-namespace

# Get all package revisions
porchctl rpkg get --namespace=example-namespace

# Get all with revision 0 (Draft/Proposed)
porchctl rpkg get --revision=0

# Display the root Kptfile of a specific package revision
porchctl rpkg get example-repo.example-package-name.example-workspace --show-kptfile --namespace=example-namespace
```

---

### rpkg pull

Pull package revision content to local filesystem.

**Aliases:** `pull`, `source`, `read`

**Usage:**
```bash
porchctl rpkg pull PACKAGE [DIR] [flags]
```

**Arguments:**

- `PACKAGE` - Kubernetes name of the package revision.
- `DIR` - (Optional) Local directory for package content. If omitted, writes to stdout.

**Examples:**

```bash
# Pull to directory
porchctl rpkg pull example-repo.example-package-name.example-workspace \
  --namespace=example-namespace

# Pull to specific directory
porchctl rpkg pull example-repo.example-package-name.example-workspace ./my-package \
  --namespace=example-namespace
```

---

### rpkg push

Push local resources to a package revision.

**Aliases:** `push`, `sink`, `write`

**Usage:**
```bash
porchctl rpkg push PACKAGE [DIR] [flags]
```

**Arguments:**

- `PACKAGE` - Kubernetes name of the package revision.
- `DIR` - Local directory with package content, or `-` to read from stdin.

**Examples:**

```bash
# Push from directory
porchctl rpkg push example-repo.example-package-name.example-workspace ./example-package-dir \
  --namespace=example-namespace

# Push from stdin
porchctl rpkg push example-repo.example-package-name.example-workspace - <stdin> \
  --namespace=example-namespace
```

---

### rpkg propose

Propose that a package revision should be published.

**Usage:**
```bash
porchctl rpkg propose PACKAGE [...] [flags]
```

**Arguments:**

- `PACKAGE` - Kubernetes name(s) of package revision(s). Multiple packages can be space-separated.

**Examples:**

```bash
# Propose single package
porchctl rpkg propose example-repo.example-package-name.example-workspace \
  --namespace=example-namespace

# Propose multiple packages
porchctl rpkg propose pkg1 pkg2 pkg3 --namespace=example-namespace
```

---

### rpkg approve

Approve a proposal to publish a package revision.

**Usage:**
```bash
porchctl rpkg approve PACKAGE [...] [flags]
```

**Arguments:**

- `PACKAGE` - Kubernetes name(s) of package revision(s). Multiple packages can be space-separated.

**Examples:**

```bash
# Approve single package
porchctl rpkg approve example-repo.example-package-name.example-workspace \
  --namespace=example-namespace

# Approve multiple packages
porchctl rpkg approve pkg1 pkg2 --namespace=example-namespace
```

---

### rpkg reject

Reject a proposal to publish or delete a package revision.

**Usage:**
```bash
porchctl rpkg reject PACKAGE [...] [flags]
```

**Arguments:**

- `PACKAGE` - Kubernetes name(s) of package revision(s). Multiple packages can be space-separated.

**Examples:**

```bash
# Reject proposal
porchctl rpkg reject example-repo.example-package-name.example-workspace \
  --namespace=example-namespace
```

---

### rpkg upgrade

Create a new revision upgrading a downstream package to a newer upstream version.

**Usage:**
```bash
porchctl rpkg upgrade SOURCE_PACKAGE_REVISION [flags]
```

**Arguments:**

- `SOURCE_PACKAGE_REVISION` - Target downstream package revision to upgrade. Must be published and have an upstream package.

**Flags:**

| Flag | Description | Default |
|------|-------------|---------|
| `--revision int` | Upstream revision number to upgrade to. If omitted, upgrades to latest | |
| `--workspace string` | Workspace name for new package revision | (required unless `--subpackage-dir` is set) |
| `--strategy string` | Update strategy: `resource-merge`, `fast-forward`, `force-delete-replace`, `copy-merge` | `resource-merge` |
| `--discover string` | Discover available updates instead of upgrading. Options: `upstream`, `downstream` | |
| `--subpackage-dir string` | Directory path of an independent subpackage to upgrade within the parent package. When set, the parent package must be in Draft state and `--workspace` must not be specified. | |

**Examples:**

```bash
# Discover available upstream updates
porchctl rpkg upgrade --discover=upstream

# Upgrade to specific upstream revision
porchctl rpkg upgrade deployment.some-package.v1 --revision=3 --workspace=v2

# Upgrade to latest upstream
porchctl rpkg upgrade deployment.some-package.v1 --workspace=v2

# Upgrade with specific strategy
porchctl rpkg upgrade deployment.some-package.v1 \
  --revision=3 \
  --workspace=v2 \
  --strategy=copy-merge

# Upgrade an independent subpackage within a draft parent package
porchctl rpkg upgrade deployment.parent-package.v2 \
  --subpackage-dir=path/to/subpkg \
  --revision=3
```

---

### rpkg propose-delete

Propose deletion of a published package revision.

**Aliases:** `propose-delete`, `propose-del`

**Usage:**
```bash
porchctl rpkg propose-delete PACKAGE [...] [flags]
```

**Arguments:**

- `PACKAGE` - Kubernetes name(s) of package revision(s). Multiple packages can be space-separated.

**Examples:**

```bash
# Propose deletion of published package
porchctl rpkg propose-delete example-repo.example-package-name.example-workspace \
  --namespace=example-namespace
```

---


### rpkg del

Delete a package revision.

{{% alert color="primary" title="Note" %}}
To delete a **published** package revision, it must first be in the `DeletionProposed` lifecycle state.

**Draft** and **Proposed** package revisions can be deleted directly without approval.
{{% /alert %}}

**Aliases:** `del`, `delete`

**Usage:**
```bash
porchctl rpkg del PACKAGE [...] [flags]
```

**Arguments:**

- `PACKAGE` - Kubernetes name(s) of package revision(s). Multiple packages can be space-separated.

**Examples:**

```bash
# Delete package revision
porchctl rpkg del example-repo.example-package-name.example-workspace \
  -n example-namespace
```

---

## completion

Generate shell autocompletion scripts.

**Usage:**
```bash
porchctl completion [command]
```

**Available Shells:**

- `bash` - Generate bash autocompletion
- `fish` - Generate fish autocompletion
- `powershell` - Generate PowerShell autocompletion
- `zsh` - Generate zsh autocompletion

**Examples:**

```bash
# Generate bash completion
porchctl completion bash > ~/.local/share/bash-completion/completions/porchctl

# Generate zsh completion
porchctl completion zsh > "${fpath[1]}/_porchctl"
```

See each subcommand's help for shell-specific installation instructions.

---

## version

Print the version number of porchctl.

**Usage:**
```bash
porchctl version [flags]
```

**Examples:**

```bash
# Show version
porchctl version
```
