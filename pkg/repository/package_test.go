package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func TestFetchRevision(t *testing.T) {
	ctx := context.Background()
	ns := "ns"
	ref := &porchapi.PackageRevisionRef{Name: "repo.pkg.v1"}
	resolver := &fakeReferenceResolver{repo: &configapi.Repository{}}

	t.Run("not found when zero revisions", func(t *testing.T) {
		pf := &PackageFetcher{
			RepoOpener:        &fakeOpener{repo: &listRepo{revs: nil}},
			ReferenceResolver: resolver,
		}
		_, err := pf.FetchRevision(ctx, ref, ns)
		require.Error(t, err)
		assert.True(t, apierrors.IsNotFound(err), "zero revisions must yield an IsNotFound error, got %v", err)
	})

	t.Run("error when multiple revisions", func(t *testing.T) {
		pf := &PackageFetcher{
			RepoOpener: &fakeOpener{repo: &listRepo{revs: []PackageRevision{
				&fakePackageRevision{name: "a"}, &fakePackageRevision{name: "b"},
			}}},
			ReferenceResolver: resolver,
		}
		_, err := pf.FetchRevision(ctx, ref, ns)
		require.Error(t, err)
		assert.False(t, apierrors.IsNotFound(err), "multiple revisions must NOT be IsNotFound")
		assert.ErrorContains(t, err, "cannot find package revision")
	})

	t.Run("success when exactly one revision", func(t *testing.T) {
		want := &fakePackageRevision{name: "repo.pkg.v1"}
		pf := &PackageFetcher{
			RepoOpener:        &fakeOpener{repo: &listRepo{revs: []PackageRevision{want}}},
			ReferenceResolver: resolver,
		}
		got, err := pf.FetchRevision(ctx, ref, ns)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("propagates resolve error", func(t *testing.T) {
		pf := &PackageFetcher{
			RepoOpener:        &fakeOpener{},
			ReferenceResolver: &fakeReferenceResolver{err: errors.New("resolve boom")},
		}
		_, err := pf.FetchRevision(ctx, ref, ns)
		assert.ErrorContains(t, err, "cannot find repository")
	})

	t.Run("propagates open error", func(t *testing.T) {
		pf := &PackageFetcher{
			RepoOpener:        &fakeOpener{err: errors.New("open boom")},
			ReferenceResolver: resolver,
		}
		_, err := pf.FetchRevision(ctx, ref, ns)
		assert.ErrorContains(t, err, "open boom")
	})
}

// fakeOpener returns a preconfigured repo (or error) from OpenRepository.
type fakeOpener struct {
	repo Repository
	err  error
}

func (o *fakeOpener) OpenRepository(context.Context, *configapi.Repository) (Repository, error) {
	return o.repo, o.err
}

// listRepo is a minimal Repository that returns a fixed revision list from
// ListPackageRevisions; all other methods are unused by FetchRevision.
type listRepo struct {
	revs    []PackageRevision
	listErr error
}

func (r *listRepo) ListPackageRevisions(context.Context, ListPackageRevisionFilter) ([]PackageRevision, error) {
	return r.revs, r.listErr
}

// Unused interface methods.
func (r *listRepo) KubeObjectNamespace() string { return "" }
func (r *listRepo) KubeObjectName() string      { return "" }
func (r *listRepo) Key() key.RepositoryKey      { return key.RepositoryKey{} }
func (r *listRepo) CreatePackageRevisionDraft(context.Context, *porchapi.PackageRevision) (PackageRevisionDraft, error) {
	return nil, nil
}
func (r *listRepo) ClosePackageRevisionDraft(context.Context, PackageRevisionDraft, int) (PackageRevision, error) {
	return nil, nil
}
func (r *listRepo) DeletePackageRevision(context.Context, PackageRevision) error { return nil }
func (r *listRepo) UpdatePackageRevision(context.Context, PackageRevision) (PackageRevisionDraft, error) {
	return nil, nil
}
func (r *listRepo) ListPackages(context.Context, ListPackageFilter) ([]Package, error) {
	return nil, nil
}
func (r *listRepo) Version(context.Context) (string, error)          { return "", nil }
func (r *listRepo) BranchCommitHash(context.Context) (string, error) { return "", nil }
func (r *listRepo) Close(context.Context) error                      { return nil }
func (r *listRepo) Refresh(context.Context) error                    { return nil }
