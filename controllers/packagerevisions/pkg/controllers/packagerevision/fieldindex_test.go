package packagerevision

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/webhooks"
)

// recordingIndexer captures the index functions registered through IndexField.
type recordingIndexer struct {
	funcs map[string]client.IndexerFunc
	err   error
	// failOn limits err to one field. An empty value fails every registration.
	failOn string
}

func (r *recordingIndexer) IndexField(_ context.Context, _ client.Object, field string, fn client.IndexerFunc) error {
	if r.err != nil && (r.failOn == "" || r.failOn == field) {
		return r.err
	}
	if r.funcs == nil {
		r.funcs = map[string]client.IndexerFunc{}
	}
	r.funcs[field] = fn
	return nil
}

func TestSetupFieldIndexesRegistersAllIndexes(t *testing.T) {
	idx := &recordingIndexer{}
	require := assert.New(t)
	require.NoError(setupFieldIndexes(idx))

	want := []string{
		string(porchv1alpha2.PkgRevSelectorLifecycle),
		string(porchv1alpha2.PkgRevSelectorRepository),
		string(porchv1alpha2.PkgRevSelectorPackageName),
		string(porchv1alpha2.PkgRevSelectorWorkspaceName),
		string(porchv1alpha2.PkgRevSelectorRevision),
		webhooks.UpstreamNameIndex,
	}
	require.Len(idx.funcs, len(want))
	pr := &porchv1alpha2.PackageRevision{
		Spec:   porchv1alpha2.PackageRevisionSpec{RepositoryName: "repo", PackageName: "pkg", WorkspaceName: "ws", Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
		Status: porchv1alpha2.PackageRevisionStatus{Revision: 2},
	}
	for _, field := range want {
		require.Contains(idx.funcs, field)
		// Every registered function must run without error on a PackageRevision.
		require.NotPanics(func() { _ = idx.funcs[field](pr) })
	}
}

func TestSetupFieldIndexesUpstreamNameExtraction(t *testing.T) {
	idx := &recordingIndexer{}
	assert.NoError(t, setupFieldIndexes(idx))

	pr := &porchv1alpha2.PackageRevision{Spec: porchv1alpha2.PackageRevisionSpec{
		Source: &porchv1alpha2.PackageSource{
			CloneFrom: &porchv1alpha2.UpstreamPackage{UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "vendor.v1"}},
		},
	}}
	got := idx.funcs[webhooks.UpstreamNameIndex](pr)
	assert.Equal(t, []string{"vendor.v1"}, got)
}

func TestSetupFieldIndexesPropagatesError(t *testing.T) {
	boom := errors.New("boom")

	err := setupFieldIndexes(&recordingIndexer{err: boom})
	assert.ErrorIs(t, err, boom)
	assert.ErrorContains(t, err, "failed to index field")

	// Failing only the upstream name index must still surface the error.
	err = setupFieldIndexes(&recordingIndexer{err: boom, failOn: webhooks.UpstreamNameIndex})
	assert.ErrorIs(t, err, boom)
	assert.ErrorContains(t, err, webhooks.UpstreamNameIndex)
}

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

func TestUpstreamNames(t *testing.T) {
	tests := []struct {
		name string
		pr   *porchv1alpha2.PackageRevision
		want []string
	}{
		{
			name: "no source",
			pr:   &porchv1alpha2.PackageRevision{},
			want: nil,
		},
		{
			name: "clone from registered repository",
			pr: &porchv1alpha2.PackageRevision{Spec: porchv1alpha2.PackageRevisionSpec{Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "vendor.v1"}},
			}}},
			want: []string{"vendor.v1"},
		},
		{
			name: "upgrade reports the new upstream only",
			pr: &porchv1alpha2.PackageRevision{Spec: porchv1alpha2.PackageRevisionSpec{Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream: porchv1alpha2.PackageRevisionRef{Name: "vendor.v1"},
					NewUpstream: porchv1alpha2.PackageRevisionRef{Name: "vendor.v2"},
				},
			}}},
			want: []string{"vendor.v2"},
		},
		{
			name: "copy is not tracked",
			pr: &porchv1alpha2.PackageRevision{Spec: porchv1alpha2.PackageRevisionSpec{Source: &porchv1alpha2.PackageSource{
				CopyFrom: &porchv1alpha2.PackageRevisionRef{Name: "source.v1"},
			}}},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, upstreamNames(tt.pr))
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
