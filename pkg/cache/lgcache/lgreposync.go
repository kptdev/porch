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

func (r *lgRepository) syncOnce(ctx context.Context) error {
	remote, err := r.ensureRemote(ctx)
	if err != nil {
		return err
	}
	if err := remote.Refresh(ctx); err != nil {
		return fmt.Errorf("lgcache: remote refresh %s: %w", r.repoKey, err)
	}
	if err := r.local.FetchForSync(ctx); err != nil {
		return fmt.Errorf("lgcache: local sync fetch %s: %w", r.repoKey, err)
	}

	localList, err := r.local.ListPackageRevisionsShallow(ctx, cachedSyncFilter(r.pushDrafts))
	if err != nil {
		return err
	}
	localMap := prMap(localList)

	externalMap, err := r.externalPRMap(ctx, remote)
	if err != nil {
		return err
	}
	localOnly, both, externalOnly := partitionRevisionKeys(localMap, externalMap)
	klog.Infof("[LG Cache] sync %s: local-only=%d both=%d external-only=%d", r.repoKey, len(localOnly), len(both), len(externalOnly))

	if err := r.handleLocalOnly(ctx, localMap, localOnly); err != nil {
		return err
	}
	if len(externalOnly) > 0 {
		klog.Infof("[LG Cache] sync %s: %d external revisions imported into the local clone", r.repoKey, len(externalOnly))
	}
	if r.pushDrafts {
		r.handleBoth(ctx, localMap, externalMap, both)
	}
	return nil
}

func cachedSyncFilter(pushDrafts bool) repository.ListPackageRevisionFilter {
	if pushDrafts {
		return repository.ListPackageRevisionFilter{}
	}
	return repository.ListPackageRevisionFilter{
		Lifecycles: []porchapi.PackageRevisionLifecycle{
			porchapi.PackageRevisionLifecyclePublished,
			porchapi.PackageRevisionLifecycleDeletionProposed,
		},
	}
}

func (r *lgRepository) externalPRMap(ctx context.Context, remote git.GitRepository) (map[key.PackageRevisionKey]repository.PackageRevision, error) {
	version, err := remote.Version(ctx)
	if err != nil {
		return nil, err
	}
	if version != "" && version == r.lastExternalVersion && r.lastExternalPRs != nil {
		klog.Infof("[LG Cache] sync %s: external version %s unchanged", r.repoKey, version)
		return r.lastExternalPRs, nil
	}
	list, err := remote.ListPackageRevisionsShallow(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		return nil, err
	}
	mapped := prMap(list)
	r.lastExternalPRs = mapped
	r.lastExternalVersion = version
	meta := r.loadMeta()
	meta.LastSyncedRemoteVersion = version
	_ = r.saveMeta(meta)
	return mapped, nil
}

func (r *lgRepository) handleLocalOnly(ctx context.Context, localMap map[key.PackageRevisionKey]repository.PackageRevision, localOnly []key.PackageRevisionKey) error {
	for _, k := range localOnly {
		pr := localMap[k]
		life := pr.Lifecycle(ctx)
		unpushedDraft := (life == porchapi.PackageRevisionLifecycleDraft || life == porchapi.PackageRevisionLifecycleProposed) && !r.wasPushed(k)
		if unpushedDraft {
			if r.pushDrafts {
				if err := r.pushDraft(ctx, pr); err != nil {
					klog.Warningf("[LG Cache] draft push %s failed: %v", k, err)
				}
			}
			continue
		}
		if err := r.local.DeletePackageRevision(ctx, pr); err != nil {
			return fmt.Errorf("lgcache: delete local-only %s: %w", k, err)
		}
		r.unmarkPushed(k)
	}
	return nil
}

func (r *lgRepository) handleBoth(ctx context.Context, localMap, externalMap map[key.PackageRevisionKey]repository.PackageRevision, both []key.PackageRevisionKey) {
	for _, k := range both {
		localPR := localMap[k]
		life := localPR.Lifecycle(ctx)
		if life != porchapi.PackageRevisionLifecycleDraft && life != porchapi.PackageRevisionLifecycleProposed {
			continue
		}
		externalPR := externalMap[k]
		if externalPR != nil && externalPR.ResourceVersion() == localPR.ResourceVersion() {
			continue
		}
		if err := r.pushDraft(ctx, localPR); err != nil {
			klog.Warningf("[LG Cache] reconcile push %s failed: %v", k, err)
		}
	}
}

func (r *lgRepository) pushDraft(ctx context.Context, pr repository.PackageRevision) error {
	remote, err := r.ensureRemote(ctx)
	if err != nil {
		return err
	}
	if err := pushToExternal(ctx, remote, pr, false); err != nil {
		return err
	}
	r.markPushed(pr.Key())
	return nil
}
