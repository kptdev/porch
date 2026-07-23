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
	"testing"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockengine "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/endpoints/request"
)

func TestApprovalUpdateStrategy(t *testing.T) {
	s := packageRevisionApprovalStrategy{}

	type testCase struct {
		old     porchapi.PackageRevisionLifecycle
		valid   []porchapi.PackageRevisionLifecycle
		invalid []porchapi.PackageRevisionLifecycle
	}

	for _, tc := range []testCase{
		{
			old:     "",
			valid:   []porchapi.PackageRevisionLifecycle{},
			invalid: []porchapi.PackageRevisionLifecycle{"", "Wrong", porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed, porchapi.PackageRevisionLifecyclePublished, porchapi.PackageRevisionLifecycleDeletionProposed},
		},
		{
			old:     "Wrong",
			valid:   []porchapi.PackageRevisionLifecycle{},
			invalid: []porchapi.PackageRevisionLifecycle{"", "Wrong", porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed, porchapi.PackageRevisionLifecyclePublished, porchapi.PackageRevisionLifecycleDeletionProposed},
		},
		{
			old:     porchapi.PackageRevisionLifecycleDraft,
			valid:   []porchapi.PackageRevisionLifecycle{},
			invalid: []porchapi.PackageRevisionLifecycle{"", "Wrong", porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed, porchapi.PackageRevisionLifecyclePublished, porchapi.PackageRevisionLifecycleDeletionProposed},
		},
		{
			old:     porchapi.PackageRevisionLifecyclePublished,
			valid:   []porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecycleDeletionProposed},
			invalid: []porchapi.PackageRevisionLifecycle{"", "Wrong", porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed, porchapi.PackageRevisionLifecyclePublished},
		},
		{
			old:     porchapi.PackageRevisionLifecycleDeletionProposed,
			valid:   []porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecyclePublished},
			invalid: []porchapi.PackageRevisionLifecycle{"", "Wrong", porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecycleProposed, porchapi.PackageRevisionLifecycleDeletionProposed},
		},
		{
			old:     porchapi.PackageRevisionLifecycleProposed,
			valid:   []porchapi.PackageRevisionLifecycle{porchapi.PackageRevisionLifecycleDraft, porchapi.PackageRevisionLifecyclePublished},
			invalid: []porchapi.PackageRevisionLifecycle{"", "Wrong", porchapi.PackageRevisionLifecycleProposed, porchapi.PackageRevisionLifecycleDeletionProposed},
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

func TestApprovalUpdate(t *testing.T) {
	// Setup approval instance
	approval := &packageRevisionApproval{
		packageCommon: packageCommon{
			scheme:         runtime.NewScheme(),
			gr:             porchapi.Resource("packagerevisions"),
			coreClient:     nil,
			updateStrategy: packageRevisionApprovalStrategy{},
			createStrategy: packageRevisionApprovalStrategy{},
		},
	}

	mockClient := mockclient.NewMockClient(t)
	approval.coreClient = mockClient
	mockEngine := mockengine.NewMockCaDEngine(t)
	approval.cad = mockEngine

	ctx := request.WithNamespace(context.TODO(), "someDummyNamespace")
	pkgRevName := "repo.1234567890.ws"

	// Create a proposed package revision for testing approval
	proposedPackageRevision := &fake.FakePackageRevision{
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
		PackageLifecycle: porchapi.PackageRevisionLifecycleProposed,
		PackageRevision: &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Labels:          make(map[string]string),
				ResourceVersion: "123",
			},
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleProposed,
			},
		},
	}

	// Success case
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		proposedPackageRevision,
	}, nil).Once()
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).Return(nil).Maybe()
	mockEngine.On("UpdatePackageRevision", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(proposedPackageRevision, nil).Once()

	objInfo := &mockApprovalUpdatedObjectInfo{
		updatedObj: &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				ResourceVersion: "123",
			},
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecyclePublished,
			},
		},
	}

	result, created, err := approval.Update(ctx, pkgRevName, objInfo, nil, nil, false, &metav1.UpdateOptions{})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, created)
	assert.IsType(t, &porchapi.PackageRevision{}, result)

	//=========================================================================================

	// Error case - updatePackageRevision fails
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		proposedPackageRevision,
	}, nil).Once()
	mockEngine.On("UpdatePackageRevision", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("approval update failed")).Once()

	result, created, err = approval.Update(ctx, pkgRevName, objInfo, nil, nil, false, &metav1.UpdateOptions{})
	assert.Nil(t, result)
	assert.False(t, created)
	assert.True(t, apierrors.IsInternalError(err))
	assert.ErrorContains(t, err, "approval update failed")
}

// mockApprovalUpdatedObjectInfo is a mock implementation of rest.UpdatedObjectInfo for approval tests
type mockApprovalUpdatedObjectInfo struct {
	updatedObj runtime.Object
}

func (m *mockApprovalUpdatedObjectInfo) UpdatedObject(ctx context.Context, oldObj runtime.Object) (runtime.Object, error) {
	return m.updatedObj, nil
}

func (m *mockApprovalUpdatedObjectInfo) Preconditions() *metav1.Preconditions {
	return nil
}
