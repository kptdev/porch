// Copyright 2022, 2024 The kpt Authors
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
	"context"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
)

type packages struct {
	packageCommon
	rest.TableConvertor
}

var _ rest.Storage = &packages{}
var _ rest.Lister = &packages{}
var _ rest.Getter = &packages{}
var _ rest.Scoper = &packages{}
var _ rest.Creater = &packages{}
var _ rest.Updater = &packages{}
var _ rest.GracefulDeleter = &packages{}
var _ rest.SingularNameProvider = &packages{}

// GetSingularName implements the SingularNameProvider interface
func (r *packages) GetSingularName() string {
	return "package"
}

func (r *packages) New() runtime.Object {
	return &porchapi.PorchPackage{}
}

func (r *packages) Destroy() {}

func (r *packages) NewList() runtime.Object {
	return &porchapi.PorchPackageList{}
}

func (r *packages) NamespaceScoped() bool {
	return true
}

// List selects resources in the storage which match to the selector. 'options' can be nil.
func (r *packages) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	ctx, span := tracer.Start(ctx, "packages::List", trace.WithAttributes())
	defer span.End()

	result := &porchapi.PorchPackageList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageList",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
	}

	ns, _ := genericapirequest.NamespaceFrom(ctx)

	filter, err := parsePackageFieldSelector(options.FieldSelector, ns)
	if err != nil {
		return nil, err
	}

	if err := r.listPackages(ctx, filter, func(p repository.Package) error {
		item := p.GetPackage(ctx)
		result.Items = append(result.Items, *item)
		return nil
	}); err != nil {
		return nil, err
	}

	return result, nil
}

// Get implements the Getter interface
func (r *packages) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	ctx, span := tracer.Start(ctx, "packages::Get", trace.WithAttributes())
	defer span.End()

	pkg, err := r.getPackage(ctx, name)
	if err != nil {
		return nil, err
	}

	obj := pkg.GetPackage(ctx)
	return obj, nil
}

// Create implements the Creater interface.
func (r *packages) Create(_ context.Context, _ runtime.Object, _ rest.ValidateObjectFunc, _ *metav1.CreateOptions) (runtime.Object, error) {
	return nil, apierrors.NewMethodNotSupported(
		porchapi.PackageGVR.GroupResource(),
		"create or clone a PackageRevision resource to create a package as direct package creation",
	)
}

// Update implements the Updater interface.

// Update finds a resource in the storage and updates it. Some implementations
// may allow updates creates the object - they should set the created boolean
// to true.
func (r *packages) Update(_ context.Context, _ string, _ rest.UpdatedObjectInfo, _ rest.ValidateObjectFunc,
	_ rest.ValidateObjectUpdateFunc, _ bool, _ *metav1.UpdateOptions) (runtime.Object, bool, error) {

	return nil, false, apierrors.NewMethodNotSupported(
		porchapi.PackageGVR.GroupResource(),
		"update a PackageRevision resource to update a package as direct package update",
	)
}

// Delete implements the GracefulDeleter interface.
// Delete finds a resource in the storage and deletes it.
// The delete attempt is validated by the deleteValidation first.
// If options are provided, the resource will attempt to honor them or return an invalid
// request error.
// Although it can return an arbitrary error value, IsNotFound(err) is true for the
// returned error value err when the specified resource is not found.
// Delete *may* return the object that was deleted, or a status object indicating additional
// information about deletion.
// It also returns a boolean which is set to true if the resource was instantly
// deleted or false if it will be deleted asynchronously.
func (r *packages) Delete(ctx context.Context, name string, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions) (runtime.Object, bool, error) {
	return nil, false, apierrors.NewMethodNotSupported(
		porchapi.PackageGVR.GroupResource(),
		"delete all PackageRevisions resources of a package to delete a package as direct package deletion",
	)
}
