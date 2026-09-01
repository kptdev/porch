---
title: "Working with Package Revisions"
type: docs
weight: 3
description: A group of guides outlining how to interact with Package Revisions in Porch
---

## Prerequisites

- Porch deployed on a Kubernetes cluster. See, [Setup Porch Guide]({{% relref "/docs/3_getting_started/installing-porch.md" %}}).
- **Porchctl** CLI tool installed. See, [Setup Porchctl Guide]({{% relref "/docs/3_getting_started/installing-porchctl.md" %}}).
- A Git repository registered with Porch. See, [Setup Repositories Guide]({{% relref "/docs/4_tutorials_and_how-tos/working_with_porch_repositories/repository-registration.md" %}}).
- **Kubectl** configured to access your cluster.

---

## Understanding Package Revisions

In Porch, you work with **PackageRevisions** - there is no separate "Package" resource. When we say "package" colloquially, we're referring to a PackageRevision. The `rpkg` command stands for "revision package".

PackageRevisions are Kubernetes resources that represent versioned collections of configuration files stored in Git repositories. Each PackageRevision contains:

- **KRM Resources**: Kubernetes Resource Model files (YAML configurations)
- **Kptfile**: Package metadata and pipeline configuration
- **Pipeline Functions**: KRM functions that transform package resources
- **Lifecycle State**: Current state in the package workflow

**PackageRevision Operations:**

| Operation            | Git Command                    | Description                                                                   |
|----------------------|--------------------------------|-------------------------------------------------------------------------------|
| Creation             | `init`, `clone`, `copy`        | Create new PackageRevisions from scratch, existing packages, or new revisions |
| Inspection           | `get`                          | List and view PackageRevision information and metadata                        |
| Content Management   | `pull`, `push`                 | Move PackageRevision content between Git repositories and local filesystem    |
| Lifecycle Management | `propose`, `approve`, `reject` | Control PackageRevision workflow states                                       |
| Upgrading            | `upgrade`                      | Create new revision upgrading downstream to more recent upstream package      |
| Deletion             | `propose-delete`, `del`        | Propose deletion of published PackageRevisions, then delete them              |

---

## PackageRevision Lifecycle

PackageRevisions follow a structured lifecycle with three main states:

- **Draft**: Work in progress, fully editable. Revision number is 0.
- **Proposed**: Ready for review, contents are immutable. To make further changes, reject back to Draft. Revision number remains 0.
- **Published**: Approved and immutable. Revision number increments to 1+.

PackageRevisions use the following lifecycle transitions:

| Transition                       | Command                        | Meaning                             |
|----------------------------------|--------------------------------|-------------------------------------|
| **Draft → Proposed**             | `porchctl rpkg propose`        | Signal readiness for review         |
| **Proposed → Published**         | `porchctl rpkg approve`        | Approve and make immutable          |
| **Proposed → Draft**             | `porchctl rpkg reject`         | Return for more work                |
| **Published → DeletionProposed** | `porchctl rpkg propose-delete` | Mark for deletion, pending approval |
| **DeletionProposed → Published** | `porchctl rpkg reject`         | Reject deletion proposal            |

---

## PackageRevision Naming

Porch generates PackageRevision names automatically using the `{repositoryName}.{packageName}.{workspaceName}` format. An example PackageRevision naming would be `porch-test.my-first-package.v1`.

The name consists of the following components:

- **Repository Name**: Name of the registered Git repository
- **Package Name**: Logical name for the package (can have multiple revisions)
- **Workspace Name**: Unique identifier within the package (maps to Git branch/tag)

{{% alert title="Note" color="primary" %}}
Workspace names must be unique within a package. Multiple PackageRevisions can share the same package name with different workspaces and published PackageRevisions get tagged in Git using the workspace name.
{{% /alert %}}

---

## Working with PackageRevision Content

PackageRevisions contain structured configuration files that can be modified through various operations:

**Local Operations:**

1. **Pull**: Download PackageRevision contents to local filesystem
2. **Modify**: Edit files locally using standard tools
3. **Push**: Upload changes back to Porch (triggers pipeline rendering)

KRM functions defined in the Kptfile automatically transform resources; they run when PackageRevisions are pushed to Porch. Common examples include `set-namespace`, `apply-replacements`, and `search-replace`.

{{% alert title="Note" color="primary" %}}
When specifying a function image in the Kptfile pipeline, you can use the shorthand form (e.g. `set-namespace:latest`) without a full container registry path. Porch will automatically resolve it using the default registry prefix configured on porch-server (`--default-image-prefix`). You can also use the full image path (e.g. `ghcr.io/kptdev/krm-functions-catalog/set-namespace:latest`) if you prefer to be explicit. To see which functions are available, run `kubectl get functionconfigs -n porch-fn-system`.
{{% /alert %}}

**Content Structure:**

```bash
package-revision/
├── Kptfile                   # Package metadata and pipeline
├── .KptRevisionMetadata      # Porch-managed metadata
├── package-context.yaml      # Package context information
├── README.md                 # Package documentation
└── *.yaml                    # KRM resources
```

---

## Repository Integration

PackageRevisions are stored in Git repositories registered with Porch:

**Git Branch Mapping:**

- **Draft**: Stored in `draft/{workspace}` branch
- **Proposed**: Stored in `proposed/{workspace}` branch  
- **Published**: Tagged as `{workspace}` and stored in main branch

Porch differentiates two types of repositories: "blueprint" and "deployment". Blueprint repositories are meant to contain upstream package templates for cloning, while deployment repositories should store deployment-ready packages. The latter are marked with the `--deployment` flag.

Porch automatically syncs with Git repositories, however you can perform a manual sync with the `porchctl repo sync <repository-name>` command. You can also configure periodic sync with cron expressions.

---

## Troubleshooting

Common issues when working with PackageRevisions and their solutions:

**PackageRevision stuck in Draft?**

- Check readiness conditions with the `porchctl rpkg get <PACKAGE-REVISION> -o yaml | grep -A 5 conditions` command
- Verify that all required fields are populated in the Kptfile
- Check for pipeline function errors in the Porch server logs

**Push fails with conflict?**

- Pull the latest version first with the `porchctl rpkg pull <PACKAGE-REVISION> <directory>` command
- Check if the PackageRevision has been modified by someone else
- Resolve conflicts locally and push again

**Cannot modify Published PackageRevision?**

- Published PackageRevisions are immutable by design
- Create a new revision with the `porchctl rpkg copy` command
- Use the copying workflow to create editable versions

**PackageRevision not found?**

- Verify the exact PackageRevision name with the `porchctl rpkg get --namespace default` command
- Check if you are using the correct namespace
- Ensure the repository is registered and synchronized

**Permission denied errors?**

- Check RBAC permissions with the `kubectl auth can-i get packagerevisions -n default` command
- Verify that the service account has proper roles for PackageRevision operations
- Ensure the repository authentication is configured correctly

**Errors about "placeholder package revision"?**

- Clone, edit/copy, and upgrade operations cannot be performed on or using placeholder PackageRevisions
  - (identified by revision number -1 and workspace name matching the repository's Git branch, typically 'main')
- Use published PackageRevisions with specific version numbers in these operations
- For more information on placeholder PackageRevisions, see [Core Concepts]({{% relref "/docs/2_concepts#core-concepts" %}})

**Pipeline functions failing?**

- Check the function image availability and version
- Verify the function configuration in Kptfile
- Review the function logs in the Porch server output during push operations
- To save work-in-progress despite failures, add the `porch.kpt.dev/push-on-render-failure: "true"` annotation to the PackageRevision
  ```bash
  kubectl annotate packagerevision <name> porch.kpt.dev/push-on-render-failure=true
  ```
- The behavior of partially-rendered resources can be further controlled via Kptfile annotations (see [kpt documentation](https://kpt.dev/book/04-using-functions/#debugging-render-failures))

---

## Key Concepts

Important terms and concepts for working with PackageRevisions:

- **PackageRevision**: The Kubernetes resource managed by Porch (there is no separate "Package" resource)
- **Workspace**: Unique identifier for a PackageRevision within a package (maps to Git branch/tag)
- **Lifecycle**: Current state of the PackageRevision (Draft, Proposed, Published, DeletionProposed)
- **Revision Number**: 0 for Draft/Proposed, 1+ for Published (increments with each publication)
- **Latest**: Flag indicating the most recent published PackageRevision of a package
- **Pipeline**: KRM functions defined in Kptfile that transform PackageRevision resources
- **Upstream/Downstream**: Relationship between source PackageRevisions (upstream) and their clones (downstream)
- **Repository**: Git repository where PackageRevisions are stored and managed
- **Namespace Scope**: PackageRevisions exist within a Kubernetes namespace and inherit repository namespace
- **Rendering**: Process of executing pipeline functions to transform PackageRevision resources
- **Kptfile**: YAML file containing PackageRevision metadata, pipeline configuration, and dependency information

---
