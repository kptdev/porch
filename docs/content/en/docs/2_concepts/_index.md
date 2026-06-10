---
title: "Porch Concepts"
type: docs
weight: 2
description: |
  The fundamental topics necessary to understand Porch as "package orchestration" on a conceptual level.
---

## Core Concepts

This section introduces some core concepts of Porch's package orchestration:

* ***[Package]({{% relref "package" %}})***: A Porch **Package** encapsulates the orchestration of a single [kpt package](https://kpt.dev/book/03-packages/),
  stored in a Git repository. A package may be orchestrated many times, resulting in the creation of many revisions of the
  package, each of which is modelled as a **package revision**. Packages are stored in Git repositories. When you interact
  with Porch via `porchctl` or `kubectl`, you orchestrate revisions of packages, thus working with `PackageRevision` resources
  and not directly with packages.

* ***[Repository]({{% relref "repositories" %}})***: This is a version-control repository used to store package file
 contents. For example, a [Git](https://git-scm.org/) or (experimentally) [OCI](https://github.com/opencontainers/image-spec/blob/main/spec.md)
  repository.

* ***[Package Revision]({{% relref "package-revisions" %}})***: This is a single version of a package. Many versions of
  a package may exist because Porch allows users to perform multiple orchestrations on a single package. A **Package Revision**
  represents a particular version of a package and tracks the state of its orchestration.

* ***[Lifecycle]({{% relref "package-revision-lifecycle" %}})***: The orchestration of a package revision in Porch has
  a **lifecycle**. The package revision passes through a number of lifecycle stages in its journey towards publication as
  a published package revision (as illustrated below):
  * ***Draft*** - the package revision is being authored (created or edited). The package revision contents can be modified,
    but the package revision is not ready to be used/deployed.
  * ***Proposed*** - the package revision's author has completed the preparation of the package revision and its files and
    has proposed that the package revision be published.
  * ***Published*** - the package revision has been approved and is ready to be used. Published package revisions may be
    deployed. A published package revision may be copied to create a new package revision of the same package, in which
    development of the package may continue. A published package revision may also be cloned to create the first package
    revision of an entirely new package.
  * ***DeletionProposed*** - a user has proposed that this package revision be deleted from the repository. A package revision
    must be proposed for deletion before it can be deleted from Porch.
  ![Package Lifecycle Workflow](/static/images/porch/lifecycle-flowchart.drawio.svg)

* ***[Workspace]({{% relref "workspace" %}})***: A **workspace** is the *unique identifier* of a package revision *within*
  a package. It is specified either by the user (when creating a new package revision by initialisation, cloning, or editing)
  or by Porch (when discovering a package revision in a Git repository). For the workspace name, the following rules apply:
    * you can use whatever string you like (e.g., "cell1", "district", "add-a-feature", "delete-me")
      * as long as it is [compliant with DNS name conventions](https://kubernetes.io/docs/concepts/overview/working-with-objects/names#dns-label-names). 
    * the workspace name must be unique in its own package
    * the same workspace name can be used on workspaces on other packages, so for two packages called `ran-package` and
      `ev-battery-package` it is legal for both to have a workspace called `cell1`
    * the workspace only has scope within a package, so if the same workspace name appears in two packages, Porch treats
      these as two completely independent package revisions completely independently
      * hence the two `cell1` workspaces above have no intrinsic relationship whatever
    * when Porch discovers package revisions in Git repositories, it takes the branch, tag or SHA, from which it read the
      package revision, as a workspace name for the package revision

  The workspace name of a package revision never changes once it is specified, and persists through all lifecycle transitions,
  even when the package revision is approved and is assigned a revision number.

* ***[Revision numbering]({{% relref "package-revisions#published-package-revision-numbering" %}})***: A **Revision**
  number on a package revision identifies the order in which package revisions of a package were published. When a
  PackageRevision is approved and moves to the Published lifecycle stage, it is assigned a Revision one higher than the
  highest existing Revision.

  The following rules apply:
    * a package revision in the Published or DeletionProposed lifecycle stage has a Revision > `0`
    * a package revision in the Draft or Proposed lifecycle stage has a Revision == `0`
    * for discovered package revisions in Git, Porch uses naming conventions to determine the appropriate Revision. If
      Porch cannot determine the Revision of an upstream package revision, it sets the Revision to `-1`
    * *placeholder package revisions* (see below) have a revision of `-1`

  Notes:
    * Porch uses the workspace name and not the Revision of a package revision to uniquely identify the package revision
    * the Revision of a package revision is not unique because all Draft package revisions have a Revision of `0`
    * there is no relationship between the workspace name and the Revision of a package revision. For example, even if a
      package revision is created with its workspace name given a value of "V2", it could very well end up with a Revision
      of `4` once published

* ***Placeholder package revision***: A dummy package revision reference that points at a package's latest package revision.
  The placeholder package revision is created by Porch simultaneously with the first package revision for a particular
  package. Each time a new package revision is published on the package, the placeholder package revision is updated (actually
  deleted and recreated).

  The following rules apply:
  * there is always at most one placeholder package revision for a package
  * it always has a revision number of `-1`
  * its workspace name is always the branch in the Git repository on which the package revision exists - usually (though
    not always) `main`
  * its naming comvention is `{repository-name}.{package-name}.{branch-name}`, where {branch-name} is the branch in Git
    on which the package revision exists

* ***[Upstream and Downstream]({{% relref "upstream-downstream" %}})***: source-and-derivation relationships between
  package revisions. When a package revision is cloned, it becomes the **upstream** (source) in its relationship to the
  newly-created **downstream** (derived) package revision(s). Downstream package revisions maintain a link to their upstream
  source package revision and can be upgraded when new versions of the upstream package revision are published.

* ***[Subpackages]({{% relref "subpackages" %}})***: A **subpackage** is a kpt package nested within a parent
  package at a specific subdirectory. An **independent subpackage** maintains its own upstream source whereas a
  **dependent subpackage** has no upstream source. Independent subpackages enable composition of a single
  package from multiple upstream sources, where each subpackage can be independently cloned and upgraded on its own schedule.
  See [the kpt package documentation](https://kpt.dev/book/03-packages) for a full description of dependent and independent subpackages.

* ***[Functions]({{% relref "functions" %}})***: Specifically, [KRM functions](https://github.com/kubernetes-sigs/kustomize/blob/master/cmd/config/docs/api-conventions/functions-spec.md).
  Functions can be added to a package's [kptfile pipeline](https://kpt.dev/book/04-using-functions/#declarative-function-execution)
  in the course of modifying a package revision in *Draft* state. When a user updates or proposes a package revision, Porch
  automatically calls kpt to run the pipeline on the package contents, mutating and validating the KRM resource files.

* ***[Package Variant]({{% relref "package-variant" %}})*** and ***Package Variant Set***: These Kubernetes objects represent
  higher levels of package revision automation. Package variants can be used to automatically track an upstream package
  revision and manage cloning it to one or several downstream package revisions, as well as preparing new downstream package
  revisions when a new revision of the upstream package revision is published. Package variant sets enable the same behaviour
  for package variants themselves.

## Additional Terms

* ***Configuration as Data (CaD)***: The architectural approach that Porch implements. [CaD](https://cloud.google.com/blog/products/containers-kubernetes/understanding-configuration-as-data-in-kubernetes)
  treats configuration with the same rigour as application code: configuration data is the source of truth (stored separately
  from live state), uses a uniform serialisable data model (KRM YAML), separates data from code that acts on it (functions
  transform, controllers apply), and abstracts storage from operations (clients use APIs, not direct Git/OCI access). Key
  principles include decoupling abstractions from data, separating actuation from processing, and preferring transformation
  over generation.

* ***Deployment repository***: A repository can be designated as a deployment repository (via `spec.deployment: true`).
  Package revisions in *Published* state in a deployment repository are considered deployment-ready and can be consumed
  by GitOps tools like Flux or Config Sync. See [Repositories]({{% relref "repositories#deployment-vs-blueprint-repositories" %}})
  for details.
