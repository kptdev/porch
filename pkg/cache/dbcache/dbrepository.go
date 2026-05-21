// Copyright 2024-2026 The Nephio Authors
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
	"time"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/nephio-project/porch/api/porch/v1alpha1"
	configapi "github.com/nephio-project/porch/api/porchconfig/v1alpha1"
	cachetypes "github.com/nephio-project/porch/pkg/cache/types"
	"github.com/nephio-project/porch/pkg/externalrepo"
	externalrepotypes "github.com/nephio-project/porch/pkg/externalrepo/types"
	"github.com/nephio-project/porch/pkg/repository"
	"github.com/nephio-project/porch/pkg/util"
	context1 "github.com/nephio-project/porch/pkg/util/context"
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
		externalRepo.Close(ctx)
		return err
	}

	if err = repoWriteToDB(ctx, r); err != nil {
		klog.Warningf("dbRepository:OpenRepository: %+v DB write failed with error %q", r.Key(), err)
		externalRepo.Close(ctx)
		return err
	}

	klog.V(5).Infof("dbRepository:OpenRepository: opened repository %+v", r.Key())

	return nil
}

func (r *dbRepository) Close(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "dbRepository::Close", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("dbRepository:close: closing repository %+v", r.Key())

	r.repositorySync.Stop()

	defer func() {
		// repoDeleteFromDB is a CASCADE delete - automatically deletes all packages, revisions and resources
		if err := repoDeleteFromDB(ctx, r.Key()); err != nil {
			klog.Warningf("dbRepository:close: delete of repository %+v from DB failed: %q", r.Key(), err)
		}
	}()

	defer func() {
		if err := r.externalRepo.Close(ctx); err != nil {
			klog.Warningf("dbRepository:close: close of external repository %+v failed: %q", r.Key(), err)
		} else {
			klog.V(5).Infof("dbRepository:close: closed repository %+v", r.Key())
		}
	}()

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

	return nil
}

func (r *dbRepository) ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "dbRepository::ListPackageRevisions", trace.WithAttributes())
	defer span.End()

	klog.V(3).InfoS("[DB Cache] Retrieving PackageRevisions from database for repository",
		context1.LogMetadataFromWithExtras(ctx, "repository", r.Key())...)
	defer func() {
		klog.V(3).InfoS("[DB Cache] Completed retrieving PackageRevisions from database for repository",
			context1.LogMetadataFromWithExtras(ctx, "repository", r.Key())...)
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
		context1.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[DB Cache] Database entry for draft object created for PackageRevision",
			context1.LogMetadataFrom(ctx)...)
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
			context1.LogMetadataFrom(ctx)...)
		defer func() {
			klog.V(3).InfoS("[DB Cache] PackageRevision deleted from database and external repo for PackageRevision",
				context1.LogMetadataFrom(ctx)...)
		}()
	} else {
		klog.InfoS("[DB Cache] Deleting PackageRevision from database for PackageRevision",
			context1.LogMetadataFrom(ctx)...)
		defer func() {
			klog.V(3).InfoS("[DB Cache] PackageRevision deleted from database for PackageRevision",
				context1.LogMetadataFrom(ctx)...)
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
		context1.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[DB Cache] Draft loaded from database and ready for modifications for PackageRevision",
			context1.LogMetadataFrom(ctx)...)
	}()

	klog.V(5).Infof("dbRepository:UpdatePackageRevision: updating package revision %+v on repo %+v", updatePR.Key(), r.Key())

	updatePkgRev, ok := updatePR.(*dbPackageRevision)
	if !ok {
		return nil, fmt.Errorf("cannot update DB package revision %T", updatePR)
	}

	if err := updatePkgRev.UpdatePackageRevision(ctx); err != nil {
		return nil, err
	}

	updatePkgRev.updated = time.Now()
	updatePkgRev.updatedBy = getCurrentUser()

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

func (r *dbRepository) ClosePackageRevisionDraft(ctx context.Context, prd repository.PackageRevisionDraft, version int) (repository.PackageRevision, error) {
	_, span := tracer.Start(ctx, "dbRepository::ClosePackageRevisionDraft", trace.WithAttributes())
	defer span.End()

	klog.InfoS("[DB Cache] Saving PackageRevision to database for PackageRevision",
		context1.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[DB Cache] PackageRevision saved to database for PackageRevision",
			context1.LogMetadataFrom(ctx)...)
	}()

	dbPrd := prd.(*dbPackageRevision)

	dbPrd.resourcesSizeBytes = 0
	for _, fileString := range dbPrd.resources {
		dbPrd.resourcesSizeBytes += int64(len(fileString))
	}

	pr, err := r.savePackageRevisionDraft(ctx, prd, version)

	return repository.PackageRevision(pr), err
}

func (r *dbRepository) savePackageRevisionDraft(ctx context.Context, prd repository.PackageRevisionDraft, _ int) (*dbPackageRevision, error) {
	_, span := tracer.Start(ctx, "dbRepository::savePackageRevision", trace.WithAttributes())
	defer span.End()

	d := prd.(*dbPackageRevision)

	return r.savePackageRevision(ctx, d, true)
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

	if err := r.repositorySync.getLastSyncError(); err != nil {
		klog.Warningf("last sync returned error %q, refreshing . . .", err)
	}

	if err := r.externalRepo.Refresh(ctx); err != nil {
		return err
	}

	if err := r.repositorySync.SyncOnce(ctx); err != nil {
		klog.Warningf("sync returned error %q", err)
		return err
	}

	return nil
}
