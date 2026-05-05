// Copyright 2026 The kpt and Nephio Authors
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

package packagerevision

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	"github.com/nephio-project/porch/pkg/repository"
	mockclient "github.com/nephio-project/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockrepository "github.com/nephio-project/porch/test/mockery/mocks/porch/pkg/repository"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestApplySourceInit(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName: "test-pkg",
			Source: &porchv1alpha2.PackageSource{
				Init: &porchv1alpha2.PackageInitSpec{
					Description: "a test package",
				},
			},
		},
	}

	resources, source, err := r.applySource(context.Background(), pr)
	require.NoError(t, err)
	assert.Equal(t, "init", source)
	assert.Contains(t, resources, "Kptfile")
	assert.Contains(t, resources["Kptfile"], "test-pkg")
	assert.Contains(t, resources["Kptfile"], "a test package")
}

func TestApplySourceSkipsWhenAlreadyCreated(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName: "test-pkg",
			Source: &porchv1alpha2.PackageSource{
				Init: &porchv1alpha2.PackageInitSpec{},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			CreationSource: "init",
		},
	}

	resources, source, err := r.applySource(context.Background(), pr)
	assert.NoError(t, err)
	assert.Nil(t, resources)
	assert.Empty(t, source)
}

func TestApplySourceSkipsWhenNoSource(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName: "test-pkg",
		},
	}

	resources, source, err := r.applySource(context.Background(), pr)
	assert.NoError(t, err)
	assert.Nil(t, resources)
	assert.Empty(t, source)
}

func TestInitPackage(t *testing.T) {
	resources, err := initPackage(context.Background(), "my-pkg", &porchv1alpha2.PackageInitSpec{
		Description: "my description",
		Keywords:    []string{"kw1", "kw2"},
		Site:        "https://example.com",
	})
	require.NoError(t, err)

	kptfile, ok := resources["Kptfile"]
	require.True(t, ok, "Kptfile should exist")
	assert.Contains(t, kptfile, "my-pkg")
	assert.Contains(t, kptfile, "my description")
}

func TestInitPackageEmpty(t *testing.T) {
	resources, err := initPackage(context.Background(), "empty-pkg", &porchv1alpha2.PackageInitSpec{})
	require.NoError(t, err)

	_, ok := resources["Kptfile"]
	assert.True(t, ok, "Kptfile should exist even with empty spec")
}

func TestApplySourceEmptySourceStruct(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName: "test-pkg",
			Source:      &porchv1alpha2.PackageSource{},
		},
	}

	_, _, err := r.applySource(context.Background(), pr)
	assert.ErrorContains(t, err, "source has no fields set")
}

func TestApplySourceCopy(t *testing.T) {
	ctx := context.Background()

	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "repo.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			src := obj.(*porchv1alpha2.PackageRevision)
			src.Spec.PackageName = "pkg"
			src.Spec.RepositoryName = "repo"
			src.Spec.WorkspaceName = "v1"
			src.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(ctx).Return(map[string]string{"Kptfile": "test-content"}, nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(ctx,
		repository.RepositoryKey{Namespace: "default", Name: "repo"}, "pkg", "v1",
	).Return(mockContent, nil)

	r := &PackageRevisionReconciler{Client: mc, ContentCache: mockCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "repo.pkg.v2", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "repo",
			WorkspaceName:  "v2",
			Source: &porchv1alpha2.PackageSource{
				CopyFrom: &porchv1alpha2.PackageRevisionRef{Name: "repo.pkg.v1"},
			},
		},
	}

	resources, source, err := r.applySource(ctx, pr)
	require.NoError(t, err)
	assert.Equal(t, "copy", source)
	assert.Equal(t, map[string]string{"Kptfile": "test-content"}, resources)
}

func TestApplySourceCopyDifferentRepo(t *testing.T) {
	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "other-repo.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			src := obj.(*porchv1alpha2.PackageRevision)
			src.Spec.RepositoryName = "other-repo"
			src.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})

	r := &PackageRevisionReconciler{Client: mc}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "repo.pkg.v2", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "repo",
			WorkspaceName:  "v2",
			Source: &porchv1alpha2.PackageSource{
				CopyFrom: &porchv1alpha2.PackageRevisionRef{Name: "other-repo.pkg.v1"},
			},
		},
	}

	_, _, err := r.applySource(context.Background(), pr)
	assert.ErrorContains(t, err, "same repository")
}

func TestApplySourceCopyNotPublished(t *testing.T) {
	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "repo.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			src := obj.(*porchv1alpha2.PackageRevision)
			src.Spec.PackageName = "pkg"
			src.Spec.RepositoryName = "repo"
			src.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDraft
			return nil
		})

	r := &PackageRevisionReconciler{Client: mc}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "repo.pkg.v2", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "repo",
			WorkspaceName:  "v2",
			Source: &porchv1alpha2.PackageSource{
				CopyFrom: &porchv1alpha2.PackageRevisionRef{Name: "repo.pkg.v1"},
			},
		},
	}

	_, _, err := r.applySource(context.Background(), pr)
	assert.ErrorContains(t, err, "must be published")
}

func TestApplySourceCopyNotFound(t *testing.T) {
	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "repo.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		Return(assert.AnError)

	r := &PackageRevisionReconciler{Client: mc}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "repo.pkg.v2", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "repo",
			WorkspaceName:  "v2",
			Source: &porchv1alpha2.PackageSource{
				CopyFrom: &porchv1alpha2.PackageRevisionRef{Name: "repo.pkg.v1"},
			},
		},
	}

	_, _, err := r.applySource(context.Background(), pr)
	assert.ErrorContains(t, err, "failed to get source package")
}

func TestApplySourceCopyDifferentPackageName(t *testing.T) {
	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "repo.other-pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			src := obj.(*porchv1alpha2.PackageRevision)
			src.Spec.PackageName = "other-pkg"
			src.Spec.RepositoryName = "repo"
			src.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})

	r := &PackageRevisionReconciler{Client: mc}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "repo.pkg.v2", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "repo",
			WorkspaceName:  "v2",
			Source: &porchv1alpha2.PackageSource{
				CopyFrom: &porchv1alpha2.PackageRevisionRef{Name: "repo.other-pkg.v1"},
			},
		},
	}

	_, _, err := r.applySource(context.Background(), pr)
	assert.ErrorContains(t, err, "same package")
}

func TestApplySourceCloneUpstreamRef(t *testing.T) {
	ctx := context.Background()

	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream-repo.upstream-pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			src := obj.(*porchv1alpha2.PackageRevision)
			src.Spec.PackageName = "upstream-pkg"
			src.Spec.RepositoryName = "upstream-repo"
			src.Spec.WorkspaceName = "v1"
			src.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(ctx).Return(map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: upstream-pkg\n",
	}, nil)
	mockContent.EXPECT().GetLock(ctx).Return(
		kptfilev1.Upstream{Type: kptfilev1.GitOrigin, Git: &kptfilev1.Git{Repo: "https://example.com/repo.git", Directory: "/upstream-pkg", Ref: "v1"}},
		kptfilev1.Locator{Type: kptfilev1.GitOrigin, Git: &kptfilev1.GitLock{Repo: "https://example.com/repo.git", Directory: "/upstream-pkg", Ref: "v1", Commit: "abc123"}},
		nil,
	)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(ctx,
		repository.RepositoryKey{Namespace: "default", Name: "upstream-repo"}, "upstream-pkg", "v1",
	).Return(mockContent, nil)

	r := &PackageRevisionReconciler{Client: mc, ContentCache: mockCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.my-pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream-repo.upstream-pkg.v1"},
				},
			},
		},
	}

	resources, source, err := r.applySource(ctx, pr)
	require.NoError(t, err)
	assert.Equal(t, "clone", source)
	assert.Contains(t, resources, "Kptfile")
	// Kptfile should have been updated with upstream info and renamed to my-pkg
	assert.Contains(t, resources["Kptfile"], "my-pkg")
}

func TestApplySourceCloneUpstreamRefNotPublished(t *testing.T) {
	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream-repo.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			src := obj.(*porchv1alpha2.PackageRevision)
			src.Spec.PackageName = "pkg"
			src.Spec.RepositoryName = "upstream-repo"
			src.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDraft
			return nil
		})

	r := &PackageRevisionReconciler{Client: mc}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream-repo.pkg.v1"},
				},
			},
		},
	}

	_, _, err := r.applySource(context.Background(), pr)
	assert.ErrorContains(t, err, "must be published")
}

func TestApplySourceCloneUpstreamRefNotFound(t *testing.T) {
	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream-repo.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		Return(assert.AnError)

	r := &PackageRevisionReconciler{Client: mc}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream-repo.pkg.v1"},
				},
			},
		},
	}

	_, _, err := r.applySource(context.Background(), pr)
	assert.ErrorContains(t, err, "failed to get upstream package")
}

func TestApplySourceCloneGit(t *testing.T) {
	ctx := context.Background()

	gitSpec := &porchv1alpha2.GitPackage{
		Repo:      "https://example.com/repo.git",
		Ref:       "v1",
		Directory: "/pkg",
	}

	mockFetcher := mockrepository.NewMockExternalPackageFetcher(t)
	mockFetcher.EXPECT().FetchExternalGitPackage(ctx, gitSpec, "default").Return(
		map[string]string{"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: pkg\n"},
		kptfilev1.GitLock{Repo: "https://example.com/repo.git", Directory: "/pkg", Ref: "v1", Commit: "abc123"},
		nil,
	)

	r := &PackageRevisionReconciler{ExternalPackageFetcher: mockFetcher}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.my-pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					Git: gitSpec,
				},
			},
		},
	}

	resources, source, err := r.applySource(ctx, pr)
	require.NoError(t, err)
	assert.Equal(t, "clone", source)
	assert.Contains(t, resources, "Kptfile")
	assert.Contains(t, resources["Kptfile"], "my-pkg")
}

func TestApplySourceCloneNoSourceSpecified(t *testing.T) {
	r := &PackageRevisionReconciler{}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{},
			},
		},
	}

	_, _, err := r.applySource(context.Background(), pr)
	assert.ErrorContains(t, err, "must specify either upstreamRef or git")
}

func TestApplySourceCloneUpstreamRefGetContentError(t *testing.T) {
	ctx := context.Background()

	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream-repo.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			src := obj.(*porchv1alpha2.PackageRevision)
			src.Spec.PackageName = "pkg"
			src.Spec.RepositoryName = "upstream-repo"
			src.Spec.WorkspaceName = "v1"
			src.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(ctx,
		repository.RepositoryKey{Namespace: "default", Name: "upstream-repo"}, "pkg", "v1",
	).Return(nil, assert.AnError)

	r := &PackageRevisionReconciler{Client: mc, ContentCache: mockCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream-repo.pkg.v1"},
				},
			},
		},
	}

	_, _, err := r.applySource(ctx, pr)
	assert.ErrorContains(t, err, "failed to get upstream package content")
}

func TestApplySourceCloneUpstreamRefGetLockError(t *testing.T) {
	ctx := context.Background()

	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream-repo.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			src := obj.(*porchv1alpha2.PackageRevision)
			src.Spec.PackageName = "pkg"
			src.Spec.RepositoryName = "upstream-repo"
			src.Spec.WorkspaceName = "v1"
			src.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(ctx).Return(map[string]string{"Kptfile": "test"}, nil)
	mockContent.EXPECT().GetLock(ctx).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, assert.AnError)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(ctx,
		repository.RepositoryKey{Namespace: "default", Name: "upstream-repo"}, "pkg", "v1",
	).Return(mockContent, nil)

	r := &PackageRevisionReconciler{Client: mc, ContentCache: mockCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream-repo.pkg.v1"},
				},
			},
		},
	}

	_, _, err := r.applySource(ctx, pr)
	assert.ErrorContains(t, err, "failed to get upstream lock")
}

func TestApplySourceCloneGitFetchError(t *testing.T) {
	ctx := context.Background()

	gitSpec := &porchv1alpha2.GitPackage{
		Repo:      "https://example.com/repo.git",
		Ref:       "v1",
		Directory: "/pkg",
	}

	mockFetcher := mockrepository.NewMockExternalPackageFetcher(t)
	mockFetcher.EXPECT().FetchExternalGitPackage(ctx, gitSpec, "default").
		Return(nil, kptfilev1.GitLock{}, assert.AnError)

	r := &PackageRevisionReconciler{ExternalPackageFetcher: mockFetcher}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					Git: gitSpec,
				},
			},
		},
	}

	_, _, err := r.applySource(ctx, pr)
	assert.ErrorContains(t, err, "failed to fetch from git")
}

func TestApplySourceCloneUpstreamRefGetResourcesError(t *testing.T) {
	ctx := context.Background()

	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream-repo.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			src := obj.(*porchv1alpha2.PackageRevision)
			src.Spec.PackageName = "pkg"
			src.Spec.RepositoryName = "upstream-repo"
			src.Spec.WorkspaceName = "v1"
			src.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(ctx).Return(nil, assert.AnError)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(ctx,
		repository.RepositoryKey{Namespace: "default", Name: "upstream-repo"}, "pkg", "v1",
	).Return(mockContent, nil)

	r := &PackageRevisionReconciler{Client: mc, ContentCache: mockCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream-repo.pkg.v1"},
				},
			},
		},
	}

	_, _, err := r.applySource(ctx, pr)
	assert.ErrorContains(t, err, "failed to read upstream resources")
}

func TestApplySourceCloneIdempotent(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName: "pkg",
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream-repo.pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			CreationSource: "clone",
		},
	}

	resources, source, err := r.applySource(context.Background(), pr)
	assert.NoError(t, err)
	assert.Nil(t, resources)
	assert.Empty(t, source)
}

func TestApplySourceUpgrade(t *testing.T) {
	ctx := context.Background()

	mc := mockclient.NewMockClient(t)
	// oldUpstream
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			pr := obj.(*porchv1alpha2.PackageRevision)
			pr.Namespace = "default"
			pr.Spec.PackageName = "pkg"
			pr.Spec.RepositoryName = "upstream"
			pr.Spec.WorkspaceName = "v1"
			pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})
	// newUpstream
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream.pkg.v2"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			pr := obj.(*porchv1alpha2.PackageRevision)
			pr.Namespace = "default"
			pr.Spec.PackageName = "pkg"
			pr.Spec.RepositoryName = "upstream"
			pr.Spec.WorkspaceName = "v2"
			pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})
	// currentPackage
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "downstream.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			pr := obj.(*porchv1alpha2.PackageRevision)
			pr.Namespace = "default"
			pr.Spec.PackageName = "pkg"
			pr.Spec.RepositoryName = "downstream"
			pr.Spec.WorkspaceName = "v1"
			pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})

	kptfileContent := "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: pkg\n"

	// oldUpstream content
	oldContent := mockrepository.NewMockPackageContent(t)
	oldContent.EXPECT().GetResourceContents(ctx).Return(map[string]string{"Kptfile": kptfileContent}, nil)

	// newUpstream content — used for both resources and lock
	newContent := mockrepository.NewMockPackageContent(t)
	newContent.EXPECT().GetResourceContents(ctx).Return(map[string]string{
		"Kptfile":  kptfileContent,
		"new.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: new\n",
	}, nil)
	newContent.EXPECT().GetLock(ctx).Return(
		kptfilev1.Upstream{Type: kptfilev1.GitOrigin, Git: &kptfilev1.Git{Repo: "https://example.com/upstream.git", Directory: "/pkg", Ref: "v2"}},
		kptfilev1.Locator{Type: kptfilev1.GitOrigin, Git: &kptfilev1.GitLock{Repo: "https://example.com/upstream.git", Directory: "/pkg", Ref: "v2", Commit: "def456"}},
		nil,
	)

	// currentPackage content
	currentContent := mockrepository.NewMockPackageContent(t)
	currentContent.EXPECT().GetResourceContents(ctx).Return(map[string]string{
		"Kptfile":    kptfileContent,
		"local.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: local\n",
	}, nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(ctx,
		repository.RepositoryKey{Namespace: "default", Name: "upstream"}, "pkg", "v1",
	).Return(oldContent, nil)
	mockCache.EXPECT().GetPackageContent(ctx,
		repository.RepositoryKey{Namespace: "default", Name: "upstream"}, "pkg", "v2",
	).Return(newContent, nil)
	mockCache.EXPECT().GetPackageContent(ctx,
		repository.RepositoryKey{Namespace: "default", Name: "downstream"}, "pkg", "v1",
	).Return(currentContent, nil)

	r := &PackageRevisionReconciler{Client: mc, ContentCache: mockCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "downstream.pkg.v2", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "downstream",
			WorkspaceName:  "v2",
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
					NewUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v2"},
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "downstream.pkg.v1"},
				},
			},
		},
	}

	resources, source, err := r.applySource(ctx, pr)
	require.NoError(t, err)
	assert.Equal(t, "upgrade", source)
	// Should contain both local and new upstream resources after merge.
	assert.Contains(t, resources, "local.yaml")
	assert.Contains(t, resources, "new.yaml")
	assert.Contains(t, resources, "Kptfile")
	// Kptfile should reference new upstream.
	assert.Contains(t, resources["Kptfile"], "v2")
}

func TestApplySourceUpgradeOldUpstreamNotFound(t *testing.T) {
	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		Return(assert.AnError)

	r := &PackageRevisionReconciler{Client: mc}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "downstream.pkg.v2", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "downstream",
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
					NewUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v2"},
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "downstream.pkg.v1"},
				},
			},
		},
	}

	_, _, err := r.applySource(context.Background(), pr)
	assert.ErrorContains(t, err, "old upstream")
	assert.ErrorContains(t, err, "failed to get package")
}

func TestApplySourceUpgradeNewUpstreamNotPublished(t *testing.T) {
	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			obj.(*porchv1alpha2.PackageRevision).Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream.pkg.v2"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			obj.(*porchv1alpha2.PackageRevision).Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDraft
			return nil
		})

	r := &PackageRevisionReconciler{Client: mc}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "downstream.pkg.v2", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "downstream",
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
					NewUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v2"},
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "downstream.pkg.v1"},
				},
			},
		},
	}

	_, _, err := r.applySource(context.Background(), pr)
	assert.ErrorContains(t, err, "new upstream")
	assert.ErrorContains(t, err, "must be published")
}

func TestApplySourceUpgradeCurrentPackageNotFound(t *testing.T) {
	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			obj.(*porchv1alpha2.PackageRevision).Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream.pkg.v2"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			obj.(*porchv1alpha2.PackageRevision).Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "downstream.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		Return(assert.AnError)

	r := &PackageRevisionReconciler{Client: mc}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "downstream.pkg.v2", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "downstream",
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
					NewUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v2"},
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "downstream.pkg.v1"},
				},
			},
		},
	}

	_, _, err := r.applySource(context.Background(), pr)
	assert.ErrorContains(t, err, "current package")
	assert.ErrorContains(t, err, "failed to get package")
}

func TestApplySourceUpgradeReadResourcesError(t *testing.T) {
	ctx := context.Background()

	mc := mockclient.NewMockClient(t)
	for _, name := range []string{"upstream.pkg.v1", "upstream.pkg.v2", "downstream.pkg.v1"} {
		mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: name}, &porchv1alpha2.PackageRevision{}).
			RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
				pr := obj.(*porchv1alpha2.PackageRevision)
				pr.Namespace = "default"
				pr.Spec.RepositoryName = "upstream"
				pr.Spec.PackageName = "pkg"
				pr.Spec.WorkspaceName = "v1"
				pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
				return nil
			})
	}

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(ctx,
		repository.RepositoryKey{Namespace: "default", Name: "upstream"}, "pkg", "v1",
	).Return(nil, assert.AnError)

	r := &PackageRevisionReconciler{Client: mc, ContentCache: mockCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "downstream.pkg.v2", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "downstream",
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
					NewUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v2"},
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "downstream.pkg.v1"},
				},
			},
		},
	}

	_, _, err := r.applySource(ctx, pr)
	assert.ErrorContains(t, err, "failed to read old upstream resources")
}

func TestApplySourceUpgradeGetLockError(t *testing.T) {
	ctx := context.Background()

	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			pr := obj.(*porchv1alpha2.PackageRevision)
			pr.Namespace = "default"
			pr.Spec.PackageName = "pkg"
			pr.Spec.RepositoryName = "upstream"
			pr.Spec.WorkspaceName = "v1"
			pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "upstream.pkg.v2"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			pr := obj.(*porchv1alpha2.PackageRevision)
			pr.Namespace = "default"
			pr.Spec.PackageName = "pkg"
			pr.Spec.RepositoryName = "upstream"
			pr.Spec.WorkspaceName = "v2"
			pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "downstream.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			pr := obj.(*porchv1alpha2.PackageRevision)
			pr.Namespace = "default"
			pr.Spec.PackageName = "pkg"
			pr.Spec.RepositoryName = "downstream"
			pr.Spec.WorkspaceName = "v1"
			pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})

	kptfileContent := "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: pkg\n"

	oldContent := mockrepository.NewMockPackageContent(t)
	oldContent.EXPECT().GetResourceContents(ctx).Return(map[string]string{"Kptfile": kptfileContent}, nil)

	// newUpstream content — GetResourceContents succeeds, GetLock fails.
	newContent := mockrepository.NewMockPackageContent(t)
	newContent.EXPECT().GetResourceContents(ctx).Return(map[string]string{"Kptfile": kptfileContent}, nil)
	newContent.EXPECT().GetLock(ctx).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, assert.AnError)

	currentContent := mockrepository.NewMockPackageContent(t)
	currentContent.EXPECT().GetResourceContents(ctx).Return(map[string]string{"Kptfile": kptfileContent}, nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(ctx,
		repository.RepositoryKey{Namespace: "default", Name: "upstream"}, "pkg", "v1",
	).Return(oldContent, nil)
	mockCache.EXPECT().GetPackageContent(ctx,
		repository.RepositoryKey{Namespace: "default", Name: "upstream"}, "pkg", "v2",
	).Return(newContent, nil)
	mockCache.EXPECT().GetPackageContent(ctx,
		repository.RepositoryKey{Namespace: "default", Name: "downstream"}, "pkg", "v1",
	).Return(currentContent, nil)

	r := &PackageRevisionReconciler{Client: mc, ContentCache: mockCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "downstream.pkg.v2", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "pkg",
			RepositoryName: "downstream",
			WorkspaceName:  "v2",
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
					NewUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v2"},
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "downstream.pkg.v1"},
				},
			},
		},
	}

	_, _, err := r.applySource(ctx, pr)
	assert.ErrorContains(t, err, "failed to get new upstream lock")
}

func TestApplySourceUpgradeIdempotent(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName: "pkg",
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
					NewUpstream:    porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v2"},
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "downstream.pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			CreationSource: "upgrade",
		},
	}

	resources, source, err := r.applySource(context.Background(), pr)
	assert.NoError(t, err)
	assert.Nil(t, resources)
	assert.Empty(t, source)
}

func TestStripKptfileStatus(t *testing.T) {
	kfWithStatus := `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: pkg
status:
  conditions:
  - type: Rendered
    status: "True"
    reason: Rendered
`
	resources := map[string]string{"Kptfile": kfWithStatus, "other.yaml": "data"}
	stripKptfileStatus(resources)

	assert.NotContains(t, resources["Kptfile"], "status:")
	assert.NotContains(t, resources["Kptfile"], "Rendered")
	assert.Contains(t, resources["Kptfile"], "name: pkg")
	assert.Equal(t, "data", resources["other.yaml"])
}

func TestStripKptfileStatusNoKptfile(t *testing.T) {
	resources := map[string]string{"other.yaml": "data"}
	stripKptfileStatus(resources)
	assert.Equal(t, "data", resources["other.yaml"])
}

func TestStripKptfileStatusNoStatus(t *testing.T) {
	kf := `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: pkg
`
	resources := map[string]string{"Kptfile": kf}
	stripKptfileStatus(resources)
	assert.Equal(t, kf, resources["Kptfile"])
}

func TestStripKptfileStatusMalformed(t *testing.T) {
	resources := map[string]string{"Kptfile": "not: valid: yaml: ["}
	stripKptfileStatus(resources)
	assert.Equal(t, "not: valid: yaml: [", resources["Kptfile"])
}

func TestCopyResources(t *testing.T) {
	src := map[string]string{"a": "1", "b": "2"}
	dst := copyResources(src)

	assert.Equal(t, src, dst)
	dst["a"] = "modified"
	assert.Equal(t, "1", src["a"], "original should not be modified")
}
