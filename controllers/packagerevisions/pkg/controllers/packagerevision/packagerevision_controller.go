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
	"fmt"
	"path"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/krm-functions-sdk/go/fn/kptfileko"
	porchapi "github.com/kptdev/porch/api/porch"
	"github.com/kptdev/porch/api/porch/key"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/functionconfigs"
	"github.com/kptdev/porch/internal/telemetry"
	"github.com/kptdev/porch/pkg/repository"
	pkgerrors "github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

const reconcilerName = "packagerevisions"

// reconciledRetryInterval: requeue interval for transient failures (content cache, status writes, label backfills).
const reconciledRetryInterval = 100 * time.Millisecond

// PackageRevisionReconciler reconciles v1alpha2 PackageRevision CRDs, driving
// lifecycle transitions via the shared cache.
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
	// TransientErrorRetryDeadline bounds how long a transient failure (content cache,
	// lifecycle, render read) may keep requeuing before escalating to a terminal
	// Failed condition. Measured from when the PackageRevision first went Pending.
	TransientErrorRetryDeadline time.Duration
	renderLimiter               chan struct{} // bounds concurrent fn-runner calls
	apiReader                   client.Reader // bypasses informer cache for direct etcd reads
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

	// Backfill the latest-revision label if missing — self-healing (adds default
	// "false" only; never overwrites the "true" winner).
	if err := r.ensureLatestRevisionLabel(ctx, &pr); err != nil {
		return ctrl.Result{}, err
	}

	desiredLifecycle := pr.Spec.Lifecycle
	if desiredLifecycle == "" {
		return ctrl.Result{}, nil
	}

	repoKey := key.RepositoryKey{
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

	// Re-read to pick up spec changes that occurred while render was in-flight.
	if err := r.Get(ctx, req.NamespacedName, &pr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileLifecycle(ctx, &pr, repoKey)
}

// reconcileFinalizer ensures the finalizer/ownerReference and handles deletion
// gating. Returns (nil, nil) when reconciliation should continue.
func (r *PackageRevisionReconciler) reconcileFinalizer(ctx context.Context, pr *porchv1alpha2.PackageRevision) (*ctrl.Result, error) {
	if !pr.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, pr)
	}
	return nil, r.ensureFinalizerAndOwner(ctx, pr)
}

func (r *PackageRevisionReconciler) reconcileLifecycle(ctx context.Context, pr *porchv1alpha2.PackageRevision, repoKey key.RepositoryKey) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	desiredLifecycle := pr.Spec.Lifecycle

	content, err := r.ContentCache.GetPackageContent(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName)
	if err != nil {
		// Non-transient errors (e.g. a malformed package key) are terminal: fail
		// immediately rather than requeuing Pending for the whole deadline.
		if !repository.IsTransientError(err) {
			log.Error(err, "failed to get package content", "name", pr.Name)
			r.updateStatus(ctx, pr, nil, "", "", readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonFailed, err.Error()))
			return ctrl.Result{}, nil
		}
		// Transient (e.g. cache not yet populated after a restart): requeue to
		// self-heal, but escalate to terminal Failed once the transient-retry
		// streak passes the deadline.
		cond := readyCondition(pr.Generation, metav1.ConditionFalse, transientRetryReasonInternal, err.Error())
		pendingSince := pendingSince(pr)
		elapsedPending := time.Since(pendingSince)
		if pendingExpired(pr, r.TransientErrorRetryDeadline) {
			log.Error(err, "failed to get package content; giving up after deadline",
				"deadline", r.TransientErrorRetryDeadline, "elapsed", elapsedPending, "name", pr.Name)
			cond.Reason = porchv1alpha2.ReasonFailed
			r.updateStatus(ctx, pr, nil, "", "", cond)
			return ctrl.Result{}, nil
		}
		// Preserve the first-Pending time so the deadline measures elapsed failure time.
		if !pendingSince.IsZero() {
			cond.LastTransitionTime = metav1.NewTime(pendingSince)
		}
		remainingDeadline := r.TransientErrorRetryDeadline - elapsedPending
		log.Error(err, "failed to get package content; requeuing",
			"name", pr.Name, "elapsedPending", elapsedPending, "remainingDeadline", remainingDeadline,
			"retryAfter", reconciledRetryInterval)
		r.updateStatus(ctx, pr, nil, "", "", cond)
		return ctrl.Result{RequeueAfter: reconciledRetryInterval}, nil
	}

	currentLifecycle := porchv1alpha2.PackageRevisionLifecycle(content.Lifecycle(ctx))
	if currentLifecycle == desiredLifecycle {
		r.updateStatus(ctx, pr, content, "", "", readyCondition(pr.Generation, metav1.ConditionTrue, porchv1alpha2.ReasonReady, ""))
		// The main placeholder (Revision -1) must not drive latest labeling or the
		// main CR — only numbered published revisions do.
		if porchv1alpha2.LifecycleIsPublished(desiredLifecycle) && pr.Status.Revision != -1 {
			// Requeue so a stale/stranded latest-revision label converges.
			latestName, err := r.updateLatestRevisionLabels(ctx, pr)
			if err != nil {
				log.V(2).Info("latest-revision update deferred; requeuing", "name", pr.Name, "reason", err.Error())
				return ctrl.Result{RequeueAfter: reconciledRetryInterval}, nil
			}
			// Only the latest published revision refreshes the main CR. Every
			// numbered revision reaches this branch when its lifecycle already
			// matches, so refreshing from a non-latest one (e.g. v1 after v2) would
			// overwrite main's HEAD-tracking status with stale metadata.
			if pr.Name == latestName {
				if err := r.createMainPackageRevision(ctx, pr, repoKey); err != nil {
					log.Error(err, "failed to create/update main PackageRevision", "name", pr.Name)
					return ctrl.Result{Requeue: true}, nil
				}
			}
		}
		return ctrl.Result{}, nil
	}

	// Render race guard: prevent publishing/proposing while render is incomplete
	if desiredLifecycle == porchv1alpha2.PackageRevisionLifecyclePublished ||
		desiredLifecycle == porchv1alpha2.PackageRevisionLifecycleProposed {
		if err := r.validateRenderStateBeforePublish(pr); err != nil {
			log.Info("lifecycle transition blocked by render guard", "name", pr.Name, "reason", err.Error())
			r.updateStatus(ctx, pr, content, "", "", readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonPending, err.Error()))
			return ctrl.Result{Requeue: true}, nil
		}
	}

	log.Info("lifecycle transition", "name", pr.Name, "current", currentLifecycle, "desired", desiredLifecycle)

	start := time.Now()
	updated, err := r.ContentCache.UpdateLifecycle(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName, string(desiredLifecycle))
	telemetry.RecordControllerOperation(telemetry.ResourcePackageRevision, "UPDATE", start)
	if err != nil {
		// Transient git/cache failures (e.g. connection reset) stay Pending and
		// requeue until they recover or the deadline is hit; genuine failures are
		// terminal and stop requeuing.
		if repository.IsTransientError(err) && !pendingExpired(pr, r.TransientErrorRetryDeadline) {
			cond := readyCondition(pr.Generation, metav1.ConditionFalse, transientRetryReasonInternal, err.Error())
			if t := pendingSince(pr); !t.IsZero() {
				cond.LastTransitionTime = metav1.NewTime(t)
			}
			log.Error(err, "lifecycle transition failed; requeuing", "name", pr.Name, "retryAfter", reconciledRetryInterval)
			r.updateStatus(ctx, pr, nil, "", "", cond)
			return ctrl.Result{RequeueAfter: reconciledRetryInterval}, nil
		}
		log.Error(err, "lifecycle transition failed", "name", pr.Name)
		r.updateStatus(ctx, pr, nil, "", "", readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonFailed, err.Error()))
		return ctrl.Result{}, nil
	}

	r.updateStatus(ctx, pr, updated, "", "", readyCondition(pr.Generation, metav1.ConditionTrue, porchv1alpha2.ReasonReady, ""))

	if porchv1alpha2.LifecycleIsPublished(desiredLifecycle) {
		// Create the main CR on publish (non-fatal; the requeue retries).
		if err := r.createMainPackageRevision(ctx, pr, repoKey); err != nil {
			log.Error(err, "failed to create/update main PackageRevision", "name", pr.Name)
		}
		// Requeue so the cache indexes the new status.revision before latest labeling.
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
func (r *PackageRevisionReconciler) reconcileSource(ctx context.Context, pr *porchv1alpha2.PackageRevision, repoKey key.RepositoryKey) (*ctrl.Result, error) {
	// Skip if already created or no source to apply (same conditions as applySource).
	if pr.Status.CreationSource != "" || pr.Spec.Source == nil {
		return nil, nil
	}

	// Reject source on non-migrated repos before any work; backstops the webhook.
	if err := r.verifyRepoMigrated(ctx, repoKey); err != nil {
		return nil, r.setFailedConditionsAndLog(ctx, pr, "", err)
	}

	resources, sourceOperationType, err := r.applySource(ctx, pr)
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

// reconcileSubpackageOperation handles a one-time subpackage operation from spec.
func (r *PackageRevisionReconciler) reconcileSubpackageOperation(ctx context.Context, pr *porchv1alpha2.PackageRevision, repoKey key.RepositoryKey) (*ctrl.Result, error) {
	subpackageResources, subpackageOperationType, err := r.applySubpackageOperation(ctx, pr)
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
				pkgerrors.Wrapf(err, "failed to write package name %q to subpackage Kptfile", path.Base(pr.Spec.SubpackageOperation.SubpackageDir)))
	}

	if err := kptFile.WriteToPackage(subpackageResources); err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to write to subpackage Kptfile %q", path.Join(pr.Spec.SubpackageOperation.SubpackageDir, kptfilev1.KptFileName))
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

	return r.finalizeDraftAndUpdateStatus(ctx, pr, repoKey, draft, parentResources, "", r.getSubpackageOperationHash(pr))
}

// verifyRepoMigrated errors if the target repo lacks the v1alpha2-migration annotation.
func (r *PackageRevisionReconciler) verifyRepoMigrated(ctx context.Context, repoKey key.RepositoryKey) error {
	var repo configapi.Repository
	if err := r.Get(ctx, types.NamespacedName{Namespace: repoKey.Namespace, Name: repoKey.Name}, &repo); err != nil {
		return fmt.Errorf("get repository %s/%s: %w", repoKey.Namespace, repoKey.Name, err)
	}
	if repo.Annotations[configapi.AnnotationKeyV1Alpha2Migration] != configapi.AnnotationValueMigrationEnabled {
		return fmt.Errorf("repository %s/%s not enabled for v1alpha2", repoKey.Namespace, repoKey.Name)
	}
	return nil
}

// finalizeDraftAndUpdateStatus updates resources, closes the draft, and persists
// status. If the completion marker can't be confirmed, it requeues (bounded by
// transientErrorRetryDeadline) rather than replaying the already-committed git
// mutation with stale completion state, escalating to a terminal failure only once
// the deadline elapses.
func (r *PackageRevisionReconciler) finalizeDraftAndUpdateStatus(
	ctx context.Context,
	pr *porchv1alpha2.PackageRevision,
	repoKey key.RepositoryKey,
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
		log.Error(err, "failed to read back package content after source execution", "name", pr.Name)
	}

	// Persist completion markers (CreationSource / LastSubpackageOperationHash) with
	// retry. These are the idempotency guards that prevent the committed git mutation
	// from being replayed on the next reconcile. A transient patch failure must not
	// cause a requeue with stale state.
	if err := r.updateStatusWithRetry(ctx, pr, content, creationSource, subpackageOperationHash,
		readyCondition(pr.Generation, metav1.ConditionFalse, porchv1alpha2.ReasonPending, "awaiting render")); err != nil {
		// The git mutation already committed but the idempotency marker
		// (CreationSource / hash) couldn't be confirmed within the in-reconcile
		// readback window. Under load the SSA patch often DID land and only the
		// cached read lagged, so this is a transient condition — requeue to
		// re-confirm (reconcileSource/reconcileSubpackageOperation is idempotent and
		// skips once the marker is visible) rather than failing a good operation.
		// Only escalate to a terminal failure once the Pending streak exceeds
		// transientErrorRetryDeadline, matching the content-lookup/lifecycle paths.
		if pendingExpired(pr, r.TransientErrorRetryDeadline) {
			log.Error(err, "completion-status confirmation still failing past deadline; marking failed",
				"name", pr.Name, "operation", operationType, "deadline", r.TransientErrorRetryDeadline)
			return nil, r.setFailedConditionsAndLog(ctx, pr, operationType,
				pkgerrors.Wrap(err, "completion status not confirmed"))
		}
		// Preserve the first transient-retry time so the deadline measures elapsed failure time.
		cond := readyCondition(pr.Generation, metav1.ConditionFalse, transientRetryReasonInternal,
			"awaiting completion-status confirmation")
		if t := pendingSince(pr); !t.IsZero() {
			cond.LastTransitionTime = metav1.NewTime(t)
		}
		r.updateStatus(ctx, pr, nil, creationSource, subpackageOperationHash, cond)
		log.V(4).Info("completion-status confirmation deferred; requeuing",
			"name", pr.Name, "operation", operationType, "retryAfter", reconciledRetryInterval)
		return &ctrl.Result{RequeueAfter: reconciledRetryInterval}, nil
	}
	// Best-effort Rendered=Unknown marker: a dropped marker doesn't block recovery
	// since the source trigger still fires and the requeue below retries render.
	if err := r.updateRenderStatus(ctx, pr, "", "",
		renderedCondition(pr.Generation, metav1.ConditionUnknown, porchv1alpha2.ReasonPending, "awaiting render")); err != nil {
		log.Error(err, "failed to set Rendered=Unknown marker after draft close; render will be retried via requeue", "name", pr.Name)
	}
	telemetry.RecordControllerOperation(telemetry.ResourcePackageRevision, "CREATE", time.Now())

	// RequeueAfter (not a bare Requeue) so render is reliably retried: with
	// observedGeneration == generation no watch event will wake a dropped requeue,
	// which would leave the draft stuck "awaiting render". The follow-up is idempotent.
	result := ctrl.Result{RequeueAfter: reconciledRetryInterval}
	// Requeue to run render. Absence of "render started" log indicates stuck draft.
	log.V(3).Info("source operation completed, draft closed; awaiting render",
		"name", pr.Name, "operation", operationType, "requeueAfter", reconciledRetryInterval)
	return &result, nil
}

// reconcileRender renders when the render-request annotation changed or a source or
// subpackage operation ran but Rendered != True. Returns (nil, nil) if no render is needed.
func (r *PackageRevisionReconciler) reconcileRender(ctx context.Context, pr *porchv1alpha2.PackageRevision, repoKey key.RepositoryKey) (*ctrl.Result, error) {
	if r.Renderer == nil {
		return nil, nil
	}

	requested, annotationTrigger, sourceTrigger := renderTrigger(pr)
	if !annotationTrigger && !sourceTrigger {
		r.refreshRenderedGeneration(ctx, pr)
		return nil, nil
	}

	log := log.FromContext(ctx)
	// "render started" pairs with "awaiting render": a draft that logs the latter but
	// never the former had its requeue dropped — the stuck-draft signature.
	log.V(3).Info("render started", "name", pr.Name,
		"annotationTrigger", annotationTrigger, "sourceTrigger", sourceTrigger, "requested", requested)
	// Best-effort "rendering" marker; non-fatal since render writes the terminal condition.
	_ = r.updateRenderStatus(ctx, pr, requested, "",
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
		// Verification reads through the content cache, so a transient
		// propagation/backend failure after a successful write must be retried via
		// the bounded helper, not treated as a terminal render failure.
		if result := r.requeueOrFailRender(ctx, pr, err, "verify resources after render"); result != nil {
			return result, nil
		}
		log.Error(err, "resources not available after render - render verification failed", "name", pr.Name)
		r.setRenderFailed(ctx, pr, err)
		return nil, err
	}

	if err := r.updateRenderStatus(ctx, pr, "", observed,
		renderedCondition(pr.Generation, metav1.ConditionTrue, porchv1alpha2.ReasonRendered, ""),
	); err != nil {
		// Render succeeded but the terminal Rendered=True failed to persist; requeue
		// to retry, else it stays stuck at Rendered=Unknown with no watch event.
		log.Error(err, "failed to persist Rendered=True condition after render completion; requeuing to retry",
			"name", pr.Name, "retryAfter", reconciledRetryInterval)
		return &ctrl.Result{RequeueAfter: reconciledRetryInterval}, nil
	}
	log.V(3).Info("render completed successfully", "name", pr.Name)
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
