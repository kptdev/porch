// Copyright 2025-2026 The kpt Authors
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
	"strings"
	stdSync "sync"
	"time"
	"unicode/utf8"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/internal/telemetry"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/repository"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/klog/v2"
)

type repositorySync struct {
	repo                    *dbRepository
	mutex                   stdSync.Mutex
	syncWg                  stdSync.WaitGroup
	lastExternalRepoVersion string
	lastExternalPRMap       map[repository.PackageRevisionKey]repository.PackageRevision
	lastSyncStats           repositorySyncStats
}

type repositorySyncStats struct {
	cachedOnly   int
	externalOnly int
	both         int
}

func newRepositorySync(repo *dbRepository, options cachetypes.CacheOptions) *repositorySync {
	s := repositorySync{
		repo: repo,
	}
	return &s
}

// SyncOnce synchronizes the DB cache with the external repository
func (s *repositorySync) SyncOnce(ctx context.Context) error {
	s.syncWg.Add(1)
	defer s.syncWg.Done()
	var err error
	s.lastSyncStats, err = s.sync(ctx)
	return err
}

func (s *repositorySync) sync(ctx context.Context) (repositorySyncStats, error) {
	ctx, span := tracer.Start(ctx, "Repository::sync", trace.WithAttributes())
	defer span.End()

	if !s.mutex.TryLock() {
		return repositorySyncStats{}, fmt.Errorf("repositorySync %+v: sync start failed because sync is already in progress", s.repo.Key())
	}
	defer s.mutex.Unlock()

	start := time.Now()
	klog.Infof("repositorySync %+v: sync started", s.repo.Key())

	defer func() {
		klog.Infof("repositorySync %+v: sync finished in %f secs", s.repo.Key(), time.Since(start).Seconds())
		klog.Infof(" %d package revisions were already cached", s.lastSyncStats.both)
		klog.Infof(" %d package revisions were cached from the external repository", s.lastSyncStats.externalOnly)
		klog.Infof(" %d cached package revisions not found in the external repo were removed from the cache", s.lastSyncStats.cachedOnly)
	}()

	cachedPrMap, err := s.getCachedPRMap(ctx)
	if err != nil {
		return repositorySyncStats{}, pkgerrors.Wrap(err, "sync failed reading cached package revisions")
	}

	klog.Infof("repositorySync %+v: found %d deployed package revisions in cached repository", s.repo.Key(), len(cachedPrMap))

	externalPrMap, err := s.getExternalPRMap(ctx)
	if err != nil {
		return repositorySyncStats{}, pkgerrors.Wrap(err, "sync failed reading external package revisions")
	}

	klog.Infof("repositorySync %+v: found %d package revisions in external repository", s.repo.Key(), len(externalPrMap))

	inCachedOnly, inBoth, inExternalOnly := s.comparePRMaps(ctx, cachedPrMap, externalPrMap)
	klog.Infof("repositorySync %+v: found %d cached only, %d in both, %d external only", s.repo.Key(), len(inCachedOnly), len(inBoth), len(inExternalOnly))

	if err = s.handleInCachedOnly(ctx, cachedPrMap, inCachedOnly); err != nil {
		return repositorySyncStats{}, err
	}

	if err = s.cacheExternalPRs(ctx, externalPrMap, inExternalOnly); err != nil {
		return repositorySyncStats{}, err
	}

	if s.repo.pushDraftsToGit {
		s.handleInBoth(ctx, cachedPrMap, inBoth)
	}

	return repositorySyncStats{
		cachedOnly:   len(inCachedOnly),
		externalOnly: len(inExternalOnly),
		both:         len(inBoth),
	}, nil
}

func (s *repositorySync) getCachedPRMap(ctx context.Context) (map[repository.PackageRevisionKey]repository.PackageRevision, error) {
	deployedFilter := repository.ListPackageRevisionFilter{}
	if !s.repo.pushDraftsToGit {
		deployedFilter = repository.ListPackageRevisionFilter{
			Lifecycles: []porchapi.PackageRevisionLifecycle{
				porchapi.PackageRevisionLifecyclePublished,
				porchapi.PackageRevisionLifecycleDeletionProposed,
			},
		}
	}

	cachedPrList, err := s.repo.ListPackageRevisions(ctx, deployedFilter)
	if err != nil {
		klog.Errorf("repositorySync %+v: failed to list cached package revisions", s.repo.Key())
		return nil, err
	}

	return repository.PrSlice2Map(cachedPrList), nil
}

func (s *repositorySync) getExternalPRMap(ctx context.Context) (map[repository.PackageRevisionKey]repository.PackageRevision, error) {
	externalRepoVersion, err := s.repo.Version(ctx)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "fetch of external repository %+v version failed", s.repo.Key())
	}

	if s.lastExternalRepoVersion == externalRepoVersion {
		klog.Infof("repositorySync %+v: external repository is still on cached version %s, new read of external repo not required", s.repo.Key(), s.lastExternalRepoVersion)
		return s.lastExternalPRMap, nil
	}

	externalPRList, err := s.repo.externalRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		klog.Errorf("repositorySync %+v: failed to list external package revisions", s.repo.Key())
		return nil, err
	}

	externalPRMap := repository.PrSlice2Map(externalPRList)

	s.lastExternalPRMap = externalPRMap
	s.lastExternalRepoVersion = externalRepoVersion

	return externalPRMap, nil
}

func (s *repositorySync) comparePRMaps(ctx context.Context, leftMap, rightMap map[repository.PackageRevisionKey]repository.PackageRevision) (leftOnly, both, rightOnly []repository.PackageRevisionKey) {
	_, span := tracer.Start(ctx, "Repository::comparePRMaps", trace.WithAttributes())
	defer span.End()

	var inLeftOnly, inBoth, inRightOnly []repository.PackageRevisionKey

	for leftPrKey := range leftMap {
		if _, ok := rightMap[leftPrKey]; ok {
			inBoth = append(inBoth, leftPrKey)
		} else {
			inLeftOnly = append(inLeftOnly, leftPrKey)
		}
	}

	for rightPrKey := range rightMap {
		if _, ok := leftMap[rightPrKey]; !ok {
			inRightOnly = append(inRightOnly, rightPrKey)
		}
	}

	return inLeftOnly, inBoth, inRightOnly
}

func (s *repositorySync) cacheExternalPRs(ctx context.Context, externalPrMap map[repository.PackageRevisionKey]repository.PackageRevision, inExternalOnly []repository.PackageRevisionKey) error {
	for _, extPRKey := range inExternalOnly {
		extPR := externalPrMap[extPRKey]

		extAPIPR, err := externalPrMap[extPRKey].GetPackageRevision(ctx)
		if err != nil {
			klog.Errorf("repositorySync %+v: failed to get API version of external package revision %+v", s.repo.Key(), extPRKey)
			return err
		}

		extPRResources, err := extPR.GetResources(ctx)
		if err != nil {
			klog.Errorf("repositorySync %+v: failed to get resources for external package revision %+v", s.repo.Key(), extPRKey)
			return err
		}

		// Guard against nil return from GetResources (interface contract allows it).
		resources, resourcesSize := s.sanitizeResources(extPRKey, extPRResources)

		if extAPIPR.CreationTimestamp.Time.IsZero() {
			extAPIPR.CreationTimestamp.Time = time.Now()
		}

		_, extPRUpstreamLock, _ := extPR.GetLock(ctx)

		if extPRKey.Revision == 0 && porchapi.LifecycleIsPublished(extAPIPR.Spec.Lifecycle) {
			klog.Warningf("repositorySync %+v: skipping external package revision %+v with invalid combination (revision=0, lifecycle=%s)",
				s.repo.Key(), extPRKey, extAPIPR.Spec.Lifecycle)
			continue
		}

		dbPR := dbPackageRevision{
			repo:               s.repo,
			pkgRevKey:          extPRKey,
			meta:               extAPIPR.ObjectMeta,
			spec:               &extAPIPR.Spec,
			updated:            time.Now(),
			lifecycle:          extAPIPR.Spec.Lifecycle,
			extPRID:            extPRUpstreamLock,
			tasks:              extAPIPR.Spec.Tasks,
			resources:          resources,
			deployment:         s.repo.deployment,
			kptfileStatus:      extractKptfileStatus(resources),
			resourcesSizeBytes: resourcesSize,
		}
		_, err = s.repo.savePackageRevision(ctx, &dbPR, true)
		if err != nil {
			klog.Errorf("repositorySync %+v: failed to save external package revision %+v to database", s.repo.Key(), extPRKey)
			return err
		}

		telemetry.RecordPackageRevisionResourcesSize(ctx, dbPR.Key(), dbPR.resourcesSizeBytes)
	}

	return nil
}

// sanitizeResources copies an external package revision's resources, dropping any files whose key or
// value contains invalid UTF-8 or NUL bytes (which PostgreSQL TEXT columns cannot store).
func (s *repositorySync) sanitizeResources(prKey repository.PackageRevisionKey, extPRResources *porchapi.PackageRevisionResources) (map[string]string, int64) {
	var resources map[string]string
	var resourcesSize int64
	if extPRResources == nil || extPRResources.Spec.Resources == nil {
		resources = make(map[string]string)
		resourcesSize = 0
	} else {
		// Filter out files with invalid UTF-8 or NUL bytes to avoid PostgreSQL TEXT errors.
		// Both resource_key and resource_value are TEXT columns, so both must be validated.
		resources = make(map[string]string, len(extPRResources.Spec.Resources))
		for key, val := range extPRResources.Spec.Resources {
			if !utf8.ValidString(key) || strings.Contains(key, "\x00") ||
				!utf8.ValidString(val) || strings.Contains(val, "\x00") {
				klog.Warningf("repositorySync %+v: skipping file %q in PR %+v (not compatible with PostgreSQL TEXT)", s.repo.Key(), key, prKey)
				continue
			}
			resources[key] = val
			resourcesSize += int64(len(val))
		}
	}
	return resources, resourcesSize
}

func (s *repositorySync) handleInCachedOnly(ctx context.Context, cachedPrMap map[repository.PackageRevisionKey]repository.PackageRevision, inCachedOnly []repository.PackageRevisionKey) error {
	var prsToPush []*dbPackageRevision

	for _, dbPRKey := range inCachedOnly {
		dbPR := cachedPrMap[dbPRKey]

		if dbPkgRev, ok := dbPR.(*dbPackageRevision); ok &&
			(dbPkgRev.lifecycle == porchapi.PackageRevisionLifecycleDraft || dbPkgRev.lifecycle == porchapi.PackageRevisionLifecycleProposed) &&
			!hasBeenPushedToGit(dbPkgRev) {
			if s.repo.pushDraftsToGit {
				klog.Infof("repositorySync %+v: cached-only %s PR %+v has not been pushed to git yet, queuing for push instead of deleting", s.repo.Key(), dbPkgRev.lifecycle, dbPRKey)
				prsToPush = append(prsToPush, dbPkgRev)
			} else {
				klog.Infof("repositorySync %+v: skipping deletion of cached %s PR %+v because it has not been pushed to git yet", s.repo.Key(), dbPkgRev.lifecycle, dbPRKey)
			}
			continue
		}

		if err := s.deleteCachedOnlyPR(ctx, dbPRKey, dbPR); err != nil {
			return err
		}
	}

	for _, pr := range prsToPush {
		s.enqueuePush(ctx, pr)
	}

	return nil
}

func (s *repositorySync) deleteCachedOnlyPR(ctx context.Context, dbPRKey repository.PackageRevisionKey, snapshot repository.PackageRevision) error {
	pkgKey := dbPRKey.PKey()
	pkgMutex := getOrInsertPkgLock(pkgKey)
	pkgMutex.Lock()
	defer func() {
		pkgMutex.Unlock()
		deletePkgLock(pkgKey)
	}()

	freshPR, err := pkgRevReadFromDB(ctx, dbPRKey, false)
	if err != nil {
		if err == sql.ErrNoRows {
			klog.Infof("repositorySync %+v: handleInCachedOnly: PR %+v already removed from the database, skipping deletion", s.repo.Key(), dbPRKey)
			return nil
		}
		return err
	}

	if snap, ok := snapshot.(*dbPackageRevision); ok && !freshPR.updated.Equal(snap.updated) {
		klog.Infof("repositorySync %+v: handleInCachedOnly: PR %+v changed since the cached list was taken (updated %v -> %v), skipping deletion", s.repo.Key(), dbPRKey, snap.updated, freshPR.updated)
		return nil
	}

	if (freshPR.lifecycle == porchapi.PackageRevisionLifecycleDraft || freshPR.lifecycle == porchapi.PackageRevisionLifecycleProposed) &&
		!hasBeenPushedToGit(freshPR) {
		klog.Infof("repositorySync %+v: handleInCachedOnly: PR %+v is now an unpushed %s revision, skipping deletion", s.repo.Key(), dbPRKey, freshPR.lifecycle)
		return nil
	}

	pkgList, err := s.repo.ListPackages(ctx, repository.ListPackageFilter{Key: pkgKey})
	if err != nil {
		return err
	}

	if len(pkgList) != 1 {
		err := fmt.Errorf("handleInCachedOnly: reading package %+v should return 1 package, it returned %d packages", pkgKey, len(pkgList))
		klog.Warning(err.Error())
		return err
	}

	dbPkg := pkgList[0].(*dbPackage)
	if err = dbPkg.DeletePackageRevision(ctx, freshPR, false); err != nil {
		klog.Errorf("repositorySync %+v: failed to delete cached PR %+v not in external repo", s.repo.Key(), dbPRKey)
		return err
	}

	return nil
}

func (s *repositorySync) handleInBoth(ctx context.Context, cachedPrMap map[repository.PackageRevisionKey]repository.PackageRevision, inBoth []repository.PackageRevisionKey) {
	ctx, span := tracer.Start(ctx, "Repository::handleInBoth", trace.WithAttributes())
	defer span.End()

	for _, prKey := range inBoth {
		cachedPR, ok := cachedPrMap[prKey].(*dbPackageRevision)
		if !ok {
			continue
		}

		if cachedPR.lifecycle != porchapi.PackageRevisionLifecycleDraft && cachedPR.lifecycle != porchapi.PackageRevisionLifecycleProposed {
			continue
		}

		if dbContentChangedSincePush(cachedPR) {
			klog.Infof("repositorySync %+v: reconcile %+v: DB changed since last push, pushing to git", s.repo.Key(), prKey)
			s.enqueuePush(ctx, cachedPR)
		}
	}
}

func (s *repositorySync) enqueuePush(ctx context.Context, pr *dbPackageRevision) {
	pushCtx := context.WithoutCancel(ctx)
	go PushDraftPackageRevision(pushCtx, s.repo.Key(), pr)
}
