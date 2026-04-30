package packagerevision

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	configapi "github.com/nephio-project/porch/api/porchconfig/v1alpha1"
	"github.com/nephio-project/porch/pkg/repository"
	mockclient "github.com/nephio-project/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockrepository "github.com/nephio-project/porch/test/mockery/mocks/porch/pkg/repository"
)

// setupMockContentDefaults adds common Maybe() expectations for PackageContent methods
// that updateStatus may call.
func setupMockContentDefaults(m *mockrepository.MockPackageContent) {
	m.EXPECT().Key().Return(repository.PackageRevisionKey{}).Maybe()
	m.EXPECT().GetCommitInfo().Return(time.Time{}, "").Maybe()
	m.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Maybe()
	m.EXPECT().GetUpstreamLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Maybe()
}

func newTestReconciler(mockClient *mockclient.MockClient, cache *mockrepository.MockContentCache) *PackageRevisionReconciler {
	return &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: cache,
	}
}

// readyObjectMeta returns a standard ObjectMeta with finalizer and ownerReference already set,
// representing a PR that has already been reconciled at least once.
func readyObjectMeta(name, namespace, repoName string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:       name,
		Namespace:  namespace,
		Finalizers: []string{porchv1alpha2.PackageRevisionFinalizer},
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: configapi.GroupVersion.Identifier(),
			Kind:       configapi.TypeRepository.Kind,
			Name:       repoName,
			UID:        "repo-uid",
		}},
	}
}

func TestReconcileNotFound(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "missing-pr", Namespace: "default"}}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, &porchv1alpha2.PackageRevision{}).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "missing-pr"))

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileGetError(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, &porchv1alpha2.PackageRevision{}).
		Return(errors.New("api server unavailable"))

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	_, err := r.Reconcile(ctx, req)

	assert.Error(t, err)
}

func TestReconcileFinalizerAddedWhenMissing(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			RepositoryName: "my-repo",
		},
	}

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo", Namespace: "default", UID: "repo-uid-123"},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"}, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*configapi.Repository) = *repo
		}).Return(nil)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			pr := obj.(*porchv1alpha2.PackageRevision)
			assert.Contains(t, pr.GetFinalizers(), porchv1alpha2.PackageRevisionFinalizer)
			require.Len(t, pr.OwnerReferences, 1)
			assert.Equal(t, "my-repo", pr.OwnerReferences[0].Name)
			assert.Equal(t, types.UID("repo-uid-123"), pr.OwnerReferences[0].UID)
			assert.Equal(t, configapi.TypeRepository.Kind, pr.OwnerReferences[0].Kind)
		}).Return(nil)

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileFinalizerAddPatchFails(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			RepositoryName: "my-repo",
		},
	}

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo", Namespace: "default", UID: "repo-uid-123"},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"}, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*configapi.Repository) = *repo
		}).Return(nil)
	mockClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("conflict"))

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	_, err := r.Reconcile(ctx, req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to patch finalizer/ownerReference")
}

func TestReconcileDeletionBlockedWhenPublishedAndRepoExists(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{porchv1alpha2.PackageRevisionFinalizer},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
			RepositoryName: "my-repo",
		},
	}

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo", Namespace: "default", UID: "repo-uid-123"},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"}, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*configapi.Repository) = *repo
		}).Return(nil)
	// No Patch expected — finalizer must NOT be removed.

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileDeletionAllowedWhenPublishedButRepoGone(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{porchv1alpha2.PackageRevisionFinalizer},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
			RepositoryName: "deleted-repo",
		},
	}

	var patched bool
	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "deleted-repo"}, mock.AnythingOfType("*v1alpha1.Repository")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "deleted-repo"))
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			patched = true
			assert.NotContains(t, obj.GetFinalizers(), porchv1alpha2.PackageRevisionFinalizer)
		}).Return(nil)
	// No List expected — updateLatestRevisionLabels is skipped when repo is gone.

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().DeletePackage(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
	assert.True(t, patched, "finalizer should be removed when repo is gone")
}

func TestReconcileDeletionAllowedForDraft(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{porchv1alpha2.PackageRevisionFinalizer},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	var patched bool
	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			patched = true
			assert.NotContains(t, obj.GetFinalizers(), porchv1alpha2.PackageRevisionFinalizer)
		}).Return(nil)
	// updateLatestRevisionLabels is called after finalizer removal
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).Return(nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().DeletePackage(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
	assert.True(t, patched, "finalizer should have been removed for Draft package")
}

func TestReconcileDeletionAllowedWhenDeletionProposed(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{porchv1alpha2.PackageRevisionFinalizer},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecycleDeletionProposed,
		},
	}

	var patched bool
	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			patched = true
			assert.NotContains(t, obj.GetFinalizers(), porchv1alpha2.PackageRevisionFinalizer)
		}).Return(nil)
	// updateLatestRevisionLabels is called after finalizer removal
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).Return(nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().DeletePackage(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
	assert.True(t, patched, "finalizer should have been removed via Patch")
}

func TestReconcileDeletionRemoveFinalizerPatchFails(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{porchv1alpha2.PackageRevisionFinalizer},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecycleDeletionProposed,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("conflict"))

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().DeletePackage(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	r := newTestReconciler(mockClient, mockCache)
	_, err := r.Reconcile(ctx, req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to remove finalizer")
}

func TestReconcileDeletionProposedNoFinalizer(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{}, // already removed
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecycleDeletionProposed,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	// No Patch expected — finalizer already absent.
	
	// updateLatestRevisionLabels is called after finalizer removal
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).Return(nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().DeletePackage(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileDeletionGitCleanupFailsBlocksFinalizer(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{porchv1alpha2.PackageRevisionFinalizer},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecycleDeletionProposed,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	// No Patch expected — git cleanup fails so finalizer must NOT be removed.

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().DeletePackage(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("git push failed"))

	r := newTestReconciler(mockClient, mockCache)
	_, err := r.Reconcile(ctx, req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete package from git")
}

func TestReconcileDeletionGitCleanupFailsBestEffortForPublishedRepoGone(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{porchv1alpha2.PackageRevisionFinalizer},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
			RepositoryName: "deleted-repo",
		},
	}

	var patched bool
	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "deleted-repo"}, mock.AnythingOfType("*v1alpha1.Repository")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "deleted-repo"))
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			patched = true
			assert.NotContains(t, obj.GetFinalizers(), porchv1alpha2.PackageRevisionFinalizer)
		}).Return(nil)

	mockCache := mockrepository.NewMockContentCache(t)
	// Git cleanup fails, but since repo is gone this is best-effort — finalizer should still be removed.
	mockCache.EXPECT().DeletePackage(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("repo not in cache"))

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
	assert.True(t, patched, "finalizer should be removed even when best-effort git cleanup fails")
}

func TestReconcileDeletionAllowedForProposed(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{porchv1alpha2.PackageRevisionFinalizer},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecycleProposed,
		},
	}

	var patched bool
	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			patched = true
			assert.NotContains(t, obj.GetFinalizers(), porchv1alpha2.PackageRevisionFinalizer)
		}).Return(nil)
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).Return(nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().DeletePackage(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
	assert.True(t, patched, "finalizer should have been removed for Proposed package")
}

func TestReconcileDeletionPassesCorrectRepoKey(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "my-ns"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "my-ns",
			DeletionTimestamp: &now,
			Finalizers:        []string{porchv1alpha2.PackageRevisionFinalizer},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDeletionProposed,
			RepositoryName: "my-repo",
			PackageName:    "my-pkg",
			WorkspaceName:  "ws-v1",
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).Return(nil)
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).Return(nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().DeletePackage(mock.Anything,
		repository.RepositoryKey{Namespace: "my-ns", Name: "my-repo"},
		"my-pkg", "ws-v1",
	).Return(nil)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileDeletionPRAlreadyGoneOnPatch(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{porchv1alpha2.PackageRevisionFinalizer},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecycleDeletionProposed,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	// PR disappeared between DeletePackage and Patch — NotFound should be silently ignored.
	mockClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "test-pr"))

	// updateLatestRevisionLabels is called after finalizer removal
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).Return(nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().DeletePackage(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err, "NotFound on Patch should be silently ignored")
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileDeletionPublishedTransientAPIError(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{porchv1alpha2.PackageRevisionFinalizer},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
			RepositoryName: "my-repo",
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	// Repo lookup returns a transient error — should NOT fall into best-effort path.
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"}, mock.AnythingOfType("*v1alpha1.Repository")).
		Return(errors.New("api server timeout"))
	// No Patch, no DeletePackage — error should propagate.

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	_, err := r.Reconcile(ctx, req)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check owner repository")
}

func TestReconcileDeletionGitPackageNotFoundIsNoop(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	now := metav1.Now()
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pr",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{porchv1alpha2.PackageRevisionFinalizer},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	var patched bool
	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			patched = true
			assert.NotContains(t, obj.GetFinalizers(), porchv1alpha2.PackageRevisionFinalizer)
		}).Return(nil)
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).Return(nil)

	mockCache := mockrepository.NewMockContentCache(t)
	// Package never existed in git — "not found" should be treated as success.
	mockCache.EXPECT().DeletePackage(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("package revision /  not found in repository default/"))

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
	assert.True(t, patched, "finalizer should be removed when package doesn't exist in git")
}

func TestReconcileOwnerRefAlreadySet(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pr",
			Namespace:  "default",
			Finalizers: []string{porchv1alpha2.PackageRevisionFinalizer},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: configapi.GroupVersion.Identifier(),
				Kind:       configapi.TypeRepository.Kind,
				Name:       "my-repo",
				UID:        "repo-uid-123",
			}},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{RepositoryName: "my-repo"},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	// No Patch, no repo Get — both finalizer and ownerRef already present.

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileOwnerRefRepoLookupFails(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{RepositoryName: "missing-repo"},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "missing-repo"}, mock.AnythingOfType("*v1alpha1.Repository")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "missing-repo"))
	// Finalizer still needs to be added even if ownerRef lookup fails.
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			pr := obj.(*porchv1alpha2.PackageRevision)
			assert.Contains(t, pr.GetFinalizers(), porchv1alpha2.PackageRevisionFinalizer)
			assert.Empty(t, pr.OwnerReferences, "ownerRef should not be set when repo lookup fails")
		}).Return(nil)

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileEmptyLifecycle(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pr", Namespace: "default",
			Finalizers: []string{porchv1alpha2.PackageRevisionFinalizer},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: configapi.GroupVersion.Identifier(),
				Kind:       configapi.TypeRepository.Kind,
				Name:       "my-repo",
				UID:        "repo-uid",
			}},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{RepositoryName: "my-repo"},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileNoChange(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().Lifecycle(mock.Anything).Return("Draft")
	setupMockContentDefaults(mockContent)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "my-pkg", "ws-1").Return(mockContent, nil)

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileLifecycleTransition(t *testing.T) {
	tests := []struct {
		name    string
		current string
		desired porchv1alpha2.PackageRevisionLifecycle
	}{
		{"Draft to Proposed", "Draft", porchv1alpha2.PackageRevisionLifecycleProposed},
		{"Proposed to Draft", "Proposed", porchv1alpha2.PackageRevisionLifecycleDraft},
		{"Proposed to Published", "Proposed", porchv1alpha2.PackageRevisionLifecyclePublished},
		{"Published to DeletionProposed", "Published", porchv1alpha2.PackageRevisionLifecycleDeletionProposed},
		{"DeletionProposed to Published", "DeletionProposed", porchv1alpha2.PackageRevisionLifecyclePublished},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

			pr := &porchv1alpha2.PackageRevision{
				ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
				Spec: porchv1alpha2.PackageRevisionSpec{
					PackageName:    "my-pkg",
					RepositoryName: "my-repo",
					WorkspaceName:  "ws-1",
					Lifecycle:      tt.desired,
				},
			}

			mockClient := mockclient.NewMockClient(t)
			mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
				Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
					*obj.(*porchv1alpha2.PackageRevision) = *pr
				}).Return(nil)

			mockContent := mockrepository.NewMockPackageContent(t)
			mockContent.EXPECT().Lifecycle(mock.Anything).Return(tt.current)

			updatedContent := mockrepository.NewMockPackageContent(t)
			updatedContent.EXPECT().Lifecycle(mock.Anything).Return(string(tt.desired)).Maybe()
			setupMockContentDefaults(updatedContent)

			mockCache := mockrepository.NewMockContentCache(t)
			mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "my-pkg", "ws-1").Return(mockContent, nil)
			mockCache.EXPECT().UpdateLifecycle(mock.Anything, mock.Anything, "my-pkg", "ws-1", string(tt.desired)).Return(updatedContent, nil)

			mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
			mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
			mockClient.EXPECT().Status().Return(mockStatusWriter)

			r := newTestReconciler(mockClient, mockCache)
			result, err := r.Reconcile(ctx, req)

			assert.NoError(t, err)
			if porchv1alpha2.LifecycleIsPublished(tt.desired) {
				assert.Equal(t, ctrl.Result{Requeue: true}, result)
			} else {
				assert.Equal(t, ctrl.Result{}, result)
			}
		})
	}
}

func TestReconcileLifecycleTransitionFailure(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleProposed,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().Lifecycle(mock.Anything).Return("Draft")

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "my-pkg", "ws-1").Return(mockContent, nil)
	mockCache.EXPECT().UpdateLifecycle(mock.Anything, mock.Anything, "my-pkg", "ws-1", "Proposed").Return(nil, errors.New("git push failed"))

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			pr := obj.(*porchv1alpha2.PackageRevision)
			assert.Len(t, pr.Status.Conditions, 1)
			assert.Equal(t, porchv1alpha2.ConditionReady, pr.Status.Conditions[0].Type)
			assert.Equal(t, metav1.ConditionFalse, pr.Status.Conditions[0].Status)
			assert.Equal(t, porchv1alpha2.ReasonFailed, pr.Status.Conditions[0].Reason)
			assert.Contains(t, pr.Status.Conditions[0].Message, "git push failed")
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	// Controller returns nil error (failure is recorded in status)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileGetPackageContentFailure(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "my-pkg", "ws-1").Return(nil, errors.New("repository not found"))

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			pr := obj.(*porchv1alpha2.PackageRevision)
			assert.Len(t, pr.Status.Conditions, 1)
			assert.Equal(t, metav1.ConditionFalse, pr.Status.Conditions[0].Status)
			assert.Equal(t, porchv1alpha2.ReasonFailed, pr.Status.Conditions[0].Reason)
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// --- Source execution tests ---

// fakeDraftSlim is a simple test double for PackageRevisionDraftSlim.
type fakeDraftSlim struct {
	resources map[string]string
	commitMsg string
	lifecycle string
	updateErr error
}

func (f *fakeDraftSlim) Key() repository.PackageRevisionKey {
	return repository.PackageRevisionKey{}
}

func (f *fakeDraftSlim) UpdateResources(_ context.Context, resources map[string]string, commitMsg string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.resources = resources
	f.commitMsg = commitMsg
	return nil
}

func (f *fakeDraftSlim) UpdateLifecycle(_ context.Context, lifecycle string) error {
	f.lifecycle = lifecycle
	return nil
}

func TestReconcileInitSource(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			Source: &porchv1alpha2.PackageSource{
				Init: &porchv1alpha2.PackageInitSpec{
					Description: "test package",
				},
			},
		},
		// No CreationSource — triggers source execution
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	mockDraft := &fakeDraftSlim{}

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().CreateNewDraft(mock.Anything, repository.RepositoryKey{Namespace: "default", Name: "my-repo"}, "my-pkg", "ws-1", "Draft").Return(mockDraft, nil)
	mockCache.EXPECT().CloseDraft(mock.Anything, repository.RepositoryKey{Namespace: "default", Name: "my-repo"}, mockDraft, 0).Return(nil)

	// After CloseDraft, reconcileSource reads back the content for lock info
	mockContentAfterInit := mockrepository.NewMockPackageContent(t)
	mockContentAfterInit.EXPECT().Lifecycle(mock.Anything).Return("Draft").Maybe()
	setupMockContentDefaults(mockContentAfterInit)
	mockCache.EXPECT().GetPackageContent(mock.Anything, repository.RepositoryKey{Namespace: "default", Name: "my-repo"}, "my-pkg", "ws-1").Return(mockContentAfterInit, nil)

	// Expect SSA status patches: updateStatus (Ready) then updateRenderStatus (Rendered)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	var statusPatches []porchv1alpha2.PackageRevisionStatus
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			statusPatches = append(statusPatches, obj.(*porchv1alpha2.PackageRevision).Status)
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	// Expect merge patch for latest-revision label
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).Return(nil)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{Requeue: true}, result)

	// Verify init created a Kptfile
	assert.Contains(t, mockDraft.resources, "Kptfile")
	assert.Equal(t, "init", mockDraft.commitMsg)

	// First patch: updateStatus with Ready=False/Pending and creationSource
	require.GreaterOrEqual(t, len(statusPatches), 1)
	assert.Equal(t, "init", statusPatches[0].CreationSource)
	var readyCond metav1.Condition
	for _, c := range statusPatches[0].Conditions {
		if c.Type == porchv1alpha2.ConditionReady {
			readyCond = c
		}
	}
	assert.Equal(t, metav1.ConditionFalse, readyCond.Status)
	assert.Equal(t, porchv1alpha2.ReasonPending, readyCond.Reason)

	// Second patch: updateRenderStatus with Rendered=Unknown/Pending
	require.GreaterOrEqual(t, len(statusPatches), 2)
	var renderedCond metav1.Condition
	for _, c := range statusPatches[1].Conditions {
		if c.Type == porchv1alpha2.ConditionRendered {
			renderedCond = c
		}
	}
	assert.Equal(t, metav1.ConditionUnknown, renderedCond.Status)
	assert.Equal(t, porchv1alpha2.ReasonPending, renderedCond.Reason)
}

func TestReconcileInitSourceAlreadyCreated(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	// CreationSource already set — source execution should be skipped
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			Source: &porchv1alpha2.PackageSource{
				Init: &porchv1alpha2.PackageInitSpec{Description: "test"},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			CreationSource: "init",
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().Lifecycle(mock.Anything).Return("Draft")
	setupMockContentDefaults(mockContent)

	mockCache := mockrepository.NewMockContentCache(t)
	// No CreateNewDraft expected — source already applied
	mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "my-pkg", "ws-1").Return(mockContent, nil)

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileInitSourceCreateDraftFails(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			Source: &porchv1alpha2.PackageSource{
				Init: &porchv1alpha2.PackageInitSpec{Description: "test"},
			},
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().CreateNewDraft(mock.Anything, mock.Anything, "my-pkg", "ws-1", "Draft").
		Return(nil, errors.New("repo not found"))

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			pr := obj.(*porchv1alpha2.PackageRevision)
			// Should have both Ready=False and Rendered=False
			assert.Len(t, pr.Status.Conditions, 2)
			for _, c := range pr.Status.Conditions {
				assert.Equal(t, metav1.ConditionFalse, c.Status)
				assert.Equal(t, porchv1alpha2.ReasonFailed, c.Reason)
				assert.Contains(t, c.Message, "repo not found")
			}
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileNoSource(t *testing.T) {
	// PR with no Source and no CreationSource — discovered from git by repo controller.
	// Should skip source execution and go straight to lifecycle.
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			// No Source — git-discovered package
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().Lifecycle(mock.Anything).Return("Draft")
	setupMockContentDefaults(mockContent)

	mockCache := mockrepository.NewMockContentCache(t)
	// No CreateNewDraft expected
	mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "my-pkg", "ws-1").Return(mockContent, nil)

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}


// mockRenderer is a test double for the renderer interface.
type mockRenderer struct {
	resources   map[string]string
	err         error // infrastructure error
	pipelineErr error // pipeline failure (returned via renderResult.err)
}

func (m *mockRenderer) Render(_ context.Context, _ map[string]string) (*renderResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &renderResult{
		resources: m.resources,
		err:       m.pipelineErr,
	}, nil
}

// helper to build a PR for render tests.
func renderTestPR(annotation, observed, rendering, creationSource string, renderedStatus metav1.ConditionStatus) *porchv1alpha2.PackageRevision {
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-pr",
			Namespace:   "default",
			Annotations: map[string]string{},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
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

var testRepoKey = repository.RepositoryKey{Namespace: "default", Name: "my-repo"}

// noStaleReader returns a mock apiReader for the stale check that returns
// a PR with no render-request annotation (i.e. no push happened during render).
func noStaleReader(t *testing.T) *mockclient.MockReader {
	t.Helper()
	prAfterRender := renderTestPR("", "", "", "init", metav1.ConditionTrue)
	mockReader := mockclient.NewMockReader(t)
	mockReader.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "test-pr"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *prAfterRender
		}).Return(nil)
	return mockReader
}

func TestReconcileRenderNoRenderer(t *testing.T) {
	r := &PackageRevisionReconciler{Renderer: nil}
	pr := renderTestPR("v1", "", "", "init", metav1.ConditionUnknown)
	result, err := r.reconcileRender(t.Context(), pr, testRepoKey)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestReconcileRenderNoTrigger(t *testing.T) {
	r := &PackageRevisionReconciler{Renderer: &mockRenderer{}}
	pr := renderTestPR("", "", "", "", "")
	result, err := r.reconcileRender(t.Context(), pr, testRepoKey)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestReconcileRenderAlreadyRendered(t *testing.T) {
	r := &PackageRevisionReconciler{Renderer: &mockRenderer{}}
	pr := renderTestPR("v1", "v1", "", "init", metav1.ConditionTrue)
	result, err := r.reconcileRender(t.Context(), pr, testRepoKey)
	assert.NoError(t, err)
	assert.Nil(t, result)
}


func TestReconcileRenderSourceTrigger(t *testing.T) {
	ctx := t.Context()
	rendered := map[string]string{"Kptfile": "rendered"}

	mockCache := mockrepository.NewMockContentCache(t)
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "original"}, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockContent, nil)

	mockDraft := &fakeDraftSlim{}
	mockCache.EXPECT().CreateDraftFromExisting(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockDraft, nil)
	mockCache.EXPECT().CloseDraft(mock.Anything, testRepoKey, mockDraft, 0).Return(nil)

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
		Renderer:     &mockRenderer{resources: rendered},
		apiReader:    noStaleReader(t),
	}

	pr := renderTestPR("", "", "", "init", metav1.ConditionUnknown)
	result, err := r.reconcileRender(ctx, pr, testRepoKey)
	assert.NoError(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "rendered", mockDraft.resources["Kptfile"])
}

func TestReconcileRenderAnnotationTrigger(t *testing.T) {
	ctx := t.Context()
	rendered := map[string]string{"Kptfile": "rendered"}

	mockCache := mockrepository.NewMockContentCache(t)
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "original"}, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockContent, nil)

	mockDraft := &fakeDraftSlim{}
	mockCache.EXPECT().CreateDraftFromExisting(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockDraft, nil)
	mockCache.EXPECT().CloseDraft(mock.Anything, testRepoKey, mockDraft, 0).Return(nil)

	// Re-read for stale check returns same annotation (via apiReader, bypasses informer cache).
	prAfterRender := renderTestPR("v1", "", "", "init", metav1.ConditionTrue)
	mockReader := mockclient.NewMockReader(t)
	mockReader.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "test-pr"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *prAfterRender
		}).Return(nil)

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
		Renderer:     &mockRenderer{resources: rendered},
		apiReader:    mockReader,
	}

	pr := renderTestPR("v1", "", "", "init", metav1.ConditionUnknown)
	result, err := r.reconcileRender(ctx, pr, testRepoKey)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestReconcileRenderStale(t *testing.T) {
	ctx := t.Context()

	mockCache := mockrepository.NewMockContentCache(t)
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "original"}, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockContent, nil)

	// Re-read returns different annotation — stale.
	prAfterRender := renderTestPR("v2", "", "", "", "")
	mockReader := mockclient.NewMockReader(t)
	mockReader.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "test-pr"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *prAfterRender
		}).Return(nil)

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
		Renderer:     &mockRenderer{resources: map[string]string{"Kptfile": "rendered"}},
		apiReader:    mockReader,
	}

	pr := renderTestPR("v1", "", "", "", metav1.ConditionUnknown)
	pr.Status.CreationSource = "init"
	result, err := r.reconcileRender(ctx, pr, testRepoKey)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Requeue)
}

func TestReconcileRenderFailure(t *testing.T) {
	ctx := t.Context()

	mockCache := mockrepository.NewMockContentCache(t)
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "original"}, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockContent, nil)

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(3)
	mockClient.EXPECT().Status().Return(mockStatusWriter).Times(3)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
		Renderer:     &mockRenderer{err: errors.New("fn-runner unavailable")},
	}

	pr := renderTestPR("", "", "", "init", metav1.ConditionUnknown)
	result, err := r.reconcileRender(ctx, pr, testRepoKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "render failed")
	assert.Nil(t, result)
}

func TestReconcileRenderReadContentFails(t *testing.T) {
	ctx := t.Context()

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(nil, errors.New("repo not found"))

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
		Renderer:     &mockRenderer{},
	}

	pr := renderTestPR("", "", "", "init", metav1.ConditionUnknown)
	result, err := r.reconcileRender(ctx, pr, testRepoKey)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "get content for render")
}

func TestReconcileRenderWriteFails(t *testing.T) {
	ctx := t.Context()

	mockCache := mockrepository.NewMockContentCache(t)
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "original"}, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockContent, nil)
	mockCache.EXPECT().CreateDraftFromExisting(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(nil, errors.New("draft failed"))

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
		Renderer:     &mockRenderer{resources: map[string]string{"Kptfile": "rendered"}},
		apiReader:    noStaleReader(t),
	}

	pr := renderTestPR("", "", "", "init", metav1.ConditionUnknown)
	result, err := r.reconcileRender(ctx, pr, testRepoKey)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "create draft for render")
}

func TestReconcileSourceUpdateResourcesFails(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			Source:         &porchv1alpha2.PackageSource{Init: &porchv1alpha2.PackageInitSpec{}},
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	badDraft := &fakeDraftSlim{updateErr: errors.New("write failed")}
	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().CreateNewDraft(mock.Anything, mock.Anything, "my-pkg", "ws-1", "Draft").Return(badDraft, nil)

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err) // error handled internally, status updated
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileSourceCloseDraftFails(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			Source:         &porchv1alpha2.PackageSource{Init: &porchv1alpha2.PackageInitSpec{}},
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	mockDraft := &fakeDraftSlim{}
	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().CreateNewDraft(mock.Anything, mock.Anything, "my-pkg", "ws-1", "Draft").Return(mockDraft, nil)
	mockCache.EXPECT().CloseDraft(mock.Anything, mock.Anything, mockDraft, 0).Return(errors.New("git push failed"))

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}


func TestReconcileRenderErrorSetsStatus(t *testing.T) {
	// Reconcile should handle reconcileRender returning an error
	// by logging and returning (no crash, no requeue).
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := renderTestPR("", "", "", "init", metav1.ConditionUnknown)
	pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleDraft
	pr.Spec.RepositoryName = "my-repo"
	pr.Finalizers = []string{porchv1alpha2.PackageRevisionFinalizer}
	pr.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: configapi.GroupVersion.Identifier(),
		Kind:       configapi.TypeRepository.Kind,
		Name:       "my-repo",
		UID:        "repo-uid",
	}}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").
		Return(nil, errors.New("cache down"))

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
		Renderer:     &mockRenderer{},
	}

	result, err := r.Reconcile(ctx, req)
	assert.NoError(t, err) // error handled internally
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileRenderStaleReReadFails(t *testing.T) {
	ctx := t.Context()

	mockCache := mockrepository.NewMockContentCache(t)
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "x"}, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockContent, nil)

	mockReader := mockclient.NewMockReader(t)
	// Re-read for stale check fails.
	mockReader.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "test-pr"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(errors.New("api server gone"))

	mockClient := mockclient.NewMockClient(t)

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
		Renderer:     &mockRenderer{resources: map[string]string{"Kptfile": "rendered"}},
		apiReader:    mockReader,
	}

	pr := renderTestPR("v1", "", "", "", metav1.ConditionUnknown)
	pr.Status.CreationSource = "init"
	result, err := r.reconcileRender(ctx, pr, testRepoKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "re-read PR after render")
	assert.Nil(t, result)
}

func TestReadPackageResourcesGetContentsFails(t *testing.T) {
	mockCache := mockrepository.NewMockContentCache(t)
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(mock.Anything).Return(nil, errors.New("corrupt"))
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockContent, nil)

	r := &PackageRevisionReconciler{ContentCache: mockCache}
	_, err := r.readPackageResources(t.Context(), testRepoKey, "my-pkg", "ws-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read resources for render")
}

func TestWriteRenderedResourcesUpdateFails(t *testing.T) {
	mockDraft := &fakeDraftSlim{updateErr: errors.New("write failed")}
	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().CreateDraftFromExisting(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockDraft, nil)

	r := &PackageRevisionReconciler{ContentCache: mockCache}
	err := r.writeRenderedResources(t.Context(), testRepoKey, "my-pkg", "ws-1", map[string]string{"Kptfile": "x"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "write rendered resources")
}

func TestWriteRenderedResourcesCloseDraftFails(t *testing.T) {
	mockDraft := &fakeDraftSlim{}
	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().CreateDraftFromExisting(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockDraft, nil)
	mockCache.EXPECT().CloseDraft(mock.Anything, testRepoKey, mockDraft, 0).Return(errors.New("git push failed"))

	r := &PackageRevisionReconciler{ContentCache: mockCache}
	err := r.writeRenderedResources(t.Context(), testRepoKey, "my-pkg", "ws-1", map[string]string{"Kptfile": "x"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "close draft after render")
}


func TestReconcileRenderPipelineFailureNoPush(t *testing.T) {
	ctx := t.Context()

	mockCache := mockrepository.NewMockContentCache(t)
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "original"}, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockContent, nil)

	// No CreateDraftFromExisting expected — resources should NOT be written.

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(3)
	mockClient.EXPECT().Status().Return(mockStatusWriter).Times(3)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
		Renderer:     &mockRenderer{resources: map[string]string{"Kptfile": "partial"}, pipelineErr: errors.New("function failed")},
		apiReader:    noStaleReader(t),
	}

	pr := renderTestPR("", "", "", "init", metav1.ConditionUnknown)
	result, err := r.reconcileRender(ctx, pr, testRepoKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "render pipeline failed")
	assert.Nil(t, result)
}

func TestReconcileRenderPipelineFailureWithPush(t *testing.T) {
	ctx := t.Context()

	mockCache := mockrepository.NewMockContentCache(t)
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "original"}, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockContent, nil)

	// Resources SHOULD be written when push-on-render-failure is set.
	mockDraft := &fakeDraftSlim{}
	mockCache.EXPECT().CreateDraftFromExisting(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockDraft, nil)
	mockCache.EXPECT().CloseDraft(mock.Anything, testRepoKey, mockDraft, 0).Return(nil)

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(3)
	mockClient.EXPECT().Status().Return(mockStatusWriter).Times(3)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
		Renderer:     &mockRenderer{resources: map[string]string{"Kptfile": "partial"}, pipelineErr: errors.New("function failed")},
		apiReader:    noStaleReader(t),
	}

	pr := renderTestPR("", "", "", "init", metav1.ConditionUnknown)
	pr.Annotations[porchv1alpha2.PushOnFnRenderFailureKey] = porchv1alpha2.PushOnFnRenderFailureValue
	result, err := r.reconcileRender(ctx, pr, testRepoKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "render pipeline failed")
	assert.Nil(t, result)
	assert.Equal(t, "partial", mockDraft.resources["Kptfile"], "partial resources should be written")
}

func TestReconcileRenderPipelineFailureWithPushWriteFails(t *testing.T) {
	ctx := t.Context()

	mockCache := mockrepository.NewMockContentCache(t)
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "original"}, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockContent, nil)

	// Write fails — should still set Rendered=False, not crash.
	mockCache.EXPECT().CreateDraftFromExisting(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(nil, errors.New("draft failed"))

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(3)
	mockClient.EXPECT().Status().Return(mockStatusWriter).Times(3)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
		Renderer:     &mockRenderer{resources: map[string]string{"Kptfile": "partial"}, pipelineErr: errors.New("function failed")},
		apiReader:    noStaleReader(t),
	}

	pr := renderTestPR("", "", "", "init", metav1.ConditionUnknown)
	pr.Annotations[porchv1alpha2.PushOnFnRenderFailureKey] = porchv1alpha2.PushOnFnRenderFailureValue
	result, err := r.reconcileRender(ctx, pr, testRepoKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "render pipeline failed")
	assert.Nil(t, result)
}


func TestRenderWithConcurrencyLimitRequeues(t *testing.T) {
	limiter := make(chan struct{}, 1)
	limiter <- struct{}{} // fill the limiter

	r := &PackageRevisionReconciler{
		Renderer:           &mockRenderer{resources: map[string]string{"Kptfile": "x"}},
		renderLimiter:      limiter,
		RenderRequeueDelay: 2 * time.Second,
	}

	result, requeue, err := r.renderWithConcurrencyLimit(t.Context(), nil)
	assert.NoError(t, err)
	assert.Nil(t, result)
	assert.NotNil(t, requeue)
	assert.Equal(t, 2*time.Second, requeue.RequeueAfter)
}

func TestSyncKptfileFieldsParseError(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	r := &PackageRevisionReconciler{Client: mockClient}
	pr := renderTestPR("", "", "", "", "")

	// Malformed Kptfile — should log error, not panic.
	r.syncKptfileFields(t.Context(), pr, map[string]string{"Kptfile": "not: valid: yaml: ["})
}
