// Copyright 2022, 2024-2025 The kpt Authors
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

package git

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/kptfile/kptfileutil"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type gitPackageRevision struct {
	prKey     repository.PackageRevisionKey
	repo      *gitRepository // repo is repo containing the package
	updated   time.Time
	updatedBy string
	ref       *plumbing.Reference // ref is the Git reference at which the package exists
	tree      plumbing.Hash       // Cached tree of the package itself, some descendent of commit.Tree()
	commit    plumbing.Hash       // Current version of the package (commit sha)
	tasks     []porchapi.Task
	metadata  metav1.ObjectMeta
	mutex     sync.Mutex
}

var _ repository.PackageRevision = &gitPackageRevision{}

func (c *gitPackageRevision) KubeObjectName() string {
	return repository.ComposePkgRevObjName(c.Key())
}

func (c *gitPackageRevision) KubeObjectNamespace() string {
	return c.Key().RKey().Namespace
}

func (c *gitPackageRevision) UID() types.UID {
	return util.GenerateUid("packagerevision:", c.KubeObjectNamespace(), c.KubeObjectName())
}

func (p *gitPackageRevision) ResourceVersion() string {
	return p.commit.String()
}

func (p *gitPackageRevision) Key() repository.PackageRevisionKey {
	return p.prKey
}

func (p *gitPackageRevision) GetPackageRevision(ctx context.Context) (*porchapi.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "gitPackageRevision::GetPackageRevision", trace.WithAttributes())
	defer span.End()

	key := p.Key()

	_, upstreamLock, _ := p.GetUpstreamLock(ctx)
	_, selfLock, _ := p.GetLock(ctx)
	kf, _ := p.GetKptfile(ctx)

	status := porchapi.PackageRevisionStatus{
		UpstreamLock: repository.KptUpstreamLock2APIUpstreamLock(upstreamLock),
		SelfLock:     repository.KptUpstreamLock2APIUpstreamLock(selfLock),
		Deployment:   p.repo.deployment,
		Conditions:   repository.ToAPIConditions(kf),
	}

	lifecycle := p.Lifecycle(ctx)
	if porchapi.LifecycleIsPublished(lifecycle) {
		if !p.updated.IsZero() {
			status.PublishedAt = metav1.Time{Time: p.updated}
		}
		if p.updatedBy != "" {
			status.PublishedBy = p.updatedBy
		}
	}
	p.mutex.Lock()
	pr := &porchapi.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            p.KubeObjectName(),
			Namespace:       p.KubeObjectNamespace(),
			UID:             p.UID(),
			ResourceVersion: p.commit.String(),
			CreationTimestamp: metav1.Time{
				Time: p.metadata.CreationTimestamp.Time,
			},
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    key.PkgKey.ToPkgPathname(),
			RepositoryName: key.RKey().Name,
			Lifecycle:      lifecycle,
			Tasks:          p.tasks,
			ReadinessGates: repository.ToAPIReadinessGates(kf),
			WorkspaceName:  key.WorkspaceName,
			Revision:       key.Revision,
			PackageMetadata: &porchapi.PackageMetadata{
				Labels:      kf.Labels,
				Annotations: kf.Annotations,
			},
		},
		Status: status,
	}
	p.mutex.Unlock()
	return pr, nil
}

func (p *gitPackageRevision) GetResources(context.Context) (*porchapi.PackageRevisionResources, error) {
	resources, err := p.repo.getResources(p.tree)
	if err != nil {
		return nil, fmt.Errorf("failed to load package resources: %w", err)
	}

	p.mutex.Lock()
	prRes := &porchapi.PackageRevisionResources{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevisionResources",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            p.KubeObjectName(),
			Namespace:       p.KubeObjectNamespace(),
			UID:             p.UID(),
			ResourceVersion: p.commit.String(),
			CreationTimestamp: metav1.Time{
				Time: p.metadata.CreationTimestamp.Time,
			},
			OwnerReferences: []metav1.OwnerReference{}, // TODO: should point to repository resource
		},
		Spec: porchapi.PackageRevisionResourcesSpec{
			PackageName:    p.Key().PkgKey.ToPkgPathname(),
			WorkspaceName:  p.Key().WorkspaceName,
			Revision:       p.Key().Revision,
			RepositoryName: p.Key().RKey().Name,

			Resources: resources,
		},
	}
	p.mutex.Unlock()
	return prRes, nil
}

// Creates a gitPackageRevision reference that is acting as the main branch package revision.
// It doesn't do any git operations, so this package should only be used if the actual git updates
// were executed on the main branch.
func (p *gitPackageRevision) ToMainPackageRevision(context.Context) repository.PackageRevision {
	//Need to compute a separate reference, otherwise the ref will be the same as the versioned package,
	//while the main gitPackageRevision needs to point at the main branch.

	mainBranchRef := plumbing.NewHashReference(p.repo.branch.refInLocal(), p.commit)
	mainPr := &gitPackageRevision{
		repo:      p.repo,
		prKey:     p.prKey,
		updated:   p.updated,
		updatedBy: p.updatedBy,
		ref:       mainBranchRef,
		tree:      p.tree,
		commit:    p.commit,
		tasks:     p.tasks,
	}
	mainPr.prKey.Revision = -1
	mainPr.prKey.WorkspaceName = mainPr.Key().RKey().PlaceholderWSname

	return mainPr
}

func (p *gitPackageRevision) GetKptfile(context.Context) (kptfilev1.KptFile, error) {
	kfString, err := p.repo.getResource(p.tree, kptfilev1.KptFileName)
	if err != nil {
		return kptfilev1.KptFile{}, pkgerrors.Wrapf(err, "error getting %s resource", kptfilev1.KptFileName)
	}
	kf, err := kptfileutil.DecodeKptfile(strings.NewReader(kfString))
	if err != nil {
		return kptfilev1.KptFile{}, pkgerrors.Wrapf(err, "error decoding %s", kptfilev1.KptFileName)
	}
	return *kf, nil
}

// GetUpstreamLock returns the upstreamLock info present in the Kptfile of the package.
func (p *gitPackageRevision) GetUpstreamLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	kf, err := p.GetKptfile(ctx)
	if err != nil {
		return kptfilev1.Upstream{}, kptfilev1.Locator{}, fmt.Errorf("cannot determine package lock; cannot retrieve resources: %w", err)
	}

	if kf.Upstream == nil || kf.UpstreamLock == nil || kf.Upstream.Git == nil {
		// the package doesn't have any upstream package.
		return kptfilev1.Upstream{}, kptfilev1.Locator{}, nil
	}
	return *kf.Upstream, *kf.UpstreamLock, nil
}

// GetLock returns the self version of the package. Think of it as the Git commit information
// that represent the package revision of this package. Please note that it uses Upstream types
// to represent this information but it has no connection with the associated upstream package (if any).
func (p *gitPackageRevision) GetLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	_, span := tracer.Start(ctx, "gitPackageRevision::GetLock", trace.WithAttributes())
	defer span.End()

	repo, err := p.repo.GetRepo()
	if err != nil {
		return kptfilev1.Upstream{}, kptfilev1.Locator{}, fmt.Errorf("cannot determine package lock: %w", err)
	}

	if p.ref == nil {
		return kptfilev1.Upstream{}, kptfilev1.Locator{}, fmt.Errorf("cannot determine package lock; package has no ref")
	}

	ref, err := refInRemoteFromRefInLocal(p.ref.Name())
	if err != nil {
		return kptfilev1.Upstream{}, kptfilev1.Locator{}, fmt.Errorf("cannot determine package lock for %q: %v", p.ref, err)
	}

	return kptfilev1.Upstream{
			Type: kptfilev1.GitOrigin,
			Git: &kptfilev1.Git{
				Repo:      repo,
				Directory: p.prKey.PkgKey.ToPkgPathname(),
				Ref:       ref.Short(),
			},
		}, kptfilev1.Locator{
			Type: kptfilev1.GitOrigin,
			Git: &kptfilev1.GitLock{
				Repo:      repo,
				Directory: p.prKey.PkgKey.ToPkgPathname(),
				Ref:       ref.Short(),
				Commit:    p.commit.String(),
			},
		}, nil
}

func (p *gitPackageRevision) GetCommitInfo() (time.Time, string) {
	return p.updated, p.updatedBy
}

func (p *gitPackageRevision) IsLatestRevision() bool {
	return false
}

func (p *gitPackageRevision) Lifecycle(ctx context.Context) porchapi.PackageRevisionLifecycle {
	return p.repo.GetLifecycle(ctx, p)
}

func (p *gitPackageRevision) UpdateLifecycle(ctx context.Context, new porchapi.PackageRevisionLifecycle) error {
	ctx, span := tracer.Start(ctx, "gitPackageRevision::UpdateLifecycle", trace.WithAttributes())
	defer span.End()

	return p.repo.UpdateLifecycle(ctx, p, new)
}

func (p *gitPackageRevision) GetMeta() metav1.ObjectMeta {
	p.mutex.Lock()
	metadata := p.metadata
	p.mutex.Unlock()
	return metadata
}

func (p *gitPackageRevision) SetMeta(_ context.Context, metadata metav1.ObjectMeta) error {
	p.mutex.Lock()
	p.metadata = metadata
	p.mutex.Unlock()
	return nil
}
