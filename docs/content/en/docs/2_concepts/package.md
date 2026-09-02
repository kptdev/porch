---
title: "Packages"
type: docs
weight: 1
description: |
  Understanding packages in Porch: the relationship between kpt packages and Porch's PackageRevision API.
---

{{% alert title="Warning" color="warning" %}}

A "Porch package" is a collection of **package revisions on the same kpt package**. Porch does not have the concept of a
"Package" internally - whenever an API request is received on a package, Porch composes the response from the package revisions
associated with the kpt package. When users say they are creating, editing, or upgrading "a package" in Porch, they are
manipulating a **PackageRevision** resource and its paired **PackageRevisionResources** resource. These are Porch's Kubernetes
API interpretations of kpt package metadata and contents stored in Git repositories.

{{% /alert %}}

## What is a Package in Porch?

In Porch, a **package** is represented, not as files you directly manipulate, but through Kubernetes API resources presenting
different views of the package's file contents. When you work with a package in Porch, you interact with revisions of the
package, and each such **package revision** has a pair of resources: a **PackageRevision** resource and a **PackageRevisionResources**
resource:

- **PackageRevision**: A Kubernetes resource containing the kpt package metadata (name, repository, revision number, lifecycle
  stage, workspace, task list)
- **PackageRevisionResources**: A companion resource containing the actual kpt package contents, represented as a key-value
  map of file names to YAML file contents. You can [read a subset of files]({{% relref "/docs/4_tutorials_and_how-tos/working_with_package_revisions/inspecting-packages.md#reading-selected-package-files" %}})
  or [update selected files without replacing the rest of the package]({{% relref "/docs/4_tutorials_and_how-tos/working_with_package_revisions/inspecting-packages.md#partial-package-content-updates" %}}).

These resources expose [kpt packages](https://kpt.dev/book/03-packages/), stored in Git repositories, as Kubernetes-native
objects.

## The Relationship: kpt Packages and Porch

**kpt packages** are the underlying storage format: a directory that contains a `Kptfile` and one or more KRM (Kubernetes Resource Model) YAML files. They may have subdirectories with independent or dependent kpt subpackages, are stored in Git repositories (branches, tags, commits) by Porch, and follow the [kpt package specification](https://kpt.dev/book/03-packages/).

**Porch packages** are the API layer. PackageRevision and PackageRevisionResources resources expose kpt packages through the Kubernetes API, enable declarative package revision lifecycle management (Draft → Proposed → Published), support operations like init, clone, edit, and upgrade through Kubernetes-style interactions, as well as automatically sync changes between the API resources and the underlying Git storage.

{{% alert title="Warning" color="warning" %}}

It is **NOT recommended** to directly edit downstream API resources on Git.

{{% /alert %}}

## Example: Creating and Viewing a Package

When you create a "package" with `porchctl`:

```bash
porchctl rpkg init my-package --repository=blueprints --workspace=add-ai-feature
```

Porch creates:
1. A **PackageRevision** resource with metadata (name, repository, workspace, lifecycle=Draft)
2. A **PackageRevisionResources** resource with initial content (Kptfile + any starter resources)
3. A Git branch, containing the kpt package files, in the `blueprints` repository (if Porch is configured with the
   [CR cache]({{% relref "../5_architecture_and_components/package-cache/cr-cache" %}}))

When you get "packages"/package revisions in Porch:

```bash
porchctl rpkg get
```

You see **PackageRevision** resources, not raw Git files. Each row represents a PackageRevision with its metadata.

When you pull a "package" contents in Porch:

```bash
porchctl rpkg pull blueprints.my-package.add-ai-feature ./local-dir
```

Porch fetches the **PackageRevisionResources** content and writes it as kpt package files in the local file system.

## Key Distinctions

- **Storage layer**: kpt packages in Git (Kptfile + YAML files)
- **API layer**: PackageRevision and PackageRevisionResources in Kubernetes
- **User interaction**: You work with PackageRevision and PackageRevisionResources resources using `porchctl`, `kubectl`,
  or Porch's Kubernetes API
- **Compatibility**: The underlying kpt packages remain standard and, when pulled, can be used locally with the kpt CLI

This separation allows Porch to add orchestration capabilities (lifecycle management, approval workflows, automation) on
top of standard kpt packages, without vendor lock-in.
