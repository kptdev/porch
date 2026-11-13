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

package lgcache

import (
	"context"
	"fmt"

	"github.com/kptdev/porch/api/porch/key"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/lgcache/meta"
	"github.com/kptdev/porch/pkg/cache/repomap"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo"
	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var tracer = otel.Tracer("lgcache")

type lgCache struct {
	repositories repomap.SafeRepoMap
	options      cachetypes.CacheOptions
	meta         *meta.Store
}

var _ cachetypes.Cache = &lgCache{}

func (c *lgCache) OpenRepository(ctx context.Context, repositorySpec *configapi.Repository) (repository.Repository, error) {
	_, span := tracer.Start(ctx, "lgCache::OpenRepository", trace.WithAttributes())
	defer span.End()

	repoKey, err := externalrepo.RepositoryKey(repositorySpec)
	if err != nil {
		return nil, err
	}
	repo, err := c.repositories.LoadOrCreate(repoKey, func() (repository.Repository, error) {
		return c.createRepository(ctx, repoKey, repositorySpec)
	})
	if err != nil {
		return nil, err
	}
	lgRepo := repo.(*lgRepository)
	lgRepo.spec = repositorySpec
	return lgRepo, nil
}

func (c *lgCache) createRepository(ctx context.Context, repoKey key.RepositoryKey, repositorySpec *configapi.Repository) (repository.Repository, error) {
	klog.Infof("[LG Cache] opening repository %s", repoKey)
	lgRepo := &lgRepository{
		repoKey:     repoKey,
		spec:        repositorySpec,
		pvcRoot:     c.pvcRoot(),
		lockTimeout: c.options.LGCacheOptions.RepoLockTimeout,
		shardID:     c.options.LGCacheOptions.ShardID,
		pushDrafts:  c.options.DbPushDraftsToGit,
		options:     c.options.ExternalRepoOptions,
		meta:        c.meta,
		notifier:    c.options.RepoPRChangeNotifier,
	}
	if err := lgRepo.open(ctx); err != nil {
		return nil, err
	}
	return lgRepo, nil
}

func (c *lgCache) pvcRoot() string {
	if c.options.LGCacheOptions.PVCRoot != "" {
		return c.options.LGCacheOptions.PVCRoot
	}
	return c.options.ExternalRepoOptions.LocalDirectory
}

func (c *lgCache) CloseRepository(ctx context.Context, repositorySpec *configapi.Repository, _ []configapi.Repository) error {
	_, span := tracer.Start(ctx, "lgCache::CloseRepository", trace.WithAttributes())
	defer span.End()
	repoKey, err := externalrepo.RepositoryKey(repositorySpec)
	if err != nil {
		return err
	}
	repo, ok := c.repositories.LoadAndDelete(repoKey)
	if !ok || repo == nil {
		return nil
	}
	return repo.Close(ctx)
}

func (c *lgCache) GetRepositories() []*configapi.Repository {
	var out []*configapi.Repository
	c.repositories.Range(func(_, value any) bool {
		out = append(out, value.(*lgRepository).spec)
		return true
	})
	return out
}

func (c *lgCache) GetRepository(repoKey key.RepositoryKey) repository.Repository {
	repo, ok := c.repositories.Load(repoKey)
	if !ok || repo == nil {
		return nil
	}
	return repo
}

func (c *lgCache) UpdateRepository(_ context.Context, repositorySpec *configapi.Repository) error {
	repoKey, err := externalrepo.RepositoryKey(repositorySpec)
	if err != nil {
		return err
	}
	repo, ok := c.repositories.Load(repoKey)
	if !ok || repo == nil {
		return fmt.Errorf("lgcache: repository %s is not open", repoKey)
	}
	repo.(*lgRepository).spec = repositorySpec
	return nil
}

func (c *lgCache) CheckRepositoryConnectivity(ctx context.Context, repositorySpec *configapi.Repository) error {
	opts := c.options.ExternalRepoOptions
	opts.DisableRemoteFetchOnRead = false
	return externalrepo.CheckRepositoryConnection(ctx, repositorySpec, opts)
}

func (c *lgCache) FindAllUpstreamReferencesInRepositories(ctx context.Context, namespace, prName string) (string, error) {
	if prName == "" {
		return "", nil
	}
	var found string
	var walkErr error
	c.repositories.Range(func(k, value any) bool {
		repoKey := k.(key.RepositoryKey)
		if namespace != "" && repoKey.Namespace != namespace {
			return true
		}
		listed, err := value.(*lgRepository).ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
		if err != nil {
			walkErr = err
			return false
		}
		for _, pr := range listed {
			if pr.Key().Revision == -1 {
				continue
			}
			apiPR, err := pr.GetPackageRevision(ctx)
			if err != nil || apiPR == nil {
				continue
			}
			if upstreamRefName(apiPR.Spec.Tasks) == prName {
				found = pr.KubeObjectName()
				return false
			}
		}
		return true
	})
	return found, walkErr
}

func (c *lgCache) ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	if c.options.CoreClient == nil {
		return c.listOpenRepositories(ctx, filter)
	}
	repos, err := c.repositoriesForList(ctx, filter)
	if err != nil {
		return nil, err
	}
	var out []repository.PackageRevision
	for _, repo := range repos {
		opened, err := c.OpenRepository(ctx, repo)
		if err != nil {
			return nil, err
		}
		listed, err := opened.ListPackageRevisions(ctx, filter)
		if err != nil {
			return nil, err
		}
		out = append(out, listed...)
	}
	return out, nil
}

func (c *lgCache) listOpenRepositories(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	var out []repository.PackageRevision
	var walkErr error
	c.repositories.Range(func(k, value any) bool {
		repoKey := k.(key.RepositoryKey)
		if ok, _ := filter.MatchesNamespace(repoKey.Namespace); !ok {
			return true
		}
		if want := filter.FilteredRepository(); want != "" && want != repoKey.Name {
			return true
		}
		listed, err := value.(*lgRepository).ListPackageRevisions(ctx, filter)
		if err != nil {
			walkErr = err
			return false
		}
		out = append(out, listed...)
		return true
	})
	return out, walkErr
}

func (c *lgCache) repositoriesForList(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]*configapi.Repository, error) {
	var opts []client.ListOption
	if ns, ok := genericapirequest.NamespaceFrom(ctx); ok && ns != "" {
		if matches, filtered := filter.MatchesNamespace(ns); !matches {
			return nil, fmt.Errorf("conflicting namespaces specified: %q and %q", ns, filtered)
		}
		opts = append(opts, client.InNamespace(ns))
	} else if ns := filter.Key.RKey().Namespace; ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}

	var repoList configapi.RepositoryList
	if err := c.options.CoreClient.List(ctx, &repoList, opts...); err != nil {
		return nil, fmt.Errorf("lgcache: listing repositories: %w", err)
	}
	want := filter.FilteredRepository()
	var out []*configapi.Repository
	for i := range repoList.Items {
		item := &repoList.Items[i]
		if want != "" && item.Name != want {
			continue
		}
		if item.Spec.Type != configapi.RepositoryTypeGit {
			continue
		}
		out = append(out, item.DeepCopy())
	}
	return out, nil
}

func (c *lgCache) EvictCachedRepository(ctx context.Context, namespace, name string) error {
	var found bool
	c.repositories.Range(func(k, value any) bool {
		repoKey := k.(key.RepositoryKey)
		if repoKey.Namespace != namespace || repoKey.Name != name {
			return true
		}
		c.repositories.LoadAndDelete(repoKey)
		if value != nil {
			if err := value.(*lgRepository).Close(ctx); err != nil {
				klog.Warningf("[LG Cache] evict %s/%s: %v", namespace, name, err)
			}
		}
		found = true
		return false
	})
	if !found {
		klog.V(4).Infof("[LG Cache] evict %s/%s: not cached", namespace, name)
	}
	return nil
}
