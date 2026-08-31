---
title: "Glossary"
type: docs
weight: 11
description: Definitions of key terms and concepts used in Porch documentation
---

This glossary defines terms and concepts used throughout the Porch documentation. Terms are organized by topic for easy reference.

---

## Core Concepts

Fundamental concepts for understanding Porch.

### Configuration as Data

An approach to managing configuration (infrastructure, policy, services, applications) that:
- Makes configuration data the source of truth, stored separately from live state
- Uses a uniform, serializable data model (KRM YAML)
- Separates code that acts on configuration from the data itself
- Abstracts configuration file structure and storage from operations

Porch implements Configuration as Data principles for package management.

*See also*: [KRM](#krm)

### Package

A logical grouping of Kubernetes resources. In kpt, a package is a directory containing a Kptfile and KRM resources. In Porch, packages are surfaced through PackageRevision and PackageRevisionResources API resources.

*See also*: [PackageRevision](#packagerevision), [kpt](#kpt)

### PackageRevision

A Kubernetes custom resource representing a specific revision of a package with metadata and lifecycle state. PackageRevision contains package name, repository, workspace, revision number, lifecycle stage, and tasks. It is paired with PackageRevisionResources which contains the actual file content.

*See also*: [PackageRevisionResources](#packagerevisionresources), [Lifecycle](#lifecycle)

### PackageRevisionResources

A Kubernetes custom resource containing the actual file contents of a package revision. It stores KRM resources as key-value pairs (filename → content). This resource is always paired with a PackageRevision resource.

*See also*: [PackageRevision](#packagerevision)

### Repository

A Kubernetes custom resource that connects Porch to a Git repository or OCI registry where packages are stored. Repository resources specify location, authentication, branch, directory, and whether it's a deployment repository.

*See also*: [Blueprint Repository](#blueprint-repository), [Deployment Repository](#deployment-repository)

### Workspace

A named, isolated environment for working on a package revision. Each Draft or Proposed PackageRevision has a unique workspace name that maps to Git branches (`drafts/<workspace>` or `proposed/<workspace>`). Workspaces enable multiple users to work on different changes to the same package simultaneously.

*See also*: [Draft](#draft), [PackageRevision](#packagerevision)

---

## Lifecycle & States

Terms related to package revision lifecycle stages and state transitions.

### Lifecycle

The current stage of a PackageRevision in Porch's orchestration process. Package revisions move through stages (Draft → Proposed → Published) with approval gates between them. The lifecycle controls when a package can be modified or deployed.

*See also*: [Draft](#draft), [Proposed](#proposed), [Published](#published), [DeletionProposed](#deletionproposed)

### Draft

A lifecycle stage where a package revision is being authored (created or edited). Draft packages are mutable, stored in temporary Git branches, and not ready for deployment. Only Draft packages can have their content modified.

*See also*: [Lifecycle](#lifecycle), [Workspace](#workspace)

### Proposed

A lifecycle stage where a package revision is ready for review and awaiting approval. Proposed packages are immutable (cannot be edited) and stored in temporary Git branches. They can be approved (→ Published) or rejected (→ Draft).

*See also*: [Lifecycle](#lifecycle), [Draft](#draft), [Published](#published)

### Published

A lifecycle stage where a package revision has been approved and finalized. Published packages are immutable, assigned a revision number (v1, v2, etc.), committed to the main Git branch, and ready for deployment or cloning.

*See also*: [Lifecycle](#lifecycle), [Proposed](#proposed)

### DeletionProposed

A lifecycle stage indicating a Published package revision has been proposed for deletion and is awaiting approval. This intermediate stage allows review before permanent removal.

*See also*: [Lifecycle](#lifecycle), [Published](#published)

### Approval Workflow

The process of moving a package revision through lifecycle stages with explicit approval gates. A Draft package is proposed, reviewed, and then approved to become Published, ensuring changes are validated before finalization.

*See also*: [Lifecycle](#lifecycle), [Proposed](#proposed)

### Revision Number

An integer assigned to Published package revisions, starting at 1 and incrementing with each publication. Draft and Proposed packages have revision number 0. Revision numbers provide sequential versioning within a package.

*See also*: [PackageRevision](#packagerevision), [Published](#published)

---

## Package Relationships

Terms describing relationships between packages.

### Upstream

A source package that can be cloned to create downstream packages. Upstream packages are typically stored in blueprint repositories and serve as templates for customization. Downstream packages maintain a link to their upstream source.

*See also*: [Downstream](#downstream), [UpstreamLock](#upstreamlock)

### Downstream

A package that was cloned from an upstream source. Downstream packages maintain a link to their upstream package via UpstreamLock and can be upgraded when new upstream versions are published.

*See also*: [Upstream](#upstream), [UpstreamLock](#upstreamlock)

### UpstreamLock

Metadata stored in a PackageRevision's status that identifies the upstream source package. It contains the repository URL, directory, ref (branch/tag), and commit SHA. UpstreamLock enables tracking package lineage and upgrading downstream packages.

*See also*: [Upstream](#upstream), [Downstream](#downstream)

### Blueprint Repository

A repository containing upstream package templates and blueprints that can be cloned and customized. Blueprint repositories are typically read-only sources of reusable configurations, marked with `deployment: false`.

*See also*: [Deployment Repository](#deployment-repository), [Repository](#repository)

### Deployment Repository

A repository containing deployment-ready packages, marked with `deployment: true`. Published packages in deployment repositories are ready for GitOps tools like Config Sync or Flux to deploy to clusters.

*See also*: [Blueprint Repository](#blueprint-repository), [Repository](#repository)

---

## Automation

Terms related to automated package operations and controllers.

### Controller

A Kubernetes control loop that watches intended and actual state, making changes to align them. In Porch, controllers automate package operations like cloning, upgrading, and variant generation.

*See also*: [PackageVariant](#packagevariant)

### PackageVariant

A Kubernetes custom resource that automates package cloning and updates. PackageVariant tracks an upstream package and manages one or more downstream packages, automatically creating new downstream revisions when upstream packages are updated.

*See also*: [PackageVariantSet](#packagevariantset), [Upstream](#upstream), [Downstream](#downstream)

### PackageVariantSet

A Kubernetes custom resource that manages sets of PackageVariants. It enables one-to-many package distribution, creating multiple PackageVariants based on selectors (repository labels, object selectors, or explicit lists).

*See also*: [PackageVariant](#packagevariant)

---

## Architecture & Components

Terms describing Porch's architecture and internal components.

### Porch

Package Orchestration Server - "kpt-as-a-service". Porch provides opinionated package management, manipulation, and lifecycle operations through a Kubernetes API, enabling automation using standard Kubernetes controller techniques.

*See also*: [kpt](#kpt), [Porch Server](#porch-server)

### Porch Server

The main Porch component implemented as a Kubernetes aggregated API server. It serves PackageRevision, PackageRevisionResources, and Repository APIs, and includes the orchestration engine, package cache, repository adapters, and the in-process pod evaluator.

*See also*: [Aggregated API Server](#aggregated-api-server), [Function Runner](#function-runner), [Pod Evaluator](#pod-evaluator)

### Aggregated API Server

A Kubernetes extension mechanism that allows adding custom API servers to a cluster. Porch is implemented as an aggregated API server, integrating seamlessly with the Kubernetes API while providing package orchestration capabilities.

*See also*: [Porch Server](#porch-server)

### Function Runner

A Porch microservice that evaluates **cached** KRM function binaries over gRPC. The Engine supplies `exec_path` from the FunctionConfig store. Container-based (pod) evaluation runs in the Engine (porch-server and the PackageRevision controller), not in Function Runner.

*See also*: [KRM Function](#krm-function), [Rendering](#rendering), [Pod Evaluator](#pod-evaluator)

### Pod Evaluator

In-process Engine runtime that executes KRM functions in Kubernetes pods with wrapper-server gRPC, TTL-based pod cache, and image/registry authentication. Required on porch-server (`WRAPPER_SERVER_IMAGE`). Optional on the PackageRevision controller when that env is set.

*See also*: [Function Runner](#function-runner), [KRM Function](#krm-function)

---

## Tools & CLI

Command-line tools for interacting with Porch.

### porchctl

The command-line interface for interacting with Porch. It provides commands for managing repositories (`repo`) and packages (`rpkg`), communicating with the Porch API server to perform operations like registration, cloning, and lifecycle management.

*See also*: [Porch](#porch)

### kpt

An open-source tool for managing bundles of Kubernetes resource configurations (kpt packages) using Configuration as Data methodology. Porch provides kpt functionality as a Kubernetes API service.

*See also*: [Porch](#porch), [Package](#package)

---

## Technical Terms

Technical concepts and implementation details.

### KRM

Kubernetes Resource Model - the declarative, intent-based API model and machinery underlying Kubernetes. KRM defines how resources are structured, validated, and managed through the Kubernetes API.

### KRM Function

An executable that takes Kubernetes resources as input and produces Kubernetes resources as output. Functions can add, remove, or modify resources. In Porch, functions are declared in a package's Kptfile and executed during rendering.

*See also*: [Function Runner](#function-runner), [Pod Evaluator](#pod-evaluator), [Rendering](#rendering)

### Rendering

The process of executing KRM functions defined in a package's Kptfile pipeline. Rendering occurs automatically when Draft packages are modified (via push operations). Function results are stored in the PackageRevisionResources API resource's `status.renderStatus` field.

By default, render failures prevent resources from being persisted. The `porch.kpt.dev/push-on-render-failure` annotation can override this behavior to save work-in-progress packages even when rendering fails.

*See also*: [KRM Function](#krm-function), [Function Runner](#function-runner), [Pod Evaluator](#pod-evaluator), [Push on Render Failure](#push-on-render-failure)

### Push on Render Failure

An annotation (`porch.kpt.dev/push-on-render-failure: "true"`) that enables persisting Draft PackageRevision resources even when the kpt function render pipeline fails during resource updates. This allows saving work-in-progress packages during iterative development when the pipeline isn't fully functional yet. It does not apply to package creation operations (clone, init, edit, copy).

The behavior of partially-rendered resources can be further controlled via Kptfile annotations (see [kpt documentation](https://kpt.dev/book/04-using-functions/#debugging-render-failures)).

*See also*: [Rendering](#rendering), [Draft](#draft)

### Task

A record of an operation performed on a PackageRevision, stored in the `spec.tasks` array. Task types include init, clone, edit, upgrade, render, and push. Tasks provide an audit trail of package operations.

*See also*: [PackageRevision](#packagerevision)

### Custom Resource (CR)

A resource in a Kubernetes API server with a Group/Version/Kind, added via a Custom Resource Definition. Porch's PackageRevision, Repository, and PackageVariant are all custom resources.

*See also*: [Custom Resource Definition](#custom-resource-definition)

### Custom Resource Definition (CRD)

A built-in Kubernetes resource used to define custom resources within an API server. CRDs extend Kubernetes functionality by adding new resource types with defined schemas and API endpoints.

*See also*: [Custom Resource](#custom-resource)

---

## Abbreviations

### Porch-Related
- **CaD**: Configuration as Data
- **CR**: Custom Resource
- **CRD**: Custom Resource Definition
- **KRM**: Kubernetes Resource Model
- **PV**: PackageVariant
- **PVS**: PackageVariantSet

### Kubernetes-Related
- **API**: Application Programming Interface
- **RBAC**: Role-Based Access Control
- **YAML**: YAML Ain't Markup Language (recursive acronym)
- **OCI**: Open Container Initiative
- **gRPC**: gRPC Remote Procedure Call (recursive acronym)
