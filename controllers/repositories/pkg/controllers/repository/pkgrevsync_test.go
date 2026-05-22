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

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
		key: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{Namespace: "default", Name: "my-repo"},
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
	key          repository.PackageRevisionKey
	lifecycle    porchv1alpha1.PackageRevisionLifecycle
	kptfile      kptfilev1.KptFile
	upstreamLock kptfilev1.Locator
	selfLock     kptfilev1.Locator
	commitTime   time.Time
	commitAuthor string
	isLatest     bool
	resources    map[string]string
}

func (f *fakePackageRevision) KubeObjectNamespace() string                          { return f.key.RKey().Namespace }
func (f *fakePackageRevision) KubeObjectName() string                               { return repository.ComposePkgRevObjName(f.key) }
func (f *fakePackageRevision) Key() repository.PackageRevisionKey                   { return f.key }
func (f *fakePackageRevision) UID() types.UID                                       { return "" }
func (f *fakePackageRevision) ResourceVersion() string                              { return "" }
func (f *fakePackageRevision) GetMeta() metav1.ObjectMeta                           { return metav1.ObjectMeta{} }
func (f *fakePackageRevision) SetMeta(_ context.Context, _ metav1.ObjectMeta) error { return nil }
func (f *fakePackageRevision) Lifecycle(_ context.Context) porchv1alpha1.PackageRevisionLifecycle {
	return f.lifecycle
}
func (f *fakePackageRevision) UpdateLifecycle(_ context.Context, _ porchv1alpha1.PackageRevisionLifecycle) error {
	return nil
}
func (f *fakePackageRevision) GetPackageRevision(_ context.Context) (*porchv1alpha1.PackageRevision, error) {
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

		crd, err := buildPackageRevision(ctx, repo, pkgRev)
		assert.NoError(t, err)

		// Metadata
		assert.Equal(t, "PackageRevision", crd.Kind)
		assert.Equal(t, porchv1alpha2.SchemeGroupVersion.Identifier(), crd.APIVersion)
		assert.Equal(t, "default", crd.Namespace)
		assert.Equal(t, repo.Name, crd.OwnerReferences[0].Name)
		assert.Equal(t, repo.UID, crd.OwnerReferences[0].UID)

		// Spec — repo-owned identity fields
		assert.Equal(t, "path/to/my-pkg", crd.Spec.PackageName)
		assert.Equal(t, "my-repo", crd.Spec.RepositoryName)
		assert.Equal(t, "v3", crd.Spec.WorkspaceName)

		// Spec — seed fields NOT included (applied separately via applySeedFields)
		assert.Empty(t, crd.Spec.Lifecycle)
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

		crd, err := buildPackageRevision(ctx, repo, pkgRev)
		assert.NoError(t, err)

		// buildPackageRevision never includes seed fields
		assert.Equal(t, 0, crd.Status.Revision)
		assert.Empty(t, crd.Status.PublishedBy)
		assert.Nil(t, crd.Status.PublishedAt)
		assert.Empty(t, crd.Spec.Lifecycle)
	})

	t.Run("empty kptfile yields nil optional fields", func(t *testing.T) {
		pkgRev := newFakePkgRev("bare-pkg", "ws1", porchv1alpha2.PackageRevisionLifecycleDraft)

		crd, err := buildPackageRevision(ctx, repo, pkgRev)
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

		crd, err := buildPackageRevision(ctx, repo, pkgRev)
		assert.NoError(t, err)
		assert.Equal(t, int64(10), crd.Status.ResourcesSizeBytes)
	})

	t.Run("ResourcesSizeBytes zero when no resources", func(t *testing.T) {
		pkgRev := newFakePkgRev("empty-pkg", "ws1", porchv1alpha2.PackageRevisionLifecycleDraft)

		crd, err := buildPackageRevision(ctx, repo, pkgRev)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), crd.Status.ResourcesSizeBytes)
	})
}

// --- Tests: packageRevisionUpToDate ---

func TestPackageRevisionUpToDate(t *testing.T) {
	base := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{RepositoryLabel: "repo1"},
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
		{name: "labels changed", modify: func(pr *porchv1alpha2.PackageRevision) {
			pr.Labels[porchv1alpha2.LatestPackageRevisionKey] = porchv1alpha2.LatestPackageRevisionValue
		}, expected: false},
		{name: "annotations differ - still up to date", modify: func(pr *porchv1alpha2.PackageRevision) {
			pr.Annotations = map[string]string{"foo": "bar"}
		}, expected: true},
		{name: "ResourcesSizeBytes changed", modify: func(pr *porchv1alpha2.PackageRevision) {
			pr.Status.ResourcesSizeBytes = 12345
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
	upToDatePR, _ := buildPackageRevision(ctx, repo, draftPkgRev)

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
				// applyPackageRevision: spec Patch + status Patch
				m.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				sw := mockclient.NewMockSubResourceWriter(t)
				sw.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				// applySeedFields: spec Patch (no ForceOwnership, so 1 fewer option) + status Patch
				m.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything).Return(nil).Once()
				sw.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything).Return(nil).Once()
				m.EXPECT().Status().Return(sw)
			},
		},
		{
			name:    "skips up-to-date PackageRevision",
			pkgRevs: []repository.PackageRevision{draftPkgRev},
			setupMocks: func(t *testing.T, m *mockclient.MockClient) {
				mockListReturning(m, []porchv1alpha2.PackageRevision{*upToDatePR})
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
	pkgRev.key = repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{RepoKey: repository.RepositoryKey{Namespace: "default", Name: "my-repo"}, Package: "my-pkg"},
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

	crd, err := buildPackageRevision(ctx, repo, pkgRev)
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
	crd, err := buildPackageRevision(ctx, repo, pkgRev)
	assert.NoError(t, err)

	// The resource must NOT contain publish metadata — those are PR-controller-owned.
	assert.Equal(t, 0, crd.Status.Revision, "status.revision must not be set by repo controller")
	assert.Empty(t, crd.Status.PublishedBy, "status.publishedBy must not be set by repo controller")
	assert.Nil(t, crd.Status.PublishedAt, "status.publishedAt must not be set by repo controller")
	assert.Empty(t, crd.Spec.Lifecycle, "spec.lifecycle must not be set by repo controller")
}

// TestSyncUpdatePathDoesNotCallSeedFields verifies that when the informer
// cache correctly identifies an existing resource (isUpdate=true), applySeedFields
// is NOT called — only applyPackageRevision runs.
func TestSyncUpdatePathDoesNotCallSeedFields(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo()

	pkgRev := newFakePkgRev("existing-pkg", "v1", porchv1alpha2.PackageRevisionLifecyclePublished)
	pkgRev.selfLock = kptfilev1.Locator{
		Type: kptfilev1.GitOrigin,
		Git:  &kptfilev1.GitLock{Repo: "https://example.com/repo.git", Ref: "main", Directory: "existing-pkg", Commit: "new-commit"},
	}

	// Existing resource has an old selfLock so it's NOT up-to-date.
	existingPR, _ := buildPackageRevision(ctx, repo, pkgRev)
	existingPR.Status.SelfLock = nil // different from desired → triggers update

	mockClient := mockclient.NewMockClient(t)
	mockListReturning(mockClient, []porchv1alpha2.PackageRevision{*existingPR})

	// Expect exactly 1 spec Patch + 1 status Patch (applyPackageRevision only).
	// No additional Patch calls from applySeedFields.
	var specPatchObj *porchv1alpha2.PackageRevision
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			specPatchObj = obj.(*porchv1alpha2.PackageRevision)
		}).Return(nil).Once()
	sw := mockclient.NewMockSubResourceWriter(t)
	mockClient.EXPECT().Status().Return(sw).Once()
	var statusPatchObj *porchv1alpha2.PackageRevision
	sw.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			statusPatchObj = obj.(*porchv1alpha2.PackageRevision)
		}).Return(nil).Once()

	r := &RepositoryReconciler{Client: mockClient}
	err := r.syncPackageRevisions(ctx, repo, []repository.PackageRevision{pkgRev})
	assert.NoError(t, err)

	// Verify the spec apply does not include lifecycle.
	assert.Empty(t, specPatchObj.Spec.Lifecycle, "update path must not set spec.lifecycle")

	// Verify the status apply does not include revision or publish metadata.
	assert.Equal(t, 0, statusPatchObj.Status.Revision, "update path must not set status.revision")
	assert.Empty(t, statusPatchObj.Status.PublishedBy, "update path must not set status.publishedBy")
	assert.Nil(t, statusPatchObj.Status.PublishedAt, "update path must not set status.publishedAt")
}

// TestSeedFieldsNotCalledOnUpdate verifies that applySeedFields is only
// called on the create path (!isUpdate), not on the update path.
func TestSeedFieldsNotCalledOnUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo()

	pkgRev := newFakePkgRev("pkg1", "v1", porchv1alpha2.PackageRevisionLifecyclePublished)
	pkgRev.key.Revision = 1

	// Simulate existing resource with different labels to force an update.
	existingPR, _ := buildPackageRevision(ctx, repo, pkgRev)
	existingPR.Labels["extra"] = "label"

	mockClient := mockclient.NewMockClient(t)
	mockListReturning(mockClient, []porchv1alpha2.PackageRevision{*existingPR})

	// Only applyPackageRevision calls expected (1 spec Patch + 1 status Patch).
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	sw := mockclient.NewMockSubResourceWriter(t)
	mockClient.EXPECT().Status().Return(sw).Once()
	sw.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	// If applySeedFields were called, there would be additional Patch calls
	// which would cause the mock to fail with unexpected calls.

	r := &RepositoryReconciler{Client: mockClient}
	err := r.syncPackageRevisions(ctx, repo, []repository.PackageRevision{pkgRev})
	assert.NoError(t, err)
}
