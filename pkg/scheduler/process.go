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

package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	kptfileapi "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	kpt "github.com/kptdev/kpt/pkg/lib/kptops"
	api "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	pctx "github.com/kptdev/porch/pkg/util/context"
	pkgerrors "github.com/pkg/errors"
	"go.uber.org/multierr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

// StartProcessing starts n background workers to process the render queue.
func (rs *RenderScheduler) StartProcessing(n int) error {
	klog.Infof("Starting %v background workers...", n)
	for i := range n {
		rs.wg.Add(1)
		go func(workerID int) {
			defer rs.wg.Done()
			rs.runRenderWorker(workerID)
		}(i)
	}
	return nil
}

// StopProcessing gracefully shuts down the scheduler.
func (rs *RenderScheduler) StopProcessing() {
	klog.Infof("Stopping RenderScheduler...")

	rs.mu.Lock()

	for uid, req := range rs.scheduled {
		klog.Infof("Cancelling scheduled render %q during shutdown.", uid)
		if req.cancel != nil {
			req.cancel()
		}
	}
	for uid, ongoing := range rs.ongoing {
		klog.Infof("Cancelling ongoing render %q during shutdown.", uid)
		if ongoing.cancel != nil {
			ongoing.cancel()
		}
	}
	rs.scheduled = make(map[repository.PackageRevisionKey]RenderExecutionRequest)
	rs.ongoing = make(map[repository.PackageRevisionKey]RenderExecutionRequest)
	rs.largeWaitlist = nil
	rs.largeRendersOngoing = 0
	rs.mu.Unlock()

	close(rs.workQueue)
	rs.wg.Wait()

	klog.Infof("RenderScheduler stopped.")
}

func (rs *RenderScheduler) Start(ctx context.Context) error {
	if err := rs.StartProcessing(rs.workerNum); err != nil {
		return err
	}
	<-ctx.Done()
	rs.StopProcessing()
	return nil
}

func (rs *RenderScheduler) NeedLeaderElection() bool {
	return true
}

func (rs *RenderScheduler) runRenderWorker(workerID int) {
	for request := range rs.workQueue {
		if request.pr == nil {
			klog.Errorf("Worker %d: received render request with nil PackageRevision, skipping", workerID)
			rs.releaseLargeRender(request)
			continue
		}

		ctx, prKey, proceed := rs.validateScheduledRender(&request, workerID)
		if !proceed {
			rs.releaseLargeRender(request)
			continue
		}

		rs.process(request, workerID)

		logRenderResult(workerID, prKey.K8SName(), ctx, request)
		rs.releaseOngoingRender(prKey, workerID, request)
	}
	klog.Infof("Worker %d shutting down - channel is closed", workerID)
}

func (rs *RenderScheduler) validateScheduledRender(request *RenderExecutionRequest, workerID int) (
	ctx context.Context,
	prKey repository.PackageRevisionKey,
	proceed bool,
) {
	prKey = request.pr.Key()
	prName := prKey.K8SName()
	ctx = request.ctx

	rs.mu.Lock()
	select {
	case <-ctx.Done():
		if scheduled, exists := rs.scheduled[prKey]; exists && scheduled.resourceVersion != request.resourceVersion {
			klog.Infof("Worker %d: render for %s (rv=%s) was cancelled before started, skipping because newer request exists (rv=%s)",
				workerID, prName, request.resourceVersion, scheduled.resourceVersion)
			rs.mu.Unlock()
			return ctx, prKey, false
		}
		klog.Warningf("Worker %d: render for %s was cancelled before started, but no newer request found - trying process to avoid lost render",
			workerID, prName)
		request.ctx, request.cancel = context.WithCancel(context.Background())
		request.ctx = pctx.WithPorchValuesFrom(ctx, request.ctx)
		ctx = request.ctx
	default:
	}

	if scheduled, exists := rs.scheduled[prKey]; exists {
		if scheduled.resourceVersion == request.resourceVersion {
			delete(rs.scheduled, prKey)
		} else {
			klog.Errorf("Worker %d: a new render has been scheduled for %s without cancelling the previous one", workerID, prName)
			rs.mu.Unlock()
			return ctx, prKey, false
		}
	}

	request.renderStartTime = time.Now()
	request.workerID = workerID
	rs.ongoing[prKey] = *request
	rs.mu.Unlock()
	return ctx, prKey, true
}

func logRenderResult(workerID int, prName string, ctx context.Context, request RenderExecutionRequest) {
	renderEndTime := time.Now()
	queueDuration := request.renderStartTime.Sub(request.scheduledTime)
	renderDuration := renderEndTime.Sub(request.renderStartTime)
	totalDuration := renderEndTime.Sub(request.scheduledTime)

	select {
	case <-ctx.Done():
		klog.Infof("Worker %d: render for %q was cancelled (queued: %v, render: %v, total: %v)",
			workerID, prName, queueDuration, renderDuration, totalDuration)
	default:
		klog.Infof("Worker %d: render for %q completed (queued: %v, render: %v, total: %v)",
			workerID, prName, queueDuration, renderDuration, totalDuration)
	}
}

func (rs *RenderScheduler) releaseOngoingRender(prKey repository.PackageRevisionKey, workerID int, request RenderExecutionRequest) {
	rs.mu.Lock()
	if ongoing, exists := rs.ongoing[prKey]; exists {
		if ongoing.workerID == workerID {
			delete(rs.ongoing, prKey)
		}
	}
	rs.mu.Unlock()
	rs.releaseLargeRender(request)
}

func (rs *RenderScheduler) process(request RenderExecutionRequest, workerID int) {
	ctx := request.ctx

	prKey, prName, ok := validateRequest(request, workerID)
	if !ok {
		return
	}

	klog.Infof("Worker %d rendering PackageRevision %s", workerID, prName)

	prr, ok := loadPRR(ctx, request, workerID, prName)
	if !ok {
		return
	}

	originalHash := computeResourceHash(prr.Spec.Resources)
	renderedResources, renderStatus := rs.doRender(ctx, prr.Spec.Resources, prKey)

	if rs.HasParallelRender(prKey, workerID) {
		klog.Infof("Worker %d: Skipping updating PackageRevisionResources %q, as parallel render was detected.", workerID, prName)
		// Conditions are in correct state and RenderFinished will be set to true by the parallel render - safe to return here
		return
	}

	applyRenderedResourcesToPRR(&prr, prr.Spec.Resources, renderedResources, renderStatus, workerID, prName)

	if err := rs.tryToUpdatePrrReallyHard(ctx, &prr, originalHash, prKey, workerID); err != nil {
		rs.handlePRRUpdateFailure(ctx, request, workerID, prKey, &prr, originalHash, renderedResources, renderStatus, err)
	}
}

func validateRequest(request RenderExecutionRequest, workerID int) (repository.PackageRevisionKey, string, bool) {
	if request.pr == nil {
		klog.Errorf("Worker %d: received render request with nil PackageRevision! retryNum=%d. This indicates a bug in rescheduleRender or ScheduleRender.",
			workerID, request.retryNum)
		return repository.PackageRevisionKey{}, "", false
	}

	prKey := request.pr.Key()
	prName := prKey.K8SName()
	if prName == "" {
		klog.Errorf("Worker %d: received render request with empty package revision key! retryNum=%d. This indicates a bug in rescheduleRender or ScheduleRender.",
			workerID, request.retryNum)
		return prKey, "", false
	}

	return prKey, prName, true
}

func loadPRR(
	ctx context.Context,
	request RenderExecutionRequest,
	workerID int,
	prName string,
) (api.PackageRevisionResources, bool) {
	prr, err := request.pr.GetResources(ctx)
	if err != nil {
		if repository.IsNotFoundError(err) {
			klog.Infof("Worker %d: PackageRevision %q was deleted before render started, skipping", workerID, prName)
			return api.PackageRevisionResources{}, false
		}
		klog.Errorf("Worker %d: failed to fetch resources for %q before render: %v", workerID, prName, err)
		return api.PackageRevisionResources{}, false
	}
	return *prr, true
}

func applyRenderedResourcesToPRR(
	prr *api.PackageRevisionResources,
	inputResources map[string]string,
	renderedResources map[string]string,
	renderStatus kptfileapi.RenderStatus,
	workerID int,
	prName string,
) {
	if len(inputResources) > 0 && !hasKptfile(renderedResources) {
		klog.Errorf("Worker %d: Cannot update PackageRevisionResources %q with empty/incomplete render results (got %d files, no Kptfile)", workerID, prName, len(renderedResources))
	}

	if err := setFinalRenderConditions(renderedResources, &renderStatus); err != nil {
		klog.Errorf("Worker %d: Failed to add render status conditions to Kptfile for %q, reason: %v .", workerID, prName, err)
		// Not stopping on error, but trying to write at least the render results into the PkgRev
	}

	prr.Spec.Resources = renderedResources
	prr.Status.RenderStatus = renderStatus
	prr.Spec.DisableRender = true
}

func (rs *RenderScheduler) handlePRRUpdateFailure(
	ctx context.Context,
	request RenderExecutionRequest,
	workerID int,
	prKey repository.PackageRevisionKey,
	prr *api.PackageRevisionResources,
	originalHash uint32,
	renderedResources map[string]string,
	renderStatus kptfileapi.RenderStatus,
	updateErr error,
) {
	prName := prKey.K8SName()

	if rs.HasParallelRender(prKey, workerID) {
		klog.Infof("Worker %d: Updating Package Revision Resources with render results failed for %q, but parallel render was detected. Skipping update. Error was: %v", workerID, prName, updateErr)
		return
	}
	if errors.IsNotFound(updateErr) {
		klog.Infof("Worker %d: PackageRevision %q was deleted during render, skipping status condition update: %v", workerID, prName, updateErr)
		return
	}
	if request.retryNum < maxRenderRetry {
		klog.Infof("Worker %d: Updating Package Revision Resources with render results failed for %q, will retry render: %v", workerID, prName, updateErr)
		if err := rs.rescheduleRender(ctx, request, workerID); err == nil {
			return
		}
	}
	klog.Errorf("!!THIS SHOULDN'T HAPPEN!! Exceeded max number of render retries (%d) Worker %d: Updating Package Revision Resources with render results failed for %q !!: %v", maxRenderRetry, workerID, prName, updateErr)
	errMsg := fmt.Sprintf("Failed to update PackageRevisionResources with render results after %d retries: %v", maxRenderRetry, updateErr)
	if renderStatus.ErrorSummary == "" {
		renderStatus.ErrorSummary = errMsg
	} else {
		renderStatus.ErrorSummary = fmt.Sprintf("%s\n%s", renderStatus.ErrorSummary, updateErr)
	}
	if err := setFinalRenderConditions(renderedResources, &renderStatus); err != nil {
		klog.Errorf("Worker %d: Failed to add render status conditions to Kptfile for %q, reason: %v .", workerID, prName, err)
	}
	if err := rs.tryToUpdatePrrReallyHard(context.Background(), prr, originalHash, prKey, workerID); err != nil {
		klog.Errorf("Tried really hard to update PackageRevisionResources %q with final render error status, but failed: %v", prName, err)
	}
}

func (rs *RenderScheduler) tryToUpdatePrrReallyHard(
	ctx context.Context,
	prr *api.PackageRevisionResources,
	originalHash uint32,
	prKey repository.PackageRevisionKey,
	workerId int,
) error {
	retriable := func(e error) bool {
		if e == nil {
			return false
		}
		if pkgerrors.Is(e, context.Canceled) || pkgerrors.Is(e, context.DeadlineExceeded) || ctx.Err() != nil {
			return false
		}
		if rs.HasParallelRender(prKey, workerId) {
			return false
		}
		if errors.IsConflict(e) && strings.Contains(e.Error(), "the object has been modified") {
			// Re-fetch the latest PRR to determine the cause of the conflict. If
			// the resources are identical to what this render started with, only
			// metadata changed - safe to update the RV and retry. If the resources
			// differ, a real content update happened and the render results must not
			// overwrite it, so the caller can reschedule a fresh render.
			var latestPrr api.PackageRevisionResources
			if fetchErr := rs.kubeClient.Get(ctx, client.ObjectKey{
				Namespace: prr.Namespace,
				Name:      prr.Name,
			}, &latestPrr); fetchErr == nil && computeResourceHash(latestPrr.Spec.Resources) == originalHash {
				prr.ResourceVersion = latestPrr.ResourceVersion
				return true
			}
			return false
		}
		if errors.IsNotFound(e) {
			if strings.Contains(e.Error(), "exceeded") && strings.Contains(e.Error(), "trying to list package revisions") {
				return true
			}
			return false
		}
		return true
	}

	timing := wait.Backoff{
		Duration: 100 * time.Millisecond,
		Factor:   1.1,
		Steps:    600,
		Cap:      2 * time.Second,
	}

	if err := retry.OnError(timing, retriable, func() error {
		return rs.kubeClient.Update(ctx, prr)
	}); err != nil {
		if ctx.Err() != nil {
			klog.Warningf("Package revision resources %q update context canceled, trying to update one last time without context", prr.Name)
			if err2 := rs.kubeClient.Update(context.Background(), prr); err2 != nil {
				klog.Warningf("Completely failed to update package revision resources %q, even ignoring the context: %v", prr.Name, err2)
			} else {
				klog.Warningf("Package revision resources %q updated after context has been canceled", prr.Name)
			}
		}
		return err
	}

	return nil
}

func (rs *RenderScheduler) doRender(
	ctx context.Context,
	inputResources map[string]string,
	prKey repository.PackageRevisionKey,
) (
	map[string]string,
	kptfileapi.RenderStatus,
) {
	tempDir, cleanup, err := createTempDir(rs.renderTmpDir)
	if err != nil {
		return inputResources, kptfileapi.RenderStatus{
			ErrorSummary: fmt.Sprintf("couldn't create render temp directory: %v", err),
		}
	}
	defer cleanup()

	fs := filesys.MakeFsOnDisk()
	pkgPath, err := writeInputResourcesToDisk(fs, inputResources, tempDir)
	if err != nil {
		return inputResources, kptfileapi.RenderStatus{ErrorSummary: err.Error()}
	}

	multiErr := rs.runKptRender(ctx, fs, pkgPath, prKey)

	renderedResources, readErr := readRenderedResources(fs, tempDir, inputResources, prKey)
	multiErr = multierr.Append(multiErr, readErr)

	return rs.renderStatusFromRenderedKptfile(inputResources, renderedResources.Contents, prKey, multiErr)
}

func writeInputResourcesToDisk(fs filesys.FileSystem, inputResources map[string]string, tempDir string) (pkgPath string, err error) {
	resources := repository.PackageResources{Contents: inputResources}
	pkgPath, writeErr := WriteResources(fs, resources, tempDir)
	if writeErr != nil {
		return "", fmt.Errorf("couldn't write resources into render files: %v", writeErr)
	}
	if pkgPath == "" {
		return "", fmt.Errorf("skipping render as no package was found")
	}
	return pkgPath, nil
}

func (rs *RenderScheduler) runKptRender(ctx context.Context, fs filesys.FileSystem, pkgPath string, prKey repository.PackageRevisionKey) error {
	kptRenderer := kpt.NewRenderer(rs.runnerOptionsFor(prKey))
	// TODO: instead of _, once kpt returns the same status that gets stored in the Kptfile, use that
	_, err := kptRenderer.Render(ctx, fs, fn.RenderOptions{
		PkgPath: pkgPath,
		Runtime: rs.runtime,
	})
	return err
}

func readRenderedResources(
	fs filesys.FileSystem,
	tempDir string,
	inputResources map[string]string,
	prKey repository.PackageRevisionKey,
) (repository.PackageResources, error) {
	renderedResources, readErr := ReadResources(fs, tempDir)
	if readErr != nil {
		renderedResources.Contents = inputResources
		return renderedResources, fmt.Errorf("failed to read rendered resources from fs: %w", readErr)
	}

	if !hasKptfile(renderedResources.Contents) {
		renderedResources.Contents = inputResources
		return renderedResources, fmt.Errorf(
			"render produced no Kptfile for %s (got %d files); preserving input resources",
			prKey.K8SName(), len(renderedResources.Contents))
	}
	return renderedResources, nil
}

func (rs *RenderScheduler) renderStatusFromRenderedKptfile(
	inputResources, renderedContents map[string]string,
	prKey repository.PackageRevisionKey,
	multiErr error,
) (map[string]string, kptfileapi.RenderStatus) {
	kptfileContents := []byte(renderedContents[kptfileapi.KptFileName])
	kptfile := &kptfileapi.KptFile{}

	if err := yaml.Unmarshal(kptfileContents, kptfile); err != nil {
		klog.Warningf("Failed to unmarshal Kptfile contents of package %q (returning empty renderStatus): %v", prKey.K8SName(), err)
		return renderedContents, kptfileapi.RenderStatus{}
	}

	renderStatus := kptfile.Status.RenderStatus // intentionally not deepcopy
	if renderStatus == nil {
		return rs.missingRenderStatusResult(prKey, inputResources, renderedContents, multiErr)
	}

	util.ExtendErrorSummary(renderStatus, multiErr)

	kptfileContents, err := yaml.Marshal(kptfile)

	if err != nil {
		klog.Warningf("Failed to marshal extended error summary back into Kptfile contents for package %q: %v", prKey.K8SName(), err)
	} else {
		renderedContents[kptfileapi.KptFileName] = string(kptfileContents)
	}

	return renderedContents, *renderStatus
}

// missingRenderStatusResult handles a render whose Kptfile carries no render status.
// kpt always writes a status after a completed run that executed functions, so a missing
// status together with an error means kpt aborted midway (e.g. failed to write results
// back). The FS then holds a mix of old and new files, so the input is preserved and the
// error is reported instead of being published as a successful render.
func (rs *RenderScheduler) missingRenderStatusResult(
	prKey repository.PackageRevisionKey,
	inputResources, renderedContents map[string]string,
	renderErr error,
) (map[string]string, kptfileapi.RenderStatus) {
	if renderErr == nil {
		klog.Warningf("Kptfile of package %q has empty render status", prKey.K8SName())
		return renderedContents, kptfileapi.RenderStatus{}
	}
	klog.Errorf("Render of package %q failed without writing a render status, preserving input resources: %v",
		prKey.K8SName(), renderErr)
	return inputResources, kptfileapi.RenderStatus{ErrorSummary: renderErr.Error()}
}
