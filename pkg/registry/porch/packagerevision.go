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
	"github.com/kptdev/porch/pkg/repository"
	context1 "github.com/kptdev/porch/pkg/util/context"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
)

var tracer = otel.Tracer("packagerevision")

type packageRevisions struct {
	packageCommon
	rest.TableConvertor
}

var _ rest.Storage = &packageRevisions{}
var _ rest.Lister = &packageRevisions{}
var _ rest.Getter = &packageRevisions{}
var _ rest.Scoper = &packageRevisions{}
var _ rest.Creater = &packageRevisions{}
var _ rest.Updater = &packageRevisions{}
var _ rest.GracefulDeleter = &packageRevisions{}
var _ rest.Watcher = &packageRevisions{}
var _ rest.SingularNameProvider = &packageRevisions{}

// GetSingularName implements the SingularNameProvider interface
func (r *packageRevisions) GetSingularName() string {
	return "packagerevision"
}

func (r *packageRevisions) New() runtime.Object {
	return &porchapi.PackageRevision{}
}

func (r *packageRevisions) Destroy() {}

func (r *packageRevisions) NewList() runtime.Object {
	return &porchapi.PackageRevisionList{}
}

func (r *packageRevisions) NamespaceScoped() bool {
	return true
}

// List selects resources in the storage which match to the selector. 'options' can be nil.
func (r *packageRevisions) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	ctx, span := tracer.Start(ctx, "[START]::packageRevisions::List", trace.WithAttributes())
	defer span.End()

	ctx = context1.WithNewRequestID(ctx)

	klog.V(3).InfoS("[API] List operation started for PackageRevisions", context1.LogMetadataFrom(ctx)...)

	ns, _ := genericapirequest.NamespaceFrom(ctx)

	result := &porchapi.PackageRevisionList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevisionList",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
	}

	filter, err := parsePackageRevisionFieldSelector(options, ns)
	if err != nil {
		return nil, err
	}

	if err := r.listPackageRevisions(ctx, *filter, func(ctx context.Context, p repository.PackageRevision) error {
		item, err := p.GetPackageRevision(ctx)
		if err != nil {
			// Skip package revisions that fail to fetch (stale cache, deleted, etc.)
			klog.Warningf("Failed to fetch package revision %s during list, skipping: %v", p.KubeObjectName(), err)
			return nil
		}
		result.Items = append(result.Items, *item)
		return nil
	}); err != nil {
		return nil, err
	}

	klog.V(3).InfoS("[API] List operation completed for PackageRevisions",
		context1.LogMetadataFromWithExtras(ctx, "found", len(result.Items))...)

	return result, nil
}

// Get implements the Getter interface
func (r *packageRevisions) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	ctx, span := tracer.Start(ctx, "[START]::packageRevisions::Get", trace.WithAttributes())
	defer span.End()

	ctx = context1.WithNewRequestIDAndPackageRevision(ctx, name)

	klog.V(3).InfoS("[API] Get operation started for PackageRevision", context1.LogMetadataFrom(ctx)...)

	repoPkgRev, err := r.getRepoPkgRev(ctx, name)
	if err != nil {
		return nil, err
	}

	apiPkgRev, err := repoPkgRev.GetPackageRevision(ctx)
	if err != nil {
		return nil, err
	}

	klog.V(3).InfoS("[API] Get operation completed for PackageRevision", context1.LogMetadataFrom(ctx)...)

	return apiPkgRev, nil
}

// Create implements the Creater interface.
func (r *packageRevisions) Create(ctx context.Context, runtimeObject runtime.Object, _ rest.ValidateObjectFunc,
	_ *metav1.CreateOptions) (runtime.Object, error) {
	ctx, span := tracer.Start(ctx, "[START]::packageRevisions::Create", trace.WithAttributes())
	defer span.End()

	ctx = context1.WithNewRequestID(ctx)

	ns, namespaced := genericapirequest.NamespaceFrom(ctx)
	if !namespaced {
		return nil, apierrors.NewBadRequest("namespace must be specified")
	}

	newApiPkgRev, ok := runtimeObject.(*porchapi.PackageRevision)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected PackageRevision object, got %T", runtimeObject))
	}

	// TODO: Accept some form of client-provided name, for example using GenerateName
	// and figure out where we can store it (in Kptfile?). Porch can then append unique
	// suffix to the names while respecting client-provided value as well.
	if newApiPkgRev.Name != "" {
		klog.Warningf("Client provided metadata.name %q", newApiPkgRev.Name)
	}

	repositoryName := newApiPkgRev.Spec.RepositoryName
	if repositoryName == "" {
		return nil, apierrors.NewBadRequest("spec.repositoryName is required")
	}

	action := createAction(newApiPkgRev)
	pkgKeyStruct := repository.FromFullPathname(repository.RepositoryKey{Name: repositoryName}, newApiPkgRev.Spec.PackageName)

	prName := repository.PackageRevisionKey{
		PkgKey:        pkgKeyStruct,
		WorkspaceName: newApiPkgRev.Spec.WorkspaceName,
	}.K8SName()
	ctx = context1.WithPackageRevision(ctx, prName)

	repositoryObj, err := r.getRepositoryObj(ctx, types.NamespacedName{Name: repositoryName, Namespace: ns})
	if err != nil {
		return nil, err
	}

	if isV1Alpha2Repo(repositoryObj) {
		return nil, apierrors.NewGone(fmt.Sprintf("repository %q is managed by v1alpha2; use the v1alpha2 API", repositoryName))
	}

	fieldErrors := r.createStrategy.Validate(ctx, runtimeObject)
	if len(fieldErrors) > 0 {
		return nil, apierrors.NewInvalid(porchapi.SchemeGroupVersion.WithKind("PackageRevision").GroupKind(), newApiPkgRev.Name, fieldErrors)
	}

	klog.InfoS("[API] Operation started for PackageRevision",
		context1.LogMetadataFromWithExtras(ctx, "action", action)...)

	var parentPackage repository.PackageRevision
	if newApiPkgRev.Spec.Parent != nil && newApiPkgRev.Spec.Parent.Name != "" {
		p, err := r.getRepoPkgRev(ctx, newApiPkgRev.Spec.Parent.Name)
		if err != nil {
			return nil, fmt.Errorf("cannot get parent package %q: %w", newApiPkgRev.Spec.Parent.Name, err)
		}
		parentPackage = p
	}

	pkgMutexKey := uncreatedPackageMutexKey(newApiPkgRev)
	pkgMutex := getMutexForPackage(pkgMutexKey)

	locked := pkgMutex.TryLock()
	if !locked {
		conflictError := creationConflictError(newApiPkgRev)
		return nil,
			apierrors.NewConflict(
				porchapi.Resource("packagerevisions"),
				"(new creation)",
				conflictError)
	}
	defer pkgMutex.Unlock()

	createdRepoPkgRev, err := r.cad.CreatePackageRevision(ctx, repositoryObj, newApiPkgRev, parentPackage)
	if err != nil {
		return nil, apierrors.NewInternalError(err)
	}

	createdApiPkgRev, err := createdRepoPkgRev.GetPackageRevision(ctx)
	if err != nil {
		return nil, apierrors.NewInternalError(err)
	}

	klog.InfoS("[API] Operation completed for PackageRevision",
		context1.LogMetadataFromWithExtras(ctx, "action", action)...)

	return createdApiPkgRev, nil
}

func createAction(pkgRev *porchapi.PackageRevision) string {
	// Nil checks
	if pkgRev == nil || pkgRev.Spec.Tasks == nil {
		return "Create"
	}
	// Check tasks for specific operation types
	var taskType []porchapi.TaskType
	for _, task := range pkgRev.Spec.Tasks {
		switch task.Type {
		case porchapi.TaskTypeInit:
			taskType = append(taskType, porchapi.TaskTypeInit)
		case porchapi.TaskTypeClone:
			taskType = append(taskType, porchapi.TaskTypeClone)
		case porchapi.TaskTypeUpgrade:
			taskType = append(taskType, porchapi.TaskTypeUpgrade)
		case porchapi.TaskTypeEdit:
			// Edit task with sourceRef indicates a copy operation
			if task.Edit != nil && task.Edit.Source != nil {
				taskType = append(taskType, porchapi.TaskTypeEdit)
			}
		}
	}
	// If multiple different task types found, return "Create"
	if len(taskType) > 1 {
		return "Create"
	}
	// If single task type found, return specific action
	if len(taskType) == 1 {
		switch taskType[0] {
		case porchapi.TaskTypeInit:
			return "Init"
		case porchapi.TaskTypeClone:
			return "Clone"
		case porchapi.TaskTypeUpgrade:
			return "Upgrade"
		case porchapi.TaskTypeEdit:
			return "Copy"
		}
	}
	return "Create"
}

// Update implements the Updater interface.

// Update finds a resource in the storage and updates it. Some implementations
// may allow updates creates the object - they should set the created boolean
// to true.
func (r *packageRevisions) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc,
	updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool, _ *metav1.UpdateOptions) (runtime.Object, bool, error) {
	ctx, span := tracer.Start(ctx, "[START]::packageRevisions::Update", trace.WithAttributes())
	defer span.End()

	ctx = context1.WithNewRequestIDAndPackageRevision(ctx, name)

	runTimeObj, ok, err := r.updatePackageRevision(ctx, name, objInfo, createValidation, updateValidation, forceAllowCreate)
	if err != nil {
		klog.ErrorS(err, "[API] PackageRevision update operation failed", context1.LogMetadataFrom(ctx)...)
	}
	return runTimeObj, ok, err
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
func (r *packageRevisions) Delete(ctx context.Context, name string, deleteValidation rest.ValidateObjectFunc, _ *metav1.DeleteOptions) (runtime.Object, bool, error) {
	ctx, span := tracer.Start(ctx, "[START]::packageRevisions::Delete", trace.WithAttributes())
	defer span.End()

	ctx = context1.WithNewRequestIDAndPackageRevision(ctx, name)

	ns, namespaced := genericapirequest.NamespaceFrom(ctx)
	if !namespaced {
		return nil, false, apierrors.NewBadRequest("namespace must be specified")
	}

	repoPkgRev, err := r.getRepoPkgRev(ctx, name)
	if err != nil {
		return nil, false, err
	}

	apiPkgRev, err := repoPkgRev.GetPackageRevision(ctx)
	if err != nil {
		return nil, false, apierrors.NewInternalError(err)
	}

	repositoryObj, err := r.validateDelete(ctx, deleteValidation, apiPkgRev, name, ns)
	if err != nil {
		return nil, false, err
	}

	klog.InfoS("[API] Delete operation started for PackageRevision", context1.LogMetadataFrom(ctx)...)

	if err := r.checkIfUpstreamIsReferenced(ctx, apiPkgRev); err != nil {
		return nil, false, err
	}

	pkgMutexKey := getPackageMutexKey(ns, name)
	pkgMutex := getMutexForPackage(pkgMutexKey)

	locked := pkgMutex.TryLock()
	if !locked {
		return nil, false,
			apierrors.NewConflict(
				porchapi.Resource("packagerevisions"),
				name,
				fmt.Errorf(GenericConflictErrorMsg, "package revision", pkgMutexKey))
	}
	defer pkgMutex.Unlock()

	if err := r.cad.DeletePackageRevision(ctx, repositoryObj, repoPkgRev); err != nil {
		return nil, false, apierrors.NewInternalError(err)
	}

	klog.InfoS("[API] Delete operation completed for PackageRevision", context1.LogMetadataFrom(ctx)...)

	// TODO: Should we do an async delete?
	return apiPkgRev, true, nil
}

func uncreatedPackageMutexKey(newApiPkgRev *porchapi.PackageRevision) string {
	return fmt.Sprintf("%s-%s-%s-%s",
		newApiPkgRev.Namespace,
		newApiPkgRev.Spec.RepositoryName,
		newApiPkgRev.Spec.PackageName,
		newApiPkgRev.Spec.WorkspaceName,
	)
}

func creationConflictError(newApiPkgRev *porchapi.PackageRevision) error {
	return fmt.Errorf(
		fmt.Sprintf(
			ConflictErrorMsgBase,
			"to create package revision with details namespace=%q, repository=%q, package=%q,workspace=%q",
		),
		newApiPkgRev.Namespace,
		newApiPkgRev.Spec.RepositoryName,
		newApiPkgRev.Spec.PackageName,
		newApiPkgRev.Spec.WorkspaceName,
	)
}

func (r *packageRevisions) checkIfUpstreamIsReferenced(ctx context.Context, apiPkgRev *porchapi.PackageRevision) error {
	klog.Infof("[API] Checking if upstream PackageRevision is referenced: %s", apiPkgRev.Name)
	ns, _ := genericapirequest.NamespaceFrom(ctx)
	downstream, err := r.cad.FindAllUpstreamReferencesInRepositories(ctx, ns, apiPkgRev.Name)
	if err != nil {
		klog.Warningf("[API] Failed to check if upstream is referenced for PackageRevision %s: %v", apiPkgRev.Name, err)
		return apierrors.NewInternalError(fmt.Errorf("failed to check upstream references: %w", err))
	}

	if downstream != "" {
		klog.Infof("[API] PackageRevision %s is referenced as upstream by: %s", apiPkgRev.Name, downstream)
		return apierrors.NewForbidden(
			porchapi.Resource("packagerevisions"),
			apiPkgRev.Name,
			fmt.Errorf("cannot delete package revision, it is referenced as upstream by: %s", downstream))
	}
	klog.Infof("[API] PackageRevision %s is not referenced as upstream", apiPkgRev.Name)
	return nil
}
