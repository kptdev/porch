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

package cache

import (
	"context"
	"fmt"

	"github.com/kptdev/porch/pkg/cache/crcache"
	"github.com/kptdev/porch/pkg/cache/dbcache"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("cache")

func GetCacheImpl(ctx context.Context, options cachetypes.CacheOptions) (cachetypes.Cache, error) {
	ctx, span := tracer.Start(ctx, "Repository::RepositoryFactory", trace.WithAttributes())
	defer span.End()

	if cachetypes.CacheInstance != nil {
		return cachetypes.CacheInstance, nil
	}

	var cacheFactory cachetypes.CacheFactory

	switch cacheType := options.CacheType; cacheType {
	case cachetypes.CRCacheType:
		cacheFactory = new(crcache.CrCacheFactory)

	case cachetypes.DBCacheType:
		cacheFactory = new(dbcache.DBCacheFactory)

	default:
		return nil, fmt.Errorf("type %q not supported", cacheType)
	}

	if newCache, err := cacheFactory.NewCache(ctx, options); err == nil {
		cachetypes.CacheInstance = newCache
		return cachetypes.CacheInstance, err
	} else {
		return nil, err
	}
}
