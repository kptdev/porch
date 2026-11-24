---
title: "Package Variants and Package Variant Sets"
type: docs
weight: 8
description: |
  Understanding PackageVariant and PackageVariantSet: automation for package cloning and lifecycle management.
---

## What are PackageVariants?

A **PackageVariant** is a Kubernetes custom resource that automates the creation and maintenance of downstream package
revisions based on an upstream package. It establishes a relationship between an upstream (source) package and one or
more downstream (target) packages, automatically keeping them synchronized.

PackageVariants enable:
- Automatic cloning of package revisions from upstream to downstream repositories
- Continuous tracking of package revisions of the upstream package
- Automated creation of new downstream revisions when a new revision of the upstream is published
- Customization of downstream package revisions through injectors, functions, and package context

## How PackageVariants Work

Porch's PackageVariant controller watches for changes to:
1. The PackageVariant resource itself
2. The upstream PackageRevision it references
3. The downstream PackageRevisions it owns

When changes occur, the controller creates a new downstream PackageRevision if none exists (via clone task), updates existing downstream PackageRevisions when upstream changes (via upgrade task), applies customizations specified in the PackageVariant spec (functions, injectors, labels, annotations), and manages the lifecycle of downstream PackageRevisions (draft → published).

## PackageVariant Spec

A PackageVariant defines:

**Upstream**: The source PackageRevision to track
```yaml
upstream:
  repo: blueprints
  package: my-app
  revision: v1  # optional: tracks latest if omitted
```

**Downstream**: The target PackageRevision to create/maintain
```yaml
downstream:
  repo: deployments
  package: prod-my-app
```

**Customizations**: How to modify the downstream PackageRevision
- `pipeline`: Additional KRM functions (mutators/validators) to inject into the Kptfile
- `injectors`: References to cluster objects whose data should be injected into the PackageRevision
- `packageContext`: Key-value data to add/remove from the package-context.yaml ConfigMap
- `labels` and `annotations`: Metadata to apply to the downstream PackageRevision

**Policies**:
- `adoptionPolicy`: Whether to adopt existing PackageRevisions (`adoptNone` or `adoptExisting`)
- `deletionPolicy`: Whether to delete or orphan downstream PackageRevisions when the PackageVariant is deleted (`delete`
  or `orphan`)

## PackageVariant Lifecycle

The PackageVariant controller manages downstream PackageRevisions through their lifecycle:

1. **Initial creation**: Creates a Draft PackageRevision with a clone task
2. **Customization**: Applies functions, injectors, and package context modifications
3. **Upstream updates**: When upstream changes, creates a new Draft with an upgrade task
4. **Downstream modifications**: When customizations change, creates a new Draft with an edit task
5. **Cleanup**: When PackageVariant is deleted, deletes or orphans downstream PackageRevisions based on deletionPolicy

## What are PackageVariantSets?

**PackageVariantSet** is a higher-level resource that creates multiple PackageVariants from a single upstream package to
multiple downstream targets. It's essentially a template for generating PackageVariants.

PackageVariantSets enable:
- One-to-many package distribution (one upstream → many downstreams)
- Dynamic target selection via label selectors
- Templated downstream package naming and customization
- Centralized management of multiple PackageVariants

## PackageVariantSet Spec

A PackageVariantSet defines:

**Upstream**: The source PackageRevision (same as PackageVariant)

**Targets**: How to select downstream repositories
- `repositories`: Explicit list of Repository resources
- `repositorySelector`: Label selector to dynamically select repositories
- `objectSelector`: Selector against arbitrary cluster objects

**Template**: How to generate each PackageVariant
- Defines the spec for generated PackageVariants
- Supports CEL expressions for dynamic values (e.g., `packageExpr`, `repoExpr`)
- Can customize labels, annotations, pipeline, injectors per target

## PackageVariantSet Behavior

The PackageVariantSet controller evaluates target selectors to determine downstream repositories, creates a PackageVariant resource for each target, applies the template to customize each PackageVariant, manages the lifecycle of generated PackageVariants, and updates PackageVariants when the PackageVariantSet changes.

## Status

`status.downstreamTargets` lists downstream PackageRevisions the controller created or adopted. Each entry includes `renderStatus` with the same fields as PackageRevisionResources: `errorSummary`, `mutationSteps`, and `validationSteps`. The former `error` and `result` fields were removed.

## Key Points

- PackageVariant automates the upstream → downstream relationship between package revisions
- The controller creates and updates downstream PackageRevisions automatically
- Customizations (functions, injectors, context) are applied to downstream PackageRevisions
- PackageVariantSet creates multiple PackageVariants from a single definition
- Both resources use Kubernetes controller patterns for reconciliation
- Adoption and deletion policies control how existing PackageRevisions are handled on PackageVariant creation and deletion
