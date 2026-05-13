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

package task

import (
	"context"
	"strings"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

var (
	res = &fakeReferenceResolver{}
)

func createMockedResources() repository.PackageResources {
	return repository.PackageResources{
		Contents: map[string]string{
			"apiVersion": "kpt.dev/v1alpha1",
			"kind":       "Kptfile",
		},
	}
}

func TestApplyErrorInvalidUpstreamUprade(t *testing.T) {
	ctx := context.Background()

	// Mock resources and tasks with valid upstream but no resources to fetch
	resources := createMockedResources()
	updateTask := &porchapi.Task{
		Type: porchapi.TaskTypeUpgrade,
		Upgrade: &porchapi.PackageUpgradeTaskSpec{
			OldUpstream: porchapi.PackageRevisionRef{
				Name: "original",
			},
			NewUpstream: porchapi.PackageRevisionRef{
				Name: "upstream",
			},
			LocalPackageRevisionRef: porchapi.PackageRevisionRef{
				Name: "destination",
			},
		},
	}

	mutation := &upgradePackageMutation{
		upgradeTask: updateTask,
		pkgName:     "test-package",
	}

	_, _, err := mutation.apply(ctx, resources)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error fetching the resources for package")
}

func TestUpgradeToPlaceholderAsNewUpstream(t *testing.T) {
	repositoryNamespace := "test-namespace"
	repositoryName := "repo"
	pkg := "1234567890"
	revision := -1
	workspace := "main"
	placeholderPackageName := "repo.1234567890.main"
	placeholderPackageRevision := &fake.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: repositoryNamespace,
					Name:      repositoryName,
				},
				Package: pkg,
			},
			Revision:      revision,
			WorkspaceName: workspace,
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
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

	oldRev := *placeholderPackageRevision
	oldPackageRevision := &oldRev
	oldPackageRevision.PrKey.WorkspaceName = "old"

	repo := &fake.Repository{
		PackageRevisions: []repository.PackageRevision{
			oldPackageRevision,
			placeholderPackageRevision,
		},
	}
	repoOpener := &fakeRepositoryOpener{
		repository: repo,
	}

	mutation := upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream: porchapi.PackageRevisionRef{
					Name: "repo.1234567890.old",
				},
				NewUpstream: porchapi.PackageRevisionRef{
					Name: placeholderPackageName,
				},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{
					Name: "destination",
				},
			},
		},

		namespace:         "test-namespace",
		pkgName:           pkg,
		referenceResolver: res,
		repoOpener:        repoOpener,
	}
	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "placeholder package revision", "Expected error upgrading to the placeholder package revision as new upstream")

	mutation.referenceResolver = (&errorAfterNReferenceResolver{r: res, err: errors.New("error resolving reference")}).startAt(2)

	_, _, err = mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "failed to resolve repository reference")

	mutation.referenceResolver = res
}

func TestUpgradePlaceholderAsLocal(t *testing.T) {
	repositoryNamespace := "test-namespace"
	repositoryName := "repo"
	pkg := "1234567890"
	revision := -1
	workspace := "main"
	placeholderPackageName := "repo.1234567890.main"
	placeholderPackageRevision := &fake.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: repositoryNamespace,
					Name:      repositoryName,
				},
				Package: pkg,
			},
			Revision:      revision,
			WorkspaceName: workspace,
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
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

	oldRev := *placeholderPackageRevision
	oldPackageRevision := &oldRev
	oldPackageRevision.PrKey.WorkspaceName = "old"

	newRev := *placeholderPackageRevision
	newPackageRevision := &newRev
	newPackageRevision.PrKey.WorkspaceName = "new"

	repo := &fake.Repository{
		PackageRevisions: []repository.PackageRevision{
			oldPackageRevision,
			newPackageRevision,
			placeholderPackageRevision,
		},
	}
	repoOpener := &fakeRepositoryOpener{
		repository: repo,
	}

	mutation := upgradePackageMutation{
		upgradeTask: &porchapi.Task{
			Type: porchapi.TaskTypeUpgrade,
			Upgrade: &porchapi.PackageUpgradeTaskSpec{
				OldUpstream: porchapi.PackageRevisionRef{
					Name: "repo.1234567890.old",
				},
				NewUpstream: porchapi.PackageRevisionRef{
					Name: "repo.1234567890.new",
				},
				LocalPackageRevisionRef: porchapi.PackageRevisionRef{
					Name: placeholderPackageName,
				},
			},
		},

		namespace:         "test-namespace",
		pkgName:           pkg,
		referenceResolver: res,
		repoOpener:        repoOpener,
	}
	_, _, err := mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "placeholder package revision", "Expected error upgrading the placeholder package revision")

	mutation.referenceResolver = (&errorAfterNReferenceResolver{r: res, err: errors.New("error resolving reference")}).startAt(4)

	_, _, err = mutation.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "failed to resolve repository reference")

	mutation.referenceResolver = res
}
