package scheduler

import (
	"context"
	"errors"
	"io"
	"maps"
	"testing"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	kptfilesdk "github.com/kptdev/krm-functions-sdk/go/fn/kptfileko"
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
	"k8s.io/apimachinery/pkg/util/wait"
)

const testLargePackageThreshold int64 = 100

type noopTestFunctionRuntime struct{}

func (noopTestFunctionRuntime) GetRunner(context.Context, *kptfilev1.Function) (fn.FunctionRunner, error) {
	return noopTestFunctionRunner{}, nil
}

type noopTestFunctionRunner struct{}

func (noopTestFunctionRunner) Run(r io.Reader, w io.Writer) error {
	_, err := io.Copy(w, r)
	return err
}

func testPackageRevisionKey(repoName, packageName, workspace string) repository.PackageRevisionKey {
	return repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Namespace: "test-namespace",
				Name:      repoName,
			},
			Package: packageName,
		},
		WorkspaceName: workspace,
	}
}

func newSleepFnResources() map[string]string {
	return map[string]string{
		"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
 name: test
pipeline:
 mutators:
 - name: test-sleep
   image: ghcr.io/kptdev/krm-functions-catalog/sleep:latest
   configMap:
    duration: "1s"
`,
	}
}

func minimalKptfile() string {
	return `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test
`
}

func setupMockPRExpectations(pr *mockrepo.MockPackageRevision, key repository.PackageRevisionKey, resourceVersion string, resources map[string]string) {
	setupMockPRExpectationsWithSize(pr, key, resourceVersion, resources, 0)
}

func setupMockPRExpectationsWithSize(pr *mockrepo.MockPackageRevision, key repository.PackageRevisionKey, resourceVersion string, resources map[string]string, sizeBytes int64) {
	pr.On("Key").Return(key).Maybe()
	pr.On("GetResources", mock.Anything).Return(&porchapi.PackageRevisionResources{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.K8SName(),
			Namespace: key.K8SNS(),
		},
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: resources,
		},
	}, nil).Maybe()
	pr.On("KubeObjectName").Return(key.K8SName()).Maybe()
	pr.On("KubeObjectNamespace").Return(key.K8SNS()).Maybe()
	pr.On("ResourceVersion").Return(resourceVersion).Maybe()
	pr.On("GetPackageRevision", mock.Anything, mock.Anything).Return(&porchapi.PackageRevision{
		Status: porchapi.PackageRevisionStatus{ResourcesSizeBytes: sizeBytes},
	}, nil).Maybe()
}

func setupMockRepoExpectations(repo *mockrepo.MockRepository, draft *mockrepo.MockPackageRevisionDraft, pr *mockrepo.MockPackageRevision) {
	repo.On("UpdatePackageRevision", mock.Anything, pr).Return(draft, nil).Maybe()
	repo.On("ClosePackageRevisionDraftNoResources", mock.Anything, draft, 0).Return(pr, nil).Maybe()
}

func setupMockPRDraftExpectations(draft *mockrepo.MockPackageRevisionDraft, key repository.PackageRevisionKey, kptfileContent string) {
	draft.On("GetKptfileContent", mock.Anything).Return(kptfileContent, nil)
	draft.On("UpdateKptfileContent", mock.Anything, mock.Anything).Return(nil)
	draft.On("Key").Return(key)
}

func setupMockClient() *mockclient.MockClient {
	client := &mockclient.MockClient{}
	client.On("Update", mock.Anything, mock.Anything).Return(nil)
	return client
}

func setupMockClientForResources(key repository.PackageRevisionKey, resources map[string]string) *mockclient.MockClient {
	store := map[string]map[string]string{
		key.K8SName(): resources,
	}
	return setupMockClientForResourceStore(store)
}

func setupMockClientForResourceStore(store map[string]map[string]string) *mockclient.MockClient {
	client := &mockclient.MockClient{}

	client.On("Get", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		key := args.Get(1).(types.NamespacedName)
		prr := args.Get(2).(*porchapi.PackageRevisionResources)
		if resources, ok := store[key.Name]; ok {
			prr.Spec.Resources = maps.Clone(resources)
		}
		prr.Name = key.Name
		prr.Namespace = key.Namespace
		if prr.ResourceVersion == "" {
			prr.ResourceVersion = "1"
		}
	}).Return(nil).Maybe()

	client.On("Update", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		prr := args.Get(1).(*porchapi.PackageRevisionResources)
		if existing, ok := store[prr.Name]; ok {
			for k := range existing {
				delete(existing, k)
			}
			for k, v := range prr.Spec.Resources {
				existing[k] = v
			}
		} else {
			store[prr.Name] = maps.Clone(prr.Spec.Resources)
		}
	}).Return(nil).Maybe()

	return client
}

func setupMockClientUpdateFails(store map[string]map[string]string, key repository.PackageRevisionKey, resources map[string]string) *mockclient.MockClient {
	if store == nil {
		store = map[string]map[string]string{
			key.K8SName(): resources,
		}
	} else if store[key.K8SName()] == nil {
		store[key.K8SName()] = maps.Clone(resources)
	}
	client := &mockclient.MockClient{}

	client.On("Get", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		nsKey := args.Get(1).(types.NamespacedName)
		prr := args.Get(2).(*porchapi.PackageRevisionResources)
		if stored, ok := store[nsKey.Name]; ok {
			prr.Spec.Resources = maps.Clone(stored)
		}
		prr.Name = nsKey.Name
		prr.Namespace = nsKey.Namespace
		if prr.ResourceVersion == "" {
			prr.ResourceVersion = "1"
		}
	}).Return(nil).Maybe()

	client.On("Update", mock.Anything, mock.Anything).Return(apierrors.NewConflict(porchapi.Resource("packagerevisionresources"), "test", errors.New(""))).Once()
	client.On("Update", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		prr := args.Get(1).(*porchapi.PackageRevisionResources)
		if existing, ok := store[prr.Name]; ok {
			for k := range existing {
				delete(existing, k)
			}
			for k, v := range prr.Spec.Resources {
				existing[k] = v
			}
		} else {
			store[prr.Name] = maps.Clone(prr.Spec.Resources)
		}
	}).Return(nil)

	return client
}

func waitUntilScheduledRenderFinishes(t *testing.T, ctx context.Context, renderscheduler *RenderScheduler, pr repository.PackageRevision, resourceStore map[string]map[string]string) {
	t.Helper()
	const timeout = 15 * time.Second
	key := pr.Key()
	err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, timeout, true, func(ctx context.Context) (done bool, err error) {
		if resourceStore != nil {
			if resources, ok := resourceStore[key.K8SName()]; ok {
				renderFinished, rendered := kptfileConditionsFromResources(t, resources)
				if renderFinished == kptfilev1.ConditionTrue && rendered == kptfilev1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		}
		renderscheduler.mu.Lock()
		_, scheduled := renderscheduler.scheduled[key]
		queueLen := len(renderscheduler.workQueue)
		renderscheduler.mu.Unlock()
		t.Logf("Render pending: scheduled=%v workQueue=%d", scheduled, queueLen)
		return !scheduled && queueLen == 0 && renderscheduler.RenderExecutionStatus(pr) == RenderStatusUnknown, nil
	})
	if err != nil {
		t.Fatalf("Render not finished yet: %v", err)
	}
}

func kptfileConditionsFromResources(t *testing.T, resources map[string]string) (kptfilev1.ConditionStatus, kptfilev1.ConditionStatus) {
	t.Helper()
	kf, err := kptfilesdk.NewFromPackage(resources)
	require.NoError(t, err)

	renderFinishedCondition, err := kf.GetTypedCondition(RenderFinishedConditionType)
	require.NoError(t, err)
	require.NotNil(t, renderFinishedCondition)

	renderedCondition, err := kf.GetTypedCondition(RenderedConditionType)
	require.NoError(t, err)
	require.NotNil(t, renderedCondition)

	return renderFinishedCondition.Status, renderedCondition.Status
}

func newSizedPackageRevision(key repository.PackageRevisionKey, resourceVersion string, resources map[string]string, sizeBytes int64) *mockrepo.MockPackageRevision {
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectationsWithSize(pr, key, resourceVersion, resources, sizeBytes)
	return pr
}

func setupMockRepoForPR(repo *mockrepo.MockRepository, draft *mockrepo.MockPackageRevisionDraft, pr repository.PackageRevision) {
	repo.On("UpdatePackageRevision", mock.Anything, mock.Anything).Return(draft, nil).Maybe()
	repo.On("ClosePackageRevisionDraftNoResources", mock.Anything, draft, 0).Return(pr, nil).Maybe()
}

func newTestRenderScheduler(t *testing.T, store map[string]map[string]string, opts ...RenderSchedulerOption) *RenderScheduler {
	t.Helper()
	rs := NewRenderScheduler(opts...)
	rs.kubeClient = setupMockClientForResourceStore(store)
	rs.SetRuntime(noopTestFunctionRuntime{})
	return rs
}

func scheduleSizedRender(t *testing.T, rs *RenderScheduler, store map[string]map[string]string, packageName, resourceVersion string, sizeBytes int64, resources map[string]string) *mockrepo.MockPackageRevision {
	t.Helper()
	key := testPackageRevisionKey("repo", packageName, "v1")
	pr := newSizedPackageRevision(key, resourceVersion, resources, sizeBytes)
	store[key.K8SName()] = maps.Clone(resources)
	repo := &mockrepo.MockRepository{}
	draft := &mockrepo.MockPackageRevisionDraft{}
	setupMockPRDraftExpectations(draft, key, resources["Kptfile"])
	setupMockRepoForPR(repo, draft, pr)
	_, err := rs.ScheduleRender(t.Context(), repo, draft, false)
	require.NoError(t, err)
	return pr
}

func renderQueueSnapshot(rs *RenderScheduler) (workQueueLen, waitlistLen, largeOngoing int) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return len(rs.workQueue), len(rs.largeWaitlist), rs.largeRendersOngoing
}

func TestHasParallelRender(t *testing.T) {
	// given
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	key := testPackageRevisionKey("repo", "package", "v1")
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", newSleepFnResources())

	// then - no parallel render initially
	assert.False(t, rs.HasParallelRender(key, 0))

	// when - another worker is ongoing
	rs.mu.Lock()
	rs.ongoing[key] = RenderExecutionRequest{
		pr:       pr,
		workerID: 1,
	}
	rs.mu.Unlock()
	assert.True(t, rs.HasParallelRender(key, 0))
	assert.False(t, rs.HasParallelRender(key, 1))

	// when - scheduled render exists
	rs.mu.Lock()
	delete(rs.ongoing, key)
	rs.scheduled[key] = RenderExecutionRequest{pr: pr}
	rs.mu.Unlock()
	assert.True(t, rs.HasParallelRender(key, 0))
}

func TestNewRenderScheduler(t *testing.T) {
	rs := NewRenderScheduler()

	assert.Equal(t, defaultWorkQueueSize, cap(rs.workQueue))
	assert.Equal(t, 0, len(rs.workQueue))
	assert.Equal(t, defaultLargePackageBytes, rs.largePackageBytes)
	assert.Equal(t, defaultMaxConcurrentRenders, rs.maxConcurrentLargeRenders)

	queueSize := 5
	rs = NewRenderScheduler(WithWorkQueueSize(queueSize))

	assert.Equal(t, queueSize, cap(rs.workQueue))
	assert.Equal(t, defaultLargePackageBytes, rs.largePackageBytes)
	assert.Equal(t, defaultMaxConcurrentRenders, rs.maxConcurrentLargeRenders)
}

func TestRescheduleRender_ContextCancellation(t *testing.T) {
	// given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rs := NewRenderScheduler(WithWorkQueueSize(10))
	rs.SetRuntime(noopTestFunctionRuntime{})

	key := testPackageRevisionKey("test-repo", "test-pkg", "v1")
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", map[string]string{"Kptfile": minimalKptfile()})

	client := &mockclient.MockClient{}
	client.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("some error")).Maybe()
	rs.SetKubeClient(client)

	prevRequest := RenderExecutionRequest{
		pr:              pr,
		resourceVersion: "1",
		ctx:             ctx,
		retryNum:        0,
		scheduledTime:   time.Now(),
	}

	// when
	err := rs.rescheduleRender(ctx, prevRequest, 0)

	// then
	assert.Nil(t, err)

	rs.mu.Lock()
	_, exists := rs.scheduled[key]
	rs.mu.Unlock()
	assert.False(t, exists)
}

func TestRescheduleRender_DefensiveValidation(t *testing.T) {
	// given
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	rs.SetRuntime(noopTestFunctionRuntime{})

	pr := new(mockrepo.MockPackageRevision)
	pr.On("Key").Return(repository.PackageRevisionKey{}).Maybe()
	pr.On("GetResources", mock.Anything).Return(&porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{"Kptfile": minimalKptfile()},
		},
	}, nil).Maybe()

	malformedRequest := RenderExecutionRequest{
		pr:            pr,
		ctx:           context.Background(),
		retryNum:      0,
		scheduledTime: time.Now(),
	}

	// when
	rs.process(malformedRequest, 0)

	// then - handled gracefully without panic
}

func TestRescheduleRender_DeletedPackageRevision(t *testing.T) {
	ctx := context.Background()
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	key := testPackageRevisionKey("test-repo", "test-pkg", "v1")
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", map[string]string{"Kptfile": minimalKptfile()})

	client := &mockclient.MockClient{}
	client.On("Get", mock.Anything, mock.Anything, mock.Anything).
		Return(apierrors.NewNotFound(porchapi.Resource("packagerevisionresources"), key.K8SName())).Maybe()
	rs.SetKubeClient(client)

	err := rs.rescheduleRender(ctx, RenderExecutionRequest{pr: pr, ctx: ctx}, 0)
	require.NoError(t, err)
}

func TestRescheduleRender_NilPackageRevision(t *testing.T) {
	// given
	rs := NewRenderScheduler(WithWorkQueueSize(10))

	// when
	err := rs.rescheduleRender(context.Background(), RenderExecutionRequest{}, 0)

	// then
	assert.Error(t, err)
}

func TestRescheduleRender_ParallelRenderDetection(t *testing.T) {
	// given
	ctx := context.Background()
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	rs.SetRuntime(noopTestFunctionRuntime{})

	key := testPackageRevisionKey("test-repo", "test-pkg", "v1")
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", map[string]string{"Kptfile": minimalKptfile()})

	rs.mu.Lock()
	rs.ongoing[key] = RenderExecutionRequest{
		pr:       pr,
		workerID: 99,
	}
	rs.mu.Unlock()

	client := &mockclient.MockClient{}
	client.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("some error")).Maybe()
	rs.SetKubeClient(client)

	prevRequest := RenderExecutionRequest{
		pr:              pr,
		resourceVersion: "1",
		ctx:             ctx,
		retryNum:        0,
		scheduledTime:   time.Now(),
	}

	// when
	err := rs.rescheduleRender(ctx, prevRequest, 0)

	// then
	assert.Nil(t, err)

	rs.mu.Lock()
	_, scheduled := rs.scheduled[key]
	rs.mu.Unlock()
	assert.False(t, scheduled)
}

func TestRescheduleRender_PreservesPackageRevision(t *testing.T) {
	// given
	ctx := context.Background()
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	rs.SetRuntime(noopTestFunctionRuntime{})

	key := testPackageRevisionKey("test-repo", "test-pkg", "v1")
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", map[string]string{"Kptfile": minimalKptfile()})

	client := &mockclient.MockClient{}
	client.On("Get", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		prr := args.Get(2).(*porchapi.PackageRevisionResources)
		prr.ObjectMeta = metav1.ObjectMeta{
			Name:            key.K8SName(),
			Namespace:       key.K8SNS(),
			ResourceVersion: "2",
		}
		prr.Spec.Resources = map[string]string{"Kptfile": minimalKptfile()}
	}).Return(nil).Once()
	rs.SetKubeClient(client)

	prevRequest := RenderExecutionRequest{
		pr:              pr,
		resourceVersion: "1",
		ctx:             ctx,
		retryNum:        0,
		scheduledTime:   time.Now().Add(-5 * time.Second),
	}

	// when
	err := rs.rescheduleRender(ctx, prevRequest, 0)

	// then
	require.NoError(t, err)

	rs.mu.Lock()
	rescheduledReq, exists := rs.scheduled[key]
	queueLen := len(rs.workQueue)
	rs.mu.Unlock()

	assert.True(t, exists, "Request should be rescheduled")
	assert.Equal(t, 1, queueLen, "Work queue should have one item")
	assert.Equal(t, key, rescheduledReq.pr.Key())
	assert.False(t, rescheduledReq.scheduledTime.IsZero())
	assert.Equal(t, 1, rescheduledReq.retryNum)
	assert.Equal(t, "2", rescheduledReq.resourceVersion)

	client.AssertExpectations(t)
}

func TestScheduleRenderAdmitsUpToMaxConcurrentLargeRenders(t *testing.T) {
	resources := newSleepFnResources()
	store := map[string]map[string]string{}
	rs := newTestRenderScheduler(t, store, WithWorkQueueSize(10), WithLargePackageThreshold(testLargePackageThreshold), WithMaxConcurrentLargeRenders(2))

	_ = scheduleSizedRender(t, rs, store, "package-1", "1", testLargePackageThreshold, resources)
	_ = scheduleSizedRender(t, rs, store, "package-2", "1", testLargePackageThreshold, resources)
	_ = scheduleSizedRender(t, rs, store, "package-3", "1", testLargePackageThreshold, resources)

	workQueueLen, waitlistLen, largeOngoing := renderQueueSnapshot(rs)
	assert.Equal(t, 2, workQueueLen)
	assert.Equal(t, 1, waitlistLen)
	assert.Equal(t, 2, largeOngoing)

	rs.StopProcessing()
}

func TestScheduleRenderPackageUnderThresholdUsesWorkQueue(t *testing.T) {
	resources := newSleepFnResources()
	store := map[string]map[string]string{}
	rs := newTestRenderScheduler(t, store, WithWorkQueueSize(10), WithLargePackageThreshold(testLargePackageThreshold))

	_ = scheduleSizedRender(t, rs, store, "package-1", "1", testLargePackageThreshold-1, resources)
	_ = scheduleSizedRender(t, rs, store, "package-2", "1", testLargePackageThreshold-1, resources)

	workQueueLen, waitlistLen, largeOngoing := renderQueueSnapshot(rs)
	assert.Equal(t, 2, workQueueLen)
	assert.Equal(t, 0, waitlistLen)
	assert.Equal(t, 0, largeOngoing)
}

func TestScheduleRenderReplacesWaitlistedPackage(t *testing.T) {
	resources := newSleepFnResources()
	store := map[string]map[string]string{}
	rs := newTestRenderScheduler(t, store, WithWorkQueueSize(10), WithLargePackageThreshold(testLargePackageThreshold))
	_ = scheduleSizedRender(t, rs, store, "package-1", "1", testLargePackageThreshold, resources)
	_ = scheduleSizedRender(t, rs, store, "package-2", "2", testLargePackageThreshold, resources)

	workQueueLen, waitlistLen, _ := renderQueueSnapshot(rs)
	require.Equal(t, 1, workQueueLen)
	require.Equal(t, 1, waitlistLen)

	_ = scheduleSizedRender(t, rs, store, "package-2", "3", testLargePackageThreshold, resources)

	workQueueLen, waitlistLen, _ = renderQueueSnapshot(rs)
	assert.Equal(t, 1, workQueueLen)
	assert.Equal(t, 1, waitlistLen)

	rs.mu.Lock()
	require.Len(t, rs.largeWaitlist, 1)
	assert.Equal(t, "3", rs.largeWaitlist[0].resourceVersion)
	rs.mu.Unlock()
}

func TestScheduleRenderSkipsCancelledWaitlistOnPromote(t *testing.T) {
	resources := newSleepFnResources()
	store := map[string]map[string]string{}
	rs := newTestRenderScheduler(t, store, WithWorkQueueSize(10), WithLargePackageThreshold(testLargePackageThreshold))
	pr1 := scheduleSizedRender(t, rs, store, "package-1", "1", testLargePackageThreshold, resources)

	key2 := testPackageRevisionKey("repo", "package-2", "v1")
	pr2 := newSizedPackageRevision(key2, "2", resources, testLargePackageThreshold)
	store[key2.K8SName()] = maps.Clone(resources)
	repo2 := &mockrepo.MockRepository{}
	draft2 := &mockrepo.MockPackageRevisionDraft{}
	setupMockPRDraftExpectations(draft2, key2, resources["Kptfile"])
	setupMockRepoForPR(repo2, draft2, pr2)
	_, err := rs.ScheduleRender(t.Context(), repo2, draft2, false)
	require.NoError(t, err)

	_, waitlistLen, _ := renderQueueSnapshot(rs)
	require.Equal(t, 1, waitlistLen)

	skipDraft := &mockrepo.MockPackageRevisionDraft{}
	setupMockPRDraftExpectations(skipDraft, key2, resources["Kptfile"])
	setupMockRepoForPR(repo2, skipDraft, pr2)

	_, err = rs.ScheduleRender(t.Context(), repo2, skipDraft, true)
	require.NoError(t, err)

	workQueueLen, waitlistLen, _ := renderQueueSnapshot(rs)
	assert.Equal(t, 1, workQueueLen)
	assert.Equal(t, 0, waitlistLen)

	require.NoError(t, rs.StartProcessing(1))
	waitUntilScheduledRenderFinishes(t, t.Context(), rs, pr1, store)

	workQueueLen, waitlistLen, largeOngoing := renderQueueSnapshot(rs)
	assert.Equal(t, 0, workQueueLen)
	assert.Equal(t, 0, waitlistLen)
	assert.Equal(t, 0, largeOngoing)

	rs.StopProcessing()
}

func TestScheduleRenderSmallPackageProceedsWhileLargeOngoing(t *testing.T) {
	resources := newSleepFnResources()
	store := map[string]map[string]string{}
	rs := newTestRenderScheduler(t, store, WithWorkQueueSize(10), WithLargePackageThreshold(testLargePackageThreshold))

	_ = scheduleSizedRender(t, rs, store, "package-large", "1", testLargePackageThreshold, resources)
	_ = scheduleSizedRender(t, rs, store, "package-small", "1", testLargePackageThreshold-1, resources)

	workQueueLen, waitlistLen, largeOngoing := renderQueueSnapshot(rs)
	assert.Equal(t, 2, workQueueLen)
	assert.Equal(t, 0, waitlistLen)
	assert.Equal(t, 1, largeOngoing)

	rs.StopProcessing()
}

func TestScheduleRenderUnknownSizeUsesWorkQueue(t *testing.T) {
	resources := newSleepFnResources()
	rs := NewRenderScheduler(WithWorkQueueSize(10), WithLargePackageThreshold(testLargePackageThreshold))
	ctx := t.Context()

	scheduleUnknownSize := func(packageName string) {
		t.Helper()
		key := testPackageRevisionKey("repo", packageName, "v1")
		pr := new(mockrepo.MockPackageRevision)
		repo := &mockrepo.MockRepository{}
		draft := &mockrepo.MockPackageRevisionDraft{}
		setupMockPRExpectations(pr, key, "1", resources)
		setupMockPRDraftExpectations(draft, key, resources["Kptfile"])
		setupMockRepoExpectations(repo, draft, pr)
		_, err := rs.ScheduleRender(ctx, repo, draft, false)
		require.NoError(t, err)
	}

	scheduleUnknownSize("package-1")
	scheduleUnknownSize("package-2")

	workQueueLen, waitlistLen, largeOngoing := renderQueueSnapshot(rs)
	assert.Equal(t, 2, workQueueLen)
	assert.Equal(t, 0, waitlistLen)
	assert.Equal(t, 0, largeOngoing)
}

func TestScheduleRenderWaitlistsSecondLargePackage(t *testing.T) {
	resources := newSleepFnResources()
	store := map[string]map[string]string{}
	rs := newTestRenderScheduler(t, store, WithWorkQueueSize(10), WithLargePackageThreshold(testLargePackageThreshold))

	pr1 := scheduleSizedRender(t, rs, store, "package-1", "1", testLargePackageThreshold, resources)
	pr2 := scheduleSizedRender(t, rs, store, "package-2", "1", testLargePackageThreshold, resources)

	workQueueLen, waitlistLen, largeOngoing := renderQueueSnapshot(rs)
	assert.Equal(t, 1, workQueueLen)
	assert.Equal(t, 1, waitlistLen)
	assert.Equal(t, 1, largeOngoing)

	require.NoError(t, rs.StartProcessing(1))
	waitUntilScheduledRenderFinishes(t, t.Context(), rs, pr1, store)
	waitUntilScheduledRenderFinishes(t, t.Context(), rs, pr2, store)

	workQueueLen, waitlistLen, largeOngoing = renderQueueSnapshot(rs)
	assert.Equal(t, 0, workQueueLen)
	assert.Equal(t, 0, waitlistLen)
	assert.Equal(t, 0, largeOngoing)

	rs.StopProcessing()
}

func TestScheduleRender_FullQueue(t *testing.T) {
	// given
	ctx := context.Background()
	resources := newSleepFnResources()

	queueSize := 1
	repo := &mockrepo.MockRepository{}
	resourceStore := map[string]map[string]string{}
	mockClient := setupMockClientForResourceStore(resourceStore)
	rs := NewRenderScheduler(WithWorkQueueSize(queueSize))
	rs.kubeClient = mockClient
	rs.SetRuntime(noopTestFunctionRuntime{})

	pr1 := new(mockrepo.MockPackageRevision)
	key1 := testPackageRevisionKey("repo", "package-1", "v1")
	setupMockPRExpectations(pr1, key1, "1", resources)
	resourceStore[key1.K8SName()] = maps.Clone(resources)

	prKey1 := pr1.Key()
	draft1 := &mockrepo.MockPackageRevisionDraft{}
	setupMockPRDraftExpectations(draft1, key1, resources["Kptfile"])
	setupMockRepoExpectations(repo, draft1, pr1)

	// when - first render fills the queue
	pkgRev1, err := rs.ScheduleRender(ctx, repo, draft1, false)
	require.NoError(t, err)

	rs.mu.Lock()
	queueLen := len(rs.workQueue)
	rs.mu.Unlock()
	assert.Equal(t, 1, queueLen)

	pr2 := new(mockrepo.MockPackageRevision)
	key2 := testPackageRevisionKey("repo", "package-2", "v1")
	setupMockPRExpectations(pr2, key2, "2", resources)

	prKey2 := pr2.Key()
	draft2 := &mockrepo.MockPackageRevisionDraft{}
	draft2.On("GetKptfileContent", mock.Anything).Return(resources["Kptfile"], nil)
	draft2.On("Key").Return(key2)
	repo.On("UpdatePackageRevision", mock.Anything, pr2).Return(draft2, nil).Maybe()
	repo.On("ClosePackageRevisionDraftNoResources", mock.Anything, draft2, 0).Return(pr2, nil).Maybe()

	var queueFullKptfile string
	draft2.On("UpdateKptfileContent", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		queueFullKptfile = args.Get(1).(string)
	}).Return(nil)

	pkgRev2, err := rs.ScheduleRender(ctx, repo, draft2, false)

	// then - second render fails because queue is full
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "work queue is full")

	rs.mu.Lock()
	_, scheduled1 := rs.scheduled[prKey1]
	_, scheduled2 := rs.scheduled[prKey2]
	finalQueueLen := len(rs.workQueue)
	rs.mu.Unlock()

	assert.True(t, scheduled1, "PR 1 is not scheduled")
	assert.False(t, scheduled2, "PR 2 is scheduled")
	assert.Equal(t, 1, finalQueueLen)

	require.NotEmpty(t, queueFullKptfile)
	renderFinished, rendered := kptfileConditionsFromResources(t, map[string]string{"Kptfile": queueFullKptfile})
	assert.Equal(t, kptfilev1.ConditionTrue, renderFinished)
	assert.Equal(t, kptfilev1.ConditionFalse, rendered)

	err = rs.StartProcessing(1)
	require.NoError(t, err)

	waitUntilScheduledRenderFinishes(t, ctx, rs, pkgRev1, resourceStore)
	waitUntilScheduledRenderFinishes(t, ctx, rs, pkgRev2, nil)

	rs.mu.Lock()
	_, stillScheduled := rs.scheduled[prKey1]
	_, stillOngoing := rs.ongoing[prKey1]
	rs.mu.Unlock()

	assert.False(t, stillScheduled)
	assert.False(t, stillOngoing)

	require.Contains(t, resourceStore, key1.K8SName())
	renderFinished, rendered = kptfileConditionsFromResources(t, resourceStore[key1.K8SName()])
	assert.Equal(t, kptfilev1.ConditionTrue, renderFinished)
	assert.Equal(t, kptfilev1.ConditionTrue, rendered)

	rs.StopProcessing()

	pr1.AssertExpectations(t)
	pr2.AssertExpectations(t)
	_ = pkgRev2
}

func TestScheduleRender_GetKptfileError(t *testing.T) {
	ctx := context.Background()
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	repo := &mockrepo.MockRepository{}
	draft := &mockrepo.MockPackageRevisionDraft{}
	key := testPackageRevisionKey("repo", "package", "v1")

	draft.On("Key").Return(key)
	draft.On("GetKptfileContent", mock.Anything).Return("", errors.New("read failed"))

	_, err := rs.ScheduleRender(ctx, repo, draft, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "could not load Kptfile")
}

func TestScheduleRender_KptfileOnlyPath(t *testing.T) {
	// given
	ctx := context.Background()
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	pr := &mockrepo.MockPackageRevision{}
	repo := &mockrepo.MockRepository{}
	draft := &mockrepo.MockPackageRevisionDraft{}
	key := testPackageRevisionKey("repo", "package", "v1")
	resources := newSleepFnResources()

	setupMockPRExpectations(pr, key, "1", resources)
	setupMockPRDraftExpectations(draft, key, resources["Kptfile"])
	setupMockRepoExpectations(repo, draft, pr)
	prKey := pr.Key()

	// when
	_, err := rs.ScheduleRender(ctx, repo, draft, false)

	// then
	assert.NoError(t, err)

	rs.mu.Lock()
	_, scheduled := rs.scheduled[prKey]
	_, ongoing := rs.ongoing[prKey]
	queueLen := len(rs.workQueue)
	rs.mu.Unlock()

	assert.True(t, scheduled)
	assert.False(t, ongoing)
	assert.Equal(t, 1, queueLen)

	pr.AssertExpectations(t)
}

func TestScheduleRender_NoPipelineNeeded(t *testing.T) {
	// given
	ctx := context.Background()
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	pr := &mockrepo.MockPackageRevision{}
	repo := &mockrepo.MockRepository{}
	draft := &mockrepo.MockPackageRevisionDraft{}
	key := testPackageRevisionKey("repo", "package", "v1")
	kptfile := minimalKptfile()

	setupMockPRExpectations(pr, key, "1", map[string]string{"Kptfile": kptfile})
	setupMockPRDraftExpectations(draft, key, kptfile)
	setupMockRepoExpectations(repo, draft, pr)
	prKey := pr.Key()

	// when
	_, err := rs.ScheduleRender(ctx, repo, draft, false)

	// then
	assert.NoError(t, err)

	rs.mu.Lock()
	_, scheduled := rs.scheduled[prKey]
	queueLen := len(rs.workQueue)
	rs.mu.Unlock()

	assert.False(t, scheduled)
	assert.Equal(t, 0, queueLen)
}

func TestScheduleRender_OngoingCancelsAndReschedules(t *testing.T) {
	// given
	ctx, cancelFunc := context.WithCancel(context.Background())

	rs := NewRenderScheduler(WithWorkQueueSize(10))
	pr := new(mockrepo.MockPackageRevision)
	repo := &mockrepo.MockRepository{}
	draft := &mockrepo.MockPackageRevisionDraft{}
	key := testPackageRevisionKey("repo", "package", "v1")
	resources := newSleepFnResources()

	setupMockPRExpectations(pr, key, "1", resources)
	setupMockPRDraftExpectations(draft, key, resources["Kptfile"])
	setupMockRepoExpectations(repo, draft, pr)
	prKey := pr.Key()

	rs.mu.Lock()
	rs.ongoing[prKey] = RenderExecutionRequest{
		pr:       pr,
		ctx:      ctx,
		cancel:   cancelFunc,
		workerID: 1,
	}
	rs.mu.Unlock()

	// when
	_, err := rs.ScheduleRender(ctx, repo, draft, false)

	// then
	assert.NoError(t, err)
	assert.True(t, errors.Is(ctx.Err(), context.Canceled))

	rs.mu.Lock()
	_, scheduled := rs.scheduled[prKey]
	_, ongoing := rs.ongoing[prKey]
	queueLen := len(rs.workQueue)
	rs.mu.Unlock()

	assert.True(t, scheduled)
	assert.False(t, ongoing)
	assert.Equal(t, 1, queueLen)

	pr.AssertExpectations(t)
}

func TestScheduleRender_ScheduledCancelsAndReschedules(t *testing.T) {
	// given
	ctx, cancelFunc := context.WithCancel(context.Background())

	rs := NewRenderScheduler(WithWorkQueueSize(10))
	pr := new(mockrepo.MockPackageRevision)
	repo := &mockrepo.MockRepository{}
	draft := &mockrepo.MockPackageRevisionDraft{}
	key := testPackageRevisionKey("repo", "package", "v1")
	resources := newSleepFnResources()

	setupMockPRExpectations(pr, key, "1", resources)
	setupMockPRDraftExpectations(draft, key, resources["Kptfile"])
	setupMockRepoExpectations(repo, draft, pr)
	prKey := pr.Key()

	rs.mu.Lock()
	rs.scheduled[prKey] = RenderExecutionRequest{
		pr:     pr,
		ctx:    ctx,
		cancel: cancelFunc,
	}
	rs.mu.Unlock()

	// when
	_, err := rs.ScheduleRender(ctx, repo, draft, false)

	// then
	assert.NoError(t, err)
	assert.True(t, errors.Is(ctx.Err(), context.Canceled))

	rs.mu.Lock()
	_, scheduled := rs.scheduled[prKey]
	queueLen := len(rs.workQueue)
	rs.mu.Unlock()

	assert.True(t, scheduled)
	assert.Equal(t, 1, queueLen)
}

func TestScheduleRender_SkipRender(t *testing.T) {
	ctx := context.Background()
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	pr := &mockrepo.MockPackageRevision{}
	repo := &mockrepo.MockRepository{}
	draft := &mockrepo.MockPackageRevisionDraft{}
	key := testPackageRevisionKey("repo", "package", "v1")
	resources := newSleepFnResources()

	setupMockPRExpectations(pr, key, "1", resources)
	setupMockPRDraftExpectations(draft, key, resources["Kptfile"])
	setupMockRepoExpectations(repo, draft, pr)

	_, err := rs.ScheduleRender(ctx, repo, draft, true)
	require.NoError(t, err)

	rs.mu.Lock()
	queueLen := len(rs.workQueue)
	rs.mu.Unlock()
	assert.Equal(t, 0, queueLen)
}

func TestWriteFinalError_UpdatePackageRevisionFails(t *testing.T) {
	ctx := context.Background()
	pr := new(mockrepo.MockPackageRevision)
	repo := &mockrepo.MockRepository{}
	repo.On("UpdatePackageRevision", mock.Anything, pr).Return(nil, errors.New("update failed"))

	_, err := writeFinalError(ctx, repo, pr, "test-pr", minimalKptfile(), errors.New("queue full"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "queue full")
	assert.Contains(t, err.Error(), "update failed")
}

func TestWriteFinalError_InvalidKptfile(t *testing.T) {
	ctx := context.Background()
	pr := new(mockrepo.MockPackageRevision)
	repo := &mockrepo.MockRepository{}

	_, err := writeFinalError(ctx, repo, pr, "test-pr", "not-a-kptfile", errors.New("queue full"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "queue full")
	assert.Contains(t, err.Error(), "failed to read Kptfile")
}

func TestWriteFinalError_UpdateKptfileContentFails(t *testing.T) {
	ctx := context.Background()
	pr := new(mockrepo.MockPackageRevision)
	draft := &mockrepo.MockPackageRevisionDraft{}
	repo := &mockrepo.MockRepository{}
	repo.On("UpdatePackageRevision", mock.Anything, pr).Return(draft, nil)
	draft.On("UpdateKptfileContent", mock.Anything, mock.Anything).Return(errors.New("kptfile write failed"))

	_, err := writeFinalError(ctx, repo, pr, "test-pr", minimalKptfile(), errors.New("queue full"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kptfile write failed")
}

func TestWriteFinalError_CloseDraftFails(t *testing.T) {
	ctx := context.Background()
	pr := new(mockrepo.MockPackageRevision)
	draft := &mockrepo.MockPackageRevisionDraft{}
	repo := &mockrepo.MockRepository{}
	repo.On("UpdatePackageRevision", mock.Anything, pr).Return(draft, nil)
	draft.On("UpdateKptfileContent", mock.Anything, mock.Anything).Return(nil)
	repo.On("ClosePackageRevisionDraftNoResources", mock.Anything, draft, 0).Return(nil, errors.New("close failed"))

	_, err := writeFinalError(ctx, repo, pr, "test-pr", minimalKptfile(), errors.New("queue full"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "close failed")
}

func TestPrepareKptfileRenderConditions_UpdateKptfileFails(t *testing.T) {
	ctx := context.Background()
	repo := &mockrepo.MockRepository{}
	draft := &mockrepo.MockPackageRevisionDraft{}
	key := testPackageRevisionKey("repo", "package", "v1")
	draft.On("Key").Return(key).Maybe()
	draft.On("UpdateKptfileContent", mock.Anything, mock.Anything).Return(errors.New("write failed"))

	_, err := prepareKptfileRenderConditions(ctx, repo, draft, key.K8SName(), newSleepFnResources()["Kptfile"], true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write failed")
}

func TestPrepareKptfileRenderConditions_CloseDraftFails(t *testing.T) {
	ctx := context.Background()
	repo := &mockrepo.MockRepository{}
	draft := &mockrepo.MockPackageRevisionDraft{}
	key := testPackageRevisionKey("repo", "package", "v1")
	draft.On("Key").Return(key).Maybe()
	draft.On("UpdateKptfileContent", mock.Anything, mock.Anything).Return(nil)
	repo.On("ClosePackageRevisionDraftNoResources", mock.Anything, draft, 0).Return(nil, errors.New("close failed"))

	_, err := prepareKptfileRenderConditions(ctx, repo, draft, key.K8SName(), minimalKptfile(), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "close failed")
}
