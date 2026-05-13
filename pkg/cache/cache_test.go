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

package cache

import (
	"context"
	"testing"

	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/stretchr/testify/assert"
)

func TestCreateCacheImpl(t *testing.T) {
	cacheOptions := cachetypes.CacheOptions{
		CacheType: cachetypes.CRCacheType,
	}

	cacheOptions.CacheType = "BAD-CACHE"
	cache, err := GetCacheImpl(context.TODO(), cacheOptions)
	assert.True(t, err != nil)
	assert.Equal(t, cache, nil)

	cacheOptions.CacheType = "DB"
	cache, err = GetCacheImpl(context.TODO(), cacheOptions)
	assert.True(t, err != nil)
	assert.Equal(t, cache, nil)

	cacheOptions.CacheType = "CR"
	cache, err = GetCacheImpl(context.TODO(), cacheOptions)
	assert.True(t, err == nil)
	assert.Equal(t, 0, len(cache.GetRepositories()))

	cacheSame, err := GetCacheImpl(context.TODO(), cacheOptions)
	assert.True(t, err == nil)
	assert.Equal(t, cache, cacheSame)
}
