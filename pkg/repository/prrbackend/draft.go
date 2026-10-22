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

package prrbackend

import (
	"context"
	"sync"

	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// prrDraft wraps the git draft and records the lifecycle it is closed with, so
// ClosePackageRevisionDraft can tell an unpushed Draft/Proposed (kept only in the
// PRRStore) without a lookup. Every operation forwards to the inner draft.
type prrDraft struct {
	inner repository.PackageRevisionDraft

	mutex     sync.Mutex
	lifecycle porchapi.PackageRevisionLifecycle

	// staged and stagedVersion record the PRRStore version the draft's content was
	// copied from. The close write-back is conditional on it, so content pushed since is
	// kept rather than overwritten.
	staged        bool
	stagedVersion string
}

var _ repository.PackageRevisionDraft = &prrDraft{}

// newPRRDraft wraps inner. initial is the lifecycle of the revision it was opened from,
// or Draft for a new package.
func newPRRDraft(inner repository.PackageRevisionDraft, initial porchapi.PackageRevisionLifecycle) *prrDraft {
	return &prrDraft{inner: inner, lifecycle: initial}
}

func (d *prrDraft) Key() key.PackageRevisionKey { return d.inner.Key() }

func (d *prrDraft) GetMeta() metav1.ObjectMeta { return d.inner.GetMeta() }

func (d *prrDraft) UpdateResources(ctx context.Context, res *porchapi.PackageRevisionResources, task *porchapi.Task) error {
	return d.inner.UpdateResources(ctx, res, task)
}

func (d *prrDraft) UpdateLifecycle(ctx context.Context, lifecycle porchapi.PackageRevisionLifecycle) error {
	if err := d.inner.UpdateLifecycle(ctx, lifecycle); err != nil {
		return err
	}
	d.mutex.Lock()
	d.lifecycle = lifecycle
	d.mutex.Unlock()
	return nil
}

// markStaged records that the draft's content was taken from the PRRStore at version.
func (d *prrDraft) markStaged(version string) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.staged, d.stagedVersion = true, version
}

// stagedBase returns the PRRStore version the draft was staged from, and whether it was
// staged at all.
func (d *prrDraft) stagedBase() (string, bool) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.stagedVersion, d.staged
}

// unwrapPRRDraft returns the inner draft and the recorded lifecycle. A draft of another
// type is returned as is with an empty lifecycle, so it is never marked store-only.
func unwrapPRRDraft(prd repository.PackageRevisionDraft) (repository.PackageRevisionDraft, porchapi.PackageRevisionLifecycle) {
	if w, ok := prd.(*prrDraft); ok {
		w.mutex.Lock()
		defer w.mutex.Unlock()
		return w.inner, w.lifecycle
	}
	return prd, ""
}
