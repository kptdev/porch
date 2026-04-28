---
title: "Package Revisions"
type: docs
weight: 3
description: |
  Understanding package revisions: their versioning and nature as the working unit in Porch.
---

## What is a Package Revision?

A **package revision** is the actual working unit in Porch. While users may often refer to "working with a package", they
are actually working with a specific revision of that package.

Think of it like Git commits:
- A Git repository contains a project
- Each commit is a specific version of that project
- You work with commits, not the abstract "project"

Similarly in Porch:
- A Repository contains kpt packages
- Each package revision is a specific version of a kpt package
- You work with package revisions, not the abstract "package"

## Published Package Revision Numbering

Package revisions use simple integer sequence versioning (`1`, `2`, `3`, etc.):

- **1**: First published revision
- **2**: Second published revision
- **3**: Third published revision
- And so on...

This simple scheme enables easy comparison (`3` is newer than `2`), automatic version assignment on publication, optimistic concurrency control (to detect conflicting edits), and straightforward automation without parsing semantic versions.

## Draft vs Published Revisions

Package revisions exist in different [lifecycle stages]({{% relref "package-revision-lifecycle" %}}):

- **Draft revisions** are work-in-progress versions that are **not yet assigned a revision number**, they are automatically set to `0`. They are identified by workspace name only (for example, `our-repo.my-package.firststab`, where `firststab` is the workspace), can be freely modified or deleted, and are not visible to downstream consumers.
- **Published revisions** are finalized, immutable versions identified by an automatically-assigned integer revision number (`1`, `2`, `3`). They cannot be modified (you must create a new draft to make changes) or directly deleted (you must first propose deletion). They are visible to downstream consumers and can be cloned or deployed.

## The Latest Revision

The "latest" package revision is the most recently published revision (highest revision number). Porch marks it with a
Kubernetes label:

```yaml
kpt.dev/latest-revision: "true"
```

This label makes it easy to query for the latest revision using label selectors, automatically track the newest version of a kpt package, and build automation that follows the latest published revision.

## Package Revision Identity

A package revision is uniquely identified by three parts: the **repository name** (which Repository resource it belongs to), the **package name** (the kpt package directory name), and the **workspace name** (an identifier for the package revision that is user-defined at creation time).

Example package revision names:
- `blueprints.abc123.firststab` - Draft in workspace `firststab` of package `abc123` in repository `blueprints`
- `blueprints.abc123.ready` - Published revision `ready` of package `abc123` in repository `blueprints`

## Package Revision Discovery

When Porch starts, it reads the Git repositories it has been connected to via Porch Repository resources to discover existing
package revisions. Porch subsequently runs a periodic job that checks the Git repositories for updates, refreshing the package
revisions if any changes are detected.

Porch uses the following approach for each repository:

    1. Iterate over the specified branch of the repository and walk the directory tree, from the root down each subdirectory
        a.  The subdirectory contains a `Kptfile`
            i. Create a PackageRevision with the name [repo-name].[directory-path-to-this-subdirectory].[subdirectory-name].[branch-name]
            ii. The WorkspaceName of the PackageRevision is set to the branch name of the porch repository.
            iii. Exit from this subdirectory to the next highest level
        b.  The subdirectory does not contain a `Kptfile`
            i.  Continue searching subdirectories of this subdirectory (go to a. above)

    2. Iterate over all tags in the repository that match a path in the repository on that tag (that is, the tag is of the
       form `[path/to/directory/containing/package/revision]/[version]`)
        a. The `[path/to/directory/containing/package/revision]` location contains a `Kptfile`
            i. Create a PackageRevision with the name repo-name.path-to-directory-containing-package-revision.version
            ii. The WorkspaceName of the PackageRevision is set to the version of the tag.
            iii. If the version is of the form `vx`, set the Revision of the PackageRevision to the value of x
            iv. Continue to next tag
        b. The location does not contain a `Kptfile`
            i. Report a warning and continue to next tag

{{% details summary="**Package revision discovery: worked example** (click to expand)" %}}

Consider the [Porch Catalog git repository](https://github.com/nephio-project/catalog)

It has (among others) the branches `main`, `v4`, and `v3`.

The package [oai-ran-operator](https://github.com/nephio-project/catalog/tree/main/workloads/oai/oai-ran-operator) has
the following tags:

```bash
$ git tag --list | grep oai-ran-operator

workloads/oai/oai-ran-operator/v1
workloads/oai/oai-ran-operator/v2.0.0
workloads/oai/oai-ran-operator/v3.0.0
```

Consider the three Porch repositories defined below:

```yaml
apiVersion: config.porch.kpt.dev/v1alpha1
kind: Repository
metadata:
  name: catalog-main
  namespace: default
spec:
  deployment: false
  type: git
  git:
    branch: main
    directory: /
    repo: https://github.com/nephio-project/catalog.git

---

apiVersion: config.porch.kpt.dev/v1alpha1
kind: Repository
metadata:
  name: catalog-v4-0-0
  namespace: default
spec:
  content: Package
  deployment: false
  type: git
  git:
    branch: v4
    directory: /
    repo: https://github.com/nephio-project/catalog.git

---

apiVersion: config.porch.kpt.dev/v1alpha1
kind: Repository
metadata:
  name: catalog-v3-0-0
  namespace: default
spec:
  content: Package
  deployment: false
  type: git
  git:
    branch: v3
    directory: /
    repo: https://github.com/nephio-project/catalog.git
```

Porch discovers the following package revisions for the "oai-ran-operator" package"

```bash
$ porchctl -n default rpkg get

NAMESPACE   NAME                                                   PACKAGE                          WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY       SIZE ON DISK
default     catalog-main.workloads.oai.oai-ran-operator.main       workloads/oai/oai-ran-operator   main            -1         false    Published   catalog-main     2999 B
default     catalog-main.workloads.oai.oai-ran-operator.v2.0.0     workloads/oai/oai-ran-operator   v2.0.0          -1         false    Published   catalog-main     3003 B
default     catalog-main.workloads.oai.oai-ran-operator.v3.0.0     workloads/oai/oai-ran-operator   v3.0.0          -1         false    Published   catalog-main     3003 B
default     catalog-main.workloads.oai.oai-ran-operator.v1         workloads/oai/oai-ran-operator   v1              1          true     Published   catalog-main     3006 B
default     catalog-v3-0-0.workloads.oai.oai-ran-operator.v2.0.0   workloads/oai/oai-ran-operator   v2.0.0          -1         false    Published   catalog-v3-0-0   3003 B
default     catalog-v3-0-0.workloads.oai.oai-ran-operator.v3.0.0   workloads/oai/oai-ran-operator   v3.0.0          -1         false    Published   catalog-v3-0-0   3003 B
default     catalog-v3-0-0.workloads.oai.oai-ran-operator.v1       workloads/oai/oai-ran-operator   v1              1          false    Published   catalog-v3-0-0   3006 B
default     catalog-v3-0-0.workloads.oai.oai-ran-operator.v3       workloads/oai/oai-ran-operator   v3              3          true     Published   catalog-v3-0-0   3003 B
default     catalog-v4-0-0.workloads.oai.oai-ran-operator.v2.0.0   workloads/oai/oai-ran-operator   v2.0.0          -1         false    Published   catalog-v4-0-0   3003 B
default     catalog-v4-0-0.workloads.oai.oai-ran-operator.v3.0.0   workloads/oai/oai-ran-operator   v3.0.0          -1         false    Published   catalog-v4-0-0   3003 B
default     catalog-v4-0-0.workloads.oai.oai-ran-operator.v1       workloads/oai/oai-ran-operator   v1              1          false    Published   catalog-v4-0-0   3006 B
default     catalog-v4-0-0.workloads.oai.oai-ran-operator.v4       workloads/oai/oai-ran-operator   v4              4          true     Published   catalog-v4-0-0   3003 B
```

{{% /details %}}

## Key Points

- **Package revisions** are the working unit in Porch, not "packages"
- Each package revision represents a specific version of a kpt package
- Simple integer versioning (`1`, `2`, `3`) enables automation
- Package revisions use workspace names to identify package revisions within packages
- Published revisions have revision numbers as well as workspace names
- The latest revision is marked with the `kpt.dev/latest-revision: "true"` label
- Package revisions are immutable once published
