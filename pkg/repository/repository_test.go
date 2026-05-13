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

package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

func TestRepositoryKey(t *testing.T) {
	repoKey := RepositoryKey{
		Namespace:         "my-ns",
		Name:              "my-repo",
		Path:              "my/dir/path",
		PlaceholderWSname: "my-ws-name",
	}

	assert.Equal(t, "my-ns:my-repo:my/dir/path:my-ws-name", repoKey.String())
	assert.Equal(t, repoKey, repoKey)

	otherRepoKey := RepositoryKey{}
	assert.True(t, otherRepoKey.Matches(repoKey))

	otherRepoKey.Namespace = "other-ns"
	otherRepoKey.Name = "other-repo"
	otherRepoKey.Path = "other/dir/path"
	otherRepoKey.PlaceholderWSname = "other-ws-name"
	assert.False(t, otherRepoKey.Matches(repoKey))

	otherRepoKey.Namespace = "my-ns"
	assert.False(t, otherRepoKey.Matches(repoKey))

	otherRepoKey.Name = "my-repo"
	assert.False(t, otherRepoKey.Matches(repoKey))

	otherRepoKey.Path = "my/dir/path"
	assert.False(t, otherRepoKey.Matches(repoKey))

	otherRepoKey.Path = "my/dir/path"
	otherRepoKey.PlaceholderWSname = "my-ws-name"
	assert.True(t, otherRepoKey.Matches(repoKey))

	copiedRepoKey := RepositoryKey{}
	repoKey.DeepCopy(&copiedRepoKey)
	assert.Equal(t, copiedRepoKey, repoKey)
}

func TestPackageKey(t *testing.T) {
	pkgKey := PackageKey{
		Path:    "my/pkg/path",
		Package: "my-package-name",
	}

	assert.Equal(t, "::::my/pkg/path:my-package-name", pkgKey.String())
	assert.Equal(t, pkgKey, pkgKey)

	otherPkgKey := PackageKey{}
	assert.True(t, otherPkgKey.Matches(pkgKey))

	otherPkgKey.Path = "other/pkg/path"
	otherPkgKey.Package = "other-ws-name"
	assert.False(t, otherPkgKey.Matches(pkgKey))

	otherPkgKey.Path = "my/pkg/path"
	assert.False(t, otherPkgKey.Matches(pkgKey))

	otherPkgKey.Package = "my-package-name"
	assert.True(t, otherPkgKey.Matches(pkgKey))

	assert.Equal(t, "my/pkg/path/my-package-name", pkgKey.ToPkgPathname())
	assert.Equal(t, "my/pkg/path/my-package-name", pkgKey.ToFullPathname())

	pkgKey.RepoKey.Path = "dir/path"
	assert.Equal(t, "dir/path/my/pkg/path/my-package-name", pkgKey.ToFullPathname())

	testRepoKey := RepositoryKey{
		Namespace:         "ns",
		Name:              "repo",
		Path:              "dir/path",
		PlaceholderWSname: "ws-name",
	}
	pkgKey.RepoKey = testRepoKey
	assert.Equal(t, pkgKey, FromFullPathname(testRepoKey, pkgKey.ToPkgPathname()))
	assert.Equal(t, pkgKey, FromFullPathname(pkgKey.RKey(), pkgKey.ToPkgPathname()))

	assert.Equal(t, "ns", pkgKey.K8SNS())
	assert.Equal(t, "repo.my.pkg.path.my-package-name", pkgKey.K8SName())

	pkgKey.RepoKey = RepositoryKey{}
	assert.Equal(t, pkgKey, FromFullPathname(pkgKey.RKey(), pkgKey.ToPkgPathname()))

	pkgKey.Path = ""
	assert.Equal(t, pkgKey, FromFullPathname(pkgKey.RKey(), pkgKey.ToPkgPathname()))

	copiedPkgKey := PackageKey{}
	pkgKey.DeepCopy(&copiedPkgKey)
	assert.Equal(t, copiedPkgKey, pkgKey)

	parPRKey, err := PkgK8sName2Key("my-ns", "repo.pkg.path.package-name.my-ws-name")
	assert.Nil(t, err)
	assert.Equal(t, "repo", parPRKey.RKey().Name)

	_, err = PkgK8sName2Key("my-ns", "aaa")
	assert.NotNil(t, err)

	assert.Equal(t, "pkg", K8SName2PkgName("repo.pkg"))
}

func TestPackageRevisionKey(t *testing.T) {
	pkgRevKey := PackageRevisionKey{
		Revision:      1,
		WorkspaceName: "my-ws-name",
	}

	assert.Equal(t, "::::::1:my-ws-name", pkgRevKey.String())
	assert.Equal(t, pkgRevKey, pkgRevKey)

	otherPkgRevKey := PackageRevisionKey{}
	assert.True(t, otherPkgRevKey.Matches(pkgRevKey))

	otherPkgRevKey.Revision = 2
	otherPkgRevKey.WorkspaceName = "other-ws-name"
	assert.False(t, otherPkgRevKey.Matches(pkgRevKey))

	otherPkgRevKey.Revision = 1
	assert.False(t, otherPkgRevKey.Matches(pkgRevKey))

	otherPkgRevKey.WorkspaceName = "my-ws-name"
	assert.True(t, otherPkgRevKey.Matches(pkgRevKey))

	testPkgKey := PackageKey{
		Path:    "pkg/path",
		Package: "package-name",
	}
	pkgRevKey.PkgKey = testPkgKey
	assert.Equal(t, testPkgKey, pkgRevKey.PKey())

	testRepoKey := RepositoryKey{
		Namespace:         "ns",
		Name:              "repo",
		Path:              "dir/path",
		PlaceholderWSname: "ws-name",
	}
	pkgRevKey.PkgKey.RepoKey = testRepoKey
	assert.Equal(t, testRepoKey, pkgRevKey.RKey())

	copiedPkgRevKey := PackageRevisionKey{}
	pkgRevKey.DeepCopy(&copiedPkgRevKey)
	assert.Equal(t, copiedPkgRevKey, pkgRevKey)

	assert.Equal(t, "ns", pkgRevKey.K8SNS())
	assert.Equal(t, "repo.pkg.path.package-name.my-ws-name", pkgRevKey.K8SName())
	assert.Equal(t, "ws-name", K8SName2PkgRevWSName("pkg-name", "pkg-name.ws-name"))

	parPRKey, err := PkgRevK8sName2Key("my-ns", "repo.pkg.path.package-name.my-ws-name")
	assert.Nil(t, err)
	assert.Equal(t, "repo", parPRKey.RKey().Name)

	_, err = PkgRevK8sName2Key("my-ns", "")
	assert.NotNil(t, err)
}

func TestGetPRWorkspaceName(t *testing.T) {
	_, err := PkgRevK8sName2Key("my-ns", "")
	assert.NotNil(t, err)
	assert.True(t, strings.Contains(err.Error(), "package name part \"\" of object name invalid"))

	_, err = PkgRevK8sName2Key("my-ns", "hello")
	assert.NotNil(t, err)
	assert.True(t, strings.Contains(err.Error(), "package name part \"\" of object name invalid"))

	prKey, err := PkgRevK8sName2Key("my-ns", "repo.hello.there")
	assert.Nil(t, err)
	assert.Equal(t, "hello", prKey.PkgKey.Package)
	assert.Equal(t, "", prKey.PkgKey.Path)
	assert.Equal(t, "there", prKey.WorkspaceName)

	prKey, err = PkgRevK8sName2Key("my-ns", "repo..")
	assert.NotNil(t, err)
	assert.True(t, strings.Contains(err.Error(), "package name part \"\" of object name invalid"))

	prKey, err = PkgRevK8sName2Key("my-ns", "v.")
	assert.NotNil(t, err)
	assert.True(t, strings.Contains(err.Error(), "workspace name part \"\" of package revision name invalid"))

	prKey, err = PkgRevK8sName2Key("my-ns", "repo.hello.v1.2.3")
	assert.Nil(t, err)
	assert.Equal(t, "hello", prKey.PkgKey.Package)
	assert.Equal(t, "", prKey.PkgKey.Path)
	assert.Equal(t, "v1.2.3", prKey.WorkspaceName)

	prKey, err = PkgRevK8sName2Key("my-ns", "repo.hello.v1.2")
	assert.Nil(t, err)
	assert.Equal(t, "hello", prKey.PkgKey.Package)
	assert.Equal(t, "", prKey.PkgKey.Path)
	assert.Equal(t, "v1.2", prKey.WorkspaceName)

	prKey, err = PkgRevK8sName2Key("my-ns", "repo.hello.v1")
	assert.Nil(t, err)
	assert.Equal(t, "hello", prKey.PkgKey.Package)
	assert.Equal(t, "", prKey.PkgKey.Path)
	assert.Equal(t, "v1", prKey.WorkspaceName)

	prKey, err = PkgRevK8sName2Key("my-ns", "repo.hello.v1.v1")
	assert.Nil(t, err)
	assert.Equal(t, "v1", prKey.PkgKey.Package)
	assert.Equal(t, "hello", prKey.PkgKey.Path)
	assert.Equal(t, "v1", prKey.WorkspaceName)

	prKey, err = PkgRevK8sName2Key("my-ns", "repo.hello.v1.2.3.v4.5.6")
	assert.Nil(t, err)
	assert.Equal(t, "3", prKey.PkgKey.Package)
	assert.Equal(t, "hello/v1/2", prKey.PkgKey.Path)
	assert.Equal(t, "v4.5.6", prKey.WorkspaceName)

	prKey, err = PkgRevK8sName2Key("my-ns", "repo.hello.v1.2.3.end")
	assert.Nil(t, err)
	assert.Equal(t, "3", prKey.PkgKey.Package)
	assert.Equal(t, "hello/v1/2", prKey.PkgKey.Path)
	assert.Equal(t, "end", prKey.WorkspaceName)
}

func TestRepositoryKey_K8SNS(t *testing.T) {
	k := RepositoryKey{
		Namespace: "foo",
	}
	require.Equal(t, "foo", k.K8SNS())
}

func TestRepositoryKey_K8SName(t *testing.T) {
	k := RepositoryKey{
		Name: "foo",
	}
	require.Equal(t, "foo", k.K8SName())
}

func TestListPackageRevisionFilter_Matches(t *testing.T) {
	tests := []struct {
		name     string
		filter   ListPackageRevisionFilter
		p        PackageRevision
		negative bool
	}{
		{
			name:   "key matches",
			filter: ListPackageRevisionFilter{Key: PackageRevisionKey{PkgKey: PackageKey{RepoKey: RepositoryKey{Namespace: "ns"}}}},
			p:      &fakePackageRevision{namespace: "ns"},
		},
		{
			name:     "key doesn't match",
			filter:   ListPackageRevisionFilter{Key: PackageRevisionKey{PkgKey: PackageKey{RepoKey: RepositoryKey{Namespace: "ns2"}}}},
			p:        &fakePackageRevision{namespace: "ns"},
			negative: true,
		},
		{
			name:   "lifecycle matches",
			filter: ListPackageRevisionFilter{Lifecycles: []porchapi.PackageRevisionLifecycle{"Published"}},
			p:      &fakePackageRevision{lifecycle: porchapi.PackageRevisionLifecyclePublished},
		},
		{
			name:     "lifecycle doesn't match",
			filter:   ListPackageRevisionFilter{Lifecycles: []porchapi.PackageRevisionLifecycle{"Published"}},
			p:        &fakePackageRevision{lifecycle: porchapi.PackageRevisionLifecycleDeletionProposed},
			negative: true,
		},
		{
			name:   "repository matches",
			filter: ListPackageRevisionFilter{Key: PackageRevisionKey{PkgKey: PackageKey{RepoKey: RepositoryKey{Name: "someRepo"}}}},
			p:      &fakePackageRevision{repoName: "someRepo"},
		},
		{
			name:     "repository doesn't match",
			filter:   ListPackageRevisionFilter{Key: PackageRevisionKey{PkgKey: PackageKey{RepoKey: RepositoryKey{Name: "someReopWithAMisspelling"}}}},
			p:        &fakePackageRevision{repoName: "someRepo"},
			negative: true,
		},
		{
			name:   "name matches",
			filter: ListPackageRevisionFilter{Key: func() PackageRevisionKey { key, _ := PkgRevK8sName2Key("", "somePackageRevision"); return key }()},
			p:      &fakePackageRevision{name: "somePackageRevision"},
		},
		{
			name:   "revision matches",
			filter: ListPackageRevisionFilter{Key: PackageRevisionKey{Revision: 1}},
			p:      &fakePackageRevision{revision: 1},
		},
		{
			name:   "package name matches",
			filter: ListPackageRevisionFilter{Key: PackageRevisionKey{PkgKey: PackageKey{Package: "someSortOfRadio"}}},
			p:      &fakePackageRevision{packageName: "someSortOfRadio"},
		},
		{
			name:   "multi-folder package name matches",
			filter: ListPackageRevisionFilter{Key: PackageRevisionKey{PkgKey: PackageKey{Path: "someSortOfNetwork", Package: "someSortOfRadio"}}},
			p:      &fakePackageRevision{packagePath: "someSortOfNetwork", packageName: "someSortOfRadio"},
		},
		{
			name:   "workspace name matches",
			filter: ListPackageRevisionFilter{Key: PackageRevisionKey{WorkspaceName: "main"}},
			p:      &fakePackageRevision{workspaceName: "main"},
		},
		{
			name:   "filter on label",
			filter: ListPackageRevisionFilter{Label: labels.Set{"kpt.dev/someLabel": "foo"}.AsSelector()},
			p: &fakePackageRevision{
				labels: labels.Set{"kpt.dev/someLabel": "foo"},
			},
		},
		{
			name:   "filter on kpt.dev/latest-revision label (special case)",
			filter: ListPackageRevisionFilter{Label: labels.Set{"kpt.dev/latest-revision": "true"}.AsSelector()},
			p:      &fakePackageRevision{isLatest: true},
		},
		{
			name:   "filter on kpt.dev/latest-revision label == false (special case)",
			filter: ListPackageRevisionFilter{},
			p:      &fakePackageRevision{isLatest: false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.negative {
				require.False(t, tt.filter.Matches(context.TODO(), tt.p))
			} else {
				require.True(t, tt.filter.Matches(context.TODO(), tt.p))
			}
		})
	}
}

func TestListPackageRevisionFilter_MatchesNamespace(t *testing.T) {
	// SETUP test cases with varying selectors for a packageRevisionFilter
	//********************************************************************
	tests := []struct {
		name                  string
		filter                *ListPackageRevisionFilter
		wantMatches           bool
		wantFilteredNamespace string
	}{
		{
			name:                  "nil key",
			filter:                &ListPackageRevisionFilter{},
			wantMatches:           true,
			wantFilteredNamespace: "",
		},
		{
			name:                  "matching key",
			filter:                &ListPackageRevisionFilter{Key: PackageRevisionKey{PkgKey: PackageKey{RepoKey: RepositoryKey{Namespace: "foo"}}}},
			wantMatches:           true,
			wantFilteredNamespace: "foo",
		},
		{
			name:                  "non-matching key",
			filter:                &ListPackageRevisionFilter{Key: PackageRevisionKey{PkgKey: PackageKey{RepoKey: RepositoryKey{Namespace: "bar"}}}},
			wantMatches:           false,
			wantFilteredNamespace: "bar",
		},
		{
			name:                  "different key",
			filter:                &ListPackageRevisionFilter{Key: PackageRevisionKey{PkgKey: PackageKey{Package: "something"}}},
			wantMatches:           true,
			wantFilteredNamespace: "",
		},
	}

	for _, tt := range tests {
		// GIVEN a packageRevisionFilter selecting on the specified PackageRevisionKey
		//****************************************************************************
		t.Run(tt.name, func(t *testing.T) {
			filter := tt.filter

			// WHEN we check if the filter matches a particular namespace
			//***********************************************************
			gotMatches, gotFilteredNamespace := filter.MatchesNamespace("foo")

			// THEN the filter returns expected values for whether the namespace
			//      matches, and what namespace is being matched against
			//******************************************************************
			require.Equal(t, tt.wantMatches, gotMatches)
			require.Equal(t, tt.wantFilteredNamespace, gotFilteredNamespace)
		})
	}
}

func TestListPackageRevisionFilter_FilteredRepository(t *testing.T) {
	// SETUP test cases with varying selectors for a packageRevisionFilter
	//********************************************************************
	tests := []struct {
		name             string
		filter           *ListPackageRevisionFilter
		wantFilteredRepo string
	}{
		{
			name:             "nil key",
			filter:           &ListPackageRevisionFilter{},
			wantFilteredRepo: "",
		},
		{
			name:             "matching key",
			filter:           &ListPackageRevisionFilter{Key: PackageRevisionKey{PkgKey: PackageKey{RepoKey: RepositoryKey{Name: "foo"}}}},
			wantFilteredRepo: "foo",
		},
		{
			name:             "different key",
			filter:           &ListPackageRevisionFilter{Key: PackageRevisionKey{PkgKey: PackageKey{Package: "something"}}},
			wantFilteredRepo: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN a packageRevisionFilter selecting on the specified filter
			//****************************************************************
			// WHEN we get the filter's repository selector
			//*********************************************
			gotFilteredRepo := tt.filter.FilteredRepository()

			// THEN the filter returns expected values for the repo being matched against
			//***************************************************************************
			require.Equal(t, tt.wantFilteredRepo, gotFilteredRepo)
		})
	}
}

func TestListPackageFilter_Matches(t *testing.T) {
	f := &ListPackageFilter{Key: PackageKey{RepoKey: RepositoryKey{Namespace: "ns"}}}
	p := &fakePackage{namespace: "ns"}
	require.True(t, f.Matches(p))
}

type fakePackageRevision struct {
	name          string
	namespace     string
	labels        map[string]string
	lifecycle     porchapi.PackageRevisionLifecycle
	packagePath   string
	packageName   string
	revision      int
	repoName      string
	workspaceName string
	isLatest      bool
}

func (f *fakePackageRevision) GetPackageRevision(ctx context.Context) (*porchapi.PackageRevision, error) {
	return &porchapi.PackageRevision{ObjectMeta: metav1.ObjectMeta{Namespace: f.namespace}}, nil
}
func (f *fakePackageRevision) KubeObjectNamespace() string { return f.namespace }
func (f *fakePackageRevision) Key() PackageRevisionKey {
	return PackageRevisionKey{
		PkgKey: PackageKey{
			RepoKey: RepositoryKey{
				Namespace: f.namespace,
				Name:      f.repoName,
			},
			Path:    f.packagePath,
			Package: f.packageName,
		},
		Revision:      f.revision,
		WorkspaceName: f.workspaceName,
	}
}
func (f *fakePackageRevision) KubeObjectName() string                           { return f.name }
func (f *fakePackageRevision) UID() types.UID                                   { return "" }
func (f *fakePackageRevision) SetMeta(context.Context, metav1.ObjectMeta) error { return nil }
func (f *fakePackageRevision) ResourceVersion() string                          { return "" }
func (f *fakePackageRevision) Lifecycle(context.Context) porchapi.PackageRevisionLifecycle {

	return porchapi.PackageRevisionLifecycle(f.lifecycle)
}
func (f *fakePackageRevision) GetResources(context.Context) (*porchapi.PackageRevisionResources, error) {
	return nil, nil
}
func (f *fakePackageRevision) UpdateLifecycle(context.Context, porchapi.PackageRevisionLifecycle) error {
	return nil
}
func (f *fakePackageRevision) GetUpstreamLock(context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	return kptfilev1.Upstream{}, kptfilev1.Locator{}, nil
}
func (f *fakePackageRevision) GetKptfile(context.Context) (kptfilev1.KptFile, error) {
	return kptfilev1.KptFile{}, nil
}
func (f *fakePackageRevision) GetLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	return kptfilev1.Upstream{}, kptfilev1.Locator{}, nil
}
func (f *fakePackageRevision) ToMainPackageRevision(context.Context) PackageRevision {
	return f
}
func (f *fakePackageRevision) GetMeta() metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: f.namespace, Labels: f.labels}
}

func (f *fakePackageRevision) IsLatestRevision() bool {
	return f.isLatest
}

func (f *fakePackageRevision) GetCommitInfo() (time.Time, string) {
	return time.Time{}, ""
}

type fakePackage struct {
	namespace      string
	latestRevision int
}

func (f *fakePackage) GetPackage(ctx context.Context) *porchapi.PorchPackage {
	return &porchapi.PorchPackage{ObjectMeta: metav1.ObjectMeta{Namespace: f.namespace}}
}
func (f *fakePackage) KubeObjectNamespace() string { return f.namespace }
func (f *fakePackage) Key() PackageKey {
	return PackageKey{RepoKey: RepositoryKey{Namespace: f.namespace}}
}
func (f *fakePackage) KubeObjectName() string                    { return "" }
func (f *fakePackage) GetLatestRevision(ctx context.Context) int { return f.latestRevision }

func TestIsNotFoundError(t *testing.T) {
	testCases := map[string]struct {
		err      error
		expected bool
	}{
		"nil error": {
			err:      nil,
			expected: false,
		},
		"not found error": {
			err:      errors.New("package not found"),
			expected: true,
		},
		"does not exist error": {
			err:      errors.New("resource does not exist"),
			expected: true,
		},
		"no such error": {
			err:      errors.New("no such file"),
			expected: true,
		},
		"404 error": {
			err:      errors.New("HTTP 404 error"),
			expected: true,
		},
		"other error": {
			err:      errors.New("connection failed"),
			expected: false,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			result := IsNotFoundError(tc.err)
			assert.Equal(t, tc.expected, result)
		})
	}
}
