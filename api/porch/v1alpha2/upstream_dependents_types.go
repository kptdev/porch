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

// UpstreamDependents records the PackageRevisions that depend on one upstream
// identity through an independent subpackage. There is one object per upstream
// identity. Dependent entries are derived from the dependents' own Kptfiles and are
// written by the PackageRevision controller. Enforcement treats entries as hints and
// verifies them against the live dependent before blocking a delete.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories={porch}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Upstream",type=string,JSONPath=`.spec.upstreamKey`
// +kubebuilder:printcolumn:name="Dependents",type=integer,JSONPath=`.status.dependentCount`
// +kubebuilder:printcolumn:name="Truncated",type=boolean,JSONPath=`.status.truncated`
type UpstreamDependents struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec UpstreamDependentsSpec `json:"spec,omitempty"`

	// +optional
	Status UpstreamDependentsStatus `json:"status,omitempty"`
}

// UpstreamDependentsSpec identifies the upstream.
type UpstreamDependentsSpec struct {
	// UpstreamKey is the identity of the upstream. For an upstream PackageRevision in
	// this cluster it is that PackageRevision's name. For an upstream outside the
	// cluster it is the canonical `repo|dir|ref` locator, with commit excluded.
	// +kubebuilder:validation:MinLength=1
	UpstreamKey string `json:"upstreamKey"`
}

// UpstreamDependentsStatus lists the dependents of the upstream.
type UpstreamDependentsStatus struct {
	// Dependents lists one entry per dependent subpackage, keyed by dependent name and
	// subpackage path. The PackageRevision controller is the only writer. It updates the
	// list with optimistic concurrency, so there is one managed-fields record per object.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +listMapKey=path
	Dependents []UpstreamDependent `json:"dependents,omitempty"`

	// DependentCount is the number of entries in Dependents.
	// +optional
	DependentCount int `json:"dependentCount,omitempty"`

	// Truncated is true when the number of dependents reached the per-object cap. The
	// webhook then fails closed for deletes of this upstream.
	// +optional
	Truncated bool `json:"truncated,omitempty"`

	// Conditions report the state of the object, for example when the cap is reached.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// UpstreamDependent is one dependent subpackage of an upstream.
type UpstreamDependent struct {
	// Name is the name of the dependent PackageRevision in the same namespace.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Path is the subpackage directory within the dependent that was cloned from the
	// upstream.
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`
}

// UpstreamDependentsList is a list of UpstreamDependents.
//
// +kubebuilder:object:root=true
type UpstreamDependentsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []UpstreamDependents `json:"items"`
}
