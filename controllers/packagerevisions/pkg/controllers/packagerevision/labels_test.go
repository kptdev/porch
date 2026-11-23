// Copyright 2026 The kpt Authors.
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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSetRepositoryLabel(t *testing.T) {
	tests := []struct {
		name    string
		labels  map[string]string
		changed bool
	}{
		{"already correct", map[string]string{porchv1alpha2.RepositoryLabelKey: "my-repo"}, false},
		{"missing", map[string]string{"other": "x"}, true},
		{"nil labels", nil, true},
		{"mismatch", map[string]string{porchv1alpha2.RepositoryLabelKey: "old-repo"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &porchv1alpha2.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{Labels: tt.labels},
				Spec:       porchv1alpha2.PackageRevisionSpec{RepositoryName: "my-repo"},
			}
			assert.Equal(t, tt.changed, setRepositoryLabel(pr))
			assert.Equal(t, "my-repo", pr.Labels[porchv1alpha2.RepositoryLabelKey])
		})
	}
}

func TestSetLatestRevisionLabelDefault(t *testing.T) {
	tests := []struct {
		name    string
		labels  map[string]string
		changed bool
		want    string
	}{
		{"keeps true winner", map[string]string{porchv1alpha2.LatestPackageRevisionKey: "true"}, false, "true"},
		{"keeps false", map[string]string{porchv1alpha2.LatestPackageRevisionKey: "false"}, false, "false"},
		{"missing", map[string]string{"other": "x"}, true, "false"},
		{"nil labels", nil, true, "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &porchv1alpha2.PackageRevision{ObjectMeta: metav1.ObjectMeta{Labels: tt.labels}}
			assert.Equal(t, tt.changed, setLatestRevisionLabelDefault(pr))
			assert.Equal(t, tt.want, pr.Labels[porchv1alpha2.LatestPackageRevisionKey])
		})
	}
}

// ownedPR returns a PackageRevision that already has the finalizer and a complete Repository ownerReference.
func ownedPR(labels map[string]string) *porchv1alpha2.PackageRevision {
	controller, block := true, true
	return &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pr",
			Namespace:  "default",
			Labels:     labels,
			Finalizers: []string{porchv1alpha2.PackageRevisionFinalizer},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         configapi.GroupVersion.Identifier(),
				Kind:               configapi.TypeRepository.Kind,
				Name:               "my-repo",
				UID:                "repo-uid",
				Controller:         &controller,
				BlockOwnerDeletion: &block,
			}},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{RepositoryName: "my-repo"},
	}
}

func TestEnsureMetadataNoPatchWhenComplete(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	// No Patch expected — finalizer, ownerReference and both labels are present.
	r := &PackageRevisionReconciler{Client: mockClient}
	pr := ownedPR(map[string]string{
		porchv1alpha2.RepositoryLabelKey:       "my-repo",
		porchv1alpha2.LatestPackageRevisionKey: "true",
	})

	assert.NoError(t, r.ensureMetadata(t.Context(), pr))
	assert.Equal(t, "true", pr.Labels[porchv1alpha2.LatestPackageRevisionKey])
}

func TestEnsureMetadataSinglePatch(t *testing.T) {
	ctx := t.Context()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec:       porchv1alpha2.PackageRevisionSpec{RepositoryName: "my-repo"},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"}, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*configapi.Repository) = configapi.Repository{ObjectMeta: metav1.ObjectMeta{Name: "my-repo", Namespace: "default", UID: "repo-uid"}}
		}).Return(nil)
	// One patch carries the finalizer, ownerReference and both labels.
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			got := obj.(*porchv1alpha2.PackageRevision)
			assert.Contains(t, got.Finalizers, porchv1alpha2.PackageRevisionFinalizer)
			assert.Len(t, got.OwnerReferences, 1)
			assert.Equal(t, "my-repo", got.Labels[porchv1alpha2.RepositoryLabelKey])
			assert.Equal(t, "false", got.Labels[porchv1alpha2.LatestPackageRevisionKey])
		}).Return(nil).Once()

	r := &PackageRevisionReconciler{Client: mockClient}
	assert.NoError(t, r.ensureMetadata(ctx, pr))
}

func TestEnsureMetadataLabelsOnly(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			got := obj.(*porchv1alpha2.PackageRevision)
			assert.Equal(t, "my-repo", got.Labels[porchv1alpha2.RepositoryLabelKey])
			assert.Equal(t, "false", got.Labels[porchv1alpha2.LatestPackageRevisionKey])
		}).Return(nil).Once()

	r := &PackageRevisionReconciler{Client: mockClient}
	assert.NoError(t, r.ensureMetadata(t.Context(), ownedPR(map[string]string{porchv1alpha2.RepositoryLabelKey: "old-repo"})))
}

func TestEnsureMetadataPatchErrorReverts(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Return(assert.AnError)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := ownedPR(nil)
	pr.Finalizers = nil

	err := r.ensureMetadata(t.Context(), pr)

	// The error is returned so the caller requeues; in-memory state matches what is persisted.
	assert.ErrorContains(t, err, "failed to patch finalizer/ownerReference/labels")
	assert.Nil(t, pr.Labels)
	assert.Empty(t, pr.Finalizers)
}

func TestUpdateLatestRevisionLabels(t *testing.T) {
	tests := []struct {
		name           string
		items          []porchv1alpha2.PackageRevision
		expectPatches  int
		expectedLatest string
	}{
		{
			name: "single published — set to true",
			items: []porchv1alpha2.PackageRevision{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pr-v1", Labels: map[string]string{porchv1alpha2.LatestPackageRevisionKey: "false"}},
					Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
					Status:     porchv1alpha2.PackageRevisionStatus{Revision: 1},
				},
			},
			expectPatches:  1,
			expectedLatest: "pr-v1",
		},
		{
			name: "two published — highest wins",
			items: []porchv1alpha2.PackageRevision{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pr-v1", Labels: map[string]string{porchv1alpha2.LatestPackageRevisionKey: "true"}},
					Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
					Status:     porchv1alpha2.PackageRevisionStatus{Revision: 1},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pr-v2", Labels: map[string]string{porchv1alpha2.LatestPackageRevisionKey: "false"}},
					Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
					Status:     porchv1alpha2.PackageRevisionStatus{Revision: 2},
				},
			},
			expectPatches:  2, // v1 false→false (no-op skipped), v2 false→true
			expectedLatest: "pr-v2",
		},
		{
			name: "already correct — no patches",
			items: []porchv1alpha2.PackageRevision{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pr-v1", Labels: map[string]string{porchv1alpha2.LatestPackageRevisionKey: "true"}},
					Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
					Status:     porchv1alpha2.PackageRevisionStatus{Revision: 1},
				},
			},
			expectPatches:  0,
			expectedLatest: "pr-v1",
		},
		{
			// A published sibling whose revision number isn't observed yet
			// (Status.Revision == 0) must be SKIPPED, not block the whole
			// recompute. The observed revision still wins and gets labeled; no
			// error/requeue (which previously livelocked and blocked deletes).
			name: "unobserved published sibling is skipped, observed one wins",
			items: []porchv1alpha2.PackageRevision{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pr-v1", Labels: map[string]string{porchv1alpha2.LatestPackageRevisionKey: "false"}},
					Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
					Status:     porchv1alpha2.PackageRevisionStatus{Revision: 1},
				},
				{
					// Published but revision not yet observed — skipped.
					ObjectMeta: metav1.ObjectMeta{Name: "pr-v2", Labels: map[string]string{porchv1alpha2.LatestPackageRevisionKey: "false"}},
					Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
					Status:     porchv1alpha2.PackageRevisionStatus{Revision: 0},
				},
			},
			expectPatches:  1, // only pr-v1 false→true; pr-v2 skipped
			expectedLatest: "pr-v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mockclient.NewMockClient(t)

			mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).
				Run(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) {
					prList := list.(*porchv1alpha2.PackageRevisionList)
					prList.Items = tt.items
				}).Return(nil)

			patchCount := 0
			if tt.expectPatches > 0 {
				mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
					Run(func(_ context.Context, _ client.Object, _ client.Patch, _ ...client.PatchOption) {
						patchCount++
					}).Return(nil).Maybe()
			}

			r := &PackageRevisionReconciler{Client: mockClient}
			pr := &porchv1alpha2.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
				Spec: porchv1alpha2.PackageRevisionSpec{
					RepositoryName: "my-repo",
					PackageName:    "my-pkg",
				},
			}

			latest, err := r.updateLatestRevisionLabels(t.Context(), pr)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedLatest, latest)

			if tt.expectPatches == 0 {
				assert.Equal(t, 0, patchCount)
			}
		})
	}
}

func TestUpdateLatestRevisionLabelsPatchError(t *testing.T) {
	items := []porchv1alpha2.PackageRevision{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pr-v1", Labels: map[string]string{porchv1alpha2.LatestPackageRevisionKey: "false"}},
			Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
			Status:     porchv1alpha2.PackageRevisionStatus{Revision: 1},
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).
		Run(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) {
			list.(*porchv1alpha2.PackageRevisionList).Items = items
		}).Return(nil)

	// Patch fails — should log and continue, not panic.
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Return(assert.AnError)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			RepositoryName: "my-repo",
			PackageName:    "my-pkg",
		},
	}

	r.updateLatestRevisionLabels(t.Context(), pr)
}

func TestUpdateLatestRevisionLabelsNilLabelsOnRevision(t *testing.T) {
	// Covers the nil-labels init path inside the patch loop.
	items := []porchv1alpha2.PackageRevision{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pr-v1"}, // nil Labels
			Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
			Status:     porchv1alpha2.PackageRevisionStatus{Revision: 1},
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).
		Run(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) {
			list.(*porchv1alpha2.PackageRevisionList).Items = items
		}).Return(nil)

	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			pr := obj.(*porchv1alpha2.PackageRevision)
			assert.Equal(t, porchv1alpha2.LatestPackageRevisionValue, pr.Labels[porchv1alpha2.LatestPackageRevisionKey])
		}).Return(nil)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			RepositoryName: "my-repo",
			PackageName:    "my-pkg",
		},
	}

	r.updateLatestRevisionLabels(t.Context(), pr)
}

func TestUpdateLatestRevisionLabelsListError(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).
		Return(assert.AnError)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			RepositoryName: "my-repo",
			PackageName:    "my-pkg",
		},
	}

	// Should not panic, just log.
	r.updateLatestRevisionLabels(t.Context(), pr)
}

func TestUpdateLatestRevisionLabelsSkipsDeletingPackage(t *testing.T) {
	now := metav1.NewTime(time.Now())
	items := []porchv1alpha2.PackageRevision{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pr-v1", Labels: map[string]string{porchv1alpha2.LatestPackageRevisionKey: "false"}},
			Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
			Status:     porchv1alpha2.PackageRevisionStatus{Revision: 1},
		},
		{
			// v2 is being deleted — should be skipped.
			ObjectMeta: metav1.ObjectMeta{
				Name:              "pr-v2",
				DeletionTimestamp: &now,
				Labels:            map[string]string{porchv1alpha2.LatestPackageRevisionKey: "true"},
			},
			Spec:   porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
			Status: porchv1alpha2.PackageRevisionStatus{Revision: 2},
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).
		Run(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) {
			list.(*porchv1alpha2.PackageRevisionList).Items = items
		}).Return(nil)

	// Expect v1 to be patched to "true" (promoted to latest).
	mockClient.EXPECT().Patch(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		pr := obj.(*porchv1alpha2.PackageRevision)
		return pr.Name == "pr-v1" && pr.Labels[porchv1alpha2.LatestPackageRevisionKey] == porchv1alpha2.LatestPackageRevisionValue
	}), mock.Anything).Return(nil).Once()

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-v2", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			RepositoryName: "my-repo",
			PackageName:    "my-pkg",
		},
	}

	r.updateLatestRevisionLabels(t.Context(), pr)
}
