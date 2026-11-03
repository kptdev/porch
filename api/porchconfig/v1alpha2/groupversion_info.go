// Copyright 2022-2026 The kpt Authors
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

// Package v1alpha2 contains API Schema definitions for the v1alpha2 API group
// +kubebuilder:object:generate=true
// +kubebuilder:ac:generate=true
// +kubebuilder:ac:output:package=../../generated/applyconfigurations
// +groupName=config.porch.kpt.dev
package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "config.porch.kpt.dev", Version: "v1alpha2"}

	// SchemeGroupVersion is an alias for GroupVersion, required by the
	// generated apply configuration code.
	SchemeGroupVersion = GroupVersion

	// We removed SchemeBuilder to keep our dependencies small

	TypePackageVariantSet = TypeInfo{
		Kind:     "PackageVariantSet",
		Resource: GroupVersion.WithResource("packagevariantsets"),
		objects:  []runtime.Object{&PackageVariantSet{}, &PackageVariantSetList{}},
	}

	AllKinds = []TypeInfo{
		TypePackageVariantSet,
	}
)

//+kubebuilder:object:generate=false

// TypeInfo holds type meta-information
type TypeInfo struct {
	Kind     string
	Resource schema.GroupVersionResource
	objects  []runtime.Object
}

// GVK returns the schema.GroupVersionKind for the type
func (t *TypeInfo) GVK() schema.GroupVersionKind {
	return t.Resource.GroupVersion().WithKind(t.Kind)
}

// APIVersion returns the apiVersion for the type
func (t *TypeInfo) APIVersion() string {
	return t.Resource.GroupVersion().Identifier()
}

// GroupResource returns the GroupResource for the kind
func (t *TypeInfo) GroupResource() schema.GroupResource {
	return t.Resource.GroupResource()
}

func AddToScheme(scheme *runtime.Scheme) error {
	for _, kind := range AllKinds {
		scheme.AddKnownTypes(GroupVersion, kind.objects...)
	}
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
