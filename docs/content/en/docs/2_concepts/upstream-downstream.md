---
title: "Upstream & Downstream"
type: docs
weight: 6
description: |
  Understanding upstream and downstream packages: the source/derivation relationship in Porch.
---

## What are Upstream and Downstream Packages?

**Upstream** and **downstream** describe source-to-derivation relationship between packages and package revisions in Porch.
This relationship is fundamental to package reuse, customization, upgrade control, and lifecycle management.

- **Upstream package**: The source or template package that provides the base content
- **Downstream package**: A derived package created by cloning or copying from an upstream package

This relationship enables package reuse across teams and environments, centralized maintenance of template packages, tracking of package lineage and updates, and automated propagation of upstream changes to downstream packages.

## How the Relationship Works

When you create a downstream package revision from an upstream package revision, Porch establishes and tracks this relationship.
The downstream package revision maintains a record of its upstream source, including the exact version (Git commit) it was
derived from.

This tracking enables **lineage visibility** (knowing where a package revision came from), **update awareness** (detecting when upstream has new versions), **merge intelligence** (preserving downstream customizations when upgrading to new upstream versions), and an **audit trail** (understanding how a package has evolved over time).

## Common Patterns

### Blueprint → Deployment

The most common pattern in Porch is:

- **Blueprint repositories** (upstream) contain reusable template packages, are maintained by platform teams, and represent best practices and organizational standards. Some examples are application templates, infrastructure patterns, and policy bundles.
- **Deployment repositories** (downstream) contain environment-specific packages that are created by cloning from blueprints and customized for specific clusters, regions, or teams. Some examples are prod-app, staging-database, and dev-cluster-config.

### Multi-Level Derivation

A package revision can be both upstream and downstream simultaneously:

- **Base template** (blueprint repo) → upstream for regional template
- **Regional template** (regional repo) → upstream for environment-specific packages
- **Environment package** (deployment repo) → final deployed configuration

Each level adds its own customizations while maintaining the chain of upstream relationships.

## Upstream Changes and Downstream Updates

When an upstream package publishes a new version:

**Manual workflow**: Users can choose when to upgrade their downstream packages to incorporate upstream changes. Porch
merges upstream updates with downstream customizations.

**Automated workflow**: PackageVariant controllers can automatically detect upstream changes and create new downstream
package revisions, applying customizations consistently.

**Customization preservation**: Downstream-specific modifications (labels, resources, function configurations) are preserved
during upstream updates through intelligent merge strategies.

## Upstream Sources

Upstream packages can originate from **registered repositories** (packages in repositories that Porch manages), **external Git repositories** (direct references to Git repos not registered in Porch), or **external OCI registries** (direct references to OCI artifacts).

This flexibility allows organizations to consume packages from various sources while maintaining consistent downstream
management through Porch.

## Key Points

- Upstream == source package; Downstream == derived package
- Porch tracks the upstream → downstream relationship automatically
- Common pattern: blueprints (upstream) → deployments (downstream)
- Downstream packages can be updated when upstream changes
- Customizations in downstream packages are preserved during updates
- A package can be both upstream (to others) and downstream (from another)
- PackageVariant automates the upstream → downstream lifecycle
- **Deletion protection**: Upstream PackageRevisions cannot be deleted while downstream PackageRevisions exist

## Deletion Protection

Porch prevents deletion of upstream PackageRevisions when downstream PackageRevisions exist.

**What counts as downstream**: any PackageRevision in the same namespace, in any lifecycle, that:
- was cloned from the upstream or upgraded to it
- is a copy of such a PackageRevision
- has a subpackage cloned from the upstream or upgraded to it

Copies and subpackages count only when their Kptfile names the upstream PackageRevision. Porch records
the name when it clones or upgrades from a registered repository. Packages cloned from a Git URL do not
count. The `main` branch PackageRevision of a package never counts.

**Protection mechanism**: Proposing deletion of, or deleting, an upstream PackageRevision with existing
downstream PackageRevisions will:
- Fail with a Forbidden error. A refused proposal leaves the upstream Published.
- Name the blocking downstream PackageRevisions, up to 20, with the subpackage directory where relevant
- Require the downstream PackageRevisions to be deleted first. A subpackage in a Draft can instead be
  removed, or upgraded to another upstream.

**Deletion order**: Always delete from downstream to upstream:
1. Delete all downstream PackageRevisions
2. Delete the upstream PackageRevision

**Example**:
```
blueprints/nginx-template:v1 (upstream)
         ↓
deployments/prod-nginx:v1 (downstream)
```

To delete nginx-template:v1, first delete prod-nginx:v1, then delete nginx-template:v1.

This ensures downstream packages don't lose their source reference.
