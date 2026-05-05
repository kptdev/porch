---
title: "API Reference"
type: docs
weight: 2
description: API reference documentation for Porch resources
---

## Packages
- [porch.kpt.dev/v1alpha1](#porchkptdevv1alpha1)


## porch.kpt.dev/v1alpha1




#### Condition





_Appears in:_
- [PackageRevisionStatus](#packagerevisionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ |  |  |  |
| `status` _[ConditionStatus](#conditionstatus)_ |  |  |  |
| `reason` _string_ |  |  |  |
| `message` _string_ |  |  |  |


#### ConditionStatus





_Appears in:_
- [Condition](#condition)

| Field | Description |
| --- | --- |
| `True` |  |
| `False` |  |
| `Unknown` |  |


#### Field

Field references a field in a resource



_Appears in:_
- [ResultItem](#resultitem)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `path` _string_ | Path is the field path. This field is required. |  |  |
| `currentValue` _string_ | CurrentValue is the current field value |  |  |
| `proposedValue` _string_ | ProposedValue is the proposed value of the field to fix an issue. |  |  |


#### File

File references a file containing a resource



_Appears in:_
- [ResultItem](#resultitem)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `path` _string_ | Path is relative path to the file containing the resource.<br />This field is required. |  |  |
| `index` _integer_ | Index is the index into the file containing the resource<br />(i.e. if there are multiple resources in a single file) |  |  |


#### GitLock

GitLock is the resolved locator for a package on Git.



_Appears in:_
- [Locator](#locator)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `repo` _string_ | Repo is the git repository that was fetched.<br />e.g. 'https://github.com/kubernetes/examples.git' |  |  |
| `directory` _string_ | Directory is the sub directory of the git repository that was fetched.<br />e.g. 'staging/cockroachdb' |  |  |
| `ref` _string_ | Ref can be a Git branch, tag, or a commit SHA-1 that was fetched.<br />e.g. 'master' |  |  |
| `commit` _string_ | Commit is the SHA-1 for the last fetch of the package.<br />This is set by kpt for bookkeeping purposes. |  |  |


#### GitPackage





_Appears in:_
- [UpstreamPackage](#upstreampackage)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `repo` _string_ | Address of the Git repository, for example:<br />  `https://github.com/GoogleCloudPlatform/blueprints.git` |  |  |
| `ref` _string_ | `Ref` is the git ref containing the package. Ref can be a branch, tag, or commit SHA. |  |  |
| `directory` _string_ | Directory within the Git repository where the packages are stored. A subdirectory of this directory containing a Kptfile is considered a package. |  |  |
| `secretRef` _[SecretRef](#secretref)_ | Reference to secret containing authentication credentials. Optional. |  |  |


#### Locator

Locator is a resolved locator for the last fetch of the package.



_Appears in:_
- [PackageRevisionStatus](#packagerevisionstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[OriginType](#origintype)_ | Type is the type of origin. |  |  |
| `git` _[GitLock](#gitlock)_ | Git is the resolved locator for a package on Git. |  |  |


#### NameMeta

NameMeta contains name information.



_Appears in:_
- [ResourceIdentifier](#resourceidentifier)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the metadata.name field of a Resource |  |  |
| `namespace` _string_ | Namespace is the metadata.namespace field of a Resource |  |  |


#### OriginType





_Appears in:_
- [Locator](#locator)



#### PackageCloneTaskSpec





_Appears in:_
- [Task](#task)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `upstreamRef` _[UpstreamPackage](#upstreampackage)_ | `Upstream` is the reference to the upstream package to clone. |  |  |


#### PackageEditTaskSpec





_Appears in:_
- [Task](#task)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceRef` _[PackageRevisionRef](#packagerevisionref)_ |  |  |  |


#### PackageInitTaskSpec

PackageInitTaskSpec defines the package initialization task.



_Appears in:_
- [Task](#task)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `description` _string_ | `Description` is a short description of the package. |  |  |
| `keywords` _string array_ | `Keywords` is a list of keywords describing the package. |  |  |
| `site` _string_ | `Site` is a link to page with information about the package. |  |  |


#### PackageMergeStrategy





_Appears in:_
- [PackageUpgradeTaskSpec](#packageupgradetaskspec)

| Field | Description |
| --- | --- |
| `resource-merge` |  |
| `fast-forward` |  |
| `force-delete-replace` |  |
| `copy-merge` |  |


#### PackageMetadata





_Appears in:_
- [PackageRevisionSpec](#packagerevisionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `labels` _object (keys:string, values:string)_ |  |  |  |
| `annotations` _object (keys:string, values:string)_ |  |  |  |


#### PackageRevision

PackageRevision



_Appears in:_
- [PackageRevisionList](#packagerevisionlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `spec` _[PackageRevisionSpec](#packagerevisionspec)_ |  |  |  |
| `status` _[PackageRevisionStatus](#packagerevisionstatus)_ |  |  |  |


#### PackageRevisionLifecycle





_Appears in:_
- [PackageRevisionSpec](#packagerevisionspec)

| Field | Description |
| --- | --- |
| `Draft` |  |
| `Proposed` |  |
| `Published` |  |
| `DeletionProposed` |  |




#### PackageRevisionRef

PackageRevisionRef is a reference to a package revision.



_Appears in:_
- [PackageEditTaskSpec](#packageedittaskspec)
- [PackageUpgradeTaskSpec](#packageupgradetaskspec)
- [UpstreamPackage](#upstreampackage)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | `Name` is the name of the referenced PackageRevision resource. |  |  |


#### PackageRevisionResources

PackageRevisionResources



_Appears in:_
- [PackageRevisionResourcesList](#packagerevisionresourceslist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `spec` _[PackageRevisionResourcesSpec](#packagerevisionresourcesspec)_ |  |  |  |
| `status` _[PackageRevisionResourcesStatus](#packagerevisionresourcesstatus)_ |  |  |  |




#### PackageRevisionResourcesSpec

PackageRevisionResourcesSpec represents resources (as ResourceList serialized as yaml string) of the PackageRevision.



_Appears in:_
- [PackageRevisionResources](#packagerevisionresources)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `packageName` _string_ | PackageName identifies the package in the repository. |  |  |
| `workspaceName` _string_ | WorkspaceName identifies the workspace of the package. |  |  |
| `revision` _integer_ | Revision identifies the version of the package. |  |  |
| `repository` _string_ | RepositoryName is the name of the Repository object containing this package. |  |  |
| `resources` _object (keys:string, values:string)_ | Resources are the content of the package. |  |  |


#### PackageRevisionResourcesStatus

PackageRevisionResourcesStatus represents state of the rendered package resources.



_Appears in:_
- [PackageRevisionResources](#packagerevisionresources)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `renderStatus` _[RenderStatus](#renderstatus)_ | RenderStatus contains the result of rendering the package resources. |  |  |


#### PackageRevisionSpec

PackageRevisionSpec defines the desired state of PackageRevision



_Appears in:_
- [PackageRevision](#packagerevision)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `packageName` _string_ | PackageName identifies the package in the repository. |  |  |
| `repository` _string_ | RepositoryName is the name of the Repository object containing this package. |  |  |
| `workspaceName` _string_ | WorkspaceName is a short, unique description of the changes contained in this package revision. |  |  |
| `revision` _integer_ | Revision identifies the version of the package. |  |  |
| `parent` _[ParentReference](#parentreference)_ | Deprecated. Parent references a package that provides resources to us |  |  |
| `lifecycle` _[PackageRevisionLifecycle](#packagerevisionlifecycle)_ |  |  |  |
| `tasks` _[Task](#task) array_ | The task slice holds zero or more tasks that describe the operations<br />performed on the packagerevision. The are essentially a replayable history<br />of the packagerevision,<br />Packagerevisions that were not created in Porch may have an<br />empty task list.<br />Packagerevisions created and managed through Porch will always<br />have either an Init, Edit, or a Clone task as the first entry in their<br />task list. This represent packagerevisions created from scratch, based<br />a copy of a different revision in the same package, or a packagerevision<br />cloned from another package.<br />Each change to the packagerevision will result in a corresponding<br />task being added to the list of tasks. It will describe the operation<br />performed and will have a corresponding entry (commit or layer) in git<br />or oci.<br />The task slice describes the history of the packagerevision, so it<br />is an append only list (We might introduce some kind of compaction in the<br />future to keep the number of tasks at a reasonable number). |  |  |
| `readinessGates` _[ReadinessGate](#readinessgate) array_ |  |  |  |
| `packageMetadata` _[PackageMetadata](#packagemetadata)_ |  |  |  |


#### PackageRevisionStatus

PackageRevisionStatus defines the observed state of PackageRevision



_Appears in:_
- [PackageRevision](#packagerevision)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `upstreamLock` _[Locator](#locator)_ | UpstreamLock identifies the upstream data for this package. |  |  |
| `selfLock` _[Locator](#locator)_ | SelfLock identifies the location of the current package's data |  |  |
| `publishedBy` _string_ | PublishedBy is the identity of the user who approved the packagerevision. |  |  |
| `publishTimestamp` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.34/#time-v1-meta)_ | PublishedAt is the time when the packagerevision were approved. |  |  |
| `deployment` _boolean_ | Deployment is true if this is a deployment package (in a deployment repository). |  |  |
| `conditions` _[Condition](#condition) array_ |  |  |  |


#### PackageSpec

PackageSpec defines the desired state of Package



_Appears in:_
- [PorchPackage](#porchpackage)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `packageName` _string_ | PackageName identifies the package in the repository. |  |  |
| `repository` _string_ | RepositoryName is the name of the Repository object containing this package. |  |  |


#### PackageStatus

PackageStatus defines the observed state of Package



_Appears in:_
- [PorchPackage](#porchpackage)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `latestRevision` _integer_ | LatestRevision identifies the package revision that is the latest<br />published package revision belonging to this package. Latest is determined by comparing<br />packages that have valid semantic version as their revision. In case of git backend, branch tracking<br />revisions like "main" and in case of oci backend, revisions tracking "latest" are not considered during<br />selection of the latest revision. |  |  |


#### PackageUpgradeTaskSpec





_Appears in:_
- [Task](#task)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `oldUpstreamRef` _[PackageRevisionRef](#packagerevisionref)_ | `OldUpstream` is the reference to the original upstream package revision that is<br />the common ancestor of the local package and the new upstream package revision. |  |  |
| `newUpstreamRef` _[PackageRevisionRef](#packagerevisionref)_ | `NewUpstream` is the reference to the new upstream package revision that the<br />local package will be upgraded to. |  |  |
| `localPackageRevisionRef` _[PackageRevisionRef](#packagerevisionref)_ | `LocalPackageRevisionRef` is the reference to the local package revision that<br />contains all the local changes on top of the `OldUpstream` package revision. |  |  |
| `strategy` _[PackageMergeStrategy](#packagemergestrategy)_ | 	Defines which strategy should be used to update the package. It defaults to 'resource-merge'.<br /> * resource-merge: Perform a structural comparison of the original /<br />   updated resources, and merge the changes into the local package.<br /> * fast-forward: Fail without updating if the local package was modified<br />   since it was fetched.<br /> * force-delete-replace: Wipe all the local changes to the package and replace<br />   it with the remote version.<br /> * copy-merge: Copy all the remote changes to the local package. |  |  |


#### ParentReference

Deprecated. ParentReference is a reference to a parent package



_Appears in:_
- [PackageRevisionSpec](#packagerevisionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the parent PackageRevision |  |  |




#### PorchPackage

Package



_Appears in:_
- [PorchPackageList](#porchpackagelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `spec` _[PackageSpec](#packagespec)_ |  |  |  |
| `status` _[PackageStatus](#packagestatus)_ |  |  |  |




#### ReadinessGate





_Appears in:_
- [PackageRevisionSpec](#packagerevisionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditionType` _string_ |  |  |  |


#### RenderStatus

RenderStatus represents the result of performing render operation
on a package resources.



_Appears in:_
- [PackageRevisionResourcesStatus](#packagerevisionresourcesstatus)
- [TaskResult](#taskresult)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `result` _[ResultList](#resultlist)_ |  |  |  |
| `error` _string_ |  |  |  |




#### RepositoryType





_Appears in:_
- [UpstreamPackage](#upstreampackage)

| Field | Description |
| --- | --- |
| `git` |  |
| `oci` |  |


#### ResourceIdentifier

ResourceIdentifier contains the information needed to uniquely
identify a resource in a cluster.



_Appears in:_
- [ResultItem](#resultitem)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the metadata.name field of a Resource |  |  |
| `namespace` _string_ | Namespace is the metadata.namespace field of a Resource |  |  |


#### Result

Result contains the structured result from an individual function



_Appears in:_
- [ResultList](#resultlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ | Image is the full name of the image that generates this result<br />Image and Exec are mutually exclusive |  |  |
| `exec` _string_ | ExecPath is the the absolute os-specific path to the executable file<br />If user provides an executable file with commands, ExecPath should<br />contain the entire input string. |  |  |
| `stderr` _string_ | Enable this once test harness supports filepath based assertions.<br />Pkg is OS specific Absolute path to the package.<br />Pkg string `yaml:"pkg,omitempty"`<br />Stderr is the content in function stderr |  |  |
| `exitCode` _integer_ | ExitCode is the exit code from running the function |  |  |
| `results` _[ResultItem](#resultitem) array_ | Results is the list of results for the function |  |  |


#### ResultItem

ResultItem defines a validation result



_Appears in:_
- [Result](#result)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `message` _string_ | Message is a human readable message. This field is required. |  |  |
| `severity` _string_ | Severity is the severity of this result |  |  |
| `resourceRef` _[ResourceIdentifier](#resourceidentifier)_ | ResourceRef is a reference to a resource.<br />Required fields: apiVersion, kind, name. |  |  |
| `field` _[Field](#field)_ | Field is a reference to the field in a resource this result refers to |  |  |
| `file` _[File](#file)_ | File references a file containing the resource this result refers to |  |  |
| `tags` _object (keys:string, values:string)_ | Tags is an unstructured key value map stored with a result that may be set<br />by external tools to store and retrieve arbitrary metadata |  |  |


#### ResultList

ResultList contains aggregated results from multiple functions



_Appears in:_
- [RenderStatus](#renderstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `exitCode` _integer_ | ExitCode is the exit code of kpt command |  |  |
| `items` _[Result](#result) array_ | Items contain a list of function result |  |  |


#### SecretRef





_Appears in:_
- [GitPackage](#gitpackage)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the secret. The secret is expected to be located in the same namespace as the resource containing the reference. |  |  |




#### Task





_Appears in:_
- [PackageRevisionSpec](#packagerevisionspec)
- [TaskResult](#taskresult)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[TaskType](#tasktype)_ |  |  |  |
| `init` _[PackageInitTaskSpec](#packageinittaskspec)_ |  |  |  |
| `clone` _[PackageCloneTaskSpec](#packageclonetaskspec)_ |  |  |  |
| `edit` _[PackageEditTaskSpec](#packageedittaskspec)_ |  |  |  |
| `upgrade` _[PackageUpgradeTaskSpec](#packageupgradetaskspec)_ |  |  |  |




#### TaskType





_Appears in:_
- [Task](#task)

| Field | Description |
| --- | --- |
| `init` |  |
| `clone` |  |
| `edit` |  |
| `upgrade` |  |
| `render` |  |
| `push` |  |
| `` |  |


#### UpstreamPackage

UpstreamRepository repository may be specified directly or by referencing another Repository resource.



_Appears in:_
- [PackageCloneTaskSpec](#packageclonetaskspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[RepositoryType](#repositorytype)_ | Type of the repository (i.e. git). If empty, `upstreamRef` will be used. |  |  |
| `git` _[GitPackage](#gitpackage)_ | Git upstream package specification. Required if `type` is `git`. Must be unspecified if `type` is not `git`. |  |  |
| `upstreamRef` _[PackageRevisionRef](#packagerevisionref)_ | UpstreamRef is the reference to the package from a registered repository rather than external package. |  |  |


