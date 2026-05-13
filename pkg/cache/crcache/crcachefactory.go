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

package crcache

import (
	"context"

	"github.com/kptdev/porch/pkg/cache/crcache/meta"
	"github.com/kptdev/porch/pkg/cache/repomap"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
)

var _ cachetypes.CacheFactory = &CrCacheFactory{}

type CrCacheFactory struct {
}

func (f *CrCacheFactory) NewCache(_ context.Context, options cachetypes.CacheOptions) (cachetypes.Cache, error) {
	return &Cache{
		repositories:  repomap.SafeRepoMap{},
		metadataStore: meta.NewCrdMetadataStore(options.CoreClient),
		options:       options,
	}, nil
}
