---
title: "Design"
type: docs
weight: 2
description: |
  Design patterns and architecture of the Porch API server.
---

## REST Storage Interface

See [Functionality]({{% relref "/docs/5_architecture_and_components/porch-apiserver/functionality.md#rest-storage-implementation" %}}) for detailed CRUD operations and storage implementations.

The Porch API Server implements Kubernetes' REST storage interface to provide custom storage backends for Porch resources. Unlike standard Kubernetes resources that store data in etcd, Porch resources delegate to the Engine which manages package data in Git repositories through the Cache.

The Storage Interface implements standard Kubernetes storage. It provides CRUD operations (Create, Get, List, Update, Delete), supports Watch for real-time change notifications and delegates all operations to CaD Engine. However, it has no direct etcd storage, as the packages are stored in Git.

- **packageRevisions** manage PackageRevision resources
- **packageRevisionResources** manage PackageRevisionResources (package content)
- **packages** manage Package resources.

## Strategy Pattern

See [Functionality]({{% relref "/docs/5_architecture_and_components/porch-apiserver/functionality.md#validation-strategies" %}}) for detailed validation rules and processes.

The API Server uses Kubernetes' strategy pattern to customize resource behavior:

### Validation Strategy

The purpose of the this strategy is to validate resource specifications before persistence. It has three types: create, update and status. The create validation ensures that the fields present and the lifecycle constraints enforced. The update validation checks resource version, lifecycle transitions and immutability rules. And the status validation checks status subresource updates.

### Admission Strategy

The purpose of this strategy is to apply admission control policies and defaults. There are three admission operations: PrepareForCreate, PrepareForUpdate and Canonicalize. PrepareForCreate sets defaults, generates names and initializes status. PrepareForUpdate validates resource version and enforces immutability. Canonicalize normalizes resource representation.

### Table Conversion Strategy

The purpose of the table conversion strategy is to convert resources to table format for `kubectl` display. The table conversion defines columns for `kubectl` output (Name, Package, Workspace, Revision, Lifecycle), extracts values from resource specifications, and formats data for human-readable display. It supports both list and individual resource views.

## API Groups

The Porch API Server registers two API groups with Kubernetes:

### porch.kpt.dev API Group (Aggregated API)

**Resources:**
- **PackageRevision**: Represents a specific revision of a package
- **PackageRevisionResources**: Contains the actual resource content of a package revision
- **Package**: Represents a package across all its revisions

**Versions:** v1alpha1: Current version with all resources

**Characteristics:** The porch.kpt.dev API group is the primary API group for package management, and all its resources are namespaced. It is served via Kubernetes API aggregation, not CRDs, and supports full CRUD and Watch operations. This group integrates with Engine for all operations and uses custom REST storage, which is Git-backed, not etcd.

### config.porch.kpt.dev API Group (CRDs)

**Resources:**
- **Repository**: Configures Git repositories for package storage
- **PackageRev**: Internal metadata resource for tracking package revisions

**Versions:** v1alpha1: Current version for all resources

**Characteristics:** The config.porch.kpt.dev API group serves as the configuration API group for repository management, with all its resources being namespaced. It is implemented as standard Kubernetes CRDs and is managed by separate controllers, not directly by the API server. Its data is stored in etcd, which is the standard Kubernetes CRD storage. Within this group, PackageRev is used an internal resource specifically for metadata tracking.

## Background Operations

The Porch API Server delegates background operations to separate controller components for better separation of concerns and scalability. Repository synchronization and lifecycle management are handled by the [Repository Controller]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller/_index.md" %}}), which runs as a separate component using the controller-runtime framework.

See [Functionality]({{% relref "/docs/5_architecture_and_components/porch-apiserver/functionality.md#background-operations" %}}) for detailed implementation of other background operations including resource cleanup.

## Design Decisions

### REST Storage vs etcd

Instead of using etcd, custom REST storage is implemented that delegates to Engine.

Package data naturally lives in Git repositories, and etcd is not suitable for storing large package content. The Engine provides the necessary abstraction over Git, which enables a draft-commit workflow for package modifications.

Using etcd would require duplicating package content, causing large storage overhead. As an alternative, a hybrid approach was considered as well. Metadata would be stored in etcd, content in Git. However, that would add complexity.

This decision has some trade-offs. Custom storage, while more complex than standard etcd, offers the significant advantages of enabling Git-native package management and providing better scalability for large packages.

### Strategy-Based Validation

Kubernetes strategy pattern for validation and admission control is used in the API server.

Following Kubernetes conventions, this approach separates validation logic from storage logic, which enables reuse across different storage implementations and provides consistent validation behavior.

The API server uses strategy-based validation for its own resources. Additionally, validation webhooks run in the controllers to validate controller-managed resources (PackageRevision and Repository CRDs):

- **PackageRevision Webhook**: Validates v1alpha2 PackageRevision resources (lifecycle transitions, immutability, render race prevention)
- **Repository Webhook**: Validates Repository resources (git conflict detection, directory nesting prevention)

These webhooks provide real-time admission-time validation with immediate user feedback at the kubectl command line.

This decision has some trade-offs. The strategy pattern, while adding an abstraction layer, provides a clean separation of concerns and enables independent testing of validation. Webhooks add minimal network overhead while providing immediate user feedback for controller-managed resources.

### Watch via WatcherManager

Watch streams using Engine's WatcherManager is implemented.

The engine knows when package revisions change. Combined with the WatcherManager's efficient fan-out, this solution enables real-time notifications while avoiding the overhead of polling or etcd watches.

Two alternatives were considered, either etcd watch or polling. However, etcd watch requires storing all data in etcd, while polling is inefficient and causes high latency.

This decision has some trade-offs. A custom watch implementation, while more complex to develop, offers efficient real-time updates and scales effectively to support many concurrent watchers.

### Repository Management Pattern

Repository synchronization is extracted into a dedicated Repository Controller using `controller-runtime` framework.

This design separates concerns by dedicating the API server to request handling and the controller to managing the repository lifecycle. Leveraging `controller-runtime` provides proven patterns for watch management, work queues, and leader election, leading to better scalability through concurrent reconciliation and rate limiting. This architecture also allows for independent deployment and scaling of repository management, and facilitates cleaner shutdown and error handling.

Three alternatives were considered: background goroutines in the API server, sync on demand or the implementation of custom controller. However, background goroutines mix request handling with background sync logic, sync on demand adds latency to API requests and custom controller implementation reinvents controller-runtime features.

This decision has some trade-offs. The introduction of a dedicated Repository Controller as an additional deployment component offers significant advantages, including a better separation of concerns, enhanced operational flexibility, and improved observability through specialized controller metrics and status reporting.

The Repository Controller manages Repository CRs through standard Kubernetes reconciliation to manage Repository Custom Resources (CRs). Its core functions include watching for changes in Repository specifications, conducting scheduled health checks and full synchronizations, updating the repository status with synchronization results and package metadata, and managing repository deletion and cache cleanup. For more information, see [Repository Controller]({{% relref "/docs/5_architecture_and_components/controllers/repository-controller/_index.md" %}}).

### Dependency Injection

Engine, Cache, and clients are configured through dependency injection.

This approach is justified by its ability to enable testing with mock implementations, provide flexible configuration options, separate the construction of components from their usage, and support various deployment scenarios.

Two alternatives were considered. Either global singletons or a service locator. However, global singletons are hard to test and configure, and a service locator hides dependencies.

This decision has some trade-offs. While it requires explicit wiring during initialization, it offers the significant benefits of a clear dependency graph and enables flexible testing and configuration.