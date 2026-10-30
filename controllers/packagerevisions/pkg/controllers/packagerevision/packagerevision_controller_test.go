package packagerevision

import (
	"context"
	"errors"
	"testing"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/porch/api/porch/key"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
)

// setupMockContentDefaults adds common Maybe() expectations for PackageContent methods
// that updateStatus may call.
func setupMockContentDefaults(m *mockrepository.MockPackageContent) {
	m.EXPECT().Key().Return(key.PackageRevisionKey{}).Maybe()
	m.EXPECT().GetCommitInfo().Return(time.Time{}, "").Maybe()
	m.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Maybe()
	m.EXPECT().GetUpstreamLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Maybe()
	m.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "test"}, nil).Maybe()
	m.EXPECT().GetKptfile(mock.Anything).Return(kptfilev1.KptFile{}, nil).Maybe()
}

// expectRepoGet stubs a Repository lookup returning a repo in the given namespace/name.
// When migrated is true the repo carries the v1alpha2-migration annotation.
// Used by source-execution tests to satisfy the reconcileSource migration guard.
func expectRepoGet(mockClient *mockclient.MockClient, namespace, name string, migrated bool) {
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: namespace, Name: name}, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			repo := configapi.Repository{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
			if migrated {
				repo.Annotations = map[string]string{configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled}
			}
			*obj.(*configapi.Repository) = repo
		}).Return(nil)
}

// mockMainContent returns a MockPackageContent representing the derived "main"
// revision (Revision == -1, workspace "main") for repo "my-repo"/pkg "my-pkg".
func mockMainContent(t *testing.T) *mockrepository.MockPackageContent {
	m := mockrepository.NewMockPackageContent(t)
	m.EXPECT().Key().Return(key.PackageRevisionKey{
		PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "default", Name: "my-repo"}, Package: "my-pkg"},
		Revision:      -1,
		WorkspaceName: "main",
	}).Maybe()
	m.EXPECT().GetCommitInfo().Return(time.Now(), "author@example.com").Maybe()
	m.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Maybe()
	m.EXPECT().GetUpstreamLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Maybe()
	m.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "x"}, nil).Maybe()
	return m
}

func newTestReconciler(mockClient *mockclient.MockClient, cache *mockrepository.MockContentCache) *PackageRevisionReconciler {
	return &PackageRevisionReconciler{
		Client:                      mockClient,
		ContentCache:                cache,
		TransientErrorRetryDeadline: defaultTransientErrorRetryDeadline,
	}
}

// readyObjectMeta returns a standard ObjectMeta with finalizer and ownerReference already set,
// representing a PR that has already been reconciled at least once.
func readyObjectMeta(name, namespace, repoName string) metav1.ObjectMeta {
	controller := true
	blockOwnerDeletion := true
	return metav1.ObjectMeta{
		Name:       name,
		Namespace:  namespace,
		Finalizers: []string{porchv1alpha2.PackageRevisionFinalizer},
		// A PR that has already been reconciled carries both self-healing labels, so
		// the top-of-Reconcile ensure* calls are no-ops in these tests.
		Labels: map[string]string{
			porchv1alpha2.RepositoryLabelKey:       repoName,
			porchv1alpha2.LatestPackageRevisionKey: "false",
		},
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion:         configapi.GroupVersion.Identifier(),
			Kind:               configapi.TypeRepository.Kind,
			Name:               repoName,
			UID:                "repo-uid",
			Controller:         &controller,
			BlockOwnerDeletion: &blockOwnerDeletion,
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
			require.NotNil(t, pr.OwnerReferences[0].Controller)
			assert.True(t, *pr.OwnerReferences[0].Controller)
			require.NotNil(t, pr.OwnerReferences[0].BlockOwnerDeletion)
			assert.True(t, *pr.OwnerReferences[0].BlockOwnerDeletion)
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
	// Label recompute runs before finalizer removal; no siblings, so no patches.
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).Return(nil)
	// The finalizer-removal patch fails.
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
		key.RepositoryKey{Namespace: "my-ns", Name: "my-repo"},
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

	controller := true
	blockOwnerDeletion := true
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pr",
			Namespace:  "default",
			Finalizers: []string{porchv1alpha2.PackageRevisionFinalizer},
			Labels: map[string]string{
				porchv1alpha2.RepositoryLabelKey:       "my-repo",
				porchv1alpha2.LatestPackageRevisionKey: "false",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         configapi.GroupVersion.Identifier(),
				Kind:               configapi.TypeRepository.Kind,
				Name:               "my-repo",
				UID:                "repo-uid-123",
				Controller:         &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{RepositoryName: "my-repo"},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	// No Patch, no repo Get — finalizer, ownerRef, repository label, and
	// latest-revision label already present.

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileOwnerRefIncompleteGetsUpdated(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	// Seed an ownerRef with matching Kind/Name/UID but nil boolean pointers.
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pr",
			Namespace:  "default",
			Finalizers: []string{porchv1alpha2.PackageRevisionFinalizer},
			Labels:     map[string]string{porchv1alpha2.RepositoryLabelKey: "my-repo"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: configapi.GroupVersion.Identifier(),
				Kind:       configapi.TypeRepository.Kind,
				Name:       "my-repo",
				UID:        "repo-uid-123",
				// Controller and BlockOwnerDeletion intentionally nil
			}},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{RepositoryName: "my-repo"},
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
			patched := obj.(*porchv1alpha2.PackageRevision)
			require.Len(t, patched.OwnerReferences, 1)
			ref := patched.OwnerReferences[0]
			assert.Equal(t, "my-repo", ref.Name)
			assert.Equal(t, types.UID("repo-uid-123"), ref.UID)
			assert.Equal(t, configapi.TypeRepository.Kind, ref.Kind)
			require.NotNil(t, ref.Controller, "Controller should be set after self-healing")
			assert.True(t, *ref.Controller)
			require.NotNil(t, ref.BlockOwnerDeletion, "BlockOwnerDeletion should be set after self-healing")
			assert.True(t, *ref.BlockOwnerDeletion)
		}).Return(nil)

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileOwnerRefWrongAPIVersionGetsUpdated(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	controller := true
	blockOwnerDeletion := true
	// Seed an ownerRef with correct Kind/Name/UID and booleans but wrong APIVersion.
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pr",
			Namespace:  "default",
			Finalizers: []string{porchv1alpha2.PackageRevisionFinalizer},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         "wrong.api/v1",
				Kind:               configapi.TypeRepository.Kind,
				Name:               "my-repo",
				UID:                "repo-uid-123",
				Controller:         &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{RepositoryName: "my-repo"},
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
			patched := obj.(*porchv1alpha2.PackageRevision)
			require.Len(t, patched.OwnerReferences, 1)
			ref := patched.OwnerReferences[0]
			assert.Equal(t, configapi.GroupVersion.Identifier(), ref.APIVersion, "APIVersion should be corrected")
			assert.Equal(t, "my-repo", ref.Name)
			assert.Equal(t, types.UID("repo-uid-123"), ref.UID)
			require.NotNil(t, ref.Controller)
			assert.True(t, *ref.Controller)
			require.NotNil(t, ref.BlockOwnerDeletion)
			assert.True(t, *ref.BlockOwnerDeletion)
		}).Return(nil)

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileOwnerRefEmptyUIDGetsUpdated(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	controller := true
	blockOwnerDeletion := true
	// Seed an ownerRef with correct Kind/Name/APIVersion and booleans but empty UID.
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pr",
			Namespace:  "default",
			Finalizers: []string{porchv1alpha2.PackageRevisionFinalizer},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         configapi.GroupVersion.Identifier(),
				Kind:               configapi.TypeRepository.Kind,
				Name:               "my-repo",
				UID:                "",
				Controller:         &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{RepositoryName: "my-repo"},
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
			patched := obj.(*porchv1alpha2.PackageRevision)
			require.Len(t, patched.OwnerReferences, 1)
			ref := patched.OwnerReferences[0]
			assert.Equal(t, configapi.GroupVersion.Identifier(), ref.APIVersion)
			assert.Equal(t, "my-repo", ref.Name)
			assert.Equal(t, types.UID("repo-uid-123"), ref.UID, "UID should be populated after self-healing")
			require.NotNil(t, ref.Controller)
			assert.True(t, *ref.Controller)
			require.NotNil(t, ref.BlockOwnerDeletion)
			assert.True(t, *ref.BlockOwnerDeletion)
		}).Return(nil)

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
		Spec:       porchv1alpha2.PackageRevisionSpec{RepositoryName: "missing-repo"},
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

	controller := true
	blockOwnerDeletion := true
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pr", Namespace: "default",
			Finalizers: []string{porchv1alpha2.PackageRevisionFinalizer},
			Labels: map[string]string{
				porchv1alpha2.RepositoryLabelKey:       "my-repo",
				porchv1alpha2.LatestPackageRevisionKey: "false",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         configapi.GroupVersion.Identifier(),
				Kind:               configapi.TypeRepository.Kind,
				Name:               "my-repo",
				UID:                "repo-uid",
				Controller:         &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
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

// TestReconcileBackfillsLatestRevisionLabel verifies the self-healing invariant:
// a draft that is missing the latest-revision label (e.g. because the one-shot
// label patch during source execution failed when the reconcile was cancelled
// under load) gets the default "false" backfilled at the top of Reconcile.
func TestReconcileBackfillsLatestRevisionLabel(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	controller := true
	blockOwnerDeletion := true
	// Already created (CreationSource set) and rendered, with an empty lifecycle so
	// Reconcile returns right after the ensure* label steps — isolating the backfill.
	// Only the repository label is present; latest-revision is missing.
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pr",
			Namespace:  "default",
			Finalizers: []string{porchv1alpha2.PackageRevisionFinalizer},
			Labels:     map[string]string{porchv1alpha2.RepositoryLabelKey: "my-repo"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         configapi.GroupVersion.Identifier(),
				Kind:               configapi.TypeRepository.Kind,
				Name:               "my-repo",
				UID:                "repo-uid",
				Controller:         &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{RepositoryName: "my-repo"},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	// Expect exactly one merge Patch that backfills latest-revision="false".
	var patched bool
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			patched = true
			assert.Equal(t, "false", obj.(*porchv1alpha2.PackageRevision).Labels[porchv1alpha2.LatestPackageRevisionKey])
		}).Return(nil).Once()

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
	assert.True(t, patched, "Reconcile must backfill the missing latest-revision label")
}

// TestReconcileBackfillLabelErrorRequeues verifies that if the latest-revision
// backfill patch fails, Reconcile returns the error so the work queue retries,
// rather than proceeding and leaving the label permanently missing.
func TestReconcileBackfillLabelErrorRequeues(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	controller := true
	blockOwnerDeletion := true
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pr",
			Namespace:  "default",
			Finalizers: []string{porchv1alpha2.PackageRevisionFinalizer},
			Labels:     map[string]string{porchv1alpha2.RepositoryLabelKey: "my-repo"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         configapi.GroupVersion.Identifier(),
				Kind:               configapi.TypeRepository.Kind,
				Name:               "my-repo",
				UID:                "repo-uid",
				Controller:         &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Spec: porchv1alpha2.PackageRevisionSpec{RepositoryName: "my-repo"},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Return(assert.AnError).Once()

	r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
	_, err := r.Reconcile(ctx, req)

	assert.Error(t, err)
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

			// On publish, the controller creates/updates the main-branch
			// PackageRevision directly (independent of repo sync). These calls
			// only occur for publish transitions; Maybe() keeps non-publish rows valid.
			mainContent := mockrepository.NewMockPackageContent(t)
			setupMockContentDefaults(mainContent)
			mockCache.EXPECT().GetMainPackageContent(mock.Anything, mock.Anything, "my-pkg", "ws-1").Return(mainContent, nil).Maybe()
			// Repository lookup for the ownerRef (best-effort; NotFound is fine).
			mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"}, mock.AnythingOfType("*v1alpha1.Repository")).
				Return(apierrors.NewNotFound(schema.GroupResource{}, "my-repo")).Maybe()
			// Main CR spec apply (and label List for updateLatestRevisionLabels).
			mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).Return(nil).Maybe()

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

// TestReconcilePublishCreatesMainPackageRevision verifies that when a workspace
// is published, the controller creates/updates the "main" branch PackageRevision
// CR directly — independent of repository sync — mirroring v1alpha1.
func TestReconcilePublishCreatesMainPackageRevision(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-pr", Namespace: "default"}}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	// Current state is Proposed; desired is Published → fresh-publish path.
	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().Lifecycle(mock.Anything).Return("Proposed")

	updatedContent := mockrepository.NewMockPackageContent(t)
	updatedContent.EXPECT().Lifecycle(mock.Anything).Return("Published").Maybe()
	setupMockContentDefaults(updatedContent)

	// The derived main content: Revision == -1, workspace "main".
	mainContent := mockrepository.NewMockPackageContent(t)
	mainKey := key.PackageRevisionKey{
		PkgKey:        key.PackageKey{RepoKey: key.RepositoryKey{Namespace: "default", Name: "my-repo"}, Package: "my-pkg"},
		Revision:      -1,
		WorkspaceName: "main",
	}
	mainContent.EXPECT().Key().Return(mainKey).Maybe()
	mainContent.EXPECT().GetCommitInfo().Return(time.Now(), "author@example.com").Maybe()
	mainContent.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Maybe()
	mainContent.EXPECT().GetUpstreamLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil).Maybe()
	mainContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{"Kptfile": "x"}, nil).Maybe()

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "my-pkg", "ws-1").Return(mockContent, nil)
	mockCache.EXPECT().UpdateLifecycle(mock.Anything, mock.Anything, "my-pkg", "ws-1", "Published").Return(updatedContent, nil)
	// The key assertion: on publish the controller derives + writes the main revision.
	mainCalled := false
	mockCache.EXPECT().GetMainPackageContent(mock.Anything, mock.Anything, "my-pkg", "ws-1").
		Run(func(_ context.Context, _ key.RepositoryKey, _, _ string) { mainCalled = true }).
		Return(mainContent, nil)

	// Repository lookup for ownerRef (best-effort).
	repo := &configapi.Repository{ObjectMeta: metav1.ObjectMeta{Name: "my-repo", Namespace: "default", UID: "repo-uid"}}
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"}, mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*configapi.Repository) = *repo
		}).Return(nil).Maybe()

	// The controller GETs the main CR first; it does not exist yet → NotFound,
	// so the controller takes the Create path (mirroring v1alpha1 create-if-absent).
	mainNN := types.NamespacedName{Namespace: "default", Name: "my-repo.my-pkg.main"}
	mockClient.EXPECT().Get(mock.Anything, mainNN, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: "porch.kpt.dev", Resource: "packagerevisions"}, mainNN.Name)).Maybe()

	// Capture the main CR Create and assert its identity.
	var mainCreated *porchv1alpha2.PackageRevision
	mockClient.EXPECT().Create(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ ...client.CreateOption) {
			o := obj.(*porchv1alpha2.PackageRevision)
			if o.Spec.WorkspaceName == "main" {
				mainCreated = o
			}
		}).Return(nil).Maybe()
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).Return(nil).Maybe()

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{Requeue: true}, result)
	assert.True(t, mainCalled, "controller must derive the main revision on publish")
	if assert.NotNil(t, mainCreated, "controller must create a main PackageRevision CR when absent") {
		assert.Equal(t, "my-repo.my-pkg.main", mainCreated.Name)
		assert.Equal(t, porchv1alpha2.PackageRevisionLifecyclePublished, mainCreated.Spec.Lifecycle)
		assert.Nil(t, mainCreated.Spec.Source, "main revision must be repo-discovered (Source nil)")
		assert.Equal(t, "false", mainCreated.Labels[porchv1alpha2.LatestPackageRevisionKey])
		assert.Equal(t, "my-repo", mainCreated.Labels[porchv1alpha2.RepositoryLabelKey])
	}
}

// TestCreateMainPackageRevisionDoesNotStompDeletionProposed verifies that when
// the main CR already exists and is NOT Published (e.g. a user ran
// propose-delete → DeletionProposed), createMainPackageRevision leaves it
// entirely alone: no Create, no spec Patch, no status Patch. This matches
// v1alpha1, where createMainPackageRevision only creates when absent and never
// rewrites an existing main's lifecycle.
func TestCreateMainPackageRevisionDoesNotStompDeletionProposed(t *testing.T) {
	ctx := context.Background()
	repoKey := key.RepositoryKey{Namespace: "default", Name: "my-repo"}

	publishedPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	mainContent := mockMainContent(t)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetMainPackageContent(mock.Anything, repoKey, "my-pkg", "ws-1").Return(mainContent, nil)

	mockClient := mockclient.NewMockClient(t)
	// Repository ownerRef lookup (best-effort, NotFound is fine).
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"}, mock.AnythingOfType("*v1alpha1.Repository")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "my-repo")).Maybe()

	// Existing main CR is DeletionProposed.
	mainNN := types.NamespacedName{Namespace: "default", Name: "my-repo.my-pkg.main"}
	existingMain := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: mainNN.Name, Namespace: "default"},
		Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecycleDeletionProposed},
	}
	mockClient.EXPECT().Get(mock.Anything, mainNN, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *existingMain
		}).Return(nil)

	// No Create, no Patch, no Status() expectations — the main CR must be untouched.
	// Any such call would be an unexpected mock call and fail the test.

	r := newTestReconciler(mockClient, mockCache)
	err := r.createMainPackageRevision(ctx, publishedPR, repoKey)
	assert.NoError(t, err)
}

// TestCreateMainPackageRevisionRefreshesWhenPublished verifies that when the main
// CR exists and is Published, its HEAD-tracking status is refreshed AND its
// spec.packageMetadata/readinessGates are refreshed to track the latest published
// revision (mirroring v1alpha1) — but spec.lifecycle is never rewritten and no
// Create happens.
func TestCreateMainPackageRevisionRefreshesWhenPublished(t *testing.T) {
	ctx := context.Background()
	repoKey := key.RepositoryKey{Namespace: "default", Name: "my-repo"}

	publishedPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
			PackageMetadata: &porchv1alpha2.PackageMetadata{
				Labels: map[string]string{"env": "prod"},
			},
			ReadinessGates: []porchv1alpha2.ReadinessGate{{ConditionType: "Ready"}},
		},
	}

	mainContent := mockMainContent(t)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetMainPackageContent(mock.Anything, repoKey, "my-pkg", "ws-1").Return(mainContent, nil)

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"}, mock.AnythingOfType("*v1alpha1.Repository")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "my-repo")).Maybe()

	mainNN := types.NamespacedName{Namespace: "default", Name: "my-repo.my-pkg.main"}
	existingMain := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: mainNN.Name, Namespace: "default"},
		Spec:       porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
	}
	mockClient.EXPECT().Get(mock.Anything, mainNN, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *existingMain
		}).Return(nil)

	// A spec Patch (metadata/readinessGates) is expected — but it must NOT set
	// spec.lifecycle (user-owned). No Create.
	var specApplied *porchv1alpha2.PackageRevision
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"),
		client.Apply, client.FieldOwner(fieldManagerPRControllerMainMeta), client.ForceOwnership).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) {
			specApplied = obj.(*porchv1alpha2.PackageRevision)
		}).Return(nil)

	// Status Patch for HEAD-tracking fields.
	var statusApplied *porchv1alpha2.PackageRevision
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			statusApplied = obj.(*porchv1alpha2.PackageRevision)
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	err := r.createMainPackageRevision(ctx, publishedPR, repoKey)
	assert.NoError(t, err)

	if assert.NotNil(t, specApplied, "spec metadata should be refreshed for a Published main") {
		assert.Equal(t, "my-repo.my-pkg.main", specApplied.Name)
		// Metadata/readinessGates tracked from the published revision.
		assert.NotNil(t, specApplied.Spec.PackageMetadata)
		assert.Equal(t, "prod", specApplied.Spec.PackageMetadata.Labels["env"])
		assert.Len(t, specApplied.Spec.ReadinessGates, 1)
		// spec.lifecycle must NOT be set in the metadata apply (user-owned).
		assert.Empty(t, specApplied.Spec.Lifecycle)
	}
	if assert.NotNil(t, statusApplied, "status should be refreshed for a Published main") {
		assert.Equal(t, "my-repo.my-pkg.main", statusApplied.Name)
		assert.Equal(t, -1, statusApplied.Status.Revision)
		assert.Empty(t, statusApplied.Spec.Lifecycle)
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

	// Non-transient failure: recorded as terminal Failed in status, no requeue.
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileLifecycleTransitionTransientFailure(t *testing.T) {
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
	// Transient git failure (connection reset) during publish.
	mockCache.EXPECT().UpdateLifecycle(mock.Anything, mock.Anything, "my-pkg", "ws-1", "Proposed").Return(nil, errors.New("connection reset by peer"))

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			pr := obj.(*porchv1alpha2.PackageRevision)
			assert.Len(t, pr.Status.Conditions, 1)
			assert.Equal(t, porchv1alpha2.ConditionReady, pr.Status.Conditions[0].Type)
			assert.Equal(t, metav1.ConditionFalse, pr.Status.Conditions[0].Status)
			// Transient failures stay in the transient-retry state (not terminal
			// Failed) during recovery.
			assert.Equal(t, transientRetryReasonInternal, pr.Status.Conditions[0].Reason)
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	// Transient failure: stays Pending and requeues with a delay (bounded by deadline).
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{RequeueAfter: reconciledRetryInterval}, result)
}

// TestReconcileNonLatestPublishedDoesNotRefreshMain verifies that re-reconciling a
// non-latest published revision (v1 while v2 is the latest) does NOT refresh the
// main CR — only the latest revision should, else main's HEAD-tracking status
// would be overwritten with stale metadata.
func TestReconcileNonLatestPublishedDoesNotRefreshMain(t *testing.T) {
	ctx := t.Context()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "my-repo.my-pkg.v1", Namespace: "default"}}

	// pr is v1 (revision 1), already Published.
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("my-repo.my-pkg.v1", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
		},
		Status: porchv1alpha2.PackageRevisionStatus{Revision: 1},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)

	// Content lifecycle already matches the desired Published lifecycle.
	content := mockrepository.NewMockPackageContent(t)
	content.EXPECT().Lifecycle(mock.Anything).Return("Published")
	setupMockContentDefaults(content)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "my-pkg", "v1").Return(content, nil)
	// GetMainPackageContent must NOT be called: v1 is not the latest. (No expectation
	// set → mockery fails the test if it is invoked.)

	// updateLatestRevisionLabels List: v2 (revision 2) is the latest, not v1.
	mockClient.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevisionList"), mock.Anything, mock.Anything).
		Run(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) {
			*list.(*porchv1alpha2.PackageRevisionList) = porchv1alpha2.PackageRevisionList{
				Items: []porchv1alpha2.PackageRevision{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "my-repo.my-pkg.v1", Namespace: "default",
							Labels: map[string]string{porchv1alpha2.LatestPackageRevisionKey: "false"}},
						Spec:   porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
						Status: porchv1alpha2.PackageRevisionStatus{Revision: 1},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "my-repo.my-pkg.v2", Namespace: "default",
							Labels: map[string]string{porchv1alpha2.LatestPackageRevisionKey: "true"}},
						Spec:   porchv1alpha2.PackageRevisionSpec{Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished},
						Status: porchv1alpha2.PackageRevisionStatus{Revision: 2},
					},
				},
			}
		}).Return(nil)
	// Labels already correct (v1=false, v2=true), so no label Patch expected.

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

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
			// Transient (e.g. repository cache not ready yet) → transient-retry
			// state, not Failed, since the reconcile requeues to retry rather than
			// giving up.
			assert.Equal(t, transientRetryReasonInternal, pr.Status.Conditions[0].Reason)
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	// Must requeue so the package self-heals once the content cache is ready,
	// instead of being left permanently stuck Ready=False.
	assert.NoError(t, err)
	assert.Equal(t, reconciledRetryInterval, result.RequeueAfter)
}

// TestReconcileGetPackageContentFailureEscalates verifies that when the content
// lookup has already been failing (Ready=False/Pending) for longer than
// transientErrorDeadline, the controller escalates to a terminal Failed
// condition and stops requeuing, rather than retrying forever.
func TestReconcileGetPackageContentFailureEscalates(t *testing.T) {
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
		Status: porchv1alpha2.PackageRevisionStatus{
			Conditions: []metav1.Condition{{
				Type:    porchv1alpha2.ConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  transientRetryReasonInternal,
				Message: "repository not found",
				// First entered the transient-retry state well before the deadline.
				LastTransitionTime: metav1.NewTime(time.Now().Add(-2 * defaultTransientErrorRetryDeadline)),
			}},
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
			// Past the deadline → escalated to terminal Failed.
			assert.Equal(t, porchv1alpha2.ReasonFailed, pr.Status.Conditions[0].Reason)
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	// Escalated: no error, and NOT requeued (gives up until next watch/resync).
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// TestReconcileGetPackageContentNonTransientFails verifies that a non-transient
// content lookup error (e.g. a malformed package key) is treated as terminal
// Failed immediately, without requeuing Pending for the whole deadline.
func TestReconcileGetPackageContentNonTransientFails(t *testing.T) {
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
	// Non-transient error (not matched by IsTransientError patterns).
	mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "my-pkg", "ws-1").Return(nil, errors.New("malformed package key"))

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			pr := obj.(*porchv1alpha2.PackageRevision)
			assert.Len(t, pr.Status.Conditions, 1)
			assert.Equal(t, metav1.ConditionFalse, pr.Status.Conditions[0].Status)
			// Non-transient → terminal Failed immediately, not Pending.
			assert.Equal(t, porchv1alpha2.ReasonFailed, pr.Status.Conditions[0].Reason)
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	// Terminal: no error, no requeue.
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

func (f *fakeDraftSlim) Key() key.PackageRevisionKey {
	return key.PackageRevisionKey{}
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

	// Track the CreationSource that updateStatus has persisted so the
	// updateStatusWithRetry readback reflects it. updateStatusWithRetry loops
	// until the readback shows the landed CreationSource, so the Get must return
	// the value captured by the status Patch — otherwise the retry spins until
	// the transient-error deadline (the stuck-draft retry we added).
	var persistedCreationSource string

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
			// Reflect any status already persisted via updateStatus so the
			// readback inside updateStatusWithRetry sees the landed value.
			obj.(*porchv1alpha2.PackageRevision).Status.CreationSource = persistedCreationSource
		}).Return(nil)
	expectRepoGet(mockClient, "default", "my-repo", true)

	mockDraft := &fakeDraftSlim{}

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().CreateNewDraft(mock.Anything, key.RepositoryKey{Namespace: "default", Name: "my-repo"}, "my-pkg", "ws-1", "Draft").Return(mockDraft, nil)
	mockCache.EXPECT().CloseDraft(mock.Anything, key.RepositoryKey{Namespace: "default", Name: "my-repo"}, mockDraft, 0).Return(nil)

	// After CloseDraft, reconcileSource reads back the content for lock info
	mockContentAfterInit := mockrepository.NewMockPackageContent(t)
	mockContentAfterInit.EXPECT().Lifecycle(mock.Anything).Return("Draft").Maybe()
	setupMockContentDefaults(mockContentAfterInit)
	mockCache.EXPECT().GetPackageContent(mock.Anything, key.RepositoryKey{Namespace: "default", Name: "my-repo"}, "my-pkg", "ws-1").Return(mockContentAfterInit, nil)

	// Expect SSA status patches: updateStatus (Ready) then updateRenderStatus (Rendered).
	// Capture the CreationSource from the patch so the readback Get above can mirror it.
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	var statusPatches []porchv1alpha2.PackageRevisionStatus
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			st := obj.(*porchv1alpha2.PackageRevision).Status
			statusPatches = append(statusPatches, st)
			if st.CreationSource != "" {
				persistedCreationSource = st.CreationSource
			}
		}).Return(nil).Maybe()
	mockClient.EXPECT().Status().Return(mockStatusWriter).Maybe()

	// The latest-revision label is already present (readyObjectMeta), so the
	// top-of-Reconcile backfill is a no-op and performs no label Patch.

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	// After the draft is closed the controller requeues with a delay so the render
	// step is reliably retried (a bare Requeue can be dropped from the workqueue
	// under load, leaving the package stuck "awaiting render").
	assert.Equal(t, ctrl.Result{RequeueAfter: reconciledRetryInterval}, result)

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
	expectRepoGet(mockClient, "default", "my-repo", true)

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

func TestSourceFailureDoesNotWriteCreationSource(t *testing.T) {
	// A failed source operation must not write CreationSource — doing so would
	// permanently prevent applySource from retrying on the next reconcile.
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
		// No CreationSource — triggers source execution
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	expectRepoGet(mockClient, "default", "my-repo", true)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().CreateNewDraft(mock.Anything, mock.Anything, "my-pkg", "ws-1", "Draft").
		Return(nil, errors.New("transient error"))

	var capturedStatus porchv1alpha2.PackageRevisionStatus
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			capturedStatus = obj.(*porchv1alpha2.PackageRevision).Status
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := newTestReconciler(mockClient, mockCache)
	_, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	// CreationSource must remain empty so applySource retries on the next reconcile.
	assert.Empty(t, capturedStatus.CreationSource)
}

func TestSourceSuccessDoesNotWriteSubpackageHash(t *testing.T) {
	// A successful source (init/clone/copy/upgrade) operation must not write
	// LastSubpackageOperationHash — doing so would cause shouldSkipSubpackageOperation
	// to skip a pending subpackage operation before it runs.
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
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "my-repo.upstream-pkg.v1"},
				},
			},
		},
		// No CreationSource — triggers source execution
	}

	mockDraft := &fakeDraftSlim{}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil).Once()
	expectRepoGet(mockClient, "default", "my-repo", true)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().CreateNewDraft(mock.Anything, mock.Anything, "my-pkg", "ws-1", "Draft").Return(mockDraft, nil)
	mockCache.EXPECT().CloseDraft(mock.Anything, mock.Anything, mockDraft, 0).Return(nil)

	mockContent := mockrepository.NewMockPackageContent(t)
	mockContent.EXPECT().Lifecycle(mock.Anything).Return("Draft").Maybe()
	setupMockContentDefaults(mockContent)
	mockCache.EXPECT().GetPackageContent(mock.Anything, mock.Anything, "my-pkg", "ws-1").Return(mockContent, nil)

	var capturedStatus porchv1alpha2.PackageRevisionStatus
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			s := obj.(*porchv1alpha2.PackageRevision).Status
			if s.CreationSource != "" {
				capturedStatus = s
			}
		}).Return(nil).Times(2)
	mockClient.EXPECT().Status().Return(mockStatusWriter).Times(2)

	// updateStatusWithRetry Get — return PR with CreationSource set to confirm patch landed.
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "test-pr", Namespace: "default"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			obj.(*porchv1alpha2.PackageRevision).Status.CreationSource = "init"
		}).Return(nil)

	// The latest-revision label is already present (readyObjectMeta), so the
	// top-of-Reconcile backfill is a no-op and performs no label Patch.

	r := newTestReconciler(mockClient, mockCache)
	_, err := r.Reconcile(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, "init", capturedStatus.CreationSource)
	// LastSubpackageOperationHash must not be written by a source operation.
	assert.Empty(t, capturedStatus.LastSubpackageOperationHash)
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
			Labels: map[string]string{
				porchv1alpha2.RepositoryLabelKey:       "my-repo",
				porchv1alpha2.LatestPackageRevisionKey: "false",
			},
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

var testRepoKey = key.RepositoryKey{Namespace: "default", Name: "my-repo"}

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

	// A "not found" read error is TRANSIENT (the content cache is likely still
	// warming after a restart), so reconcileRender must requeue to retry rather
	// than mark the package terminally render-failed.
	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(nil, errors.New("repository porch-demo:my-repo:: not found"))

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	// Status patches: (1) "rendering" marker at the top of reconcileRender, then
	// requeueOrFailRender writes (2) the Ready=Pending and (3) Rendered=Unknown conditions.
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
		Renderer:     &mockRenderer{},
	}

	pr := renderTestPR("", "", "", "init", metav1.ConditionUnknown)
	result, err := r.reconcileRender(ctx, pr, testRepoKey)
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, reconciledRetryInterval, result.RequeueAfter)
}

// TestReconcileRenderReadContentFailsNonTransient verifies that a NON-transient
// read error is still treated as a terminal render failure (no requeue).
func TestReconcileRenderReadContentFailsNonTransient(t *testing.T) {
	ctx := t.Context()

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(nil, errors.New("malformed package key"))

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

// TestReconcileRenderReadContentTransientExpired verifies that once a transient
// read failure has persisted past transientErrorDeadline, it escalates to a
// terminal render failure instead of requeuing forever.
func TestReconcileRenderReadContentTransientExpired(t *testing.T) {
	ctx := t.Context()

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(nil, errors.New("repository porch-demo:my-repo:: not found"))

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:                      mockClient,
		ContentCache:                mockCache,
		Renderer:                    &mockRenderer{},
		TransientErrorRetryDeadline: defaultTransientErrorRetryDeadline,
	}

	pr := renderTestPR("", "", "", "init", metav1.ConditionUnknown)
	// Seed a transient-retry streak that started well before the deadline.
	pr.Status.Conditions = append(pr.Status.Conditions, metav1.Condition{
		Type:               porchv1alpha2.ConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             transientRetryReasonInternal,
		LastTransitionTime: metav1.NewTime(time.Now().Add(-2 * defaultTransientErrorRetryDeadline)),
	})
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
	expectRepoGet(mockClient, "default", "my-repo", true)

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
	expectRepoGet(mockClient, "default", "my-repo", true)

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
	controller := true
	blockOwnerDeletion := true
	pr.OwnerReferences = []metav1.OwnerReference{{
		APIVersion:         configapi.GroupVersion.Identifier(),
		Kind:               configapi.TypeRepository.Kind,
		Name:               "my-repo",
		UID:                "repo-uid",
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
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

	// Malformed Kptfile in rendered resources — should log error, not panic.
	r.syncKptfileFields(t.Context(), pr, map[string]string{"Kptfile": "not: valid: yaml: ["}, testRepoKey)
}

func TestVerifyRepoMigrated(t *testing.T) {
	repoKey := key.RepositoryKey{Namespace: "default", Name: "my-repo"}
	getRepo := func(annotated bool) func(context.Context, types.NamespacedName, client.Object, ...client.GetOption) {
		return func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			repo := configapi.Repository{ObjectMeta: metav1.ObjectMeta{Name: "my-repo", Namespace: "default"}}
			if annotated {
				repo.Annotations = map[string]string{configapi.AnnotationKeyV1Alpha2Migration: configapi.AnnotationValueMigrationEnabled}
			}
			*obj.(*configapi.Repository) = repo
		}
	}

	tests := []struct {
		name        string
		run         func(context.Context, types.NamespacedName, client.Object, ...client.GetOption)
		getErr      error
		errContains string // "" means no error expected
	}{
		{name: "migrated repo passes", run: getRepo(true)},
		{name: "non-migrated repo rejected", run: getRepo(false), errContains: "not enabled for v1alpha2"},
		{name: "repo lookup error", getErr: apierrors.NewNotFound(schema.GroupResource{}, "my-repo"), errContains: "get repository"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			mockClient := mockclient.NewMockClient(t)
			call := mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"}, mock.AnythingOfType("*v1alpha1.Repository"))
			if tt.run != nil {
				call.Run(tt.run)
			}
			call.Return(tt.getErr)

			r := newTestReconciler(mockClient, mockrepository.NewMockContentCache(t))
			err := r.verifyRepoMigrated(ctx, repoKey)
			if tt.errContains == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

// TestReconcileSourceBlockedOnNonMigratedRepo verifies the defense-in-depth guard:
// if a v1alpha2 PR with a source reaches the controller on a repo lacking the
// migration annotation (e.g. webhook bypassed), source execution is skipped and
// the PR is marked failed — no draft is ever created.
func TestReconcileSourceBlockedOnNonMigratedRepo(t *testing.T) {
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
		// No CreationSource — would trigger source execution if not blocked.
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, req.NamespacedName, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *pr
		}).Return(nil)
	// Repo has NO migration annotation.
	expectRepoGet(mockClient, "default", "my-repo", false)

	// Failed status is written; assert the message reflects the guard.
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			patched := obj.(*porchv1alpha2.PackageRevision)
			require.NotEmpty(t, patched.Status.Conditions)
			for _, c := range patched.Status.Conditions {
				assert.Equal(t, metav1.ConditionFalse, c.Status)
				assert.Contains(t, c.Message, "not enabled for v1alpha2",
					"failure message should reflect the migration guard, not an unrelated error")
			}
		}).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	// mockCache with NO expectations: CreateNewDraft must never be called.
	mockCache := mockrepository.NewMockContentCache(t)

	r := newTestReconciler(mockClient, mockCache)
	result, err := r.Reconcile(ctx, req)

	assert.NoError(t, err) // error handled internally via status
	assert.Equal(t, ctrl.Result{}, result)
}

// --- Subpackage operation tests ---

func TestReconcileSubpackageOperationSkippedWhenNil(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{},
	}
	result, err := r.reconcileSubpackageOperation(t.Context(), pr, testRepoKey)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestReconcileSubpackageOperationSkippedWhenAlreadyExecuted(t *testing.T) {
	r := &PackageRevisionReconciler{}
	pr := &porchv1alpha2.PackageRevision{
		Spec: porchv1alpha2.PackageRevisionSpec{
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
	}
	// Pre-set the hash so the operation is considered already executed.
	pr.Status.LastSubpackageOperationHash = r.getSubpackageOperationHash(pr)

	result, err := r.reconcileSubpackageOperation(t.Context(), pr, testRepoKey)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestReconcileSubpackageOperationInvalidSubpackageDir(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockrepository.NewMockContentCache(t),
	}
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "/absolute-path",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		},
	}

	result, err := r.reconcileSubpackageOperation(t.Context(), pr, testRepoKey)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid")
}

func TestReconcileSubpackageOperationComposeNameError(t *testing.T) {
	// INVALID-UPPERCASE fails IsValidSubpackageDir (uppercase is not DNS1123 compliant),
	// so reconcileSubpackageOperation returns an error at the validation stage.
	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockrepository.NewMockContentCache(t),
	}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "INVALID-UPPERCASE",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "my-repo.upstream-pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	result, err := r.reconcileSubpackageOperation(t.Context(), pr, testRepoKey)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid")
}

func TestReconcileSubpackageOperationSuccessfulClone(t *testing.T) {
	ctx := t.Context()

	upstreamPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.upstream-pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "upstream-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	// Get upstream PR for clone
	mockClient.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "my-repo.upstream-pkg.v1"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *upstreamPR
		}).Return(nil)
	// Note: the latest-revision label is now backfilled at the top of Reconcile
	// (self-healing), not inside the source/subpackage path, so reconcileSubpackageOperation
	// (called directly here) performs no label Patch.

	upstreamContent := mockrepository.NewMockPackageContent(t)
	upstreamContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile":       "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: upstream-pkg\n",
		"resource.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: sub-cm\n",
	}, nil)
	upstreamContent.EXPECT().GetLock(mock.Anything).Return(
		kptfilev1.Upstream{Type: kptfilev1.GitOrigin, Git: &kptfilev1.Git{Repo: "https://example.com/repo.git", Ref: "v1", Directory: "/upstream-pkg"}},
		kptfilev1.Locator{Type: kptfilev1.GitOrigin, Git: &kptfilev1.GitLock{Repo: "https://example.com/repo.git", Ref: "v1", Directory: "/upstream-pkg", Commit: "abc123"}},
		nil,
	)

	parentContent := mockrepository.NewMockPackageContent(t)
	parentContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile":     "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: my-pkg\n",
		"parent.yaml": "parent-resource",
	}, nil)

	afterCloseContent := mockrepository.NewMockPackageContent(t)
	afterCloseContent.EXPECT().Lifecycle(mock.Anything).Return("Draft").Maybe()
	setupMockContentDefaults(afterCloseContent)

	mockDraft := &fakeDraftSlim{}

	mockCache := mockrepository.NewMockContentCache(t)
	// Get upstream package content
	mockCache.EXPECT().GetPackageContent(mock.Anything, key.RepositoryKey{Namespace: "default", Name: "my-repo"}, "upstream-pkg", "v1").Return(upstreamContent, nil)
	// Get parent resources
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(parentContent, nil).Once()
	// CreateDraftFromExisting
	mockCache.EXPECT().CreateDraftFromExisting(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockDraft, nil)
	// CloseDraft
	mockCache.EXPECT().CloseDraft(mock.Anything, testRepoKey, mockDraft, 0).Return(nil)
	// Read back after close
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(afterCloseContent, nil).Once()

	// finalizeDraftAndUpdateStatus calls updateStatus + updateRenderStatus (2 Status() calls)
	// updateStatusWithRetry also does a Get to verify the hash landed.
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2)
	mockClient.EXPECT().Status().Return(mockStatusWriter).Times(2)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
	}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "my-repo.upstream-pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			CreationSource: "init",
		},
	}

	// updateStatusWithRetry reads back the PR to verify LastSubpackageOperationHash landed.
	// Compute expected hash before calling reconcile.
	expectedHash := r.getSubpackageOperationHash(pr)
	mockClient.EXPECT().Get(mock.Anything, client.ObjectKey{Name: "test-pr", Namespace: "default"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			obj.(*porchv1alpha2.PackageRevision).Status.LastSubpackageOperationHash = expectedHash
		}).Return(nil)

	result, err := r.reconcileSubpackageOperation(ctx, pr, testRepoKey)
	assert.NoError(t, err)
	require.NotNil(t, result)
	// Requeue with a delay so the render step is reliably retried after the draft closes.
	assert.Equal(t, reconciledRetryInterval, result.RequeueAfter)

	// Verify subpackage resources inserted under "my-subpkg/"
	assert.Contains(t, mockDraft.resources, "my-subpkg/Kptfile")
	assert.Contains(t, mockDraft.resources, "my-subpkg/resource.yaml")
	// Verify parent resources preserved
	assert.Contains(t, mockDraft.resources, "Kptfile")
	assert.Contains(t, mockDraft.resources, "parent.yaml")
	// Verify Kptfile name was rewritten to the composed subpackage name
	assert.Contains(t, mockDraft.resources["my-subpkg/Kptfile"], "name: my-subpkg")
}

func TestReconcileSubpackageOperationCreateDraftFails(t *testing.T) {
	ctx := t.Context()

	upstreamPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.upstream-pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "upstream-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "my-repo.upstream-pkg.v1"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *upstreamPR
		}).Return(nil)

	upstreamContent := mockrepository.NewMockPackageContent(t)
	upstreamContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: upstream-pkg\n",
	}, nil)
	upstreamContent.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)

	parentContent := mockrepository.NewMockPackageContent(t)
	parentContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: my-pkg\n",
	}, nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, key.RepositoryKey{Namespace: "default", Name: "my-repo"}, "upstream-pkg", "v1").Return(upstreamContent, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(parentContent, nil)
	mockCache.EXPECT().CreateDraftFromExisting(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(nil, errors.New("draft creation failed"))

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
	}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "my-repo.upstream-pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	result, err := r.reconcileSubpackageOperation(ctx, pr, testRepoKey)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "create draft on existing package revision")
}

func TestReconcileSubpackageOperationReadParentResourcesFails(t *testing.T) {
	ctx := t.Context()

	upstreamPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.upstream-pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "upstream-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "my-repo.upstream-pkg.v1"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *upstreamPR
		}).Return(nil)

	upstreamContent := mockrepository.NewMockPackageContent(t)
	upstreamContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: upstream-pkg\n",
	}, nil)
	upstreamContent.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, key.RepositoryKey{Namespace: "default", Name: "my-repo"}, "upstream-pkg", "v1").Return(upstreamContent, nil)
	// Reading parent fails
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(nil, errors.New("cache unavailable"))

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
	}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "my-repo.upstream-pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	result, err := r.reconcileSubpackageOperation(ctx, pr, testRepoKey)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to read parent resources")
}

func TestReconcileSubpackageOperationKptfileParseError(t *testing.T) {
	ctx := t.Context()

	upstreamPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.upstream-pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "upstream-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "my-repo.upstream-pkg.v1"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *upstreamPR
		}).Return(nil)

	// Upstream content has an invalid Kptfile — UpdateKptfileUpstream succeeds (it only needs
	// the key to exist) but kptfileko.NewFromPackage fails to parse the malformed YAML.
	upstreamContent := mockrepository.NewMockPackageContent(t)
	upstreamContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile": "{{not: valid: yaml",
	}, nil)
	upstreamContent.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, key.RepositoryKey{Namespace: "default", Name: "my-repo"}, "upstream-pkg", "v1").Return(upstreamContent, nil)

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
	}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "my-repo.upstream-pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	result, err := r.reconcileSubpackageOperation(ctx, pr, testRepoKey)
	assert.Error(t, err)
	assert.Nil(t, result)
	// Error comes from either UpdateKptfileUpstream (clone path) or kptfileko.NewFromPackage.
	assert.Contains(t, err.Error(), "Kptfile")
}

func TestReconcileSubpackageOperationSuccessWritesHash(t *testing.T) {
	ctx := t.Context()

	upstreamPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.upstream-pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "upstream-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "my-repo.upstream-pkg.v1"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *upstreamPR
		}).Return(nil)
	// Note: the latest-revision label is backfilled at the top of Reconcile now, not
	// in the source/subpackage path, so this direct reconcileSubpackageOperation call
	// performs no label Patch on the main client.

	upstreamContent := mockrepository.NewMockPackageContent(t)
	upstreamContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: upstream-pkg\n",
	}, nil)
	upstreamContent.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)

	parentContent := mockrepository.NewMockPackageContent(t)
	parentContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: my-pkg\n",
	}, nil)

	afterCloseContent := mockrepository.NewMockPackageContent(t)
	afterCloseContent.EXPECT().Lifecycle(mock.Anything).Return("Draft").Maybe()
	setupMockContentDefaults(afterCloseContent)

	mockDraft := &fakeDraftSlim{}

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, key.RepositoryKey{Namespace: "default", Name: "my-repo"}, "upstream-pkg", "v1").Return(upstreamContent, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(parentContent, nil).Once()
	mockCache.EXPECT().CreateDraftFromExisting(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockDraft, nil)
	mockCache.EXPECT().CloseDraft(mock.Anything, testRepoKey, mockDraft, 0).Return(nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(afterCloseContent, nil).Once()

	// finalizeDraftAndUpdateStatus makes two Status patches: updateStatus then updateRenderStatus.
	// Capture the first one (updateStatus) which carries LastSubpackageOperationHash.
	var capturedStatus porchv1alpha2.PackageRevisionStatus
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) {
			if capturedStatus.LastSubpackageOperationHash == "" {
				capturedStatus = obj.(*porchv1alpha2.PackageRevision).Status
			}
		}).Return(nil).Times(2)
	mockClient.EXPECT().Status().Return(mockStatusWriter).Times(2)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
	}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "my-repo.upstream-pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	expectedHash := r.getSubpackageOperationHash(pr)

	// updateStatusWithRetry reads back the PR to verify LastSubpackageOperationHash landed.
	mockClient.EXPECT().Get(mock.Anything, client.ObjectKey{Name: "test-pr", Namespace: "default"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			obj.(*porchv1alpha2.PackageRevision).Status.LastSubpackageOperationHash = expectedHash
		}).Return(nil)

	_, err := r.reconcileSubpackageOperation(ctx, pr, testRepoKey)
	assert.NoError(t, err)
	assert.Equal(t, expectedHash, capturedStatus.LastSubpackageOperationHash)
	// CreationSource must not be overwritten by a subpackage operation.
	assert.Equal(t, "init", capturedStatus.CreationSource)
}

// TestReconcileSubpackageOperationStatusNotConfirmedRequeues verifies that when the
// completion marker can't be confirmed on readback (informer cache lagging), and
// the Pending streak hasn't exceeded the deadline, finalizeDraftAndUpdateStatus
// REQUEUES rather than terminally failing — the cached read often lags under load
// while the write actually landed.
func TestReconcileSubpackageOperationStatusNotConfirmedRequeues(t *testing.T) {
	ctx := t.Context()

	upstreamPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.upstream-pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "upstream-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "my-repo.upstream-pkg.v1"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *upstreamPR
		}).Return(nil)

	upstreamContent := mockrepository.NewMockPackageContent(t)
	upstreamContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: upstream-pkg\n",
	}, nil)
	upstreamContent.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)

	parentContent := mockrepository.NewMockPackageContent(t)
	parentContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: my-pkg\n",
	}, nil)

	afterCloseContent := mockrepository.NewMockPackageContent(t)
	afterCloseContent.EXPECT().Lifecycle(mock.Anything).Return("Draft").Maybe()
	setupMockContentDefaults(afterCloseContent)

	mockDraft := &fakeDraftSlim{}

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, key.RepositoryKey{Namespace: "default", Name: "my-repo"}, "upstream-pkg", "v1").Return(upstreamContent, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(parentContent, nil).Once()
	mockCache.EXPECT().CreateDraftFromExisting(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockDraft, nil)
	mockCache.EXPECT().CloseDraft(mock.Anything, testRepoKey, mockDraft, 0).Return(nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(afterCloseContent, nil).Once()

	// updateStatus patch succeeds, but the readback never reflects the hash, so
	// updateStatusWithRetry exhausts its budget and returns an error.
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockClient.EXPECT().Status().Return(mockStatusWriter).Maybe()

	// Readback returns the PR WITHOUT the LastSubpackageOperationHash set → never confirmed.
	mockClient.EXPECT().Get(mock.Anything, client.ObjectKey{Name: "test-pr", Namespace: "default"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			obj.(*porchv1alpha2.PackageRevision).Status.CreationSource = "init"
			// LastSubpackageOperationHash deliberately left empty.
		}).Return(nil)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
	}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "my-repo.upstream-pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	result, err := r.reconcileSubpackageOperation(ctx, pr, testRepoKey)

	// Within the deadline (no prior Pending streak), an unconfirmed status requeues
	// so the next reconcile re-confirms once the cache catches up — not a terminal
	// failure.
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, reconciledRetryInterval, result.RequeueAfter)
}

// TestReconcileSubpackageOperationStatusNotConfirmedMarksFailedPastDeadline verifies
// that once the completion-status confirmation has been failing (Ready=Pending)
// for longer than TransientErrorRetryDeadline, finalizeDraftAndUpdateStatus
// escalates to a terminal failure and stops requeuing.
func TestReconcileSubpackageOperationStatusNotConfirmedMarksFailedPastDeadline(t *testing.T) {
	ctx := t.Context()

	upstreamPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.upstream-pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "upstream-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "my-repo.upstream-pkg.v1"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *upstreamPR
		}).Return(nil)

	upstreamContent := mockrepository.NewMockPackageContent(t)
	upstreamContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: upstream-pkg\n",
	}, nil)
	upstreamContent.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)

	parentContent := mockrepository.NewMockPackageContent(t)
	parentContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: my-pkg\n",
	}, nil)

	afterCloseContent := mockrepository.NewMockPackageContent(t)
	afterCloseContent.EXPECT().Lifecycle(mock.Anything).Return("Draft").Maybe()
	setupMockContentDefaults(afterCloseContent)

	mockDraft := &fakeDraftSlim{}

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, key.RepositoryKey{Namespace: "default", Name: "my-repo"}, "upstream-pkg", "v1").Return(upstreamContent, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(parentContent, nil).Once()
	mockCache.EXPECT().CreateDraftFromExisting(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(mockDraft, nil)
	mockCache.EXPECT().CloseDraft(mock.Anything, testRepoKey, mockDraft, 0).Return(nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(afterCloseContent, nil).Once()

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockClient.EXPECT().Status().Return(mockStatusWriter).Maybe()

	// Readback never shows the hash → never confirmed.
	mockClient.EXPECT().Get(mock.Anything, client.ObjectKey{Name: "test-pr", Namespace: "default"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			obj.(*porchv1alpha2.PackageRevision).Status.CreationSource = "init"
		}).Return(nil)

	r := &PackageRevisionReconciler{
		Client:                      mockClient,
		ContentCache:                mockCache,
		TransientErrorRetryDeadline: defaultTransientErrorRetryDeadline,
	}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "my-repo.upstream-pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{
			CreationSource: "init",
			// Transient-retry streak started well before the deadline → escalate to Failed.
			Conditions: []metav1.Condition{{
				Type:               porchv1alpha2.ConditionReady,
				Status:             metav1.ConditionFalse,
				Reason:             transientRetryReasonInternal,
				LastTransitionTime: metav1.NewTime(time.Now().Add(-2 * defaultTransientErrorRetryDeadline)),
			}},
		},
	}

	result, err := r.reconcileSubpackageOperation(ctx, pr, testRepoKey)

	// Past the deadline: terminal failure, no requeue.
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestReconcileSubpackageOperationConflictWithExistingContent(t *testing.T) {
	ctx := t.Context()

	upstreamPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo.upstream-pkg.v1", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "upstream-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "v1",
			Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockClient.EXPECT().Get(mock.Anything, client.ObjectKey{Namespace: "default", Name: "my-repo.upstream-pkg.v1"}, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *upstreamPR
		}).Return(nil)

	upstreamContent := mockrepository.NewMockPackageContent(t)
	upstreamContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: upstream-pkg\n",
	}, nil)
	upstreamContent.EXPECT().GetLock(mock.Anything).Return(kptfilev1.Upstream{}, kptfilev1.Locator{}, nil)

	// Parent already has content at "my-subpkg/"
	parentContent := mockrepository.NewMockPackageContent(t)
	parentContent.EXPECT().GetResourceContents(mock.Anything).Return(map[string]string{
		"Kptfile":                 "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: my-pkg\n",
		"my-subpkg/existing.yaml": "existing content",
	}, nil)

	mockCache := mockrepository.NewMockContentCache(t)
	mockCache.EXPECT().GetPackageContent(mock.Anything, key.RepositoryKey{Namespace: "default", Name: "my-repo"}, "upstream-pkg", "v1").Return(upstreamContent, nil)
	mockCache.EXPECT().GetPackageContent(mock.Anything, testRepoKey, "my-pkg", "ws-1").Return(parentContent, nil)

	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"), mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
	}

	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: readyObjectMeta("test-pr", "default", "my-repo"),
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "ws-1",
			SubpackageOperation: &porchv1alpha2.SubpackageOperation{
				SubpackageDir: "my-subpkg",
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "my-repo.upstream-pkg.v1"},
				},
			},
		},
		Status: porchv1alpha2.PackageRevisionStatus{CreationSource: "init"},
	}

	result, err := r.reconcileSubpackageOperation(ctx, pr, testRepoKey)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "cannot clone subpackage into parent")
}

// TestCreateMainPackageRevision_CreateNew tests creating a new main PackageRevision.
func TestCreateMainPackageRevision_CreateNew(t *testing.T) {
	ctx := context.Background()
	repoKey := key.RepositoryKey{Namespace: "default", Name: "my-repo"}

	publishedPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "pub-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "main",
		},
	}

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-repo",
			Namespace: "default",
			UID:       "repo-uid-456",
		},
		Spec: configapi.RepositorySpec{
			Deployment: true,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockCache := mockrepository.NewMockContentCache(t)

	// Mock GetMainPackageContent
	mainContent := mockMainContent(t)
	mockCache.EXPECT().GetMainPackageContent(mock.Anything, repoKey, "my-pkg", "main").
		Return(mainContent, nil)

	// Mock Repository Get
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"},
		mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*configapi.Repository) = *repo
		}).Return(nil)

	// Mock Create (main PR doesn't exist yet)
	mockClient.EXPECT().Get(mock.Anything, mock.MatchedBy(func(nn types.NamespacedName) bool {
		return nn.Namespace == "default" && nn.Name == "my-repo.my-pkg.main"
	}), mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "not-found"))

	// Mock Create call
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		pr, ok := obj.(*porchv1alpha2.PackageRevision)
		if !ok {
			return false
		}
		return pr.Spec.Lifecycle == porchv1alpha2.PackageRevisionLifecyclePublished &&
			pr.Spec.WorkspaceName == "main" &&
			pr.Spec.PackageName == "my-pkg"
	})).Return(nil)

	// Mock Status patch
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"),
		client.Apply, client.FieldOwner(fieldManagerPRControllerMain), client.ForceOwnership).
		Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
	}

	err := r.createMainPackageRevision(ctx, publishedPR, repoKey)
	assert.NoError(t, err)
}

// TestCreateMainPackageRevision_AlreadyExists tests skipping when main PR already exists.
func TestCreateMainPackageRevision_AlreadyExists(t *testing.T) {
	ctx := context.Background()
	repoKey := key.RepositoryKey{Namespace: "default", Name: "my-repo"}

	publishedPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "pub-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "main",
		},
	}

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-repo",
			Namespace: "default",
			UID:       "repo-uid-789",
		},
	}

	existingMain := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-repo.my-pkg.main",
			Namespace: "default",
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockCache := mockrepository.NewMockContentCache(t)

	// Mock GetMainPackageContent
	mainContent := mockMainContent(t)
	mockCache.EXPECT().GetMainPackageContent(mock.Anything, repoKey, "my-pkg", "main").
		Return(mainContent, nil)

	// Mock Repository Get
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"},
		mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*configapi.Repository) = *repo
		}).Return(nil)

	// Mock Get (main PR exists and is Published)
	mockClient.EXPECT().Get(mock.Anything, mock.MatchedBy(func(nn types.NamespacedName) bool {
		return nn.Namespace == "default" && nn.Name == "my-repo.my-pkg.main"
	}), mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *existingMain
		}).Return(nil)

	// Mock spec-metadata patch (refresh tracks latest published revision's metadata).
	mockClient.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"),
		client.Apply, client.FieldOwner(fieldManagerPRControllerMainMeta), client.ForceOwnership).
		Return(nil)

	// Mock Status patch (refresh)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.AnythingOfType("*v1alpha2.PackageRevision"),
		client.Apply, client.FieldOwner(fieldManagerPRControllerMain), client.ForceOwnership).
		Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
	}

	err := r.createMainPackageRevision(ctx, publishedPR, repoKey)
	assert.NoError(t, err)
}

// TestCreateMainPackageRevision_ExistingButDeleting tests leaving untouched when main PR is being deleted.
func TestCreateMainPackageRevision_ExistingButDeleting(t *testing.T) {
	ctx := context.Background()
	repoKey := key.RepositoryKey{Namespace: "default", Name: "my-repo"}

	publishedPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "pub-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
			WorkspaceName:  "main",
		},
	}

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-repo",
			Namespace: "default",
			UID:       "repo-uid-999",
		},
	}

	now := metav1.Now()
	existingMain := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "my-repo.my-pkg.main",
			Namespace:         "default",
			DeletionTimestamp: &now,
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Lifecycle: porchv1alpha2.PackageRevisionLifecyclePublished,
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockCache := mockrepository.NewMockContentCache(t)

	// Mock GetMainPackageContent
	mainContent := mockMainContent(t)
	mockCache.EXPECT().GetMainPackageContent(mock.Anything, repoKey, "my-pkg", "main").
		Return(mainContent, nil)

	// Mock Repository Get
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "my-repo"},
		mock.AnythingOfType("*v1alpha1.Repository")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*configapi.Repository) = *repo
		}).Return(nil)

	// Mock Get (main PR exists but is being deleted)
	mockClient.EXPECT().Get(mock.Anything, mock.MatchedBy(func(nn types.NamespacedName) bool {
		return nn.Namespace == "default" && nn.Name == "my-repo.my-pkg.main"
	}), mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Run(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) {
			*obj.(*porchv1alpha2.PackageRevision) = *existingMain
		}).Return(nil)

	// No status patch expected when being deleted

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
	}

	err := r.createMainPackageRevision(ctx, publishedPR, repoKey)
	assert.NoError(t, err)
}

// TestCreateMainPackageRevision_GetMainContentError tests error from GetMainPackageContent.
func TestCreateMainPackageRevision_GetMainContentError(t *testing.T) {
	ctx := context.Background()
	repoKey := key.RepositoryKey{Namespace: "default", Name: "my-repo"}

	publishedPR := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "pub-pr", Namespace: "default"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    "my-pkg",
			RepositoryName: "my-repo",
		},
	}

	mockClient := mockclient.NewMockClient(t)
	mockCache := mockrepository.NewMockContentCache(t)

	// Mock GetMainPackageContent to return error
	mockCache.EXPECT().GetMainPackageContent(mock.Anything, repoKey, "my-pkg", mock.Anything).
		Return(nil, errors.New("content cache error"))

	r := &PackageRevisionReconciler{
		Client:       mockClient,
		ContentCache: mockCache,
	}

	err := r.createMainPackageRevision(ctx, publishedPR, repoKey)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "content cache error")
}

// TestPatchMainStatus tests updating main PR status fields.
func TestPatchMainStatus(t *testing.T) {
	ctx := context.Background()
	namespace := "default"
	name := "main-pr"
	commitTime := time.Now()
	commitAuthor := "author@example.com"

	upstreamLock := kptfilev1.Locator{
		Type: kptfilev1.GitOrigin,
		Git:  &kptfilev1.GitLock{Repo: "https://github.com/example/repo", Ref: "v1.0"},
	}
	selfLock := kptfilev1.Locator{
		Type: kptfilev1.GitOrigin,
		Git:  &kptfilev1.GitLock{Repo: ".", Ref: "abc123"},
	}

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything,
		mock.MatchedBy(func(obj client.Object) bool {
			pr, ok := obj.(*porchv1alpha2.PackageRevision)
			if !ok {
				return false
			}
			return pr.Name == name && pr.Namespace == namespace &&
				pr.Status.Revision == -1 &&
				pr.Status.PublishedBy == commitAuthor &&
				pr.Status.Deployment == true &&
				pr.Status.ResourcesSizeBytes == 1024
		}),
		client.Apply,
		client.FieldOwner(fieldManagerPRControllerMain),
		client.ForceOwnership,
	).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{Client: mockClient}

	err := r.patchMainStatus(ctx, namespace, name, -1, commitAuthor, commitTime,
		upstreamLock, selfLock, true, 1024)
	assert.NoError(t, err)
}

// TestPatchMainStatus_NoCommitTime tests status patch with zero commit time.
func TestPatchMainStatus_NoCommitTime(t *testing.T) {
	ctx := context.Background()
	namespace := "default"
	name := "main-pr"

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything,
		mock.MatchedBy(func(obj client.Object) bool {
			pr, ok := obj.(*porchv1alpha2.PackageRevision)
			if !ok {
				return false
			}
			// PublishedAt should be nil when commitTime is zero
			return pr.Status.PublishedAt == nil
		}),
		client.Apply,
		client.FieldOwner(fieldManagerPRControllerMain),
		client.ForceOwnership,
	).Return(nil)
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{Client: mockClient}

	// Pass zero time
	err := r.patchMainStatus(ctx, namespace, name, -1, "author@example.com", time.Time{},
		kptfilev1.Locator{}, kptfilev1.Locator{}, false, 0)
	assert.NoError(t, err)
}

// TestPatchMainStatus_PatchError tests error from status patch.
func TestPatchMainStatus_PatchError(t *testing.T) {
	ctx := context.Background()

	mockClient := mockclient.NewMockClient(t)
	mockStatusWriter := mockclient.NewMockSubResourceWriter(t)
	mockStatusWriter.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("patch failed"))
	mockClient.EXPECT().Status().Return(mockStatusWriter)

	r := &PackageRevisionReconciler{Client: mockClient}

	err := r.patchMainStatus(ctx, "default", "main-pr", -1, "author", time.Now(),
		kptfilev1.Locator{}, kptfilev1.Locator{}, false, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "patch failed")
}
