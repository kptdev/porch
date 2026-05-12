// Copyright 2024-2026 The kpt Authors
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

package dbcache

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sync"
	"time"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/engine"
	"github.com/kptdev/porch/pkg/externalrepo"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	pctx "github.com/kptdev/porch/pkg/util/context"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

var _ repository.Repository = &dbRepository{}

type dbRepository struct {
	repoKey              repository.RepositoryKey
	meta                 metav1.ObjectMeta
	spec                 *configapi.Repository
	externalRepo         repository.Repository
	repositorySync       *repositorySync
	updated              time.Time
	updatedBy            string
	deployment           bool
	repoPRChangeNotifier cachetypes.RepoPRChangeNotifier

	pushDraftsToGit bool
	gitPRCacheMutex sync.RWMutex
	gitPRCache      map[string]repository.PackageRevision
}

func (r *dbRepository) gitPRCacheKey(pkgKey repository.PackageKey, workspaceName string) string {
	return fmt.Sprintf("%s/%s/%s", pkgKey.Package, pkgKey.Path, workspaceName)
}

func (r *dbRepository) getCachedGitPR(pkgKey repository.PackageKey, workspaceName string) repository.PackageRevision {
	r.gitPRCacheMutex.RLock()
	defer r.gitPRCacheMutex.RUnlock()

	cacheKey := r.gitPRCacheKey(pkgKey, workspaceName)
	return r.gitPRCache[cacheKey]
}

func (r *dbRepository) setCachedGitPR(pkgKey repository.PackageKey, workspaceName string, gitPR repository.PackageRevision) {
	if gitPR == nil {
		return
	}

	r.gitPRCacheMutex.Lock()
	defer r.gitPRCacheMutex.Unlock()

	cacheKey := r.gitPRCacheKey(pkgKey, workspaceName)
	r.gitPRCache[cacheKey] = gitPR
	klog.V(5).Infof("cached gitPR for %s", cacheKey)
}

func (r *dbRepository) deleteCachedGitPR(pkgKey repository.PackageKey, workspaceName string) {
	r.gitPRCacheMutex.Lock()
	defer r.gitPRCacheMutex.Unlock()

	cacheKey := r.gitPRCacheKey(pkgKey, workspaceName)
	delete(r.gitPRCache, cacheKey)
	klog.V(5).Infof("deleted cached gitPR for %s", cacheKey)
}

func (r *dbRepository) KubeObjectName() string {
	return r.Key().Name
}

func (r *dbRepository) KubeObjectNamespace() string {
	return r.repoKey.Namespace
}

func (r *dbRepository) UID() types.UID {
	return util.GenerateUid("repositories.", r.KubeObjectNamespace(), r.KubeObjectName())
}

func (r *dbRepository) Key() repository.RepositoryKey {
	return r.repoKey
}

func (r *dbRepository) OpenRepository(ctx context.Context, externalRepoOptions externalrepotypes.ExternalRepoOptions) error {
	ctx, span := tracer.Start(ctx, "dbRepository::OpenRepository", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("dbRepository:OpenRepository: opening repository %+v", r.Key())

	if r.pushDraftsToGit {
		r.gitPRCache = make(map[string]repository.PackageRevision)
	}

	externalRepo, err := externalrepo.CreateRepositoryImpl(ctx, r.spec, externalRepoOptions)
	if err != nil {
		klog.Warningf("dbRepository:OpenRepository: repo %+v connectivity check failed with error %q", r.Key(), err)
		return err
	}

	r.externalRepo = externalRepo

	if _, err = repoReadFromDB(ctx, r.Key()); err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		klog.Warningf("dbRepository:OpenRepository: %+v DB read failed with error %q", r.Key(), err)
		return err
	}

	if err = repoWriteToDB(ctx, r); err != nil {
		klog.Warningf("dbRepository:OpenRepository: %+v DB write failed with error %q", r.Key(), err)
		return err
	}

	klog.V(5).Infof("dbRepository:OpenRepository: opened repository %+v", r.Key())

	return nil
}

func (r *dbRepository) Close(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "dbRepository::Close", trace.WithAttributes())
	defer span.End()

	if r.repositorySync != nil {
		r.repositorySync.syncWg.Wait()
	}

	klog.V(5).Infof("dbRepository:close: closing repository %+v", r.Key())

	dbPkgs, err := pkgReadPkgsFromDB(ctx, r.Key())
	if err != nil {
		return err
	}

	for _, pkg := range dbPkgs {
		// Delete cached packages but not packages on external storage
		if err := pkg.Delete(ctx, false); err != nil {
			return err
		}
	}

	err = repoDeleteFromDB(ctx, r.Key())
	if err != nil {
		return err
	}

	err = r.externalRepo.Close(ctx)

	if err == nil {
		klog.V(5).Infof("dbRepository:close: closed repository %+v", r.Key())
	} else {
		klog.Warningf("dbRepository:close: close of repository %+v failed: %q", r.Key(), err)
	}

	return err
}

func (r *dbRepository) ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "dbRepository::ListPackageRevisions", trace.WithAttributes())
	defer span.End()

	klog.V(3).InfoS("[DB Cache] Retrieving PackageRevisions from database for repository",
		pctx.LogMetadataFromWithExtras(ctx, "repository", r.Key())...)
	defer func() {
		klog.V(3).InfoS("[DB Cache] Completed retrieving PackageRevisions from database for repository",
			pctx.LogMetadataFromWithExtras(ctx, "repository", r.Key())...)
	}()

	klog.V(5).Infof("ListPackageRevisions: listing package revisions in repository %+v with filter %+v", r.Key(), filter)

	filter.Key.PkgKey.RepoKey = r.Key()
	foundPkgRevs, err := pkgRevListPRsFromDB(ctx, filter)
	if err != nil {
		klog.Warningf("ListPackageRevisions: listing package revisions in repository %+v with filter %+v failed: %q", r.Key(), filter, err)
		return nil, err
	}

	genericPkgRevs := make([]repository.PackageRevision, len(foundPkgRevs))
	for i, pkgRev := range foundPkgRevs {
		if pkgRev.repo == nil {
			klog.V(4).Infof("ListPackageRevisions: skipping package revision %+v with nil repository", pkgRev.Key())
			continue
		}
		genericPkgRevs[i] = pkgRev
	}
	genericPkgRevs = slices.DeleteFunc(genericPkgRevs, func(rev repository.PackageRevision) bool {
		return rev == nil
	})

	klog.V(5).Infof("ListPackageRevisions: listed package revisions in repository %+v with filter %+v", r.Key(), filter)

	return genericPkgRevs, nil
}

func (r *dbRepository) CreatePackageRevisionDraft(ctx context.Context, newPR *porchapi.PackageRevision) (repository.PackageRevisionDraft, error) {
	ctx, span := tracer.Start(ctx, "dbRepository::CreatePackageRevisionDraft", trace.WithAttributes())
	defer span.End()

	klog.InfoS("[DB Cache] Creating database entry for draft object for PackageRevision",
		pctx.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[DB Cache] Database entry for draft object created for PackageRevision",
			pctx.LogMetadataFrom(ctx)...)
	}()

	klog.V(5).Infof("dbRepository:CreatePackageRevisionDraft: creating draft for %+v on repo %+v", newPR, r.Key())

	if newPR.CreationTimestamp.Time.IsZero() {
		newPR.CreationTimestamp.Time = time.Now()
	}

	dbPkgRev := &dbPackageRevision{
		repo: r,
		pkgRevKey: repository.PackageRevisionKey{
			PkgKey:        repository.FromFullPathname(r.Key(), newPR.Spec.PackageName),
			Revision:      0,
			WorkspaceName: newPR.Spec.WorkspaceName,
		},
		meta:       newPR.ObjectMeta,
		spec:       &newPR.Spec,
		lifecycle:  porchapi.PackageRevisionLifecycleDraft,
		updated:    time.Now(),
		updatedBy:  getCurrentUser(),
		deployment: r.deployment,
	}

	dbPkgRev.meta.CreationTimestamp = metav1.Time{Time: time.Now()}

	dbPkgRev.extPRID = kptfilev1.Locator{
		Type: kptfilev1.GitOrigin,
		Git: &kptfilev1.GitLock{
			Repo:      dbPkgRev.repo.spec.Spec.Git.Repo,
			Directory: dbPkgRev.Key().PKey().ToPkgPathname(),
			Ref:       "drafts/" + dbPkgRev.Key().PKey().ToPkgPathname() + "/" + dbPkgRev.Key().WorkspaceName,
			Commit:    "not-pushed",
		},
	}

	if r.pushDraftsToGit {
		prName := repository.ComposePkgRevObjName(dbPkgRev.Key())
		klog.InfoS("[DB Cache] Creating draft in Git for PackageRevision",
			pctx.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
		defer func() {
			klog.V(3).InfoS("[DB Cache] Draft created in Git for PackageRevision",
				pctx.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
		}()
		gitPRDraft, err := r.externalRepo.CreatePackageRevisionDraft(ctx, newPR)
		if err != nil {
			return nil, pkgerrors.Wrapf(err, "failed to create git draft for %+v, not saving to DB", dbPkgRev.Key())
		}
		dbPkgRev.gitPRDraft = gitPRDraft
	}

	if prDraft, err := r.savePackageRevisionDraft(ctx, dbPkgRev, 0); err == nil {
		return repository.PackageRevisionDraft(prDraft), nil
	} else {
		return nil, err
	}

}

func (r *dbRepository) DeletePackageRevision(ctx context.Context, pr2Delete repository.PackageRevision) error {
	ctx, span := tracer.Start(ctx, "dbRepository::DeletePackageRevision", trace.WithAttributes())
	defer span.End()

	// Only published packages are deleted from external repo
	// TODO should be replaced with flag when option for db-cache push to git regardless PR comes in
	if porchapi.LifecycleIsPublished(pr2Delete.Lifecycle(context.Background())) {
		klog.InfoS("[DB Cache] Deleting PackageRevision from database and external repo for PackageRevision",
			pctx.LogMetadataFrom(ctx)...)
		defer func() {
			klog.V(3).InfoS("[DB Cache] PackageRevision deleted from database and external repo for PackageRevision",
				pctx.LogMetadataFrom(ctx)...)
		}()
	} else {
		klog.InfoS("[DB Cache] Deleting PackageRevision from database for PackageRevision",
			pctx.LogMetadataFrom(ctx)...)
		defer func() {
			klog.V(3).InfoS("[DB Cache] PackageRevision deleted from database for PackageRevision",
				pctx.LogMetadataFrom(ctx)...)
		}()
	}

	if len(pr2Delete.GetMeta().Finalizers) > 0 {
		klog.V(5).Infof("dbRepository:DeletePackageRevision: deletion ordered on package revision %+v on repo %+v, but finalizers %+v exist", pr2Delete.Key(), r.Key(), pr2Delete.GetMeta().Finalizers)

		deletionTimestamp := metav1.Time{
			Time: time.Now(),
		}

		pr2DeleteMeta := pr2Delete.GetMeta()
		pr2DeleteMeta.DeletionTimestamp = &deletionTimestamp

		if err := pr2Delete.SetMeta(ctx, pr2DeleteMeta); err != nil {
			return pkgerrors.Wrapf(err, "dbRepository:DeletePackageRevision: could not update metadata on package revision %+v on repo %+v", pr2Delete.Key(), r.Key())
		}

		return nil
	}

	klog.V(5).Infof("dbRepository:DeletePackageRevision: deleting package revision %+v on repo %+v", pr2Delete.Key(), r.Key())

	pk := repository.PackageKey{
		RepoKey: r.Key(),
		Path:    pr2Delete.Key().PKey().Path,
		Package: pr2Delete.Key().PKey().Package,
	}

	foundPkg, err := pkgReadFromDB(ctx, pk)
	if err != nil {
		return err
	}

	// Delete both the cached and external package
	if err := foundPkg.DeletePackageRevision(ctx, pr2Delete, true); err != nil && err != sql.ErrNoRows {
		return err
	}

	if r.pushDraftsToGit {
		r.deleteCachedGitPR(pr2Delete.Key().PkgKey, pr2Delete.Key().WorkspaceName)
	}

	foundPRs, err := pkgRevReadPRsFromDB(ctx, foundPkg.Key())
	if err != nil {
		return err
	}

	if len(foundPRs) > 0 {
		return nil
	}

	if err = pkgDeleteFromDB(ctx, pk); err != nil {
		klog.V(5).Infof("dbRepository:DeletePackageRevision: deletion of package revision %+v on repo %+v reported error %q", pr2Delete.Key(), r.Key(), err)
	}

	klog.V(5).Infof("dbRepository:DeletePackageRevision: deleted package revision %+v on repo %+v", pr2Delete.Key(), r.Key())
	return nil
}

func (r *dbRepository) UpdatePackageRevision(ctx context.Context, updatePR repository.PackageRevision) (repository.PackageRevisionDraft, error) {
	ctx, span := tracer.Start(ctx, "dbRepository::UpdatePackageRevision", trace.WithAttributes())
	defer span.End()

	klog.InfoS("[DB Cache] Loading draft from database for update for PackageRevision",
		pctx.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[DB Cache] Draft loaded from database and ready for modifications for PackageRevision",
			pctx.LogMetadataFrom(ctx)...)
	}()

	klog.V(5).Infof("dbRepository:UpdatePackageRevision: updating package revision %+v on repo %+v", updatePR.Key(), r.Key())

	updatePkgRev, ok := updatePR.(*dbPackageRevision)
	if !ok {
		return nil, fmt.Errorf("cannot update DB package revision %T", updatePR)
	}

	if updatePkgRev.repo == nil {
		updatePkgRev.repo = r
	}

	if err := updatePkgRev.UpdatePackageRevision(ctx); err != nil {
		return nil, err
	}

	updatePkgRev.updated = time.Now()
	updatePkgRev.updatedBy = getCurrentUser()

	if r.pushDraftsToGit && updatePkgRev.gitPRDraft == nil {
		klog.InfoS("[DB Cache] Getting or creating Git draft for PackageRevision",
			pctx.LogMetadataFromWithExtras(ctx, "packageRevision", repository.ComposePkgRevObjName(updatePkgRev.Key()))...)
		defer func() {
			klog.V(3).InfoS("[DB Cache] Git draft get or create completed for PackageRevision",
				pctx.LogMetadataFromWithExtras(ctx, "packageRevision", repository.ComposePkgRevObjName(updatePkgRev.Key()))...)
		}()
		gitPRToUse := r.getCachedGitPR(updatePkgRev.Key().PkgKey, updatePkgRev.Key().WorkspaceName)

		gitPRDraft, gitPR, err := engine.GetOrCreateGitDraft(ctx, r.externalRepo, updatePkgRev, gitPRToUse)
		if err != nil {
			return nil, pkgerrors.Wrapf(err, "failed to get or create git draft for %+v", updatePkgRev.Key())
		}
		updatePkgRev.gitPRDraft = gitPRDraft
		if gitPR != nil {
			updatePkgRev.gitPR = gitPR
		}
	}

	return updatePkgRev, nil
}

func (r *dbRepository) ListPackages(ctx context.Context, filter repository.ListPackageFilter) ([]repository.Package, error) {
	ctx, span := tracer.Start(ctx, "dbRepository::ListPackages", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("ListPackages: listing packages in repository %+v with filter %+v", r.Key(), filter)

	filter.Key.RepoKey = r.Key()
	foundPkgs, err := pkgListPkgsFromDB(ctx, filter)
	if err != nil {
		klog.Warningf("ListPackages: listing packages in repository %+v with filter %+v failed: %q", r.Key(), filter, err)
		return nil, err
	}

	genericPkgs := make([]repository.Package, len(foundPkgs))
	for i, pkg := range foundPkgs {
		genericPkgs[i] = repository.Package(pkg)
	}

	klog.V(5).Infof("ListPackages: listed packages in repository %+v with filter %+v", r.Key(), filter)

	return genericPkgs, nil
}

func (r *dbRepository) Version(ctx context.Context) (string, error) {
	_, span := tracer.Start(ctx, "dbRepository::Version", trace.WithAttributes())
	defer span.End()

	return r.externalRepo.Version(ctx)
}

func (r *dbRepository) BranchCommitHash(ctx context.Context) (string, error) {
	return r.externalRepo.BranchCommitHash(ctx)
}

func (r *dbRepository) ClosePackageRevisionDraft(ctx context.Context, prd repository.PackageRevisionDraft, version int) (repository.PackageRevision, error) {
	_, span := tracer.Start(ctx, "dbRepository::ClosePackageRevisionDraft", trace.WithAttributes())
	defer span.End()

	klog.InfoS("[DB Cache] Saving PackageRevision to database for PackageRevision",
		pctx.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[DB Cache] PackageRevision saved to database for PackageRevision",
			pctx.LogMetadataFrom(ctx)...)
	}()

	dbPrd := prd.(*dbPackageRevision)

	if r.pushDraftsToGit && dbPrd.gitPRDraft != nil {
		klog.InfoS("[DB Cache] Closing Git draft and pushing to Git for PackageRevision",
			pctx.LogMetadataFromWithExtras(ctx, "packageRevision", repository.ComposePkgRevObjName(dbPrd.Key()))...)
		defer func() {
			klog.V(3).InfoS("[DB Cache] Git draft closed and pushed for PackageRevision",
				pctx.LogMetadataFromWithExtras(ctx, "packageRevision", repository.ComposePkgRevObjName(dbPrd.Key()))...)
		}()
		gitPR, err := r.externalRepo.ClosePackageRevisionDraft(ctx, dbPrd.gitPRDraft, version)
		if err != nil {
			return nil, pkgerrors.Wrapf(err, "failed to close git draft for %+v, not saving to DB", dbPrd.Key())
		}
		dbPrd.gitPR = gitPR
		dbPrd.gitPRDraft = nil
		r.setCachedGitPR(dbPrd.Key().PkgKey, dbPrd.Key().WorkspaceName, gitPR)
	}

	dbPrd.resourcesSizeBytes = 0
	for _, fileString := range dbPrd.resources {
		dbPrd.resourcesSizeBytes += int64(len(fileString))
	}

	pr, err := r.savePackageRevisionDraft(ctx, prd, version)
	if err != nil {
		return nil, err
	}

	if r.pushDraftsToGit && pr.gitPRDraft != nil && r.externalRepo != nil {
		gitPR, err := r.externalRepo.ClosePackageRevisionDraft(ctx, pr.gitPRDraft, 0)
		if err != nil {
			klog.Warningf("failed to close git draft for %+v: %v", pr.Key(), err)
		} else {
			pr.gitPR = gitPR
			pr.gitPRDraft = nil
		}
	}

	return repository.PackageRevision(pr), nil
}

func (r *dbRepository) savePackageRevisionDraft(ctx context.Context, prd repository.PackageRevisionDraft, _ int) (*dbPackageRevision, error) {
	_, span := tracer.Start(ctx, "dbRepository::savePackageRevision", trace.WithAttributes())
	defer span.End()

	d := prd.(*dbPackageRevision)

	return r.savePackageRevision(ctx, d, d.resourcesDirty)
}

func (r *dbRepository) savePackageRevision(ctx context.Context, d *dbPackageRevision, saveResources bool) (*dbPackageRevision, error) {
	_, span := tracer.Start(ctx, "dbRepository::savePackageRevision", trace.WithAttributes())
	defer span.End()

	dbPkg, err := pkgReadFromDB(ctx, d.Key().PKey())
	if err != nil {
		if err != sql.ErrNoRows {
			return nil, err
		}

		dbPkg = &dbPackage{
			repo:      r,
			pkgKey:    d.Key().PKey(),
			updated:   d.updated,
			updatedBy: d.updatedBy,
		}

		if _, err := dbPkg.savePackage(ctx); err != nil {
			return nil, err
		}
	}

	pkgRev, err := dbPkg.savePackageRevision(ctx, d, saveResources)
	if err != nil {
		return nil, err
	}

	r.updated = d.updated
	r.updatedBy = d.updatedBy
	if err = repoUpdateDB(ctx, r); err != nil {
		return nil, err
	}

	return pkgRev, nil
}

func (r *dbRepository) Refresh(ctx context.Context) error {
	_, span := tracer.Start(ctx, "dbRepository::Refresh", trace.WithAttributes())
	defer span.End()

	if err := r.externalRepo.Refresh(ctx); err != nil {
		return err
	}

	if err := r.repositorySync.SyncOnce(ctx); err != nil {
		klog.Warningf("sync returned error %q", err)
		return err
	}

	return nil
}
