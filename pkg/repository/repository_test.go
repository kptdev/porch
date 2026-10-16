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
	"testing"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/util/selector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

func TestListPackageRevisionFilter_Matches(t *testing.T) {
	tests := []struct {
		name     string
		filter   ListPackageRevisionFilter
		p        PackageRevision
		negative bool
	}{
		{
			name:   "key matches",
			filter: ListPackageRevisionFilter{Key: key.PackageRevisionKey{PkgKey: key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "ns"}}}},
			p:      &fakePackageRevision{namespace: "ns"},
		},
		{
			name:     "key doesn't match",
			filter:   ListPackageRevisionFilter{Key: key.PackageRevisionKey{PkgKey: key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "ns2"}}}},
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
			filter: ListPackageRevisionFilter{Key: key.PackageRevisionKey{PkgKey: key.PackageKey{RepoKey: key.RepositoryKey{Name: "someRepo"}}}},
			p:      &fakePackageRevision{repoName: "someRepo"},
		},
		{
			name:     "repository doesn't match",
			filter:   ListPackageRevisionFilter{Key: key.PackageRevisionKey{PkgKey: key.PackageKey{RepoKey: key.RepositoryKey{Name: "someReopWithAMisspelling"}}}},
			p:        &fakePackageRevision{repoName: "someRepo"},
			negative: true,
		},
		{
			name:   "name matches",
			filter: ListPackageRevisionFilter{Key: func() key.PackageRevisionKey { key, _ := key.PkgRevK8sName2Key("", "somePackageRevision"); return key }()},
			p:      &fakePackageRevision{name: "somePackageRevision"},
		},
		{
			name:   "revision matches",
			filter: ListPackageRevisionFilter{Key: key.PackageRevisionKey{Revision: 1}},
			p:      &fakePackageRevision{revision: 1},
		},
		{
			name:   "package name matches",
			filter: ListPackageRevisionFilter{Key: key.PackageRevisionKey{PkgKey: key.PackageKey{Package: "someSortOfRadio"}}},
			p:      &fakePackageRevision{packageName: "someSortOfRadio"},
		},
		{
			name:   "multi-folder package name matches",
			filter: ListPackageRevisionFilter{Key: key.PackageRevisionKey{PkgKey: key.PackageKey{Path: "someSortOfNetwork", Package: "someSortOfRadio"}}},
			p:      &fakePackageRevision{packagePath: "someSortOfNetwork", packageName: "someSortOfRadio"},
		},
		{
			name:   "workspace name matches",
			filter: ListPackageRevisionFilter{Key: key.PackageRevisionKey{WorkspaceName: "main"}},
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
			filter:                &ListPackageRevisionFilter{Key: key.PackageRevisionKey{PkgKey: key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "foo"}}}},
			wantMatches:           true,
			wantFilteredNamespace: "foo",
		},
		{
			name:                  "non-matching key",
			filter:                &ListPackageRevisionFilter{Key: key.PackageRevisionKey{PkgKey: key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "bar"}}}},
			wantMatches:           false,
			wantFilteredNamespace: "bar",
		},
		{
			name:                  "different key",
			filter:                &ListPackageRevisionFilter{Key: key.PackageRevisionKey{PkgKey: key.PackageKey{Package: "something"}}},
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
			filter:           &ListPackageRevisionFilter{Key: key.PackageRevisionKey{PkgKey: key.PackageKey{RepoKey: key.RepositoryKey{Name: "foo"}}}},
			wantFilteredRepo: "foo",
		},
		{
			name:             "different key",
			filter:           &ListPackageRevisionFilter{Key: key.PackageRevisionKey{PkgKey: key.PackageKey{Package: "something"}}},
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
	f := &ListPackageFilter{Key: key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "ns"}}}
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

func (f *fakePackageRevision) GetPackageRevision(ctx context.Context, readFromSource bool) (*porchapi.PackageRevision, error) {
	return &porchapi.PackageRevision{ObjectMeta: metav1.ObjectMeta{Namespace: f.namespace}}, nil
}
func (f *fakePackageRevision) KubeObjectNamespace() string { return f.namespace }
func (f *fakePackageRevision) Key() key.PackageRevisionKey {
	return key.PackageRevisionKey{
		PkgKey: key.PackageKey{
			RepoKey: key.RepositoryKey{
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
func (f *fakePackageRevision) GetFilteredResources(context.Context, selector.PRRGet) (*porchapi.PackageRevisionResources, error) {
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
func (f *fakePackage) Key() key.PackageKey {
	return key.PackageKey{RepoKey: key.RepositoryKey{Namespace: f.namespace}}
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
