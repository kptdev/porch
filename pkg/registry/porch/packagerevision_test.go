// Copyright 2022 The kpt Authors
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

package porch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockengine "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/engine"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/endpoints/request"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	packagerevisions = &packageRevisions{
		TableConvertor: packageRevisionTableConvertor,
		packageCommon: packageCommon{
			scheme:         runtime.NewScheme(),
			gr:             porchapi.Resource("packagerevisions"),
			coreClient:     nil,
			updateStrategy: packageRevisionStrategy{},
			createStrategy: packageRevisionStrategy{},
		},
	}
	dummyRepoObject = configapi.Repository{
		TypeMeta: metav1.TypeMeta{
			Kind:       configapi.TypeRepository.Kind,
			APIVersion: configapi.TypeRepository.APIVersion(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "someDummyRepo",
			Namespace: "someDummyNamespace",
		},
		Spec: configapi.RepositorySpec{
			Description: "Repository With Error",
			Type:        configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				// Use `invalid` domain: https://www.rfc-editor.org/rfc/rfc6761#section-6.4
				Repo: "https://repo.invalid/repository.git",
			},
		},
	}
	repositoryName  = "repo"
	pkg             = "1234567890"
	revision        = 1
	workspace       = "ws"
	packageRevision = &fake.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: repositoryName,
				},
				Package: pkg,
			},
			Revision:      revision,
			WorkspaceName: workspace,
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		PackageRevision: &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Labels: make(map[string]string),
			},
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecyclePublished,
			},
		},
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				PackageName:    pkg,
				Revision:       revision,
				RepositoryName: repositoryName,
				Resources: map[string]string{
					kptfilev1.KptFileName: strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: sample description
					`),
				},
			},
		},
	}
)

func setup(t *testing.T) (mockClient *mockclient.MockClient, mockEngine *mockengine.MockCaDEngine) {
	mockClient = mockclient.NewMockClient(t)
	mockEngine = mockengine.NewMockCaDEngine(t)
	// Watches start a goroutine against the shared packagerevisions receiver.
	// Register ObjectCache before publishing the mock so a late watch cannot
	// observe an unstubbed engine.
	mockWatcherManager := mockengine.NewMockWatcherManager(t)
	mockEngine.On("ObjectCache").Return(mockWatcherManager).Maybe()
	mockWatcherManager.On("WatchPackageRevisions", mock.Anything, mock.Anything, mock.Anything).
		Return(fmt.Errorf("watch not configured for this test")).Maybe()
	packagerevisions.coreClient = mockClient
	packagerevisions.cad = mockEngine
	mockClient.On("List", mock.Anything, mock.Anything, mock.Anything).Return(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
		list.(*configapi.RepositoryList).Items = []configapi.Repository{
			dummyRepoObject,
		}
		return nil
	}).Maybe()

	return
}

// filterCalls removes expected calls for a given method name, used to reset mock expectations.
func filterCalls(calls []*mock.Call, method string) []*mock.Call {
	var filtered []*mock.Call
	for _, c := range calls {
		if c.Method != method {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func TestList(t *testing.T) {
	mockClient, mockEngine := setup(t)
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).Return(nil).Maybe()
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		packageRevision,
	}, nil).Once()

	result, err := packagerevisions.List(context.TODO(), &internalversion.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, len(result.(*porchapi.PackageRevisionList).Items))

	//=========================================================================================

	result, err = packagerevisions.List(context.TODO(), &internalversion.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("something.crazy", "somethingOffTheWall"),
	})
	assert.Equal(t, nil, result)
	assert.ErrorContains(t, err, "unknown fieldSelector field")

	//=========================================================================================

	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		mockPkgRev,
	}, nil)
	mockPkgRev.On("Key").Return(repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "repo"}},
	}).Maybe()
	mockPkgRev.On("KubeObjectNamespace").Return("").Maybe()
	mockPkgRev.On("KubeObjectName").Return("test-package").Maybe()
	mockPkgRev.On("GetPackageRevision", mock.Anything).Return(nil, errors.New("error getting API package revision")).Once()
	result, err = packagerevisions.List(context.TODO(), &internalversion.ListOptions{})
	require.NoError(t, err)
	resultList, isList := result.(*porchapi.PackageRevisionList)
	assert.True(t, isList)
	assert.Equal(t, 0, len(resultList.Items))
}

func TestGet(t *testing.T) {
	mockClient, mockEngine := setup(t)
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).Return(nil).Maybe()
	pkgRevName := "repo.1234567890.ws"

	// Success case
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		packageRevision,
	}, nil).Once()

	ctx := request.WithNamespace(context.TODO(), "someDummyNamespace")
	result, err := packagerevisions.Get(ctx, pkgRevName, &metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.IsType(t, &porchapi.PackageRevision{}, result)

	//=========================================================================================

	// Not found case
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{}, nil).Once()

	result, err = packagerevisions.Get(ctx, pkgRevName, &metav1.GetOptions{})
	assert.Nil(t, result)
	assert.True(t, apierrors.IsNotFound(err))

	//=========================================================================================

	// Error from GetPackageRevision
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		mockPkgRev,
	}, nil).Once()
	mockPkgRev.On("KubeObjectName").Return(pkgRevName)
	mockPkgRev.On("GetPackageRevision", mock.Anything).Return(nil, errors.New("error getting package revision"))

	result, err = packagerevisions.Get(ctx, pkgRevName, &metav1.GetOptions{})
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestCreate(t *testing.T) {
	mockClient, mockEngine := setup(t)
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).Return(nil).Maybe()
	ctx := request.WithNamespace(context.TODO(), "someDummyNamespace")

	// Success case - Init task
	newPkgRev := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: "repo",
			PackageName:    "test-package",
			WorkspaceName:  "v1",
			Tasks: []porchapi.Task{
				{Type: porchapi.TaskTypeInit},
			},
		},
	}

	mockEngine.On("CreatePackageRevision", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(packageRevision, nil).Once()

	result, err := packagerevisions.Create(ctx, newPkgRev, nil, &metav1.CreateOptions{})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.IsType(t, &porchapi.PackageRevision{}, result)

	//=========================================================================================

	// Missing namespace
	result, err = packagerevisions.Create(context.TODO(), newPkgRev, nil, &metav1.CreateOptions{})
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "namespace must be specified")

	//=========================================================================================

	// Wrong object type
	wrongObj := &porchapi.PackageRevisionList{}
	result, err = packagerevisions.Create(ctx, wrongObj, nil, &metav1.CreateOptions{})
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "expected PackageRevision object")

	//=========================================================================================

	// Missing repository name
	invalidPkgRev := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			PackageName:   "test-package",
			WorkspaceName: "v1",
		},
	}
	result, err = packagerevisions.Create(ctx, invalidPkgRev, nil, &metav1.CreateOptions{})
	assert.Nil(t, result)
	assert.ErrorContains(t, err, "spec.repositoryName is required")

	//=========================================================================================

	// Error from CreatePackageRevision
	mockEngine.On("CreatePackageRevision", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("creation failed")).Once()

	result, err = packagerevisions.Create(ctx, newPkgRev, nil, &metav1.CreateOptions{})
	assert.Nil(t, result)
	assert.True(t, apierrors.IsInternalError(err))

	//=========================================================================================

	// v1alpha2 repo returns Forbidden
	// Reset Get mock to return v1alpha2 annotation
	mockClient.ExpectedCalls = filterCalls(mockClient.ExpectedCalls, "Get")
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
		Run(func(args mock.Arguments) {
			repo := args.Get(2).(*configapi.Repository)
			repo.Annotations = map[string]string{configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled}
		}).Return(nil).Maybe()

	result, err = packagerevisions.Create(ctx, newPkgRev, nil, &metav1.CreateOptions{})
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, apierrors.IsResourceExpired(err))
	assert.ErrorContains(t, err, "managed by v1alpha2")
}

func TestDelete(t *testing.T) {
	mockClient, mockEngine := setup(t)
	ctx := request.WithNamespace(context.TODO(), "someDummyNamespace")
	pkgRevName := "repo.1234567890.ws"

	// Success case - Published package in DeletionProposed state
	deletionProposedPkgRev := &fake.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: repositoryName,
				},
				Package: pkg,
			},
			Revision:      revision,
			WorkspaceName: workspace,
		},
		PackageLifecycle: porchapi.PackageRevisionLifecycleDeletionProposed,
		PackageRevision: &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Labels: make(map[string]string),
			},
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleDeletionProposed,
			},
		},
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				PackageName:    pkg,
				Revision:       revision,
				RepositoryName: repositoryName,
				Resources: map[string]string{
					kptfilev1.KptFileName: strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: sample description
					`),
				},
			},
		},
	}

	// Need Get calls for getRepoPkgRev->getRepositoryObj and validateDelete->getRepositoryObj
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).Return(nil).Maybe()
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		deletionProposedPkgRev,
	}, nil).Once()
	mockEngine.On("FindAllUpstreamReferencesInRepositories", mock.Anything, mock.Anything, mock.Anything).Return("", nil).Once()
	mockEngine.On("DeletePackageRevision", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	result, deleted, err := packagerevisions.Delete(ctx, pkgRevName, nil, &metav1.DeleteOptions{})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, deleted)
	assert.IsType(t, &porchapi.PackageRevision{}, result)

	//=========================================================================================

	// Failure case - Published package NOT in DeletionProposed state
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		packageRevision, // This is Published lifecycle
	}, nil).Once()

	result, deleted, err = packagerevisions.Delete(ctx, pkgRevName, nil, &metav1.DeleteOptions{})
	assert.Nil(t, result)
	assert.False(t, deleted)
	assert.True(t, apierrors.IsForbidden(err))
	assert.ErrorContains(t, err, "published PackageRevisions must be proposed for deletion")

	//=========================================================================================

	// Success case - Draft package can be deleted without DeletionProposed
	draftPkgRev := &fake.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: repositoryName,
				},
				Package: pkg,
			},
			Revision:      revision,
			WorkspaceName: workspace,
		},
		PackageLifecycle: porchapi.PackageRevisionLifecycleDraft,
		PackageRevision: &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Labels: make(map[string]string),
			},
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleDraft,
			},
		},
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				PackageName:    pkg,
				Revision:       revision,
				RepositoryName: repositoryName,
				Resources: map[string]string{
					kptfilev1.KptFileName: strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: sample description
					`),
				},
			},
		},
	}

	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		draftPkgRev,
	}, nil).Once()
	mockEngine.On("FindAllUpstreamReferencesInRepositories", mock.Anything, mock.Anything, mock.Anything).Return("", nil).Once()
	mockEngine.On("DeletePackageRevision", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	result, deleted, err = packagerevisions.Delete(ctx, pkgRevName, nil, &metav1.DeleteOptions{})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, deleted)
	assert.IsType(t, &porchapi.PackageRevision{}, result)

	//=========================================================================================

	// Missing namespace
	result, deleted, err = packagerevisions.Delete(context.TODO(), pkgRevName, nil, &metav1.DeleteOptions{})
	assert.Nil(t, result)
	assert.False(t, deleted)
	assert.ErrorContains(t, err, "namespace must be specified")

	//=========================================================================================

	// Package not found
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{}, nil).Once()

	result, deleted, err = packagerevisions.Delete(ctx, pkgRevName, nil, &metav1.DeleteOptions{})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.False(t, deleted)
	assert.True(t, apierrors.IsNotFound(err))

	//=========================================================================================

	// Error from DeletePackageRevision
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		deletionProposedPkgRev,
	}, nil).Once()
	mockEngine.On("FindAllUpstreamReferencesInRepositories", mock.Anything, mock.Anything, mock.Anything).Return("", nil).Once()
	mockEngine.On("DeletePackageRevision", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("deletion failed")).Once()

	result, deleted, err = packagerevisions.Delete(ctx, pkgRevName, nil, &metav1.DeleteOptions{})
	assert.Nil(t, result)
	assert.False(t, deleted)
	assert.True(t, apierrors.IsInternalError(err))
}

func TestWatch(t *testing.T) {
	_, mockEngine := setup(t)
	mockWatcherManager := mockengine.NewMockWatcherManager(t)
	mockEngine.On("ObjectCache").Return(mockWatcherManager).Maybe()
	mockWatcherManager.On("WatchPackageRevisions", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("error starting watch")).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := packagerevisions.Watch(ctx, &internalversion.ListOptions{})
	require.NoError(t, err)
	w.Stop()
	for range w.ResultChan() {
		// Drain until the watch goroutine exits and closes the channel.
	}

	//=========================================================================================

	result, err := packagerevisions.Watch(context.TODO(), &internalversion.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("something.crazy", "somethingOffTheWall"),
	})
	assert.Equal(t, nil, result)
	assert.ErrorContains(t, err, "unknown fieldSelector field")

	//=========================================================================================

	result, err = packagerevisions.Watch(request.WithNamespace(context.TODO(), "foo"), &internalversion.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.namespace", "somethingOffTheWall"),
	})
	assert.Equal(t, nil, result)
	assert.ErrorContains(t, err, "conflicting namespaces specified:")
}

func TestUpdateStrategyForLifecycle(t *testing.T) {
	type testCase struct {
		old     porchapi.PackageRevisionLifecycle
		valid   []porchapi.PackageRevisionLifecycle
		invalid []porchapi.PackageRevisionLifecycle
	}

	s := packageRevisionStrategy{}

	for _, tc := range []testCase{
		{
			old:     "",
			valid:   []porchapi.PackageRevisionLifecycle{"", porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed},
			invalid: []porchapi.PackageRevisionLifecycle{"Wrong", porchapi.PackageRevisionLifecyclePublished},
		},
		{
			old:     porchapi.PackageRevisionLifecycleDraft,
			valid:   []porchapi.PackageRevisionLifecycle{"", porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed},
			invalid: []porchapi.PackageRevisionLifecycle{"Wrong", porchapi.PackageRevisionLifecyclePublished},
		},
		{
			old:     porchapi.PackageRevisionLifecycleProposed,
			valid:   []porchapi.PackageRevisionLifecycle{"", porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed},
			invalid: []porchapi.PackageRevisionLifecycle{"Wrong", porchapi.PackageRevisionLifecyclePublished},
		},
		{
			old:     porchapi.PackageRevisionLifecyclePublished,
			valid:   []porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecyclePublished},
			invalid: []porchapi.PackageRevisionLifecycle{"", "Wrong", porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed},
		},
		{
			old:     "Wrong",
			valid:   []porchapi.PackageRevisionLifecycle{},
			invalid: []porchapi.PackageRevisionLifecycle{"", "Wrong", porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed, porchapi.PackageRevisionLifecyclePublished},
		},
	} {
		for _, new := range tc.valid {
			testValidateUpdate(t, s, tc.old, new, true)
		}
		for _, new := range tc.invalid {
			testValidateUpdate(t, s, tc.old, new, false)
		}
	}
}

func TestUpdateStrategy(t *testing.T) {
	s := packageRevisionStrategy{}

	testCases := map[string]struct {
		old   *porchapi.PackageRevision
		new   *porchapi.PackageRevision
		valid bool
	}{
		"spec can be updated for draft": {
			old: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleDraft,
					Tasks: []porchapi.Task{
						{
							Type: porchapi.TaskTypeInit,
							Init: &porchapi.PackageInitTaskSpec{
								Description: "This is a test",
							},
						},
					},
				},
			},
			new: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleDraft,
					Tasks: []porchapi.Task{
						{
							Type: porchapi.TaskTypeInit,
							Init: &porchapi.PackageInitTaskSpec{
								Description: "This is a test",
							},
						},
					},
				},
			},
			valid: true,
		},
		"spec can not be updated for published": {
			old: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
					Tasks: []porchapi.Task{
						{
							Type: porchapi.TaskTypeInit,
							Init: &porchapi.PackageInitTaskSpec{
								Description: "This is a test",
							},
						},
					},
				},
			},
			new: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
					Tasks: []porchapi.Task{
						{
							Type: porchapi.TaskTypeClone,
							Clone: &porchapi.PackageCloneTaskSpec{
								Upstream: porchapi.UpstreamPackage{
									Type: porchapi.RepositoryTypeGit,
									Git: &porchapi.GitPackage{
										Repo:      "https://github.com/example/repo.git",
										Ref:       "main",
										Directory: "/path/to/package",
									},
								},
							},
						},
					},
				},
			},
			valid: false,
		},
		"labels can be updated for published": {
			old: &porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"foo": "bar",
					},
				},
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				},
			},
			new: &porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"bar": "foo",
					},
				},
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				},
			},
			valid: true,
		},
		"annotations can be updated for published": {
			old: &porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"foo": "bar",
					},
				},
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				},
			},
			new: &porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"bar": "foo",
					},
				},
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				},
			},
			valid: true,
		},
		"cannot modify latest-revision label": {
			old: &porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						porchapi.LatestPackageRevisionKey: "true",
					},
				},
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleDraft,
				},
			},
			new: &porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						porchapi.LatestPackageRevisionKey: "false",
					},
				},
				Spec: porchapi.PackageRevisionSpec{
					Lifecycle: porchapi.PackageRevisionLifecycleDraft,
				},
			},
			valid: false,
		},
	}

	for tn := range testCases {
		tc := testCases[tn]
		t.Run(tn, func(t *testing.T) {
			ctx := context.Background()
			allErrs := s.ValidateUpdate(ctx, tc.new, tc.old)

			if tc.valid {
				if len(allErrs) > 0 {
					t.Errorf("Update failed unexpectedly: %v", allErrs.ToAggregate().Error())
				}
			} else {
				if len(allErrs) == 0 {
					t.Error("Update should fail but didn't")
				}
			}
		})
	}
}
func TestCreateAction(t *testing.T) {
	testCases := map[string]struct {
		pkgRev   *porchapi.PackageRevision
		expected string
	}{
		"nil package revision": {
			pkgRev:   nil,
			expected: "Create",
		},
		"nil tasks": {
			pkgRev:   &porchapi.PackageRevision{},
			expected: "Create",
		},
		"init task": {
			pkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Tasks: []porchapi.Task{
						{Type: porchapi.TaskTypeInit},
					},
				},
			},
			expected: "Init",
		},
		"clone task": {
			pkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Tasks: []porchapi.Task{
						{Type: porchapi.TaskTypeClone},
					},
				},
			},
			expected: "Clone",
		},
		"upgrade task": {
			pkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Tasks: []porchapi.Task{
						{Type: porchapi.TaskTypeUpgrade},
					},
				},
			},
			expected: "Upgrade",
		},
		"edit task with source": {
			pkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Tasks: []porchapi.Task{
						{
							Type: porchapi.TaskTypeEdit,
							Edit: &porchapi.PackageEditTaskSpec{
								Source: &porchapi.PackageRevisionRef{},
							},
						},
					},
				},
			},
			expected: "Copy",
		},
		"multiple different task types": {
			pkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Tasks: []porchapi.Task{
						{Type: porchapi.TaskTypeInit},
						{Type: porchapi.TaskTypeClone},
					},
				},
			},
			expected: "Create",
		},
		"empty tasks slice": {
			pkgRev: &porchapi.PackageRevision{
				Spec: porchapi.PackageRevisionSpec{
					Tasks: []porchapi.Task{},
				},
			},
			expected: "Create",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			result := createAction(tc.pkgRev)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestCheckIfUpstreamIsReferenced(t *testing.T) {
	_, mockEngine := setup(t)

	testCases := map[string]struct {
		downstream string
		engineErr  error
		expectErr  bool
	}{
		"no downstream": {
			downstream: "",
			engineErr:  nil,
			expectErr:  false,
		},
		"has downstream": {
			downstream: "downstream-pkg",
			engineErr:  nil,
			expectErr:  true,
		},
		"engine error": {
			downstream: "",
			engineErr:  errors.New("engine failure"),
			expectErr:  true,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			mockEngine.On("FindAllUpstreamReferencesInRepositories", mock.Anything, mock.Anything, mock.Anything).Return(tc.downstream, tc.engineErr).Once()

			apiPkgRev := &porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pkg"},
			}
			ctx := request.WithNamespace(context.Background(), "test-ns")

			err := packagerevisions.checkIfUpstreamIsReferenced(ctx, apiPkgRev)

			if tc.expectErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestUpdate(t *testing.T) {
	mockClient, mockEngine := setup(t)
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).Return(nil).Maybe()
	mockWatcherManager := mockengine.NewMockWatcherManager(t)
	mockEngine.On("ObjectCache").Return(mockWatcherManager).Maybe()
	mockWatcherManager.On("WatchPackageRevisions", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	ctx := request.WithNamespace(context.TODO(), "someDummyNamespace")
	pkgRevName := "repo.1234567890.ws"

	// Create a draft package revision for testing
	draftPackageRevision := &fake.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: repositoryName,
				},
				Package: pkg,
			},
			Revision:      revision,
			WorkspaceName: workspace,
		},
		PackageLifecycle: porchapi.PackageRevisionLifecycleDraft,
		PackageRevision: &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Labels:          make(map[string]string),
				ResourceVersion: "123",
			},
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleDraft,
			},
		},
	}

	// Success case
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		draftPackageRevision,
	}, nil).Once()
	mockEngine.On("UpdatePackageRevision", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(draftPackageRevision, nil).Once()

	objInfo := &mockUpdatedObjectInfo{
		updatedObj: &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				ResourceVersion: "123",
			},
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleProposed,
			},
		},
	}

	result, created, err := packagerevisions.Update(ctx, pkgRevName, objInfo, nil, nil, false, &metav1.UpdateOptions{})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, created)
	assert.IsType(t, &porchapi.PackageRevision{}, result)

	//=========================================================================================

	// Error case 1- generic updatePackageRevision fails
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		draftPackageRevision,
	}, nil).Once()
	mockEngine.On("UpdatePackageRevision", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("update failed")).Once()

	result, created, err = packagerevisions.Update(ctx, pkgRevName, objInfo, nil, nil, false, &metav1.UpdateOptions{})
	assert.Nil(t, result)
	assert.False(t, created)
	assert.True(t, apierrors.IsInternalError(err))
	assert.ErrorContains(t, err, "update failed")

	// Error case 2 - resource version mismatch updatePackageRevision failure

	objInfo = &mockUpdatedObjectInfo{
		updatedObj: &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				ResourceVersion: "321",
			},
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleProposed,
			},
		},
	}
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		draftPackageRevision,
	}, nil).Once()
	mockEngine.On("UpdatePackageRevision", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, apierrors.NewConflict(porchapi.Resource("packagerevisions"), pkgRevName, fmt.Errorf("the object has been modified; please apply your changes to the latest version and try again"))).Once()

	result, created, err = packagerevisions.Update(ctx, pkgRevName, objInfo, nil, nil, false, &metav1.UpdateOptions{})
	assert.Nil(t, result)
	assert.False(t, created)
	assert.True(t, apierrors.IsInternalError(err))
	assert.ErrorContains(t, err, "the object has been modified; please apply your changes to the latest version and try again")

}

// mockUpdatedObjectInfo is a mock implementation of rest.UpdatedObjectInfo
type mockUpdatedObjectInfo struct {
	updatedObj runtime.Object
}

func (m *mockUpdatedObjectInfo) UpdatedObject(ctx context.Context, oldObj runtime.Object) (runtime.Object, error) {
	return m.updatedObj, nil
}

func (m *mockUpdatedObjectInfo) Preconditions() *metav1.Preconditions {
	return nil
}
