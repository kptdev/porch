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
	"fmt"
	"time"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/crcache/meta"
	"github.com/kptdev/porch/pkg/cache/repomap"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo"
	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/fields"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var tracer = otel.Tracer("crcache")

type Cache struct {
	repositories  repomap.SafeRepoMap
	metadataStore meta.MetadataStore
	options       cachetypes.CacheOptions
}

var _ cachetypes.Cache = &Cache{}

func (c *Cache) OpenRepository(ctx context.Context, repositorySpec *configapi.Repository) (repository.Repository, error) {
	ctx, span := tracer.Start(ctx, "Cache::OpenRepository", trace.WithAttributes())
	defer span.End()
	start := time.Now()
	defer func() { klog.V(4).Infof("Cache::OpenRepository (%s) took %s", repositorySpec.Name, time.Since(start)) }()

	key, err := externalrepo.RepositoryKey(repositorySpec)
	if err != nil {
		return nil, err
	}

	repo, err := c.repositories.LoadOrCreate(key, func() (repository.Repository, error) {
		return c.createRepository(ctx, key, repositorySpec)
	})
	if err != nil {
		return nil, err
	}

	cachedRepo := repo.(*cachedRepository)
	cachedRepo.repoSpec = repositorySpec
	if err := cachedRepo.getRefreshError(); err != nil {
		return nil, err
	}
	return cachedRepo, nil
}

func (c *Cache) createRepository(ctx context.Context, key repository.RepositoryKey, repositorySpec *configapi.Repository) (repository.Repository, error) {
	klog.Infof("Creating repository : %v", key)
	externalRepo, err := externalrepo.CreateRepositoryImpl(ctx, repositorySpec, c.options.ExternalRepoOptions)
	if err != nil {
		return nil, err
	}
	return newRepository(key, repositorySpec, externalRepo, c.metadataStore, c.options), nil
}

func (c *Cache) UpdateRepository(context.Context, *configapi.Repository) error {
	panic("Update on CR cached repositories is not applicable")
}

func (c *Cache) CloseRepository(ctx context.Context, repositorySpec *configapi.Repository, allRepos []configapi.Repository) error {
	_, span := tracer.Start(ctx, "Cache::CloseRepository", trace.WithAttributes())
	defer span.End()

	key, err := externalrepo.RepositoryKey(repositorySpec)
	if err != nil {
		return err
	}

	repo, ok := c.repositories.LoadAndDelete(key)

	if ok && repo != nil {
		if err := repo.Close(ctx); err != nil {
			return err
		}
	} else if ok {
		klog.Warningf("cached repository with key %q had stored value nil", key)
	}

	return nil
}

func (c *Cache) GetRepositories() []*configapi.Repository {
	repoSlice := []*configapi.Repository{}

	c.repositories.Range(func(key, value any) bool {
		repoSlice = append(repoSlice, value.(*cachedRepository).repoSpec)
		return true
	})

	return repoSlice
}

func (c *Cache) GetRepository(repoKey repository.RepositoryKey) repository.Repository {
	repo, _ := c.repositories.Load(repoKey)
	return repo
}

func (c *Cache) CheckRepositoryConnectivity(ctx context.Context, repositorySpec *configapi.Repository) error {
	return externalrepo.CheckRepositoryConnection(ctx, repositorySpec, c.options.ExternalRepoOptions)
}

func (c *Cache) FindAllUpstreamReferencesInRepositories(ctx context.Context, namespace, prName string) (string, error) {
	var downstreamName string
	c.repositories.Range(func(key, value any) bool {
		cachedRepo := value.(*cachedRepository)
		if cachedRepo.Key().Namespace != namespace {
			return true
		}
		cachedRepo.mutex.RLock()
		for _, pr := range cachedRepo.cachedPackageRevisions {
			// Skip main branch packages (revision = -1) as they are auto-managed
			if pr.Key().Revision == -1 {
				continue
			}
			apiPR, err := pr.GetPackageRevision(ctx)
			if err != nil {
				continue
			}
			for _, task := range apiPR.Spec.Tasks {
				var matched bool
				switch task.Type {
				case "clone":
					matched = task.Clone != nil && task.Clone.Upstream.UpstreamRef != nil && task.Clone.Upstream.UpstreamRef.Name == prName
				case "upgrade":
					matched = task.Upgrade != nil && task.Upgrade.NewUpstream.Name == prName
				}
				if matched {
					downstreamName = pr.KubeObjectName()
					cachedRepo.mutex.RUnlock()
					return false
				}
			}
		}
		cachedRepo.mutex.RUnlock()
		return true
	})
	return downstreamName, nil
}

func (c *Cache) ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {

	var opts []client.ListOption

	namespace, namespaced := genericapirequest.NamespaceFrom(ctx)
	if namespaced && namespace != "" {
		if namespaceMatches, filteredNamespace := filter.MatchesNamespace(namespace); !namespaceMatches {
			return nil, fmt.Errorf("conflicting namespaces specified: %q and %q", namespace, filteredNamespace)
		}

		opts = append(opts, client.InNamespace(namespace))
	}

	if filterRepo := filter.FilteredRepository(); filterRepo != "" {
		opts = append(opts, client.MatchingFields(fields.Set{"metadata.name": filterRepo}))
	}

	var repoList configapi.RepositoryList
	if err := c.options.CoreClient.List(ctx, &repoList, opts...); err != nil {
		return nil, fmt.Errorf("error listing repository objects: %w", err)
	}
	repos := repoList.Items

	type pkgRevResult struct {
		Revisions []repository.PackageRevision
		Err       error
	}

	repoCount := len(repos)
	if repoCount == 0 {
		return []repository.PackageRevision{}, nil
	}

	workerCount := repoCount
	if c.options.CRCacheOptions.MaxConcurrentLists > 0 {
		workerCount = min(c.options.CRCacheOptions.MaxConcurrentLists, repoCount)
	}

	klog.V(3).Infof("Listing %d repositories with %d workers", repoCount, workerCount)

	resultsCh := make(chan pkgRevResult, workerCount)
	repoQueue := make(chan *configapi.Repository, repoCount)

	for i := 0; i < workerCount; i++ {
		go func() {
			for repo := range repoQueue {
				klog.V(3).Infof("[WORKER %d] Processing repository %s", i, repo.Name)
				listCtx := ctx
				var cancel context.CancelFunc
				if c.options.CRCacheOptions.ListTimeoutPerRepository != 0 {
					listCtx, cancel = context.WithTimeout(ctx, c.options.CRCacheOptions.ListTimeoutPerRepository)
				}
				cachedRepo, err := c.OpenRepository(ctx, repo)
				if err != nil {
					if cancel != nil {
						cancel()
					}
					resultsCh <- pkgRevResult{Err: err}
					continue
				}
				revisions, err := cachedRepo.ListPackageRevisions(listCtx, filter)
				klog.V(3).Infof("[WORKER %d] ListPackageRevisions for %s done, len: %d, err: %v", i, repo.Name, len(revisions), err)
				resultsCh <- pkgRevResult{Revisions: revisions, Err: err}
				if cancel != nil {
					cancel()
				}
			}
		}()
	}

	for _, repo := range repos {
		repoQueue <- &repo
	}

	innerCtx := ctx
	if deadline, ok := ctx.Deadline(); ok {
		const buffer = 5 * time.Second
		if timeout := time.Until(deadline.Add(-buffer)); timeout > 0 {
			var cancel context.CancelFunc
			innerCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}

	received := 0
	resultPRs := []repository.PackageRevision{}

	for {
		select {
		case <-innerCtx.Done():
			klog.Warningf("Timeout reached — returning partial results")
			close(repoQueue)
			return resultPRs, nil

		case res := <-resultsCh:
			received++
			klog.V(4).Infof("listPackageRevisions received %d repo", received)
			if res.Err != nil {
				klog.Warningf("error listing package revisions: %+v", res.Err)
			}
			resultPRs = append(resultPRs, res.Revisions...)

		}
		if received == repoCount {
			close(repoQueue)
			return resultPRs, nil
		}
	}
}
