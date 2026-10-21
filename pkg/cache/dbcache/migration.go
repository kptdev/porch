// Copyright 2026 The kpt Authors
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

	"github.com/kptdev/porch/api/porch/key"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/repository/prrbackend"
	"k8s.io/klog/v2"
)

// Routes repositories to either dbRepository (v1alpha1: SQL) or PRRRepository (v1alpha2: git + PRRStore).

// shouldUsePRRRepository reports whether a repo uses PRRRepository (v1alpha2, git+PRRStore)
// rather than dbRepository (v1alpha1, git+SQL), based on the migration annotation.
func shouldUsePRRRepository(repositorySpec *configapi.Repository) bool {
	if repositorySpec == nil || repositorySpec.Annotations == nil {
		return false
	}
	return repositorySpec.Annotations[configapi.AnnotationKeyV1Alpha2Migration] == configapi.AnnotationValueMigrationEnabled
}

// repoHandleTypeMismatch reports whether a cached handle's concrete type no longer
// matches the migration annotation (annotation flipped since the handle was created).
// A stale handle would keep using the wrong path — PRRStore never populates, or SQL
// metadata never updates — so it must be evicted and recreated.
func repoHandleTypeMismatch(repo repository.Repository, wantPRRRepository bool) bool {
	_, isPRRRepo := repo.(*prrbackend.Repository)
	return isPRRRepo != wantPRRRepository
}

// evictOnTypeChange closes and drops a cached handle whose type no longer matches the
// migration annotation, so the next LoadOrCreate rebuilds the correct type. No-op if
// there is no cached handle or the type already matches.
func (c *dbCache) evictOnTypeChange(ctx context.Context, repoKey key.RepositoryKey, wantPRRRepository bool) {
	repo, ok := c.repositories.Load(repoKey)
	if !ok || repo == nil {
		return
	}
	if !repoHandleTypeMismatch(repo, wantPRRRepository) {
		return
	}
	klog.Infof("dbcache: repo %+v handle type mismatch (wantPRR=%v); evicting stale handle", repoKey, wantPRRRepository)
	c.repositories.LoadAndDelete(repoKey)
	if err := closeRepositoryByType(ctx, repo); err != nil {
		klog.Warningf("dbcache: failed to close stale handle for %+v: %v", repoKey, err)
	}
}

// isTransientCacheOpenError reports whether a repo-open failure is a corrupted-cache
// condition that a single retry can recover.
func isTransientCacheOpenError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "repository does not exist") || strings.Contains(msg, "corrupted cache was removed")
}

// updateRepositoryByType handles type-specific updates (v1alpha1 → SQL; v1alpha2 → git-only).
func updateRepositoryByType(ctx context.Context, repo repository.Repository) error {
	if dbr, ok := repo.(*dbRepository); ok {
		return repoUpdateDB(ctx, dbr)
	}
	return nil
}

// closeRepositoryByType closes the appropriate implementation (dbRepository or PRRRepository).
func closeRepositoryByType(ctx context.Context, repo repository.Repository) error {
	if dbRepo, ok := repo.(*dbRepository); ok {
		if dbRepo.repositorySync != nil {
			dbRepo.repositorySync.syncWg.Wait()
		}
		if dbRepo.externalRepo != nil {
			if err := dbRepo.externalRepo.Close(ctx); err != nil {
				return fmt.Errorf("failed to close external repo: %w", err)
			}
		}
	} else if prrRepo, ok := repo.(*prrbackend.Repository); ok {
		if err := prrRepo.Close(ctx); err != nil {
			return fmt.Errorf("failed to close PRR repo: %w", err)
		}
	}
	return nil
}

// extractRepositorySpec returns the spec from either repository type.
func extractRepositorySpec(repo repository.Repository) *configapi.Repository {
	if dbRepo, ok := repo.(*dbRepository); ok {
		return dbRepo.spec
	} else if prrRepo, ok := repo.(*prrbackend.Repository); ok {
		meta := prrRepo.Meta()
		return &meta
	}
	return nil
}

// openPRRRepository creates a v1alpha2 PRRRepository via the prrstore factory.
func (c *dbCache) openPRRRepository(ctx context.Context, key key.RepositoryKey, repositorySpec *configapi.Repository) (repository.Repository, error) {
	return prrbackend.NewRepository(ctx, key, repositorySpec, c.prrStore, c.options.ExternalRepoOptions)
}
