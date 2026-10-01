---
title: "Webhook Validation Rules"
type: docs
weight: 2
description: "Detailed validation rules for Porch webhooks"
---

Webhooks validate resources at admission time, before they're written to Kubernetes etcd. This means invalid configurations are rejected immediately, preventing invalid state from entering the system. The validation rules below are enforced by two webhooks: one for PackageRevision v1alpha2 resources and one for Repository resources.

## PackageRevision Validation

### CREATE

When creating a PackageRevision, the webhook validates:

**Repository** must exist in the same namespace and have the `porch.kpt.dev/v1alpha2-migration: "true"` annotation. If missing, you'll see the `Repository {namespace}/{name} not found` or `not enabled for v1alpha2` error messages. Add the annotation to the Repository resource.

**Lifecycle** must be `Draft` or `Proposed`. Use `Draft` for work-in-progress or `Proposed` to request approval immediately. Creating directly as `Published` or `DeletionProposed` is rejected.

**Source** must specify exactly one of: `init` (new), `cloneFrom` (clone), `copyFrom` (copy), or `upgrade` (upgrade). You'll get an error if none or multiple are set.

**Workspace** must be unique within the package. If you receive the `workspace 'drafting' already exists for package 'example-package'` error message, choose a different name or delete the existing draft.

### UPDATE

**Immutable fields** (`spec.source`, `spec.packageName`, `spec.repository`, `spec.workspaceName`) cannot be changed after creation. If you need to change these, delete and recreate the PackageRevision.

**Lifecycle transitions** must follow the valid state machine: `Draft → Proposed → Published → DeletionProposed`. You cannot skip states (e.g., `Draft` directly to `Published`) or downgrade (e.g., `Published` back to `Draft`).

**Render race prevention** blocks lifecycle changes while render is in progress or before content has been rendered. Wait for rendering to complete or push new content before changing lifecycle.

### DELETE

**Upstream references** prevent deletion if other PackageRevisions reference this one as upstream (via `cloneFrom` or `upgrade`). Delete referencing packages first, or carefully use cascade deletion.

**Published packages** require moving to `DeletionProposed` first via a lifecycle transition, then delete. Draft packages can be deleted immediately.

---

## Repository Validation

### CREATE/UPDATE

**Git conflict detection** prevents multiple repositories from claiming the same git location (URL + branch + directory). If you see the `Repository conflict with existing repository: {namespace}/{name}` error message, use a different branch, directory, or namespace.

**Directory nesting** is also prevented. A root directory conflicts with any subdirectory under the same URL+branch, and subdirectories cannot be nested under each other. For example, you cannot have both `/` and `/foo` or both `/foo` and `/foo/bar` for the same URL+branch combination.

**OCI repositories** don't have these constraints since they're endpoint-based rather than directory-based. Multiple OCI repositories can use the same URL if they reference different endpoints.

---

## Common Issues

**Repository not found or not enabled for v1alpha2**: Verify the repository exists with `kubectl get repositories -n namespace`. If it exists, add the migration annotation: `kubectl annotate repository repo -n namespace porch.kpt.dev/v1alpha2-migration=true`

**Workspace already exists**: Choose a different workspace name or delete the existing draft. List with `kubectl get packagerevisions -n namespace` to see what workspaces are in use.

**Invalid lifecycle transition**: Follow the valid path: Draft → Proposed → Published. Use `porchctl rpkg propose` then `porchctl rpkg approve` rather than manually patching lifecycle.

**Cannot change lifecycle during render**: Wait for rendering to complete by checking `kubectl describe packagerevision <name> -n namespace`, or push new content and retry.

**Repository conflict**: List existing repositories with `kubectl get repositories -n namespace` and use a different branch, directory, or namespace for your new repository.

---

## Performance

Webhooks timeout after 30 seconds per operation, with expected latency <100ms p99. If you see timeouts, check pod resources (CPU/memory), etcd performance, network latency to the webhook endpoint, and webhook logs for errors.

---

## See Also

- [Webhook Architecture](./_index.md)
- [Certificate Management](./cert-manager-webhooks.md)
