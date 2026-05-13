package porch

import (
	"context"
	"testing"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func v1alpha2Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, porchv1alpha2.AddToScheme(s))
	return s
}

func TestUpdatePackageRevisionApprovalV1Alpha2_ProposedToPublished(t *testing.T) {
	s := v1alpha2Scheme(t)
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "ns"},
		Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecycleProposed},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pr).Build()

	err := UpdatePackageRevisionApprovalV1Alpha2(context.Background(), c, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
	assert.NoError(t, err)
	assert.Equal(t, porchv1alpha2.PackageRevisionLifecyclePublished, pr.Spec.Lifecycle)
}

func TestUpdatePackageRevisionApprovalV1Alpha2_ProposedToDraft(t *testing.T) {
	s := v1alpha2Scheme(t)
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "ns"},
		Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecycleProposed},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pr).Build()

	err := UpdatePackageRevisionApprovalV1Alpha2(context.Background(), c, pr, porchv1alpha2.PackageRevisionLifecycleDraft)
	assert.NoError(t, err)
	assert.Equal(t, porchv1alpha2.PackageRevisionLifecycleDraft, pr.Spec.Lifecycle)
}

func TestUpdatePackageRevisionApprovalV1Alpha2_ProposedInvalid(t *testing.T) {
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecycleProposed},
	}
	err := UpdatePackageRevisionApprovalV1Alpha2(context.Background(), nil, pr, porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
	assert.Error(t, err)
}

func TestUpdatePackageRevisionApprovalV1Alpha2_DeletionProposedToPublished(t *testing.T) {
	s := v1alpha2Scheme(t)
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "ns"},
		Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecycleDeletionProposed},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pr).Build()

	err := UpdatePackageRevisionApprovalV1Alpha2(context.Background(), c, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
	assert.NoError(t, err)
}

func TestUpdatePackageRevisionApprovalV1Alpha2_DeletionProposedInvalid(t *testing.T) {
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecycleDeletionProposed},
	}
	err := UpdatePackageRevisionApprovalV1Alpha2(context.Background(), nil, pr, porchv1alpha2.PackageRevisionLifecycleDraft)
	assert.Error(t, err)
}

func TestUpdatePackageRevisionApprovalV1Alpha2_AlreadyTarget(t *testing.T) {
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
	}
	err := UpdatePackageRevisionApprovalV1Alpha2(context.Background(), nil, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
	assert.NoError(t, err)
}

func TestUpdatePackageRevisionApprovalV1Alpha2_InvalidCurrent(t *testing.T) {
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecycleDraft},
	}
	err := UpdatePackageRevisionApprovalV1Alpha2(context.Background(), nil, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
	assert.Error(t, err)
}

func TestCreateV1Alpha2Scheme(t *testing.T) {
	s, err := createV1Alpha2Scheme()
	require.NoError(t, err)
	assert.True(t, s.IsGroupRegistered(porchv1alpha2.SchemeGroupVersion.Group))
}

func TestCreateV1Alpha2RESTMapper(t *testing.T) {
	rm := createV1Alpha2RESTMapper()
	// Should be able to resolve PackageRevision
	mapping, err := rm.RESTMapping(porchv1alpha2.SchemeGroupVersion.WithKind("PackageRevision").GroupKind(), porchv1alpha2.SchemeGroupVersion.Version)
	require.NoError(t, err)
	assert.Equal(t, "packagerevisions", mapping.Resource.Resource)
}
