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

package packagerevision

import (
	"context"
	"path"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/krm-functions-sdk/go/fn/kptfileko"
	porchapi "github.com/kptdev/porch/api/porch"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/controllers/functionconfigs"
	"github.com/kptdev/porch/internal/telemetry"
	"github.com/kptdev/porch/pkg/repository"
	pkgerrors "github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

//go:generate go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.22.0 rbac:headerFile=../../../../../scripts/boilerplate.yaml.txt,roleName=porch-controllers-packagerevisions,year=$YEAR_GEN webhook:headerFile=../../../../../scripts/boilerplate.yaml.txt,year=$YEAR_GEN paths="." output:rbac:artifacts:config=../../../config/rbac output:webhook:artifacts:config=../../../config/webhook

//+kubebuilder:webhookconfiguration:mutating=false,name=packagerevision-validating-webhook-configuration
//+kubebuilder:webhook:path=/validate-porch-kpt-dev-v1alpha2-packagerevision,mutating=false,failurePolicy=fail,groups=porch.kpt.dev,resources=packagerevisions,verbs=create;update;delete,versions=v1alpha2,name=packagerevision-validator.porch.kpt.dev,admissionReviewVersions=v1,sideEffects=None,serviceName=porch-controllers,serviceNamespace=porch-system,servicePort=9443,timeoutSeconds=30
//+kubebuilder:rbac:groups=porch.kpt.dev,resources=packagerevisions,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=porch.kpt.dev,resources=packagerevisions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=porch.kpt.dev,resources=packagerevisions/finalizers,verbs=update
//+kubebuilder:rbac:groups=config.porch.kpt.dev,resources=repositories,verbs=get
//+kubebuilder:rbac:groups=config.porch.kpt.dev,resources=functionconfigs,verbs=get;list;watch;patch
//+kubebuilder:rbac:groups=config.porch.kpt.dev,resources=functionconfigs/status,verbs=get;update;patch

const (
	reconcilerName   = "packagerevisions"
	prTelemetryName  = telemetry.ResourcePackageRevision
	prrTelemetryName = telemetry.ResourcePackageRevisionResources
	praTelemetryName = telemetry.ResourcePackageRevisionApproval
)

// PackageRevisionReconciler reconciles v1alpha2 PackageRevision CRDs.
// It handles lifecycle transitions (draft/proposed/published) by executing
// git operations via the shared cache.
type PackageRevisionReconciler struct {
	client.Client
	Scheme                 *runtime.Scheme
	ContentCache           repository.ContentCache
	ExternalPackageFetcher repository.ExternalPackageFetcher
	FunctionConfigStore    *functionconfigs.FunctionConfigStore
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

	// Ensure the repository label is always correct — self-healing invariant.
	if err := r.ensureRepositoryLabel(ctx, &pr); err != nil {
		return ctrl.Result{}, err
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

	if result, err := r.reconcilePackageMetadata(ctx, &pr, repoKey); err != nil || result != nil {
		return resultOrDefault(result), nil
	}

	if result, err := r.reconcileSubpackageOperation(ctx, &pr, repoKey); err != nil || result != nil {
		return resultOrDefault(result), nil
	}

	if result, err := r.reconcileRender(ctx, &pr, repoKey); err != nil || result != nil {
		return resultOrDefault(result), nil
	}

	// Re-read to pick up spec changes (e.g. lifecycle transitions) that
	// occurred while render was in-flight. The validating webhook and controller
	// guard now block lifecycle transitions during render, but re-reading ensures
	// we have fresh state before the guard validates render completion.
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
	desiredLifecycle := pr.Spec.Lifecycle

	getStart := time.Now()
	getOp := telemetry.Operations.Get
	telemetryName := prTelemetryName
	key, _ := repository.PkgRevK8sName2Key(pr.Namespace, pr.Name)
	recordInFlightOperationEnd := telemetry.TrackInFlightControllerOperation(ctx, telemetryName, getOp.AllCaps, getOp.TitleCase+telemetryName, pr.Spec.Lifecycle, &key)

	content, err := r.ContentCache.GetPackageContent(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName)
	if err != nil {
		log.Error(err, "failed to get package content")
		r.updateStatus(ctx, pr, nil, "", "", readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonFailed, err.Error()))
		return ctrl.Result{}, nil
	}

	currentLifecycle := porchv1alpha2.PackageRevisionLifecycle(content.Lifecycle(ctx))

	recordInFlightOperationEnd()
	telemetry.RecordControllerOperation(ctx, telemetryName, getOp.AllCaps, getOp.TitleCase+telemetryName, time.Since(getStart), err, pr.Spec.Lifecycle, &key)

	if currentLifecycle == desiredLifecycle {
		r.updateStatus(ctx, pr, content, "", "", readyCondition(pr.Generation, metav1.ConditionTrue, porchv1alpha2.ReasonReady, ""))
		if porchv1alpha2.LifecycleIsPublished(desiredLifecycle) {
			r.updateLatestRevisionLabels(ctx, pr)
		}
		return ctrl.Result{}, nil
	}

	// Render race guard: prevent publishing/proposing while render is incomplete
	if desiredLifecycle == porchv1alpha2.PackageRevisionLifecyclePublished ||
		desiredLifecycle == porchv1alpha2.PackageRevisionLifecycleProposed {
		if err := r.validateRenderStateBeforePublish(ctx, pr); err != nil {
			log.Info("lifecycle transition blocked by render guard", "reason", err.Error())
			r.updateStatus(ctx, pr, content, "", "", readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonPending, err.Error()))
			return ctrl.Result{Requeue: true}, nil
		}
	}

	log.Info("lifecycle transition", "name", pr.Name, "current", currentLifecycle, "desired", desiredLifecycle)

	updateStart := time.Now()
	updateOp := telemetry.Operations.Update
	telemetryName = func() string {
		if porchv1alpha2.LifecycleIsPublished(pr.Spec.Lifecycle) {
			// we're updating PackageRevisionApproval, conceptually if not strictly API-wise
			return praTelemetryName
		}
		return prTelemetryName
	}()
	defer telemetry.TrackInFlightControllerOperation(ctx, telemetryName, updateOp.AllCaps, updateOp.TitleCase+telemetryName, pr.Spec.Lifecycle, &key)()
	defer func() {
		lifecycle := func() porchv1alpha2.PackageRevisionLifecycle {
			if err == nil {
				return desiredLifecycle
			}
			return currentLifecycle
		}()
		telemetry.RecordControllerOperation(ctx, telemetryName, updateOp.AllCaps, updateOp.TitleCase+telemetryName, time.Since(updateStart), err, lifecycle, &key)
	}()

	updated, err := r.ContentCache.UpdateLifecycle(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName, string(desiredLifecycle))

	if err != nil {
		log.Error(err, "lifecycle transition failed")
		r.updateStatus(ctx, pr, nil, "", "", readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonFailed, err.Error()))
		return ctrl.Result{Requeue: true}, nil
	}

	r.updateStatus(ctx, pr, updated, "", "", readyCondition(pr.Generation, metav1.ConditionTrue, porchv1alpha2.ReasonReady, ""))

	if porchv1alpha2.LifecycleIsPublished(porchv1alpha2.PackageRevisionLifecycle(desiredLifecycle)) {
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
	op := telemetry.Operations.Create
	key, _ := repository.PkgRevK8sName2Key(pr.Namespace, pr.Name)
	start := time.Now()

	var err error
	sourceOperationType, action := r.selectPackageSourceAction(pr)
	desiredLifecycle := porchv1alpha2.PackageRevisionLifecycleDraft
	if sourceOperationType != "no-op" && action != nil {
		defer telemetry.TrackInFlightControllerOperation(ctx, prTelemetryName, op.AllCaps, telemetry.ParseOperation(sourceOperationType).TitleCase+prTelemetryName, desiredLifecycle, &key)()
		defer func() {
			lifecycle := func() porchv1alpha2.PackageRevisionLifecycle {
				if err != nil {
					return ""
				}
				return pr.Spec.Lifecycle
			}()
			telemetry.RecordControllerOperation(ctx, prTelemetryName, op.AllCaps, telemetry.ParseOperation(sourceOperationType).TitleCase+prTelemetryName, time.Since(start), err, lifecycle, &key)
		}()
	}

	resources, err := r.applySource(ctx, pr)
	if err != nil {
		return nil, r.setFailedConditionsAndLog(ctx, pr, sourceOperationType, err)
	}
	if resources == nil {
		return nil, nil
	}

	log := log.FromContext(ctx)
	log.Info("applying operation", "type", sourceOperationType, "name", pr.Name)

	// TODO: CreateNewDraft always receives lifecycle=Draft — consider removing the lifecycle parameter from the interface.
	draft, err := r.ContentCache.CreateNewDraft(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName, string(porchv1alpha2.PackageRevisionLifecycleDraft))
	if err != nil {
		return nil, r.setFailedConditionsAndLog(ctx, pr, sourceOperationType, pkgerrors.Wrapf(err, "create draft"))
	}

	return r.finalizeDraftAndUpdateStatus(ctx, pr, repoKey, draft, resources, sourceOperationType, "")
}

// reconcileSubpackageOperation handles one-time package creation from spec.source.
// Returns (nil, nil) if no source needs to be applied.
// Returns (result, nil) if source was applied and status was updated.
// Returns (nil, err) on failure.
func (r *PackageRevisionReconciler) reconcileSubpackageOperation(ctx context.Context, pr *porchv1alpha2.PackageRevision, repoKey repository.RepositoryKey) (*ctrl.Result, error) {
	op := telemetry.Operations.Update
	key, _ := repository.PkgRevK8sName2Key(pr.Namespace, pr.Name)
	start := time.Now()

	var (
		err     error
		saveErr = func(loseableErr error) error { err = loseableErr; return err }
	)
	subpackageOperationType, operation, err := r.selectSubpackageOperation(pr)
	desiredLifecycle := porchv1alpha2.PackageRevisionLifecycleDraft
	if subpackageOperationType != "no-op" && operation != nil {
		defer telemetry.TrackInFlightControllerOperation(ctx, prTelemetryName, op.AllCaps, telemetry.ParseOperation(subpackageOperationType).TitleCase+prTelemetryName, desiredLifecycle, &key)()
		defer func() {
			lifecycleAfter := pr.Spec.Lifecycle
			telemetry.RecordControllerOperation(ctx, prTelemetryName, op.AllCaps, telemetry.ParseOperation(subpackageOperationType).TitleCase+prTelemetryName, time.Since(start), err, lifecycleAfter, &key)
		}()
	}

	subpackageResources, err := r.applySubpackageOperation(ctx, pr)
	if err != nil {
		return nil, r.setFailedConditionsAndLog(ctx, pr, subpackageOperationType, err)
	}
	if subpackageResources == nil {
		return nil, nil
	}

	kptFile, err := kptfileko.NewFromPackage(subpackageResources)
	if err != nil {
		return nil, r.setFailedConditionsAndLog(ctx, pr, subpackageOperationType, pkgerrors.Wrap(err, "failed to parse subpackage Kptfile"))
	}

	subpackageName, err := porchapi.ComposeSubpkgObjName(pr.Spec.SubpackageOperation.SubpackageDir)
	if err != nil {
		return nil, r.setFailedConditionsAndLog(ctx, pr, subpackageOperationType, pkgerrors.Wrap(err, "failed to compose subpackage name for subpackage"))
	}

	if err := kptFile.SetName(subpackageName); err != nil {
		return nil,
			r.setFailedConditionsAndLog(ctx, pr, subpackageOperationType,
				pkgerrors.Wrapf(saveErr(err), "failed to write package name %q to subpackage Kptfile", path.Base(pr.Spec.SubpackageOperation.SubpackageDir)))
	}

	if err := kptFile.WriteToPackage(subpackageResources); err != nil {
		return nil, pkgerrors.Wrapf(saveErr(err), "failed to write to subpackage Kptfile %q", path.Join(pr.Spec.SubpackageOperation.SubpackageDir, kptfilev1.KptFileName))
	}

	log := log.FromContext(ctx)
	log.Info("applying operation", "type", subpackageOperationType, "name", pr.Name)

	parentResources, err := r.getPackageResources(ctx, pr)
	if err != nil {
		return nil, r.setFailedConditionsAndLog(ctx, pr, subpackageOperationType, pkgerrors.Wrapf(err, "failed to read parent resources"))
	}

	parentResources, err = r.upsertSubpackageResourcesInDraftResources(ctx, pr, parentResources, subpackageResources)
	if err != nil {
		return nil, r.setFailedConditionsAndLog(ctx, pr, subpackageOperationType, pkgerrors.Wrapf(err, "failed to upsert subpackage resources into parent resources"))
	}

	draft, err := r.ContentCache.CreateDraftFromExisting(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName)
	if err != nil {
		return nil, r.setFailedConditionsAndLog(ctx, pr, subpackageOperationType, pkgerrors.Wrapf(err, "create draft on existing package revision"))
	}

	// having separate lines for assign & return ensures err is available to the deferred telemetry call above
	result, err := r.finalizeDraftAndUpdateStatus(ctx, pr, repoKey, draft, parentResources, "", r.getSubpackageOperationHash(pr))
	return result, err
}

// finalizeDraftAndUpdateStatus completes the draft operation by updating resources,
// closing the draft, and updating the package revision status.
// The status patch (which records CreationSource / LastSubpackageOperationHash) is
// retried: if it fails the function returns an error so the work-queue retries the
// whole reconcile rather than requeueing with stale completion state, which would
// cause a replay of the already-committed git mutation.
func (r *PackageRevisionReconciler) finalizeDraftAndUpdateStatus(
	ctx context.Context,
	pr *porchv1alpha2.PackageRevision,
	repoKey repository.RepositoryKey,
	draft repository.PackageRevisionDraftSlim,
	resources map[string]string,
	creationSource string,
	subpackageOperationHash string) (*ctrl.Result, error) {
	log := log.FromContext(ctx)

	operationType := creationSource
	if operationType == "" {
		operationType = subpackageOperationHash
	}
	if err := draft.UpdateResources(ctx, resources, operationType); err != nil {
		return nil, r.setFailedConditionsAndLog(ctx, pr, operationType, pkgerrors.Wrapf(err, "update resources"))
	}

	if err := r.ContentCache.CloseDraft(ctx, repoKey, draft, 0); err != nil {
		return nil, r.setFailedConditionsAndLog(ctx, pr, operationType, pkgerrors.Wrapf(err, "close draft"))
	}

	// Read back the created package to get lock info for status.
	content, err := r.ContentCache.GetPackageContent(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName)
	if err != nil {
		log.Error(err, "failed to read back package content after source execution")
	}

	// Persist completion markers (CreationSource / LastSubpackageOperationHash) with
	// retry. These are the idempotency guards that prevent the committed git mutation
	// from being replayed on the next reconcile. A transient patch failure must not
	// cause a requeue with stale state.
	if err := r.updateStatusWithRetry(ctx, pr, content, creationSource, subpackageOperationHash,
		readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonPending, "awaiting render")); err != nil {
		return nil, pkgerrors.Wrap(err, "failed to persist completion status after draft close")
	}
	// Set Rendered=Unknown via the render field manager.
	r.updateRenderStatus(ctx, pr, "", "",
		renderedCondition(pr.Generation, metav1.ConditionUnknown, porchv1alpha2.ReasonPending, "awaiting render"))
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
		r.refreshRenderedGeneration(ctx, pr)
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

	_, err = r.verifyResourcesAvailable(ctx, repoKey, pr)
	if err != nil {
		log.Error(err, "resources not available after render - render verification failed")
		r.setRenderFailed(ctx, pr, err)
		return nil, err
	}

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
		return pkgerrors.Wrapf(err, "failed to setup field indexes")
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
