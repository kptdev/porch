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

package packagerevision

import (
	"context"
	"errors"
	"testing"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/porch/api/porch/key"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// TestCreateMainPackageRevision_MetadataErrorIsWrapped checks that a failed
// metadata apply on the refresh path is returned with context, so the caller
// requeues and the status is not refreshed against stale metadata.
func TestCreateMainPackageRevision_MetadataErrorIsWrapped(t *testing.T) {
	ctx := context.Background()
	repoKey := key.RepositoryKey{Namespace: "default", Name: "my-repo"}

	publishedPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "pub-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "main",
		},
		Status: porchv1alpha2.PackageRevisionStatus{Revision: 2},
	}

	mockClient := mockclient.NewMockClient(t)
	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetMainPackageContent(mock.Anything, repoKey, "my-pkg", "main").
		Return(mockMainContent(t), nil)

	mockClient.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Repository")).
		Return(errors.New("no repo")).Maybe()

	existing := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.my-pkg.main", Namespace: "default"},
		Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
	}
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo.my-pkg.main"},
		mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *existing
		}).Return(nil)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"),
		client.Apply, client.FieldOwner(fieldManagerPRControllerMainMeta), client.ForceOwnership).
		Return(errors.New("metadata apply failed"))

	r := newTestReconciler(mockClient, mockCache)
	err := r.createMainPackageRevision(ctx, publishedPR, repoKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to update main PackageRevision metadata")
	assert.Contains(t, err.Error(), "metadata apply failed")
}

// TestMainPlaceholderRecordsSubpackageUpstreams checks that a reconcile of the
// main placeholder (Revision -1) recomputes its subpackage upstreams from its
// own content, so a publish-triggered reconcile can drop a removed upstream.
func TestMainPlaceholderRecordsSubpackageUpstreams(t *testing.T) {
	r, c := newUDTestReconciler(t)

	content := mockrepository.NewMockPackageContent(t)
	content.EXPECT().Key().Return(key.PackageRevisionKey{
		PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "default", Name: "my-repo"}, Package: "parent-pkg"},
		Revision:      -1,
		WorkspaceName: "main",
	})
	content.EXPECT().Lifecycle(mock.Anything).Return("Published")
	content.EXPECT().GetCommitInfo().Return(time.Time{}, "")
	content.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)
	content.EXPECT().GetUpstreamLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)
	content.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"network/Kptfile": subpackageKptfile("vendor.v1", "https://example.com/vendor.git", "net", "v1", "bbb"),
	}, nil)

	pr := basePR()
	pr.Spec.WorkspaceName = "main"
	pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
	pr.Status.Revision = -1

	r.updateStatus(t.Context(), pr, content, "", "")

	// The main placeholder counts as a dependent of the subpackage upstream its
	// content clones, so the upstream object records it.
	obj := getUD(t, c, "vendor.v1")
	require.Len(t, obj.Status.Dependents, 1)
	assert.Equal(t, pr.Name, obj.Status.Dependents[0].Name)
	assert.Equal(t, "network", obj.Status.Dependents[0].Path)
}

func TestMainRefreshRequested(t *testing.T) {
	pred := mainRefreshRequested()

	tests := []struct {
		name    string
		oldVal  string
		newVal  string
		wantHit bool
	}{
		{name: "value changed fires", oldVal: "3", newVal: "4", wantHit: true},
		{name: "value added fires", oldVal: "", newVal: "4", wantHit: true},
		{name: "unchanged does not fire", oldVal: "4", newVal: "4", wantHit: false},
		{name: "absent on both does not fire", oldVal: "", newVal: "", wantHit: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldObj := &porchv1alpha2.PackageRevision{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
			newObj := &porchv1alpha2.PackageRevision{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
			if tt.oldVal != "" {
				oldObj.Annotations[AnnotationMainRefresh] = tt.oldVal
			}
			if tt.newVal != "" {
				newObj.Annotations[AnnotationMainRefresh] = tt.newVal
			}
			assert.Equal(t, tt.wantHit, pred.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}))
		})
	}
}
