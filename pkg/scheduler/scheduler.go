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
	"sync"
	"time"

	kptfileapi "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	kptfilesdk "github.com/kptdev/krm-functions-sdk/go/fn/kptfileko"
	api "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/metrics"
	"github.com/kptdev/porch/pkg/repository"
	pctx "github.com/kptdev/porch/pkg/util/context"
	pkgerrors "github.com/pkg/errors"
	"go.uber.org/multierr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type RenderExecutionStatus = api.RenderExecutionStatus

const (
	RenderStatusUnknown    = api.RenderStatusUnknown
	RenderStatusScheduled  = api.RenderStatusScheduled
	RenderStatusOngoing    = api.RenderStatusOngoing
	RenderStatusSuccessful = api.RenderStatusSuccessful
	RenderStatusFailed     = api.RenderStatusFailed

	RenderedConditionType       = api.RenderedConditionType
	RenderFinishedConditionType = api.RenderFinishedConditionType

	maxRenderRetry                    = 5
	defaultWorkQueueSize              = 100
	defaultWorkerNum                  = 25
	defaultLargePackageBytes    int64 = 50 * 1024 * 1024
	defaultMaxConcurrentRenders       = 1
)

type RenderExecutionRequest struct {
	pr              repository.PackageRevision
	resourceVersion string // RV at scheduling time; used for stale-request detection
	ctx             context.Context
	cancel          context.CancelFunc
	retryNum        int
	scheduledTime   time.Time // when the render was scheduled
	renderStartTime time.Time // when the render actually started processing
	sizeBytes       int64     // package resources size; used to detect large renders
	workerID        int
}

// RenderScheduler manages asynchronous render operations for package revisions.
type RenderScheduler struct {
	workQueue chan RenderExecutionRequest
	scheduled map[repository.PackageRevisionKey]RenderExecutionRequest
	ongoing   map[repository.PackageRevisionKey]RenderExecutionRequest
	mu        sync.Mutex

	largePackageBytes         int64
	largeWaitlist             []RenderExecutionRequest
	largeRendersOngoing       int
	maxConcurrentLargeRenders int
	renderTmpDir              string

	wg            sync.WaitGroup
	workerNum     int
	runtime       fn.FunctionRuntime
	kubeClient    client.Client
	runnerOptions runneroptions.RunnerOptions
}

var _ manager.Runnable = (*RenderScheduler)(nil)
var _ manager.LeaderElectionRunnable = (*RenderScheduler)(nil)

type RenderSchedulerOptions struct {
	WorkQueueSize             int
	WorkerNum                 int
	LargePackageThreshold     int64
	MaxConcurrentLargeRenders int
	RenderTmpDir              string
}

type RenderSchedulerOption func(*RenderSchedulerOptions)

func NewRenderScheduler(opts ...RenderSchedulerOption) *RenderScheduler {
	options := RenderSchedulerOptions{
		WorkQueueSize:             defaultWorkQueueSize,
		WorkerNum:                 defaultWorkerNum,
		LargePackageThreshold:     defaultLargePackageBytes,
		MaxConcurrentLargeRenders: defaultMaxConcurrentRenders,
	}

	for _, opt := range opts {
		opt(&options)
	}
	klog.Infof("Using RenderScheduler queue size: %v, workers: %d, large-package threshold: %d bytes, max concurrent large "+
		"renders: %d", options.WorkQueueSize, options.WorkerNum, options.LargePackageThreshold, options.MaxConcurrentLargeRenders)

	return &RenderScheduler{
		workQueue:                 make(chan RenderExecutionRequest, options.WorkQueueSize),
		scheduled:                 make(map[repository.PackageRevisionKey]RenderExecutionRequest),
		ongoing:                   make(map[repository.PackageRevisionKey]RenderExecutionRequest),
		largePackageBytes:         options.LargePackageThreshold,
		maxConcurrentLargeRenders: options.MaxConcurrentLargeRenders,
		renderTmpDir:              options.RenderTmpDir,
		workerNum:                 options.WorkerNum,
	}
}

func (rs *RenderScheduler) ScheduleRender(
	ctx context.Context,
	repo repository.Repository,
	draft repository.PackageRevisionDraft,
	skipRender bool,
) (repository.PackageRevision, error) {
	prKey := draft.Key()
	prName := prKey.K8SName()
	k8sUserName := pctx.GetK8sUserName(ctx)

	klog.Infof("Scheduling render for %q", prName)

	if pctx.GetPackageRevision(ctx) == pctx.EmptyPRName {
		klog.Warningf("PackageRevision name is not correctly set in the context when scheduling render")
		ctx = pctx.WithPackageRevision(ctx, prName)
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()

	rs.cancelRenderDueToNewRequest(prKey, prName, k8sUserName)

	kptfileContent, err := draft.GetKptfileContent(ctx)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "could not load Kptfile for %q", prName)
	}

	renderNeeded := rs.NeedRender(kptfileContent) && !skipRender
	pr, err := prepareKptfileRenderConditions(ctx, repo, draft, prName, kptfileContent, renderNeeded)
	if err != nil {
		return nil, err
	}
	if !renderNeeded {
		return pr, nil
	}

	var finalErr error
	renderReq := RenderExecutionRequest{
		pr:              pr,
		resourceVersion: pr.ResourceVersion(),
		scheduledTime:   time.Now(),
		sizeBytes:       packageRevisionSizeBytes(ctx, pr),
	}

	reqCtx, reqCancel := context.WithCancel(context.Background())
	reqCtx = pctx.WithPorchValuesFrom(ctx, reqCtx)
	renderReq.ctx, renderReq.cancel = reqCtx, wrapCancelFunc(reqCancel, prName)

	rs.scheduled[prKey] = renderReq

	if !rs.queueRender(renderReq) {
		delete(rs.scheduled, prKey)
		finalErr = fmt.Errorf("work queue is full, cannot schedule render for %s", prName)
	} else {
		klog.Infof("Render successfully scheduled for PackageRevision %q (size=%d bytes)", prName, renderReq.sizeBytes)
		return pr, nil
	}

	klog.Warningf("Render scheduling failed for %q, trying to write error into the render status conditions: %s", prName, finalErr.Error())

	return writeFinalError(ctx, repo, pr, prName, kptfileContent, finalErr)
}

func (rs *RenderScheduler) rescheduleRender(ctx context.Context, prevRequest RenderExecutionRequest, workerID int) error {
	if prevRequest.pr == nil {
		return fmt.Errorf("cannot reschedule render with nil PackageRevision")
	}
	prKey := prevRequest.pr.Key()
	prName := prKey.K8SName()
	retriable := func(e error) bool {
		if pkgerrors.Is(e, context.Canceled) || pkgerrors.Is(e, context.DeadlineExceeded) || ctx.Err() != nil {
			return false
		}
		if rs.HasParallelRender(prKey, workerID) {
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

	var prr api.PackageRevisionResources
	err := retry.OnError(timing, retriable, func() error {
		return rs.kubeClient.Get(ctx, client.ObjectKey{Namespace: prKey.K8SNS(), Name: prKey.K8SName()}, &prr)
	})
	if err != nil {
		if rs.HasParallelRender(prKey, workerID) {
			klog.Infof("Worker %d: Rescheduling render failed for %q, but parallel render was detected. Skipping update. Error was: %v", workerID, prName, err)
			return nil
		}
		if errors.IsNotFound(err) {
			klog.Infof("Worker %d: PackageRevision %q was deleted during rescheduling: %v", workerID, prName, err)
			return nil
		}

		if ctx.Err() != nil {
			klog.Infof("Worker %d: Rescheduling render was cancelled for PackageRevision %q: %v", workerID, prName, err)
			return nil
		}
		return err
	}

	request := RenderExecutionRequest{
		pr:              prevRequest.pr,
		resourceVersion: prr.ResourceVersion,
		retryNum:        prevRequest.retryNum + 1,
		scheduledTime:   time.Now(),
		sizeBytes:       prevRequest.sizeBytes,
	}
	request.ctx, request.cancel = context.WithCancel(context.Background())
	request.ctx = pctx.WithPorchValuesFrom(prevRequest.ctx, request.ctx)

	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.removeLargeWaitlistedPr(prKey)
	rs.scheduled[prKey] = request
	if !rs.queueRender(request) {
		delete(rs.scheduled, prKey)
		return fmt.Errorf("work queue is full, cannot reschedule render for %s", prName)
	}
	return nil
}

func (rs *RenderScheduler) HasParallelRender(prKey repository.PackageRevisionKey, myWorkerID int) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	ongoing, exists := rs.ongoing[prKey]
	if exists && ongoing.workerID != myWorkerID {
		return true
	}
	if _, exists := rs.scheduled[prKey]; exists {
		return true
	}
	return false
}

func (rs *RenderScheduler) cancelRenderDueToNewRequest(prKey repository.PackageRevisionKey, prName string, k8sUserName string) {
	if ongoing, exists := rs.ongoing[prKey]; exists {
		klog.Infof("Cancelling ongoing render for %s due to new request from %s", prName, k8sUserName)
		if ongoing.cancel != nil {
			ongoing.cancel()
		}
		delete(rs.ongoing, prKey)
	}
	if scheduled, exists := rs.scheduled[prKey]; exists {
		klog.Infof("Cancelling scheduled render for %q due to new request from %q", prName, k8sUserName)
		if scheduled.cancel != nil {
			scheduled.cancel()
		}
		delete(rs.scheduled, prKey)
	}
	rs.removeLargeWaitlistedPr(prKey)
}

func prepareKptfileRenderConditions(
	ctx context.Context,
	repo repository.Repository,
	draft repository.PackageRevisionDraft,
	prName string,
	kptfileContent string,
	renderNeeded bool,
) (repository.PackageRevision, error) {
	var updatedKptfile string
	var err error

	if !renderNeeded {
		klog.Infof("Rendering is not needed, due to an empty pipeline for %q...", prName)
		updatedKptfile, err = setFinalRenderConditionsKptfile(kptfileContent, &kptfileapi.RenderStatus{})
		if err != nil {
			return nil, pkgerrors.Wrapf(err, "could not set final render conditions for %q", prName)
		}
	} else {
		updatedKptfile, err = setInitialRenderConditions(kptfileContent)
		if err != nil {
			return nil, pkgerrors.Wrapf(err, "could not set initial render conditions for %q", prName)
		}
	}

	if err := draft.UpdateKptfileContent(ctx, updatedKptfile); err != nil {
		if !renderNeeded {
			return nil, pkgerrors.Wrapf(err, "could not write final render conditions for %q", prName)
		}
		return nil, pkgerrors.Wrapf(err, "could not write initial render condition Kptfile for %q", prName)
	}

	pr, err := repo.ClosePackageRevisionDraftNoResources(ctx, draft, 0)
	if err != nil {
		return pr, err
	}
	return pr, nil
}

func (rs *RenderScheduler) NeedRender(kptfileContent string) bool {
	kf, err := kptfilesdk.NewFromPackage(map[string]string{kptfileapi.KptFileName: kptfileContent})
	if err != nil {
		klog.Warningf("failed to load Kptfile: %s", err)
		return true
	}
	MutatorsObjects, _, err := kf.NestedSlice("pipeline", "mutators")
	if err != nil {
		klog.Warningf("couldn't get Mutators Objects: %s", err)
		return true
	}
	ValidatorsObjects, _, err := kf.NestedSlice("pipeline", "validators")
	if err != nil {
		klog.Warningf("couldn't get Validators Objects: %s", err)
		return true
	}
	return len(MutatorsObjects) != 0 || len(ValidatorsObjects) != 0
}

func (rs *RenderScheduler) queueRender(req RenderExecutionRequest) bool {
	prName := renderRequestName(req)
	if rs.isLarge(req.sizeBytes) && rs.largeRendersOngoing >= rs.maxConcurrentLargeRenders {
		rs.largeWaitlist = append(rs.largeWaitlist, req)
		klog.Infof("Waitlisting large render for %q (size=%d bytes), waitlist length %d",
			prName, req.sizeBytes, len(rs.largeWaitlist))
		metrics.RecordQueueSize("RenderSchedulerLargeWaitlist", float64(len(rs.largeWaitlist)))
		return true
	}
	select {
	case rs.workQueue <- req:
		if rs.isLarge(req.sizeBytes) {
			rs.largeRendersOngoing++
		}
		klog.Infof("Putting render for %q (size=%d bytes) in the work queue, current length is %d",
			prName, req.sizeBytes, len(rs.workQueue))
		metrics.RecordQueueSize("RenderSchedulerWorkQueue", float64(len(rs.workQueue)))
		return true
	default:
		return false
	}

}

func writeFinalError(
	ctx context.Context,
	repo repository.Repository,
	pr repository.PackageRevision,
	prName string,
	kptfileContent string,
	finalErr error,
) (repository.PackageRevision, error) {
	updatedKptfile, err := setFinalRenderConditionsKptfile(kptfileContent, &kptfileapi.RenderStatus{
		ErrorSummary: finalErr.Error(),
	})
	if err != nil {
		finalErr = multierr.Append(finalErr, pkgerrors.Wrapf(err, "could not set final render conditions for %q", prName))
		return pr, finalErr
	}
	draft, err := repo.UpdatePackageRevision(ctx, pr)
	if err != nil {
		finalErr = multierr.Append(finalErr, pkgerrors.Wrapf(err, "could not write final render conditions for %q", prName))
		return pr, finalErr
	}
	if err := draft.UpdateKptfileContent(ctx, updatedKptfile); err != nil {
		finalErr = multierr.Append(finalErr, pkgerrors.Wrapf(err, "could not write final render conditions for %q", prName))
		return pr, finalErr
	}
	pr, err = repo.ClosePackageRevisionDraftNoResources(ctx, draft, 0)
	if err != nil {
		finalErr = multierr.Append(finalErr, err)
		return pr, finalErr
	}
	return pr, finalErr
}
