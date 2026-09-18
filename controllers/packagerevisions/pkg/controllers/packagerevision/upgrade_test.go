package packagerevision

import (
	"context"
	"testing"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestGetUpgradeFromSource(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream: porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
					NewUpstream: porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v2"},
				},
			},
		},
	}

	result := r.getUpgrade(pr)
	assert.Equal(t, "upstream.pkg.v1", result.OldUpstream.Name)
	assert.Equal(t, "upstream.pkg.v2", result.NewUpstream.Name)
}

func TestGetUpgradeFromSubpackageOperation(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream: porchv1alpha2.PackageRevisionRef{Name: "source-old"},
					NewUpstream: porchv1alpha2.PackageRevisionRef{Name: "source-new"},
				},
			},
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream: porchv1alpha2.PackageRevisionRef{Name: "subpkg-old"},
					NewUpstream: porchv1alpha2.PackageRevisionRef{Name: "subpkg-new"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			CreationSource: "init",
		},
	}

	result := r.getUpgrade(pr)
	assert.Equal(t, "subpkg-old", result.OldUpstream.Name)
	assert.Equal(t, "subpkg-new", result.NewUpstream.Name)
}

func TestGetUpgradeFromSubpackageWithoutCreationSource(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream: porchv1alpha2.PackageRevisionRef{Name: "source-old"},
					NewUpstream: porchv1alpha2.PackageRevisionRef{Name: "source-new"},
				},
			},
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream: porchv1alpha2.PackageRevisionRef{Name: "subpkg-old"},
					NewUpstream: porchv1alpha2.PackageRevisionRef{Name: "subpkg-new"},
				},
			},
		},
	}

	result := r.getUpgrade(pr)
	assert.Equal(t, "source-old", result.OldUpstream.Name)
}

func TestGetPackageRevisionForUpgradeSource(t *testing.T) {
	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "downstream.pkg.v1"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			obj.(*porchv1alpha2.PackageRevision).Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
			return nil
		})

	r := &PackageRevisionReconciler{Client: mc}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "downstream.pkg.v1"},
				},
			},
		},
	}

	result, err := r.getPackageRevisionForUpgrade(context.Background(), pr)
	require.NoError(t, err)
	assert.Equal(t, porchv1alpha2.PackageRevisionLifecyclePublished, result.Spec.Lifecycle)
}

func TestGetPackageRevisionForUpgradeSubpackage(t *testing.T) {
	mc := mockclient.NewMockClient(t)
	mc.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "local.pkg.ws"}, &porchv1alpha2.PackageRevision{}).
		RunAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			obj.(*porchv1alpha2.PackageRevision).Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDraft
			return nil
		})

	r := &PackageRevisionReconciler{Client: mc}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "should-not-use"},
				},
			},
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "local.pkg.ws"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	result, err := r.getPackageRevisionForUpgrade(context.Background(), pr)
	require.NoError(t, err)
	assert.Equal(t, porchv1alpha2.PackageRevisionLifecycleDraft, result.Spec.Lifecycle)
}

func TestGetPackageResourcesForUpgradeSourcePath(t *testing.T) {
	ctx := context.Background()

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(ctx).Return(map[string]string{
		"Kptfile":    "kptfile-content",
		"local.yaml": "local-content",
	}, nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(ctx, repository.RepositoryKey{Namespace: "default", Name: "my-repo"}, "my-pkg", "v1").Return(mockContent, nil)

	r := &PackageRevisionReconciler{ContentCache: mockCache}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
		},
	}

	resources, err := r.getPackageResourcesForUpgrade(ctx, pr)
	require.NoError(t, err)
	assert.Equal(t, "kptfile-content", resources["Kptfile"])
	assert.Equal(t, "local-content", resources["local.yaml"])
}

func TestGetPackageResourcesForUpgradeSubpackagePath(t *testing.T) {
	ctx := context.Background()

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(ctx).Return(map[string]string{
		"Kptfile":                "parent-kptfile",
		"parent.yaml":            "parent-resource",
		"my-subpkg/Kptfile":      "subpkg-kptfile",
		"my-subpkg/resource.yaml": "subpkg-resource",
	}, nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(ctx, repository.RepositoryKey{Namespace: "default", Name: "my-repo"}, "my-pkg", "v1").Return(mockContent, nil)

	r := &PackageRevisionReconciler{ContentCache: mockCache}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "local.pkg.ws"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	resources, err := r.getPackageResourcesForUpgrade(ctx, pr)
	require.NoError(t, err)
	assert.Equal(t, "subpkg-kptfile", resources["Kptfile"])
	assert.Equal(t, "subpkg-resource", resources["resource.yaml"])
	assert.NotContains(t, resources, "parent.yaml")
}

func TestGetPackageResourcesForUpgradeSubpackageNotFound(t *testing.T) {
	ctx := context.Background()

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(ctx).Return(map[string]string{
		"Kptfile":     "parent-kptfile",
		"parent.yaml": "parent-resource",
	}, nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(ctx, repository.RepositoryKey{Namespace: "default", Name: "my-repo"}, "my-pkg", "v1").Return(mockContent, nil)

	r := &PackageRevisionReconciler{ContentCache: mockCache}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "nonexistent-subpkg",
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: "local.pkg.ws"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	_, err := r.getPackageResourcesForUpgrade(ctx, pr)
	assert.ErrorContains(t, err, "not found in package")
}
