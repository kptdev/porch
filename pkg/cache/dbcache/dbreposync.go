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

	if err = s.deletePRsOnlyInCache(ctx, cachedPrMap, inCachedOnly); err != nil {
		return repositorySyncStats{}, err
	}

	if err = s.cacheExternalPRs(ctx, externalPrMap, inExternalOnly); err != nil {
		return repositorySyncStats{}, err
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
					klog.Warningf("repositorySync %+v: skipping file %q in PR %+v (not compatible with PostgreSQL TEXT)", s.repo.Key(), key, extPRKey)
					continue
				}
				resources[key] = val
				resourcesSize += int64(len(val))
			}
		}

		if extAPIPR.CreationTimestamp.Time.IsZero() {
			extAPIPR.CreationTimestamp.Time = time.Now()
		}

		_, extPRUpstreamLock, _ := extPR.GetLock(ctx)

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

func (s *repositorySync) deletePRsOnlyInCache(ctx context.Context, cachedPrMap map[repository.PackageRevisionKey]repository.PackageRevision, inCachedOnly []repository.PackageRevisionKey) error {
	for _, dbPRKey := range inCachedOnly {
		dbPR := cachedPrMap[dbPRKey]

		pkgList, err := s.repo.ListPackages(ctx, repository.ListPackageFilter{Key: dbPR.Key().PKey()})
		if err != nil {
			return err
		}

		if len(pkgList) != 1 {
			err := fmt.Errorf("deletePRsOnlyInCache: reading package %+v should return 1 package, it returned %d packages", dbPR.Key().PKey(), len(pkgList))
			klog.Warning(err.Error())
			return err
		}

		dbPkg := pkgList[0].(*dbPackage)
		if err = dbPkg.DeletePackageRevision(ctx, dbPR, false); err != nil {
			klog.Errorf("repositorySync %+v: failed to delete cached PR %+v not in external repo", s.repo.Key(), dbPRKey)
			return err
		}
	}
	return nil
}
