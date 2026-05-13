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

package contentcache

import (
	"context"
	"time"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
)

// draftSlimWrapper adapts PackageRevisionDraft to PackageRevisionDraftSlim,
// converting string lifecycle to porchapi.PackageRevisionLifecycle.
type draftSlimWrapper struct {
	inner repository.PackageRevisionDraft
}

var _ repository.PackageRevisionDraftSlim = &draftSlimWrapper{}

func (d *draftSlimWrapper) Key() repository.PackageRevisionKey {
	return d.inner.Key()
}

func (d *draftSlimWrapper) UpdateResources(ctx context.Context, resources map[string]string, commitMsg string) error {
	prr := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: resources,
		},
	}
	task := &porchapi.Task{
		Type: porchapi.TaskTypeEdit,
	}
	return d.inner.UpdateResources(ctx, prr, task)
}

func (d *draftSlimWrapper) UpdateLifecycle(ctx context.Context, lifecycle string) error {
	return d.inner.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycle(lifecycle))
}

// packageContentWrapper adapts PackageRevision to PackageContent.
type packageContentWrapper struct {
	inner repository.PackageRevision
}

var _ repository.PackageContent = &packageContentWrapper{}

func (p *packageContentWrapper) Key() repository.PackageRevisionKey {
	return p.inner.Key()
}

func (p *packageContentWrapper) Lifecycle(ctx context.Context) string {
	return string(p.inner.Lifecycle(ctx))
}

func (p *packageContentWrapper) GetResourceContents(ctx context.Context) (map[string]string, error) {
	res, err := p.inner.GetResources(ctx)
	if err != nil {
		return nil, err
	}
	return res.Spec.Resources, nil
}

func (p *packageContentWrapper) GetKptfile(ctx context.Context) (kptfilev1.KptFile, error) {
	return p.inner.GetKptfile(ctx)
}

func (p *packageContentWrapper) GetUpstreamLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	return p.inner.GetUpstreamLock(ctx)
}

func (p *packageContentWrapper) GetLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	return p.inner.GetLock(ctx)
}

func (p *packageContentWrapper) GetCommitInfo() (time.Time, string) {
	return p.inner.GetCommitInfo()
}
