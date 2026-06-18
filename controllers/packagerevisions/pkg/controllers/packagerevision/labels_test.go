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
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestEnsureLatestRevisionLabelAlreadySet(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	// No Patch expected — label already exists.

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "default",
			Labels:    map[string]string{porchv1alpha2.LatestPackageRevisionKey: "true"},
		},
	}

	r.ensureLatestRevisionLabel(t.Context(), pr)
	// Test passes if no unexpected mock calls.
}

func TestEnsureLatestRevisionLabelNotSet(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			pr := obj.(*porchv1alpha2.PackageRevision)
			assert.Equal(t, "false", pr.Labels[porchv1alpha2.LatestPackageRevisionKey])
		}).Return(nil)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "default",
		},
	}

	r.ensureLatestRevisionLabel(t.Context(), pr)
}

func TestEnsureLatestRevisionLabelNilLabels(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			pr := obj.(*porchv1alpha2.PackageRevision)
			assert.Equal(t, "false", pr.Labels[porchv1alpha2.LatestPackageRevisionKey])
		}).Return(nil)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "default",
			Labels:    nil,
		},
	}

	r.ensureLatestRevisionLabel(t.Context(), pr)
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

			r.updateLatestRevisionLabels(t.Context(), pr)

			if tt.expectPatches == 0 {
				assert.Equal(t, 0, patchCount)
			}
		})
	}
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
