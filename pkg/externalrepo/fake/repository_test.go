// Copyright 2025 The kpt Authors
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

package fake

import (
	"context"
	"testing"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
)

func TestRepositoryFunctions(t *testing.T) {
	fakeRepo := Repository{
		key: repository.RepositoryKey{
			Name:      "my-repo",
			Namespace: "my-namespace",
		},
		CurrentVersion: "99.88.77",
		Packages:       []repository.Package{},
	}

	assert.Equal(t, "my-namespace", fakeRepo.KubeObjectNamespace())
	assert.Equal(t, "my-repo", fakeRepo.KubeObjectName())
	assert.Equal(t, fakeRepo.key, fakeRepo.Key())
	assert.Nil(t, fakeRepo.Close(context.TODO()))

	version, err := fakeRepo.Version(context.TODO())
	assert.Nil(t, err)
	assert.Equal(t, "99.88.77", version)

	// Test BranchCommitHash
	commitHash, err := fakeRepo.BranchCommitHash(context.TODO())
	assert.Nil(t, err)
	assert.Equal(t, "fake-commit-hash", commitHash)

	fakeRepo.ThrowError = true
	_, err = fakeRepo.ListPackageRevisions(context.TODO(), repository.ListPackageRevisionFilter{})
	assert.NotNil(t, err)
	assert.Equal(t, "fake repository threw this error because it was told to", err.Error())
	fakeRepo.ThrowError = false

	pkgRevs, err := fakeRepo.ListPackageRevisions(context.TODO(), repository.ListPackageRevisionFilter{})
	assert.Nil(t, err)
	assert.Equal(t, 0, len(pkgRevs))

	fakePkg := FakePackage{
		PkgKey: repository.PackageKey{
			RepoKey: fakeRepo.key,
			Package: "my-package",
		},
	}
	fakeRepo.Packages = append(fakeRepo.Packages, &fakePkg)

	pkgs, err := fakeRepo.ListPackages(context.TODO(), repository.ListPackageFilter{})
	assert.Nil(t, err)
	assert.Equal(t, 1, len(pkgs))

	fakePR := FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey:        fakePkg.Key(),
			WorkspaceName: "my-workspace",
		},
	}
	fakeRepo.PackageRevisions = append(fakeRepo.PackageRevisions, &fakePR)

	pkgRevs, err = fakeRepo.ListPackageRevisions(context.TODO(), repository.ListPackageRevisionFilter{})
	assert.Nil(t, err)
	assert.Equal(t, 1, len(pkgRevs))

	newPRDraft, err := fakeRepo.CreatePackageRevisionDraft(context.TODO(), &porchapi.PackageRevision{})
	assert.Nil(t, err)

	newPR, err := fakeRepo.ClosePackageRevisionDraft(context.TODO(), newPRDraft, 0)
	assert.Nil(t, err)
	assert.Equal(t, newPRDraft.Key(), newPR.Key())

	updatedPRDraft, err := fakeRepo.UpdatePackageRevision(context.TODO(), newPR)
	assert.Nil(t, err)
	assert.Nil(t, updatedPRDraft)

	assert.Nil(t, fakeRepo.DeletePackageRevision(context.TODO(), newPR))

	assert.Nil(t, fakeRepo.Refresh(context.TODO()))
}
