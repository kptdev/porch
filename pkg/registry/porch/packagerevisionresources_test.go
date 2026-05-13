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
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockengine "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/engine"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/apis/meta/internalversion"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	packagerevisionresources = &packageRevisionResources{
		TableConvertor: packageRevisionResourcesTableConvertor,
		packageCommon: packageCommon{
			scheme:         runtime.NewScheme(),
			gr:             porchapi.Resource("packagerevisions"),
			coreClient:     nil,
			updateStrategy: packageRevisionStrategy{},
			createStrategy: packageRevisionStrategy{},
		},
	}
)

func setupResourcesTest(t *testing.T) (mockClient *mockclient.MockClient, mockEngine *mockengine.MockCaDEngine) {
	mockClient = mockclient.NewMockClient(t)
	packagerevisionresources.coreClient = mockClient
	mockEngine = mockengine.NewMockCaDEngine(t)
	packagerevisionresources.cad = mockEngine
	mockClient.On("List", mock.Anything, mock.Anything, mock.Anything).Return(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
		list.(*configapi.RepositoryList).Items = []configapi.Repository{
			dummyRepoObject,
		}
		return nil
	}).Maybe()

	return
}

func TestListResources(t *testing.T) {
	mockClient, mockEngine := setupResourcesTest(t)
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).Return(nil).Maybe()
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		packageRevision,
	}, nil).Once()

	result, err := packagerevisionresources.List(context.TODO(), &internalversion.ListOptions{})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result.(*porchapi.PackageRevisionResourcesList).Items))

	//=========================================================================================

	result, err = packagerevisionresources.List(context.TODO(), &internalversion.ListOptions{
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
	mockPkgRev.On("GetResources", mock.Anything).Return(nil, errors.New("error getting API package revision")).Once()
	result, err = packagerevisionresources.List(context.TODO(), &internalversion.ListOptions{})
	assert.NoError(t, err)
	resultList, isList := result.(*porchapi.PackageRevisionResourcesList)
	assert.True(t, isList)
	assert.Equal(t, 0, len(resultList.Items))
}

func TestGetResources(t *testing.T) {
	mockClient, mockEngine := setupResourcesTest(t)
	mockClient.On("Get", mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository"), mock.Anything).Return(nil).Maybe()
	pkgRevName := "repo.1234567890.ws"

	// Success case
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		packageRevision,
	}, nil).Once()

	ctx := genericapirequest.WithNamespace(context.TODO(), "someDummyNamespace")
	result, err := packagerevisionresources.Get(ctx, pkgRevName, nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.IsType(t, &porchapi.PackageRevisionResources{}, result)

	//=========================================================================================

	// PRR Not found case
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{}, nil).Once()

	result, err = packagerevisionresources.Get(ctx, pkgRevName, nil)
	assert.Error(t, err)
	assert.Nil(t, result)

	//=========================================================================================

	// Error from GetResources
	mockPkgRev := mockrepo.NewMockPackageRevision(t)
	mockEngine.On("ListPackageRevisions", mock.Anything, mock.Anything).Return([]repository.PackageRevision{
		mockPkgRev,
	}, nil).Once()
	mockPkgRev.On("KubeObjectName").Return(pkgRevName)
	mockPkgRev.On("GetResources", mock.Anything).Return(nil, errors.New("error getting resources"))

	result, err = packagerevisionresources.Get(ctx, pkgRevName, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}
