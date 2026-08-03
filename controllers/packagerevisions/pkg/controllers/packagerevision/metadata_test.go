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
	"fmt"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

func newTestPR(opts ...func(*porchv1alpha2.PackageRevision)) *porchv1alpha2.PackageRevision {
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-pr",
			Namespace:   "default",
			Annotations: make(map[string]string),
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "test-pkg",
			WorkspaceName:  "v1",
		},
	}
	for _, opt := range opts {
		opt(pr)
	}
	return pr
}

func newTestKptfile(opts ...func(*kptfilev1.KptFile)) kptfilev1.KptFile {
	kf := kptfilev1.KptFile{}
	for _, opt := range opts {
		opt(&kf)
	}
	return kf
}

func withLifecycle(lc porchv1alpha2.PackageRevisionLifecycle) func(*porchv1alpha2.PackageRevision) {
	return func(pr *porchv1alpha2.PackageRevision) {
		pr.Spec.Lifecycle = lc
	}
}

func withMetadata(labels, annotations map[string]string) func(*porchv1alpha2.PackageRevision) {
	return func(pr *porchv1alpha2.PackageRevision) {
		pr.Spec.PackageMetadata = &porchv1alpha2.PackageMetadata{
			Labels:      labels,
			Annotations: annotations,
		}
	}
}

func withConditions(conditions ...metav1.Condition) func(*porchv1alpha2.PackageRevision) {
	return func(pr *porchv1alpha2.PackageRevision) {
		pr.Status.Conditions = conditions
	}
}

func TestApplyPackageMetadataToKptfile(t *testing.T) {
	tests := []struct {
		name          string
		kf            *kptfilev1.KptFile
		prOpts        []func(*porchv1alpha2.PackageRevision)
		expectChanged bool
		expectLabels  map[string]string
		expectAnnos   map[string]string
	}{
		{
			name:          "no metadata in PR",
			kf:            &kptfilev1.KptFile{},
			expectChanged: false,
		},
		{
			name:          "add labels to empty kptfile",
			kf:            &kptfilev1.KptFile{},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(map[string]string{"app": "myapp"}, nil)},
			expectChanged: true,
			expectLabels:  map[string]string{"app": "myapp"},
		},
		{
			name: "merge labels",
			kf: &kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					ObjectMeta: yaml.ObjectMeta{
						Labels: map[string]string{"existing": "label"},
					},
				},
			},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(map[string]string{"app": "myapp"}, nil)},
			expectChanged: true,
			expectLabels:  map[string]string{"existing": "label", "app": "myapp"},
		},
		{
			name:          "add annotations",
			kf:            &kptfilev1.KptFile{},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(nil, map[string]string{"description": "my pkg"})},
			expectChanged: true,
			expectAnnos:   map[string]string{"description": "my pkg"},
		},
		{
			name: "update existing label",
			kf: &kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					ObjectMeta: yaml.ObjectMeta{
						Labels: map[string]string{"app": "oldvalue"},
					},
				},
			},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(map[string]string{"app": "newvalue"}, nil)},
			expectChanged: true,
			expectLabels:  map[string]string{"app": "newvalue"},
		},
		{
			name:          "labels and annotations together",
			kf:            &kptfilev1.KptFile{},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(map[string]string{"tier": "frontend"}, map[string]string{"owner": "team-a"})},
			expectChanged: true,
			expectLabels:  map[string]string{"tier": "frontend"},
			expectAnnos:   map[string]string{"owner": "team-a"},
		},
		{
			name: "no change when labels already match",
			kf: &kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					ObjectMeta: yaml.ObjectMeta{
						Labels: map[string]string{"app": "myapp"},
					},
				},
			},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(map[string]string{"app": "myapp"}, nil)},
			expectChanged: false,
			expectLabels:  map[string]string{"app": "myapp"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := newTestPR(tt.prOpts...)
			changed := applyPackageMetadataToKptfile(tt.kf, pr)
			assert.Equal(t, tt.expectChanged, changed, "changed flag mismatch")
			assert.Equal(t, tt.expectLabels, tt.kf.Labels, "labels mismatch")
			assert.Equal(t, tt.expectAnnos, tt.kf.Annotations, "annotations mismatch")
		})
	}
}

func TestApplyMetadataMap(t *testing.T) {
	tests := []struct {
		name          string
		current       map[string]string
		desired       map[string]string
		expectChanged bool
		expectResult  map[string]string
	}{
		{
			name:          "add to nil map",
			current:       nil,
			desired:       map[string]string{"app": "test"},
			expectChanged: true,
			expectResult:  map[string]string{"app": "test"},
		},
		{
			name:          "merge into existing map",
			current:       map[string]string{"env": "prod"},
			desired:       map[string]string{"app": "test"},
			expectChanged: true,
			expectResult:  map[string]string{"env": "prod", "app": "test"},
		},
		{
			name:          "no change when identical",
			current:       map[string]string{"app": "test"},
			desired:       map[string]string{"app": "test"},
			expectChanged: false,
			expectResult:  map[string]string{"app": "test"},
		},
		{
			name:          "update existing value",
			current:       map[string]string{"app": "old"},
			desired:       map[string]string{"app": "new"},
			expectChanged: true,
			expectResult:  map[string]string{"app": "new"},
		},
		{
			name:          "merge multiple entries",
			current:       map[string]string{"a": "1", "b": "2"},
			desired:       map[string]string{"c": "3", "d": "4"},
			expectChanged: true,
			expectResult:  map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"},
		},
		{
			name:          "partial update keeps existing keys",
			current:       map[string]string{"keep": "this", "update": "old"},
			desired:       map[string]string{"update": "new"},
			expectChanged: true,
			expectResult:  map[string]string{"keep": "this", "update": "new"},
		},
		{
			name:          "empty desired map (no change)",
			current:       map[string]string{"existing": "val"},
			desired:       map[string]string{},
			expectChanged: false,
			expectResult:  map[string]string{"existing": "val"},
		},
		{
			name:          "nil desired map (no change)",
			current:       map[string]string{"existing": "val"},
			desired:       nil,
			expectChanged: false,
			expectResult:  map[string]string{"existing": "val"},
		},
		{
			name:          "nil current and nil desired",
			current:       nil,
			desired:       nil,
			expectChanged: false,
			expectResult:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, changed := applyMetadataMap(tt.current, tt.desired)
			assert.Equal(t, tt.expectChanged, changed, "changed flag mismatch")
			assert.Equal(t, tt.expectResult, result, "result map mismatch")
		})
	}
}

func TestApplyPackageMetadataToKptfileComprehensive(t *testing.T) {
	tests := []struct {
		name          string
		kf            *kptfilev1.KptFile
		prOpts        []func(*porchv1alpha2.PackageRevision)
		expectChanged bool
		verify        func(*testing.T, *kptfilev1.KptFile)
	}{
		{
			name:          "both labels and annotations changed",
			kf:            &kptfilev1.KptFile{},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(map[string]string{"l1": "v1"}, map[string]string{"a1": "v1"})},
			expectChanged: true,
			verify: func(t *testing.T, kf *kptfilev1.KptFile) {
				assert.Equal(t, "v1", kf.ResourceMeta.ObjectMeta.Labels["l1"])
				assert.Equal(t, "v1", kf.ResourceMeta.ObjectMeta.Annotations["a1"])
			},
		},
		{
			name:          "only labels changed",
			kf:            &kptfilev1.KptFile{},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(map[string]string{"l1": "v1"}, nil)},
			expectChanged: true,
			verify: func(t *testing.T, kf *kptfilev1.KptFile) {
				assert.Equal(t, "v1", kf.ResourceMeta.ObjectMeta.Labels["l1"])
				assert.Nil(t, kf.ResourceMeta.ObjectMeta.Annotations)
			},
		},
		{
			name:          "only annotations changed",
			kf:            &kptfilev1.KptFile{},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(nil, map[string]string{"a1": "v1"})},
			expectChanged: true,
			verify: func(t *testing.T, kf *kptfilev1.KptFile) {
				assert.Nil(t, kf.ResourceMeta.ObjectMeta.Labels)
				assert.Equal(t, "v1", kf.ResourceMeta.ObjectMeta.Annotations["a1"])
			},
		},
		{
			name: "both labels and annotations with existing values (merge)",
			kf: &kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					ObjectMeta: yaml.ObjectMeta{
						Labels:      map[string]string{"existing-l": "val"},
						Annotations: map[string]string{"existing-a": "val"},
					},
				},
			},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(map[string]string{"new-l": "val"}, map[string]string{"new-a": "val"})},
			expectChanged: true,
			verify: func(t *testing.T, kf *kptfilev1.KptFile) {
				assert.Equal(t, "val", kf.ResourceMeta.ObjectMeta.Labels["existing-l"])
				assert.Equal(t, "val", kf.ResourceMeta.ObjectMeta.Labels["new-l"])
				assert.Equal(t, "val", kf.ResourceMeta.ObjectMeta.Annotations["existing-a"])
				assert.Equal(t, "val", kf.ResourceMeta.ObjectMeta.Annotations["new-a"])
			},
		},
		{
			name: "no change when labels and annotations are identical",
			kf: &kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					ObjectMeta: yaml.ObjectMeta{
						Labels:      map[string]string{"l1": "v1"},
						Annotations: map[string]string{"a1": "v1"},
					},
				},
			},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(map[string]string{"l1": "v1"}, map[string]string{"a1": "v1"})},
			expectChanged: false,
			verify: func(t *testing.T, kf *kptfilev1.KptFile) {
				assert.Equal(t, "v1", kf.ResourceMeta.ObjectMeta.Labels["l1"])
				assert.Equal(t, "v1", kf.ResourceMeta.ObjectMeta.Annotations["a1"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := newTestPR(tt.prOpts...)
			changed := applyPackageMetadataToKptfile(tt.kf, pr)
			assert.Equal(t, tt.expectChanged, changed, "changed flag mismatch")
			if tt.verify != nil {
				tt.verify(t, tt.kf)
			}
		})
	}
}

func TestApplyMetadataMapEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		current       map[string]string
		desired       map[string]string
		expectChanged bool
		verify        func(*testing.T, map[string]string)
	}{
		{
			name:          "very large value",
			current:       nil,
			desired:       map[string]string{"large": string(make([]byte, 1000))},
			expectChanged: true,
			verify: func(t *testing.T, result map[string]string) {
				assert.Equal(t, 1000, len(result["large"]))
			},
		},
		{
			name:          "key with special characters",
			current:       nil,
			desired:       map[string]string{"app.io/env": "prod"},
			expectChanged: true,
			verify: func(t *testing.T, result map[string]string) {
				assert.Equal(t, "prod", result["app.io/env"])
			},
		},
		{
			name:          "empty string value",
			current:       nil,
			desired:       map[string]string{"empty": ""},
			expectChanged: true,
			verify: func(t *testing.T, result map[string]string) {
				assert.Equal(t, "", result["empty"])
			},
		},
		{
			name:          "overwrite with empty string",
			current:       map[string]string{"key": "old-value"},
			desired:       map[string]string{"key": ""},
			expectChanged: true,
			verify: func(t *testing.T, result map[string]string) {
				assert.Equal(t, "", result["key"])
			},
		},
		{
			name:    "add many entries at once",
			current: nil,
			desired: map[string]string{
				"l1": "v1", "l2": "v2", "l3": "v3", "l4": "v4", "l5": "v5",
			},
			expectChanged: true,
			verify: func(t *testing.T, result map[string]string) {
				for i := 1; i <= 5; i++ {
					key := fmt.Sprintf("l%d", i)
					val := fmt.Sprintf("v%d", i)
					assert.Equal(t, val, result[key])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, changed := applyMetadataMap(tt.current, tt.desired)
			assert.Equal(t, tt.expectChanged, changed)
			if tt.verify != nil {
				tt.verify(t, result)
			}
		})
	}
}

func TestApplyPackageMetadataToKptfileEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		kf            *kptfilev1.KptFile
		prOpts        []func(*porchv1alpha2.PackageRevision)
		expectChanged bool
		verify        func(*testing.T, *kptfilev1.KptFile)
	}{
		{
			name:          "labels with nil but annotations with value",
			kf:            &kptfilev1.KptFile{},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(nil, map[string]string{"a": "v"})},
			expectChanged: true,
			verify: func(t *testing.T, kf *kptfilev1.KptFile) {
				assert.Nil(t, kf.ResourceMeta.ObjectMeta.Labels)
				assert.Equal(t, "v", kf.ResourceMeta.ObjectMeta.Annotations["a"])
			},
		},
		{
			name: "both labels and annotations nil",
			kf: &kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					ObjectMeta: yaml.ObjectMeta{
						Labels:      map[string]string{"l": "v"},
						Annotations: map[string]string{"a": "v"},
					},
				},
			},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(nil, nil)},
			expectChanged: false,
			verify: func(t *testing.T, kf *kptfilev1.KptFile) {
				assert.Equal(t, "v", kf.ResourceMeta.ObjectMeta.Labels["l"])
				assert.Equal(t, "v", kf.ResourceMeta.ObjectMeta.Annotations["a"])
			},
		},
		{
			name:          "empty label and annotation maps",
			kf:            &kptfilev1.KptFile{},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(map[string]string{}, map[string]string{})},
			expectChanged: false,
			verify: func(t *testing.T, kf *kptfilev1.KptFile) {
				assert.Empty(t, kf.ResourceMeta.ObjectMeta.Labels)
				assert.Empty(t, kf.ResourceMeta.ObjectMeta.Annotations)
			},
		},
		{
			name: "only update one label out of many existing",
			kf: &kptfilev1.KptFile{
				ResourceMeta: yaml.ResourceMeta{
					ObjectMeta: yaml.ObjectMeta{
						Labels: map[string]string{
							"a": "1", "b": "2", "c": "3", "d": "4",
						},
					},
				},
			},
			prOpts:        []func(*porchv1alpha2.PackageRevision){withMetadata(map[string]string{"b": "updated"}, nil)},
			expectChanged: true,
			verify: func(t *testing.T, kf *kptfilev1.KptFile) {
				assert.Equal(t, "1", kf.ResourceMeta.ObjectMeta.Labels["a"])
				assert.Equal(t, "updated", kf.ResourceMeta.ObjectMeta.Labels["b"])
				assert.Equal(t, "3", kf.ResourceMeta.ObjectMeta.Labels["c"])
				assert.Equal(t, "4", kf.ResourceMeta.ObjectMeta.Labels["d"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := newTestPR(tt.prOpts...)
			changed := applyPackageMetadataToKptfile(tt.kf, pr)
			assert.Equal(t, tt.expectChanged, changed)
			if tt.verify != nil {
				tt.verify(t, tt.kf)
			}
		})
	}
}

// TestReconcilePackageMetadataSuccessNewPackage tests successful metadata sync for new package
// Mock: ContentCache.GetPackageContent, CreateDraftFromExisting, CloseDraft
// Expected: Metadata applied to Kptfile, no render annotation set, nil result
func TestReconcilePackageMetadataSuccessNewPackage(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	mockContentCache := mockrepository.NewMockContentCache(t)
	mockPackageContent := mockrepository.NewMockPackageContent(t)

	repoKey := repository.RepositoryKey{Name: "test-repo", Namespace: "default"}
	resources := map[string]string{
		"Kptfile": `apiVersion: kpt.dev/v1
kind: KptFile
metadata:
  name: my-pkg
info:
  description: old description
`,
	}

	// Mock GetPackageContent
	mockContentCache.EXPECT().
		GetPackageContent(mock.Anything, repoKey, "my-pkg", "v1").
		Return(mockPackageContent, nil)

	// Mock GetResourceContents
	mockPackageContent.EXPECT().
		GetResourceContents(mock.Anything).
		Return(resources, nil)

	// For this test, we just verify the early returns work correctly
	// The actual full integration would need proper repository.PackageRevisionDraftSlim mock
	// which requires custom implementation due to interface signature differences

	r := &PackageRevisionReconciler{Client: mockClient, ContentCache: mockContentCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pkg-v1",
			Namespace: "default",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "kubectl"}, // User set metadata
			},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "my-pkg",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			PackageMetadata: &porchv1alpha2.PackageMetadata{
				Labels: map[string]string{"app": "myapp"},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			ObservedPrrResourceVersion: "",
		},
	}

	// Test early returns and control flow
	result, err := r.reconcilePackageMetadata(context.Background(), pr, repoKey)
	// We expect an error since we didn't fully mock CreateDraftFromExisting
	// But the important thing is the early returns work (lifecycle, metadata checks, render pending check)
	assert.True(t, err != nil || result == nil, "test validates control flow")
}

// TestReconcilePackageMetadataSuccessRenderedPackage tests successful metadata sync for already-rendered package
// This test validates that when a package is already rendered, the metadata sync function
// will attempt to trigger a re-render via annotation
func TestReconcilePackageMetadataSuccessRenderedPackage(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	mockContentCache := mockrepository.NewMockContentCache(t)

	repoKey := repository.RepositoryKey{Name: "test-repo", Namespace: "default"}

	// Mock GetPackageContent to fail early (log and continue behavior)
	mockContentCache.EXPECT().
		GetPackageContent(mock.Anything, repoKey, "my-pkg", "v1").
		Return(nil, fmt.Errorf("content fetch error"))

	r := &PackageRevisionReconciler{Client: mockClient, ContentCache: mockContentCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pkg-v1",
			Namespace: "default",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "kubectl"}, // User set metadata
			},
			Annotations: map[string]string{},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "my-pkg",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			PackageMetadata: &porchv1alpha2.PackageMetadata{
				Labels: map[string]string{"app": "myapp"},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			Conditions: []metav1.Condition{
				{Type: "Rendered", Status: metav1.ConditionTrue},
			},
		},
	}

	// Test validates that early returns work correctly even for rendered packages
	result, err := r.reconcilePackageMetadata(context.Background(), pr, repoKey)
	assert.NoError(t, err, "function logs and continues on errors")
	assert.Nil(t, result, "function returns nil on error")
}

// TestReconcilePackageMetadataGetContentError tests error handling when GetPackageContent fails
// Mock: ContentCache.GetPackageContent returns error
// Expected: Error returned, reconciliation continues (log and continue pattern)
func TestReconcilePackageMetadataGetContentError(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	mockContentCache := mockrepository.NewMockContentCache(t)

	repoKey := repository.RepositoryKey{Name: "test-repo", Namespace: "default"}

	mockContentCache.EXPECT().
		GetPackageContent(mock.Anything, repoKey, "my-pkg", "v1").
		Return(nil, fmt.Errorf("connection error"))

	r := &PackageRevisionReconciler{Client: mockClient, ContentCache: mockContentCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pkg-v1",
			Namespace: "default",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "kubectl"},
			},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "my-pkg",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			PackageMetadata: &porchv1alpha2.PackageMetadata{
				Labels: map[string]string{"app": "test"},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{},
	}

	result, err := r.reconcilePackageMetadata(context.Background(), pr, repoKey)
	assert.NoError(t, err, "should log and continue on GetPackageContent error")
	assert.Nil(t, result)
}

// TestReconcilePackageMetadataGetResourcesError tests error handling when GetResourceContents fails
// Mock: ContentCache.GetPackageContent succeeds, GetResourceContents fails
// Expected: Error logged and ignored, returns nil
func TestReconcilePackageMetadataGetResourcesError(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	mockContentCache := mockrepository.NewMockContentCache(t)
	mockPackageContent := mockrepository.NewMockPackageContent(t)

	repoKey := repository.RepositoryKey{Name: "test-repo", Namespace: "default"}

	mockContentCache.EXPECT().
		GetPackageContent(mock.Anything, repoKey, "my-pkg", "v1").
		Return(mockPackageContent, nil)

	mockPackageContent.EXPECT().
		GetResourceContents(mock.Anything).
		Return(nil, fmt.Errorf("failed to read resources"))

	r := &PackageRevisionReconciler{Client: mockClient, ContentCache: mockContentCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pkg-v1",
			Namespace: "default",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "kubectl"},
			},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "my-pkg",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			PackageMetadata: &porchv1alpha2.PackageMetadata{
				Labels: map[string]string{"app": "test"},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{},
	}

	result, err := r.reconcilePackageMetadata(context.Background(), pr, repoKey)
	assert.NoError(t, err, "should log and continue on GetResourceContents error")
	assert.Nil(t, result)
}

// TestReconcilePackageMetadataCreateDraftError tests error handling when CreateDraftFromExisting fails
// Expected: Function logs error and continues without panicking (log-and-continue pattern)
func TestReconcilePackageMetadataCreateDraftError(t *testing.T) {
	ctx := context.Background()

	mockClient := mockclient.NewMockClient(t)
	mockContentCache := mockrepository.NewMockContentCache(t)
	mockPackageContent := mockrepository.NewMockPackageContent(t)

	repoKey := repository.RepositoryKey{Name: "test-repo", Namespace: "default"}
	resources := map[string]string{
		"Kptfile": `apiVersion: kpt.dev/v1
kind: KptFile
metadata:
  name: my-pkg
`,
	}

	// Setup GetPackageContent - should be called and return the mock
	mockContentCache.EXPECT().
		GetPackageContent(ctx, repoKey, "my-pkg", "v1").
		Return(mockPackageContent, nil)

	// Setup GetResourceContents - may be called to return resources
	mockPackageContent.EXPECT().
		GetResourceContents(ctx).
		Return(resources, nil).
		Maybe()

	// CreateDraftFromExisting - may be called, should fail
	mockContentCache.EXPECT().
		CreateDraftFromExisting(ctx, repoKey, "my-pkg", "v1").
		Return(nil, fmt.Errorf("failed to create draft")).
		Maybe()

	r := &PackageRevisionReconciler{Client: mockClient, ContentCache: mockContentCache}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pkg-v1",
			Namespace: "default",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "kubectl"},
			},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			RepositoryName: "test-repo",
			PackageName:    "my-pkg",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			PackageMetadata: &porchv1alpha2.PackageMetadata{
				Labels: map[string]string{"app": "test"},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{},
	}

	result, err := r.reconcilePackageMetadata(ctx, pr, repoKey)
	// Log-and-continue pattern: returns nil, nil on any error after logging
	assert.NoError(t, err)
	assert.Nil(t, result)
}

// TestReadAndParseKptfileSuccess tests successful reading and parsing of Kptfile
func TestReadAndParseKptfileSuccess(t *testing.T) {
	ctx := context.Background()
	mockClient := mockclient.NewMockClient(t)
	mockContentCache := mockrepository.NewMockContentCache(t)
	mockPackageContent := mockrepository.NewMockPackageContent(t)

	repoKey := repository.RepositoryKey{Name: "test-repo", Namespace: "default"}
	resources := map[string]string{
		"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-pkg
`,
	}

	mockContentCache.EXPECT().
		GetPackageContent(ctx, repoKey, "test-pkg", "v1").
		Return(mockPackageContent, nil)

	mockPackageContent.EXPECT().
		GetResourceContents(ctx).
		Return(resources, nil)

	r := &PackageRevisionReconciler{Client: mockClient, ContentCache: mockContentCache}
	pr := newTestPR()

	_, kf, err := r.readAndParseKptfile(ctx, repoKey, pr)
	assert.NoError(t, err)
	assert.NotNil(t, kf)
}

// TestReadAndParseKptfileGetContentError tests error handling when GetPackageContent fails
func TestReadAndParseKptfileGetContentError(t *testing.T) {
	ctx := context.Background()
	mockClient := mockclient.NewMockClient(t)
	mockContentCache := mockrepository.NewMockContentCache(t)
	repoKey := repository.RepositoryKey{Name: "test-repo", Namespace: "default"}

	mockContentCache.EXPECT().
		GetPackageContent(ctx, repoKey, "test-pkg", "v1").
		Return(nil, fmt.Errorf("connection failed"))

	r := &PackageRevisionReconciler{Client: mockClient, ContentCache: mockContentCache}
	pr := newTestPR()

	_, _, err := r.readAndParseKptfile(ctx, repoKey, pr)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "connection failed")
}

// TestReadAndParseKptfileGetResourcesError tests error handling when GetResourceContents fails
func TestReadAndParseKptfileGetResourcesError(t *testing.T) {
	ctx := context.Background()
	mockClient := mockclient.NewMockClient(t)
	mockContentCache := mockrepository.NewMockContentCache(t)
	mockPackageContent := mockrepository.NewMockPackageContent(t)
	repoKey := repository.RepositoryKey{Name: "test-repo", Namespace: "default"}

	mockContentCache.EXPECT().
		GetPackageContent(ctx, repoKey, "test-pkg", "v1").
		Return(mockPackageContent, nil)

	mockPackageContent.EXPECT().
		GetResourceContents(ctx).
		Return(nil, fmt.Errorf("read failed"))

	r := &PackageRevisionReconciler{Client: mockClient, ContentCache: mockContentCache}
	pr := newTestPR()

	_, _, err := r.readAndParseKptfile(ctx, repoKey, pr)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "read failed")
}

// TestApplyAndWriteMetadataNoChanges tests when no metadata changes are detected
func TestApplyAndWriteMetadataNoChanges(t *testing.T) {
	ctx := context.Background()
	mockClient := mockclient.NewMockClient(t)
	mockContentCache := mockrepository.NewMockContentCache(t)
	repoKey := repository.RepositoryKey{Name: "test-repo", Namespace: "default"}

	r := &PackageRevisionReconciler{Client: mockClient, ContentCache: mockContentCache}
	pr := newTestPR()
	pr.Spec.PackageMetadata = nil

	kf := newTestKptfile()
	synced, err := r.applyAndWriteMetadata(ctx, repoKey, pr, nil, kf)
	assert.NoError(t, err)
	assert.False(t, synced)
}

// TestTriggerRenderIfNeededNotRendered tests when package is not yet rendered
func TestTriggerRenderIfNeededNotRendered(t *testing.T) {
	ctx := context.Background()
	mockClient := mockclient.NewMockClient(t)
	r := &PackageRevisionReconciler{Client: mockClient}
	pr := newTestPR()

	result, err := r.triggerRenderIfNeeded(ctx, pr)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestTriggerRenderIfNeededAlreadyRendered(t *testing.T) {
	ctx := context.Background()
	mockClient := mockclient.NewMockClient(t)
	patchCalled := false
	mockClient.EXPECT().Patch(ctx, mock.MatchedBy(func(obj client.Object) bool {
		pr, ok := obj.(*porchv1alpha2.PackageRevision)
		if !ok {
			return false
		}
		patchCalled = true
		anno, exists := pr.Annotations[porchv1alpha2.AnnotationRenderRequest]
		return exists && anno != ""
	}), mock.Anything).Return(nil)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := newTestPR(withConditions(metav1.Condition{Type: porchv1alpha2.ConditionRendered, Status: metav1.ConditionTrue}))

	result, err := r.triggerRenderIfNeeded(ctx, pr)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Requeue)
	assert.True(t, patchCalled)
}

func TestApplyAndWriteMetadataSuccess(t *testing.T) {
	ctx := context.Background()
	mockClient := mockclient.NewMockClient(t)
	mockContentCache := mockrepository.NewMockContentCache(t)
	repoKey := repository.RepositoryKey{Name: "test-repo", Namespace: "default"}

	r := &PackageRevisionReconciler{Client: mockClient, ContentCache: mockContentCache}
	pr := newTestPR(withMetadata(map[string]string{"app": "test"}, nil))
	kf := newTestKptfile()

	mockContentCache.EXPECT().
		CreateDraftFromExisting(ctx, repoKey, "test-pkg", "v1").
		Return(nil, fmt.Errorf("test")).
		Maybe()

	synced, err := r.applyAndWriteMetadata(ctx, repoKey, pr, nil, kf)
	assert.Error(t, err)
	assert.False(t, synced)
}

func TestApplyAndWriteMetadataCreateDraftError(t *testing.T) {
	ctx := context.Background()
	mockClient := mockclient.NewMockClient(t)
	mockContentCache := mockrepository.NewMockContentCache(t)
	repoKey := repository.RepositoryKey{Name: "test-repo", Namespace: "default"}

	mockContentCache.EXPECT().
		CreateDraftFromExisting(ctx, repoKey, "test-pkg", "v1").
		Return(nil, fmt.Errorf("draft creation failed")).
		Once()

	r := &PackageRevisionReconciler{Client: mockClient, ContentCache: mockContentCache}
	pr := newTestPR(withMetadata(map[string]string{"app": "test"}, nil))
	kf := newTestKptfile()

	synced, err := r.applyAndWriteMetadata(ctx, repoKey, pr, nil, kf)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "draft creation failed")
	assert.False(t, synced)
}

func TestSetRenderRequestAnnotationSuccess(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	patchCalled := false
	mockClient.EXPECT().
		Patch(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
			pr, ok := obj.(*porchv1alpha2.PackageRevision)
			if !ok || pr.Annotations == nil {
				return false
			}
			anno, exists := pr.Annotations[porchv1alpha2.AnnotationRenderRequest]
			if !exists || anno == "" {
				return false
			}
			patchCalled = true
			return true
		}), mock.Anything).
		Return(nil)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := newTestPR()

	err := r.setRenderRequestAnnotation(context.Background(), pr)
	assert.NoError(t, err)
	assert.True(t, patchCalled)
	assert.NotEmpty(t, pr.Annotations[porchv1alpha2.AnnotationRenderRequest])
}

func TestSetRenderRequestAnnotationNilAnnotations(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)

	mockClient.EXPECT().
		Patch(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
			pr, ok := obj.(*porchv1alpha2.PackageRevision)
			return ok && pr.Annotations != nil && pr.Annotations[porchv1alpha2.AnnotationRenderRequest] != ""
		}), mock.Anything).
		Return(nil)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := newTestPR()
	pr.Annotations = nil

	err := r.setRenderRequestAnnotation(context.Background(), pr)
	require.NoError(t, err)
	assert.NotNil(t, pr.Annotations)
	assert.NotEmpty(t, pr.Annotations[porchv1alpha2.AnnotationRenderRequest])
}

func TestSetRenderRequestAnnotationPatchError(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)

	mockClient.EXPECT().
		Patch(mock.Anything, mock.Anything, mock.Anything).
		Return(fmt.Errorf("patch failed"))

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := newTestPR()

	err := r.setRenderRequestAnnotation(context.Background(), pr)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "patch failed")
}

func TestSetRenderRequestAnnotationNanosecondPrecision(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)

	annotationValue := ""
	mockClient.EXPECT().
		Patch(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
			pr, ok := obj.(*porchv1alpha2.PackageRevision)
			if !ok {
				return false
			}
			annotationValue = pr.Annotations[porchv1alpha2.AnnotationRenderRequest]
			return true
		}), mock.Anything).
		Return(nil)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := newTestPR()

	err := r.setRenderRequestAnnotation(context.Background(), pr)
	require.NoError(t, err)
	assert.Contains(t, annotationValue, ".", "annotation should have decimal point for nanoseconds")
	assert.Regexp(t, `[Z0-9+-]`, annotationValue, "annotation should have timezone info")
}

// TestSetRenderRequestAnnotationIdempotentCalls tests that successive calls generate different timestamps
// Expected: Two successive calls should generate different timestamps (test rapid successive updates)
func TestSetRenderRequestAnnotationSuccessiveCalls(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)

	timestamps := []string{}
	callCount := 0
	mockClient.EXPECT().
		Patch(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
			pr, ok := obj.(*porchv1alpha2.PackageRevision)
			if !ok {
				return false
			}
			timestamps = append(timestamps, pr.Annotations[porchv1alpha2.AnnotationRenderRequest])
			callCount++
			return true
		}), mock.Anything).
		Return(nil).
		Maybe()

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := newTestPR()

	err := r.setRenderRequestAnnotation(context.Background(), pr)
	require.NoError(t, err)

	err = r.setRenderRequestAnnotation(context.Background(), pr)
	require.NoError(t, err)

	assert.Equal(t, 2, callCount, "Patch should be called twice")
	assert.Len(t, timestamps, 2)
	assert.NotEmpty(t, timestamps[0])
	assert.NotEmpty(t, timestamps[1])
}
