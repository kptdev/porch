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

// Package dbcache implements a database cache for Porch.
package dbcache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kptdev/porch/api/porch/key"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/repomap"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo"
	"github.com/kptdev/porch/pkg/repository"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/klog/v2"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var tracer = otel.Tracer("dbcache")

var _ cachetypes.Cache = &dbCache{}

type dbCache struct {
	repositories repomap.SafeRepoMap
	options      cachetypes.CacheOptions
	prrStore     cachetypes.PRRStore

	// openLocks serialises opening and evicting the handle of one repository, so that
	// two concurrent opens after a handle-type change cannot each evict the other's handle.
	openLocks sync.Map // key.RepositoryKey -> *sync.Mutex
}

// lockRepository serialises open and evict for one repository and returns the unlock.
func (c *dbCache) lockRepository(repoKey key.RepositoryKey) func() {
	m, _ := c.openLocks.LoadOrStore(repoKey, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (c *dbCache) OpenRepository(ctx context.Context, repositorySpec *configapi.Repository) (repository.Repository, error) {
	_, span := tracer.Start(ctx, "dbCache::OpenRepository", trace.WithAttributes())
	defer span.End()

	repoKey, err := externalrepo.RepositoryKey(repositorySpec)
	if err != nil {
		return nil, err
	}

	// The migration annotation selects the handle type: v1alpha2 uses a
	// PRRRepository, v1alpha1 a dbRepository. A flip on an already-cached repo
	// requires dropping the stale handle first. The check, the eviction and the
	// creation below happen under one lock per repository.
	defer c.lockRepository(repoKey)()
	usePRR := shouldUsePRRRepository(repositorySpec)
	c.evictOnTypeChange(ctx, repoKey, usePRR)

	open := c.createRepository
	if usePRR {
		open = c.openPRRRepository
	}
	// Both handle types clone through the shared git directory pool, which can
	// transiently fail with "corrupted cache was removed" when a concurrent cleanup
	// wipes the on-disk clone; one retry recreates it. Wrapping here gives the
	// v1alpha2 (PRRStore) path the same recovery the v1alpha1 path already had.
	create := func() (repository.Repository, error) {
		repo, err := open(ctx, repoKey, repositorySpec)
		if err != nil && isTransientCacheOpenError(err) {
			klog.V(4).Infof("Retrying repository open after corrupted cache for %v", repoKey)
			time.Sleep(100 * time.Millisecond)
			repo, err = open(ctx, repoKey, repositorySpec)
		}
		return repo, err
	}

	repo, err := c.repositories.LoadOrCreate(repoKey, create)
	if err != nil {
		return nil, err
	}

	// dbRepository caches the latest spec; PRRRepository is immutable per open.
	if dbRepo, ok := repo.(*dbRepository); ok {
		dbRepo.spec = repositorySpec
	}
	return repo, nil
}

func (c *dbCache) createRepository(ctx context.Context, key key.RepositoryKey, repositorySpec *configapi.Repository) (repository.Repository, error) {
	klog.Infof("Creating repository : %v", key)

	dbRepo := &dbRepository{
		repoKey:              key,
		meta:                 repositorySpec.ObjectMeta,
		spec:                 repositorySpec,
		updated:              time.Now(),
		updatedBy:            getCurrentUser(),
		deployment:           repositorySpec.Spec.Deployment,
		repoPRChangeNotifier: c.options.RepoPRChangeNotifier,
		pushDraftsToGit:      c.options.DbPushDraftsToGit,
	}

	// Retry on transient corrupted-cache errors is handled by OpenRepository, which
	// wraps both this and the PRRStore create path uniformly.
	if err := dbRepo.OpenRepository(ctx, c.options.ExternalRepoOptions); err != nil {
		return nil, err
	}

	dbRepo.repositorySync = newRepositorySync(dbRepo, c.options)
	return dbRepo, nil
}

// SetPRRStore sets the PRRStore for v1alpha2 repos. Called after cache initialization to wire in the PRRStore.
func (c *dbCache) SetPRRStore(prrStore cachetypes.PRRStore) {
	c.prrStore = prrStore
}

func (c *dbCache) UpdateRepository(ctx context.Context, repositorySpec *configapi.Repository) error {
	_, span := tracer.Start(ctx, "dbCache::UpdateRepository", trace.WithAttributes())
	defer span.End()

	repoKey, err := externalrepo.RepositoryKey(repositorySpec)
	if err != nil {
		return err
	}

	dbRepo, ok := c.repositories.Load(repoKey)
	if !ok {
		return fmt.Errorf("dbcache.UpdateRepository: repo %+v not found", repoKey)
	}

	return updateRepositoryByType(ctx, dbRepo)
}

func (c *dbCache) CloseRepository(ctx context.Context, repositorySpec *configapi.Repository, allRepos []configapi.Repository) error {
	_, span := tracer.Start(ctx, "dbCache::CloseRepository", trace.WithAttributes())
	defer span.End()

	repoKey, err := externalrepo.RepositoryKey(repositorySpec)
	if err != nil {
		return err
	}
	defer c.lockRepository(repoKey)()

	dbRepo, ok := c.repositories.Load(repoKey)
	if !ok {
		// Not in the in-memory map (e.g. this process never opened it, or it failed
		// to open, or a different process/pod restart cleared the map). The DB row
		// can still exist independently, so we must still attempt the DB delete to
		// avoid leaving orphaned rows behind. The DB schema cascades this delete to
		// packages and package_revisions via ON DELETE CASCADE foreign keys.
		klog.V(4).Infof("dbcache.CloseRepository: repo %+v not found in cache; deleting DB row directly", repoKey)

		if err := repoDeleteFromDB(ctx, repoKey); err != nil {
			return pkgerrors.Wrapf(err, "failed to delete repo %+v from DB", repoKey)
		}
		return c.deletePRRStoreContent(ctx, repoKey)
	}

	defer func() {
		c.repositories.LoadAndDelete(repoKey)
	}()

	if dbRepo != nil {
		if err := dbRepo.Close(ctx); err != nil {
			return pkgerrors.Wrapf(err, "failed to close repository %+v", repoKey)
		}
		// dbRepository.Close removes the repository's v1alpha1 rows. A v1alpha2 handle's
		// Close only closes git, but a repository migrated from v1alpha1 keeps its old rows
		// for rollback; without this they would outlive the repository and be listed again.
		if _, isDB := dbRepo.(*dbRepository); !isDB {
			if err := repoDeleteFromDB(ctx, repoKey); err != nil {
				return pkgerrors.Wrapf(err, "failed to delete v1alpha1 rows of repo %+v", repoKey)
			}
		}
	}

	return c.deletePRRStoreContent(ctx, repoKey)
}

// deletePRRStoreContent removes the repository's PRRStore rows. The store has no foreign
// key to the repository, so without this they outlive it and list as ghost drafts if the
// name is registered again. It runs for every repository, as the migration annotation
// may have changed since the rows were written, and not in the handle's Close, which
// also evicts handles of repositories that stay registered.
func (c *dbCache) deletePRRStoreContent(ctx context.Context, repoKey key.RepositoryKey) error {
	if c.prrStore == nil {
		return nil
	}
	if err := c.prrStore.DeleteRepositoryResources(ctx, repoKey); err != nil {
		return pkgerrors.Wrapf(err, "failed to delete PRRStore content of repo %+v", repoKey)
	}
	return nil
}

// EvictCachedRepository removes a repository from the in-memory cache map and closes
// the git clone, but does NOT delete from the database. This is used by porch-server's
// cache handler to clean up memory without racing the controller on DB deletes.
func (c *dbCache) EvictCachedRepository(ctx context.Context, namespace, name string) error {
	_, span := tracer.Start(ctx, "dbCache::EvictCachedRepository", trace.WithAttributes())
	defer span.End()

	var repoKey key.RepositoryKey
	var found bool
	c.repositories.Range(func(k, _ any) bool {
		if rk := k.(key.RepositoryKey); rk.Namespace == namespace && rk.Name == name {
			repoKey, found = rk, true
			return false // stop iteration
		}
		return true
	})

	if !found {
		klog.V(4).Infof("dbCache.EvictCachedRepository: repo %s/%s not found in cache", namespace, name)
		return nil
	}

	// Remove and close under the repository lock, closing what was actually removed.
	defer c.lockRepository(repoKey)()
	if removed, ok := c.repositories.LoadAndDelete(repoKey); ok && removed != nil {
		if err := closeRepositoryByType(ctx, removed); err != nil {
			klog.Warningf("dbCache.EvictCachedRepository: failed to close repository %s/%s: %v", namespace, name, err)
		}
	}
	klog.Infof("Evicted repository from cache: %s/%s", namespace, name)

	return nil
}

func (c *dbCache) GetRepositories() []*configapi.Repository {
	var repositories []*configapi.Repository

	c.repositories.Range(func(key, value any) bool {
		// Value is stored as repository.Repository interface
		if spec := extractRepositorySpec(value.(repository.Repository)); spec != nil {
			repositories = append(repositories, spec)
		}
		return true
	})

	return repositories
}

func (c *dbCache) GetRepository(repoKey key.RepositoryKey) repository.Repository {
	repo, ok := c.repositories.Load(repoKey)
	if !ok {
		return nil
	}
	return repo
}

func (c *dbCache) CheckRepositoryConnectivity(ctx context.Context, repositorySpec *configapi.Repository) error {
	return externalrepo.CheckRepositoryConnection(ctx, repositorySpec, c.options.ExternalRepoOptions)
}

func (c *dbCache) FindAllUpstreamReferencesInRepositories(ctx context.Context, namespace, prName string) (string, error) {
	return findUpstreamRefsFromDB(ctx, namespace, prName)
}

func (c *dbCache) ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	dbprs, err := pkgRevListPRsFromDB(ctx, filter)
	if err != nil {
		return nil, err
	}
	prs := make([]repository.PackageRevision, len(dbprs))
	for i, dbpr := range dbprs {
		prs[i] = dbpr
	}
	return prs, nil
}
