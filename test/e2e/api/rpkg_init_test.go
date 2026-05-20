// Copyright 2025-2026 The kpt Authors
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

package api

import (
	"github.com/google/go-cmp/cmp"
	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (t *PorchSuite) TestInitEmptyPackage() {
	// Create a new package via init, no task specified
	const (
		repository  = "init-empty"
		packageName = "empty-package"
		workspace   = "test-workspace"
		description = "empty-package description"
	)

	// Register the repository
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create a new package (via init)
	pr := t.CreatePackageDraftF(repository, packageName, workspace)

	t.validateKptFileMetadata(pr, packageName, &kptfilev1.PackageInfo{
		Description: description,
	})

	t.validatePackageResourcesSize(pr)
}

func (t *PorchSuite) TestInitTaskPackage() {
	const (
		repository  = "init-task"
		packageName = "new-package"
		workspace   = "test-workspace"
		description = "New Package"
		site        = "https://kpt.dev/new-package"
	)
	keywords := []string{"test"}

	// Register the repository
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create PackageRevision from upstream repo
	pr := t.CreatePackageSkeleton(repository, packageName, workspace)
	pr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeInit,
			Init: &porchapi.PackageInitTaskSpec{
				Description: description,
				Keywords:    keywords,
				Site:        site,
			},
		},
	}
	t.CreateF(pr)

	t.validateKptFileMetadata(pr, packageName, &kptfilev1.PackageInfo{
		Site:        site,
		Description: description,
		Keywords:    keywords,
	})
}

func (t *PorchSuite) validateKptFileMetadata(pr *porchapi.PackageRevision, expectedName string, expectedInfo *kptfilev1.PackageInfo) {
	var pkg porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      pr.Name,
	}, &pkg)

	kptfile := t.ParseKptfileF(&pkg)
	if got, want := kptfile.Name, expectedName; got != want {
		t.Fatalf("Package name: got %q, want %q", got, want)
	}
	if got, want := kptfile.Info, expectedInfo; !cmp.Equal(want, got) {
		t.Fatalf("unexpected %s/%s package info (-want, +got) %s", pkg.Namespace, pkg.Name, cmp.Diff(want, got))
	}
}

func (t *PorchSuite) validatePackageResourcesSize(pr *porchapi.PackageRevision) {
	t.T().Helper()
	if t.UsingDBCache {
		t.Logf("DB cache is enabled: validating package resource size")
		var pkg porchapi.PackageRevisionResources
		t.GetF(client.ObjectKey{
			Namespace: t.Namespace,
			Name:      pr.Name,
		}, &pkg)

		expectedResourcesSize := int64(0)
		for _, file := range pkg.Spec.Resources {
			expectedResourcesSize += int64(len(file))
		}
		assert.EqualValues(t, expectedResourcesSize, pr.Status.ResourcesSizeBytes,
			"Expected PackageRevision %s/%s resources size of %d bytes, got %d",
			pr.Namespace, pr.Name,
			expectedResourcesSize, pr.Status.ResourcesSizeBytes)
	} else {
		assert.EqualValues(t, 0, pr.Status.ResourcesSizeBytes, "PackageRevision resources size should not be available in non-DB cache deployment")
	}
}
