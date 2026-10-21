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
	"maps"
	"slices"
	"time"

	"github.com/kptdev/porch/api/porch"
	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/internal/telemetry"
	"github.com/kptdev/porch/pkg/prrstore"
	"github.com/kptdev/porch/pkg/repository"
	pctx "github.com/kptdev/porch/pkg/util/context"
	"github.com/kptdev/porch/pkg/util/selector"
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

const prrTelemetryName = "PackageRevisionResources"

type packageRevisionResources struct {
	rest.TableConvertor
	packageCommon
	store prrstore.PRRStore
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
//
// KNOWN LIMITATION: List returns full file contents (Spec.Resources) for all matching PRs.
// Without filters, this can be extremely expensive on production systems with many large packages.
// In worst case, attempting to list all PRs across all namespaces could cause OOM or timeout.
//
// TODO(#XXXX): Implement lazy loading or pagination for List to support production scale.
// For now, clients should use Get with specific PR names instead of List for production workloads.
func (r *packageRevisionResources) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	ctx, span := tracer.Start(ctx, "[START]::PackageRevisionResources::List", trace.WithAttributes())
	start := time.Now()
	defer func() {
		span.End()
		telemetry.RecordAPICallDuration(prrTelemetryName, "LIST", telemetry.APIVersionV1Alpha1, time.Since(start).Seconds())
	}()

	telemetry.RecordRequestCount(ctx, prrTelemetryName, "LIST", telemetry.APIVersionV1Alpha1)

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

	// Query v1alpha1 resources from CAD engine
	if err := r.listV1Alpha1Resources(ctx, *filter, func(apiPkgResources *porchapi.PackageRevisionResources) error {
		result.Items = append(result.Items, *apiPkgResources)
		return nil
	}); err != nil {
		klog.Errorf("[API] List operation failed for v1alpha1 PackageRevisionResources: %v", err)
		return nil, err
	}

	// Query v1alpha2 resources from PRRStore
	if r.store != nil {
		if err := r.listV1Alpha2Resources(ctx, ns, *filter, func(apiPkgResources *porchapi.PackageRevisionResources) error {
			result.Items = append(result.Items, *apiPkgResources)
			return nil
		}); err != nil {
			klog.Errorf("[API] List operation failed for v1alpha2 PackageRevisionResources: %v", err)
			return nil, err
		}
	}

	klog.V(3).InfoS("List PackageRevisionResources completed",
		pctx.LogMetadataFromWithExtras(ctx, "found", len(result.Items))...)

	return result, nil
}

// listV1Alpha1Resources queries v1alpha1 resources from CAD engine
func (r *packageRevisionResources) listV1Alpha1Resources(ctx context.Context, filter repository.ListPackageRevisionFilter,
	callback func(*porchapi.PackageRevisionResources) error) error {

	if err := r.listPackageRevisions(ctx, filter, func(ctx context.Context, p repository.PackageRevision) error {
		apiPkgResources, err := p.GetResources(ctx)
		if err != nil {
			return err
		}
		return callback(apiPkgResources)
	}); err != nil {
		return err
	}

	return nil
}

// listV1Alpha2Resources queries v1alpha2 resources from PRRStore using field selectors to filter at the API server level.
// This avoids expensive full-namespace CRD scans by pre-filtering on repository and package name.
//
// KNOWN LIMITATION: Returns full file contents from PRRStore for each matching PR.
// Without explicit repo/package filters, this can query etcd for thousands of CRDs and hit PRRStore thousands of times.
// See List() method docstring for scale warnings.
func (r *packageRevisionResources) listV1Alpha2Resources(ctx context.Context, namespace string, filter repository.ListPackageRevisionFilter,
	callback func(*porchapi.PackageRevisionResources) error) error {

	// Use field selectors to filter at the API server level, avoiding expensive full namespace scans
	opts := []client.ListOption{
		client.InNamespace(namespace),
	}

	// Apply field-level filtering at the API server to reduce result set
	if filter.Key.PkgKey.RepoKey.Name != "" {
		opts = append(opts, client.MatchingFields{
			string(porchv1alpha2.PkgRevSelectorRepository): filter.Key.PkgKey.RepoKey.Name,
		})
	}

	if filter.Key.PkgKey.Package != "" {
		opts = append(opts, client.MatchingFields{
			string(porchv1alpha2.PkgRevSelectorPackageName): filter.Key.PkgKey.Package,
		})
	}

	// Note: Multiple lifecycle values require multiple queries (MatchingFields is a map).
	// For now, query all and filter in-memory if multiple lifecycles requested.

	// List v1alpha2 PackageRevision CRDs (pre-filtered by repo and package if specified)
	var prList porchv1alpha2.PackageRevisionList
	if err := r.coreClient.List(ctx, &prList, opts...); err != nil {
		klog.V(3).Infof("could not list v1alpha2 PackageRevisions: %v", err)
		return nil // Non-fatal: v1alpha2 CRDs might not exist
	}

	for _, prCRD := range prList.Items {
		// Build key from v1alpha2 CRD
		prKey := key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{
					Namespace: namespace,
					Name:      prCRD.Spec.RepositoryName,
				},
				Package: prCRD.Spec.PackageName,
			},
			Revision: -1, // -1 means main/unpublished
		}

		// Check lifecycle filter (not applied at API server for multiple values, so check here)
		if len(filter.Lifecycles) > 0 {
			if !slices.Contains(filter.Lifecycles, porchapi.PackageRevisionLifecycle(prCRD.Spec.Lifecycle)) {
				continue
			}
		}

		// Query PRRStore for resources
		resources, err := r.store.GetResources(ctx, prKey)
		if err != nil {
			klog.V(3).Infof("could not get resources from PRRStore for %v: %v", prKey, err)
			continue
		}

		if len(resources) == 0 {
			continue // Skip empty resources
		}

		apiPkgResources := &porchapi.PackageRevisionResources{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PackageRevisionResources",
				APIVersion: porchapi.SchemeGroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      prKey.K8SName(),
				Namespace: namespace,
			},
			Spec: porchapi.PackageRevisionResourcesSpec{
				PackageName: prKey.PKey().Package,
				Resources:   resources,
			},
		}

		if err := callback(apiPkgResources); err != nil {
			return err
		}
	}

	return nil
}

// Get implements the Getter interface
func (r *packageRevisionResources) Get(ctx context.Context, pkgRevK8sName string, _ *metav1.GetOptions) (runtime.Object, error) {
	ctx, span := tracer.Start(ctx, "[START]::PackageRevisionResources::Get", trace.WithAttributes())
	start := time.Now()
	defer func() {
		span.End()
		telemetry.RecordAPICallDuration(prrTelemetryName, "GET", telemetry.APIVersionV1Alpha1, time.Since(start).Seconds())
	}()

	telemetry.RecordRequestCount(ctx, prrTelemetryName, "GET", telemetry.APIVersionV1Alpha1)

	parsedName, resourceSelector, err := selector.ParsePRRGet(pkgRevK8sName)
	if err != nil {
		return nil, apierrors.NewBadRequest(err.Error())
	}

	ctx = pctx.WithNewRequestIDAndPackageRevision(ctx, parsedName)

	klog.V(3).InfoS("Get PackageRevisionResources started", pctx.LogMetadataFrom(ctx)...)

	namespace, namespaced := genericapirequest.NamespaceFrom(ctx)
	if !namespaced {
		return nil, apierrors.NewBadRequest("namespace must be specified")
	}

	prKey, err := key.PkgRevK8sName2Key(namespace, parsedName)
	if err != nil {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("invalid package revision name: %v", err))
	}

	// Check repository type to determine read path
	var repositoryObj v1alpha1.Repository
	repositoryID := types.NamespacedName{Namespace: prKey.RKey().Namespace, Name: prKey.RKey().Name}
	if err := r.coreClient.Get(ctx, repositoryID, &repositoryObj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, apierrors.NewNotFound(porchapi.PackageRevisionResourcesGVR.GroupResource(), repositoryID.Name)
		}
		return nil, apierrors.NewInternalError(fmt.Errorf("error getting repository %v: %w", repositoryID, err))
	}

	// Dispatch based on repository version
	if isV1Alpha2Repo(&repositoryObj) {
		return r.getV1Alpha2(ctx, namespace, parsedName, prKey, resourceSelector)
	}

	return r.getV1Alpha1(ctx, namespace, parsedName, resourceSelector)
}

// getV1Alpha2 handles reads for v1alpha2 repositories (PRRStore-based, database-only).
// PRRStore is the source of truth; no fallback to git.
func (r *packageRevisionResources) getV1Alpha2(ctx context.Context, namespace, name string,
	prKey key.PackageRevisionKey, resourceSelector selector.PRRGet) (runtime.Object, error) {

	if r.store == nil {
		return nil, apierrors.NewInternalError(fmt.Errorf("PRRStore not initialized for v1alpha2 repository"))
	}

	// Get resources from PRRStore (source of truth for v1alpha2)
	resources, err := r.store.GetResources(ctx, prKey)
	if err != nil {
		klog.Errorf("[API] Get operation failed for v1alpha2 PackageRevisionResources %s: PRRStore lookup failed: %v", name, err)
		return nil, apierrors.NewInternalError(fmt.Errorf("get resources from PRRStore: %w", err))
	}

	if len(resources) == 0 {
		klog.Errorf("[API] Get operation failed for v1alpha2 PackageRevisionResources %s: PRRStore returned empty resources", name)
		return nil, apierrors.NewNotFound(porchapi.PackageRevisionResourcesGVR.GroupResource(), name)
	}

	// Build result
	result := &porchapi.PackageRevisionResources{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevisionResources",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: porchapi.PackageRevisionResourcesSpec{
			PackageName: prKey.PKey().Package,
			Resources:   resources,
		},
	}

	// Apply resource selector: filter by file paths if specified
	if !resourceSelector.IsAllFiles() && len(resourceSelector.FilePaths) > 0 {
		filtered := make(map[string]string)
		for _, filePath := range resourceSelector.FilePaths {
			if val, ok := resources[filePath]; ok {
				filtered[filePath] = val
			}
		}
		result.Spec.Resources = filtered
	}

	result.Spec.ResourcePaths = slices.Collect(maps.Keys(result.Spec.Resources))

	if resourceSelector.PathOnly {
		result.Spec.Resources = nil
	}

	klog.V(3).InfoS("Get PackageRevisionResources completed (from PRRStore)", pctx.LogMetadataFrom(ctx)...)
	return result, nil
}

// getV1Alpha1 handles reads for v1alpha1 repositories (CAD engine/git-backed).
func (r *packageRevisionResources) getV1Alpha1(ctx context.Context, namespace, name string,
	resourceSelector selector.PRRGet) (runtime.Object, error) {

	// Get package from CAD engine
	pkg, err := r.getRepoPkgRevForResources(ctx, name)
	if err != nil {
		klog.Errorf("[API] Get operation failed for v1alpha1 PackageRevisionResources %s: %v", name, err)
		return nil, err
	}

	apiPkgResources, err := pkg.GetFilteredResources(ctx, resourceSelector)
	if err != nil {
		return nil, err
	}

	apiPkgResources.Spec.ResourcePaths = slices.Collect(maps.Keys(apiPkgResources.Spec.Resources))

	if resourceSelector.PathOnly {
		apiPkgResources.Spec.Resources = nil
	}

	klog.V(3).InfoS("Get PackageRevisionResources completed", pctx.LogMetadataFrom(ctx)...)
	return apiPkgResources, nil
}

// Update finds a resource in the storage and updates it. Some implementations
// may allow updates creates the object - they should set the created boolean
// to true.
func (r *packageRevisionResources) Update(ctx context.Context, pkgRevK8sName string, objInfo rest.UpdatedObjectInfo, _ rest.ValidateObjectFunc,
	updateValidation rest.ValidateObjectUpdateFunc, _ bool, _ *metav1.UpdateOptions) (runtime.Object, bool, error) {
	ctx, span := tracer.Start(ctx, "[START]::PackageRevisionResources::Update", trace.WithAttributes())
	start := time.Now()
	defer func() {
		span.End()
		telemetry.RecordAPICallDuration(prrTelemetryName, "UPDATE", telemetry.APIVersionV1Alpha1, time.Since(start).Seconds())
	}()

	telemetry.RecordRequestCount(ctx, prrTelemetryName, "UPDATE", telemetry.APIVersionV1Alpha1)

	parsedName, resourceSelector, err := selector.ParsePRRUpdate(pkgRevK8sName)
	if err != nil {
		return nil, false, apierrors.NewBadRequest(err.Error())
	}

	ctx = pctx.WithNewRequestIDAndPackageRevision(ctx, parsedName)

	namespace, namespaced := genericapirequest.NamespaceFrom(ctx)
	if !namespaced {
		return nil, false, apierrors.NewBadRequest("namespace must be specified")
	}

	pkgMutexKey := getPackageMutexKey(namespace, parsedName)
	pkgMutex := getMutexForPackage(pkgMutexKey)
	locked := pkgMutex.TryLock()
	if !locked {
		return nil, false,
			apierrors.NewConflict(
				porchapi.Resource("packagerevisionresources"),
				parsedName,
				fmt.Errorf(GenericConflictErrorMsg, "package revision resources", pkgMutexKey))
	}
	defer pkgMutex.Unlock()

	oldRepoPkgRev, err := r.getRepoPkgRevForResources(ctx, parsedName)
	if err != nil {
		klog.Errorf("[API] Update operation failed for PackageRevisionResources %s: %v", parsedName, err)
		return nil, false, err
	}

	// Get old resources
	oldApiPkgRevResources, err := oldRepoPkgRev.GetResources(ctx)
	if err != nil {
		klog.Infof("update failed to retrieve old object: %v", err)
		return nil, false, err
	}

	// Get new object from update info
	newRuntimeObj, err := objInfo.UpdatedObject(ctx, oldApiPkgRevResources)
	if err != nil {
		klog.Infof("update failed to construct UpdatedObject: %v", err)
		return nil, false, err
	}

	var newObj *porchapi.PackageRevisionResources
	switch obj := newRuntimeObj.(type) {
	case *porchapi.PackageRevisionResources:
		newObj = obj
	case *porch.PackageRevisionResources: // internal version
		newObj = &porchapi.PackageRevisionResources{}
		if err := r.scheme.Convert(obj, newObj, nil); err != nil {
			return nil, false, apierrors.NewBadRequest(fmt.Sprintf("could not convert to external type %T", obj))
		}
	}

	// Validate
	if updateValidation != nil {
		err := updateValidation(ctx, newObj, oldApiPkgRevResources)
		if err != nil {
			klog.Infof("update failed validation: %v", err)
			return nil, false, err
		}
	}

	klog.InfoS("[API] Update operation started for PackageRevisionResources (v1alpha1)", pctx.LogMetadataFrom(ctx)...)

	prKey, err := key.PkgRevK8sName2Key(namespace, parsedName)
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
	submittedFiles := filePathsOf(newObj.Spec.Resources)

	if isV1Alpha2Repo(&repositoryObj) {
		// v1alpha2: write resources without render. PR controller renders async.
		rev, err = r.cad.UpdatePackageResourcesWithoutRender(ctx, &repositoryObj, oldRepoPkgRev, oldApiPkgRevResources, newObj)
		if err != nil {
			return nil, false, apierrors.NewInternalError(err)
		}
		r.patchRenderRequestAnnotation(ctx, namespace, parsedName, rev.ResourceVersion())
	} else {
		rev, renderStatus, err = r.cad.UpdatePackageResources(ctx, &repositoryObj, oldRepoPkgRev, oldApiPkgRevResources, newObj, resourceSelector)
		if err != nil {
			return nil, false, apierrors.NewInternalError(err)
		}
	}

	// Build result using the original submitted files for filtering
	created, err := r.makeResult(ctx, rev, oldApiPkgRevResources.Spec.Resources, submittedFiles, resourceSelector)
	if err != nil {
		return nil, false, apierrors.NewInternalError(err)
	}
	if renderStatus != nil {
		created.Status.RenderStatus = *renderStatus
	}

	klog.InfoS("[API] Update operation completed for PackageRevisionResources (v1alpha1)", pctx.LogMetadataFrom(ctx)...)
	return created, false, nil
}

// patchRenderRequestAnnotation patches the render-request annotation on the
// v1alpha2 PackageRevision CRD to trigger async rendering.
func (r *packageRevisionResources) patchRenderRequestAnnotation(ctx context.Context, namespace, pkgRevK8sName, resourceVersion string) {
	pr := &porchv1alpha2.PackageRevision{}
	key := client.ObjectKey{Namespace: namespace, Name: pkgRevK8sName}
	if err := r.coreClient.Get(ctx, key, pr); err != nil {
		klog.Warningf("failed to get v1alpha2 PR %s/%s for render-request patch: %v", namespace, pkgRevK8sName, err)
		return
	}

	patch := client.MergeFrom(pr.DeepCopy())
	if pr.Annotations == nil {
		pr.Annotations = map[string]string{}
	}
	pr.Annotations[porchv1alpha2.AnnotationRenderRequest] = resourceVersion
	if err := r.coreClient.Patch(ctx, pr, patch); err != nil {
		klog.Warningf("failed to patch render-request annotation on %s/%s: %v", namespace, pkgRevK8sName, err)
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

	prKey, err := key.PkgRevK8sName2Key(namespace, name)
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

func (r *packageRevisionResources) makeResult(ctx context.Context, rev repository.PackageRevision, oldResources map[string]string, submittedFiles selector.PRRGet, resourceSelector selector.PRRUpdate) (*porchapi.PackageRevisionResources, error) {
	if resourceSelector.Partial {
		updated, err := rev.GetFilteredResources(ctx, submittedFiles)
		if err != nil {
			return nil, err
		}
		updated.Spec.Resources = r.diffNewAndChanged(oldResources, updated.Spec.Resources)

		return updated, nil
	}

	return rev.GetResources(ctx)
}

func filePathsOf(resources map[string]string) selector.PRRGet {
	filePaths := make([]string, 0, len(resources))
	for filePath := range resources {
		filePaths = append(filePaths, filePath)
	}
	return selector.PRRGet{FilePaths: filePaths}
}

func (r *packageRevisionResources) diffNewAndChanged(old, new map[string]string) map[string]string {
	result := make(map[string]string)
	for kNew, vNew := range new {
		if vOld, ok := old[kNew]; !ok || vOld != vNew {
			result[kNew] = vNew
		}
	}
	return result
}
