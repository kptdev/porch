// Copyright 2022, 2024-2025 The kpt Authors
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
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEdit(t *testing.T) {
	repositoryNamespace := "test-namespace"
	repositoryName := "repo"
	pkg := "1234567890"
	revision := 1
	workspace := "ws"
	packageName := "repo.1234567890.ws"
	packageRevision := &fake.FakePackageRevision{
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
	repo := &fake.Repository{
		PackageRevisions: []repository.PackageRevision{
			packageRevision,
		},
	}
	repoOpener := &fakeRepositoryOpener{
		repository: repo,
	}

	epm := editPackageMutation{
		task: &porchapi.Task{
			Type: porchapi.TaskTypeEdit,
			Edit: &porchapi.PackageEditTaskSpec{
				Source: &porchapi.PackageRevisionRef{
					Name: packageName,
				},
			},
		},

		namespace:         "test-namespace",
		packageName:       pkg,
		repositoryName:    repositoryName,
		referenceResolver: &fakeReferenceResolver{},
		repoOpener:        repoOpener,
	}

	res, _, err := epm.apply(context.Background(), repository.PackageResources{})
	if err != nil {
		t.Errorf("task apply failed: %v", err)
	}

	want := strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: sample description
	`)
	got := strings.TrimSpace(res.Contents[kptfilev1.KptFileName])
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}
}

func TestEditPlaceholder(t *testing.T) {
	repositoryNamespace := "test-namespace"
	repositoryName := "repo"
	pkg := "1234567890"
	revision := -1
	workspace := "main"
	placeholderPackageName := "repo.1234567890.main"
	packageRevision := &fake.FakePackageRevision{
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
	repo := &fake.Repository{
		PackageRevisions: []repository.PackageRevision{
			packageRevision,
		},
	}
	repoOpener := &fakeRepositoryOpener{
		repository: repo,
	}

	res := &fakeReferenceResolver{}
	epm := editPackageMutation{
		task: &porchapi.Task{
			Type: porchapi.TaskTypeEdit,
			Edit: &porchapi.PackageEditTaskSpec{
				Source: &porchapi.PackageRevisionRef{
					Name: placeholderPackageName,
				},
			},
		},

		namespace:         "test-namespace",
		packageName:       pkg,
		repositoryName:    repositoryName,
		referenceResolver: res,
		repoOpener:        repoOpener,
	}
	_, _, err := epm.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "placeholder package revision", "Expected error editing the placeholder package revision")

	epm.referenceResolver = (&errorAfterNReferenceResolver{r: res, err: errors.New("error resolving reference")}).startAt(1)

	_, _, err = epm.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "failed to resolve repository reference")

	epm.referenceResolver = res
}

// Implementation of the ReferenceResolver interface for testing.
type fakeReferenceResolver struct {
	address string
}

func (f *fakeReferenceResolver) ResolveReference(ctx context.Context, namespace, name string, result repository.Object) error {
	repo, ok := result.(*configapi.Repository)
	if ok {
		*repo = configapi.Repository{
			TypeMeta: metav1.TypeMeta{
				Kind:       configapi.TypeRepository.Kind,
				APIVersion: configapi.TypeRepository.APIVersion(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
			Spec: configapi.RepositorySpec{
				Deployment: false,
				Type:       configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      f.address,
					Branch:    "main",
					Directory: "configmap",
				},
			},
		}
		result = repo
	}
	return nil
}

// Implementation of the ReferenceResolver interface for testing.
// For the first N calls to ResolveReference, it delegates to the provided, working resolver.
// On the N+1th call, it returns the provided error.
type errorAfterNReferenceResolver struct {
	r                  repository.ReferenceResolver
	err                error
	successesRemaining atomic.Int64
}

func (f *errorAfterNReferenceResolver) startAt(successesRemaining int64) *errorAfterNReferenceResolver {
	f.successesRemaining.Store(successesRemaining)
	return f
}

func (f *errorAfterNReferenceResolver) ResolveReference(ctx context.Context, namespace, name string, result repository.Object) error {
	if f.successesRemaining.Load() > 0 {
		f.successesRemaining.Add(-1)
		return f.r.ResolveReference(ctx, namespace, name, result)
	}
	return f.err
}

type fakeRepositoryOpener struct {
	repository repository.Repository
}

func (f *fakeRepositoryOpener) OpenRepository(ctx context.Context, repositorySpec *configapi.Repository) (repository.Repository, error) {
	return f.repository, nil
}
