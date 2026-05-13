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

package porch

import (
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	coreapi "k8s.io/api/core/v1"
)

// CreateV1Alpha2ClientWithFlags creates a controller-runtime client that maps
// PackageRevision to porch.kpt.dev/v1alpha2 (CRD) while keeping
// PackageRevisionResources at porch.kpt.dev/v1alpha1 (APIService).
func CreateV1Alpha2ClientWithFlags(flags *genericclioptions.ConfigFlags) (client.Client, error) {
	config, err := flags.ToRESTConfig()
	if err != nil {
		return nil, err
	}
	return CreateV1Alpha2Client(config)
}

// CreateV1Alpha2Client creates a controller-runtime client for v1alpha2.
func CreateV1Alpha2Client(config *rest.Config) (client.Client, error) {
	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		return nil, err
	}
	return client.New(config, client.Options{
		Scheme: scheme,
		Mapper: createV1Alpha2RESTMapper(),
	})
}

func createV1Alpha2Scheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	for _, api := range (runtime.SchemeBuilder{
		porchv1alpha2.AddToScheme,
		porchapi.AddToScheme, // needed for PackageRevisionResources (stays at v1alpha1)
		configapi.AddToScheme,
		coreapi.AddToScheme,
		metav1.AddMetaToScheme,
	}) {
		if err := api(scheme); err != nil {
			return nil, err
		}
	}
	return scheme, nil
}

func createV1Alpha2RESTMapper() meta.RESTMapper {
	rm := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		porchv1alpha2.SchemeGroupVersion,
		porchapi.SchemeGroupVersion,
		configapi.GroupVersion,
		coreapi.SchemeGroupVersion,
		metav1.SchemeGroupVersion,
	})

	for _, r := range []struct {
		kind             schema.GroupVersionKind
		plural, singular string
	}{
		// v1alpha2 PackageRevision (CRD)
		{kind: porchv1alpha2.SchemeGroupVersion.WithKind("PackageRevision"), plural: "packagerevisions", singular: "packagerevision"},
		// v1alpha1 PackageRevisionResources (APIService — stays at v1alpha1)
		{kind: porchapi.SchemeGroupVersion.WithKind("PackageRevisionResources"), plural: "packagerevisionresources", singular: "packagerevisionresources"},
		// Other resources
		{kind: configapi.GroupVersion.WithKind("Repository"), plural: "repositories", singular: "repository"},
		{kind: coreapi.SchemeGroupVersion.WithKind("Secret"), plural: "secrets", singular: "secret"},
		{kind: metav1.SchemeGroupVersion.WithKind("Table"), plural: "tables", singular: "table"},
	} {
		rm.AddSpecific(
			r.kind,
			r.kind.GroupVersion().WithResource(r.plural),
			r.kind.GroupVersion().WithResource(r.singular),
			meta.RESTScopeNamespace,
		)
	}
	return rm
}
