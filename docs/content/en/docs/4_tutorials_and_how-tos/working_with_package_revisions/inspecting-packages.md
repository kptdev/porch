---
title: "Getting Package Revisions"
type: docs
weight: 3
description: "A guide to getting/listing, reading, querying, and inspecting package revisions in Porch"
---



---

## Basic Operations

These operations cover the fundamental commands for viewing and inspecting package revisions in Porch.

### Getting All Package Revisions

Get all package revisions across all repositories in a namespace:

```bash
porchctl rpkg get --namespace default
```

This command queries Porch for all PackageRevisions in the specified namespace and displays a summary table with key information. It shows PackageRevisions from all registered repositories in that namespace.

{{% alert title="Note" color="primary" %}}
`porchctl rpkg list` is an alias for `porchctl rpkg get` and can be used interchangeably: 

```bash
porchctl rpkg list --namespace default
```

{{% /alert %}}

Example output:

```bash
NAME                             PACKAGE            WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.my-app.v1             my-app             v1              1          true     Published   porch-test
porch-test.my-app.v2             my-app             v2              0          false    Draft       porch-test
blueprints.nginx.main            nginx              main            5          true     Published   blueprints
blueprints.postgres.v1           postgres           v1              0          false    Proposed    blueprints
```

Understanding the output:

| Column | Description | Example |
|--------|-------------|---------|
| **NAME** | Full package revision identifier: `repository.([pathnode.]*)package.workspace`, i.e. `<repository>.[<path-nodes>.]<package>.<workspace>` where path nodes reflect directory structure under the repo. | `porch-test.basedir.subdir.edge-function.v1` |
| **PACKAGE** | Package name, including subdirectory path when the package is not at the repository root. | `basedir/subdir/network-function` |
| **WORKSPACENAME** | User-chosen identifier for this PackageRevision, scoped to the package (so `v1` in package A is unrelated to `v1` in package B); corresponds to the Git branch or tag name. | `v1` |
| **REVISION** | Version number indicating publication status. <br> `1+` = published (increments with each publish: 1, 2, 3…) <br> `0` = unpublished (Draft or Proposed) <br> `-1` = placeholder pointing at Git branch/tag head. | `2` (published), `0` (unpublished), `-1` (placeholder) |
| **LATEST** | Indicates the latest published PackageRevision for the package (only one; highest revision number). | `true` / `false` |
| **LIFECYCLE** | Current state of the PackageRevision. <br> **Draft** — in progress, editable, visible to authors <br> **Proposed** — read-only, pending approval (approve or reject) <br> **Published** — immutable, production-ready, has revision numbers <br> **DeletionProposed** — marked for removal, pending deletion approval. | `Draft`, `Proposed`, `Published`, `DeletionProposed` |
| **REPOSITORY** | Name of the source repository. | `porch-test` |
---

### Get Detailed PackageRevision Information

Get complete details about a specific PackageRevision:

```bash
porchctl rpkg get porch-test.my-app.v1 --namespace default -o yaml
```

This command retrieves the full PackageRevision manifest, including all metadata, spec, and status fields. Displayed in YAML format for easy reading.

Example output:

```yaml
apiVersion: porch.kpt.dev/v1alpha1
kind: PackageRevision
metadata:
  creationTimestamp: "2025-11-24T13:00:14Z"
  labels:
    kpt.dev/latest-revision: "true"
  name: porch-test.my-first-package.v1
  namespace: default
  resourceVersion: 5778e0e3e9a92d248fec770cef5baf142958aa54
  uid: f9f6507d-20fc-5319-97b2-6b8050c4f9cc
spec:
  lifecycle: Published
  packageName: my-first-package
  repository: porch-test
  revision: 1
  tasks:
  - init:
      description: My first Porch package
    type: init
  workspaceName: v1
status:
  publishTimestamp: "2025-11-24T16:38:41Z"
  upstreamLock: {}
```

Key fields to inspect:

- **spec.lifecycle**: Current PackageRevision state
- **spec.tasks**: History of operations performed on this PackageRevision
- **status.publishTimestamp**: When the PackageRevision was published

{{% alert title="Tip" color="primary" %}}
Use `jq` to extract specific fields: `porchctl rpkg get <name> -n default -o json | jq '.metadata'`
{{% /alert %}}

---

### Viewing the Root Kptfile

Display the root Kptfile of a specific package revision without downloading the entire package

```bash
porchctl rpkg get porch-test.my-app.v1 --show-kptfile --namespace default
```

This command fetches the PackageRevisionResources for the specified package revision and extracts and displays only the root `Kptfile`. It is useful for quickly inspecting package metadata, pipeline configuration, and status.

Example output:

```yaml
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: my-app
info:
  description: My application package
pipeline:
  mutators:
  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.5
    configMap:
      namespace: production
status:
  conditions:
  - type: Rendered
    status: "True"
    reason: RenderSuccess
```

{{% alert title="Note" color="primary" %}}
`--show-kptfile` requires exactly one package revision name and cannot be combined with `--name`, `--revision`, `--workspace`, or `--all-namespaces`.
{{% /alert %}}

---

### Reading PackageRevision Resources

Read the actual contents of a PackageRevision:

```bash
porchctl rpkg read porch-test.my-first-package.v1 --namespace default
```

This command fetches PackageRevision resources and outputs to stdout, showing all KRM resources in ResourceList format. It displays the complete PackageRevision content.

Example output:

```yaml
apiVersion: config.kubernetes.io/v1
kind: ResourceList
items:
- apiVersion: ""
  kind: KptRevisionMetadata
  metadata:
    name: porch-test.my-first-package.v1
    namespace: default
    creationTimestamp: "2025-11-24T13:00:14Z"
    resourceVersion: 5778e0e3e9a92d248fec770cef5baf142958aa54
    uid: f9f6507d-20fc-5319-97b2-6b8050c4f9cc
    annotations:
      config.kubernetes.io/path: '.KptRevisionMetadata'
- apiVersion: kpt.dev/v1
  kind: Kptfile
  metadata:
    name: my-first-package
    annotations:
      config.kubernetes.io/local-config: "true"
      config.kubernetes.io/path: 'Kptfile'
  info:
    description: My first Porch package
  pipeline:
    mutators:
    - image: gcr.io/kpt-fn/set-namespace:v0.4.1
      configMap:
        namespace: production
- apiVersion: v1
  kind: ConfigMap
  metadata:
    name: kptfile.kpt.dev
    annotations:
      config.kubernetes.io/local-config: "true"
      config.kubernetes.io/path: 'package-context.yaml'
  data:
    name: example
- apiVersion: v1
  kind: ConfigMap
  metadata:
    name: test-config
    namespace: production
    annotations:
      config.kubernetes.io/path: 'test-config.yaml'
  data:
    Key: "Value"
```

---

## Advanced Filtering

Porch provides multiple ways to filter PackageRevisions. You can either use `porchctl`'s built-in flags, Kubernetes label selectors, or field selectors depending on your needs.

### Using Porchctl Flags

Filter by package name (substring match):

```bash
porchctl rpkg get --namespace default --name my-app
```

Filter by revision number (exact match):

```bash
porchctl rpkg get --namespace default --revision 1
```

Filter by workspace name:

```bash
porchctl rpkg get --namespace default --workspace v1
```

Example output:

```bash
$ porchctl rpkg get --namespace default --name network-function
NAME                                    PACKAGE            WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.network-function.v1          network-function   v1              1          false    Published   porch-test
porch-test.network-function.v2          network-function   v2              2          true     Published   porch-test
porch-test.network-function.main        network-function   main            0          false    Draft       porch-test
```

---

### Using Kubectl Label Selectors

Filter using Kubernetes [labels](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#list-and-watch-filtering) with the `--selector` flag:

Get all "latest" published PackageRevisions:

```bash
kubectl get packagerevisions -n default --selector 'kpt.dev/latest-revision=true'
```

Example output:

```bash
$ kubectl get packagerevisions -n default --show-labels --selector 'kpt.dev/latest-revision=true'
NAME                        PACKAGE   WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY        LABELS
porch-test.my-app.v2        my-app    v2              2          true     Published   porch-test        kpt.dev/latest-revision=true
blueprints.nginx.main       nginx     main            5          true     Published   blueprints        kpt.dev/latest-revision=true
```

{{% alert title="Note" color="primary" %}}
PackageRevision resources have limited labels. To filter by repository, package name, or other attributes, use `--field-selector` instead (see next section).
{{% /alert %}}

---

### Using Kubectl Field Selectors

Filter using PackageRevision [fields](https://kubernetes.io/docs/concepts/overview/working-with-objects/field-selectors/) with the `--field-selector` flag:

Supported fields:

- `metadata.name`
- `metadata.namespace`
- `spec.revision`
- `spec.packageName`
- `spec.repository`
- `spec.workspaceName`
- `spec.lifecycle`

Filter by repository:

```bash
kubectl get packagerevisions -n default --field-selector 'spec.repository==porch-test'
```

Filter by lifecycle:

```bash
kubectl get packagerevisions -n default --field-selector 'spec.lifecycle==Published'
```

Filter by package name:

```bash
kubectl get packagerevisions -n default --field-selector 'spec.packageName==my-app'
```

Combine multiple filters:

```bash
kubectl get packagerevisions -n default \
  --field-selector 'spec.repository==porch-test,spec.lifecycle==Published'
```

Example output:

```bash
$ kubectl get packagerevisions -n default --field-selector 'spec.repository==porch-test'
NAME                             PACKAGE            WORKSPACENAME   REVISION   LATEST   LIFECYCLE   REPOSITORY
porch-test.my-app.v1             my-app             v1              1          false    Published   porch-test
porch-test.my-app.v2             my-app             v2              2          true     Published   porch-test
porch-test.my-service.main       my-service         main            3          true     Published   porch-test
```

{{% alert title="Note" color="primary" %}}
The `--field-selector` flag supports only the `=` and `==` operators. **The `!=` operator is not supported** due to Porch's internal caching behavior.
{{% /alert %}}

---

## Additional Operations

Beyond basic listing and filtering, these operations help you monitor changes and format output.

### Watch for PackageRevision Changes

```bash
kubectl get packagerevisions -n default --watch
```

### Sort by Creation Time

```bash
kubectl get packagerevisions -n default --sort-by=.metadata.creationTimestamp
```

### Output Formatting

Both `porchctl` and `kubectl` support standard Kubernetes [output formatting flags](https://kubernetes.io/docs/reference/kubectl/#output-options):

- `-o yaml` - YAML format
- `-o json` - JSON format
- `-o wide` - Additional columns
- `-o name` - Resource names only
- `-o custom-columns=...` - Custom column output

{{% alert title="Note" color="primary" %}}
For a complete reference of all available command options and flags, see the [Porch CLI Guide]({{% relref "/docs/7_cli_api/porchctl.md" %}}).
{{% /alert %}}

---
