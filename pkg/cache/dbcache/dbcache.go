// Copyright 2024-2025 The kpt Authors
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
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kptdev/porch/api/porch/key"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/repomap"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo"
	"github.com/kptdev/porch/pkg/repository"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"k8s.io/klog/v2"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var tracer = otel.Tracer("dbcache")

var _ cachetypes.Cache = &dbCache{}

type dbCache struct {
	repositories repomap.SafeRepoMap
	options      cachetypes.CacheOptions
}

func (c *dbCache) OpenRepository(ctx context.Context, repositorySpec *configapi.Repository) (repository.Repository, error) {
	_, span := tracer.Start(ctx, "dbCache::OpenRepository")
	defer span.End()

	repoKey, err := externalrepo.RepositoryKey(repositorySpec)
	if err != nil {
		return nil, err
	}

	// On a rehome (URL changed, same cache key) the reopen rebuilds the handle and
	// can push a branch (createBranch=true). Validate here so every caller is
	// guarded, then evict so the reopen uses the new URL.
	if existing, ok := c.repositories.Load(repoKey); ok && existing != nil {
		if dbRepo, ok := existing.(*dbRepository); ok && gitURLChanged(dbRepo.spec, repositorySpec) {
			klog.Infof("dbCache.OpenRepository: git URL changed for %s/%s; validating and evicting stale cached handle",
				repositorySpec.Namespace, repositorySpec.Name)
			if err := c.ValidateRepositoryHistory(ctx, repositorySpec); err != nil {
				return nil, err
			}
			if err := c.EvictCachedRepository(ctx, repositorySpec.Namespace, repositorySpec.Name); err != nil {
				return nil, err
			}
		}
	}

	repo, err := c.repositories.LoadOrCreate(repoKey, func() (repository.Repository, error) {
		return c.createRepository(ctx, repoKey, repositorySpec)
	})
	if err != nil {
		return nil, err
	}

	dbRepo := repo.(*dbRepository)
	dbRepo.spec = repositorySpec
	return dbRepo, nil
}

// gitURLChanged reports whether both specs are git repos with differing URLs
// (a rehome). Type/OCI-registry changes alter the cache key, so never reach here.
func gitURLChanged(oldSpec, newSpec *configapi.Repository) bool {
	if oldSpec == nil || newSpec == nil {
		return false
	}
	if oldSpec.Spec.Git == nil || newSpec.Spec.Git == nil {
		return false
	}
	return oldSpec.Spec.Git.Repo != newSpec.Spec.Git.Repo
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

	err := dbRepo.OpenRepository(ctx, c.options.ExternalRepoOptions)
	if err != nil {
		// Retry once if corrupted cache was removed - allows cache to be recreated
		errMsg := err.Error()
		if strings.Contains(errMsg, "repository does not exist") || strings.Contains(errMsg, "corrupted cache was removed") {
			klog.V(4).Infof("Retrying repository open after corrupted cache for %v", key)
			time.Sleep(100 * time.Millisecond)
			err = dbRepo.OpenRepository(ctx, c.options.ExternalRepoOptions)
		}
		if err != nil {
			return nil, err
		}
	}

	dbRepo.repositorySync = newRepositorySync(dbRepo, c.options)
	return dbRepo, nil
}

func (c *dbCache) UpdateRepository(ctx context.Context, repositorySpec *configapi.Repository) error {
	_, span := tracer.Start(ctx, "dbCache::UpdateRepository")
	defer span.End()

	repoKey, err := externalrepo.RepositoryKey(repositorySpec)
	if err != nil {
		return err
	}

	dbRepo, ok := c.repositories.Load(repoKey)
	if !ok {
		return fmt.Errorf("dbcache.UpdateRepository: repo %+v not found", repoKey)
	}

	return repoUpdateDB(ctx, dbRepo.(*dbRepository))
}

func (c *dbCache) CloseRepository(ctx context.Context, repositorySpec *configapi.Repository, allRepos []configapi.Repository) error {
	_, span := tracer.Start(ctx, "dbCache::CloseRepository")
	defer span.End()

	repoKey, err := externalrepo.RepositoryKey(repositorySpec)
	if err != nil {
		return err
	}

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
		return nil
	}

	defer func() {
		c.repositories.LoadAndDelete(repoKey)
	}()

	if dbRepo != nil {
		if err := dbRepo.Close(ctx); err != nil {
			return pkgerrors.Wrapf(err, "failed to close db repository %+v", repoKey)
		}
	}

	return nil
}

// EvictCachedRepository removes a repository from the in-memory cache map and closes
// the git clone, but does NOT delete from the database. This is used by porch-server's
// cache handler to clean up memory without racing the controller on DB deletes.
func (c *dbCache) EvictCachedRepository(ctx context.Context, namespace, name string) error {
	_, span := tracer.Start(ctx, "dbCache::EvictCachedRepository")
	defer span.End()

	var found bool
	c.repositories.Range(func(k, v any) bool {
		repoKey := k.(key.RepositoryKey)
		if repoKey.Namespace == namespace && repoKey.Name == name {
			c.repositories.LoadAndDelete(repoKey)
			if v != nil {
				dbRepo := v.(*dbRepository)
				// Wait for any in-flight sync to complete before closing
				if dbRepo.repositorySync != nil {
					dbRepo.repositorySync.syncWg.Wait()
				}
				if dbRepo.externalRepo != nil {
					if err := dbRepo.externalRepo.Close(ctx); err != nil {
						klog.Warningf("dbCache.EvictCachedRepository: failed to close external repo %s/%s: %v", namespace, name, err)
					}
				}
			}
			found = true
			return false // stop iteration
		}
		return true
	})

	if !found {
		klog.V(4).Infof("dbCache.EvictCachedRepository: repo %s/%s not found in cache", namespace, name)
	} else {
		klog.Infof("Evicted repository from cache: %s/%s", namespace, name)
	}

	return nil
}

func (c *dbCache) GetRepositories() []*configapi.Repository {
	var repositories []*configapi.Repository

	c.repositories.Range(func(key, value any) bool {
		repositories = append(repositories, value.(*dbRepository).spec)
		return true
	})

	return repositories
}

func (c *dbCache) GetRepository(repoKey key.RepositoryKey) repository.Repository {
	repo, ok := c.repositories.Load(repoKey)
	if !ok || repo == nil {
		return nil
	}
	return repo.(*dbRepository)
}

var _ cachetypes.ReadOnlyRepositoryOpener = &dbCache{}

// OpenRepositoryReadOnly returns a handle that reads a repository's package revisions from the
// database. If this process has opened the repository, that handle is returned. Otherwise the
// handle is built from the spec: it is not registered in the cache, has no external repo and no
// sync, and rejects writes. The repository must already be in the database, i.e. opened by the
// process that owns it.
func (c *dbCache) OpenRepositoryReadOnly(ctx context.Context, repositorySpec *configapi.Repository) (repository.Repository, error) {
	_, span := tracer.Start(ctx, "dbCache::OpenRepositoryReadOnly")
	defer span.End()

	repoKey, err := externalrepo.RepositoryKey(repositorySpec)
	if err != nil {
		return nil, err
	}
	if repo := c.GetRepository(repoKey); repo != nil {
		return repo, nil
	}
	if _, err := repoReadFromDB(ctx, repoKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("repository %s is not in the database yet (not opened by its owner)", repoKey)
		}
		return nil, fmt.Errorf("repository %s: reading from database: %w", repoKey, err)
	}
	klog.V(3).Infof("dbCache.OpenRepositoryReadOnly: serving reads for %v from the database", repoKey)
	return &dbRepository{
		repoKey:         repoKey,
		meta:            repositorySpec.ObjectMeta,
		spec:            repositorySpec,
		deployment:      repositorySpec.Spec.Deployment,
		pushDraftsToGit: c.options.DbPushDraftsToGit,
		readOnly:        true,
	}, nil
}

func (c *dbCache) CheckRepositoryConnectivity(ctx context.Context, repositorySpec *configapi.Repository) error {
	return externalrepo.CheckRepositoryConnection(ctx, repositorySpec, c.options.ExternalRepoOptions)
}

func (c *dbCache) ValidateRepositoryHistory(ctx context.Context, repositorySpec *configapi.Repository) error {
	return externalrepo.ValidateRepositoryHistory(ctx, repositorySpec, c.options.ExternalRepoOptions)
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
