// Copyright 2026 The kpt and Nephio Authors
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

package packagerevision

import (
	"context"
	"fmt"
	"time"

	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	"github.com/nephio-project/porch/controllers/functionconfigs/reconciler"
	"github.com/nephio-project/porch/pkg/repository"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

//go:generate go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0 rbac:headerFile=../../../../../scripts/boilerplate.yaml.txt,roleName=porch-controllers-packagerevisions,year=$YEAR_GEN webhook paths="." output:rbac:artifacts:config=../../../config/rbac

//+kubebuilder:rbac:groups=porch.kpt.dev,resources=packagerevisions,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=porch.kpt.dev,resources=packagerevisions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=porch.kpt.dev,resources=packagerevisions/finalizers,verbs=update
//+kubebuilder:rbac:groups=config.porch.kpt.dev,resources=repositories,verbs=get
//+kubebuilder:rbac:groups=config.porch.kpt.dev,resources=functionconfigs,verbs=get;list;watch;patch
//+kubebuilder:rbac:groups=config.porch.kpt.dev,resources=functionconfigs/status,verbs=get;update;patch

const reconcilerName = "packagerevisions"

// PackageRevisionReconciler reconciles v1alpha2 PackageRevision CRDs.
// It handles lifecycle transitions (draft/proposed/published) by executing
// git operations via the shared cache.
type PackageRevisionReconciler struct {
	client.Client
	Scheme                 *runtime.Scheme
	ContentCache           repository.ContentCache
	ExternalPackageFetcher repository.ExternalPackageFetcher
	FunctionConfigStore    *reconciler.FunctionConfigStore
	Renderer               renderer // nil = skip rendering

	MaxConcurrentReconciles    int
	MaxConcurrentRenders       int
	RenderRequeueDelay         time.Duration
	RepoOperationRetryAttempts int
	MaxGRPCMessageSize         int
	renderLimiter              chan struct{} // bounds concurrent fn-runner calls
	apiReader                  client.Reader // bypasses informer cache for direct etcd reads
}

func (r *PackageRevisionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pr porchv1alpha2.PackageRevision
	if err := r.Get(ctx, req.NamespacedName, &pr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if result, err := r.reconcileFinalizer(ctx, &pr); err != nil || result != nil {
		return resultOrDefault(result), err
	}

	desired := string(pr.Spec.Lifecycle)
	if desired == "" {
		return ctrl.Result{}, nil
	}

	repoKey := repository.RepositoryKey{
		Namespace: pr.Namespace,
		Name:      pr.Spec.RepositoryName,
	}

	// TODO: Errors from sub-reconciles are swallowed (returned as nil to controller-runtime),
	// so the work queue doesn't apply exponential backoff on persistent failures.
	// Consider returning errors to enable backoff, but note the side effects:
	// double logging, error metrics, and inconsistency with reconcileLifecycle.
	if result, err := r.reconcileSource(ctx, &pr, repoKey); err != nil || result != nil {
		return resultOrDefault(result), nil
	}

	if result, err := r.reconcileRender(ctx, &pr, repoKey); err != nil || result != nil {
		return resultOrDefault(result), nil
	}

	// Re-read to pick up spec changes (e.g. lifecycle transitions) that
	// occurred while render was in-flight. A validating webhook should
	// eventually block lifecycle transitions during render, making this
	// re-read unnecessary.
	if err := r.Get(ctx, req.NamespacedName, &pr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileLifecycle(ctx, &pr, repoKey)
}

// reconcileFinalizer ensures the finalizer and ownerReference are present,
// and handles deletion gating.
// Published packages require DeletionProposed before deletion, unless the
// owner Repository has been deleted (GC cascade).
// Returns (nil, nil) when reconciliation should continue.
func (r *PackageRevisionReconciler) reconcileFinalizer(ctx context.Context, pr *porchv1alpha2.PackageRevision) (*ctrl.Result, error) {
	if !pr.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, pr)
	}
	return nil, r.ensureFinalizerAndOwner(ctx, pr)
}

func (r *PackageRevisionReconciler) reconcileLifecycle(ctx context.Context, pr *porchv1alpha2.PackageRevision, repoKey repository.RepositoryKey) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	desired := string(pr.Spec.Lifecycle)

	content, err := r.ContentCache.GetPackageContent(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName)
	if err != nil {
		log.Error(err, "failed to get package content")
		r.updateStatus(ctx, pr, nil, "", readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonFailed, err.Error()))
		return ctrl.Result{}, nil
	}

	current := content.Lifecycle(ctx)
	if current == desired {
		r.updateStatus(ctx, pr, content, "", readyCondition(pr.Generation, metav1.ConditionTrue, porchv1alpha2.ReasonReady, ""))
		if porchv1alpha2.LifecycleIsPublished(porchv1alpha2.PackageRevisionLifecycle(desired)) {
			r.updateLatestRevisionLabels(ctx, pr)
		}
		return ctrl.Result{}, nil
	}

	log.Info("lifecycle transition", "name", pr.Name, "current", current, "desired", desired)

	updated, err := r.ContentCache.UpdateLifecycle(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName, desired)
	if err != nil {
		log.Error(err, "lifecycle transition failed")
		r.updateStatus(ctx, pr, nil, "", readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonFailed, err.Error()))
		return ctrl.Result{}, nil
	}

	r.updateStatus(ctx, pr, updated, "", readyCondition(pr.Generation, metav1.ConditionTrue, porchv1alpha2.ReasonReady, ""))

	if porchv1alpha2.LifecycleIsPublished(porchv1alpha2.PackageRevisionLifecycle(desired)) {
		// Requeue so the informer cache indexes the new status.revision
		// before updateLatestRevisionLabels runs its List query.
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

func resultOrDefault(result *ctrl.Result) ctrl.Result {
	if result != nil {
		return *result
	}
	return ctrl.Result{}
}

// reconcileSource handles one-time package creation from spec.source.
// Returns (nil, nil) if no source needs to be applied.
// Returns (result, nil) if source was applied and status was updated.
// Returns (nil, err) on failure.
func (r *PackageRevisionReconciler) reconcileSource(ctx context.Context, pr *porchv1alpha2.PackageRevision, repoKey repository.RepositoryKey) (*ctrl.Result, error) {
	resources, creationSource, err := r.applySource(ctx, pr)
	if err != nil {
		return nil, r.setSourceFailed(ctx, pr, err)
	}
	if resources == nil {
		return nil, nil
	}

	log := log.FromContext(ctx)
	log.Info("applying source", "type", creationSource, "name", pr.Name)

	// TODO: CreateNewDraft always receives lifecycle=Draft — consider removing the lifecycle parameter from the interface.
	draft, err := r.ContentCache.CreateNewDraft(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName, string(porchv1alpha2.PackageRevisionLifecycleDraft))
	if err != nil {
		return nil, r.setSourceFailed(ctx, pr, fmt.Errorf("create draft: %w", err))
	}

	if err := draft.UpdateResources(ctx, resources, creationSource); err != nil {
		return nil, r.setSourceFailed(ctx, pr, fmt.Errorf("update resources: %w", err))
	}

	if err := r.ContentCache.CloseDraft(ctx, repoKey, draft, 0); err != nil {
		return nil, r.setSourceFailed(ctx, pr, fmt.Errorf("close draft: %w", err))
	}

	// Read back the created package to get lock info for status.
	content, err := r.ContentCache.GetPackageContent(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName)
	if err != nil {
		log.Error(err, "failed to read back package content after source execution")
	}

	r.updateStatus(ctx, pr, content, creationSource,
		readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonPending, "awaiting render"),
	)
	// Set Rendered=Unknown via the render field manager.
	r.updateRenderStatus(ctx, pr, "", "",
		renderedCondition(pr.Generation, metav1.ConditionUnknown, porchv1alpha2.ReasonPending, "awaiting render"),
	)
	r.ensureLatestRevisionLabel(ctx, pr)

	result := ctrl.Result{Requeue: true}
	return &result, nil
}

// reconcileRender checks if rendering is needed and renders if so.
// Two triggers:
//   - Annotation porch.kpt.dev/render-request differs from status.observedPrrResourceVersion (push path)
//   - Source was executed but Rendered != True (source execution path)
//
// Returns (nil, nil) if no render is needed.
// TODO: Consider centralising all ctrl.Result creation in packagerevision_controller.go
// so requeue decisions are visible in one place. Sub-functions would return signals
// and the controller translates them into ctrl.Result.
func (r *PackageRevisionReconciler) reconcileRender(ctx context.Context, pr *porchv1alpha2.PackageRevision, repoKey repository.RepositoryKey) (*ctrl.Result, error) {
	if r.Renderer == nil {
		return nil, nil
	}

	requested, annotationTrigger, sourceTrigger := renderTrigger(pr)
	if !annotationTrigger && !sourceTrigger {
		return nil, nil
	}

	log := log.FromContext(ctx)
	log.Info("render requested", "requested", requested)
	r.updateRenderStatus(ctx, pr, requested, "",
		renderedCondition(pr.Generation, metav1.ConditionUnknown, porchv1alpha2.ReasonPending, "rendering"),
	)

	result, err := r.executeRender(ctx, pr, repoKey)
	if err != nil {
		return nil, err
	}
	if result != nil {
		return result, nil
	}

	observed := observedVersionAfterRender(requested, pr.Annotations)
	r.updateRenderStatus(ctx, pr, "", observed,
		renderedCondition(pr.Generation, metav1.ConditionTrue, porchv1alpha2.ReasonRendered, ""),
	)
	return nil, nil
}

func (r *PackageRevisionReconciler) Name() string { return reconcilerName }

func (r *PackageRevisionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	log := ctrl.Log.WithName(r.Name())

	r.Client = mgr.GetClient()
	r.apiReader = mgr.GetAPIReader()

	if r.MaxConcurrentRenders > 0 {
		r.renderLimiter = make(chan struct{}, r.MaxConcurrentRenders)
	}

	if err := setupFieldIndexes(mgr); err != nil {
		return fmt.Errorf("failed to setup field indexes: %w", err)
	}

	err := ctrl.NewControllerManagedBy(mgr).
		For(&porchv1alpha2.PackageRevision{}).
		WithEventFilter(predicate.Or(
			predicate.GenerationChangedPredicate{},
			renderRequestChanged(),
		)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.MaxConcurrentReconciles,
		}).
		Named("packagerevision").
		Complete(r)

	if err == nil {
		log.V(1).Info("PackageRevision controller successfully registered")
	}
	return err
}
