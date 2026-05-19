// Copyright 2022, 2024-2026 The kpt Authors
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
	"fmt"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	pctx "github.com/kptdev/porch/pkg/util/context"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type packageRevisionResources struct {
	rest.TableConvertor
	packageCommon
}

var _ rest.Storage = &packageRevisionResources{}
var _ rest.Lister = &packageRevisionResources{}
var _ rest.Getter = &packageRevisionResources{}
var _ rest.Scoper = &packageRevisionResources{}
var _ rest.Updater = &packageRevisionResources{}
var _ rest.SingularNameProvider = &packageRevisionResources{}

// GetSingularName implements the SingularNameProvider interface
func (r *packageRevisionResources) GetSingularName() string {
	return "packagerevisionresources"
}

func (r *packageRevisionResources) New() runtime.Object {
	return &porchapi.PackageRevisionResources{}
}

func (r *packageRevisionResources) Destroy() {}

func (r *packageRevisionResources) NewList() runtime.Object {
	return &porchapi.PackageRevisionResourcesList{}
}

func (r *packageRevisionResources) NamespaceScoped() bool {
	return true
}

// List selects resources in the storage which match to the selector. 'options' can be nil.
func (r *packageRevisionResources) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	ctx, span := tracer.Start(ctx, "[START]::PackageRevisionResources::List", trace.WithAttributes())
	defer span.End()

	ctx = pctx.WithNewRequestID(ctx)

	klog.V(3).InfoS("List PackageRevisionResources started", pctx.LogMetadataFrom(ctx)...)

	result := &porchapi.PackageRevisionResourcesList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevisionResourcesList",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
	}

	ns, _ := genericapirequest.NamespaceFrom(ctx)

	filter, err := parsePackageRevisionResourcesFieldSelector(options, ns)
	if err != nil {
		return nil, err
	}

	if err := r.listPackageRevisions(ctx, *filter, func(ctx context.Context, p repository.PackageRevision) error {
		apiPkgResources, err := p.GetResources(ctx)
		if err != nil {
			return err
		}
		result.Items = append(result.Items, *apiPkgResources)
		return nil
	}); err != nil {
		return nil, err
	}

	klog.V(3).InfoS("List PackageRevisionResources completed",
		pctx.LogMetadataFromWithExtras(ctx, "found", len(result.Items))...)

	return result, nil
}

// Get implements the Getter interface
func (r *packageRevisionResources) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	ctx, span := tracer.Start(ctx, "[START]::PackageRevisionResources::Get", trace.WithAttributes())
	defer span.End()

	ctx = pctx.WithNewRequestIDAndPackageRevision(ctx, name)

	klog.V(3).InfoS("Get PackageRevisionResources started", pctx.LogMetadataFrom(ctx)...)

	pkg, err := r.getRepoPkgRevForResources(ctx, name)
	if err != nil {
		return nil, err
	}

	apiPkgResources, err := pkg.GetResources(ctx)
	if err != nil {
		return nil, err
	}

	klog.V(3).InfoS("Get PackageRevisionResources completed", pctx.LogMetadataFrom(ctx)...)

	return apiPkgResources, nil
}

// Update finds a resource in the storage and updates it. Some implementations
// may allow updates creates the object - they should set the created boolean
// to true.
func (r *packageRevisionResources) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, _ rest.ValidateObjectFunc,
	updateValidation rest.ValidateObjectUpdateFunc, _ bool, _ *metav1.UpdateOptions) (runtime.Object, bool, error) {
	ctx, span := tracer.Start(ctx, "[START]::PackageRevisionResources::Update", trace.WithAttributes())
	defer span.End()

	ctx = pctx.WithNewRequestIDAndPackageRevision(ctx, name)

	namespace, namespaced := genericapirequest.NamespaceFrom(ctx)
	if !namespaced {
		return nil, false, apierrors.NewBadRequest("namespace must be specified")
	}

	pkgMutexKey := getPackageMutexKey(namespace, name)
	pkgMutex := getMutexForPackage(pkgMutexKey)
	locked := pkgMutex.TryLock()
	if !locked {
		return nil, false,
			apierrors.NewConflict(
				porchapi.Resource("packagerevisionresources"),
				name,
				fmt.Errorf(GenericConflictErrorMsg, "package revision resources", pkgMutexKey))
	}
	defer pkgMutex.Unlock()

	oldRepoPkgRev, err := r.getRepoPkgRevForResources(ctx, name)
	if err != nil {
		return nil, false, err
	}

	oldApiPkgRevResources, err := oldRepoPkgRev.GetResources(ctx)
	if err != nil {
		klog.Infof("update failed to retrieve old object: %v", err)
		return nil, false, err
	}

	newRuntimeObj, err := objInfo.UpdatedObject(ctx, oldApiPkgRevResources)
	if err != nil {
		klog.Infof("update failed to construct UpdatedObject: %v", err)
		return nil, false, err
	}
	newObj, ok := newRuntimeObj.(*porchapi.PackageRevisionResources)
	if !ok {
		return nil, false, apierrors.NewBadRequest(fmt.Sprintf("expected PackageRevisionResources object, got %T", newRuntimeObj))
	}

	if updateValidation != nil {
		err := updateValidation(ctx, newObj, oldApiPkgRevResources)
		if err != nil {
			klog.Infof("update failed validation: %v", err)
			return nil, false, err
		}
	}
	klog.InfoS("[API] Update operation started for PackageRevisionResources", pctx.LogMetadataFrom(ctx)...)

	prKey, err := repository.PkgRevK8sName2Key(namespace, name)
	if err != nil {
		return nil, false, err
	}

	var repositoryObj v1alpha1.Repository
	repositoryID := types.NamespacedName{Namespace: prKey.RKey().Namespace, Name: prKey.RKey().Name}
	if err := r.coreClient.Get(ctx, repositoryID, &repositoryObj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, apierrors.NewNotFound(porchapi.PackageRevisionResourcesGVR.GroupResource(), repositoryID.Name)
		}
		return nil, false, apierrors.NewInternalError(fmt.Errorf("error getting repository %v: %w", repositoryID, err))
	}

	var rev repository.PackageRevision
	var renderStatus *porchapi.RenderStatus

	if isV1Alpha2Repo(&repositoryObj) {
		// v1alpha2: write resources without render. PR controller renders async.
		rev, err = r.cad.UpdatePackageResourcesWithoutRender(ctx, &repositoryObj, oldRepoPkgRev, oldApiPkgRevResources, newObj)
		if err != nil {
			return nil, false, apierrors.NewInternalError(err)
		}
		r.patchRenderRequestAnnotation(ctx, namespace, name, rev.ResourceVersion())
	} else {
		rev, renderStatus, err = r.cad.UpdatePackageResources(ctx, &repositoryObj, oldRepoPkgRev, oldApiPkgRevResources, newObj)
		if err != nil {
			return nil, false, apierrors.NewInternalError(err)
		}
	}

	created, err := rev.GetResources(ctx)
	if err != nil {
		return nil, false, apierrors.NewInternalError(err)
	}
	if renderStatus != nil {
		created.Status.RenderStatus = *renderStatus
	}

	klog.InfoS("[API] Update operation completed for PackageRevisionResources", pctx.LogMetadataFrom(ctx)...)

	return created, false, nil
}

// patchRenderRequestAnnotation patches the render-request annotation on the
// v1alpha2 PackageRevision CRD to trigger async rendering.
func (r *packageRevisionResources) patchRenderRequestAnnotation(ctx context.Context, namespace, name, resourceVersion string) {
	pr := &porchv1alpha2.PackageRevision{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := r.coreClient.Get(ctx, key, pr); err != nil {
		klog.Warningf("failed to get v1alpha2 PR %s/%s for render-request patch: %v", namespace, name, err)
		return
	}

	patch := client.MergeFrom(pr.DeepCopy())
	if pr.Annotations == nil {
		pr.Annotations = map[string]string{}
	}
	pr.Annotations[porchv1alpha2.AnnotationRenderRequest] = resourceVersion
	if err := r.coreClient.Patch(ctx, pr, patch); err != nil {
		klog.Warningf("failed to patch render-request annotation on %s/%s: %v", namespace, name, err)
	}
}

// getRepoPkgRevForResources looks up a package revision in the cache, including v1alpha2 repos.
// TODO: Replace r.cad.ListPackageRevisions with direct cache access when engine is removed
func (r *packageRevisionResources) getRepoPkgRevForResources(ctx context.Context, name string) (repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "packageRevisionResources::getRepoPkgRevForResources", trace.WithAttributes())
	defer span.End()

	namespace, namespaced := genericapirequest.NamespaceFrom(ctx)
	if !namespaced {
		return nil, fmt.Errorf("namespace must be specified")
	}

	prKey, err := repository.PkgRevK8sName2Key(namespace, name)
	if err != nil {
		return nil, err
	}

	repositoryObj, err := r.getRepositoryObj(ctx, types.NamespacedName{
		Name:      prKey.RKey().Name,
		Namespace: prKey.RKey().Namespace,
	})
	if err != nil {
		return nil, err
	}

	if repositoryObj.DeletionTimestamp != nil {
		return nil, apierrors.NewNotFound(r.gr, name)
	}

	revisions, err := r.cad.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{Key: prKey})
	if err != nil {
		return nil, err
	}
	for _, rev := range revisions {
		if rev.KubeObjectName() == name {
			return rev, nil
		}
	}

	return nil, apierrors.NewNotFound(r.gr, name)
}
