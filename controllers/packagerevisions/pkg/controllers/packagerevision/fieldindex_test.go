package packagerevision

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"

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

func TestMultiFieldIndexUpstreamKeys(t *testing.T) {
	pr := &porchv1alpha2.PackageRevision{
		Status: porchv1alpha2.PackageRevisionStatus{
			UpstreamKeys: []string{"https://v.com/v.git|net-bp|v2", "https://v.com/v.git|cmp-bp|v3"},
		},
	}
	require.Len(t, multiFieldIndexes, 1)
	assert.Equal(t, porchv1alpha2.PkgRevSelectorUpstreamKey, multiFieldIndexes[0].field)
	assert.Equal(t, pr.Status.UpstreamKeys, multiFieldIndexes[0].extract(pr))
}

// TestReverseQueryViaMultiValuedIndex proves the reverse dependency query:
// an indexed List by upstream key returns both sub-package and top-level
// dependents, and excludes unrelated packages.
func TestReverseQueryViaMultiValuedIndex(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, porchv1alpha2.AddToScheme(scheme))

	vendorKey := porchv1alpha2.UpstreamKey(idxLoc("https://vendor.com/v.git", "net-bp", "v2"))

	// Depends on vendor v2 as a SUB-PACKAGE (missed by the old name-based guard).
	customA := idxPR("custom-a")
	customA.Status.SubpackageUpstreams = []porchv1alpha2.SubpackageUpstream{
		{Path: "sub/net", Upstream: idxLoc("https://vendor.com/v.git", "net-bp", "v2")},
	}
	customA.Status.UpstreamKeys = customA.ComputeUpstreamKeys()

	// Depends on vendor v2 as its TOP-LEVEL upstream.
	deployB := idxPR("deploy-b")
	deployB.Status.UpstreamLock = idxLoc("https://vendor.com/v.git", "net-bp", "v2")
	deployB.Status.UpstreamKeys = deployB.ComputeUpstreamKeys()

	// Depends on a DIFFERENT blueprint — must not match.
	customC := idxPR("custom-c")
	customC.Status.UpstreamLock = idxLoc("https://vendor.com/v.git", "cmp-bp", "v3")
	customC.Status.UpstreamKeys = customC.ComputeUpstreamKeys()

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(customA, deployB, customC).
		WithIndex(&porchv1alpha2.PackageRevision{}, string(porchv1alpha2.PkgRevSelectorUpstreamKey),
			func(o client.Object) []string { return o.(*porchv1alpha2.PackageRevision).Status.UpstreamKeys }).
		Build()

	var list porchv1alpha2.PackageRevisionList
	require.NoError(t, c.List(context.Background(), &list,
		client.MatchingFields{string(porchv1alpha2.PkgRevSelectorUpstreamKey): vendorKey}))

	names := map[string]bool{}
	for _, pr := range list.Items {
		names[pr.Name] = true
	}
	assert.True(t, names["custom-a"], "sub-package dependent found")
	assert.True(t, names["deploy-b"], "top-level dependent found")
	assert.False(t, names["custom-c"], "unrelated excluded")
	assert.Len(t, list.Items, 2)
}

// fakeIndexer records the fields registered via IndexField and invokes each
// extractValue closure so the closures built in setupFieldIndexes are exercised.
// If failOn is set, IndexField returns an error for that field.
type fakeIndexer struct {
	registered []string
	failOn     string
}

func (f *fakeIndexer) IndexField(_ context.Context, _ client.Object, field string, extract client.IndexerFunc) error {
	if field == f.failOn {
		return assert.AnError
	}
	// Exercise the extractor closure (covers the obj type assertion).
	extract(&porchv1alpha2.PackageRevision{
		Spec:   porchv1alpha2.PackageRevisionSpec{RepositoryName: "r"},
		Status: porchv1alpha2.PackageRevisionStatus{UpstreamKeys: []string{"k"}},
	})
	f.registered = append(f.registered, field)
	return nil
}

// fakeIndexerManager is a ctrl.Manager that only serves a FieldIndexer.
type fakeIndexerManager struct {
	manager.Manager
	indexer *fakeIndexer
}

func (m *fakeIndexerManager) GetFieldIndexer() client.FieldIndexer { return m.indexer }

func TestSetupFieldIndexes(t *testing.T) {
	idxr := &fakeIndexer{}
	mgr := &fakeIndexerManager{indexer: idxr}

	require.NoError(t, setupFieldIndexes(mgr))

	// Every single- and multi-valued field must have been registered.
	got := make(map[string]bool)
	for _, f := range idxr.registered {
		got[f] = true
	}
	for _, idx := range fieldIndexes {
		assert.True(t, got[string(idx.field)], "single field not registered: %s", idx.field)
	}
	for _, idx := range multiFieldIndexes {
		assert.True(t, got[string(idx.field)], "multi field not registered: %s", idx.field)
	}
}

func TestSetupFieldIndexesSingleFieldError(t *testing.T) {
	mgr := &fakeIndexerManager{indexer: &fakeIndexer{failOn: string(fieldIndexes[0].field)}}
	err := setupFieldIndexes(mgr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), string(fieldIndexes[0].field))
}

func TestSetupFieldIndexesMultiFieldError(t *testing.T) {
	mgr := &fakeIndexerManager{indexer: &fakeIndexer{failOn: string(multiFieldIndexes[0].field)}}
	err := setupFieldIndexes(mgr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), string(multiFieldIndexes[0].field))
}

func idxLoc(repo, dir, ref string) *porchv1alpha2.Locator {
	return &porchv1alpha2.Locator{Type: "git", Git: &porchv1alpha2.GitLock{Repo: repo, Directory: dir, Ref: ref, Commit: "c"}}
}

func idxPR(name string) *porchv1alpha2.PackageRevision {
	return &porchv1alpha2.PackageRevision{
		TypeMeta:   metav1.TypeMeta{Kind: "PackageRevision", APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier()},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns1"},
	}
}
