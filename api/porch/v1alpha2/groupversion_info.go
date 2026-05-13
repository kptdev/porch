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

// Package v1alpha2 contains API Schema definitions for the porch v1alpha2 API group.
// This version introduces PackageRevision as a CRD.
//
// PackageRevisionResources remains at porch.kpt.dev/v1alpha1 (APIService).
// Kubernetes routes per group+version path, so v1alpha1 APIService and v1alpha2 CRD
// coexist in the same API group without conflict.
//
// Note: v1alpha2 types do not have code-gen clients (clientset/listers/informers).
// Use controller-runtime client to access these resources.
//
// +k8s:openapi-gen=true
// +k8s:deepcopy-gen=package,register
// +k8s:defaulter-gen=TypeMeta
// +kubebuilder:object:generate=true
// +groupName=porch.kpt.dev
package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

//go:generate go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.1 object:headerFile="../../../scripts/boilerplate.go.txt",year=$YEAR_GEN paths=.
//go:generate go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.1 crd:crdVersions=v1,headerFile="../../../scripts/boilerplate.yaml.txt",year=$YEAR_GEN output:crd:artifacts:config=. paths=.

const GroupName = "porch.kpt.dev"

var (
	// SchemeGroupVersion is group version used to register these objects
	SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha2"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder      runtime.SchemeBuilder
	localSchemeBuilder = &SchemeBuilder
	AddToScheme        = localSchemeBuilder.AddToScheme

	PackageRevisionGVR = SchemeGroupVersion.WithResource("packagerevisions")
)

func init() {
	localSchemeBuilder.Register(addKnownTypes)
}

// addKnownTypes adds the list of known types to the given scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&PackageRevision{},
		&PackageRevisionList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

// Resource takes an unqualified resource and returns a Group qualified GroupResource
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}
