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

package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/scheduler"
	"github.com/kptdev/porch/pkg/task"
	"github.com/kptdev/porch/pkg/util"
	pctx "github.com/kptdev/porch/pkg/util/context"
	"github.com/kptdev/porch/pkg/util/selector"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/klog/v2"
)

var tracer = otel.Tracer("engine")

const (
	OptimisticLockErrorMsg = "the object has been modified; please apply your changes to the latest version and try again"
)

type CaDEngine interface {
	// ObjectCache() is a cache of all our objects.
	ObjectCache() WatcherManager

	UpdatePackageResources(ctx context.Context, repositoryObj *configapi.Repository, oldPackage repository.PackageRevision, old, new *porchapi.PackageRevisionResources, resourceSelector selector.PRRUpdate) (repository.PackageRevision, *kptfilev1.RenderStatus, error)
	UpdatePackageResourcesWithoutRender(ctx context.Context, repositoryObj *configapi.Repository, oldPackage repository.PackageRevision, old, new *porchapi.PackageRevisionResources) (repository.PackageRevision, error)

	ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error)
	CreatePackageRevision(ctx context.Context, repositoryObj *configapi.Repository, obj *porchapi.PackageRevision, parent repository.PackageRevision) (repository.PackageRevision, error)
	UpdatePackageRevision(ctx context.Context, version int, repositoryObj *configapi.Repository, oldPackage repository.PackageRevision, old, new *porchapi.PackageRevision, parent repository.PackageRevision) (repository.PackageRevision, error)
	DeletePackageRevision(ctx context.Context, repositoryObj *configapi.Repository, obj repository.PackageRevision) error

	ListPackages(ctx context.Context, repositorySpec *configapi.Repository, filter repository.ListPackageFilter) ([]repository.Package, error)

	FindAllUpstreamReferencesInRepositories(ctx context.Context, namespace, prName string) (string, error)
}

func NewCaDEngine(opts ...EngineOption) (CaDEngine, error) {
	engine := &cadEngine{
		taskHandler: task.GetDefaultTaskHandler(),
	}

	for _, opt := range opts {
		if err := opt.apply(engine); err != nil {
			return nil, err
		}
	}
	return engine, nil
}

type cadEngine struct {
	cache cachetypes.Cache

	userInfoProvider repository.UserInfoProvider
	watcherManager   *watcherManager
	taskHandler      task.TaskHandler
	renderScheduler  *scheduler.RenderScheduler
	asyncRendering   bool
}

var _ CaDEngine = &cadEngine{}

// ObjectCache is a cache of all our objects.
func (cad *cadEngine) ObjectCache() WatcherManager {
	return cad.watcherManager
}

func (cad *cadEngine) OpenRepository(ctx context.Context, repositorySpec *configapi.Repository) (repository.Repository, error) {
	ctx, span := tracer.Start(ctx, "cadEngine::OpenRepository", trace.WithAttributes())
	defer span.End()

	return cad.cache.OpenRepository(ctx, repositorySpec)
}

func (cad *cadEngine) ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "cadEngine::ListPackageRevisions", trace.WithAttributes())
	defer span.End()

	return cad.cache.ListPackageRevisions(ctx, filter)
}

func (cad *cadEngine) CreatePackageRevision(ctx context.Context, repositoryObj *configapi.Repository, newPr *porchapi.PackageRevision, parent repository.PackageRevision) (repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "cadEngine::CreatePackageRevision", trace.WithAttributes())
	defer span.End()

	if len(newPr.Spec.Tasks) > 1 {
		return nil, pkgerrors.New("task list must not contain more than one task")
	}

	if len(newPr.Spec.Tasks) == 0 {
		newPr.Spec.Tasks = []porchapi.Task{{
			Type: porchapi.TaskTypeInit,
			Init: &porchapi.PackageInitTaskSpec{
				Description: fmt.Sprintf("%s description", newPr.Spec.PackageName),
			},
		}}
	}

	// Validate package lifecycle. Cannot create a final package
	switch newPr.Spec.Lifecycle {
	case "":
		// Set draft as default
		newPr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDraft
	case porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed:
		// These values are ok
	case porchapi.PackageRevisionLifecyclePublished, porchapi.PackageRevisionLifecycleDeletionProposed:
		// TODO: generate errors that can be translated to correct HTTP responses
		return nil, fmt.Errorf("cannot create a package revision with lifecycle value 'Final'")
	default:
		return nil, fmt.Errorf("unsupported lifecycle value: %s", newPr.Spec.Lifecycle)
	}

	repo, err := cad.cache.OpenRepository(ctx, repositoryObj)
	if err != nil {
		return nil, err
	}

	pkgKey := repository.FromFullPathname(repo.Key(), newPr.Spec.PackageName)
	klog.InfoS("[CaD Engine] Validating and preparing package creation for PackageRevision",
		pctx.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[CaD Engine] Package creation delegated to cache for PackageRevision",
			pctx.LogMetadataFrom(ctx)...)
	}()

	if err := util.ValidPkgRevObjName(repositoryObj.Name, pkgKey.Path, pkgKey.Package, newPr.Spec.WorkspaceName); err != nil {
		return nil, fmt.Errorf("failed to create packagerevision: %w", err)
	}

	sameRepoFilter := repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: newPr.Spec.RepositoryName,
				},
			},
		},
	}
	sameRepoRevs, err := repo.ListPackageRevisions(ctx, sameRepoFilter)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "error listing package revisions")
	}

	var revs []repository.PackageRevision
	for _, rev := range sameRepoRevs {
		if rev.Key().PkgKey.Package == newPr.Spec.PackageName {
			revs = append(revs, rev)
		}
	}

	if err := ensureUniqueWorkspaceName(newPr, revs); err != nil {
		return nil, err
	}

	if newPr.Spec.Tasks[0].Type == porchapi.TaskTypeClone {
		if err := validateCloneTask(newPr, revs); err != nil {
			return nil, err
		}
	}

	if porchapi.IsPackageCreation(newPr) {
		if err := repository.ValidatePackagePathOverlap(newPr, sameRepoRevs); err != nil {
			return nil, err
		}
	}

	if newPr.Spec.Tasks[0].Type == porchapi.TaskTypeUpgrade {
		if err := validateUpgradeTask(ctx, revs, newPr.Spec.Tasks[0].Upgrade); err != nil {
			return nil, err
		}
	}

	// Create a draft package revision
	draft, err := repo.CreatePackageRevisionDraft(ctx, newPr)
	if err != nil {
		return nil, err
	}

	// Setup rollback function in case of errors
	rollback := func() {
		// Try to convert the draft to a PackageRevision for deletion
		// If the conversion fails, we can't do much more since we can't delete a draft directly
		if pkgRev, err := repo.ClosePackageRevisionDraft(ctx, draft, 0); err == nil {
			if err := repo.DeletePackageRevision(ctx, pkgRev); err != nil {
				klog.Warningf("Failed to rollback package revision creation: %v", err)
			}
		} else {
			// If we can't convert the draft, log the error and continue
			// The draft will be cleaned up by the repository's garbage collection
			klog.Warningf("Failed to convert draft to package revision for rollback: %v", err)
		}
	}

	// Apply tasks. When async rendering is enabled, skip the synchronous pipeline
	// here. Clone and upgrade copy already-rendered package contents, so
	// ScheduleRender(skipRender=true) closes the draft without running the
	// pipeline again. Init with an empty pipeline is a no-op the same way.
	if err := cad.taskHandler.ApplyTask(ctx, draft, newPr, cad.asyncRendering); err != nil {
		rollback()
		return nil, err
	}

	// Update lifecycle
	if err := draft.UpdateLifecycle(ctx, newPr.Spec.Lifecycle); err != nil {
		rollback()
		return nil, err
	}

	var repoPkgRev repository.PackageRevision
	if cad.asyncRendering {
		repoPkgRev, err = cad.scheduleRender(ctx, repo, draft, true)
		if err != nil {
			if (apierrors.IsUnauthorized(err) || apierrors.IsForbidden(err)) && repository.AnyBlockOwnerDeletionSet(newPr.ObjectMeta) {
				return nil, fmt.Errorf("failed to create internal PackageRev object, because blockOwnerDeletion is enabled for some ownerReference "+
					"(it is likely that the serviceaccount of porch-server does not have the rights to update finalizers in the owner object): %w", err)
			}
			return nil, err
		}
	} else {
		repoPkgRev, err = repo.ClosePackageRevisionDraft(ctx, draft, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to close package revision draft: %w", err)
		}
	}

	if err := cad.updatePkgRevMeta(ctx, repoPkgRev, newPr); err != nil {
		return nil, err
	}

	return repoPkgRev, nil
}

// validateUpgradeTask returns an error if one of the source revisions of the upgrade are not published
func validateUpgradeTask(ctx context.Context, revs []repository.PackageRevision, spec *porchapi.PackageUpgradeTaskSpec) error {
	parts := []string{
		spec.OldUpstream.Name,
		spec.NewUpstream.Name,
		spec.LocalPackageRevisionRef.Name,
	}
	for _, rev := range revs {
		if slices.Contains(parts, rev.KubeObjectName()) {
			if !porchapi.LifecycleIsPublished(rev.Lifecycle(ctx)) {
				return pkgerrors.Errorf("all source PackageRevisions of upgrade task must be published, %q is not", rev.KubeObjectName())
			}
		}
	}
	return nil
}

// The workspaceName must be unique, because it is used to generate the package revision's metadata.name.
func ensureUniqueWorkspaceName(obj *porchapi.PackageRevision, existingRevs []repository.PackageRevision) error {
	for _, r := range existingRevs {
		k := r.Key()
		if k.WorkspaceName == obj.Spec.WorkspaceName {
			return fmt.Errorf("package revision workspaceNames must be unique; package revision with name %s in repo %s with "+
				"workspaceName %s already exists", obj.Spec.PackageName, obj.Spec.RepositoryName, obj.Spec.WorkspaceName)
		}
	}
	return nil
}

// validateCloneTask returns an error if the package already exists in the repository
func validateCloneTask(obj *porchapi.PackageRevision, existingRevs []repository.PackageRevision) error {
	for _, r := range existingRevs {
		k := r.Key()
		if k.PkgKey.RepoKey.Name == obj.Spec.RepositoryName && k.PkgKey.Package == obj.Spec.PackageName {
			return fmt.Errorf("`clone` cannot create a new revision for package %q that already exists in repo %q; make subsequent revisions using `copy`",
				obj.Spec.PackageName, obj.Spec.RepositoryName)
		}
	}
	return nil
}

func (cad *cadEngine) UpdatePackageRevision(
	ctx context.Context,
	version int,
	repositoryObj *configapi.Repository,
	repoPr repository.PackageRevision,
	oldObj, newObj *porchapi.PackageRevision,
	parent repository.PackageRevision) (repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "cadEngine::UpdatePackageRevision", trace.WithAttributes())
	defer span.End()

	newRV := newObj.GetResourceVersion()
	if len(newRV) == 0 {
		return nil, fmt.Errorf("resourceVersion must be specified for an update")
	}

	if newRV != oldObj.GetResourceVersion() {
		return nil, apierrors.NewConflict(porchapi.Resource("packagerevisions"), oldObj.GetName(), fmt.Errorf("%s", OptimisticLockErrorMsg))
	}

	repo, err := cad.cache.OpenRepository(ctx, repositoryObj)
	if err != nil {
		return nil, err
	}

	klog.InfoS("[CaD Engine] Processing lifecycle change and preparing update for PackageRevision",
		pctx.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[CaD Engine] Lifecycle change processed and delegated to cache for PackageRevision",
			pctx.LogMetadataFrom(ctx)...)
	}()

	// Check if the PackageRevision is in the terminating state
	// and this request removes the last finalizer.
	repoPkgRev := repoPr

	// If this is in the terminating state and we are removing the last finalizer,
	// we delete the resource instead of updating it.
	if repoPkgRev.GetMeta().DeletionTimestamp != nil && len(newObj.Finalizers) == 0 {
		if err := cad.updatePkgRevMeta(ctx, repoPkgRev, newObj); err != nil {
			return nil, err
		}
		if err := cad.deletePackageRevision(ctx, repo, repoPkgRev); err != nil {
			return nil, err
		}
		return repoPkgRev, nil
	}

	// Validate package lifecycle. Can only update a draft.
	switch lifecycle := oldObj.Spec.Lifecycle; lifecycle {

	case porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed:
		// Draft or proposed can be updated.

	case porchapi.PackageRevisionLifecyclePublished, porchapi.PackageRevisionLifecycleDeletionProposed:
		// Only metadata (currently labels and annotations) and lifecycle can be updated for published packages.
		if oldObj.Spec.Lifecycle != newObj.Spec.Lifecycle {
			if err := repoPr.UpdateLifecycle(ctx, newObj.Spec.Lifecycle); err != nil {
				return nil, err
			}
		}

		err = cad.updatePkgRevMeta(ctx, repoPkgRev, newObj)
		if err != nil {
			return nil, err
		}
		sent := cad.watcherManager.NotifyPackageRevisionChange(watch.Modified, repoPkgRev)
		klog.Infof("engine: sent %d for updated PackageRevision metadata %s/%s", sent, repoPkgRev.KubeObjectNamespace(), repoPkgRev.KubeObjectName())
		return repoPkgRev, nil

	default:
		return nil, fmt.Errorf("invalid original lifecycle value: %q", lifecycle)
	}

	switch lifecycle := newObj.Spec.Lifecycle; lifecycle {

	case porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed, porchapi.PackageRevisionLifecyclePublished, porchapi.PackageRevisionLifecycleDeletionProposed:
		// These values are ok

	default:
		return nil, fmt.Errorf("invalid desired lifecycle value: %q", lifecycle)
	}

	if cad.asyncRendering {
		return cad.updatePackageRevisionAsync(ctx, version, repo, repoPr, oldObj, newObj)
	}

	// Do we need to clean up this draft later?
	draft, err := repo.UpdatePackageRevision(ctx, repoPr)
	if err != nil {
		return nil, err
	}

	renderErr := cad.taskHandler.DoPRMutations(ctx, repoPr, oldObj, newObj, draft)

	if renderErr != nil {
		if result, err := handleMutationError(renderErr, newObj); err != nil {
			return result, err
		}
	}

	if err := draft.UpdateLifecycle(ctx, newObj.Spec.Lifecycle); err != nil {
		return nil, err
	}

	// Updates are done.
	repoPkgRev, err = repo.ClosePackageRevisionDraft(ctx, draft, version)
	if err != nil {
		return nil, err
	}

	err = cad.updatePkgRevMeta(ctx, repoPkgRev, newObj)
	if err != nil {
		if (apierrors.IsUnauthorized(err) || apierrors.IsForbidden(err)) && repository.AnyBlockOwnerDeletionSet(newObj.ObjectMeta) {
			return nil, fmt.Errorf("failed to update internal PackageRev object, because blockOwnerDeletion is enabled for some ownerReference "+
				"(it is likely that the serviceaccount of porch-server does not have the rights to update finalizers in the owner object): %w", err)
		}
		return nil, err
	}

	sent := cad.watcherManager.NotifyPackageRevisionChange(watch.Modified, repoPkgRev)
	klog.Infof("engine: sent %d for updated PackageRevision %s/%s", sent, repoPkgRev.KubeObjectNamespace(), repoPkgRev.KubeObjectName())

	return repoPkgRev, nil
}

func (cad *cadEngine) updatePkgRevMeta(ctx context.Context, repoPkgRev repository.PackageRevision, apiPkgRev *porchapi.PackageRevision) error {
	pkgRevMeta := metav1.ObjectMeta{
		Name:              repoPkgRev.KubeObjectName(),
		Namespace:         repoPkgRev.KubeObjectNamespace(),
		Labels:            apiPkgRev.Labels,
		Annotations:       apiPkgRev.Annotations,
		Finalizers:        apiPkgRev.Finalizers,
		OwnerReferences:   apiPkgRev.OwnerReferences,
		CreationTimestamp: apiPkgRev.GetCreationTimestamp(),
		DeletionTimestamp: apiPkgRev.DeletionTimestamp,
	}
	return repoPkgRev.SetMeta(ctx, pkgRevMeta)
}

func (cad *cadEngine) DeletePackageRevision(ctx context.Context, repositoryObj *configapi.Repository, pr2Del repository.PackageRevision) error {
	ctx, span := tracer.Start(ctx, "cadEngine::DeletePackageRevision", trace.WithAttributes())
	defer span.End()

	klog.InfoS("[CaD Engine] Preparing to delete PackageRevision",
		pctx.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[CaD Engine] PackageRevision deletion delegated to cache",
			pctx.LogMetadataFrom(ctx)...)
	}()

	repo, err := cad.cache.OpenRepository(ctx, repositoryObj)
	if err != nil {
		return err
	}

	return cad.deletePackageRevision(ctx, repo, pr2Del)
}

func (cad *cadEngine) deletePackageRevision(ctx context.Context, repo repository.Repository, repoPkgRev repository.PackageRevision) error {
	ctx, span := tracer.Start(ctx, "cadEngine::deletePackageRevision", trace.WithAttributes())
	defer span.End()

	if err := repo.DeletePackageRevision(ctx, repoPkgRev); err != nil {
		return err
	}

	return nil
}

func (cad *cadEngine) ListPackages(ctx context.Context, repositorySpec *configapi.Repository, filter repository.ListPackageFilter) ([]repository.Package, error) {
	ctx, span := tracer.Start(ctx, "cadEngine::ListPackages", trace.WithAttributes())
	defer span.End()

	repo, err := cad.cache.OpenRepository(ctx, repositorySpec)
	if err != nil {
		return nil, err
	}

	pkgs, err := repo.ListPackages(ctx, filter)
	if err != nil {
		return nil, err
	}
	var packages []repository.Package
	packages = append(packages, pkgs...)

	return packages, nil
}

func (cad *cadEngine) UpdatePackageResources(ctx context.Context, repositoryObj *configapi.Repository, pr2Update repository.PackageRevision, oldRes, newRes *porchapi.PackageRevisionResources, resourceSelector selector.PRRUpdate) (repository.PackageRevision, *kptfilev1.RenderStatus, error) {
	ctx, span := tracer.Start(ctx, "cadEngine::UpdatePackageResources", trace.WithAttributes())
	defer span.End()

	klog.InfoS("[CaD Engine] Processing resource updates for PackageRevision", pctx.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[CaD Engine] Resource updates processed and delegated to cache for PackageRevision",
			pctx.LogMetadataFrom(ctx)...)
	}()

	rev, err := pr2Update.GetPackageRevision(ctx, true)
	if err != nil {
		return nil, nil, err
	}

	// Validate package lifecycle. Can only update a draft.
	switch lifecycle := rev.Spec.Lifecycle; lifecycle {
	default:
		return nil, nil, fmt.Errorf("invalid original lifecycle value: %q", lifecycle)
	case porchapi.PackageRevisionLifecycleDraft:
		// Only drafts can be updated.
	case porchapi.PackageRevisionLifecycleProposed, porchapi.PackageRevisionLifecyclePublished, porchapi.PackageRevisionLifecycleDeletionProposed:
		// TODO: generate errors that can be translated to correct HTTP responses
		return nil, nil, fmt.Errorf("cannot update a package revision with lifecycle value %q; package must be Draft", lifecycle)
	}

	newRV := newRes.GetResourceVersion()
	if len(newRV) == 0 {
		return nil, nil, fmt.Errorf("resourceVersion must be specified for an update")
	}

	if newRV != oldRes.GetResourceVersion() {
		return nil, nil, apierrors.NewConflict(porchapi.Resource("packagerevisionresources"), oldRes.GetName(), errors.New(OptimisticLockErrorMsg))
	}

	repo, err := cad.cache.OpenRepository(ctx, repositoryObj)
	if err != nil {
		return nil, nil, err
	}
	draft, err := repo.UpdatePackageRevision(ctx, pr2Update)
	if err != nil {
		return nil, nil, err
	}

	prr := cad.makePackageRevisionResources(oldRes.Spec.Resources, newRes.Spec.Resources, resourceSelector)
	newRes.Spec.Resources = prr.Spec.Resources

	if cad.asyncRendering {
		return cad.updatePackageResourcesAsync(ctx, repo, draft, newRes, prr)
	}

	if newRes.Spec.DisableRender {
		if err := draft.UpdateResources(ctx, prr, &porchapi.Task{Type: porchapi.TaskTypePush}); err != nil {
			return nil, nil, err
		}
		repoPkgRev, closeErr := repo.ClosePackageRevisionDraft(ctx, draft, 0)
		if closeErr != nil {
			return nil, nil, closeErr
		}
		return repoPkgRev, nil, nil
	}

	renderStatus, renderErr := cad.taskHandler.DoPRResourceMutations(ctx, pr2Update, draft, oldRes, newRes)

	if renderErr != nil {
		if result, err := handleMutationError(renderErr, rev); err != nil {
			return result, renderStatus, err
		}
	}

	// No render error, or annotation allows push on render failure
	repoPkgRev, closeErr := repo.ClosePackageRevisionDraft(ctx, draft, 0)
	if closeErr != nil {
		if renderErr != nil {
			return nil, renderStatus, fmt.Errorf("failed to push package to remote: %w; render error: %v", closeErr, renderErr)
		}
		return nil, renderStatus, closeErr
	}
	if renderErr != nil {
		return nil, renderStatus, fmt.Errorf("error rendering package in kpt function pipeline. "+
			"Package pushed to remote despite render failure. Details: %w", renderErr)
	}
	return repoPkgRev, renderStatus, nil
}

func (cad *cadEngine) FindAllUpstreamReferencesInRepositories(ctx context.Context, namespace, prName string) (string, error) {
	return cad.cache.FindAllUpstreamReferencesInRepositories(ctx, namespace, prName)
}

// UpdatePackageResourcesWithoutRender writes new resources without rendering.
// Used by the PRR handler for v1alpha2 repos where the PR controller renders async.
func (cad *cadEngine) UpdatePackageResourcesWithoutRender(ctx context.Context, repositoryObj *configapi.Repository, pr2Update repository.PackageRevision, oldRes, newRes *porchapi.PackageRevisionResources) (repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "cadEngine::UpdatePackageResourcesWithoutRender", trace.WithAttributes())
	defer span.End()

	klog.InfoS("[CaD Engine] Writing resources without render for v1alpha2", pctx.LogMetadataFrom(ctx)...)

	newRV := newRes.GetResourceVersion()
	if len(newRV) == 0 {
		return nil, fmt.Errorf("resourceVersion must be specified for an update")
	}
	if newRV != oldRes.GetResourceVersion() {
		return nil, apierrors.NewConflict(porchapi.Resource("packagerevisionresources"), oldRes.GetName(), errors.New(OptimisticLockErrorMsg))
	}

	switch lifecycle := pr2Update.Lifecycle(ctx); lifecycle {
	case porchapi.PackageRevisionLifecycleDraft:
	default:
		return nil, fmt.Errorf("cannot update a package revision with lifecycle value %q; package must be Draft", lifecycle)
	}

	if err := util.ValidateResourcePaths(newRes.Spec.Resources); err != nil {
		return nil, err
	}

	repo, err := cad.cache.OpenRepository(ctx, repositoryObj)
	if err != nil {
		return nil, err
	}
	draft, err := repo.UpdatePackageRevision(ctx, pr2Update)
	if err != nil {
		return nil, err
	}

	prr := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: newRes.Spec.Resources,
		},
	}
	if err := draft.UpdateResources(ctx, prr, &porchapi.Task{Type: porchapi.TaskTypeRender}); err != nil {
		return nil, err
	}

	return repo.ClosePackageRevisionDraft(ctx, draft, 0)
}

// handleMutationError decides whether to bail out or allow push-on-render-failure.
// Returns a non-nil error to signal the caller should return immediately.
// Returns a nil error to signal the caller should proceed to close the draft.
func handleMutationError(renderErr error, rev *porchapi.PackageRevision) (repository.PackageRevision, error) {
	// If persistence failed after render, never push — draft contents are stale.
	var persistErr *task.RenderPersistError
	if errors.As(renderErr, &persistErr) {
		return nil, renderErr
	}

	// Only apply push-on-render-failure for actual render errors.
	// For any other error (e.g. draft.UpdateResources failure when render succeeded),
	// never push — the draft contents may be stale.
	var renderError *task.RenderError
	if !errors.As(renderErr, &renderError) {
		return nil, renderErr
	}

	if !rev.IsPushOnRenderFailure() {
		return nil, fmt.Errorf("error rendering package in kpt function pipeline. "+
			"Package NOT pushed to remote. Fix locally (until 'kpt fn render' succeeds) and retry. Details: %w", renderErr)
	}

	return nil, nil
}

func (cad *cadEngine) makePackageRevisionResources(oldResources, newResources map[string]string, resourceSelector selector.PRRUpdate) *porchapi.PackageRevisionResources {
	if resourceSelector.Partial {
		clonedOldResources := map[string]string{}
		for k, v := range oldResources {
			clonedOldResources[k] = v
		}
		for k, v := range newResources {
			clonedOldResources[k] = v
		}
		return &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: clonedOldResources,
			},
		}
	}

	return &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: newResources,
		},
	}
}

func (cad *cadEngine) scheduleRender(
	ctx context.Context,
	repo repository.Repository,
	draft repository.PackageRevisionDraft,
	skipRender bool,
) (repository.PackageRevision, error) {
	if cad.renderScheduler == nil {
		return nil, fmt.Errorf("async rendering is enabled but render scheduler is not configured")
	}
	return cad.renderScheduler.ScheduleRender(ctx, repo, draft, skipRender)
}

func (cad *cadEngine) updatePackageRevisionAsync(
	ctx context.Context,
	version int,
	repo repository.Repository,
	repoPr repository.PackageRevision,
	oldObj, newObj *porchapi.PackageRevision,
) (repository.PackageRevision, error) {
	if err := cad.rejectLifecycleChangeDuringRender(ctx, repoPr, oldObj, newObj); err != nil {
		return nil, err
	}

	newKptfileContent, kptFileChanged, err := task.PatchKptfile(ctx, repoPr, newObj)
	if err != nil {
		return nil, err
	}

	newLifecycle := newObj.Spec.Lifecycle
	if kptFileChanged || newLifecycle != oldObj.Spec.Lifecycle {
		repoPr, err = cad.applyPackageRevisionSpecChanges(ctx, version, repo, repoPr, newObj, newLifecycle, newKptfileContent, kptFileChanged)
		if err != nil {
			return nil, err
		}
	}

	err = cad.updatePkgRevMeta(ctx, repoPr, newObj)
	if err != nil {
		if (apierrors.IsUnauthorized(err) || apierrors.IsForbidden(err)) && repository.AnyBlockOwnerDeletionSet(newObj.ObjectMeta) {
			return nil, fmt.Errorf("failed to update internal PackageRev object, because blockOwnerDeletion is enabled for some ownerReference "+
				"(it is likely that the serviceaccount of porch-server does not have the rights to update finalizers in the owner object): %w", err)
		}
		return nil, err
	}

	if cad.watcherManager != nil {
		sent := cad.watcherManager.NotifyPackageRevisionChange(watch.Modified, repoPr)
		klog.Infof("engine: sent %d for updated PackageRevision %s/%s", sent, repoPr.KubeObjectNamespace(), repoPr.KubeObjectName())
	}
	return repoPr, nil
}

func (cad *cadEngine) applyPackageRevisionSpecChanges(
	ctx context.Context,
	version int,
	repo repository.Repository,
	repoPr repository.PackageRevision,
	newObj *porchapi.PackageRevision,
	newLifecycle porchapi.PackageRevisionLifecycle,
	newKptfileContent string,
	kptFileChanged bool,
) (repository.PackageRevision, error) {
	draft, err := repo.UpdatePackageRevision(ctx, repoPr)
	if err != nil {
		return nil, err
	}

	if err := draft.UpdateLifecycle(ctx, newLifecycle); err != nil {
		return nil, err
	}

	switch newLifecycle {
	case porchapi.PackageRevisionLifecycleDraft:
		if kptFileChanged && newKptfileContent != "" && newKptfileContent != "{}\n" {
			if err := draft.UpdateKptfileContent(ctx, newKptfileContent); err != nil {
				return nil, fmt.Errorf("failed to write updated Kptfile for %q: %w", repoPr.KubeObjectName(), err)
			}
		}
		repoPr, err = cad.renderScheduler.ScheduleRender(ctx, repo, draft, false)
		if err != nil {
			return nil, fmt.Errorf("failed to schedule render for PackageRevision %s/%s: %w", newObj.GetNamespace(), newObj.GetName(), err)
		}
	case porchapi.PackageRevisionLifecyclePublished:
		if published, ok := draft.(repository.PackageRevision); ok {
			repoPr = published
		} else {
			repoPr, err = cad.closePackageRevisionDraft(ctx, repo, draft, version, kptFileChanged)
			if err != nil {
				return nil, err
			}
		}
	default:
		repoPr, err = cad.closePackageRevisionDraft(ctx, repo, draft, version, kptFileChanged)
		if err != nil {
			return nil, err
		}
	}

	return repoPr, nil
}

func (cad *cadEngine) closePackageRevisionDraft(
	ctx context.Context,
	repo repository.Repository,
	draft repository.PackageRevisionDraft,
	version int,
	saveResources bool,
) (repository.PackageRevision, error) {
	if saveResources {
		return repo.ClosePackageRevisionDraft(ctx, draft, version)
	}
	return repo.ClosePackageRevisionDraftNoResources(ctx, draft, version)
}

func (cad *cadEngine) rejectLifecycleChangeDuringRender(
	ctx context.Context,
	repoPr repository.PackageRevision,
	oldObj, newObj *porchapi.PackageRevision,
) error {
	if oldObj.Spec.Lifecycle == newObj.Spec.Lifecycle {
		return nil
	}

	switch newObj.Spec.Lifecycle {
	case porchapi.PackageRevisionLifecycleProposed, porchapi.PackageRevisionLifecyclePublished:
	default:
		return nil
	}

	kf, err := repoPr.GetKptfile(ctx)
	if err != nil {
		return err
	}

	renderFinished := true
	renderSuccess := true
	if kf.Status != nil {
		renderFinished = conditionIsTrue(scheduler.RenderFinishedConditionType, kf.Status.Conditions)
		renderSuccess = conditionIsTrue(scheduler.RenderedConditionType, kf.Status.Conditions)
	}

	if cad.renderScheduler != nil && (!renderFinished || cad.renderScheduler.RenderExecutionStatus(repoPr) != scheduler.RenderStatusUnknown) {
		return apierrors.NewConflict(
			porchapi.Resource("packagerevisions"),
			oldObj.GetName(),
			fmt.Errorf("package revision is not ready to be %s, because a Render operation is still in progress",
				newObj.Spec.Lifecycle))
	}
	if !renderSuccess {
		return fmt.Errorf("package revision is not ready to be %s, because its pipeline failed (see the %q condition for details)", newObj.Spec.Lifecycle, scheduler.RenderedConditionType)
	}

	if kf.Info != nil {
		var conditions []kptfilev1.Condition
		if kf.Status != nil {
			conditions = kf.Status.Conditions
		}
		for _, gate := range kf.Info.ReadinessGates {
			if !conditionIsTrue(gate.ConditionType, conditions) {
				return fmt.Errorf("package revision is not ready to be %s, because it fails its %q readiness gate", newObj.Spec.Lifecycle, gate.ConditionType)
			}
		}
	}
	return nil
}

func conditionIsTrue(conditionType string, conditions []kptfilev1.Condition) bool {
	for _, c := range conditions {
		if c.Type == conditionType {
			return c.Status == kptfilev1.ConditionTrue
		}
	}
	return false
}

func (cad *cadEngine) updatePackageResourcesAsync(
	ctx context.Context,
	repo repository.Repository,
	draft repository.PackageRevisionDraft,
	newRes *porchapi.PackageRevisionResources,
	prr *porchapi.PackageRevisionResources,
) (repository.PackageRevision, *kptfilev1.RenderStatus, error) {
	if err := draft.UpdateResources(ctx, prr, &porchapi.Task{Type: porchapi.TaskTypePush}); err != nil {
		return nil, nil, err
	}

	if !newRes.Spec.DisableRender {
		repoPkgRev, err := cad.scheduleRender(ctx, repo, draft, false)
		return repoPkgRev, nil, err
	}

	repoPkgRev, err := repo.ClosePackageRevisionDraft(ctx, draft, 0)
	return repoPkgRev, nil, err
}
