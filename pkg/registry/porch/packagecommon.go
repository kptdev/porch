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
	"sync"

	unversionedapi "github.com/kptdev/porch/api/porch"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/engine"
	"github.com/kptdev/porch/pkg/repository"
	context1 "github.com/kptdev/porch/pkg/util/context"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// isV1Alpha2Repo returns true if the repository is annotated for v1alpha2 CRD management.
func isV1Alpha2Repo(repo *configapi.Repository) bool {
	return repo.Annotations[configapi.AnnotationKeyV1Alpha2Migration] == configapi.AnnotationValueMigrationEnabled
}

const ConflictErrorMsgBase = "another request is already in progress %s"

var GenericConflictErrorMsg = fmt.Sprintf(ConflictErrorMsgBase, "on %s \"%s\"")

var mutexMapMutex sync.Mutex
var pkgRevOperationMutexes = map[string]*sync.Mutex{}

type packageCommon struct {
	// scheme holds our scheme, for type conversions etc
	scheme *runtime.Scheme

	cad engine.CaDEngine
	// coreClient is a client back to the core kubernetes API server, useful for querying CRDs etc
	coreClient     client.Client
	gr             schema.GroupResource
	updateStrategy SimpleRESTUpdateStrategy
	createStrategy SimpleRESTCreateStrategy
}

func (r *packageCommon) listPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter,
	callback func(ctx context.Context, p repository.PackageRevision) error) error {
	ctx, span := tracer.Start(ctx, "packageCommon::listPackageRevisions", trace.WithAttributes())
	defer span.End()
	revisions, err := r.cad.ListPackageRevisions(ctx, filter)
	if err != nil {
		return err
	}

	v1alpha2Repos := r.getV1Alpha2RepoSet(ctx, revisions)

	for _, rev := range revisions {
		if v1alpha2Repos[rev.Key().RKey().Name] {
			continue
		}
		if err := callback(ctx, rev); err != nil {
			klog.Warningf("callback error for revision from repository: %+v", err)
			continue
		}
	}
	return nil
}

// getV1Alpha2RepoSet returns a set of repo names that are v1alpha2-managed,
// deduplicating lookups by repo name. Uses the informer-cached coreClient.
func (r *packageCommon) getV1Alpha2RepoSet(ctx context.Context, revisions []repository.PackageRevision) map[string]bool {
	v1alpha2Repos := map[string]bool{}
	for _, rev := range revisions {
		repoName := rev.Key().RKey().Name
		if _, checked := v1alpha2Repos[repoName]; checked {
			continue
		}
		var repo configapi.Repository
		if err := r.coreClient.Get(ctx, types.NamespacedName{Name: repoName, Namespace: rev.KubeObjectNamespace()}, &repo); err != nil {
			v1alpha2Repos[repoName] = false // fail open
			continue
		}
		v1alpha2Repos[repoName] = isV1Alpha2Repo(&repo)
	}
	return v1alpha2Repos
}

func (r *packageCommon) listPackages(ctx context.Context, filter repository.ListPackageFilter, callback func(p repository.Package) error) error {
	var opts []client.ListOption
	if ns := filter.Key.RepoKey.Namespace; ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}

	// TODO: Filter on filter.Repository?
	var repositories configapi.RepositoryList
	if err := r.coreClient.List(ctx, &repositories, opts...); err != nil {
		return fmt.Errorf("error listing repository objects: %w", err)
	}

	for i := range repositories.Items {
		repositoryObj := &repositories.Items[i]

		if filter.Key.RKey().Name != "" && filter.Key.RKey().Name != repositoryObj.GetName() {
			continue
		}

		revisions, err := r.cad.ListPackages(ctx, repositoryObj, filter)
		if err != nil {
			klog.Warningf("error listing packages from repository %s/%s: %s", repositoryObj.GetNamespace(), repositoryObj.GetName(), err)
			continue
		}
		for _, rev := range revisions {
			if err := callback(rev); err != nil {
				return err
			}
		}
	}
	return nil
}

// namespaceFilteringWatcher wraps an ObjectWatcher and filters events by namespace
// Only events for the specified namespace are passed to the delegate watcher
type namespaceFilteringWatcher struct {
	ns       string
	delegate engine.ObjectWatcher
}

func (n *namespaceFilteringWatcher) OnPackageRevisionChange(eventType watch.EventType, obj repository.PackageRevision) bool {
	// Use KubeObjectNamespace for efficient namespace filtering
	if obj.KubeObjectNamespace() != n.ns {
		return true // skip, but keep watching
	}
	return n.delegate.OnPackageRevisionChange(eventType, obj)
}

// v1alpha2FilteringWatcher filters out watch events for v1alpha2-managed repositories.
type v1alpha2FilteringWatcher struct {
	coreClient client.Client
	delegate   engine.ObjectWatcher
}

func (v *v1alpha2FilteringWatcher) OnPackageRevisionChange(eventType watch.EventType, obj repository.PackageRevision) bool {
	repoName := obj.Key().RKey().Name
	ns := obj.KubeObjectNamespace()
	var repo configapi.Repository
	if err := v.coreClient.Get(context.Background(), types.NamespacedName{Name: repoName, Namespace: ns}, &repo); err != nil {
		// If we can't look up the repo, let the event through (fail open)
		return v.delegate.OnPackageRevisionChange(eventType, obj)
	}
	if isV1Alpha2Repo(&repo) {
		return true // skip, but keep watching
	}
	return v.delegate.OnPackageRevisionChange(eventType, obj)
}

func (r *packageCommon) watchPackages(ctx context.Context, filter repository.ListPackageRevisionFilter, callback engine.ObjectWatcher) error {
	var watcher = callback

	if ns, namespaced := genericapirequest.NamespaceFrom(ctx); namespaced && ns != "" {
		watcher = &namespaceFilteringWatcher{ns: ns, delegate: watcher}
	}
	watcher = &v1alpha2FilteringWatcher{coreClient: r.coreClient, delegate: watcher}

	return r.cad.ObjectCache().WatchPackageRevisions(ctx, filter, watcher)
}

func (r *packageCommon) getRepositoryObj(ctx context.Context, repositoryID types.NamespacedName) (*configapi.Repository, error) {
	var repositoryObj configapi.Repository
	if err := r.coreClient.Get(ctx, repositoryID, &repositoryObj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, apierrors.NewNotFound(configapi.TypeRepository.GroupResource(), repositoryID.Name)
		}
		return nil, apierrors.NewInternalError(fmt.Errorf("error getting repository %v: %w", repositoryID, err))
	}
	return &repositoryObj, nil
}

func (r *packageCommon) getRepoPkgRev(ctx context.Context, name string) (repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "packageCommon::getRepoPkgRev", trace.WithAttributes())
	defer span.End()

	namespace, namespaced := genericapirequest.NamespaceFrom(ctx)
	if !namespaced {
		return nil, fmt.Errorf("namespace must be specified")
	}

	prKey, err := repository.PkgRevK8sName2Key(namespace, name)
	if err != nil {
		return nil, err
	}

	repositoryObj, err := r.getRepositoryObj(ctx, types.NamespacedName{Name: prKey.RKey().Name, Namespace: prKey.RKey().Namespace})
	if err == nil && isV1Alpha2Repo(repositoryObj) {
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

func (r *packageCommon) getPackage(ctx context.Context, name string) (repository.Package, error) {
	ctx, span := tracer.Start(ctx, "packageCommon::getPackage", trace.WithAttributes())
	defer span.End()

	namespace, namespaced := genericapirequest.NamespaceFrom(ctx)
	if !namespaced {
		return nil, fmt.Errorf("namespace must be specified")
	}

	pkgKey, err := repository.PkgK8sName2Key(namespace, name)
	if err != nil {
		return nil, err
	}

	repositoryObj, err := r.getRepositoryObj(ctx, types.NamespacedName{Name: pkgKey.RKey().Name, Namespace: pkgKey.RKey().Namespace})
	if err != nil {
		return nil, err
	}

	// Check if repository is being deleted
	if repositoryObj.DeletionTimestamp != nil {
		return nil, apierrors.NewNotFound(r.gr, name)
	}

	revisions, err := r.cad.ListPackages(ctx, repositoryObj, repository.ListPackageFilter{Key: pkgKey})
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

// Common implementation of PackageRevision update logic.
func (r *packageCommon) updatePackageRevision(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo,
	createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool) (runtime.Object, bool, error) {
	ctx, span := tracer.Start(ctx, "packageCommon::updatePackageRevision", trace.WithAttributes())
	defer span.End()

	// TODO: Is this all boilerplate??
	klog.V(3).InfoS("PackageRevision update validation started", context1.LogMetadataFrom(ctx)...)

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
				porchapi.Resource("packagerevisions"),
				name,
				fmt.Errorf(GenericConflictErrorMsg, "package revision", pkgMutexKey))
	}
	defer pkgMutex.Unlock()

	// isCreate tracks whether this is an update that creates an object (this happens in server-side apply)
	isCreate := false
	oldRepoPkgRev, err := r.getRepoPkgRev(ctx, name)
	if err != nil {
		if forceAllowCreate && apierrors.IsNotFound(err) {
			// For server-side apply, we can create the object here
			isCreate = true
		} else {
			return nil, false, err
		}
	}

	// We have to be runtime.Object (and not *api.PackageRevision) or else nil-checks fail (because a nil object is not a nil interface)
	var oldApiPkgRev runtime.Object
	if !isCreate {
		oldApiPkgRev, err = oldRepoPkgRev.GetPackageRevision(ctx)
		if err != nil {
			return nil, false, err
		}
	}

	newRuntimeObj, err := objInfo.UpdatedObject(ctx, oldApiPkgRev)
	if err != nil {
		klog.Infof("update failed to construct UpdatedObject: %v", err)
		return nil, false, err
	}

	// This type conversion is necessary because mutations work with unversioned types
	// (mostly for historical reasons).  So the server-side-apply library returns an unversioned object.
	if unversioned, isUnversioned := newRuntimeObj.(*unversionedapi.PackageRevision); isUnversioned {
		klog.Warningf("converting from unversioned to versioned object")
		typed := &porchapi.PackageRevision{}
		if err := r.scheme.Convert(unversioned, typed, nil); err != nil {
			return nil, false, fmt.Errorf("failed to convert %T to %T: %w", unversioned, typed, err)
		}
		newRuntimeObj = typed
	}

	if err := r.validateUpdate(ctx, newRuntimeObj, oldApiPkgRev, isCreate, createValidation,
		updateValidation, "PackageRevision", name); err != nil {
		return nil, false, err
	}

	newApiPkgRev, ok := newRuntimeObj.(*porchapi.PackageRevision)
	if !ok {
		return nil, false, apierrors.NewBadRequest(fmt.Sprintf("expected PackageRevision object, got %T", newRuntimeObj))
	}

	klog.V(3).InfoS("PackageRevision update validation completed", context1.LogMetadataFrom(ctx)...)

	if oldApiPkgRev != nil {
		action := getLifecycleTransition(oldApiPkgRev.(*porchapi.PackageRevision), newApiPkgRev)
		klog.InfoS("[API] Operation started for PackageRevision", context1.LogMetadataFromWithExtras(ctx, "action", action)...)
	} else {
		klog.InfoS("[API] Update operation started for PackageRevision", context1.LogMetadataFrom(ctx)...)
	}

	prKey, err := repository.PkgRevK8sName2Key(namespace, name)
	if err != nil {
		return nil, false, err
	}
	if isCreate {
		if newApiPkgRev.Spec.RepositoryName == "" {
			return nil, false, apierrors.NewBadRequest(fmt.Sprintf("invalid repositoryName %q", name))
		}
		prKey.PkgKey.RepoKey.Name = newApiPkgRev.Spec.RepositoryName
	}

	var repositoryObj configapi.Repository
	repositoryID := types.NamespacedName{Namespace: prKey.RKey().Namespace, Name: prKey.RKey().Name}
	if err := r.coreClient.Get(ctx, repositoryID, &repositoryObj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, apierrors.NewNotFound(configapi.TypeRepository.GroupResource(), repositoryID.Name)
		}
		return nil, false, apierrors.NewInternalError(fmt.Errorf("error getting repository %v: %w", repositoryID, err))
	}

	if isV1Alpha2Repo(&repositoryObj) {
		return nil, false, apierrors.NewGone(fmt.Sprintf("repository %q is managed by v1alpha2; use the v1alpha2 API", repositoryID.Name))
	}

	var parentPackage repository.PackageRevision
	if newApiPkgRev.Spec.Parent != nil && newApiPkgRev.Spec.Parent.Name != "" {
		p, err := r.getRepoPkgRev(ctx, newApiPkgRev.Spec.Parent.Name)
		if err != nil {
			return nil, false, fmt.Errorf("cannot get parent package %q: %w", newApiPkgRev.Spec.Parent.Name, err)
		}
		parentPackage = p
	}

	if isCreate {
		rev, err := r.cad.CreatePackageRevision(ctx, &repositoryObj, newApiPkgRev, parentPackage)
		if err != nil {
			klog.Infof("error creating package: %v", err)
			return nil, false, apierrors.NewInternalError(err)
		}
		createdApiPkgRev, err := rev.GetPackageRevision(ctx)
		if err != nil {
			return nil, false, apierrors.NewInternalError(err)
		}

		return createdApiPkgRev, true, nil
	}

	rev, err := r.cad.UpdatePackageRevision(ctx, 0, &repositoryObj, oldRepoPkgRev, oldApiPkgRev.(*porchapi.PackageRevision), newApiPkgRev, parentPackage)
	if err != nil {
		return nil, false, apierrors.NewInternalError(err)
	}

	updated, err := rev.GetPackageRevision(ctx)
	if err != nil {
		return nil, false, apierrors.NewInternalError(err)
	}

	if action := getLifecycleTransition(oldApiPkgRev.(*porchapi.PackageRevision), newApiPkgRev); action != "" {
		klog.InfoS("[API] Operation completed for PackageRevision", context1.LogMetadataFromWithExtras(ctx, "action", action)...)
	}

	return updated, false, nil
}

// getLifecycleTransition determines the type of update operation
func getLifecycleTransition(oldPkgRev, newPkgRev *porchapi.PackageRevision) string {
	// Handle lifecycle changes
	if oldPkgRev.Spec.Lifecycle != newPkgRev.Spec.Lifecycle {
		// Propose operation: Draft -> Proposed
		if oldPkgRev.Spec.Lifecycle == porchapi.PackageRevisionLifecycleDraft &&
			newPkgRev.Spec.Lifecycle == porchapi.PackageRevisionLifecycleProposed {
			return "Propose"
		}
		// Propose delete operation: Published -> DeletionProposed
		if oldPkgRev.Spec.Lifecycle == porchapi.PackageRevisionLifecyclePublished &&
			newPkgRev.Spec.Lifecycle == porchapi.PackageRevisionLifecycleDeletionProposed {
			return "Propose-delete"
		}
		// Reject operation: Proposed -> Draft
		if oldPkgRev.Spec.Lifecycle == porchapi.PackageRevisionLifecycleProposed &&
			newPkgRev.Spec.Lifecycle == porchapi.PackageRevisionLifecycleDraft {
			return "Reject"
		}
		// Approve operation: Proposed -> Published
		if oldPkgRev.Spec.Lifecycle == porchapi.PackageRevisionLifecycleProposed &&
			newPkgRev.Spec.Lifecycle == porchapi.PackageRevisionLifecyclePublished {
			return "Approve"
		}
		// Approve or Reject Operation: DeletionProposed -> Published
		if oldPkgRev.Spec.Lifecycle == porchapi.PackageRevisionLifecycleDeletionProposed &&
			newPkgRev.Spec.Lifecycle == porchapi.PackageRevisionLifecyclePublished {
			return "Approve/Reject"
		}
	}
	return "Update"
}

func (r *packageCommon) validateDelete(ctx context.Context, deleteValidation rest.ValidateObjectFunc, obj runtime.Object, name, namespace string) (*configapi.Repository, error) {
	// Check if the PackageRevision is Published and not in DeletionProposed state
	if pkgRev, ok := obj.(*porchapi.PackageRevision); ok {
		if pkgRev.Spec.Lifecycle == porchapi.PackageRevisionLifecyclePublished {
			return nil, apierrors.NewForbidden(
				porchapi.Resource("packagerevisions"),
				name,
				fmt.Errorf("published PackageRevisions must be proposed for deletion by setting spec.lifecycle to 'DeletionProposed' prior to deletion"))
		}
	}

	if deleteValidation != nil {
		err := deleteValidation(ctx, obj)
		if err != nil {
			klog.Infof("delete failed validation: %v", err)
			return nil, err
		}
	}

	// Check if the PackageRevision is Published and not in DeletionProposed state
	if pkgRev, ok := obj.(*porchapi.PackageRevision); ok {
		if pkgRev.Spec.Lifecycle == porchapi.PackageRevisionLifecyclePublished {
			return nil, apierrors.NewForbidden(
				porchapi.Resource("packagerevisions"),
				name,
				fmt.Errorf("published PackageRevisions must be proposed for deletion by setting spec.lifecycle to 'DeletionProposed' prior to deletion"))
		}
	}

	prKey, err := repository.PkgRevK8sName2Key(namespace, name)
	if err != nil {
		return nil, err
	}

	repositoryObj, err := r.getRepositoryObj(ctx, types.NamespacedName{Name: prKey.RKey().Name, Namespace: prKey.RKey().Namespace})
	if err != nil {
		return nil, err
	}
	return repositoryObj, nil
}

func (r *packageCommon) validateUpdate(ctx context.Context, newRuntimeObj runtime.Object, oldRuntimeObj runtime.Object, create bool,
	createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, kind string, name string) error {
	r.updateStrategy.PrepareForUpdate(ctx, newRuntimeObj, oldRuntimeObj)

	if create {
		if createValidation != nil {
			err := createValidation(ctx, newRuntimeObj)
			if err != nil {
				klog.Infof("update failed create validation: %v", err)
				return err
			}
		}

		fieldErrors := r.createStrategy.Validate(ctx, newRuntimeObj)
		if len(fieldErrors) > 0 {
			return apierrors.NewInvalid(porchapi.SchemeGroupVersion.WithKind(kind).GroupKind(), name, fieldErrors)
		}
	}

	if !create {
		if updateValidation != nil {
			err := updateValidation(ctx, newRuntimeObj, oldRuntimeObj)
			if err != nil {
				klog.Infof("update failed validation: %v", err)
				return err
			}
		}

		fieldErrors := r.updateStrategy.ValidateUpdate(ctx, newRuntimeObj, oldRuntimeObj)
		if len(fieldErrors) > 0 {
			return apierrors.NewInvalid(porchapi.SchemeGroupVersion.WithKind(kind).GroupKind(), name, fieldErrors)
		}
	}

	r.updateStrategy.Canonicalize(newRuntimeObj)
	return nil
}

func getPackageMutexKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

func getMutexForPackage(pkgMutexKey string) *sync.Mutex {
	mutexMapMutex.Lock()
	defer mutexMapMutex.Unlock()
	pkgMutex, alreadyPresent := pkgRevOperationMutexes[pkgMutexKey]
	if !alreadyPresent {
		pkgMutex = &sync.Mutex{}
		pkgRevOperationMutexes[pkgMutexKey] = pkgMutex
	}
	return pkgMutex
}
