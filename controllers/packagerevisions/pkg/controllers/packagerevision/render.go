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
	iofs "io/fs"
	"strings"

	fnresult "github.com/kptdev/kpt/api/fnresult/v1"
	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/kptfile/kptfileutil"
	"github.com/kptdev/kpt/pkg/lib/kptops"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

// renderResult holds the output of a render operation.
type renderResult struct {
	resources map[string]string
	results   *fnresult.ResultList // per-function execution results; nil if no pipeline ran
	err       error                // pipeline failure; nil on success
}

// renderer renders a package's KRM function pipeline.
// If nil on the reconciler, rendering is skipped.
// The returned error is an infrastructure failure (cannot read/write FS).
// A pipeline failure is reported via renderResult.err.
type renderer interface {
	Render(ctx context.Context, resources map[string]string) (*renderResult, error)
}

// kptRenderer implements renderer using kpt's render library.
type kptRenderer struct {
	runtime       fn.FunctionRuntime
	runnerOptions runneroptions.RunnerOptions
}

// newKptRenderer creates a renderer backed by a kpt FunctionRuntime.
func newKptRenderer(runtime fn.FunctionRuntime, opts runneroptions.RunnerOptions) renderer {
	return &kptRenderer{runtime: runtime, runnerOptions: opts}
}

func (r *kptRenderer) Render(ctx context.Context, resources map[string]string) (*renderResult, error) {
	fs := filesys.MakeFsInMemory()
	pkgPath, err := repository.WriteResourcesToFS(fs, "", resources)
	if err != nil {
		return nil, fmt.Errorf("failed to write resources for render: %w", err)
	}

	if pkgPath == "" {
		return &renderResult{resources: resources}, nil
	}

	renderer := kptops.NewRenderer(r.runnerOptions)
	resultList, renderErr := renderer.Render(ctx, fs, fn.RenderOptions{
		PkgPath: pkgPath,
		Runtime: r.runtime,
	})

	// Read back from FS — honours save-on-render-failure on failure,
	// picks up rendered resources + kpt status conditions on success.
	fsResources, readErr := readResourcesFromFS(fs)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read resources after render: %w", readErr)
	}

	return &renderResult{
		resources: fsResources,
		results:   resultList,
		err:       renderErr,
	}, nil
}

func readResourcesFromFS(fs filesys.FileSystem) (map[string]string, error) {
	contents := map[string]string{}
	if err := fs.Walk("/", func(p string, info iofs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			data, err := fs.ReadFile(p)
			if err != nil {
				return err
			}
			contents[strings.TrimPrefix(p, "/")] = string(data)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return contents, nil
}

// renderTrigger determines if rendering is needed.
// Render only applies to Draft packages
// Returns the annotation value (requested), and which trigger fired.
func renderTrigger(pr *porchv1alpha2.PackageRevision) (requested string, annotation, source bool) {
	if pr.Spec.Lifecycle != porchv1alpha2.PackageRevisionLifecycleDraft {
		return "", false, false
	}
	requested = pr.Annotations[porchv1alpha2.AnnotationRenderRequest]
	annotation = requested != "" && requested != pr.Status.ObservedPrrResourceVersion
	source = pr.Status.CreationSource != "" && !isRenderedTrue(pr)
	return
}

// isRenderStale returns true if the annotation changed during render.
func isRenderStale(currentAnnotation, rendered string) bool {
	return currentAnnotation != rendered
}

// observedVersionAfterRender returns the value to set as observedPrrResourceVersion.
// Uses the annotation value if available, so the annotation trigger won't re-fire.
func observedVersionAfterRender(requested string, annotations map[string]string) string {
	if requested != "" {
		return requested
	}
	return annotations[porchv1alpha2.AnnotationRenderRequest]
}

// kptfileFromResources parses the Kptfile from an in-memory resource map.
// Returns a zero-value KptFile if no Kptfile is present.
func kptfileFromResources(resources map[string]string) (kptfilev1.KptFile, error) {
	kfString, ok := resources[kptfilev1.KptFileName]
	if !ok || kfString == "" {
		return kptfilev1.KptFile{}, nil
	}
	kf, err := kptfileutil.DecodeKptfile(strings.NewReader(kfString))
	if err != nil {
		return kptfilev1.KptFile{}, fmt.Errorf("decode Kptfile: %w", err)
	}
	return *kf, nil
}

func isRenderedTrue(pr *porchv1alpha2.PackageRevision) bool {
	for _, c := range pr.Status.Conditions {
		if c.Type == porchv1alpha2.ConditionRendered {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

// isPushOnRenderFailure returns true if the PR opts into persisting resources on render failure.
func isPushOnRenderFailure(pr *porchv1alpha2.PackageRevision) bool {
	return pr.Annotations[porchv1alpha2.PushOnFnRenderFailureKey] == porchv1alpha2.PushOnFnRenderFailureValue
}

// executeRender performs the render, handles failure/stale, and writes results.
func (r *PackageRevisionReconciler) executeRender(ctx context.Context, pr *porchv1alpha2.PackageRevision, repoKey repository.RepositoryKey) (*ctrl.Result, error) {
	log := log.FromContext(ctx)

	resources, err := r.readPackageResources(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName)
	if err != nil {
		r.setRenderFailed(ctx, pr, err)
		return nil, err
	}
	log.V(1).Info("read package resources", "count", len(resources))

	result, requeueResult, err := r.renderWithConcurrencyLimit(ctx, resources)
	if requeueResult != nil {
		log.Info("render concurrency limit reached, requeuing")
		return requeueResult, nil
	}
	if err != nil {
		r.setRenderFailed(ctx, pr, err)
		return nil, fmt.Errorf("render failed: %w", err)
	}

	requested := pr.Annotations[porchv1alpha2.AnnotationRenderRequest]
	if stale, err := r.checkRenderStale(ctx, pr, requested); err != nil {
		return nil, err
	} else if stale != nil {
		return stale, nil
	}

	if result.err != nil {
		log.Error(result.err, "render pipeline failed", "fnResults", result.results)
		if isPushOnRenderFailure(pr) {
			log.Info("persisting partial resources (push-on-render-failure)")
			r.persistAndSyncKptfile(ctx, pr, repoKey, result.resources)
		}
		r.setRenderFailed(ctx, pr, result.err)
		return nil, fmt.Errorf("render pipeline failed: %w", result.err)
	}

	if err := r.writeRenderedResources(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName, result.resources); err != nil {
		r.setRenderFailed(ctx, pr, err)
		return nil, err
	}
	log.V(1).Info("rendered resources written")
	r.syncKptfileFields(ctx, pr, result.resources, repoKey)
	log.Info("render complete")
	return nil, nil
}

func (r *PackageRevisionReconciler) renderWithConcurrencyLimit(ctx context.Context, resources map[string]string) (*renderResult, *ctrl.Result, error) {
	if r.renderLimiter != nil {
		select {
		case r.renderLimiter <- struct{}{}:
			defer func() { <-r.renderLimiter }()
		default:
			return nil, &ctrl.Result{RequeueAfter: r.RenderRequeueDelay}, nil
		}
	}
	result, err := r.Renderer.Render(ctx, resources)
	return result, nil, err
}

func (r *PackageRevisionReconciler) checkRenderStale(ctx context.Context, pr *porchv1alpha2.PackageRevision, rendered string) (*ctrl.Result, error) {
	var current porchv1alpha2.PackageRevision
	if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(pr), &current); err != nil {
		return nil, fmt.Errorf("re-read PR after render: %w", err)
	}
	if isRenderStale(current.Annotations[porchv1alpha2.AnnotationRenderRequest], rendered) {
		log.FromContext(ctx).Info("render stale, requeuing", "rendered", rendered,
			"current", current.Annotations[porchv1alpha2.AnnotationRenderRequest])
		return &ctrl.Result{Requeue: true}, nil
	}
	return nil, nil
}

func (r *PackageRevisionReconciler) persistAndSyncKptfile(ctx context.Context, pr *porchv1alpha2.PackageRevision, repoKey repository.RepositoryKey, resources map[string]string) {
	if err := r.writeRenderedResources(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName, resources); err != nil {
		log.FromContext(ctx).Error(err, "failed to write resources")
		return
	}
	r.syncKptfileFields(ctx, pr, resources, repoKey)
}

func (r *PackageRevisionReconciler) syncKptfileFields(ctx context.Context, pr *porchv1alpha2.PackageRevision, renderedResources map[string]string, repoKey repository.RepositoryKey) {
	log := log.FromContext(ctx)

	// Parse Kptfile from rendered resources
	kf, err := kptfileFromResources(renderedResources)
	if err != nil {
		log.Error(err, "failed to parse Kptfile for CRD sync")
		return
	}

	// All packages must have a Kptfile (enforced by git layer during package creation).
	if kf.Kind == "" {
		log.Error(nil, "Kptfile missing from rendered resources")
		return
	}

	r.updateKptfileFields(ctx, pr, kf)
}

func (r *PackageRevisionReconciler) readPackageResources(ctx context.Context, repoKey repository.RepositoryKey, pkg, workspace string) (map[string]string, error) {
	content, err := r.ContentCache.GetPackageContent(ctx, repoKey, pkg, workspace)
	if err != nil {
		return nil, fmt.Errorf("get content for render: %w", err)
	}
	resources, err := content.GetResourceContents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read resources for render: %w", err)
	}
	return resources, nil
}

func (r *PackageRevisionReconciler) writeRenderedResources(ctx context.Context, repoKey repository.RepositoryKey, pkg, workspace string, resources map[string]string) error {
	draft, err := r.ContentCache.CreateDraftFromExisting(ctx, repoKey, pkg, workspace)
	if err != nil {
		return fmt.Errorf("create draft for render: %w", err)
	}
	if err := draft.UpdateResources(ctx, resources, "rendered"); err != nil {
		return fmt.Errorf("write rendered resources: %w", err)
	}
	if err := r.ContentCache.CloseDraft(ctx, repoKey, draft, 0); err != nil {
		return fmt.Errorf("close draft after render: %w", err)
	}
	return nil
}

// renderRequestChanged fires when the render-request annotation changes.
func renderRequestChanged() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldVal := e.ObjectOld.GetAnnotations()[porchv1alpha2.AnnotationRenderRequest]
			newVal := e.ObjectNew.GetAnnotations()[porchv1alpha2.AnnotationRenderRequest]
			return oldVal != newVal
		},
	}
}

// validateRenderStateBeforePublish verifies render is complete before publishing.
// Second layer of defense against rendering races; webhook provides first layer.
func (r *PackageRevisionReconciler) validateRenderStateBeforePublish(ctx context.Context, pr *porchv1alpha2.PackageRevision) error {
	// No render operation in progress
	if pr.Status.RenderingPrrResourceVersion != "" {
		return fmt.Errorf("render in progress")
	}

	// Render result matches current content
	observed := pr.Status.ObservedPrrResourceVersion
	requested := ""
	if pr.Annotations != nil {
		requested = pr.Annotations[porchv1alpha2.AnnotationRenderRequest]
	}

	if observed != requested {
		return fmt.Errorf("render not yet observed (observed=%q, requested=%q)", observed, requested)
	}

	return nil
}
