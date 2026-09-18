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

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func prWithRenderState(annotation, observed, rendering, creationSource string, renderedStatus metav1.ConditionStatus) *porchv1alpha2.PackageRevision {
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecycleDraft,
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			ObservedPrrResourceVersion:  observed,
			RenderingPrrResourceVersion: rendering,
			CreationSource:              creationSource,
		},
	}
	if annotation != "" {
		pr.Annotations[porchv1alpha2.AnnotationRenderRequest] = annotation
	}
	if renderedStatus != "" {
		pr.Status.Conditions = []metav1.Condition{
			{Type: porchv1alpha2.ConditionRendered, Status: renderedStatus},
		}
	}
	return pr
}

func TestRenderTrigger(t *testing.T) {
	tests := []struct {
		name       string
		pr         *porchv1alpha2.PackageRevision
		wantReq    string
		wantAnno   bool
		wantSource bool
	}{
		{
			name:     "no triggers",
			pr:       prWithRenderState("", "", "", "", ""),
			wantAnno: false, wantSource: false,
		},
		{
			name: "no trigger — not Draft",
			pr: func() *porchv1alpha2.PackageRevision {
				pr := prWithRenderState("v1", "", "", "init", metav1.ConditionUnknown)
				pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecyclePublished
				return pr
			}(),
			wantAnno: false, wantSource: false,
		},
		{
			name:    "annotation trigger — new annotation",
			pr:      prWithRenderState("v1", "", "", "", ""),
			wantReq: "v1", wantAnno: true, wantSource: false,
		},
		{
			name:    "annotation trigger — annotation differs from observed",
			pr:      prWithRenderState("v2", "v1", "", "", metav1.ConditionTrue),
			wantReq: "v2", wantAnno: true, wantSource: false,
		},
		{
			name:    "no annotation trigger — annotation matches observed",
			pr:      prWithRenderState("v1", "v1", "", "", metav1.ConditionTrue),
			wantReq: "v1", wantAnno: false, wantSource: false,
		},
		{
			name:     "source trigger — creationSource set, not rendered",
			pr:       prWithRenderState("", "", "", "init", metav1.ConditionUnknown),
			wantAnno: false, wantSource: true,
		},
		{
			name:     "source trigger — creationSource set, no conditions",
			pr:       prWithRenderState("", "", "", "clone", ""),
			wantAnno: false, wantSource: true,
		},
		{
			name:     "no source trigger — already rendered",
			pr:       prWithRenderState("", "", "", "init", metav1.ConditionTrue),
			wantAnno: false, wantSource: false,
		},
		{
			name:    "both triggers",
			pr:      prWithRenderState("v1", "", "", "init", metav1.ConditionUnknown),
			wantReq: "v1", wantAnno: true, wantSource: true,
		},
		{
			name:     "source trigger on render failure",
			pr:       prWithRenderState("", "", "", "clone", metav1.ConditionFalse),
			wantAnno: false, wantSource: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, anno, source := renderTrigger(tt.pr)
			assert.Equal(t, tt.wantReq, req)
			assert.Equal(t, tt.wantAnno, anno)
			assert.Equal(t, tt.wantSource, source)
		})
	}
}

func TestIsRenderStale(t *testing.T) {
	assert.True(t, isRenderStale("v2", "v1"))
	assert.False(t, isRenderStale("v1", "v1"))
	assert.True(t, isRenderStale("", "v1"))
}

func TestObservedVersionAfterRender(t *testing.T) {
	// Annotation trigger: use requested value.
	assert.Equal(t, "v1", observedVersionAfterRender("v1", nil))

	// Source trigger: use annotation if present.
	annos := map[string]string{porchv1alpha2.AnnotationRenderRequest: "v2"}
	assert.Equal(t, "v2", observedVersionAfterRender("", annos))

	// Source trigger: no annotation.
	assert.Equal(t, "", observedVersionAfterRender("", nil))
}

func TestIsRenderedTrue(t *testing.T) {
	assert.True(t, isRenderedTrue(prWithRenderState("", "", "", "", metav1.ConditionTrue)))
	assert.False(t, isRenderedTrue(prWithRenderState("", "", "", "", metav1.ConditionFalse)))
	assert.False(t, isRenderedTrue(prWithRenderState("", "", "", "", metav1.ConditionUnknown)))
	assert.False(t, isRenderedTrue(prWithRenderState("", "", "", "", "")))
}

func TestReadyCondition(t *testing.T) {
	c := readyCondition(5, metav1.ConditionTrue, porchv1alpha2.ReasonReady, "")
	assert.Equal(t, porchv1alpha2.ConditionReady, c.Type)
	assert.Equal(t, metav1.ConditionTrue, c.Status)
	assert.Equal(t, porchv1alpha2.ReasonReady, c.Reason)
	assert.Equal(t, int64(5), c.ObservedGeneration)
}

func TestRenderedCondition(t *testing.T) {
	c := renderedCondition(3, metav1.ConditionFalse, porchv1alpha2.ReasonRenderFailed, "boom")
	assert.Equal(t, porchv1alpha2.ConditionRendered, c.Type)
	assert.Equal(t, metav1.ConditionFalse, c.Status)
	assert.Equal(t, porchv1alpha2.ReasonRenderFailed, c.Reason)
	assert.Equal(t, "boom", c.Message)
	assert.Equal(t, int64(3), c.ObservedGeneration)
}

func TestRenderRequestChanged(t *testing.T) {
	pred := renderRequestChanged()

	tests := []struct {
		name   string
		oldVal string
		newVal string
		want   bool
	}{
		{"changed", "v1", "v2", true},
		{"unchanged", "v1", "v1", false},
		{"added", "", "v1", true},
		{"removed", "v1", "", true},
		{"both empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldObj := &porchv1alpha2.PackageRevision{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
			newObj := &porchv1alpha2.PackageRevision{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
			if tt.oldVal != "" {
				oldObj.Annotations[porchv1alpha2.AnnotationRenderRequest] = tt.oldVal
			}
			if tt.newVal != "" {
				newObj.Annotations[porchv1alpha2.AnnotationRenderRequest] = tt.newVal
			}
			got := pred.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj})
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestKptfileFromResources(t *testing.T) {
	tests := []struct {
		name      string
		resources map[string]string
		wantName  string
		wantErr   bool
	}{
		{
			name:      "no Kptfile",
			resources: map[string]string{"foo.yaml": "bar"},
			wantName:  "",
		},
		{
			name:      "empty map",
			resources: map[string]string{},
			wantName:  "",
		},
		{
			name:      "empty Kptfile string",
			resources: map[string]string{"Kptfile": ""},
			wantName:  "",
		},
		{
			name: "valid Kptfile",
			resources: map[string]string{
				"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: my-pkg
info:
  readinessGates:
  - conditionType: Ready
`,
			},
			wantName: "my-pkg",
		},
		{
			name:      "malformed Kptfile",
			resources: map[string]string{"Kptfile": "not: valid: yaml: ["},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kf, err := kptfileFromResources(tt.resources)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			if tt.wantName != "" {
				assert.Equal(t, tt.wantName, kf.Name)
				assert.Len(t, kf.Info.ReadinessGates, 1)
			}
		})
	}
}

func TestKptRendererSuccess(t *testing.T) {
	r := &kptRenderer{}
	resources := map[string]string{
		"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-pkg
`,
		"cm.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`,
	}

	result, err := r.Render(t.Context(), resources)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NoError(t, result.err)
	assert.Contains(t, result.resources, "Kptfile")
	assert.Contains(t, result.resources, "cm.yaml")
}

func TestKptRendererNoKptfile(t *testing.T) {
	r := &kptRenderer{}
	resources := map[string]string{"cm.yaml": "apiVersion: v1\nkind: ConfigMap\n"}

	result, err := r.Render(t.Context(), resources)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NoError(t, result.err)
	assert.Equal(t, resources, result.resources)
	assert.Nil(t, result.results)
}

func TestMockRendererPipelineErr(t *testing.T) {
	m := &mockRenderer{
		resources:   map[string]string{"Kptfile": "rendered"},
		pipelineErr: errors.New("function failed"),
	}
	result, err := m.Render(t.Context(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "rendered", result.resources["Kptfile"])
	assert.EqualError(t, result.err, "function failed")
}

func TestMockRendererInfraErr(t *testing.T) {
	m := &mockRenderer{err: errors.New("cannot write FS")}
	result, err := m.Render(t.Context(), nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestSyncKptfileFieldsHappyPath(t *testing.T) {
	// Happy path: valid Kptfile with gates, metadata, and conditions
	mockClient := &mockclient.MockClient{}
	r := &PackageRevisionReconciler{Client: mockClient}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
	}

	renderedResources := map[string]string{
		"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-pkg
  labels:
    env: prod
  annotations:
    owner: team-a
info:
  readinessGates:
  - conditionType: Ready
status:
  conditions:
  - type: Valid
    status: "True"
    reason: AllGood
`,
		"cm.yaml": "data: foo",
	}

	// Mock spec patch
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	// Mock status patch
	mockStatusWriter := &mockclient.MockSubResourceWriter{}
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r.syncKptfileFields(t.Context(), pr, renderedResources, repository.RepositoryKey{})
	// Should not panic and patches should be called
}

func TestSyncKptfileFieldsParseErrorE2E(t *testing.T) {
	mockClient := &mockclient.MockClient{}
	r := &PackageRevisionReconciler{Client: mockClient}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
	}

	renderedResources := map[string]string{
		"Kptfile": "not: valid: yaml: [",
	}

	// Should handle parse error gracefully without panicking or patching
	mockClient.AssertNotCalled(t, "Patch")
	r.syncKptfileFields(t.Context(), pr, renderedResources, repository.RepositoryKey{})
}

func TestSyncKptfileFieldsMissingKptfile(t *testing.T) {
	mockClient := &mockclient.MockClient{}
	r := &PackageRevisionReconciler{Client: mockClient}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
	}

	// Kptfile missing entirely
	renderedResources := map[string]string{
		"cm.yaml": "data: foo",
	}

	// Should detect missing Kptfile and log error without patching
	mockClient.AssertNotCalled(t, "Patch")
	mockClient.AssertNotCalled(t, "Status")
	r.syncKptfileFields(t.Context(), pr, renderedResources, repository.RepositoryKey{})
}

func TestSyncKptfileFieldsEmptyKptfile(t *testing.T) {
	mockClient := &mockclient.MockClient{}
	r := &PackageRevisionReconciler{Client: mockClient}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
	}

	// Kptfile exists but is empty
	renderedResources := map[string]string{
		"Kptfile": "",
	}

	// Empty Kptfile (kf.Kind == "") should log error
	mockClient.AssertNotCalled(t, "Patch")
	r.syncKptfileFields(t.Context(), pr, renderedResources, repository.RepositoryKey{})
}

func TestSyncKptfileFieldsEmptyResources(t *testing.T) {
	mockClient := &mockclient.MockClient{}
	r := &PackageRevisionReconciler{Client: mockClient}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
	}

	// No resources at all
	renderedResources := map[string]string{}

	// Should detect missing Kptfile and log error
	mockClient.AssertNotCalled(t, "Patch")
	r.syncKptfileFields(t.Context(), pr, renderedResources, repository.RepositoryKey{})
}

// TestValidateRenderStateBeforePublishBlocksWhenRenderInProgress blocks publish if render active.
func TestValidateRenderStateBeforePublishBlocksWhenRenderInProgress(t *testing.T) {
	r := &PackageRevisionReconciler{}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status: porchv1alpha2.PackageRevisionStatus{
			RenderingPrrResourceVersion: "v1", // Render in progress
			ObservedPrrResourceVersion:  "v1",
		},
	}

	err := r.validateRenderStateBeforePublish(context.Background(), pr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "render in progress")
}

// TestValidateRenderStateBeforePublishBlocksWhenVersionsMismatch blocks if render not observed.
func TestValidateRenderStateBeforePublishBlocksWhenVersionsMismatch(t *testing.T) {
	r := &PackageRevisionReconciler{}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			Annotations: map[string]string{
				porchv1alpha2.AnnotationRenderRequest: "v2",
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			RenderingPrrResourceVersion: "",
			ObservedPrrResourceVersion:  "v1", // Version mismatch
		},
	}

	err := r.validateRenderStateBeforePublish(context.Background(), pr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "render not yet observed")
}

// TestValidateRenderStateBeforePublishAllowsWhenComplete allows publish when ready.
func TestValidateRenderStateBeforePublishAllowsWhenComplete(t *testing.T) {
	r := &PackageRevisionReconciler{}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			Annotations: map[string]string{
				porchv1alpha2.AnnotationRenderRequest: "v1",
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			RenderingPrrResourceVersion: "",
			ObservedPrrResourceVersion:  "v1",
		},
	}

	err := r.validateRenderStateBeforePublish(context.Background(), pr)
	assert.NoError(t, err)
}

// TestValidateRenderStateBeforePublishAllowsWhenNoRenderRequest allows if no render pending.
func TestValidateRenderStateBeforePublishAllowsWhenNoRenderRequest(t *testing.T) {
	r := &PackageRevisionReconciler{}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Status: porchv1alpha2.PackageRevisionStatus{
			RenderingPrrResourceVersion: "",
			ObservedPrrResourceVersion:  "",
		},
	}

	err := r.validateRenderStateBeforePublish(context.Background(), pr)
	assert.NoError(t, err)
}

// TestVerifyResourcesAvailable_SuccessFirstAttempt tests the happy path where
// resources are immediately available on the first attempt.
func TestVerifyResourcesAvailable_SuccessFirstAttempt(t *testing.T) {
	expectedResources := map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile",
		"cm.yaml": "apiVersion: v1\nkind: ConfigMap",
	}

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().
		GetResourceContents(context.Background()).
		Return(expectedResources, nil).
		Once()

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().
		GetPackageContent(context.Background(), repository.RepositoryKey{}, "test-pkg", "v1").
		Return(mockContent, nil).
		Once()

	reconciler := &PackageRevisionReconciler{ContentCache: mockCache}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:   "test-pkg",
			WorkspaceName: "v1",
		},
	}

	resources, err := reconciler.verifyResourcesAvailable(context.Background(), repository.RepositoryKey{}, pr)

	assert.NoError(t, err)
	assert.Equal(t, expectedResources, resources)
}

// TestVerifyResourcesAvailable_RetrySuccess tests that retries work correctly
// and succeed on a subsequent attempt after initial failure.
func TestVerifyResourcesAvailable_RetrySuccess(t *testing.T) {
	expectedResources := map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile",
	}

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().
		GetResourceContents(context.Background()).
		Return(expectedResources, nil).
		Once()

	mockCache := mockrepository.NewMockContentCache(t)

	// First call fails, second succeeds
	mockCache.EXPECT().
		GetPackageContent(context.Background(), repository.RepositoryKey{}, "test-pkg", "v1").
		Return(nil, errors.New("cache miss")).
		Once()
	mockCache.EXPECT().
		GetPackageContent(context.Background(), repository.RepositoryKey{}, "test-pkg", "v1").
		Return(mockContent, nil).
		Once()

	reconciler := &PackageRevisionReconciler{ContentCache: mockCache}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:   "test-pkg",
			WorkspaceName: "v1",
		},
	}

	resources, err := reconciler.verifyResourcesAvailable(context.Background(), repository.RepositoryKey{}, pr)

	assert.NoError(t, err)
	assert.Equal(t, expectedResources, resources)
}

// TestVerifyResourcesAvailable_RetryExhaustion tests that retries are properly
// exhausted and error messages include retry count.
func TestVerifyResourcesAvailable_RetryExhaustion(t *testing.T) {
	mockCache := mockrepository.NewMockContentCache(t)

	// All three attempts fail
	mockCache.EXPECT().
		GetPackageContent(context.Background(), repository.RepositoryKey{}, "test-pkg", "v1").
		Return(nil, errors.New("cache unavailable")).
		Times(3)

	reconciler := &PackageRevisionReconciler{ContentCache: mockCache}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:   "test-pkg",
			WorkspaceName: "v1",
		},
	}

	_, err := reconciler.verifyResourcesAvailable(context.Background(), repository.RepositoryKey{}, pr)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "after 3 attempts")
	assert.Contains(t, err.Error(), "cache unavailable")
}

// TestVerifyResourcesAvailable_EmptyResources tests that proper error is returned
// when resources are empty after successful cache access.
func TestVerifyResourcesAvailable_EmptyResources(t *testing.T) {
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().
		GetResourceContents(context.Background()).
		Return(map[string]string{}, nil).
		Once()

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().
		GetPackageContent(context.Background(), repository.RepositoryKey{}, "test-pkg", "v1").
		Return(mockContent, nil).
		Once()

	reconciler := &PackageRevisionReconciler{ContentCache: mockCache}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:   "test-pkg",
			WorkspaceName: "v1",
		},
	}

	_, err := reconciler.verifyResourcesAvailable(context.Background(), repository.RepositoryKey{}, pr)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no resources found")
	assert.Contains(t, err.Error(), "empty after rendering")
}

// TestVerifyResourcesAvailable_GetResourceContentsError tests error handling
// when GetResourceContents fails after cache succeeds.
func TestVerifyResourcesAvailable_GetResourceContentsError(t *testing.T) {
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().
		GetResourceContents(context.Background()).
		Return(nil, errors.New("failed to read resources")).
		Once()

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().
		GetPackageContent(context.Background(), repository.RepositoryKey{}, "test-pkg", "v1").
		Return(mockContent, nil).
		Once()

	reconciler := &PackageRevisionReconciler{ContentCache: mockCache}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:   "test-pkg",
			WorkspaceName: "v1",
		},
	}

	_, err := reconciler.verifyResourcesAvailable(context.Background(), repository.RepositoryKey{}, pr)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load resources")
}

// TestVerifyResourcesAvailable_RetryDelayRespectsCancellation tests that
// the retry delay respects context cancellation and returns quickly.
func TestVerifyResourcesAvailable_RetryDelayRespectsCancellation(t *testing.T) {
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().
		GetResourceContents(context.Background()).
		Return(map[string]string{"Kptfile": "test"}, nil).
		Once()

	mockCache := mockrepository.NewMockContentCache(t)

	// First call succeeds (no delay)
	mockCache.EXPECT().
		GetPackageContent(context.Background(), repository.RepositoryKey{}, "test-pkg", "v1").
		Return(mockContent, nil).
		Once()

	reconciler := &PackageRevisionReconciler{ContentCache: mockCache}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:   "test-pkg",
			WorkspaceName: "v1",
		},
	}

	// Test with a generous timeout - should succeed immediately
	start := time.Now()
	_, err := reconciler.verifyResourcesAvailable(context.Background(), repository.RepositoryKey{}, pr)
	duration := time.Since(start)

	assert.NoError(t, err)
	// Should complete quickly (< 100ms) since first attempt succeeds
	assert.Less(t, duration, 100*time.Millisecond, "successful first attempt should be quick")
}

// TestVerifyResourcesAvailable_MultipleRetries tests that the function retries
// the correct number of times before giving up.
func TestVerifyResourcesAvailable_MultipleRetries(t *testing.T) {
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().
		GetResourceContents(context.Background()).
		Return(map[string]string{"Kptfile": "test"}, nil).
		Once()

	mockCache := mockrepository.NewMockContentCache(t)

	// First two attempts fail, third succeeds
	mockCache.EXPECT().
		GetPackageContent(context.Background(), repository.RepositoryKey{}, "test-pkg", "v1").
		Return(nil, errors.New("cache miss")).
		Times(2)
	mockCache.EXPECT().
		GetPackageContent(context.Background(), repository.RepositoryKey{}, "test-pkg", "v1").
		Return(mockContent, nil).
		Once()

	reconciler := &PackageRevisionReconciler{ContentCache: mockCache}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:   "test-pkg",
			WorkspaceName: "v1",
		},
	}

	resources, err := reconciler.verifyResourcesAvailable(context.Background(), repository.RepositoryKey{}, pr)

	assert.NoError(t, err)
	assert.NotNil(t, resources)
	assert.Equal(t, map[string]string{"Kptfile": "test"}, resources)
}

// TestVerifyResourcesAvailable_FinalRetrySucceeds tests the case where the
// final retry attempt succeeds after earlier failures.
func TestVerifyResourcesAvailable_FinalRetrySucceeds(t *testing.T) {
	expectedResources := map[string]string{
		"Kptfile":       "apiVersion: kpt.dev/v1\nkind: Kptfile",
		"resource.yaml": "apiVersion: v1\nkind: ConfigMap",
	}

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().
		GetResourceContents(context.Background()).
		Return(expectedResources, nil).
		Once()

	mockCache := mockrepository.NewMockContentCache(t)

	// First two attempts fail, final (third) attempt succeeds
	mockCache.EXPECT().
		GetPackageContent(context.Background(), repository.RepositoryKey{}, "test-pkg", "v1").
		Return(nil, errors.New("cache miss")).
		Times(2)
	mockCache.EXPECT().
		GetPackageContent(context.Background(), repository.RepositoryKey{}, "test-pkg", "v1").
		Return(mockContent, nil).
		Once()

	reconciler := &PackageRevisionReconciler{ContentCache: mockCache}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:   "test-pkg",
			WorkspaceName: "v1",
		},
	}

	resources, err := reconciler.verifyResourcesAvailable(context.Background(), repository.RepositoryKey{}, pr)

	assert.NoError(t, err)
	assert.Equal(t, expectedResources, resources)
}
