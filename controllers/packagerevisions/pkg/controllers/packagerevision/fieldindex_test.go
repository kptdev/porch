package packagerevision

import (
	"testing"

	"github.com/stretchr/testify/assert"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
)

func TestFieldIndexExtractors(t *testing.T) {
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
			RepositoryName: "my-repo",
			PackageName:    "my-pkg",
			WorkspaceName:  "ws-1",
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			Revision: 3,
		},
	}

	expected := map[porchv1alpha2.PkgRevFieldSelector]string{
		porchv1alpha2.PkgRevSelectorLifecycle:     "Published",
		porchv1alpha2.PkgRevSelectorRepository:    "my-repo",
		porchv1alpha2.PkgRevSelectorPackageName:   "my-pkg",
		porchv1alpha2.PkgRevSelectorWorkspaceName: "ws-1",
		porchv1alpha2.PkgRevSelectorRevision:      "3",
	}

	for _, idx := range fieldIndexes {
		t.Run(string(idx.field), func(t *testing.T) {
			got := idx.extract(pr)
			want, ok := expected[idx.field]
			assert.True(t, ok, "unexpected field index: %s", idx.field)
			assert.Equal(t, want, got)
		})
	}
}

func TestFieldIndexExtractorsEmpty(t *testing.T) {
	pr := &porchv1alpha2.PackageRevision{}

	expected := map[porchv1alpha2.PkgRevFieldSelector]string{
		porchv1alpha2.PkgRevSelectorLifecycle:     "",
		porchv1alpha2.PkgRevSelectorRepository:    "",
		porchv1alpha2.PkgRevSelectorPackageName:   "",
		porchv1alpha2.PkgRevSelectorWorkspaceName: "",
		porchv1alpha2.PkgRevSelectorRevision:      "0",
	}

	for _, idx := range fieldIndexes {
		t.Run(string(idx.field)+"_empty", func(t *testing.T) {
			got := idx.extract(pr)
			assert.Equal(t, expected[idx.field], got)
		})
	}
}

func TestFieldIndexCoversAllSelectors(t *testing.T) {
	// Ensure every non-metadata selector has an extractor.
	indexed := make(map[porchv1alpha2.PkgRevFieldSelector]bool)
	for _, idx := range fieldIndexes {
		indexed[idx.field] = true
	}

	for _, sel := range porchv1alpha2.PackageRevisionSelectableFields {
		if sel == porchv1alpha2.PkgRevSelectorName || sel == porchv1alpha2.PkgRevSelectorNamespace {
			continue // metadata fields are handled by k8s natively
		}
		assert.True(t, indexed[sel], "missing field index for selector %s", sel)
	}
}
