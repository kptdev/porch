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

package content

import (
	"context"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/prrstore"
	"github.com/kptdev/porch/pkg/repository"
)

// draftSlimWrapper adapts PackageRevisionDraft to PackageRevisionDraftSlim,
// converting string lifecycle to porchapi.PackageRevisionLifecycle.
type draftSlimWrapper struct {
	inner repository.PackageRevisionDraft
}

var _ repository.PackageRevisionDraftSlim = &draftSlimWrapper{}

func (d *draftSlimWrapper) Key() key.PackageRevisionKey {
	return d.inner.Key()
}

func (d *draftSlimWrapper) UpdateResources(ctx context.Context, resources map[string]string, commitMsg string) error {
	prr := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: resources,
		},
	}
	task := &porchapi.Task{
		Type: taskTypeFor(commitMsg),
	}
	return d.inner.UpdateResources(ctx, prr, task)
}

// taskTypeFor maps the commit message the PackageRevision controller passes
// to UpdateResources to the v1alpha1 task type recorded with the commit.
// The creation sources map to the task v1alpha1 records for them. Every other
// write (render, metadata sync, subpackage operation) is recorded as a render
// task, as v1alpha1 does, so that it does not replace the creation task.
func taskTypeFor(commitMsg string) porchapi.TaskType {
	switch commitMsg {
	case "init":
		return porchapi.TaskTypeInit
	case "clone":
		return porchapi.TaskTypeClone
	case "copy":
		// v1alpha1 records a copy of an earlier revision as an edit task.
		return porchapi.TaskTypeEdit
	case "upgrade":
		return porchapi.TaskTypeUpgrade
	default:
		return porchapi.TaskTypeRender
	}
}

func (d *draftSlimWrapper) UpdateLifecycle(ctx context.Context, lifecycle string) error {
	return d.inner.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycle(lifecycle))
}

// packageContentWrapper adapts PackageRevision to PackageContent.
type packageContentWrapper struct {
	inner repository.PackageRevision
	// prrStore is the authoritative v1alpha2 resource store and is mandatory for
	// v1alpha2 (CRD) repositories: GetResourceContents reads from it exclusively so
	// content reflects PRR pushes/renders (the source of truth), never falling back
	// to the git-backed inner revision, which lags. It is nil only in v1alpha1 /
	// CR-cache mode, where reads come from git.
	prrStore prrstore.PRRReader
}

var _ repository.PackageContent = &packageContentWrapper{}

func (p *packageContentWrapper) Key() key.PackageRevisionKey {
	return p.inner.Key()
}

func (p *packageContentWrapper) Lifecycle(ctx context.Context) string {
	return string(p.inner.Lifecycle(ctx))
}

func (p *packageContentWrapper) GetResourceContents(ctx context.Context) (map[string]string, error) {
	// For v1alpha2 (CRD) the PRRStore is the authoritative content store and is
	// read exclusively — no fallback to git. A nil store is only the v1alpha1 /
	// CR-cache mode, where content comes from the git-backed revision.
	if p.prrStore != nil {
		return p.prrStore.GetResources(ctx, p.inner.Key())
	}
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
