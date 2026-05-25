package packagerevision

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func captureStatusPatch(t *testing.T, mockClient *mockclient.MockClient) *porchv1alpha2.PackageRevisionStatus {
	t.Helper()
	var captured porchv1alpha2.PackageRevisionStatus
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			captured = obj.(*porchv1alpha2.PackageRevision).Status
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)
	return &captured
}

func basePR() *porchv1alpha2.PackageRevision {
	return &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pr",
			Namespace:  "default",
			Generation: 3,
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			CreationSource: "init",
		},
	}
}

func TestUpdateStatusBasic(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	captured := captureStatusPatch(t, mockClient)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := basePR()

	r.updateStatus(t.Context(), pr, nil, "clone",
		readyCondition(pr.Generation, metav1.ConditionTrue, porchv1alpha2.ReasonReady, ""),
	)

	assert.Equal(t, int64(3), captured.ObservedGeneration)
	assert.Equal(t, "clone", captured.CreationSource)
	assert.Len(t, captured.Conditions, 1)
	assert.Equal(t, porchv1alpha2.ConditionReady, captured.Conditions[0].Type)
}

func TestUpdateStatusPreservesCreationSource(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	captured := captureStatusPatch(t, mockClient)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := basePR()

	r.updateStatus(t.Context(), pr, nil, "",
		readyCondition(pr.Generation, metav1.ConditionTrue, porchv1alpha2.ReasonReady, ""),
	)

	assert.Equal(t, "init", captured.CreationSource)
}

func TestUpdateStatusWithPublishedContent(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	captured := captureStatusPatch(t, mockClient)

	content := mockrepository.NewMockPackageContent(t)
	content.EXPECT().Lifecycle(mock.Anything).Return("Published")
	content.EXPECT().Key().Return(repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{},
		WorkspaceName: "ws",
		Revision:      5,
	})
	commitTime := time.Date(2025, 7, 1, 12, 0, 0, 0, time.UTC)
	content.EXPECT().GetCommitInfo().Return(commitTime, "user@example.com")
	content.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)
	content.EXPECT().GetUpstreamLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)
	content.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "abc", "cm.yaml": "defgh"}, nil)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := basePR()

	r.updateStatus(t.Context(), pr, content, "",
		readyCondition(pr.Generation, metav1.ConditionTrue, porchv1alpha2.ReasonReady, ""),
	)

	assert.Equal(t, 5, captured.Revision)
	assert.Equal(t, "user@example.com", captured.PublishedBy)
	assert.NotNil(t, captured.PublishedAt)
	assert.Equal(t, int64(8), captured.ResourcesSizeBytes)
}

func TestUpdateStatusWithDraftContent(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	captured := captureStatusPatch(t, mockClient)

	content := mockrepository.NewMockPackageContent(t)
	content.EXPECT().Lifecycle(mock.Anything).Return("Draft")
	content.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)
	content.EXPECT().GetUpstreamLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)
	content.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "draft-content"}, nil)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := basePR()

	r.updateStatus(t.Context(), pr, content, "")

	assert.Equal(t, 0, captured.Revision)
	assert.Empty(t, captured.PublishedBy)
	assert.Nil(t, captured.PublishedAt)
	assert.Equal(t, int64(13), captured.ResourcesSizeBytes)
}

func TestUpdateRenderStatusInProgress(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	captured := captureStatusPatch(t, mockClient)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := basePR()
	pr.Status.ObservedPrrResourceVersion = "old-version"

	r.updateRenderStatus(t.Context(), pr, "v1", "",
		renderedCondition(pr.Generation, metav1.ConditionUnknown, porchv1alpha2.ReasonPending, "rendering"),
	)

	assert.Equal(t, "v1", captured.RenderingPrrResourceVersion)
	assert.Equal(t, "old-version", captured.ObservedPrrResourceVersion) // preserved
	assert.Len(t, captured.Conditions, 1)
	assert.Equal(t, metav1.ConditionUnknown, captured.Conditions[0].Status)
}

func TestUpdateRenderStatusComplete(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	captured := captureStatusPatch(t, mockClient)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := basePR()

	r.updateRenderStatus(t.Context(), pr, "", "v1",
		renderedCondition(pr.Generation, metav1.ConditionTrue, porchv1alpha2.ReasonRendered, ""),
	)

	assert.Empty(t, captured.RenderingPrrResourceVersion)
	assert.Equal(t, "v1", captured.ObservedPrrResourceVersion)
	assert.Equal(t, metav1.ConditionTrue, captured.Conditions[0].Status)
}

func TestSetRenderFailed(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)

	// setRenderFailed now makes two status patches: Rendered=False then Ready=False
	var renderPatch porchv1alpha2.PackageRevisionStatus
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			renderPatch = obj.(*porchv1alpha2.PackageRevision).Status
		}).Return(nil).Once()
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()
	mockClient.EXPECT().Status().Return(mockStatusWriter).Times(2)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := basePR()
	pr.Status.ObservedPrrResourceVersion = "prev"

	r.setRenderFailed(t.Context(), pr, assert.AnError)

	assert.Empty(t, renderPatch.RenderingPrrResourceVersion)
	assert.Equal(t, "prev", renderPatch.ObservedPrrResourceVersion) // preserved
	assert.Equal(t, metav1.ConditionFalse, renderPatch.Conditions[0].Status)
	assert.Equal(t, porchv1alpha2.ReasonRenderFailed, renderPatch.Conditions[0].Reason)
}

func TestUpdateKptfileFields(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)

	var specPatch porchv1alpha2.PackageRevisionSpec
	var statusPatch porchv1alpha2.PackageRevisionStatus

	// Spec apply (Patch on the object)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			specPatch = obj.(*porchv1alpha2.PackageRevision).Spec
		}).Return(nil)

	// Status apply
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			statusPatch = obj.(*porchv1alpha2.PackageRevision).Status
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := basePR()

	kf := kptfilev1.KptFile{
		Info: &kptfilev1.PackageInfo{
			ReadinessGates: []kptfilev1.ReadinessGate{{ConditionType: "Ready"}},
		},
		Status: &kptfilev1.Status{
			Conditions: []kptfilev1.Condition{
				{Type: "Ready", Status: kptfilev1.ConditionTrue, Reason: "AllGood"},
			},
		},
	}

	r.updateKptfileFields(t.Context(), pr, kf)

	assert.Len(t, specPatch.ReadinessGates, 1)
	assert.Equal(t, "Ready", specPatch.ReadinessGates[0].ConditionType)
	assert.Nil(t, specPatch.PackageMetadata) // no labels/annotations set on KptFile
	assert.Len(t, statusPatch.PackageConditions, 1)
	assert.Equal(t, "Ready", statusPatch.PackageConditions[0].Type)
}

func TestUpdateKptfileFieldsSkipsWhenEmpty(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	// No Patch or Status calls expected — should be a no-op.

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := basePR()

	kf := kptfilev1.KptFile{}
	r.updateKptfileFields(t.Context(), pr, kf)
}

func TestUpdateKptfileFieldsConditionsOnly(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)

	// Only status patch expected (no spec patch since no gates/meta).
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	var statusPatch porchv1alpha2.PackageRevisionStatus
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			statusPatch = obj.(*porchv1alpha2.PackageRevision).Status
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := basePR()

	kf := kptfilev1.KptFile{
		Status: &kptfilev1.Status{
			Conditions: []kptfilev1.Condition{
				{Type: "MyGate", Status: kptfilev1.ConditionTrue, Reason: "OK"},
			},
		},
	}

	r.updateKptfileFields(t.Context(), pr, kf)
	assert.Len(t, statusPatch.PackageConditions, 1)
	assert.Equal(t, "MyGate", statusPatch.PackageConditions[0].Type)
}

func TestUpdateKptfileFieldsGatesOnly(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)

	// Only spec patch expected (no status patch since no conditions).
	var specPatch porchv1alpha2.PackageRevisionSpec
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			specPatch = obj.(*porchv1alpha2.PackageRevision).Spec
		}).Return(nil)

	r := &PackageRevisionReconciler{Client: mockClient}
	pr := basePR()

	kf := kptfilev1.KptFile{
		Info: &kptfilev1.PackageInfo{
			ReadinessGates: []kptfilev1.ReadinessGate{{ConditionType: "Ready"}},
		},
	}

	r.updateKptfileFields(t.Context(), pr, kf)
	assert.Len(t, specPatch.ReadinessGates, 1)
}
