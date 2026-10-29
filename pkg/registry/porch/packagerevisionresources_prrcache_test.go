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
	"testing"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockprrstore "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/prrstore"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/endpoints/request"
)

// ============================================================================
// PRRStore Integration Tests - Get Operation
// ============================================================================
// These tests verify that packagerevisionresources correctly integrates with PRRStore
// for storing and retrieving resources. PRRStore is used for v1alpha2 repositories.

func TestGetV1Alpha2RepositoryFromPRRStore(t *testing.T) {
	// Test Get operation reading resources from PRRStore (v1alpha2 repositories use PRRStore)
	mockPRRStore := mockprrstore.NewMockPRRStore(t)
	mockClient := mockclient.NewMockClient(t)

	v1alpha2Repo := newV1Alpha2Repository("test-repo", "default")
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(args mock.Arguments) {
			repo := args.Get(2).(*configapi.Repository)
			*repo = *v1alpha2Repo
		}).
		Return(nil)

	storage := &packageRevisionResources{
		packageCommon: packageCommon{coreClient: mockClient},
		store:         mockPRRStore,
	}

	resources := map[string]string{"Kptfile": "content", "deploy.yaml": "deployment"}
	mockPRRStore.On("GetResources", mock.Anything, mock.Anything).Return(resources, nil)

	ctx := request.WithNamespace(context.Background(), "default")

	// When: Get resources from v1alpha2 repo
	result, err := storage.Get(ctx, "test-repo.1234567890.v1", nil)

	// Then: Should read from PRRStore successfully
	require.NoError(t, err)
	require.NotNil(t, result)
	prr, ok := result.(*porchapi.PackageRevisionResources)
	require.True(t, ok)
	require.Equal(t, resources, prr.Spec.Resources)
	mockPRRStore.AssertCalled(t, "GetResources", mock.Anything, mock.Anything)
}

func TestGetV1Alpha2RepositoryPRRStoreMissError(t *testing.T) {
	// Test error when PRRStore is empty for v1alpha2 repo (no fallback to git)
	mockPRRStore := mockprrstore.NewMockPRRStore(t)
	mockClient := mockclient.NewMockClient(t)

	v1alpha2Repo := newV1Alpha2Repository("test-repo", "default")
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(args mock.Arguments) {
			repo := args.Get(2).(*configapi.Repository)
			*repo = *v1alpha2Repo
		}).
		Return(nil)

	storage := &packageRevisionResources{
		packageCommon: packageCommon{coreClient: mockClient, gr: porchapi.Resource("packagerevisions"), cad: nil},
		store:         mockPRRStore,
	}

	// PRRStore returns empty resources (cache miss)
	mockPRRStore.On("GetResources", mock.Anything, mock.Anything).Return(map[string]string{}, nil)

	ctx := request.WithNamespace(context.Background(), "default")

	// When: Get resources with PRRStore miss
	_, err := storage.Get(ctx, "test-repo.1234567890.v1", nil)

	// Then: Should return error (no fallback to git for v1alpha2)
	require.Error(t, err)
	require.True(t, apierrors.IsNotFound(err))
}

func TestGetV1Alpha2RepositoryPRRStoreNotInitializedError(t *testing.T) {
	// Test error when PRRStore is not initialized for v1alpha2 repo
	mockClient := mockclient.NewMockClient(t)

	v1alpha2Repo := newV1Alpha2Repository("test-repo", "default")
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(args mock.Arguments) {
			repo := args.Get(2).(*configapi.Repository)
			*repo = *v1alpha2Repo
		}).
		Return(nil)

	storage := &packageRevisionResources{
		packageCommon: packageCommon{coreClient: mockClient},
		store:         nil, // Not initialized
	}

	ctx := request.WithNamespace(context.Background(), "default")

	// When: Get resources from v1alpha2 repo without PRRStore
	result, err := storage.Get(ctx, "test-repo.1234567890.v1", nil)

	// Then: Should return error about uninitialized PRRStore
	require.Error(t, err)
	require.Nil(t, result)
	require.ErrorContains(t, err, "PRRStore not initialized")
}

func TestGetV1Alpha2RepositoryWithResourceSelector(t *testing.T) {
	// Test Get operation with resource selector (file filtering)
	// Note: Query strings in k8s API are handled by the REST layer, not passed to storage.Get.
	// This test verifies that the storage layer correctly handles multiple resource requests
	// when called through the aggregated API with selected files.
	mockPRRStore := mockprrstore.NewMockPRRStore(t)
	mockClient := mockclient.NewMockClient(t)

	v1alpha2Repo := newV1Alpha2Repository("test-repo", "default")
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(args mock.Arguments) {
			repo := args.Get(2).(*configapi.Repository)
			*repo = *v1alpha2Repo
		}).
		Return(nil)

	storage := &packageRevisionResources{
		packageCommon: packageCommon{coreClient: mockClient},
		store:         mockPRRStore,
	}

	allResources := map[string]string{
		"Kptfile":     "kptfile-content",
		"deploy.yaml": "deploy-content",
		"README.md":   "readme-content",
	}
	mockPRRStore.On("GetResources", mock.Anything, mock.Anything).Return(allResources, nil)

	ctx := request.WithNamespace(context.Background(), "default")

	// When: Get resources from PRRStore
	result, err := storage.Get(ctx, "test-repo.1234567890.v1", nil)

	// Then: Should return all resources from PRRStore
	require.NoError(t, err)
	prr, ok := result.(*porchapi.PackageRevisionResources)
	require.True(t, ok)
	require.Equal(t, allResources, prr.Spec.Resources)
	mockPRRStore.AssertCalled(t, "GetResources", mock.Anything, mock.Anything)
}

// ============================================================================
// PRRStore Integration Tests - Update Operation
// ============================================================================
// These tests verify Update operations through the PRRStore integration layer.

func TestUpdateV1Alpha2RepositoryFromPRRStore(t *testing.T) {
	// Test Update operation for v1alpha2 repository with existing resources in PRRStore
	mockPRRStore := mockprrstore.NewMockPRRStore(t)
	mockClient := mockclient.NewMockClient(t)

	v1alpha2Repo := newV1Alpha2Repository("test-repo", "default")
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(args mock.Arguments) {
			repo := args.Get(2).(*configapi.Repository)
			*repo = *v1alpha2Repo
		}).
		Return(nil).
		Maybe()

	// Mock Get call for patchRenderRequestAnnotation to fetch v1alpha2.PackageRevision
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(args mock.Arguments) {
			pr := args.Get(2).(*porchv1alpha2.PackageRevision)
			pr.Name = "test-repo.1234567890.v1"
			pr.Namespace = "default"
			if pr.Annotations == nil {
				pr.Annotations = map[string]string{}
			}
		}).
		Return(nil).
		Maybe()

	// Mock Patch call for patchRenderRequestAnnotation
	mockClient.On("Patch", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	storage := &packageRevisionResources{
		packageCommon: packageCommon{coreClient: mockClient, gr: porchapi.Resource("packagerevisions"), cad: nil},
		store:         mockPRRStore,
	}

	oldResources := map[string]string{"Kptfile": "old-content"}
	mockPRRStore.On("GetResources", mock.Anything, mock.Anything).Return(oldResources, nil)
	mockPRRStore.On("WriteResources", mock.Anything, mock.Anything, mock.Anything).Return("v2", nil)

	newResources := map[string]string{"Kptfile": "new-content"}
	objInfo := &mockUpdatedObjectInfo{
		updatedObj: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: newResources,
			},
		},
	}

	ctx := request.WithNamespace(context.Background(), "default")

	// When: Update v1alpha2 package with resources in PRRStore
	result, created, err := storage.Update(ctx, "test-repo.1234567890.v1", objInfo, nil, nil, false, &metav1.UpdateOptions{})

	// Then: Should write to PRRStore and return success
	require.NoError(t, err)
	require.True(t, created) // PRRStore-only path returns created=true
	require.NotNil(t, result)
	prr, ok := result.(*porchapi.PackageRevisionResources)
	require.True(t, ok)
	require.Equal(t, newResources, prr.Spec.Resources)
	mockPRRStore.AssertCalled(t, "WriteResources", mock.Anything, mock.Anything, newResources)
}

func TestUpdateV1Alpha2RepositoryAPICreatedDraftNotInCADEngine(t *testing.T) {
	// Test Update for v1alpha2 repo where package exists only in PRRStore (API-created draft)
	mockPRRStore := mockprrstore.NewMockPRRStore(t)
	mockClient := mockclient.NewMockClient(t)

	v1alpha2Repo := newV1Alpha2Repository("test-repo", "default")
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(args mock.Arguments) {
			repo := args.Get(2).(*configapi.Repository)
			*repo = *v1alpha2Repo
		}).
		Return(nil)

	// Mock Get call for patchRenderRequestAnnotation
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(args mock.Arguments) {
			pr := args.Get(2).(*porchv1alpha2.PackageRevision)
			pr.Name = "test-repo.1234567890.v1"
			pr.Namespace = "default"
			if pr.Annotations == nil {
				pr.Annotations = map[string]string{}
			}
		}).
		Return(nil).
		Maybe()

	// Mock Patch call for patchRenderRequestAnnotation
	mockClient.On("Patch", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// PRRStore has the API-created draft resources
	oldResources := map[string]string{"Kptfile": "api-created"}
	mockPRRStore.On("GetResources", mock.Anything, mock.Anything).Return(oldResources, nil)
	mockPRRStore.On("WriteResources", mock.Anything, mock.Anything, mock.Anything).Return("v2", nil)

	storage := &packageRevisionResources{
		packageCommon: packageCommon{coreClient: mockClient, cad: nil},
		store:         mockPRRStore,
	}

	newResources := map[string]string{"Kptfile": "updated"}
	objInfo := &mockUpdatedObjectInfo{
		updatedObj: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: newResources,
			},
		},
	}

	ctx := request.WithNamespace(context.Background(), "default")

	// When: Update API-created draft only in PRRStore
	result, created, err := storage.Update(ctx, "test-repo.1234567890.v1", objInfo, nil, nil, false, &metav1.UpdateOptions{})

	// Then: Should update from PRRStore.
	// Returns created=true because v1alpha2 path returns created=true.
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, result)
	prr, ok := result.(*porchapi.PackageRevisionResources)
	require.True(t, ok)
	require.Equal(t, newResources, prr.Spec.Resources)
	mockPRRStore.AssertCalled(t, "WriteResources", mock.Anything, mock.Anything, newResources)
}

func TestUpdateV1Alpha2RepositoryPRRStoreWriteSucceeds(t *testing.T) {
	// Test successful update when PRRStore write succeeds for v1alpha2 repo
	mockPRRStore := mockprrstore.NewMockPRRStore(t)
	mockClient := mockclient.NewMockClient(t)

	v1alpha2Repo := newV1Alpha2Repository("test-repo", "default")
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(args mock.Arguments) {
			repo := args.Get(2).(*configapi.Repository)
			*repo = *v1alpha2Repo
		}).
		Return(nil).
		Maybe()

	// Mock Get call for patchRenderRequestAnnotation
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(args mock.Arguments) {
			pr := args.Get(2).(*porchv1alpha2.PackageRevision)
			pr.Name = "test-repo.1234567890.v1"
			pr.Namespace = "default"
			if pr.Annotations == nil {
				pr.Annotations = map[string]string{}
			}
		}).
		Return(nil).
		Maybe()

	// Mock Patch call for patchRenderRequestAnnotation
	mockClient.On("Patch", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	storage := &packageRevisionResources{
		packageCommon: packageCommon{coreClient: mockClient, cad: nil},
		store:         mockPRRStore,
	}

	// PRRStore has old resources for this draft
	oldResources := map[string]string{"Kptfile": "old"}
	mockPRRStore.On("GetResources", mock.Anything, mock.Anything).Return(oldResources, nil)

	// PRRStore write succeeds
	mockPRRStore.On("WriteResources", mock.Anything, mock.Anything, mock.Anything).Return("v2", nil)

	newResources := map[string]string{"Kptfile": "new"}
	objInfo := &mockUpdatedObjectInfo{
		updatedObj: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: newResources,
			},
		},
	}

	ctx := request.WithNamespace(context.Background(), "default")

	// When: Update v1alpha2 package and PRRStore write succeeds
	result, created, err := storage.Update(ctx, "test-repo.1234567890.v1", objInfo, nil, nil, false, &metav1.UpdateOptions{})

	// Then: Should return success
	require.NoError(t, err)
	require.True(t, created) // v1alpha2 path returns created=true
	require.NotNil(t, result)
	prr, ok := result.(*porchapi.PackageRevisionResources)
	require.True(t, ok)
	require.Equal(t, newResources, prr.Spec.Resources)
	mockPRRStore.AssertCalled(t, "WriteResources", mock.Anything, mock.Anything, newResources)
}

func TestUpdateV1Alpha2RepositoryPRRStoreNotInitializedError(t *testing.T) {
	// Test error when PRRStore is not initialized for v1alpha2 repo
	mockClient := mockclient.NewMockClient(t)

	v1alpha2Repo := newV1Alpha2Repository("test-repo", "default")
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(args mock.Arguments) {
			repo := args.Get(2).(*configapi.Repository)
			*repo = *v1alpha2Repo
		}).
		Return(nil)

	storage := &packageRevisionResources{
		packageCommon: packageCommon{coreClient: mockClient, cad: nil},
		store:         nil, // Not initialized - should trigger error
	}

	objInfo := &mockUpdatedObjectInfo{
		updatedObj: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				Resources: map[string]string{"Kptfile": "new"},
			},
		},
	}

	ctx := request.WithNamespace(context.Background(), "default")

	// When: Update v1alpha2 package without initialized PRRStore
	result, created, err := storage.Update(ctx, "test-repo.1234567890.v1", objInfo, nil, nil, false, &metav1.UpdateOptions{})

	// Then: Should return error about uninitialized PRRStore
	require.Error(t, err)
	require.False(t, created)
	require.Nil(t, result)
	require.ErrorContains(t, err, "PRRStore not initialized")
}

// ============================================================================
// Helpers
// ============================================================================

// newV1Alpha2Repository creates a v1alpha2-annotated Repository object
func newV1Alpha2Repository(name, namespace string) *configapi.Repository {
	return &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled,
			},
		},
	}
}
