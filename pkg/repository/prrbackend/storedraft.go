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
	"fmt"
	"strings"
	"sync"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/kptfile/kptfileutil"
	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/prrstore"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	"github.com/kptdev/porch/pkg/util/selector"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// storeDraftRevision is a PackageRevision for an unpushed Draft/Proposed revision that
// exists only in the PRRStore (push-drafts-to-git disabled). Git discovery cannot see
// it, so the repository lists it from the store-only marker.
//
// It models only what such a draft has: it is never published and has no revision number
// or git location, so no commit info or self lock. Resources and the Kptfile (with its
// upstream lock) are read from the store on demand, as gitPackageRevision reads its tree.
type storeDraftRevision struct {
	key       key.PackageRevisionKey
	lifecycle porchapi.PackageRevisionLifecycle
	store     prrstore.PRRReader

	mutex    sync.Mutex
	metadata metav1.ObjectMeta
}

var _ repository.PackageRevision = &storeDraftRevision{}

// newStoreDraftRevision builds a revision from a store-only row, whose key already
// carries the repository's own RepositoryKey.
func newStoreDraftRevision(rev prrstore.StoreOnlyRevision, store prrstore.PRRReader) *storeDraftRevision {
	return &storeDraftRevision{
		key:       rev.Key,
		lifecycle: porchapi.PackageRevisionLifecycle(rev.Lifecycle),
		store:     store,
	}
}

func (d *storeDraftRevision) KubeObjectName() string { return key.ComposePkgRevObjName(d.key) }

func (d *storeDraftRevision) KubeObjectNamespace() string { return d.key.RKey().Namespace }

func (d *storeDraftRevision) Key() key.PackageRevisionKey { return d.key }

func (d *storeDraftRevision) UID() types.UID {
	return util.GenerateUid("packagerevision:", d.KubeObjectNamespace(), d.KubeObjectName())
}

func (d *storeDraftRevision) Lifecycle(context.Context) porchapi.PackageRevisionLifecycle {
	return d.lifecycle
}

// UpdateLifecycle is unsupported: Draft and Proposed change through the draft/close cycle.
func (d *storeDraftRevision) UpdateLifecycle(context.Context, porchapi.PackageRevisionLifecycle) error {
	return fmt.Errorf("cannot update lifecycle of store-only package revision %s directly; use the draft cycle", d.key)
}

func (d *storeDraftRevision) GetPackageRevision(ctx context.Context) (*porchapi.PackageRevision, error) {
	return d.toPackageRevision(ctx), nil
}

// toPackageRevision builds the API object. Unreadable store content is not an error: the
// Kptfile-derived fields are simply left empty, so the revision can still be listed.
func (d *storeDraftRevision) toPackageRevision(ctx context.Context) *porchapi.PackageRevision {
	kf, _ := d.GetKptfile(ctx)
	_, upstreamLock, _ := d.GetUpstreamLock(ctx)

	d.mutex.Lock()
	created := d.metadata.CreationTimestamp
	d.mutex.Unlock()

	return &porchapi.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              d.KubeObjectName(),
			Namespace:         d.KubeObjectNamespace(),
			UID:               d.UID(),
			CreationTimestamp: created,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    d.key.PkgKey.ToPkgPathname(),
			RepositoryName: d.key.RKey().Name,
			Lifecycle:      d.lifecycle,
			ReadinessGates: repository.ToAPIReadinessGates(kf),
			WorkspaceName:  d.key.WorkspaceName,
			Revision:       d.key.Revision,
			PackageMetadata: &porchapi.PackageMetadata{
				Labels:      kf.Labels,
				Annotations: kf.Annotations,
			},
		},
		Status: porchapi.PackageRevisionStatus{
			UpstreamLock: repository.KptUpstreamLock2APIUpstreamLock(upstreamLock),
			Conditions:   repository.ToAPIConditions(kf),
		},
	}
}

func (d *storeDraftRevision) GetResources(ctx context.Context) (*porchapi.PackageRevisionResources, error) {
	resources, err := d.store.GetResources(ctx, d.key)
	if err != nil {
		return nil, fmt.Errorf("failed to load package resources: %w", err)
	}
	return d.makePackageRevisionResources(resources), nil
}

func (d *storeDraftRevision) GetFilteredResources(ctx context.Context, sel selector.PRRGet) (*porchapi.PackageRevisionResources, error) {
	resources, err := d.store.GetResources(ctx, d.key)
	if err != nil {
		return nil, fmt.Errorf("failed to load package resources: %w", err)
	}

	if !sel.IsAllFiles() {
		filtered := make(map[string]string, len(sel.FilePaths))
		for _, p := range sel.FilePaths {
			v, ok := resources[p]
			if !ok {
				// Same behaviour as the git tree: asking for a file that is not there is an error.
				return nil, fmt.Errorf("failed to load package resources: file %q not found in %s", p, d.key)
			}
			filtered[p] = v
		}
		resources = filtered
	}
	if sel.PathOnly {
		paths := make(map[string]string, len(resources))
		for p := range resources {
			paths[p] = ""
		}
		resources = paths
	}
	return d.makePackageRevisionResources(resources), nil
}

func (d *storeDraftRevision) makePackageRevisionResources(resources map[string]string) *porchapi.PackageRevisionResources {
	d.mutex.Lock()
	created := d.metadata.CreationTimestamp
	d.mutex.Unlock()

	return &porchapi.PackageRevisionResources{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevisionResources",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              d.KubeObjectName(),
			Namespace:         d.KubeObjectNamespace(),
			UID:               d.UID(),
			CreationTimestamp: created,
			OwnerReferences:   []metav1.OwnerReference{},
		},
		Spec: porchapi.PackageRevisionResourcesSpec{
			PackageName:    d.key.PkgKey.ToPkgPathname(),
			WorkspaceName:  d.key.WorkspaceName,
			Revision:       d.key.Revision,
			RepositoryName: d.key.RKey().Name,
			Resources:      resources,
		},
	}
}

func (d *storeDraftRevision) GetKptfile(ctx context.Context) (kptfilev1.KptFile, error) {
	resources, err := d.store.GetResources(ctx, d.key)
	if err != nil {
		return kptfilev1.KptFile{}, fmt.Errorf("error getting %s resource: %w", kptfilev1.KptFileName, err)
	}
	kf, err := kptfileutil.DecodeKptfile(strings.NewReader(resources[kptfilev1.KptFileName]))
	if err != nil {
		return kptfilev1.KptFile{}, fmt.Errorf("error decoding %s: %w", kptfilev1.KptFileName, err)
	}
	return *kf, nil
}

// GetUpstreamLock returns the Kptfile's upstream lock, as git does. A cloned draft must
// keep it, or the controller blanks status.upstreamLock.
func (d *storeDraftRevision) GetUpstreamLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	kf, err := d.GetKptfile(ctx)
	if err != nil {
		return kptfilev1.Upstream{}, kptfilev1.Locator{}, fmt.Errorf("cannot determine package lock; cannot retrieve resources: %w", err)
	}
	if kf.Upstream == nil || kf.UpstreamLock == nil || kf.Upstream.Git == nil {
		return kptfilev1.Upstream{}, kptfilev1.Locator{}, nil
	}
	return *kf.Upstream, *kf.UpstreamLock, nil
}

// GetLock returns an empty lock: the self lock is a git location, which an unpushed
// draft lacks until it is published.
func (d *storeDraftRevision) GetLock(context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	return kptfilev1.Upstream{}, kptfilev1.Locator{}, nil
}

// ResourceVersion returns "": there is no git commit. The store's own resourceVersion
// (for render compare-and-swap) is read from the store directly.
func (d *storeDraftRevision) ResourceVersion() string { return "" }

// ToMainPackageRevision returns nil: only a published revision has a main-branch placeholder.
func (d *storeDraftRevision) ToMainPackageRevision(context.Context) repository.PackageRevision {
	return nil
}

func (d *storeDraftRevision) GetMeta() metav1.ObjectMeta {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.metadata
}

func (d *storeDraftRevision) SetMeta(_ context.Context, metadata metav1.ObjectMeta) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.metadata = metadata
	return nil
}

func (d *storeDraftRevision) GetCommitInfo() (time.Time, string) { return time.Time{}, "" }

func (d *storeDraftRevision) IsLatestRevision() bool { return false }
