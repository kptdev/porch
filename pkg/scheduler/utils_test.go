package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	kptfileapi "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

func TestLargePackageThresholdKeepsDefaultForNonPositive(t *testing.T) {
	rs := NewRenderScheduler(WithLargePackageThreshold(-1))

	assert.Equal(t, defaultLargePackageBytes, rs.largePackageBytes)

	rs = NewRenderScheduler(WithLargePackageThreshold(testLargePackageThreshold))

	assert.Equal(t, testLargePackageThreshold, rs.largePackageBytes)
}

func TestMaxConcurrentLargeRendersKeepsDefaultForNonPositive(t *testing.T) {
	rs := NewRenderScheduler(WithMaxConcurrentLargeRenders(-1))

	assert.Equal(t, defaultMaxConcurrentRenders, rs.maxConcurrentLargeRenders)

	rs = NewRenderScheduler(WithMaxConcurrentLargeRenders(3))

	assert.Equal(t, 3, rs.maxConcurrentLargeRenders)
}

func TestNeedRender(t *testing.T) {
	rs := NewRenderScheduler(WithWorkQueueSize(1))

	testCases := map[string]struct {
		kptfile  string
		expected bool
	}{
		"empty pipeline": {
			kptfile:  minimalKptfile(),
			expected: false,
		},
		"with mutators": {
			kptfile:  newSleepFnResources()["Kptfile"],
			expected: true,
		},
		"with validators only": {
			kptfile: `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test
pipeline:
  validators:
  - image: ghcr.io/example/validator:latest
`,
			expected: true,
		},
		"invalid kptfile defaults to render": {
			kptfile:  "not-a-kptfile",
			expected: true,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, rs.NeedRender(tc.kptfile))
		})
	}
}

func TestNeedRender_InvalidPipelineSection(t *testing.T) {
	rs := NewRenderScheduler(WithWorkQueueSize(1))
	kptfile := `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test
pipeline: not-a-map
`
	assert.True(t, rs.NeedRender(kptfile))
}

func TestRelativeResourcePath(t *testing.T) {
	assert.Equal(t, "subdir/file.yaml", relativeResourcePath("/tmp/pkg", "/tmp/pkg/subdir/file.yaml"))
	assert.Equal(t, "file.yaml", relativeResourcePath("/", "/file.yaml"))
}

func TestRenderExecutionStatus(t *testing.T) {
	// given
	rs := NewRenderScheduler(WithWorkQueueSize(10))
	pr := new(mockrepo.MockPackageRevision)
	key := testPackageRevisionKey("repo", "package", "v1")

	setupMockPRExpectations(pr, key, "1", newSleepFnResources())
	prKey := pr.Key()

	// then - unknown by default
	assert.Equal(t, RenderStatusUnknown, rs.RenderExecutionStatus(pr))

	// when - scheduled
	rs.mu.Lock()
	rs.scheduled[prKey] = RenderExecutionRequest{
		pr: pr,
	}
	rs.mu.Unlock()
	assert.Equal(t, RenderStatusScheduled, rs.RenderExecutionStatus(pr))

	// when - ongoing
	rs.mu.Lock()
	delete(rs.scheduled, prKey)
	rs.ongoing[prKey] = RenderExecutionRequest{
		pr:       pr,
		workerID: 1,
	}
	rs.mu.Unlock()
	assert.Equal(t, RenderStatusOngoing, rs.RenderExecutionStatus(pr))

	// then - back to unknown
	rs.mu.Lock()
	delete(rs.ongoing, prKey)
	rs.mu.Unlock()
	assert.Equal(t, RenderStatusUnknown, rs.RenderExecutionStatus(pr))
}

func TestResourcesHash(t *testing.T) {
	// given
	resourcesA := map[string]string{"a": "1", "b": "2"}
	resourcesB := map[string]string{"b": "2", "a": "1"}
	resourcesC := map[string]string{"a": "1", "b": "3"}

	// then
	assert.Equal(t, computeResourceHash(resourcesA), computeResourceHash(resourcesB))
	assert.NotEqual(t, computeResourceHash(resourcesA), computeResourceHash(resourcesC))
}

func TestSetRunnerOptions(t *testing.T) {
	rs := NewRenderScheduler(WithWorkQueueSize(1))
	rs.SetRunnerOptions(func(namespace string) runneroptions.RunnerOptions {
		return runneroptions.RunnerOptions{ImagePrefix: "custom-prefix"}
	})
	assert.Equal(t, "custom-prefix", rs.runnerOptions.ImagePrefix)
}

func TestWorkQueueSizeKeepsDefaultForNonPositive(t *testing.T) {
	rs := NewRenderScheduler(WithWorkQueueSize(-1))

	assert.Equal(t, defaultWorkQueueSize, cap(rs.workQueue))

	rs = NewRenderScheduler(WithWorkQueueSize(7))

	assert.Equal(t, 7, cap(rs.workQueue))
}

func TestWrapCancelFunc_LogsAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wrapped := wrapCancelFunc(cancel, "repo.package.v1")
	wrapped()
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestWriteAndReadResources(t *testing.T) {
	// given
	fs := filesys.MakeFsOnDisk()
	root := t.TempDir()
	resources := repository.PackageResources{
		Contents: map[string]string{
			"Kptfile": minimalKptfile(),
			"subdir/resource.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`,
		},
	}

	// when
	pkgPath, err := WriteResources(fs, resources, root)
	require.NoError(t, err)
	require.NotEmpty(t, pkgPath)

	readBack, err := ReadResources(fs, root)

	// then
	require.NoError(t, err)
	assert.Equal(t, resources.Contents, readBack.Contents)
}

func TestWriteResources_NoKptfile(t *testing.T) {
	// given
	fs := filesys.MakeFsOnDisk()
	root := filepath.Join(t.TempDir(), "pkg")

	// when
	pkgPath, err := WriteResources(fs, repository.PackageResources{
		Contents: map[string]string{"only.yaml": "data"},
	}, root)

	// then
	require.NoError(t, err)
	assert.Empty(t, pkgPath)
}

func TestHasKptfile(t *testing.T) {
	assert.False(t, hasKptfile(map[string]string{}))
	assert.False(t, hasKptfile(map[string]string{kptfileapi.KptFileName: ""}))
	assert.True(t, hasKptfile(map[string]string{kptfileapi.KptFileName: minimalKptfile()}))
}

func TestRunnerOptionsFor_SetsPackageNameFormat(t *testing.T) {
	rs := NewRenderScheduler()
	key := testPackageRevisionKey("my-repo", "my-pkg", "ws-1")
	opts := rs.runnerOptionsFor(key)
	assert.Equal(t, "my-repo.%s.ws-1", opts.LogOptions.PkgNameFormat)
}

func TestPackageRevisionSizeBytes(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, int64(0), packageRevisionSizeBytes(ctx, nil))

	pr := new(mockrepo.MockPackageRevision)
	pr.On("GetPackageRevision", mock.Anything, false).Return(nil, errors.New("api down")).Maybe()
	assert.Equal(t, int64(0), packageRevisionSizeBytes(ctx, pr))

	pr2 := new(mockrepo.MockPackageRevision)
	pr2.On("GetPackageRevision", mock.Anything, false).Return((*porchapi.PackageRevision)(nil), nil).Maybe()
	assert.Equal(t, int64(0), packageRevisionSizeBytes(ctx, pr2))

	pr3 := new(mockrepo.MockPackageRevision)
	pr3.On("GetPackageRevision", mock.Anything, false).Return(&porchapi.PackageRevision{
		Status: porchapi.PackageRevisionStatus{ResourcesSizeBytes: 42},
	}, nil).Maybe()
	assert.Equal(t, int64(42), packageRevisionSizeBytes(ctx, pr3))
}

func TestRenderRequestName(t *testing.T) {
	assert.Empty(t, renderRequestName(RenderExecutionRequest{}))
	key := testPackageRevisionKey("repo", "package", "v1")
	pr := new(mockrepo.MockPackageRevision)
	pr.On("Key").Return(key).Maybe()
	assert.Equal(t, key.K8SName(), renderRequestName(RenderExecutionRequest{pr: pr}))
}

func TestPromoteWaitlist_WorkQueueFullRequeues(t *testing.T) {
	rs := NewRenderScheduler(WithWorkQueueSize(1), WithLargePackageThreshold(10))
	key := testPackageRevisionKey("repo", "package", "v1")
	pr := new(mockrepo.MockPackageRevision)
	setupMockPRExpectations(pr, key, "1", newSleepFnResources())

	waitlisted := RenderExecutionRequest{pr: pr, sizeBytes: 10, ctx: context.Background()}

	rs.mu.Lock()
	rs.workQueue <- RenderExecutionRequest{pr: pr, sizeBytes: 10}
	rs.largeRendersOngoing = 1
	rs.largeWaitlist = []RenderExecutionRequest{waitlisted}
	rs.mu.Unlock()

	rs.releaseLargeRender(RenderExecutionRequest{sizeBytes: 10})

	rs.mu.Lock()
	assert.Len(t, rs.largeWaitlist, 1)
	assert.Equal(t, 0, rs.largeRendersOngoing)
	rs.mu.Unlock()
}

func TestRemoveLargeWaitlistedPr(t *testing.T) {
	rs := NewRenderScheduler()
	key1 := testPackageRevisionKey("repo", "pkg-1", "v1")
	key2 := testPackageRevisionKey("repo", "pkg-2", "v1")
	pr1 := new(mockrepo.MockPackageRevision)
	pr1.On("Key").Return(key1).Maybe()
	pr2 := new(mockrepo.MockPackageRevision)
	pr2.On("Key").Return(key2).Maybe()

	rs.mu.Lock()
	rs.largeWaitlist = []RenderExecutionRequest{
		{pr: pr1},
		{pr: pr2},
	}
	rs.removeLargeWaitlistedPr(key1)
	assert.Len(t, rs.largeWaitlist, 1)
	assert.Equal(t, pr2, rs.largeWaitlist[0].pr)
	rs.mu.Unlock()
}
