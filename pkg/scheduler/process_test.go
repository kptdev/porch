package scheduler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"sync"
	"testing"
	"time"

	kptfileapi "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

func TestDoRender_NoPackageFound(t *testing.T) {
	rs := NewRenderScheduler(WithWorkQueueSize(1))
	rs.SetRuntime(noopTestFunctionRuntime{})
	key := testPackageRevisionKey("repo", "package", "v1")

	resources, status := rs.doRender(context.Background(), map[string]string{"only.yaml": "data"}, key)

	assert.Equal(t, map[string]string{"only.yaml": "data"}, resources)
	assert.Contains(t, status.ErrorSummary, "no package was found")
}

func TestProcess(t *testing.T) {
	resources := newSleepFnResources()
	ctx := context.Background()

	klog.InitFlags(nil)
	testCases := map[string]struct {
		mockClient         *mockclient.MockClient
		skipRender         bool
		scheduled          bool
		initialQueueLength int
	}{
		"process_runs_successfully": {
			mockClient:         setupMockClientForResources(testPackageRevisionKey("repo", "package", "v1"), resources),
			skipRender:         false,
			scheduled:          true,
			initialQueueLength: 1,
		},
		"process_fails_at_update": {
			mockClient:         nil,
			skipRender:         false,
			scheduled:          true,
			initialQueueLength: 1,
		},
		"process_skipped_because_of_cloned_package": {
			mockClient:         setupMockClient(),
			skipRender:         true,
			scheduled:          false,
			initialQueueLength: 0,
		},
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			// given
			key := testPackageRevisionKey("repo", "package", "v1")
			var resourceStore map[string]map[string]string
			mockClient := tc.mockClient
			if tc.scheduled {
				resourceStore = map[string]map[string]string{key.K8SName(): maps.Clone(resources)}
				if tn == "process_fails_at_update" {
					mockClient = setupMockClientUpdateFails(resourceStore, key, resources)
				} else {
					mockClient = setupMockClientForResourceStore(resourceStore)
				}
			}
			rs := NewRenderScheduler(WithWorkQueueSize(10))
			rs.kubeClient = mockClient
			rs.SetRuntime(noopTestFunctionRuntime{})

			pr := new(mockrepo.MockPackageRevision)
			repo := &mockrepo.MockRepository{}
			draft := &mockrepo.MockPackageRevisionDraft{}

			setupMockPRExpectations(pr, key, "1", resources)
			setupMockPRDraftExpectations(draft, key, resources["Kptfile"])
			setupMockRepoExpectations(repo, draft, pr)
			prKey := pr.Key()

			// when
			_, err := rs.ScheduleRender(ctx, repo, draft, tc.skipRender)
			require.NoError(t, err)

			rs.mu.Lock()
			_, scheduled := rs.scheduled[prKey]
			initialQueueLen := len(rs.workQueue)
			rs.mu.Unlock()
			assert.Equal(t, tc.scheduled, scheduled)
			assert.Equal(t, tc.initialQueueLength, initialQueueLen)

			err = rs.StartProcessing(1)
			require.NoError(t, err)

			waitUntilScheduledRenderFinishes(t, ctx, rs, pr, resourceStore)

			// then
			rs.mu.Lock()
			_, stillScheduled := rs.scheduled[prKey]
			_, stillOngoing := rs.ongoing[prKey]
			finalQueueLen := len(rs.workQueue)
			rs.mu.Unlock()

			assert.False(t, stillScheduled)
			assert.False(t, stillOngoing)
			assert.Equal(t, 0, finalQueueLen)

			rs.StopProcessing()
			pr.AssertExpectations(t)
		})
	}
}

func TestProcess_GetResourcesFailure(t *testing.T) {
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	key := testPackageRevisionKey("repo", "package", "v1")
	pr := new(mockrepo.MockPackageRevision)
	pr.On("Key").Return(key).Maybe()
	pr.On("GetResources", mock.Anything).Return((*porchapi.PackageRevisionResources)(nil), errors.New("network error")).Maybe()

	rs.process(RenderExecutionRequest{pr: pr, ctx: context.Background()}, 0)
}

func TestProcess_NilPackageRevision(t *testing.T) {
	rs := NewRenderScheduler(WithWorkQueueSize(10))

	assert.NotPanics(t, func() {
		rs.process(RenderExecutionRequest{retryNum: 1}, 0)
	})
}

func TestProcess_PackageRevisionDeletedBeforeRender(t *testing.T) {
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	key := testPackageRevisionKey("repo", "package", "v1")
	pr := new(mockrepo.MockPackageRevision)
	pr.On("Key").Return(key).Maybe()
	pr.On("GetResources", mock.Anything).Return((*porchapi.PackageRevisionResources)(nil), errors.New("not found")).Maybe()

	rs.process(RenderExecutionRequest{
		pr:  pr,
		ctx: context.Background(),
	}, 0)
}

func TestProcess_ParallelRenderSkipsUpdate(t *testing.T) {
	resources := newSleepFnResources()
	key := testPackageRevisionKey("repo", "package", "v1")

	rs := NewRenderScheduler(WithWorkQueueSize(10))
	rs.kubeClient = setupMockClientForResources(key, resources)
	rs.SetRuntime(noopTestFunctionRuntime{})

	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", resources)

	rs.mu.Lock()
	rs.ongoing[key] = RenderExecutionRequest{
		pr:       pr,
		workerID: 99,
	}
	rs.mu.Unlock()

	rs.process(RenderExecutionRequest{
		pr:  pr,
		ctx: context.Background(),
	}, 0)
}

func testRenderKptfile(name string) string {
	return fmt.Sprintf(`apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: %s
pipeline:
  mutators:
    - image: noop
`, name)
}

func testRenderConfigMap(name string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
data:
  key: value
`, name)
}

func TestRenderLogsKubernetesPackageNames(t *testing.T) {
	// given
	oldKlogState := klog.CaptureState()
	var logs bytes.Buffer
	klog.SetOutput(&logs)
	klog.LogToStderr(false)
	t.Cleanup(func() {
		klog.Flush()
		oldKlogState.Restore()
	})

	rs := NewRenderScheduler()
	rs.SetRuntime(noopTestFunctionRuntime{})
	rs.SetRunnerOptions(func(string) runneroptions.RunnerOptions {
		return runneroptions.RunnerOptions{LogOptions: runneroptions.LogOptions{
			PkgNameSep: ".",
			PkgNameID:  runneroptions.KptfileMeta,
		}}
	})
	key := testPackageRevisionKey("my-repo", "parent-pkg", "my-ws")
	resources := map[string]string{
		"Kptfile":        testRenderKptfile("parent-pkg"),
		"cm.yaml":        testRenderConfigMap("parent"),
		"subdir/Kptfile": testRenderKptfile("child-pkg"),
		"subdir/cm.yaml": testRenderConfigMap("child"),
	}

	// when
	_, status := rs.doRender(t.Context(), resources, key)
	klog.Flush()

	// then
	require.Empty(t, status.ErrorSummary)
	logged := logs.String()
	assert.Contains(t, logged, `Package "my-repo.parent-pkg.my-ws":`)
	assert.Contains(t, logged, `Package "my-repo.parent-pkg.child-pkg.my-ws":`)
}

func TestStartProcessing_CancelledRequestWithoutReplacement(t *testing.T) {
	resources := newSleepFnResources()
	key := testPackageRevisionKey("repo", "package", "v1")

	resourceStore := map[string]map[string]string{key.K8SName(): maps.Clone(resources)}
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	rs.kubeClient = setupMockClientForResourceStore(resourceStore)
	rs.SetRuntime(noopTestFunctionRuntime{})

	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", resources)

	staleCtx, staleCancel := context.WithCancel(context.Background())
	staleCancel()

	rs.workQueue <- RenderExecutionRequest{
		pr:              pr,
		resourceVersion: "1",
		ctx:             staleCtx,
		scheduledTime:   time.Now(),
	}

	require.NoError(t, rs.StartProcessing(1))
	waitUntilScheduledRenderFinishes(t, context.Background(), rs, pr, resourceStore)
	rs.StopProcessing()
}

func TestStartProcessing_SkipsStaleCancelledRequest(t *testing.T) {
	resources := newSleepFnResources()
	key := testPackageRevisionKey("repo", "package", "v1")

	rs := NewRenderScheduler(WithWorkQueueSize(10))
	rs.kubeClient = setupMockClientForResources(key, resources)
	rs.SetRuntime(noopTestFunctionRuntime{})

	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", resources)

	staleCtx, staleCancel := context.WithCancel(context.Background())
	staleCancel()

	rs.mu.Lock()
	rs.scheduled[key] = RenderExecutionRequest{
		pr:              pr,
		resourceVersion: "2",
	}
	rs.mu.Unlock()

	rs.workQueue <- RenderExecutionRequest{
		pr:              pr,
		resourceVersion: "1",
		ctx:             staleCtx,
		scheduledTime:   time.Now(),
	}

	require.NoError(t, rs.StartProcessing(1))
	time.Sleep(100 * time.Millisecond)
	rs.StopProcessing()

	rs.mu.Lock()
	_, ongoing := rs.ongoing[key]
	rs.mu.Unlock()
	assert.False(t, ongoing)
}

func TestStartProcessing_WorkerLifecycle(t *testing.T) {
	// given
	resources := newSleepFnResources()
	ctx := context.Background()

	key := testPackageRevisionKey("repo", "package", "v1")
	resourceStore := map[string]map[string]string{key.K8SName(): maps.Clone(resources)}
	mockClient := setupMockClientForResourceStore(resourceStore)
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	rs.kubeClient = mockClient
	rs.SetRuntime(noopTestFunctionRuntime{})

	pr := new(mockrepo.MockPackageRevision)
	repo := &mockrepo.MockRepository{}
	draft := &mockrepo.MockPackageRevisionDraft{}

	setupMockPRExpectations(pr, key, "1", resources)
	setupMockPRDraftExpectations(draft, key, resources["Kptfile"])
	setupMockRepoExpectations(repo, draft, pr)
	prKey := pr.Key()

	// when
	_, err := rs.ScheduleRender(ctx, repo, draft, false)
	require.NoError(t, err)

	rs.mu.Lock()
	_, scheduled := rs.scheduled[prKey]
	initialQueueLen := len(rs.workQueue)
	rs.mu.Unlock()
	assert.True(t, scheduled)
	assert.Equal(t, 1, initialQueueLen)

	err = rs.StartProcessing(1)
	require.NoError(t, err)

	waitUntilScheduledRenderFinishes(t, ctx, rs, pr, resourceStore)

	// then
	rs.mu.Lock()
	_, stillScheduled := rs.scheduled[prKey]
	_, stillOngoing := rs.ongoing[prKey]
	finalQueueLen := len(rs.workQueue)
	rs.mu.Unlock()

	assert.False(t, stillScheduled)
	assert.False(t, stillOngoing)
	assert.Equal(t, 0, finalQueueLen)

	rs.StopProcessing()
	pr.AssertExpectations(t)
}

func TestNeedLeaderElection(t *testing.T) {
	rs := NewRenderScheduler()

	needLeaderElection := rs.NeedLeaderElection()

	require.True(t, needLeaderElection)
}

func TestStart_ReturnsAfterContextCancelled(t *testing.T) {
	rs := NewRenderScheduler(WithWorkQueueSize(1), WithWorkerNum(1))
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- rs.Start(ctx)
	}()

	cancel()

	var err error
	require.Eventually(t, func() bool {
		select {
		case err = <-errCh:
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond)
	require.NoError(t, err)
}

func TestStopProcessing_CancelsAll(t *testing.T) {
	// given
	rs := NewRenderScheduler(WithWorkQueueSize(10))

	numOperations := 3
	cancelledUIDs := make(map[repository.PackageRevisionKey]bool)
	cancelSignals := make(chan repository.PackageRevisionKey, numOperations)
	var mu sync.Mutex

	for i := range numOperations {
		uid := testPackageRevisionKey("test-repo", "uid", fmt.Sprintf("v%d", i))
		cancelFunc := func(capturedUID repository.PackageRevisionKey) func() {
			return func() {
				mu.Lock()
				cancelledUIDs[capturedUID] = true
				mu.Unlock()
				cancelSignals <- capturedUID
			}
		}(uid)
		rs.mu.Lock()
		rs.ongoing[uid] = RenderExecutionRequest{
			cancel:   cancelFunc,
			workerID: 1,
		}
		rs.mu.Unlock()
	}

	// when
	rs.StopProcessing()

	// then
	cancelledCount := 0
	timeout := time.After(200 * time.Millisecond)
	for cancelledCount < numOperations {
		select {
		case uid := <-cancelSignals:
			t.Logf("Cancelled operation for UID: %s", uid)
			cancelledCount++
		case <-timeout:
			t.Fatalf("Timeout waiting for cancellations. Only %d/%d operations cancelled", cancelledCount, numOperations)
		}
	}

	mu.Lock()
	assert.Equal(t, numOperations, len(cancelledUIDs))
	for i := 0; i < numOperations; i++ {
		uid := testPackageRevisionKey("test-repo", "uid", fmt.Sprintf("v%d", i))
		assert.True(t, cancelledUIDs[uid], "Operation %s should be cancelled", uid)
	}
	mu.Unlock()

	rs.mu.Lock()
	assert.Equal(t, 0, len(rs.ongoing))
	rs.mu.Unlock()
}

func TestTryToUpdatePrrReallyHard_ConflictWithSameResourcesRetries(t *testing.T) {
	key := testPackageRevisionKey("repo", "package", "v1")
	resources := map[string]string{"Kptfile": minimalKptfile()}
	store := map[string]map[string]string{key.K8SName(): maps.Clone(resources)}

	client := &mockclient.MockClient{}
	client.On("Get", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		nsKey := args.Get(1).(types.NamespacedName)
		prr := args.Get(2).(*porchapi.PackageRevisionResources)
		prr.Name = nsKey.Name
		prr.Namespace = nsKey.Namespace
		prr.ResourceVersion = "2"
		prr.Spec.Resources = maps.Clone(store[nsKey.Name])
	}).Return(nil).Maybe()

	updateCalls := 0
	client.On("Update", mock.Anything, mock.Anything).Return(apierrors.NewConflict(porchapi.Resource("packagerevisionresources"), key.K8SName(), errors.New("the object has been modified"))).Once()
	client.On("Update", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		updateCalls++
		prr := args.Get(1).(*porchapi.PackageRevisionResources)
		store[prr.Name] = maps.Clone(prr.Spec.Resources)
	}).Return(nil)

	rs := NewRenderScheduler(WithWorkQueueSize(1))
	rs.kubeClient = client

	prr := &porchapi.PackageRevisionResources{
		ObjectMeta: metav1.ObjectMeta{
			Name:            key.K8SName(),
			Namespace:       key.K8SNS(),
			ResourceVersion: "1",
		},
		Spec: porchapi.PackageRevisionResourcesSpec{Resources: maps.Clone(resources)},
	}

	err := rs.tryToUpdatePrrReallyHard(context.Background(), prr, computeResourceHash(resources), key, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, updateCalls)
}

func TestTryToUpdatePrrReallyHard_ContextCancelledFallback(t *testing.T) {
	key := testPackageRevisionKey("repo", "package", "v1")
	resources := map[string]string{"Kptfile": minimalKptfile()}

	client := &mockclient.MockClient{}
	client.On("Update", mock.Anything, mock.Anything).Return(errors.New("update failed")).Once()
	client.On("Update", mock.Anything, mock.Anything).Return(nil).Once()

	rs := NewRenderScheduler(WithWorkQueueSize(1))
	rs.kubeClient = client

	prr := &porchapi.PackageRevisionResources{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.K8SName(),
			Namespace: key.K8SNS(),
		},
		Spec: porchapi.PackageRevisionResourcesSpec{Resources: resources},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := rs.tryToUpdatePrrReallyHard(ctx, prr, computeResourceHash(resources), key, 0)
	assert.Error(t, err)
	client.AssertNumberOfCalls(t, "Update", 2)
}

func TestValidateScheduledRender_SkipsWhenNewerScheduledExists(t *testing.T) {
	rs := NewRenderScheduler()
	key := testPackageRevisionKey("repo", "package", "v1")
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", newSleepFnResources())

	staleCtx, cancel := context.WithCancel(context.Background())
	cancel()

	rs.mu.Lock()
	rs.scheduled[key] = RenderExecutionRequest{resourceVersion: "2"}
	rs.mu.Unlock()

	req := RenderExecutionRequest{pr: pr, resourceVersion: "1", ctx: staleCtx}
	_, _, proceed := rs.validateScheduledRender(&req, 0)
	assert.False(t, proceed)
}

func TestValidateScheduledRender_SkipsWhenScheduledRVMismatch(t *testing.T) {
	rs := NewRenderScheduler()
	key := testPackageRevisionKey("repo", "package", "v1")
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", newSleepFnResources())

	rs.mu.Lock()
	rs.scheduled[key] = RenderExecutionRequest{resourceVersion: "2"}
	rs.mu.Unlock()

	req := RenderExecutionRequest{pr: pr, resourceVersion: "1", ctx: context.Background()}
	_, _, proceed := rs.validateScheduledRender(&req, 0)
	assert.False(t, proceed)
}

func TestValidateScheduledRender_ProceedsAndClearsScheduled(t *testing.T) {
	rs := NewRenderScheduler()
	key := testPackageRevisionKey("repo", "package", "v1")
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", newSleepFnResources())

	rs.mu.Lock()
	rs.scheduled[key] = RenderExecutionRequest{resourceVersion: "1"}
	rs.mu.Unlock()

	req := RenderExecutionRequest{
		pr:              pr,
		resourceVersion: "1",
		ctx:             context.Background(),
		scheduledTime:   time.Now(),
	}
	_, _, proceed := rs.validateScheduledRender(&req, 3)
	require.True(t, proceed)
	assert.Equal(t, 3, req.workerID)
	assert.False(t, req.renderStartTime.IsZero())

	rs.mu.Lock()
	_, stillScheduled := rs.scheduled[key]
	rs.mu.Unlock()
	assert.False(t, stillScheduled)
}

func TestLogRenderResult_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	now := time.Now()
	logRenderResult(0, "test-pr", ctx, RenderExecutionRequest{
		scheduledTime:   now.Add(-time.Second),
		renderStartTime: now,
	})
}

func TestReleaseOngoingRender_KeepsEntryForDifferentWorker(t *testing.T) {
	rs := NewRenderScheduler()
	key := testPackageRevisionKey("repo", "package", "v1")

	rs.mu.Lock()
	rs.ongoing[key] = RenderExecutionRequest{workerID: 1}
	rs.mu.Unlock()

	rs.releaseOngoingRender(key, 0, RenderExecutionRequest{})

	rs.mu.Lock()
	_, exists := rs.ongoing[key]
	rs.mu.Unlock()
	assert.True(t, exists)
}

func TestReleaseOngoingRender_RemovesEntryForMatchingWorker(t *testing.T) {
	rs := NewRenderScheduler()
	key := testPackageRevisionKey("repo", "package", "v1")

	rs.mu.Lock()
	rs.ongoing[key] = RenderExecutionRequest{workerID: 2}
	rs.mu.Unlock()

	rs.releaseOngoingRender(key, 2, RenderExecutionRequest{})

	rs.mu.Lock()
	_, exists := rs.ongoing[key]
	rs.mu.Unlock()
	assert.False(t, exists)
}

func TestApplyRenderedResourcesToPRR_MissingKptfileInOutput(t *testing.T) {
	prr := porchapi.PackageRevisionResources{}
	input := map[string]string{kptfileapi.KptFileName: minimalKptfile()}
	rendered := map[string]string{"orphan.yaml": "data"}

	applyRenderedResourcesToPRR(&prr, input, rendered, kptfileapi.RenderStatus{}, 0, "test-pr")

	assert.Equal(t, rendered, prr.Spec.Resources)
	assert.True(t, prr.Spec.DisableRender)
}

func TestApplyRenderedResourcesToPRR_InvalidKptfileForConditions(t *testing.T) {
	prr := porchapi.PackageRevisionResources{}
	badRendered := map[string]string{kptfileapi.KptFileName: "not-valid-kptfile"}

	applyRenderedResourcesToPRR(&prr, map[string]string{kptfileapi.KptFileName: minimalKptfile()}, badRendered, kptfileapi.RenderStatus{}, 0, "test-pr")

	assert.Equal(t, badRendered, prr.Spec.Resources)
}

func TestHandlePRRUpdateFailure_ParallelRenderSkips(t *testing.T) {
	rs := NewRenderScheduler()
	key := testPackageRevisionKey("repo", "package", "v1")
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", newSleepFnResources())

	rs.mu.Lock()
	rs.scheduled[key] = RenderExecutionRequest{pr: pr}
	rs.mu.Unlock()

	prr := &porchapi.PackageRevisionResources{}
	rs.handlePRRUpdateFailure(
		context.Background(),
		RenderExecutionRequest{pr: pr},
		0,
		key,
		prr,
		0,
		map[string]string{kptfileapi.KptFileName: minimalKptfile()},
		kptfileapi.RenderStatus{},
		errors.New("update failed"),
	)
}

func TestHandlePRRUpdateFailure_NotFoundSkips(t *testing.T) {
	rs := NewRenderScheduler()
	key := testPackageRevisionKey("repo", "package", "v1")
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", newSleepFnResources())

	prr := &porchapi.PackageRevisionResources{}
	rs.handlePRRUpdateFailure(
		context.Background(),
		RenderExecutionRequest{pr: pr},
		0,
		key,
		prr,
		0,
		map[string]string{kptfileapi.KptFileName: minimalKptfile()},
		kptfileapi.RenderStatus{},
		apierrors.NewNotFound(porchapi.Resource("packagerevisionresources"), key.K8SName()),
	)
}

func TestHandlePRRUpdateFailure_ReschedulesWhenRetryAllowed(t *testing.T) {
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	rs.SetRuntime(noopTestFunctionRuntime{})
	key := testPackageRevisionKey("repo", "package", "v1")
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", map[string]string{kptfileapi.KptFileName: minimalKptfile()})

	client := &mockclient.MockClient{}
	client.On("Get", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		prr := args.Get(2).(*porchapi.PackageRevisionResources)
		prr.ObjectMeta = metav1.ObjectMeta{
			Name:            key.K8SName(),
			Namespace:       key.K8SNS(),
			ResourceVersion: "9",
		}
		prr.Spec.Resources = map[string]string{kptfileapi.KptFileName: minimalKptfile()}
	}).Return(nil)
	rs.SetKubeClient(client)

	prr := &porchapi.PackageRevisionResources{
		ObjectMeta: metav1.ObjectMeta{Name: key.K8SName(), Namespace: key.K8SNS()},
	}
	rs.handlePRRUpdateFailure(
		context.Background(),
		RenderExecutionRequest{pr: pr, retryNum: 0, sizeBytes: 0},
		0,
		key,
		prr,
		computeResourceHash(map[string]string{kptfileapi.KptFileName: minimalKptfile()}),
		map[string]string{kptfileapi.KptFileName: minimalKptfile()},
		kptfileapi.RenderStatus{},
		errors.New("update failed"),
	)

	rs.mu.Lock()
	_, scheduled := rs.scheduled[key]
	queueLen := len(rs.workQueue)
	rs.mu.Unlock()
	assert.True(t, scheduled)
	assert.Equal(t, 1, queueLen)
}

func TestHandlePRRUpdateFailure_ExhaustedRetriesWritesErrorStatus(t *testing.T) {
	rs := NewRenderScheduler(WithWorkQueueSize(1))
	key := testPackageRevisionKey("repo", "package", "v1")
	resources := map[string]string{kptfileapi.KptFileName: minimalKptfile()}

	client := &mockclient.MockClient{}
	client.On("Update", mock.Anything, mock.Anything).Return(nil)
	rs.SetKubeClient(client)

	prr := &porchapi.PackageRevisionResources{
		ObjectMeta: metav1.ObjectMeta{Name: key.K8SName(), Namespace: key.K8SNS()},
		Spec:       porchapi.PackageRevisionResourcesSpec{Resources: resources},
	}

	rs.handlePRRUpdateFailure(
		context.Background(),
		RenderExecutionRequest{pr: nil, retryNum: maxRenderRetry},
		0,
		key,
		prr,
		computeResourceHash(resources),
		resources,
		kptfileapi.RenderStatus{ErrorSummary: "render had failed"},
		errors.New("update failed"),
	)

	client.AssertNumberOfCalls(t, "Update", 1)
	renderFinished, rendered := kptfileConditionsFromResources(t, prr.Spec.Resources)
	assert.Equal(t, kptfileapi.ConditionTrue, renderFinished)
	assert.Equal(t, kptfileapi.ConditionFalse, rendered)
}

func TestLoadPRR_NotFoundError(t *testing.T) {
	key := testPackageRevisionKey("repo", "package", "v1")
	pr := new(mockrepo.MockPackageRevision)
	pr.On("Key").Return(key).Maybe()
	pr.On("GetResources", mock.Anything).Return((*porchapi.PackageRevisionResources)(nil), errors.New("package not found")).Maybe()

	_, ok := loadPRR(context.Background(), RenderExecutionRequest{pr: pr}, 0, key.K8SName())
	assert.False(t, ok)
}

func TestValidateRequest_EmptyPackageRevisionName(t *testing.T) {
	pr := new(mockrepo.MockPackageRevision)
	pr.On("Key").Return(repository.PackageRevisionKey{}).Maybe()

	_, name, ok := validateRequest(RenderExecutionRequest{pr: pr}, 0)
	assert.False(t, ok)
	assert.Empty(t, name)
}

func TestReadRenderedResources_NoKptfileOnDisk(t *testing.T) {
	fs := filesys.MakeFsOnDisk()
	root := t.TempDir()
	require.NoError(t, fs.WriteFile(filepath.Join(root, "only.yaml"), []byte("data")))

	key := testPackageRevisionKey("repo", "package", "v1")
	input := map[string]string{kptfileapi.KptFileName: minimalKptfile()}
	_, err := readRenderedResources(fs, root, input, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no Kptfile")
}

func TestRenderStatusFromRenderedKptfile_InvalidYAML(t *testing.T) {
	rs := NewRenderScheduler()
	key := testPackageRevisionKey("repo", "package", "v1")
	input := map[string]string{kptfileapi.KptFileName: minimalKptfile()}
	contents := map[string]string{kptfileapi.KptFileName: "{{not valid yaml"}
	_, status := rs.renderStatusFromRenderedKptfile(input, contents, key, errors.New("step failed"))
	assert.Empty(t, status.MutationSteps)
}

func kptfileWithRenderStatus() string {
	return `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test
status:
  renderStatus:
    errorSummary: initial
`
}

func TestRenderStatusFromRenderedKptfile_ExtendsErrorSummary(t *testing.T) {
	rs := NewRenderScheduler()
	key := testPackageRevisionKey("repo", "package", "v1")
	input := map[string]string{kptfileapi.KptFileName: minimalKptfile()}
	contents := map[string]string{kptfileapi.KptFileName: kptfileWithRenderStatus()}
	updated, status := rs.renderStatusFromRenderedKptfile(input, contents, key, errors.New("extra failure"))
	assert.Contains(t, status.ErrorSummary, "extra failure")
	assert.Contains(t, updated[kptfileapi.KptFileName], "extra failure")
}

func TestMissingRenderStatusResult(t *testing.T) {
	key := testPackageRevisionKey("repo", "package", "v1")
	input := map[string]string{"Kptfile": "input", "a.yaml": "original"}
	rendered := map[string]string{"Kptfile": "rendered", "a.yaml": "partial", "new.layers": "x"}
	rs := NewRenderScheduler()

	t.Run("no error keeps rendered contents", func(t *testing.T) {
		got, status := rs.missingRenderStatusResult(key, input, rendered, nil)

		assert.Equal(t, rendered, got)
		assert.Empty(t, status.ErrorSummary)
	})

	t.Run("error preserves input and reports the error", func(t *testing.T) {
		got, status := rs.missingRenderStatusResult(key, input, rendered, errors.New("failed to save resources: no space left on device"))

		assert.Equal(t, input, got)
		assert.Contains(t, status.ErrorSummary, "no space left on device")
	})
}

func TestDoRender_UsesConfiguredRenderTmpDir(t *testing.T) {
	tmp := t.TempDir()
	rs := NewRenderScheduler(WithRenderTmpDir(tmp))
	rs.SetRuntime(noopTestFunctionRuntime{})
	rs.SetRunnerOptions(func(string) runneroptions.RunnerOptions { return runneroptions.RunnerOptions{} })
	key := testPackageRevisionKey("repo", "pkg", "ws")

	_, status := rs.doRender(t.Context(), map[string]string{"Kptfile": testRenderKptfile("pkg"), "cm.yaml": testRenderConfigMap("x")}, key)

	assert.Empty(t, status.ErrorSummary)
	entries, err := filepath.Glob(filepath.Join(tmp, "porch-render-*"))
	require.NoError(t, err)
	assert.Empty(t, entries, "scratch dir must be removed after render")
}

func TestDoRender_RenderTmpDirUnusable(t *testing.T) {
	rs := NewRenderScheduler(WithRenderTmpDir(filepath.Join(t.TempDir(), "does-not-exist")))
	rs.SetRuntime(noopTestFunctionRuntime{})
	input := map[string]string{"Kptfile": testRenderKptfile("pkg")}

	got, status := rs.doRender(t.Context(), input, testPackageRevisionKey("repo", "pkg", "ws"))

	assert.Equal(t, input, got)
	assert.Contains(t, status.ErrorSummary, "couldn't create render temp directory")
}
