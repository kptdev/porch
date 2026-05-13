// Copyright 2022, 2024-2025 The kpt Authors
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

package crcache

import (
	"context"
	"strings"
	stdSync "sync"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/crcache/meta"
	context1 "github.com/kptdev/porch/pkg/util/context"

	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/klog/v2"
)

// We take advantage of the cache having a global view of all the packages
// in a repository and compute the latest package revision in the cache
// rather than add another level of caching in the repositories themselves.
// This also reuses the revision comparison code and ensures same behavior
// between Git and OCI.

var _ repository.Repository = &cachedRepository{}

type cachedRepository struct {
	key      repository.RepositoryKey
	repoSpec *configapi.Repository
	repo     repository.Repository

	// Last version the cache has seen for this repository.
	// This needs to be kept separately from the version in the externalRepository
	// because there can be changes that are pushed down through the cache,
	// but not reflected in the list of packageRevisions correctly.
	lastVersion string

	mutex                  stdSync.RWMutex
	refreshWg              stdSync.WaitGroup
	cachedPackageRevisions map[repository.PackageRevisionKey]*cachedPackageRevision
	cachedPackages         map[repository.PackageKey]*cachedPackage
	refreshRevisionsError  error

	metadataStore        meta.MetadataStore
	repoPRChangeNotifier cachetypes.RepoPRChangeNotifier
}

func newRepository(repoKey repository.RepositoryKey, repoSpec *configapi.Repository, repo repository.Repository,
	metadataStore meta.MetadataStore, options cachetypes.CacheOptions) *cachedRepository {
	r := &cachedRepository{
		key:                  repoKey,
		repoSpec:             repoSpec,
		repo:                 repo,
		metadataStore:        metadataStore,
		repoPRChangeNotifier: options.RepoPRChangeNotifier,
	}
	return r
}

func (r *cachedRepository) KubeObjectNamespace() string {
	return r.Key().Namespace
}

func (r *cachedRepository) KubeObjectName() string {
	return r.Key().Name
}

func (r *cachedRepository) Key() repository.RepositoryKey {
	return r.key
}

func (r *cachedRepository) Refresh(ctx context.Context) error {

	_, _, err := r.refreshAllCachedPackages(ctx)

	return err
}

func (r *cachedRepository) Version(ctx context.Context) (string, error) {
	ctx, span := tracer.Start(ctx, "cachedRepository::Version", trace.WithAttributes())
	defer span.End()

	return r.repo.Version(ctx)
}

func (r *cachedRepository) BranchCommitHash(ctx context.Context) (string, error) {
	return r.repo.BranchCommitHash(ctx)
}

func (r *cachedRepository) ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	klog.V(3).InfoS("[CR Cache] Retrieving cached package revisions and enriching with PackageRev CR metadata from etcd for repository",
		context1.LogMetadataFromWithExtras(ctx, "repository", r.Key())...)
	defer func() {
		klog.V(3).InfoS("[CR Cache] Completed retrieving and enriching package revisions with PackageRev CR metadata for repository",
			context1.LogMetadataFromWithExtras(ctx, "repository", r.Key())...)
	}()

	packages, err := r.getPackageRevisions(ctx, filter, false)
	if err != nil {
		return nil, err
	}

	for _, pr := range packages {
		pkgRevMeta, err := r.metadataStore.Get(ctx, types.NamespacedName{
			Name:      pr.KubeObjectName(),
			Namespace: pr.KubeObjectNamespace(),
		})
		if err != nil {
			// If a PackageRev CR doesn't exist, we treat the
			// Packagerevision as not existing.
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		if err := pr.SetMeta(ctx, pkgRevMeta); err != nil {
			return nil, err
		}
	}

	return packages, nil
}

func (r *cachedRepository) getRefreshError() error {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return r.refreshRevisionsError
}

func (r *cachedRepository) getPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter, forceRefresh bool) ([]repository.PackageRevision, error) {
	if forceRefresh {
		klog.Infof("crcache: getPackageRevisions(%s) fetching packages from external repository", r.Key())
	} else {
		klog.V(2).Infof("crcache: getPackageRevisions(%s) using cached packages", r.Key())
	}
	_, packageRevisions, err := r.getCachedPackages(ctx, forceRefresh)
	if err != nil {
		return nil, err
	}
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return toPackageRevisionSlice(ctx, packageRevisions, filter), nil
}

func (r *cachedRepository) getPackages(ctx context.Context, filter repository.ListPackageFilter, forceRefresh bool) ([]repository.Package, error) {

	packages, _, err := r.getCachedPackages(ctx, forceRefresh)
	if err != nil {
		return nil, err
	}
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return toPackageSlice(packages, filter), nil
}

// getCachedPackages returns cachedPackages; fetching it if not cached or if forceRefresh.
func (r *cachedRepository) getCachedPackages(ctx context.Context, forceRefresh bool) (map[repository.PackageKey]*cachedPackage, map[repository.PackageRevisionKey]*cachedPackageRevision, error) {
	r.mutex.RLock()
	packages := r.cachedPackages
	packageRevisions := r.cachedPackageRevisions
	r.mutex.RUnlock()
	var err error

	if forceRefresh {
		packages = nil
		packageRevisions = nil

		r.mutex.Lock()
		err := r.repo.Refresh(ctx)
		r.mutex.Unlock()

		if err != nil {
			return nil, nil, err
		}
	}

	if packages == nil {
		packages, packageRevisions, err = r.refreshAllCachedPackages(ctx)
	}

	return packages, packageRevisions, err
}

func (r *cachedRepository) CreatePackageRevisionDraft(ctx context.Context, obj *porchapi.PackageRevision) (repository.PackageRevisionDraft, error) {
	klog.InfoS("[CR Cache] Creating draft and preparing to save to Git for PackageRevision", context1.LogMetadataFrom(ctx))
	defer func() {
		klog.V(3).InfoS("[CR Cache] Draft created and saved to Git for PackageRevision", context1.LogMetadataFrom(ctx))
	}()

	return r.repo.CreatePackageRevisionDraft(ctx, obj)
}

func (r *cachedRepository) ClosePackageRevisionDraft(ctx context.Context, prd repository.PackageRevisionDraft, version int) (repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "cachedRepository::ClosePackageRevisionDraft", trace.WithAttributes())
	defer span.End()

	klog.InfoS("[CR Cache] Closing draft and pushing lifecycle change to Git for PackageRevision", context1.LogMetadataFrom(ctx))
	defer func() {
		klog.V(3).InfoS("[CR Cache] Draft closed and lifecycle change pushed to Git for PackageRevision", context1.LogMetadataFrom(ctx))
	}()

	v, err := r.Version(ctx)
	if err != nil {
		return nil, err
	}
	r.mutex.Lock()
	ver := r.lastVersion
	r.mutex.Unlock()
	if v != ver {
		_, _, err = r.refreshAllCachedPackages(ctx)
		if err != nil {
			return nil, err
		}
	}

	revisions, err := r.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				Path:    prd.Key().PkgKey.Path,
				Package: prd.Key().PkgKey.Package,
			},
		},
	})
	if err != nil {
		return nil, err
	}

	highestRevision := 0
	for _, rev := range revisions {
		if porchapi.LifecycleIsPublished(rev.Lifecycle(ctx)) && rev.Key().Revision > highestRevision {
			highestRevision = rev.Key().Revision
		}
	}

	closedPr, err := r.repo.ClosePackageRevisionDraft(ctx, prd, highestRevision+1)
	if err != nil {
		return nil, err
	}

	cachedPr, err := r.update(ctx, closedPr)
	if err != nil {
		return nil, err
	}
	pkgRevMeta := metav1.ObjectMeta{
		Name:            cachedPr.KubeObjectName(),
		Namespace:       cachedPr.KubeObjectNamespace(),
		Labels:          prd.GetMeta().Labels,
		Annotations:     prd.GetMeta().Annotations,
		Finalizers:      prd.GetMeta().Finalizers,
		OwnerReferences: prd.GetMeta().OwnerReferences,
	}

	pkgRevMeta, err = r.metadataStore.Create(ctx, pkgRevMeta, r.Key().Name, cachedPr.UID())
	if err != nil {
		return nil, err
	}

	if err := cachedPr.SetMeta(ctx, pkgRevMeta); err != nil {
		return nil, err
	}

	sent := r.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Added, cachedPr)
	klog.Infof("crcache: sent %d for new PackageRevision %s/%s", sent, cachedPr.KubeObjectNamespace(), cachedPr.KubeObjectName())
	return cachedPr, nil
}

func (r *cachedRepository) UpdatePackageRevision(ctx context.Context, old repository.PackageRevision) (repository.PackageRevisionDraft, error) {
	klog.InfoS("[CR Cache] Loading draft for update from Git for PackageRevision", context1.LogMetadataFrom(ctx))
	defer func() {
		klog.V(3).InfoS("[CR Cache] Draft loaded and ready for modifications for PackageRevision", context1.LogMetadataFrom(ctx))
	}()

	// Unwrap
	unwrapped := old.(*cachedPackageRevision).PackageRevision

	return r.repo.UpdatePackageRevision(ctx, unwrapped)
}

func (r *cachedRepository) update(ctx context.Context, updated repository.PackageRevision) (*cachedPackageRevision, error) {
	// TODO: Technically we only need this package, not all packages
	if _, _, err := r.getCachedPackages(ctx, false); err != nil {
		klog.Warningf("failed to get cached packages: %v", err)
		// TODO: Invalidate all watches? We're dropping an add/update event
		return nil, err
	}

	r.mutex.Lock()
	if porchapi.LifecycleIsPublished(updated.Lifecycle(ctx)) {
		prevKey := updated.Key()
		prevKey.Revision = 0 // Drafts always have revision of 0
		delete(r.cachedPackageRevisions, prevKey)
	}

	cached := &cachedPackageRevision{
		PackageRevision: updated,
		metadataStore:   r.metadataStore,
	}
	r.cachedPackageRevisions[updated.Key()] = cached

	// Recompute latest package revisions.
	identifyLatestRevisions(ctx, r.cachedPackageRevisions)
	r.mutex.Unlock()

	// Create the main package revision
	if porchapi.LifecycleIsPublished(updated.Lifecycle(ctx)) {
		updatedMain := updated.ToMainPackageRevision(ctx)
		err := r.createMainPackageRevision(ctx, updatedMain)
		if err != nil {
			return nil, err
		}
	} else {
		version, err := r.repo.Version(ctx)
		if err != nil {
			return nil, err
		}
		r.mutex.Lock()
		r.lastVersion = version
		r.mutex.Unlock()
	}

	return cached, nil
}

func (r *cachedRepository) createMainPackageRevision(ctx context.Context, updatedMain repository.PackageRevision) error {
	// Search and delete any old main pkgRev of an older workspace in the cache
	var oldMainPR *cachedPackageRevision
	r.mutex.Lock()
	revisionsMap := r.cachedPackageRevisions
	r.mutex.Unlock()
	for pkgRevKey := range revisionsMap {
		r.mutex.Lock()
		rev := pkgRevKey.Revision
		key := pkgRevKey.PkgKey
		r.mutex.Unlock()
		if rev == -1 {
			if key == updatedMain.Key().PkgKey {
				r.mutex.Lock()
				oldMainPR = r.cachedPackageRevisions[pkgRevKey]
				r.mutex.Unlock()
				break
			}
		}
	}
	if oldMainPR != nil {
		delete(r.cachedPackageRevisions, oldMainPR.Key())
	}

	r.mutex.Lock()
	cachedMain := &cachedPackageRevision{
		PackageRevision: updatedMain,
		metadataStore:   r.metadataStore,
	}
	r.cachedPackageRevisions[updatedMain.Key()] = cachedMain
	r.mutex.Unlock()
	pkgRevMetaNN := types.NamespacedName{
		Name:      updatedMain.KubeObjectName(),
		Namespace: updatedMain.KubeObjectNamespace(),
	}

	// Create the package if it doesn't exist
	_, err := r.metadataStore.Get(ctx, pkgRevMetaNN)
	if apierrors.IsNotFound(err) {
		pkgRevMeta := metav1.ObjectMeta{
			Name:      updatedMain.KubeObjectName(),
			Namespace: updatedMain.KubeObjectNamespace(),
		}
		_, err := r.metadataStore.Create(ctx, pkgRevMeta, r.Key().Name, updatedMain.UID())
		if err != nil {
			klog.Warningf("unable to create PackageRev CR for %s/%s: %v",
				updatedMain.KubeObjectNamespace(), updatedMain.KubeObjectName(), err)
		}
	}
	version, err := r.repo.Version(ctx)
	if err != nil {
		return err
	}
	r.mutex.Lock()
	r.lastVersion = version
	r.mutex.Unlock()
	return nil
}

func (r *cachedRepository) DeletePackageRevision(ctx context.Context, prToDelete repository.PackageRevision) error {
	// We delete the PackageRev regardless of any finalizers, since it
	// will always have the same finalizers as the PackageRevision. This
	// will put the PackageRev, and therefore the PackageRevision in the
	// terminating state.
	// But we only delete the PackageRevision from the repo once all finalizers
	// have been removed.
	klog.InfoS("[CR Cache] Deleting PackageRev CR from etcd and branch from Git for PackageRevision", context1.LogMetadataFrom(ctx))
	defer func() {
		klog.V(3).InfoS("[CR Cache] PackageRev CR and Git branch deleted for PackageRevision", context1.LogMetadataFrom(ctx))
	}()

	namespacedName := types.NamespacedName{
		Name:      prToDelete.KubeObjectName(),
		Namespace: prToDelete.KubeObjectNamespace(),
	}
	pkgRevMeta, err := r.metadataStore.Delete(ctx, namespacedName, false)
	if err != nil && !apierrors.IsNotFound(err) {
		klog.Warningf("Error deleting PkgRevMeta %s: %v", namespacedName.String(), err)
	}

	if len(pkgRevMeta.Finalizers) > 0 {
		klog.Infof("PackageRevision %s deleted, but still have finalizers: %s", prToDelete.KubeObjectName(), strings.Join(pkgRevMeta.Finalizers, ","))
		sent := r.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Modified, prToDelete)
		klog.Infof("crcache: sent %d modified for deleted PackageRevision %s/%s with finalizers", sent, prToDelete.KubeObjectNamespace(), prToDelete.KubeObjectName())
		return nil
	}
	klog.Infof("PackageRevision %s deleted for real since no finalizers", prToDelete.KubeObjectName())

	// Check if this is the latest revision before deletion
	isLatest := prToDelete.(*cachedPackageRevision).IsLatestRevision()

	// Unwrap
	unwrapped := prToDelete.(*cachedPackageRevision).PackageRevision
	if err := r.repo.DeletePackageRevision(ctx, unwrapped); err != nil {
		return err
	}

	r.mutex.Lock()
	if r.cachedPackages != nil {
		k := prToDelete.Key()
		delete(r.cachedPackageRevisions, k)

		// Recompute latest package revisions.
		// TODO: Only for affected object / key?
		identifyLatestRevisions(ctx, r.cachedPackageRevisions)
	}

	// Check if we need to send async notification for new latest revision
	if isLatest {
		klog.Infof("crcache: %+v: latest PackageRevision deleted. Sending notification.", prToDelete.Key().PkgKey)
		go r.sendLatestPkgUpdateNotification(prToDelete.Key().PkgKey)
	}

	r.mutex.Unlock()

	if _, err := r.metadataStore.Delete(ctx, namespacedName, true); err != nil {
		// If this fails, the CR will be cleaned up by the background job.
		if !apierrors.IsNotFound(err) {
			klog.Warningf("Error deleting PkgRevMeta %s: %v", namespacedName.String(), err)
		}
	}

	sent := r.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Deleted, prToDelete)
	klog.Infof("crcache: sent %d for deleted PackageRevision %s/%s", sent, prToDelete.KubeObjectNamespace(), prToDelete.KubeObjectName())

	return nil
}

// sendLatestPkgUpdateNotification sends async notification when a new latest package revision is identified
func (r *cachedRepository) sendLatestPkgUpdateNotification(pkgKey repository.PackageKey) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, span := tracer.Start(ctx, "cachedRepository::sendLatestPkgUpdateNotification", trace.WithAttributes())
	defer span.End()
	// Find the new latest revision for this package
	r.mutex.RLock()
	var newLatest repository.PackageRevision
	for _, pr := range r.cachedPackageRevisions {
		if pr.Key().PkgKey == pkgKey && pr.IsLatestRevision() {
			newLatest = pr
			break
		}
	}
	r.mutex.RUnlock()

	if newLatest != nil {
		sent := r.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Modified, newLatest)
		klog.Infof("crcache: async notification sent %d for new latest PackageRevision %s/%s", sent, newLatest.KubeObjectNamespace(), newLatest.KubeObjectName())
	} else {
		klog.Infof("crcache: no new latest revision found for package %s after deletion. Notification not sent.", pkgKey.Package)
	}
}

func (r *cachedRepository) ListPackages(ctx context.Context, filter repository.ListPackageFilter) ([]repository.Package, error) {
	packages, err := r.getPackages(ctx, filter, false)
	if err != nil {
		return nil, err
	}

	return packages, nil
}

func (r *cachedRepository) Close(ctx context.Context) error {
	r.refreshWg.Wait()
	// Query PackageRevs from API server to ensure we delete all of them,
	// not just those in the in-memory cache
	pkgRevs, err := r.metadataStore.List(ctx, r.repoSpec)
	if err != nil {
		klog.Warningf("repo %+v: error listing PackageRevs during close: %v", r.Key(), err)
	} else {
		klog.Infof("repo %+v: Close called, found %d PackageRevs to delete", r.Key(), len(pkgRevs))
		sent := 0
		for _, pkgRevMeta := range pkgRevs {
			nn := types.NamespacedName{
				Name:      pkgRevMeta.Name,
				Namespace: pkgRevMeta.Namespace,
			}
			klog.Infof("repo %+v: deleting packagerev %s/%s because repository is closed", r.Key(), nn.Namespace, nn.Name)
			_, err := r.metadataStore.Delete(ctx, nn, true)
			if err != nil {
				klog.Warningf("repo %+v: error deleting packagerev %s: %v", r.Key(), nn.Name, err)
			} else {
				klog.Infof("repo %+v: successfully deleted packagerev %s/%s", r.Key(), nn.Namespace, nn.Name)
			}
			// Send delete notification if we have the PR in cache
			for _, pr := range r.cachedPackageRevisions {
				if pr.KubeObjectName() == nn.Name && pr.KubeObjectNamespace() == nn.Namespace {
					sent += r.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Deleted, pr)
					break
				}
			}
		}
		klog.Infof("repo %+v: sent %d notifications for %d package revisions during close", r.Key(), sent, len(pkgRevs))
	}

	return r.repo.Close(ctx)
}

// refreshAllCachedPackages updates the cached map for this repository with all the newPackages,
// it also triggers notifications for all package changes.
// mutex must be held.
func (r *cachedRepository) refreshAllCachedPackages(ctx context.Context) (map[repository.PackageKey]*cachedPackage, map[repository.PackageRevisionKey]*cachedPackageRevision, error) {
	ctx, span := tracer.Start(ctx, "cachedRepository::refreshAllCachedPackages", trace.WithAttributes())
	defer span.End()

	r.refreshWg.Add(1)
	defer r.refreshWg.Done()

	// TODO: Avoid simultaneous fetches?
	// TODO: Push-down partial refresh?

	start := time.Now()
	defer func() { klog.Infof("repo %+v: refresh finished in %f secs", r.Key(), time.Since(start).Seconds()) }()

	curVer, err := r.Version(ctx)
	if err != nil {
		return nil, nil, err
	}
	r.mutex.RLock()
	lastVer := r.lastVersion
	r.mutex.RUnlock()
	if curVer == lastVer {
		return r.cachedPackages, r.cachedPackageRevisions, nil
	}

	// Look up all existing PackageRevCRs so we an compare those to the
	// actual Packagerevisions found in git/oci, and add/prune PackageRevCRs
	// as necessary.
	existingPkgRevCRs, err := r.metadataStore.List(ctx, r.repoSpec)
	if err != nil {
		return nil, nil, err
	}
	// Create a map so we can quickly check if a specific PackageRevisionMeta exists.
	existingPkgRevCRsMap := make(map[string]metav1.ObjectMeta)
	for i := range existingPkgRevCRs {
		pr := existingPkgRevCRs[i]
		existingPkgRevCRsMap[pr.Name] = pr
	}

	// TODO: Can we avoid holding the lock for the ListPackageRevisions / identifyLatestRevisions section?
	newPackageRevisions, err := r.repo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		return nil, nil, errors.Wrap(err, "error listing packages")
	}

	// Build mapping from kubeObjectName to PackageRevisions for new PackageRevisions.
	r.mutex.Lock()
	newPackageRevisionNames := make(map[string]*cachedPackageRevision, len(newPackageRevisions))
	for _, newPackageRevision := range newPackageRevisions {
		kname := newPackageRevision.KubeObjectName()
		if newPackageRevisionNames[kname] != nil {
			klog.Warningf("repo %+v: found duplicate packages with name %v", r.Key(), kname)
		}

		pkgRev := &cachedPackageRevision{
			PackageRevision:  newPackageRevision,
			metadataStore:    r.metadataStore,
			isLatestRevision: false,
		}
		newPackageRevisionNames[newPackageRevision.KubeObjectName()] = pkgRev
	}
	r.mutex.Unlock()

	// Build mapping from kubeObjectName to PackageRevisions for existing PackageRevisions
	r.mutex.Lock()
	oldPackageRevisionNames := make(map[string]*cachedPackageRevision, len(r.cachedPackageRevisions))
	for _, oldPackage := range r.cachedPackageRevisions {
		oldPackageRevisionNames[oldPackage.KubeObjectName()] = oldPackage
	}
	r.mutex.Unlock()
	// We go through all PackageRev CRs that represents PackageRevisions
	// in the current repo and make sure they all have a corresponding
	// PackageRevision. The ones that doesn't is removed.
	for _, prm := range existingPkgRevCRs {
		if _, found := newPackageRevisionNames[prm.Name]; !found {
			klog.Infof("repo %+v: deleting PackageRev %s/%s because parent PackageRevision was not found",
				r.Key(), prm.Namespace, prm.Name)
			if _, err := r.metadataStore.Delete(ctx, types.NamespacedName{
				Name:      prm.Name,
				Namespace: prm.Namespace,
			}, true); err != nil {
				if !apierrors.IsNotFound(err) {
					// This will be retried the next time the sync runs.
					klog.Warningf("repo %+v: unable to delete PackageRev CR for %s/%s: %v",
						r.Key(), prm.Name, prm.Namespace, err)
				}
			}
		}
	}

	// Send notification for packages that changed before the creation of PkgRev to avoid race conditions because of ownerReferences.
	addSent := 0
	modSent := 0
	for kname, newPackage := range newPackageRevisionNames {
		oldPackage := oldPackageRevisionNames[kname]
		if oldPackage == nil {
			addSent += r.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Added, newPackage)
		} else {
			if oldPackage.ResourceVersion() != newPackage.ResourceVersion() {
				modSent += r.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Modified, newPackage)
			}
		}
	}

	// We go through all the PackageRevisions and make sure they have
	// a corresponding PackageRev CR.
	for pkgRevName, pkgRev := range newPackageRevisionNames {
		if _, found := existingPkgRevCRsMap[pkgRevName]; !found {
			pkgRevMeta := metav1.ObjectMeta{
				Name:      pkgRevName,
				Namespace: r.Key().Namespace,
			}
			if _, err := r.metadataStore.Create(ctx, pkgRevMeta, r.Key().Name, pkgRev.UID()); err != nil {
				// TODO: We should try to find a way to make these errors available through
				// either the repository CR or the PackageRevision CR. This will be
				// retried on the next sync.
				klog.Warningf("unable to create PackageRev CR for %s/%s: %v",
					r.Key().Namespace, pkgRevName, err)
			}
		}
	}

	delSent := 0
	// Send notifications for packages that was deleted in the SoT
	for kname, oldPackage := range oldPackageRevisionNames {
		if newPackageRevisionNames[kname] == nil {
			nn := types.NamespacedName{
				Name:      oldPackage.KubeObjectName(),
				Namespace: oldPackage.KubeObjectNamespace(),
			}
			klog.Infof("repo %+v: deleting PackageRev %s/%s because PackageRevision was removed from SoT",
				r.Key(), nn.Namespace, nn.Name)
			delSent += r.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Deleted, oldPackage)
		}
	}
	klog.Infof("repo %+v: addSent %d, modSent %d, delSent for %d old and %d new repo packages", r.Key(), addSent, modSent, len(oldPackageRevisionNames), len(newPackageRevisionNames))

	newPackageRevisionMap := make(map[repository.PackageRevisionKey]*cachedPackageRevision, len(newPackageRevisions))
	for _, newPackage := range newPackageRevisions {
		k := newPackage.Key()
		pkgRev := &cachedPackageRevision{
			PackageRevision:  newPackage,
			metadataStore:    r.metadataStore,
			isLatestRevision: false,
		}
		newPackageRevisionMap[k] = pkgRev
	}

	identifyLatestRevisions(ctx, newPackageRevisionMap)

	newPackageMap := make(map[repository.PackageKey]*cachedPackage)

	for _, newPackageRevision := range newPackageRevisionMap {
		if !newPackageRevision.isLatestRevision {
			continue
		}
	}
	r.mutex.Lock()
	r.cachedPackageRevisions = newPackageRevisionMap
	r.cachedPackages = newPackageMap
	r.lastVersion = curVer
	r.mutex.Unlock()

	return newPackageMap, newPackageRevisionMap, nil
}
