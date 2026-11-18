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
	"os"
	"path/filepath"

	"github.com/kptdev/porch/pkg/cache/lgcache/meta"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
)

type LGCacheFactory struct{}

func (f *LGCacheFactory) NewCache(_ context.Context, options cachetypes.CacheOptions) (cachetypes.Cache, error) {
	if !options.LGCacheOptions.RequireV1Alpha2 {
		return nil, fmt.Errorf("cache-type LG requires v1alpha2 PackageRevision mode (create-v1alpha2-rpkg); v1alpha1 aggregated-API-only deployments cannot use LG")
	}
	root := options.LGCacheOptions.PVCRoot
	if root == "" {
		root = options.ExternalRepoOptions.LocalDirectory
	}
	if root == "" {
		return nil, fmt.Errorf("lgcache PVC root is empty; set LG_CACHE_DIR or --cache-directory")
	}
	if err := os.MkdirAll(filepath.Join(root, "repos"), 0o755); err != nil {
		return nil, fmt.Errorf("lgcache PVC root %s is not writable: %w", root, err)
	}
	probe := filepath.Join(root, ".porch-lgcache-write")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return nil, fmt.Errorf("lgcache PVC root %s is not writable: %w", root, err)
	}
	_ = os.Remove(probe)

	options.LGCacheOptions.PVCRoot = root
	return &lgCache{
		options: options,
		meta:    &meta.Store{Client: options.CoreClient},
	}, nil
}
