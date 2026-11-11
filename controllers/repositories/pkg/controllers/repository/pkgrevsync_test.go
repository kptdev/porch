// Copyright 2026 The kpt Authors
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
	"fmt"
	"testing"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/porch/api/porch/key"
	porchv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util/selector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
)

// --- Test helpers ---

func newTestRepo() *configapi.Repository {
	return &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-repo",
			Namespace: "default",
			UID:       types.UID("repo-uid"),
		},
		Spec: configapi.RepositorySpec{Deployment: true},
	}
}

func newFakePkgRev(pkg, workspace string, lifecycle porchv1alpha2.PackageRevisionLifecycle) *fakePackageRevision {
	return &fakePackageRevision{
		key: key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{Namespace: "default", Name: "my-repo"},
				Package: pkg,
			},
			WorkspaceName: workspace,
		},
		lifecycle: porchv1alpha1.PackageRevisionLifecycle(lifecycle),
		kptfile:   kptfilev1.KptFile{},
	}
}

// mockListReturning sets up a mock List that populates the result with the given PackageRevisions.
func mockListReturning(m *mockclient.MockClient, items []porchv1alpha2.PackageRevision) {
	m.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).
		Run(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) {
			list.(*porchv1alpha2.PackageRevisionList).Items = items
		}).Return(nil)
}

// mockApplySuccess sets up mock expectations for a successful SSA apply (Patch + Status().Patch).
func mockApplySuccess(t *testing.T, m *mockclient.MockClient) {
	t.Helper()
	m.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil)
	sw := mockclient.NewMockSubResourceWriter(t)
	m.EXPECT().Status().Return(sw)
	sw.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil)
}

// --- fakePackageRevision ---

// fakePackageRevision implements repository.PackageRevision and the optional
// GetCommitInfo/IsLatestRevision interfaces for testing.
type fakePackageRevision struct {
	key          key.PackageRevisionKey
	lifecycle    porchv1alpha1.PackageRevisionLifecycle
	kptfile      kptfilev1.KptFile
	upstreamLock kptfilev1.Locator
	selfLock     kptfilev1.Locator
	commitTime   time.Time
	commitAuthor string
	isLatest     bool
	resources    map[string]string
	meta         metav1.ObjectMeta
}

func (f *fakePackageRevision) KubeObjectNamespace() string                          { return f.key.RKey().Namespace }
func (f *fakePackageRevision) KubeObjectName() string                               { return key.ComposePkgRevObjName(f.key) }
func (f *fakePackageRevision) Key() key.PackageRevisionKey                          { return f.key }
func (f *fakePackageRevision) UID() types.UID                                       { return "" }
func (f *fakePackageRevision) ResourceVersion() string                              { return "" }
func (f *fakePackageRevision) GetMeta() metav1.ObjectMeta                           { return f.meta }
func (f *fakePackageRevision) SetMeta(_ context.Context, _ metav1.ObjectMeta) error { return nil }
func (f *fakePackageRevision) Lifecycle(_ context.Context) porchv1alpha1.PackageRevisionLifecycle {
	return f.lifecycle
}
func (f *fakePackageRevision) UpdateLifecycle(_ context.Context, _ porchv1alpha1.PackageRevisionLifecycle) error {
	return nil
}
func (f *fakePackageRevision) GetPackageRevision(_ context.Context, _ bool) (*porchv1alpha1.PackageRevision, error) {
	return nil, nil
}
func (f *fakePackageRevision) GetResources(_ context.Context) (*porchv1alpha1.PackageRevisionResources, error) {
	if f.resources != nil {
		return &porchv1alpha1.PackageRevisionResources{
			Spec: porchv1alpha1.PackageRevisionResourcesSpec{
				Resources: f.resources,
			},
		}, nil
	}
	return nil, nil
}
func (f *fakePackageRevision) GetFilteredResources(ctx context.Context, _ selector.PRRGet) (*porchv1alpha1.PackageRevisionResources, error) {
	return f.GetResources(ctx)
}
func (f *fakePackageRevision) GetUpstreamLock(_ context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	return kptfilev1.Upstream{}, f.upstreamLock, nil
}
func (f *fakePackageRevision) GetKptfile(_ context.Context) (kptfilev1.KptFile, error) {
	return f.kptfile, nil
}
func (f *fakePackageRevision) GetLock(_ context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	return kptfilev1.Upstream{}, f.selfLock, nil
}
func (f *fakePackageRevision) ToMainPackageRevision(_ context.Context) repository.PackageRevision {
	return nil
}
func (f *fakePackageRevision) GetCommitInfo() (time.Time, string) {
	return f.commitTime, f.commitAuthor
}
func (f *fakePackageRevision) IsLatestRevision() bool { return f.isLatest }

// --- Tests: buildPackageRevision ---

func TestBuildPackageRevision(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo()

	t.Run("published with full metadata", func(t *testing.T) {
		pkgRev := newFakePkgRev("my-pkg", "v3", porchv1alpha2.PackageRevisionLifecyclePublished)
		pkgRev.key.PkgKey.Path = "path/to"
		pkgRev.key.Revision = 3
		pkgRev.commitTime = time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		pkgRev.commitAuthor = "user@example.com"
		pkgRev.kptfile = kptfilev1.KptFile{
			Info: &kptfilev1.PackageInfo{
				ReadinessGates: []kptfilev1.ReadinessGate{{ConditionType: "Ready"}},
			},
			Status: &kptfilev1.Status{
				Conditions: []kptfilev1.Condition{
					{Type: "Ready", Status: kptfilev1.ConditionTrue, Reason: "AllGood"},
				},
			},
		}
		pkgRev.upstreamLock = kptfilev1.Locator{
			Type: kptfilev1.GitOrigin,
			Git:  &kptfilev1.GitLock{Repo: "https://github.com/upstream.git", Ref: "v1.0", Directory: "/", Commit: "abc"},
		}
		pkgRev.selfLock = kptfilev1.Locator{
			Type: kptfilev1.GitOrigin,
			Git:  &kptfilev1.GitLock{Repo: "https://github.com/self.git", Ref: "main", Directory: "path/to/my-pkg", Commit: "def"},
		}

		crd, err := buildPackageRevision(ctx, repo, pkgRev, true)
		assert.NoError(t, err)

		// Metadata
		assert.Equal(t, "PackageRevision", crd.Kind)
		assert.Equal(t, porchv1alpha2.SchemeGroupVersion.Identifier(), crd.APIVersion)
		assert.Equal(t, "default", crd.Namespace)
		assert.Equal(t, repo.Name, crd.OwnerReferences[0].Name)
		assert.Equal(t, repo.UID, crd.OwnerReferences[0].UID)
		require.NotNil(t, crd.OwnerReferences[0].Controller)
		assert.True(t, *crd.OwnerReferences[0].Controller)
		require.NotNil(t, crd.OwnerReferences[0].BlockOwnerDeletion)
		assert.True(t, *crd.OwnerReferences[0].BlockOwnerDeletion)

		// Spec — repo-owned identity fields
		assert.Equal(t, "path/to/my-pkg", crd.Spec.PackageName)
		assert.Equal(t, "my-repo", crd.Spec.RepositoryName)
		assert.Equal(t, "v3", crd.Spec.WorkspaceName)

		// Spec — lifecycle determined from git state by repo controller
		assert.Equal(t, porchv1alpha2.PackageRevisionLifecyclePublished, crd.Spec.Lifecycle)
		// ReadinessGates and PackageMetadata omitted (applied separately via applySeedFields)
		assert.Nil(t, crd.Spec.ReadinessGates)

		// Status — repo-owned fields
		assert.True(t, crd.Status.Deployment)
		assert.Equal(t, "https://github.com/upstream.git", crd.Status.UpstreamLock.Git.Repo)
		assert.Equal(t, "https://github.com/self.git", crd.Status.SelfLock.Git.Repo)

		// Status — seed fields NOT included
		assert.Equal(t, 0, crd.Status.Revision)
		assert.Empty(t, crd.Status.PublishedBy)
		assert.Nil(t, crd.Status.PublishedAt)
		assert.Nil(t, crd.Status.PackageConditions)

		// Labels
		assert.Equal(t, "my-repo", crd.Labels[RepositoryLabel])
	})

	t.Run("draft has no publish metadata", func(t *testing.T) {
		pkgRev := newFakePkgRev("draft-pkg", "ws1", porchv1alpha2.PackageRevisionLifecycleDraft)

		crd, err := buildPackageRevision(ctx, repo, pkgRev, false)
		assert.NoError(t, err)

		// Lifecycle is determined from git state by repo controller
		assert.Equal(t, porchv1alpha2.PackageRevisionLifecycleDraft, crd.Spec.Lifecycle)
		// Publish metadata not set for draft packages
		assert.Equal(t, 0, crd.Status.Revision)
		assert.Empty(t, crd.Status.PublishedBy)
		assert.Nil(t, crd.Status.PublishedAt)
	})

	t.Run("empty kptfile yields nil optional fields", func(t *testing.T) {
		pkgRev := newFakePkgRev("bare-pkg", "ws1", porchv1alpha2.PackageRevisionLifecycleDraft)

		crd, err := buildPackageRevision(ctx, repo, pkgRev, false)
		assert.NoError(t, err)

		assert.Nil(t, crd.Spec.ReadinessGates)
		assert.Nil(t, crd.Spec.PackageMetadata)
		assert.Nil(t, crd.Status.PackageConditions)
		assert.Nil(t, crd.Status.UpstreamLock)
		assert.Nil(t, crd.Status.SelfLock)
	})

	t.Run("ResourcesSizeBytes calculated from resources", func(t *testing.T) {
		pkgRev := newFakePkgRev("sized-pkg", "ws1", porchv1alpha2.PackageRevisionLifecyclePublished)
		pkgRev.resources = map[string]string{
			"Kptfile": "abc",   // 3 bytes
			"cm.yaml": "defgh", // 5 bytes
			"ns.yaml": "ij",    // 2 bytes
		}

		crd, err := buildPackageRevision(ctx, repo, pkgRev, false)
		assert.NoError(t, err)
		assert.Equal(t, int64(10), crd.Status.ResourcesSizeBytes)
	})

	t.Run("ResourcesSizeBytes zero when no resources", func(t *testing.T) {
		pkgRev := newFakePkgRev("empty-pkg", "ws1", porchv1alpha2.PackageRevisionLifecycleDraft)

		crd, err := buildPackageRevision(ctx, repo, pkgRev, false)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), crd.Status.ResourcesSizeBytes)
	})
}

// --- Tests: packageRevisionUpToDate ---

func TestPackageRevisionUpToDate(t *testing.T) {
	base := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				RepositoryLabel:                        "repo1",
				porchv1alpha2.LatestPackageRevisionKey: "false",
			},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName: "pkg1",
			Lifecycle:   porchv1alpha2.PackageRevisionLifecyclePublished,
		},
		Status: porchv1alpha2.PackageRevisionStatus{Revision: 1},
	}

	tests := []struct {
		name     string
		modify   func(*porchv1alpha2.PackageRevision)
		expected bool
	}{
		{name: "identical", modify: nil, expected: true},
		{name: "lifecycle changed - still up to date (client-owned)", modify: func(pr *porchv1alpha2.PackageRevision) {
			pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDraft
		}, expected: true},
		{name: "status changed (packageConditions — PR controller owned)", modify: func(pr *porchv1alpha2.PackageRevision) {
			pr.Status.PackageConditions = []porchv1alpha2.PackageCondition{{Type: "new"}}
		}, expected: true},
		{name: "latest-revision label changed", modify: func(pr *porchv1alpha2.PackageRevision) {
			pr.Labels[porchv1alpha2.LatestPackageRevisionKey] = porchv1alpha2.LatestPackageRevisionValue
		}, expected: false},
		{name: "annotations differ - still up to date", modify: func(pr *porchv1alpha2.PackageRevision) {
			pr.Annotations = map[string]string{"foo": "bar"}
		}, expected: true},
		{name: "non-repo-owned label differs - still up to date", modify: func(pr *porchv1alpha2.PackageRevision) {
			pr.Labels["user.example.com/custom"] = "something"
		}, expected: true},
		{name: "ResourcesSizeBytes changed", modify: func(pr *porchv1alpha2.PackageRevision) {
			pr.Status.ResourcesSizeBytes = 12345
		}, expected: false},
		{name: "repository label changed", modify: func(pr *porchv1alpha2.PackageRevision) {
			pr.Labels[RepositoryLabel] = "different-repo"
		}, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desired := base.DeepCopy()
			if tt.modify != nil {
				tt.modify(desired)
			}
			assert.Equal(t, tt.expected, packageRevisionUpToDate(base, desired))
		})
	}
}

func TestResourcesSizeBytesUpToDate(t *testing.T) {
	tests := []struct {
		name     string
		existing int64
		desired  int64
		expected bool
	}{
		{name: "both zero", existing: 0, desired: 0, expected: true},
		{name: "equal non-zero", existing: 100, desired: 100, expected: true},
		{name: "desired changed", existing: 100, desired: 200, expected: false},
		{name: "desired zero (unknown) - skip comparison", existing: 500, desired: 0, expected: true},
		{name: "existing zero, desired non-zero", existing: 0, desired: 100, expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, resourcesSizeBytesUpToDate(tt.existing, tt.desired))
		})
	}
}

// --- Tests: packageRevisionLabels ---

func TestPackageRevisionLabels(t *testing.T) {
	tests := []struct {
		name       string
		isLatest   bool
		wantLatest bool
	}{
		{name: "non-latest", isLatest: false, wantLatest: false},
		{name: "latest", isLatest: true, wantLatest: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkgRev := &fakePackageRevision{isLatest: tt.isLatest}
			labels := packageRevisionLabels("my-repo", pkgRev)
			assert.Equal(t, "my-repo", labels[RepositoryLabel])
			if tt.wantLatest {
				assert.Equal(t, porchv1alpha2.LatestPackageRevisionValue, labels[porchv1alpha2.LatestPackageRevisionKey])
			} else {
				assert.Equal(t, "false", labels[porchv1alpha2.LatestPackageRevisionKey])
			}
		})
	}
}

func TestPackageRevisionLabelsWithLatest(t *testing.T) {
	t.Run("latest=true", func(t *testing.T) {
		labels := packageRevisionLabelsWithLatest("my-repo", true)
		assert.Equal(t, "my-repo", labels[RepositoryLabel])
		assert.Equal(t, porchv1alpha2.LatestPackageRevisionValue, labels[porchv1alpha2.LatestPackageRevisionKey])
	})

	t.Run("latest=false", func(t *testing.T) {
		labels := packageRevisionLabelsWithLatest("my-repo", false)
		assert.Equal(t, "my-repo", labels[RepositoryLabel])
		assert.Equal(t, "false", labels[porchv1alpha2.LatestPackageRevisionKey])
	})
}

func TestComputeLatestRevisions(t *testing.T) {
	ctx := context.Background()

	t.Run("single package with multiple revisions", func(t *testing.T) {
		v1 := newFakePkgRev("pkg1", "v1", porchv1alpha2.PackageRevisionLifecyclePublished)
		v1.key.Revision = 1
		v2 := newFakePkgRev("pkg1", "v2", porchv1alpha2.PackageRevisionLifecyclePublished)
		v2.key.Revision = 2
		v3 := newFakePkgRev("pkg1", "v3", porchv1alpha2.PackageRevisionLifecyclePublished)
		v3.key.Revision = 3

		result := computeLatestRevisions(ctx, []repository.PackageRevision{v1, v2, v3})
		assert.True(t, result[v3.KubeObjectName()])
		assert.False(t, result[v1.KubeObjectName()])
		assert.False(t, result[v2.KubeObjectName()])
	})

	t.Run("draft packages are excluded", func(t *testing.T) {
		v1 := newFakePkgRev("pkg1", "v1", porchv1alpha2.PackageRevisionLifecyclePublished)
		v1.key.Revision = 1
		draft := newFakePkgRev("pkg1", "ws1", porchv1alpha2.PackageRevisionLifecycleDraft)
		draft.key.Revision = 0

		result := computeLatestRevisions(ctx, []repository.PackageRevision{v1, draft})
		assert.True(t, result[v1.KubeObjectName()])
		assert.False(t, result[draft.KubeObjectName()])
	})

	t.Run("main branch (revision 0) is excluded", func(t *testing.T) {
		main := newFakePkgRev("pkg1", "main", porchv1alpha2.PackageRevisionLifecyclePublished)
		main.key.Revision = 0
		v1 := newFakePkgRev("pkg1", "v1", porchv1alpha2.PackageRevisionLifecyclePublished)
		v1.key.Revision = 1

		result := computeLatestRevisions(ctx, []repository.PackageRevision{main, v1})
		assert.True(t, result[v1.KubeObjectName()])
		assert.False(t, result[main.KubeObjectName()])
	})

	t.Run("multiple packages each get their own latest", func(t *testing.T) {
		pkg1v1 := newFakePkgRev("pkg1", "v1", porchv1alpha2.PackageRevisionLifecyclePublished)
		pkg1v1.key.Revision = 1
		pkg1v2 := newFakePkgRev("pkg1", "v2", porchv1alpha2.PackageRevisionLifecyclePublished)
		pkg1v2.key.Revision = 2
		pkg2v1 := newFakePkgRev("pkg2", "v1", porchv1alpha2.PackageRevisionLifecyclePublished)
		pkg2v1.key.Revision = 1

		result := computeLatestRevisions(ctx, []repository.PackageRevision{pkg1v1, pkg1v2, pkg2v1})
		assert.True(t, result[pkg1v2.KubeObjectName()])
		assert.True(t, result[pkg2v1.KubeObjectName()])
		assert.False(t, result[pkg1v1.KubeObjectName()])
	})

	t.Run("DeletionProposed counts as published", func(t *testing.T) {
		v1 := newFakePkgRev("pkg1", "v1", porchv1alpha2.PackageRevisionLifecyclePublished)
		v1.key.Revision = 1
		v2 := newFakePkgRev("pkg1", "v2", porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
		v2.key.Revision = 2

		result := computeLatestRevisions(ctx, []repository.PackageRevision{v1, v2})
		assert.True(t, result[v2.KubeObjectName()])
		assert.False(t, result[v1.KubeObjectName()])
	})

	t.Run("empty list returns empty map", func(t *testing.T) {
		result := computeLatestRevisions(ctx, nil)
		assert.Empty(t, result)
	})
}

// --- Tests: syncPackageRevisions ---

func TestSyncPackageRevisions(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo()
	draftPkgRev := newFakePkgRev("pkg1", "ws1", porchv1alpha2.PackageRevisionLifecycleDraft)

	stalePR := porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stale-pr",
			Namespace: "default",
			Labels:    map[string]string{RepositoryLabel: "my-repo"},
		},
	}

	// Pre-build the "up to date" resource for the skip test
	upToDatePR, _ := buildPackageRevision(ctx, repo, draftPkgRev, false)

	tests := []struct {
		name        string
		pkgRevs     []repository.PackageRevision
		setupMocks  func(t *testing.T, m *mockclient.MockClient)
		expectError string
	}{
		{
			name:    "creates new PackageRevision with seed fields",
			pkgRevs: []repository.PackageRevision{draftPkgRev},
			setupMocks: func(t *testing.T, m *mockclient.MockClient) {
				mockListReturning(m, nil)
				// applyPackageRevision: spec Patch + status Patch (with ForceOwnership)
				m.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				sw := mockclient.NewMockSubResourceWriter(t)
				sw.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				// applySeedFields: spec patch may be skipped if specFieldsUpToDate (fake kptfile is empty, no fields to seed).
				// But to match v1alpha1 parity, we SHOULD seed fields. For now, the optimization will skip
				// since desired fields are nil. We don't expect patches if nothing changes.
				// Let the test accept 0 or 2 patches for seed fields depending on what's extracted from Kptfile.
				m.EXPECT().Status().Return(sw)
			},
		},
		{
			name:    "skips up-to-date PackageRevision (including seed fields)",
			pkgRevs: []repository.PackageRevision{draftPkgRev},
			setupMocks: func(t *testing.T, m *mockclient.MockClient) {
				mockListReturning(m, []porchv1alpha2.PackageRevision{*upToDatePR})
				// Even though packageRevisionUpToDate is true, we still call applySeedFields now.
				// The optimization inside applySeedFields will skip patches if fields match.
				// Since upToDatePR has no seed fields, applySeedFields will attempt to patch.
				// But the kptfile extraction will find nil metadata (fake pkgRev has empty kptfile),
				// so specFieldsUpToDate will see desired=nil matching current=nil → skip patch.
				// Same for status: no published data (Draft lifecycle) → skip status patch.
				// Result: No Patch calls expected despite applySeedFields being called.
			},
		},
		{
			name:    "deletes stale PackageRevision",
			pkgRevs: nil,
			setupMocks: func(t *testing.T, m *mockclient.MockClient) {
				mockListReturning(m, []porchv1alpha2.PackageRevision{stalePR})
				m.EXPECT().Delete(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision")).Return(nil)
			},
		},
		{
			name:    "deletes only stale PackageRevisions when mixed with current",
			pkgRevs: []repository.PackageRevision{draftPkgRev},
			setupMocks: func(t *testing.T, m *mockclient.MockClient) {
				// List returns both the current resource (matching draftPkgRev) and a stale one
				mockListReturning(m, []porchv1alpha2.PackageRevision{*upToDatePR, stalePR})
				// Only the stale one should be deleted
				m.EXPECT().Delete(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
					return obj.GetName() == "stale-pr"
				})).Return(nil)
			},
		},
		{
			name:    "delete error logged but sync succeeds",
			pkgRevs: nil,
			setupMocks: func(t *testing.T, m *mockclient.MockClient) {
				mockListReturning(m, []porchv1alpha2.PackageRevision{stalePR})
				m.EXPECT().Delete(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision")).Return(fmt.Errorf("delete failed"))
			},
		},
		{
			name: "list error",
			setupMocks: func(t *testing.T, m *mockclient.MockClient) {
				m.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).
					Return(fmt.Errorf("list failed"))
			},
			expectError: "list failed",
		},
		{
			name:    "apply error logged but sync succeeds",
			pkgRevs: []repository.PackageRevision{draftPkgRev},
			setupMocks: func(t *testing.T, m *mockclient.MockClient) {
				mockListReturning(m, nil)
				m.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
					Return(fmt.Errorf("apply failed"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mockclient.NewMockClient(t)
			tt.setupMocks(t, mockClient)

			r := &RepositoryReconciler{Client: mockClient}
			err := r.syncPackageRevisions(ctx, repo, tt.pkgRevs)

			if tt.expectError != "" {
				assert.ErrorContains(t, err, tt.expectError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// --- Tests: applyPackageRevision ---

func TestApplyPackageRevision(t *testing.T) {
	ctx := context.Background()

	pr := &porchv1alpha2.PackageRevision{
		TypeMeta:   metav1.TypeMeta{Kind: "PackageRevision", APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier()},
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec:       porchv1alpha2.PackageRevisionSpec{PackageName: "pkg1"},
		Status:     porchv1alpha2.PackageRevisionStatus{Revision: 1},
	}

	tests := []struct {
		name        string
		setupMocks  func(t *testing.T, m *mockclient.MockClient)
		expectError string
	}{
		{
			name: "success",
			setupMocks: func(t *testing.T, m *mockclient.MockClient) {
				mockApplySuccess(t, m)
			},
		},
		{
			name: "spec patch fails",
			setupMocks: func(t *testing.T, m *mockclient.MockClient) {
				m.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
					Return(fmt.Errorf("spec patch failed"))
			},
			expectError: "spec patch failed",
		},
		{
			name: "status patch fails",
			setupMocks: func(t *testing.T, m *mockclient.MockClient) {
				m.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil)
				sw := mockclient.NewMockSubResourceWriter(t)
				m.EXPECT().Status().Return(sw)
				sw.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
					Return(fmt.Errorf("status patch failed"))
			},
			expectError: "status patch failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mockclient.NewMockClient(t)
			tt.setupMocks(t, mockClient)

			r := &RepositoryReconciler{Client: mockClient}
			err := r.applyPackageRevision(ctx, pr.DeepCopy())

			if tt.expectError != "" {
				assert.ErrorContains(t, err, tt.expectError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBuildPackageRevisionOmitsNonOwnedFields(t *testing.T) {
	ctx := context.Background()
	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo", Namespace: "default", UID: "repo-uid"},
		Spec:       configapi.RepositorySpec{Deployment: true},
	}

	pkgRev := &fakePackageRevision{}
	pkgRev.key = key.PackageRevisionKey{
		PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "default", Name: "my-repo"}, Package: "my-pkg"},
		WorkspaceName: "v1",
	}
	pkgRev.kptfile = kptfilev1.KptFile{
		Info: &kptfilev1.PackageInfo{
			ReadinessGates: []kptfilev1.ReadinessGate{{ConditionType: "Ready"}},
		},
		Status: &kptfilev1.Status{
			Conditions: []kptfilev1.Condition{
				{Type: "Ready", Status: kptfilev1.ConditionTrue},
			},
		},
	}

	crd, err := buildPackageRevision(ctx, repo, pkgRev, false)
	assert.NoError(t, err)

	// Verify identity fields are present.
	assert.Equal(t, "my-pkg", crd.Spec.PackageName)
	assert.Equal(t, "my-repo", crd.Spec.RepositoryName)
	assert.Equal(t, "v1", crd.Spec.WorkspaceName)
	assert.True(t, crd.Status.Deployment)

	// Verify Kptfile-derived fields are omitted (PR controller owns them).
	assert.Nil(t, crd.Spec.ReadinessGates)
	assert.Nil(t, crd.Spec.PackageMetadata)
	assert.Nil(t, crd.Status.PackageConditions)
}

// --- Tests: re-sync and race scenarios ---

// TestResyncDoesNotStripNonOwnedStatusFields verifies that when the repo
// controller re-syncs an existing resource (update path), the SSA apply object
// does not include status.revision, status.publishedBy, or status.publishedAt.
// This prevents SSA from removing those fields on re-sync, which was the
// root cause of status.revision being reset to 0 after repo re-sync.
func TestResyncDoesNotStripNonOwnedStatusFields(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo()

	pkgRev := newFakePkgRev("resync-pkg", "v1", porchv1alpha2.PackageRevisionLifecyclePublished)
	pkgRev.key.Revision = 1
	pkgRev.commitTime = time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	pkgRev.commitAuthor = "user@example.com"

	// Build the resource as the repo controller would for the update path.
	crd, err := buildPackageRevision(ctx, repo, pkgRev, true)
	assert.NoError(t, err)

	// Lifecycle is determined from git state by repo controller
	assert.Equal(t, porchv1alpha2.PackageRevisionLifecyclePublished, crd.Spec.Lifecycle, "spec.lifecycle set by repo controller from git state")
	// The resource must NOT contain publish metadata — those are PR-controller-owned and set via applySeedFields.
	assert.Equal(t, 0, crd.Status.Revision, "status.revision must not be set by repo controller")
	assert.Empty(t, crd.Status.PublishedBy, "status.publishedBy must not be set by repo controller")
	assert.Nil(t, crd.Status.PublishedAt, "status.publishedAt must not be set by repo controller")
}

// TestSyncUpdatePathSkipsSeedFields verifies that applySeedFields
// is NOT called on the update path (matches v1alpha1 behavior).
func TestSyncUpdatePathSkipsSeedFields(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo()

	pkgRev := newFakePkgRev("existing-pkg", "v1", porchv1alpha2.PackageRevisionLifecyclePublished)
	pkgRev.selfLock = kptfilev1.Locator{
		Type: kptfilev1.GitOrigin,
		Git:  &kptfilev1.GitLock{Repo: "https://example.com/repo.git", Ref: "main", Directory: "existing-pkg", Commit: "new-commit"},
	}

	// Existing resource - update should NOT trigger applySeedFields
	existingPR, _ := buildPackageRevision(ctx, repo, pkgRev, true)
	existingPR.Status.SelfLock = nil // different from desired → triggers update

	mockClient := mockclient.NewMockClient(t)
	mockListReturning(mockClient, []porchv1alpha2.PackageRevision{*existingPR})

	// Only applyPackageRevision calls expected (1 spec Patch + 1 status Patch).
	// No applySeedFields on update path.
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	sw := mockclient.NewMockSubResourceWriter(t)
	mockClient.EXPECT().Status().Return(sw).Once()
	sw.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	r := &RepositoryReconciler{Client: mockClient}
	err := r.syncPackageRevisions(ctx, repo, []repository.PackageRevision{pkgRev})
	assert.NoError(t, err)
}

// --- Tests: sourceMetadata ---

func TestSourceMetadata(t *testing.T) {
	t.Run("empty meta returns nil", func(t *testing.T) {
		pkgRev := &fakePackageRevision{}
		labels, annotations := sourceMetadata(pkgRev)
		assert.Nil(t, labels)
		assert.Nil(t, annotations)
	})

	t.Run("system labels are excluded", func(t *testing.T) {
		pkgRev := &fakePackageRevision{
			meta: metav1.ObjectMeta{
				Labels: map[string]string{
					porchv1alpha2.RepositoryLabelKey:       "my-repo",
					porchv1alpha2.LatestPackageRevisionKey: "true",
					"kpt.dev/latest-revision":              "true", // v1alpha1 variant
					"app.kubernetes.io/name":               "my-app",
				},
			},
		}
		labels, annotations := sourceMetadata(pkgRev)
		assert.Equal(t, map[string]string{"app.kubernetes.io/name": "my-app"}, labels)
		assert.Nil(t, annotations)
	})

	t.Run("only system labels returns nil", func(t *testing.T) {
		pkgRev := &fakePackageRevision{
			meta: metav1.ObjectMeta{
				Labels: map[string]string{
					porchv1alpha2.RepositoryLabelKey:       "my-repo",
					porchv1alpha2.LatestPackageRevisionKey: "true",
					"kpt.dev/latest-revision":              "true",
				},
			},
		}
		labels, annotations := sourceMetadata(pkgRev)
		assert.Nil(t, labels)
		assert.Nil(t, annotations)
	})

	t.Run("annotations are passed through", func(t *testing.T) {
		pkgRev := &fakePackageRevision{
			meta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"team":    "platform",
					"purpose": "infra",
				},
			},
		}
		labels, annotations := sourceMetadata(pkgRev)
		assert.Nil(t, labels)
		assert.Equal(t, map[string]string{"team": "platform", "purpose": "infra"}, annotations)
	})

	t.Run("mixed labels and annotations", func(t *testing.T) {
		pkgRev := &fakePackageRevision{
			meta: metav1.ObjectMeta{
				Labels: map[string]string{
					porchv1alpha2.RepositoryLabelKey: "repo",
					"env":                            "production",
					"app":                            "frontend",
				},
				Annotations: map[string]string{
					"description": "production frontend",
				},
			},
		}
		labels, annotations := sourceMetadata(pkgRev)
		assert.Equal(t, map[string]string{"env": "production", "app": "frontend"}, labels)
		assert.Equal(t, map[string]string{"description": "production frontend"}, annotations)
	})
}

// --- Tests: applySeedFields with source metadata ---

func TestApplySeedFieldsWithSourceMetadata(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo()

	t.Run("source labels included in seed spec apply", func(t *testing.T) {
		pkgRev := newFakePkgRev("labeled-pkg", "v1", porchv1alpha2.PackageRevisionLifecyclePublished)
		pkgRev.key.Revision = 1
		pkgRev.meta = metav1.ObjectMeta{
			Labels: map[string]string{
				porchv1alpha2.RepositoryLabelKey: "my-repo",    // system — should be excluded
				"team":                           "networking", // source — should be included
				"env":                            "staging",    // source — should be included
			},
			Annotations: map[string]string{
				"contact": "team-net@example.com",
			},
		}

		// Build the CRD first (as applyDesiredPackageRevisions would)
		crd, err := buildPackageRevision(ctx, repo, pkgRev, true)
		require.NoError(t, err)

		mockClient := mockclient.NewMockClient(t)

		// Expect seed spec Patch with source labels in ObjectMeta
		var seedSpecObj *porchv1alpha2.PackageRevision
		mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything).
			Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
				seedSpecObj = obj.(*porchv1alpha2.PackageRevision)
			}).Return(nil).Once()

		// Expect seed status Patch
		sw := mockclient.NewMockSubResourceWriter(t)
		mockClient.EXPECT().Status().Return(sw)
		sw.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything).Return(nil).Once()

		r := &RepositoryReconciler{Client: mockClient}
		r.applySeedFields(ctx, pkgRev, crd)

		// Verify source labels (excluding system) are in the seed apply ObjectMeta
		require.NotNil(t, seedSpecObj)
		assert.Equal(t, "networking", seedSpecObj.Labels["team"])
		assert.Equal(t, "staging", seedSpecObj.Labels["env"])
		assert.NotContains(t, seedSpecObj.Labels, porchv1alpha2.RepositoryLabelKey)

		// Verify annotations are included
		assert.Equal(t, "team-net@example.com", seedSpecObj.Annotations["contact"])
	})

	t.Run("no source metadata means no labels on seed ObjectMeta", func(t *testing.T) {
		pkgRev := newFakePkgRev("bare-pkg", "v1", porchv1alpha2.PackageRevisionLifecycleDraft)
		// meta left empty — no source labels/annotations
		// kptfile also empty — no extracted metadata

		crd, err := buildPackageRevision(ctx, repo, pkgRev, false)
		require.NoError(t, err)

		mockClient := mockclient.NewMockClient(t)

		// With the patch-skipping optimization: since kptfile is empty, extraction yields nil metadata.
		// specFieldsUpToDate will see: desired=(nil gates, nil metadata, nil labels) all matching current.
		// statusFieldsUpToDate will see: desired=(nil conditions) matching current (Draft, no published data).
		// Result: No patches expected because fields are already up-to-date.

		r := &RepositoryReconciler{Client: mockClient}
		r.applySeedFields(ctx, pkgRev, crd)

		// No patches should occur with the optimization. Test passes if no mock violations.
	})
}

// --- Tests: Optimization Functions (specFieldsUpToDate, statusFieldsUpToDate, filterSystemLabels) ---

// TestFilterSystemLabels tests filtering system-managed labels from source labels.
func TestFilterSystemLabels(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected map[string]string
	}{
		{
			name:     "empty map returns nil",
			input:    map[string]string{},
			expected: nil,
		},
		{
			name: "only system labels returns nil",
			input: map[string]string{
				porchv1alpha2.RepositoryLabelKey:       "my-repo",
				porchv1alpha2.LatestPackageRevisionKey: "true",
				"kpt.dev/latest-revision":              "true",
			},
			expected: nil,
		},
		{
			name: "only user labels returns all",
			input: map[string]string{
				"env":  "prod",
				"team": "platform",
			},
			expected: map[string]string{
				"env":  "prod",
				"team": "platform",
			},
		},
		{
			name: "mixed labels filters system only",
			input: map[string]string{
				porchv1alpha2.RepositoryLabelKey:       "my-repo",
				"env":                                  "prod",
				porchv1alpha2.LatestPackageRevisionKey: "true",
				"team":                                 "platform",
				"kpt.dev/latest-revision":              "true",
			},
			expected: map[string]string{
				"env":  "prod",
				"team": "platform",
			},
		},
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterSystemLabels(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSpecFieldsUpToDate tests the optimization check for spec seed fields.
func TestSpecFieldsUpToDate(t *testing.T) {
	tests := []struct {
		name                     string
		crd                      *porchv1alpha2.PackageRevision
		desiredGates             []porchv1alpha2.ReadinessGate
		desiredMeta              *porchv1alpha2.PackageMetadata
		desiredSourceLabels      map[string]string
		desiredSourceAnnotations map[string]string
		expectedUpToDate         bool
	}{
		{
			name: "all fields match returns true",
			crd: &porchv1alpha2.PackageRevision{
				Spec: porchv1alpha2.PackageRevisionSpec{
					ReadinessGates: []porchv1alpha2.ReadinessGate{
						{ConditionType: "Ready"},
					},
					PackageMetadata: &porchv1alpha2.PackageMetadata{
						Labels:      map[string]string{"env": "prod"},
						Annotations: map[string]string{"team": "platform"},
					},
				},
				ObjectMeta: metav1.ObjectMeta{
					// Object labels must include both the source label ("env") and the
					// promoted packageMetadata label ("porch.kpt.dev/kptfile-label__env")
					// for the object to be considered fully up-to-date.
					Labels: map[string]string{
						"env":                              "prod",
						"porch.kpt.dev/kptfile-label__env": "prod",
					},
					Annotations: map[string]string{"team": "platform"},
				},
			},
			desiredGates: []porchv1alpha2.ReadinessGate{
				{ConditionType: "Ready"},
			},
			desiredMeta: &porchv1alpha2.PackageMetadata{
				Labels:      map[string]string{"env": "prod"},
				Annotations: map[string]string{"team": "platform"},
			},
			desiredSourceLabels:      map[string]string{"env": "prod"},
			desiredSourceAnnotations: map[string]string{"team": "platform"},
			expectedUpToDate:         true,
		},
		{
			name: "different ReadinessGates returns false",
			crd: &porchv1alpha2.PackageRevision{
				Spec: porchv1alpha2.PackageRevisionSpec{
					ReadinessGates: []porchv1alpha2.ReadinessGate{
						{ConditionType: "Ready"},
					},
				},
			},
			desiredGates: []porchv1alpha2.ReadinessGate{
				{ConditionType: "Rendered"},
			},
			expectedUpToDate: false,
		},
		{
			name: "different PackageMetadata returns false",
			crd: &porchv1alpha2.PackageRevision{
				Spec: porchv1alpha2.PackageRevisionSpec{
					PackageMetadata: &porchv1alpha2.PackageMetadata{
						Labels: map[string]string{"env": "prod"},
					},
				},
			},
			desiredMeta: &porchv1alpha2.PackageMetadata{
				Labels: map[string]string{"env": "dev"},
			},
			expectedUpToDate: false,
		},
		{
			name: "different source labels returns false",
			crd: &porchv1alpha2.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"env":                                  "prod",
						porchv1alpha2.RepositoryLabelKey:       "my-repo",
						porchv1alpha2.LatestPackageRevisionKey: "true",
					},
				},
			},
			desiredSourceLabels: map[string]string{"env": "dev"},
			expectedUpToDate:    false,
		},
		{
			name: "different source annotations returns false",
			crd: &porchv1alpha2.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"team": "platform"},
				},
			},
			desiredSourceAnnotations: map[string]string{"team": "infrastructure"},
			expectedUpToDate:         false,
		},
		{
			name: "all nil/empty fields returns true",
			crd: &porchv1alpha2.PackageRevision{
				Spec: porchv1alpha2.PackageRevisionSpec{
					ReadinessGates:  nil,
					PackageMetadata: nil,
				},
				ObjectMeta: metav1.ObjectMeta{},
			},
			desiredGates:             nil,
			desiredMeta:              nil,
			desiredSourceLabels:      nil,
			desiredSourceAnnotations: nil,
			expectedUpToDate:         true,
		},
		{
			name: "system labels in CRD are filtered out for comparison",
			crd: &porchv1alpha2.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"env":                                  "prod",
						porchv1alpha2.RepositoryLabelKey:       "my-repo",
						porchv1alpha2.LatestPackageRevisionKey: "true",
						"team":                                 "platform",
					},
				},
			},
			desiredSourceLabels: map[string]string{
				"env":  "prod",
				"team": "platform",
			},
			expectedUpToDate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := specFieldsUpToDate(tt.crd, tt.desiredGates, tt.desiredMeta, tt.desiredSourceLabels, tt.desiredSourceAnnotations)
			assert.Equal(t, tt.expectedUpToDate, result, "specFieldsUpToDate result mismatch")
		})
	}
}

// TestStatusFieldsUpToDate tests the optimization check for status seed fields.
func TestStatusFieldsUpToDate(t *testing.T) {
	pastTime := metav1.Time{Time: time.Now().Add(-1 * time.Hour)}
	futureTime := metav1.Time{Time: time.Now().Add(1 * time.Hour)}

	tests := []struct {
		name               string
		crd                *porchv1alpha2.PackageRevision
		desiredConditions  []porchv1alpha2.PackageCondition
		desiredRevision    int
		desiredPublishedBy string
		desiredPublishedAt *metav1.Time
		expectedUpToDate   bool
	}{
		{
			name: "all fields match returns true",
			crd: &porchv1alpha2.PackageRevision{
				Status: porchv1alpha2.PackageRevisionStatus{
					PackageConditions: []porchv1alpha2.PackageCondition{
						{Type: "Ready", Status: porchv1alpha2.PackageConditionTrue},
					},
					Revision:    5,
					PublishedBy: "user@example.com",
					PublishedAt: &pastTime,
				},
			},
			desiredConditions: []porchv1alpha2.PackageCondition{
				{Type: "Ready", Status: porchv1alpha2.PackageConditionTrue},
			},
			desiredRevision:    5,
			desiredPublishedBy: "user@example.com",
			desiredPublishedAt: &pastTime,
			expectedUpToDate:   true,
		},
		{
			name: "different PackageConditions returns false",
			crd: &porchv1alpha2.PackageRevision{
				Status: porchv1alpha2.PackageRevisionStatus{
					PackageConditions: []porchv1alpha2.PackageCondition{
						{Type: "Ready", Status: porchv1alpha2.PackageConditionTrue},
					},
				},
			},
			desiredConditions: []porchv1alpha2.PackageCondition{
				{Type: "Ready", Status: porchv1alpha2.PackageConditionFalse},
			},
			expectedUpToDate: false,
		},
		{
			name: "different Revision returns false",
			crd: &porchv1alpha2.PackageRevision{
				Status: porchv1alpha2.PackageRevisionStatus{
					Revision: 5,
				},
			},
			desiredRevision:  6,
			expectedUpToDate: false,
		},
		{
			name: "different PublishedBy returns false",
			crd: &porchv1alpha2.PackageRevision{
				Status: porchv1alpha2.PackageRevisionStatus{
					PublishedBy: "user@example.com",
				},
			},
			desiredPublishedBy: "other@example.com",
			expectedUpToDate:   false,
		},
		{
			name: "different PublishedAt times returns false",
			crd: &porchv1alpha2.PackageRevision{
				Status: porchv1alpha2.PackageRevisionStatus{
					PublishedAt: &pastTime,
				},
			},
			desiredPublishedAt: &futureTime,
			expectedUpToDate:   false,
		},
		{
			name: "all nil/zero fields returns true",
			crd: &porchv1alpha2.PackageRevision{
				Status: porchv1alpha2.PackageRevisionStatus{
					PackageConditions: nil,
					Revision:          0,
					PublishedBy:       "",
					PublishedAt:       nil,
				},
			},
			desiredConditions:  nil,
			desiredRevision:    0,
			desiredPublishedBy: "",
			desiredPublishedAt: nil,
			expectedUpToDate:   true,
		},
		{
			name: "both PublishedAt nil returns true",
			crd: &porchv1alpha2.PackageRevision{
				Status: porchv1alpha2.PackageRevisionStatus{
					PublishedAt: nil,
				},
			},
			desiredPublishedAt: nil,
			expectedUpToDate:   true,
		},
		{
			name: "one PublishedAt nil returns false",
			crd: &porchv1alpha2.PackageRevision{
				Status: porchv1alpha2.PackageRevisionStatus{
					PublishedAt: &pastTime,
				},
			},
			desiredPublishedAt: nil,
			expectedUpToDate:   false,
		},
		{
			name: "empty conditions list matches nil",
			crd: &porchv1alpha2.PackageRevision{
				Status: porchv1alpha2.PackageRevisionStatus{
					PackageConditions: []porchv1alpha2.PackageCondition{},
				},
			},
			desiredConditions: nil,
			expectedUpToDate:  true, // equality.Semantic.DeepEqual treats empty and nil as equal
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := statusFieldsUpToDate(tt.crd, tt.desiredConditions, tt.desiredRevision, tt.desiredPublishedBy, tt.desiredPublishedAt)
			assert.Equal(t, tt.expectedUpToDate, result, "statusFieldsUpToDate result mismatch")
		})
	}
}

// TestTimesEqual tests the TimesEqual utility function.
func TestTimesEqual(t *testing.T) {
	time1 := metav1.NewTime(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	time2 := metav1.NewTime(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)) // same as time1
	time3 := metav1.NewTime(time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)) // different

	tests := []struct {
		name     string
		a        *metav1.Time
		b        *metav1.Time
		expected bool
	}{
		{
			name:     "both nil returns true",
			a:        nil,
			b:        nil,
			expected: true,
		},
		{
			name:     "one nil returns false",
			a:        &time1,
			b:        nil,
			expected: false,
		},
		{
			name:     "other nil returns false",
			a:        nil,
			b:        &time1,
			expected: false,
		},
		{
			name:     "identical times returns true",
			a:        &time1,
			b:        &time2,
			expected: true,
		},
		{
			name:     "different times returns false",
			a:        &time1,
			b:        &time3,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := porchv1alpha2.TimesEqual(tt.a, tt.b)
			assert.Equal(t, tt.expected, result, "TimesEqual result mismatch")
		})
	}
}
