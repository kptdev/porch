// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PackageRevision represents a package revision.
// In v1alpha2, PackageRevision is a CRD stored in etcd.
// Use controller-runtime client to access this resource (no code-gen clients).
// Note: Approval subresource is not yet implemented for v1alpha2 CRDs.
//
// +k8s:openapi-gen=true
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=packagerevisions,singular=packagerevision,shortName=rpkg
// +kubebuilder:printcolumn:name="Package",type=string,JSONPath=`.spec.packageName`
// +kubebuilder:printcolumn:name="WorkspaceName",type=string,JSONPath=`.spec.workspaceName`
// +kubebuilder:printcolumn:name="Revision",type=string,JSONPath=`.status.revision`
// +kubebuilder:printcolumn:name="Latest",type=string,JSONPath=".metadata.labels['porch.kpt.dev/latest-revision']"
// +kubebuilder:printcolumn:name="Lifecycle",type=string,JSONPath=`.spec.lifecycle`
// +kubebuilder:printcolumn:name="Repository",type=string,JSONPath=`.spec.repository`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:selectablefield:JSONPath=`.spec.lifecycle`
// +kubebuilder:selectablefield:JSONPath=`.spec.repository`
// +kubebuilder:selectablefield:JSONPath=`.spec.packageName`
// +kubebuilder:selectablefield:JSONPath=`.spec.workspaceName`
// +kubebuilder:selectablefield:JSONPath=`.status.revision`
type PackageRevision struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PackageRevisionSpec   `json:"spec,omitempty"`
	Status PackageRevisionStatus `json:"status,omitempty"`
}

// PackageRevisionList contains a list of PackageRevision
//
// +kubebuilder:object:root=true
type PackageRevisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []PackageRevision `json:"items"`
}

// Key and value of the latest package revision label
const (
	LatestPackageRevisionKey   = "porch.kpt.dev/latest-revision"
	LatestPackageRevisionValue = "true"
)

// AnnotationRenderRequest triggers async rendering when patched by the PRR handler.
const AnnotationRenderRequest = "porch.kpt.dev/render-request"

// PackageRevisionFinalizer prevents deletion of packages that have not been through DeletionProposed.
const PackageRevisionFinalizer = "porch.kpt.dev/packagerevisions"

const (
	// PushOnFnRenderFailureKey controls whether resources are written back
	// to storage when the render pipeline fails.
	PushOnFnRenderFailureKey   = "porch.kpt.dev/push-on-render-failure"
	PushOnFnRenderFailureValue = "true"
)

// PkgRevFieldSelector defines field selectors for PackageRevision.
// Requires controller-runtime field indexing setup in controller.
type PkgRevFieldSelector string

const (
	PkgRevSelectorName          PkgRevFieldSelector = "metadata.name"
	PkgRevSelectorNamespace     PkgRevFieldSelector = "metadata.namespace"
	PkgRevSelectorRevision      PkgRevFieldSelector = "status.revision"
	PkgRevSelectorPackageName   PkgRevFieldSelector = "spec.packageName"
	PkgRevSelectorRepository    PkgRevFieldSelector = "spec.repository"
	PkgRevSelectorWorkspaceName PkgRevFieldSelector = "spec.workspaceName"
	PkgRevSelectorLifecycle     PkgRevFieldSelector = "spec.lifecycle"
)

// PackageRevisionSelectableFields lists all selectable fields for PackageRevision
var PackageRevisionSelectableFields = []PkgRevFieldSelector{
	PkgRevSelectorName,
	PkgRevSelectorNamespace,
	PkgRevSelectorRevision,
	PkgRevSelectorPackageName,
	PkgRevSelectorRepository,
	PkgRevSelectorWorkspaceName,
	PkgRevSelectorLifecycle,
}

// PackageRevisionLifecycle represents the lifecycle state of a package revision
// +kubebuilder:validation:Enum=Draft;Proposed;Published;DeletionProposed
type PackageRevisionLifecycle string

const (
	PackageRevisionLifecycleDraft            PackageRevisionLifecycle = "Draft"
	PackageRevisionLifecycleProposed         PackageRevisionLifecycle = "Proposed"
	PackageRevisionLifecyclePublished        PackageRevisionLifecycle = "Published"
	PackageRevisionLifecycleDeletionProposed PackageRevisionLifecycle = "DeletionProposed"
)

// PackageRevisionSpec defines the desired state of PackageRevision
type PackageRevisionSpec struct {
	// PackageName identifies the package in the repository.
	PackageName string `json:"packageName,omitempty"`

	// RepositoryName is the name of the Repository object containing this package.
	RepositoryName string `json:"repository,omitempty"`

	// WorkspaceName is a short, unique description of the changes contained in this package revision.
	WorkspaceName string `json:"workspaceName,omitempty"`

	// Lifecycle specifies the lifecycle state of the package revision.
	Lifecycle PackageRevisionLifecycle `json:"lifecycle,omitempty"`

	// Source specifies how this package was created.
	// For new packages, exactly one field in Source must be set.
	// For packages discovered from git (existed before Porch), Source will be nil.
	// This field is immutable after creation.
	// TODO: Consider adding validation webhook to require Source for user-created packages.
	// +optional
	Source *PackageSource `json:"source,omitempty"`

	// SubpackageOperation specifies an operation to be carried out on an independent subpackage
	// in the package.
	// When set, exactly one subpackage operation may be specified.
	// +optional
	SubpackageOperation *SubpackageOperation `json:"subpackageOperation,omitempty"`

	// ReadinessGates specifies conditions that must be met before the package is considered ready.
	ReadinessGates []ReadinessGate `json:"readinessGates,omitempty"`

	// PackageMetadata contains labels and annotations for the package.
	PackageMetadata *PackageMetadata `json:"packageMetadata,omitempty"`
}

// PackageMetadata contains metadata for a package
type PackageMetadata struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ReadinessGate specifies a condition that must be met
type ReadinessGate struct {
	ConditionType string `json:"conditionType,omitempty"`
}

// PackageRevisionStatus defines the observed state of PackageRevision
type PackageRevisionStatus struct {
	// ObservedGeneration is the generation of the PackageRevision spec that was last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Revision identifies the version of the package.
	// This is assigned by the system when the package is published.
	Revision int `json:"revision,omitempty"`

	// UpstreamLock identifies the upstream data for this package.
	UpstreamLock *Locator `json:"upstreamLock,omitempty"`

	// SelfLock identifies the location of the current package's data
	SelfLock *Locator `json:"selfLock,omitempty"`

	// PublishedBy is the identity of the user who approved the package revision.
	PublishedBy string `json:"publishedBy,omitempty"`

	// PublishedAt is the time when the package revision was approved.
	// +optional
	PublishedAt *metav1.Time `json:"publishedAt,omitempty"`

	// Deployment is true if this is a deployment package (in a deployment repository).
	Deployment bool `json:"deployment,omitempty"`

	// ObservedPrrResourceVersion tracks the last observed PRR resourceVersion.
	// Prevents concurrent lifecycle changes and PRR updates.
	ObservedPrrResourceVersion string `json:"observedPrrResourceVersion,omitempty"`

	// RenderingPrrResourceVersion tracks the PRR resourceVersion currently being rendered.
	// Prevents concurrent renders.
	RenderingPrrResourceVersion string `json:"renderingPrrResourceVersion,omitempty"`

	// CreationSource indicates how this package was created (for debugging/history).
	// Possible values: "init", "clone", "copy", "upgrade".
	// This is a read-only field populated by the system.
	// +optional
	CreationSource string `json:"creationSource,omitempty"`

	// LastSubpackageOperation holds the last operation that was carried out on an independent subpackage
	// in the package. It is used to prevent re-execution of the same operation.
	LastSubpackageOperation *SubpackageOperation `json:"lastSubpackageOperation,omitempty"`

	// PackageConditions from Kptfile. Set by KRM functions, used for ReadinessGates.
	PackageConditions []PackageCondition `json:"packageConditions,omitempty"`

	// Conditions for controller state (e.g., Ready, Rendered).
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ResourcesSizeBytes is the total file size, in bytes, of the package revision's resources.
	ResourcesSizeBytes int64 `json:"resourcesSizeBytes,omitempty"`
}

// PackageSource specifies how a package was created.
// Exactly one field must be set.
// +kubebuilder:validation:XValidation:rule="[has(self.init), has(self.cloneFrom), has(self.copyFrom), has(self.upgrade)].filter(x, x).size() == 1",message="exactly one of init, cloneFrom, copyFrom, or upgrade must be set"
type PackageSource struct {
	// Init creates a brand new package from scratch.
	Init *PackageInitSpec `json:"init,omitempty"`

	// CloneFrom copies a package from an upstream source (first time).
	CloneFrom *UpstreamPackage `json:"cloneFrom,omitempty"`

	// CopyFrom creates a new revision from an existing package in the same repository.
	CopyFrom *PackageRevisionRef `json:"copyFrom,omitempty"`

	// Upgrade merges changes from a new upstream version into a local package.
	Upgrade *PackageUpgradeSpec `json:"upgrade,omitempty"`
}

// SubpackageOperation specifies an operation on an independent subpackage of a package.
// Exactly one field must be set.
// +kubebuilder:validation:XValidation:rule="[has(self.cloneFrom), has(self.upgrade)].filter(x, x).size() == 1",message="exactly one of cloneFrom or upgrade must be set"
// +kubebuilder:validation:XValidation:rule="has(self.subpackageDir) && !self.subpackageDir.startsWith('/') && !self.subpackageDir.startsWith('./') && !self.subpackageDir.contains('../')",message="subpackageDir must be set and a valid relative path without leading '/' or './' and without '../' segments"
type SubpackageOperation struct {
	// `SubpackageDir` is the path to a subdirectory in an existing package revision
	// into which the package specified in `CloneFrom` will be cloned as an
	// independent subpackage, or which identifies the independent subpackage whose
	// upstream will be upgraded by `Upgrade`.
	// It is a relative path within the package being modified by the subpackage
	// operation. The path may not have any leading or trailing '/', and may not
	// contain any path segment equal to '.' or '..'.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^([^./][^/]*|\.[^./][^/]*|\.\.[^/]+)(/([^./][^/]*|\.[^./][^/]*|\.\.[^/]+))*$`
	SubpackageDir string `json:"subpackageDir"`

	// `CloneFrom` specifies an upstream package from which to clone the independent
	// subpackage. The package specified in `CloneFrom` is cloned into the subdirectory specified
	// in `SubpackageDir`.
	CloneFrom *UpstreamPackage `json:"cloneFrom,omitempty"`

	// `Upgrade` specifies an upgrade of the upstream package of the independent subpackage
	// in `SubpackageDir`. The independent subpackage in the subdirectory specified in `SubpackageDir`
	// is upgraded.
	Upgrade *PackageUpgradeSpec `json:"upgrade,omitempty"`
}

// PackageCondition describes a condition from the Kptfile (package content).
// This matches the structure of conditions in Kptfile and is used for ReadinessGates validation.
type PackageCondition struct {
	// Type of the condition
	Type string `json:"type"`

	// Status of the condition (True, False, Unknown)
	Status PackageConditionStatus `json:"status"`

	// Reason for the condition's last transition
	Reason string `json:"reason,omitempty"`

	// Message with details about the condition
	Message string `json:"message,omitempty"`
}

// PackageConditionStatus represents the status of a package condition
type PackageConditionStatus string

const (
	PackageConditionTrue    PackageConditionStatus = "True"
	PackageConditionFalse   PackageConditionStatus = "False"
	PackageConditionUnknown PackageConditionStatus = "Unknown"
)

// Condition types for PackageRevision.Conditions (controller state)
const (
	// ConditionReady indicates the package is ready for use.
	// This is a summary condition that aggregates other conditions.
	ConditionReady = "Ready"

	// ConditionRendered indicates whether the latest content has been rendered.
	ConditionRendered = "Rendered"
)

// Condition reasons for PackageRevision.Conditions
const (
	ReasonReady        = "Ready"
	ReasonPending      = "Pending"
	ReasonFailed       = "Failed"
	ReasonRendered     = "Rendered"
	ReasonRenderFailed = "RenderFailed"
)
