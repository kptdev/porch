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

package porch

import (
	"context"
	"errors"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	apiporch "github.com/kptdev/porch/api/porch"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockengine "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/engine"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testPRRNamespace       = "someDummyNamespace"
	testPRRName            = "repo.1234567890.ws"
	testPRRResourceVersion = "rv-42"
	testReadmeFile         = "README.md"
	testDeployFile         = "deploy.yaml"
)

func TestUpdateRejectsInvalidPartialQuery(t *testing.T) {
	// given
	setupResourcesTest(t)

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName+"?partial=true&partial=false",
		testUpdatedObjectInfo(map[string]string{kptfilev1.KptFileName: "kptfile"}),
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.True(t, apierrors.IsBadRequest(err))
	require.ErrorContains(t, err, "multiple partial values found")
}

func TestUpdateRequiresNamespace(t *testing.T) {
	// given
	setupResourcesTest(t)

	// when
	result, created, err := packagerevisionresources.Update(
		context.TODO(),
		testPRRName,
		testUpdatedObjectInfo(map[string]string{kptfilev1.KptFileName: "kptfile"}),
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.True(t, apierrors.IsBadRequest(err))
	require.ErrorContains(t, err, "namespace must be specified")
}

func TestUpdateReturnsConflictWhenPackageIsLocked(t *testing.T) {
	// given
	setupResourcesTest(t)
	lockedName := "repo.locked-pkg.ws"
	pkgMutex := getMutexForPackage(getPackageMutexKey(testPRRNamespace, lockedName))
	pkgMutex.Lock()
	defer pkgMutex.Unlock()

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		lockedName,
		testUpdatedObjectInfo(map[string]string{kptfilev1.KptFileName: "kptfile"}),
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.True(t, apierrors.IsConflict(err))
	require.ErrorContains(t, err, "already in progress")
}

func TestUpdateReturnsErrorWhenPackageRevisionNotFound(t *testing.T) {
	// given
	mockClient, mockEngine := setupResourcesTest(t)
	stubRepositoryGet(mockClient, nil)
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{}, nil)

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName,
		testUpdatedObjectInfo(map[string]string{kptfilev1.KptFileName: "kptfile"}),
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.True(t, apierrors.IsNotFound(err))
}

func TestUpdateReturnsErrorWhenGetResourcesFails(t *testing.T) {
	// given
	mockClient, mockEngine := setupResourcesTest(t)
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	stubRepositoryGet(mockClient, nil)
	stubListedPackageRevision(mockEngine, mockPkgRev, testPRRName)
	mockPkgRev.On("GetResources", mock.Anything).Return(nil, errors.New("read failed"))

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName,
		testUpdatedObjectInfo(map[string]string{kptfilev1.KptFileName: "kptfile"}),
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.ErrorContains(t, err, "read failed")
}

func TestUpdateReturnsErrorWhenUpdatedObjectFails(t *testing.T) {
	// given
	mockClient, mockEngine := setupResourcesTest(t)
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	stubRepositoryGet(mockClient, nil)
	stubListedPackageRevision(mockEngine, mockPkgRev, testPRRName)
	mockPkgRev.On("GetResources", mock.Anything).Return(testPRRResources(map[string]string{kptfilev1.KptFileName: "old"}), nil)

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName,
		&mockUpdatedObjectInfo{err: errors.New("cannot build updated object")},
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.ErrorContains(t, err, "cannot build updated object")
}

func TestUpdateReturnsErrorWhenValidationFails(t *testing.T) {
	// given
	mockClient, mockEngine := setupResourcesTest(t)
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	stubRepositoryGet(mockClient, nil)
	stubListedPackageRevision(mockEngine, mockPkgRev, testPRRName)
	mockPkgRev.On("GetResources", mock.Anything).Return(testPRRResources(map[string]string{kptfilev1.KptFileName: "old"}), nil)
	validate := func(context.Context, runtime.Object, runtime.Object) error {
		return errors.New("validation failed")
	}

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName,
		testUpdatedObjectInfo(map[string]string{kptfilev1.KptFileName: "new"}),
		nil, validate, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.ErrorContains(t, err, "validation failed")
}

func TestUpdateReturnsNotFoundWhenRepositoryIsMissing(t *testing.T) {
	// given
	mockClient, mockEngine := setupResourcesTest(t)
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	stubListedPackageRevision(mockEngine, mockPkgRev, testPRRName)
	mockPkgRev.On("GetResources", mock.Anything).Return(testPRRResources(map[string]string{kptfilev1.KptFileName: "old"}), nil)
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
		Return(nil).Once()
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
		Return(apierrors.NewNotFound(configapi.TypeRepository.GroupResource(), "repo")).Once()

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName,
		testUpdatedObjectInfo(map[string]string{kptfilev1.KptFileName: "new"}),
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.True(t, apierrors.IsNotFound(err))
}

func TestUpdateReturnsInternalErrorWhenRepositoryGetFails(t *testing.T) {
	// given
	mockClient, mockEngine := setupResourcesTest(t)
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	stubListedPackageRevision(mockEngine, mockPkgRev, testPRRName)
	mockPkgRev.On("GetResources", mock.Anything).Return(testPRRResources(map[string]string{kptfilev1.KptFileName: "old"}), nil)
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
		Return(nil).Once()
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
		Return(errors.New("etcd unavailable")).Once()

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName,
		testUpdatedObjectInfo(map[string]string{kptfilev1.KptFileName: "new"}),
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.True(t, apierrors.IsInternalError(err))
	require.ErrorContains(t, err, "etcd unavailable")
}

func TestUpdateReturnsInternalErrorWhenEngineUpdateFails(t *testing.T) {
	// given
	mockClient, mockEngine := setupResourcesTest(t)
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	stubRepositoryGet(mockClient, nil)
	stubListedPackageRevision(mockEngine, mockPkgRev, testPRRName)
	mockPkgRev.On("GetResources", mock.Anything).Return(testPRRResources(map[string]string{kptfilev1.KptFileName: "old"}), nil)
	mockEngine.On("UpdatePackageResources", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, (*porchapi.RenderStatus)(nil), errors.New("engine update failed"))

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName,
		testUpdatedObjectInfo(map[string]string{kptfilev1.KptFileName: "new"}),
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.True(t, apierrors.IsInternalError(err))
	require.ErrorContains(t, err, "engine update failed")
}

func TestUpdateReturnsInternalErrorWhenMakeResultFails(t *testing.T) {
	// given
	mockClient, mockEngine := setupResourcesTest(t)
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	oldResources := testPRRResources(map[string]string{kptfilev1.KptFileName: "old"})
	stubRepositoryGet(mockClient, nil)
	stubListedPackageRevision(mockEngine, mockPkgRev, testPRRName)
	mockPkgRev.On("GetResources", mock.Anything).Return(oldResources, nil).Once()
	mockEngine.On("UpdatePackageResources", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(mockPkgRev, (*porchapi.RenderStatus)(nil), nil)
	mockPkgRev.On("GetResources", mock.Anything).Return(nil, errors.New("result load failed")).Once()

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName,
		testUpdatedObjectInfo(map[string]string{kptfilev1.KptFileName: "new"}),
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.True(t, apierrors.IsInternalError(err))
	require.ErrorContains(t, err, "result load failed")
}

func TestUpdateCompleteReplaceReturnsAllResourcesAndRenderStatus(t *testing.T) {
	// given
	mockClient, mockEngine := setupResourcesTest(t)
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	updatedResources := testPRRResources(map[string]string{
		kptfilev1.KptFileName: "new-kptfile",
		testReadmeFile:        "new-readme",
	})
	renderStatus := &porchapi.RenderStatus{Err: "render warning"}
	stubRepositoryGet(mockClient, nil)
	stubListedPackageRevision(mockEngine, mockPkgRev, testPRRName)
	mockPkgRev.On("GetResources", mock.Anything).Return(testPRRResources(map[string]string{kptfilev1.KptFileName: "old"}), nil).Once()
	mockEngine.On("UpdatePackageResources", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(mockPkgRev, renderStatus, nil)
	mockPkgRev.On("GetResources", mock.Anything).Return(updatedResources, nil).Once()

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName,
		testUpdatedObjectInfo(updatedResources.Spec.Resources),
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.NoError(t, err)
	require.False(t, created)
	updated, ok := result.(*porchapi.PackageRevisionResources)
	require.True(t, ok)
	require.Equal(t, updatedResources.Spec.Resources, updated.Spec.Resources)
	require.Equal(t, "render warning", updated.Status.RenderStatus.Err)
}

func TestUpdateConvertsInternalPackageRevisionResources(t *testing.T) {
	// given
	mockClient, mockEngine := setupResourcesTest(t)
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	stubRepositoryGet(mockClient, nil)
	stubListedPackageRevision(mockEngine, mockPkgRev, testPRRName)
	mockPkgRev.On("GetResources", mock.Anything).Return(testPRRResources(map[string]string{kptfilev1.KptFileName: "old"}), nil)

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName,
		&mockUpdatedObjectInfo{
			updatedObj: &apiporch.PackageRevisionResources{
				Spec: apiporch.PackageRevisionResourcesSpec{
					Resources: map[string]string{kptfilev1.KptFileName: "new"},
				},
			},
		},
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.True(t, apierrors.IsBadRequest(err))
	require.ErrorContains(t, err, "could not convert to external type")
}

func TestUpdateV1Alpha2WritesWithoutRenderAndPatchesAnnotation(t *testing.T) {
	// given
	mockClient, mockEngine := setupResourcesTest(t)
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	updatedResources := testPRRResources(map[string]string{kptfilev1.KptFileName: "new-kptfile"})
	stubRepositoryGet(mockClient, v1alpha2Repository())
	stubListedPackageRevision(mockEngine, mockPkgRev, testPRRName)
	mockPkgRev.On("GetResources", mock.Anything).Return(testPRRResources(map[string]string{kptfilev1.KptFileName: "old"}), nil).Once()
	mockEngine.On("UpdatePackageResourcesWithoutRender", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(mockPkgRev, nil)
	mockPkgRev.On("ResourceVersion").Return(testPRRResourceVersion)
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(args mock.Arguments) {
			pr := args.Get(2).(*porchv1alpha2.PackageRevision)
			pr.Name = testPRRName
			pr.Namespace = testPRRNamespace
		}).Return(nil)
	mockClient.On("Patch", mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(args mock.Arguments) {
			pr := args.Get(1).(*porchv1alpha2.PackageRevision)
			require.Equal(t, testPRRResourceVersion, pr.Annotations[porchv1alpha2.AnnotationRenderRequest])
		}).Return(nil)
	mockPkgRev.On("GetResources", mock.Anything).Return(updatedResources, nil).Once()

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName,
		testUpdatedObjectInfo(updatedResources.Spec.Resources),
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.NoError(t, err)
	require.False(t, created)
	updated, ok := result.(*porchapi.PackageRevisionResources)
	require.True(t, ok)
	require.Equal(t, updatedResources.Spec.Resources, updated.Spec.Resources)
	mockEngine.AssertNotCalled(t, "UpdatePackageResources")
	mockClient.AssertCalled(t, "Patch", mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything)
}

func TestUpdateV1Alpha2ReturnsInternalErrorWhenEngineUpdateFails(t *testing.T) {
	// given
	mockClient, mockEngine := setupResourcesTest(t)
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	stubRepositoryGet(mockClient, v1alpha2Repository())
	stubListedPackageRevision(mockEngine, mockPkgRev, testPRRName)
	mockPkgRev.On("GetResources", mock.Anything).Return(testPRRResources(map[string]string{kptfilev1.KptFileName: "old"}), nil)
	mockEngine.On("UpdatePackageResourcesWithoutRender", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("v1alpha2 write failed"))

	// when
	result, created, err := packagerevisionresources.Update(
		namespacedPRRContext(),
		testPRRName,
		testUpdatedObjectInfo(map[string]string{kptfilev1.KptFileName: "new"}),
		nil, nil, false, &metav1.UpdateOptions{})

	// then
	require.Nil(t, result)
	require.False(t, created)
	require.True(t, apierrors.IsInternalError(err))
	require.ErrorContains(t, err, "v1alpha2 write failed")
}

func TestDiffNewAndChanged(t *testing.T) {
	tests := []struct {
		name string
		old  map[string]string
		new  map[string]string
		want map[string]string
	}{
		{
			name: "includes changed and added files",
			old: map[string]string{
				kptfilev1.KptFileName: "old-kptfile",
				testReadmeFile:        "old-readme",
			},
			new: map[string]string{
				kptfilev1.KptFileName: "new-kptfile",
				testReadmeFile:        "old-readme",
				testDeployFile:        "added-deploy",
			},
			want: map[string]string{
				kptfilev1.KptFileName: "new-kptfile",
				testDeployFile:        "added-deploy",
			},
		},
		{
			name: "omits unchanged files",
			old: map[string]string{
				kptfilev1.KptFileName: "same",
			},
			new: map[string]string{
				kptfilev1.KptFileName: "same",
			},
			want: map[string]string{},
		},
		{
			name: "omits files only present in the old package",
			old: map[string]string{
				kptfilev1.KptFileName: "kptfile",
				testReadmeFile:        "readme",
			},
			new: map[string]string{
				kptfilev1.KptFileName: "kptfile",
			},
			want: map[string]string{},
		},
		{
			name: "treats all files as added when old map is nil",
			old:  nil,
			new: map[string]string{
				kptfilev1.KptFileName: "kptfile",
			},
			want: map[string]string{
				kptfilev1.KptFileName: "kptfile",
			},
		},
		{
			name: "returns empty map when new map is empty",
			old: map[string]string{
				kptfilev1.KptFileName: "kptfile",
			},
			new:  map[string]string{},
			want: map[string]string{},
		},
		{
			name: "returns empty map when new map is nil",
			old: map[string]string{
				kptfilev1.KptFileName: "kptfile",
			},
			new:  nil,
			want: map[string]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// given
			// when
			got := packagerevisionresources.diffNewAndChanged(tc.old, tc.new)

			// then
			require.Equal(t, tc.want, got)
		})
	}
}

func TestPatchRenderRequestAnnotationSetsAnnotation(t *testing.T) {
	// given
	pr := v1alpha2PackageRevision(nil)
	storage, fakeClient := newPRRStorageWithFakeClient(t, pr)

	// when
	storage.patchRenderRequestAnnotation(context.Background(), testPRRNamespace, testPRRName, testPRRResourceVersion)

	// then
	got := &porchv1alpha2.PackageRevision{}
	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKey{Namespace: testPRRNamespace, Name: testPRRName}, got))
	require.Equal(t, testPRRResourceVersion, got.Annotations[porchv1alpha2.AnnotationRenderRequest])
}

func TestPatchRenderRequestAnnotationPreservesOtherAnnotations(t *testing.T) {
	// given
	pr := v1alpha2PackageRevision(map[string]string{"keep": "me"})
	storage, fakeClient := newPRRStorageWithFakeClient(t, pr)

	// when
	storage.patchRenderRequestAnnotation(context.Background(), testPRRNamespace, testPRRName, testPRRResourceVersion)

	// then
	got := &porchv1alpha2.PackageRevision{}
	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKey{Namespace: testPRRNamespace, Name: testPRRName}, got))
	require.Equal(t, "me", got.Annotations["keep"])
	require.Equal(t, testPRRResourceVersion, got.Annotations[porchv1alpha2.AnnotationRenderRequest])
}

func TestPatchRenderRequestAnnotationOverwritesExistingRenderRequest(t *testing.T) {
	// given
	pr := v1alpha2PackageRevision(map[string]string{porchv1alpha2.AnnotationRenderRequest: "old-rv"})
	storage, fakeClient := newPRRStorageWithFakeClient(t, pr)

	// when
	storage.patchRenderRequestAnnotation(context.Background(), testPRRNamespace, testPRRName, testPRRResourceVersion)

	// then
	got := &porchv1alpha2.PackageRevision{}
	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKey{Namespace: testPRRNamespace, Name: testPRRName}, got))
	require.Equal(t, testPRRResourceVersion, got.Annotations[porchv1alpha2.AnnotationRenderRequest])
}

func TestPatchRenderRequestAnnotationIgnoresMissingPackageRevision(t *testing.T) {
	// given
	storage, fakeClient := newPRRStorageWithFakeClient(t)

	// when
	storage.patchRenderRequestAnnotation(context.Background(), testPRRNamespace, testPRRName, testPRRResourceVersion)

	// then
	list := &porchv1alpha2.PackageRevisionList{}
	require.NoError(t, fakeClient.List(context.Background(), list))
	require.Empty(t, list.Items)
}

func TestPatchRenderRequestAnnotationIgnoresPatchFailure(t *testing.T) {
	// given
	mockClient := mockclient.NewMockClient(t)
	storage := &packageRevisionResources{
		packageCommon: packageCommon{coreClient: mockClient},
	}
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).Return(nil)
	mockClient.On("Patch", mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Return(errors.New("patch failed"))

	// when
	storage.patchRenderRequestAnnotation(context.Background(), testPRRNamespace, testPRRName, testPRRResourceVersion)

	// then
	mockClient.AssertCalled(t, "Patch", mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything)
}

func namespacedPRRContext() context.Context {
	return genericapirequest.WithNamespace(context.TODO(), testPRRNamespace)
}

func testPRRResources(resources map[string]string) *porchapi.PackageRevisionResources {
	return &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: resources,
		},
	}
}

func testUpdatedObjectInfo(resources map[string]string) rest.UpdatedObjectInfo {
	return &mockUpdatedObjectInfo{
		updatedObj: &porchapi.PackageRevisionResources{
			ObjectMeta: metav1.ObjectMeta{Name: testPRRName},
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: resources,
			},
		},
	}
}

func stubRepositoryGet(mockClient *mockclient.MockClient, repo *configapi.Repository) {
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).
		Run(func(args mock.Arguments) {
			dest := args.Get(2).(*configapi.Repository)
			if repo != nil {
				*dest = *repo.DeepCopy()
			}
		}).Return(nil)
}

func stubListedPackageRevision(mockEngine *mockengine.MockCaDEngine, mockPkgRev *mockrepo.MockPackageRevision, name string) {
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{mockPkgRev}, nil)
	mockPkgRev.On("KubeObjectName").Return(name)
}

func v1alpha2Repository() *configapi.Repository {
	return &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled,
			},
		},
	}
}

func v1alpha2PackageRevision(annotations map[string]string) *porchv1alpha2.PackageRevision {
	return &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testPRRName,
			Namespace:   testPRRNamespace,
			Annotations: annotations,
		},
	}
}

func newPRRStorageWithFakeClient(t *testing.T, objects ...client.Object) (*packageRevisionResources, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, porchv1alpha2.AddToScheme(scheme))
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &packageRevisionResources{
		packageCommon: packageCommon{coreClient: fakeClient},
	}, fakeClient
}
