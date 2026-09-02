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

package engine

import (
	"context"
	"fmt"
	"testing"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	mockrepo "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestListPackagesReturnsPackagesFromRepository(t *testing.T) {
	f := newTestFixture(t)
	expected := []repository.Package{
		&fake.FakePackage{PkgKey: repository.PackageKey{Package: "pkg-a"}},
		&fake.FakePackage{PkgKey: repository.PackageKey{Package: "pkg-b"}},
	}
	f.mockRepo.On("ListPackages", mock.Anything, mock.Anything).Return(expected, nil)

	got, err := f.engine.ListPackages(context.Background(), f.repositoryObj, repository.ListPackageFilter{})

	require.NoError(t, err)
	require.Equal(t, expected, got)
	f.mockRepo.AssertExpectations(t)
	f.mockCache.AssertExpectations(t)
}

func TestListPackagesReturnsErrorWhenOpeningRepositoryFails(t *testing.T) {
	mockCache := &mockCache{}
	repositoryObj := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "default"},
	}
	mockCache.On("OpenRepository", mock.Anything, repositoryObj).
		Return((*mockrepo.MockRepository)(nil), fmt.Errorf("repo unavailable"))
	engine := &cadEngine{cache: mockCache}

	got, err := engine.ListPackages(context.Background(), repositoryObj, repository.ListPackageFilter{})

	require.Nil(t, got)
	require.ErrorContains(t, err, "repo unavailable")
}

func TestListPackagesReturnsErrorWhenListingPackagesFails(t *testing.T) {
	f := newTestFixture(t)
	f.mockRepo.On("ListPackages", mock.Anything, mock.Anything).
		Return([]repository.Package(nil), fmt.Errorf("list failed"))

	got, err := f.engine.ListPackages(context.Background(), f.repositoryObj, repository.ListPackageFilter{})

	require.Nil(t, got)
	require.ErrorContains(t, err, "list failed")
}

func TestDeletePackageRevisionDeletesFromRepository(t *testing.T) {
	f := newTestFixture(t)
	pkgRev := setupMockPackageRevision(t)
	f.mockRepo.On("DeletePackageRevision", mock.Anything, pkgRev).Return(nil)

	err := f.engine.DeletePackageRevision(context.Background(), f.repositoryObj, pkgRev)

	require.NoError(t, err)
	f.mockRepo.AssertExpectations(t)
	f.mockCache.AssertExpectations(t)
}

func TestDeletePackageRevisionReturnsErrorWhenOpeningRepositoryFails(t *testing.T) {
	mockCache := &mockCache{}
	repositoryObj := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "default"},
	}
	mockCache.On("OpenRepository", mock.Anything, repositoryObj).
		Return((*mockrepo.MockRepository)(nil), fmt.Errorf("repo unavailable"))
	engine := &cadEngine{cache: mockCache}

	err := engine.DeletePackageRevision(context.Background(), repositoryObj, setupMockPackageRevision(t))

	require.ErrorContains(t, err, "repo unavailable")
}

func TestDeletePackageRevisionReturnsErrorWhenRepositoryDeleteFails(t *testing.T) {
	f := newTestFixture(t)
	pkgRev := setupMockPackageRevision(t)
	f.mockRepo.On("DeletePackageRevision", mock.Anything, pkgRev).Return(fmt.Errorf("delete failed"))

	err := f.engine.DeletePackageRevision(context.Background(), f.repositoryObj, pkgRev)

	require.ErrorContains(t, err, "delete failed")
}
