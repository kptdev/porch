package packagerevision

import (
	"testing"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/stretchr/testify/assert"
)

func TestGetCloneFromSourceClone(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "source.pkg.v1"},
				},
			},
		},
	}

	result := r.getCloneFrom(pr)
	assert.Equal(t, "source.pkg.v1", result.UpstreamRef.Name)
}

func TestGetCloneFromSubpackageClone(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "source.pkg.v1"},
				},
			},
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "subpkg-upstream.pkg.v1"},
				},
			},
		},
	}

	result := r.getCloneFrom(pr)
	assert.Equal(t, "subpkg-upstream.pkg.v1", result.UpstreamRef.Name)
}

func TestGetCloneFromSubpackageCloneWithoutCreationSource(t *testing.T) {
	// SubpackageOperation.CloneFrom takes precedence regardless of CreationSource.
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "source.pkg.v1"},
				},
			},
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "subpkg-upstream.pkg.v1"},
				},
			},
		},
	}

	result := r.getCloneFrom(pr)
	assert.Equal(t, "subpkg-upstream.pkg.v1", result.UpstreamRef.Name)
}

func TestGetClonePackagenameSource(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName: "my-pkg",
		},
	}

	assert.Equal(t, "my-pkg", r.getClonePackagename(pr))
}

func TestGetClonePackagenameSubpackage(t *testing.T) {
	// Uses dot-separated full path (ComposeSubpkgObjName), not just the base name.
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName: "my-pkg",
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "level1/level2/my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
	}

	assert.Equal(t, "level1.level2.my-subpkg", r.getClonePackagename(pr))
}

func TestGetClonePackagenameSubpackageWithoutCreationSource(t *testing.T) {
	// CreationSource is no longer required — subpackage name is used regardless.
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName: "my-pkg",
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "level1/my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
	}

	assert.Equal(t, "level1.my-subpkg", r.getClonePackagename(pr))
}
