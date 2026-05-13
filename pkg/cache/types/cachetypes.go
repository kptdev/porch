// Copyright 2025 The kpt Authors
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

// Package cachetypes contains type definitions for caches in Porch.
package cachetypes

import (
	"context"
	"strings"
	"time"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CacheType string

const (
	CRCacheType      CacheType = "CR"
	DBCacheType      CacheType = "DB"
	DefaultCacheType CacheType = CRCacheType
)

type CacheOptions struct {
	ExternalRepoOptions        externalrepotypes.ExternalRepoOptions
	RepoOperationRetryAttempts int
	RepoPRChangeNotifier       RepoPRChangeNotifier
	CoreClient                 client.WithWatch
	CacheType                  CacheType
	DBCacheOptions             DBCacheOptions
	CRCacheOptions             CRCacheOptions
	DbPushDraftsToGit          bool
	RepoSyncFrequency          time.Duration
}

const DefaultDBCacheDriver string = "pgx"

type DBCacheOptions struct {
	Driver             string
	DataSource         string
	MaxConnections     int
	MaxIdleConnections int
	MaxConnLifetime    time.Duration
}

type CRCacheOptions struct {
	MaxConcurrentLists       int
	ListTimeoutPerRepository time.Duration
}

type Cache interface {
	OpenRepository(ctx context.Context, repositorySpec *configapi.Repository) (repository.Repository, error)
	CloseRepository(ctx context.Context, repositorySpec *configapi.Repository, allRepos []configapi.Repository) error
	GetRepositories() []*configapi.Repository
	GetRepository(repository.RepositoryKey) repository.Repository
	UpdateRepository(ctx context.Context, repositorySpec *configapi.Repository) error
	CheckRepositoryConnectivity(ctx context.Context, repositorySpec *configapi.Repository) error
	FindAllUpstreamReferencesInRepositories(ctx context.Context, namespace, prName string) (string, error)
	ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error)
}

var (
	CacheInstance Cache
)

type CacheFactory interface {
	NewCache(ctx context.Context, options CacheOptions) (Cache, error)
}

type RepoPRChangeNotifier interface {
	NotifyPackageRevisionChange(eventType watch.EventType, obj repository.PackageRevision) int
}

// noOpRepoPRChangeNotifier is a no-op implementation
type noOpRepoPRChangeNotifier struct{}

func (n *noOpRepoPRChangeNotifier) NotifyPackageRevisionChange(eventType watch.EventType, obj repository.PackageRevision) int {
	return 0
}

// NewNoOpRepoPRChangeNotifier creates a no-op notifier
func NewNoOpRepoPRChangeNotifier() RepoPRChangeNotifier {
	return &noOpRepoPRChangeNotifier{}
}

func IsACacheType(ct string) bool {
	switch strings.ToUpper(ct) {
	case string(CRCacheType):
		return true
	case string(DBCacheType):
		return true
	default:
		return false
	}
}
