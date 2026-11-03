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
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/git"
	"github.com/kptdev/porch/pkg/repository"
	"k8s.io/klog/v2"
)

func pushToExternal(ctx context.Context, remote repository.Repository, pr repository.PackageRevision, published bool) error {
	if remote == nil {
		return fmt.Errorf("lgcache: no remote repository for push of %s", pr.Key())
	}
	lifecycle := pr.Lifecycle(ctx)
	if published && lifecycle != porchapi.PackageRevisionLifecyclePublished {
		return fmt.Errorf("lgcache: cannot publish %s, lifecycle is %s", pr.Key(), lifecycle)
	}

	apiPR, err := pr.GetPackageRevision(ctx)
	if err != nil {
		return fmt.Errorf("lgcache: push %s: %w", pr.Key(), err)
	}
	resources, err := pr.GetResources(ctx)
	if err != nil {
		return fmt.Errorf("lgcache: push resources %s: %w", pr.Key(), err)
	}
	if resources == nil {
		resources = &porchapi.PackageRevisionResources{}
	}

	draft, err := draftForPush(ctx, remote, pr.Key(), apiPR)
	if err != nil {
		return err
	}
	if err := draft.UpdateResources(ctx, resources, commitTask(apiPR)); err != nil {
		return fmt.Errorf("lgcache: update resources for push %s: %w", pr.Key(), err)
	}
	if err := draft.UpdateLifecycle(ctx, lifecycle); err != nil {
		return err
	}

	version := pr.Key().Revision
	if published && version <= 0 {
		gitRemote, ok := remote.(git.GitRepository)
		if !ok {
			return fmt.Errorf("lgcache: remote for %s is not a git repository", pr.Key())
		}
		maxRev, revErr := gitRemote.MaxPublishedRevision(ctx, pr.Key().PKey())
		if revErr != nil {
			return revErr
		}
		version = maxRev + 1
	}
	if _, err := remote.ClosePackageRevisionDraft(ctx, draft, version); err != nil {
		return fmt.Errorf("lgcache: close pushed revision %s: %w", pr.Key(), err)
	}
	klog.Infof("[LG Cache] pushed %s lifecycle=%s revision=%d", pr.Key(), lifecycle, version)
	return nil
}

func draftForPush(ctx context.Context, remote repository.Repository, prKey key.PackageRevisionKey, apiPR *porchapi.PackageRevision) (repository.PackageRevisionDraft, error) {
	gitRemote, ok := remote.(git.GitRepository)
	if !ok {
		return nil, fmt.Errorf("lgcache: remote for %s is not a git repository", prKey)
	}
	existing, err := gitRemote.FindPackageRevisionByKey(ctx, prKey)
	if err == nil && existing != nil {
		draft, uerr := gitRemote.UpdatePackageRevisionHead(ctx, existing)
		if uerr == nil {
			return draft, nil
		}
		klog.V(2).Infof("[LG Cache] update of existing external revision %s failed, creating a new draft: %v", prKey, uerr)
	}
	draft, err := remote.CreatePackageRevisionDraft(ctx, apiPR)
	if err != nil {
		return nil, fmt.Errorf("lgcache: create external draft %s: %w", prKey, err)
	}
	return draft, nil
}
