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
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util/selector"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type lgPackageRevision struct {
	repo  *lgRepository
	gitPR repository.PackageRevision
	crd   *porchv1alpha2.PackageRevision
}

var _ repository.PackageRevision = &lgPackageRevision{}

func (p *lgPackageRevision) KubeObjectNamespace() string {
	if p.crd != nil {
		return p.crd.Namespace
	}
	return p.gitPR.KubeObjectNamespace()
}

func (p *lgPackageRevision) KubeObjectName() string {
	if p.crd != nil && p.crd.Name != "" {
		return p.crd.Name
	}
	return p.gitPR.KubeObjectName()
}

func (p *lgPackageRevision) Key() key.PackageRevisionKey {
	if p.gitPR != nil {
		k := p.gitPR.Key()
		if k.Revision == -1 {
			return k
		}
		if p.crd != nil && p.crd.Status.Revision != 0 {
			k.Revision = p.crd.Status.Revision
		}
		return k
	}
	parsed, err := key.PkgRevK8sName2Key(p.crd.Namespace, p.crd.Name)
	if err != nil {
		return key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: p.repo.repoKey,
				Package: p.crd.Spec.PackageName,
			},
			WorkspaceName: p.crd.Spec.WorkspaceName,
			Revision:      p.crd.Status.Revision,
		}
	}
	parsed.Revision = p.crd.Status.Revision
	return parsed
}

func (p *lgPackageRevision) UID() types.UID {
	if p.crd != nil && p.crd.UID != "" {
		return p.crd.UID
	}
	if p.gitPR != nil {
		return p.gitPR.UID()
	}
	return ""
}

func (p *lgPackageRevision) Lifecycle(ctx context.Context) porchapi.PackageRevisionLifecycle {
	if p.gitPR != nil {
		return p.gitPR.Lifecycle(ctx)
	}
	if p.crd != nil && p.crd.Spec.Lifecycle != "" {
		return porchapi.PackageRevisionLifecycle(p.crd.Spec.Lifecycle)
	}
	return ""
}

func (p *lgPackageRevision) UpdateLifecycle(ctx context.Context, lifecycle porchapi.PackageRevisionLifecycle) error {
	current := p.Lifecycle(ctx)
	if current == porchapi.PackageRevisionLifecycleProposed && lifecycle == porchapi.PackageRevisionLifecyclePublished {
		return p.repo.publish(ctx, p)
	}
	if p.gitPR != nil {
		if err := p.gitPR.UpdateLifecycle(ctx, lifecycle); err != nil {
			return err
		}
	}
	return p.repo.writeLifecycle(ctx, p, lifecycle)
}

func (p *lgPackageRevision) GetPackageRevision(ctx context.Context) (*porchapi.PackageRevision, error) {
	var apiPR *porchapi.PackageRevision
	var err error
	if p.gitPR != nil {
		apiPR, err = p.gitPR.GetPackageRevision(ctx)
		if err != nil {
			return nil, err
		}
	} else if p.crd != nil {
		apiPR = &porchapi.PackageRevision{ObjectMeta: p.crd.ObjectMeta}
	} else {
		return nil, fmt.Errorf("package revision not found")
	}
	if p.crd == nil {
		return apiPR, nil
	}
	apiPR.ObjectMeta = p.crd.ObjectMeta
	apiPR.Spec.Lifecycle = porchapi.PackageRevisionLifecycle(p.crd.Spec.Lifecycle)
	apiPR.Spec.PackageName = p.crd.Spec.PackageName
	apiPR.Spec.RepositoryName = p.crd.Spec.RepositoryName
	apiPR.Spec.WorkspaceName = p.crd.Spec.WorkspaceName
	if p.crd.Status.Revision != 0 {
		apiPR.Spec.Revision = p.crd.Status.Revision
	}
	if p.crd.Spec.PackageMetadata != nil {
		apiPR.Spec.PackageMetadata = &porchapi.PackageMetadata{
			Labels:      p.crd.Spec.PackageMetadata.Labels,
			Annotations: p.crd.Spec.PackageMetadata.Annotations,
		}
	}
	if len(p.crd.Spec.ReadinessGates) > 0 {
		gates := make([]porchapi.ReadinessGate, len(p.crd.Spec.ReadinessGates))
		for i, gate := range p.crd.Spec.ReadinessGates {
			gates[i] = porchapi.ReadinessGate{ConditionType: gate.ConditionType}
		}
		apiPR.Spec.ReadinessGates = gates
	}
	if p.crd.Status.PublishedBy != "" {
		apiPR.Status.PublishedBy = p.crd.Status.PublishedBy
	}
	if p.crd.Status.PublishedAt != nil {
		apiPR.Status.PublishedAt = *p.crd.Status.PublishedAt
	}
	apiPR.Status.Deployment = p.crd.Status.Deployment
	apiPR.Status.ResourcesSizeBytes = p.crd.Status.ResourcesSizeBytes
	return apiPR, nil
}

func (p *lgPackageRevision) GetResources(ctx context.Context) (*porchapi.PackageRevisionResources, error) {
	if p.gitPR == nil {
		return &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{}},
		}, nil
	}
	return p.gitPR.GetResources(ctx)
}

func (p *lgPackageRevision) GetFilteredResources(ctx context.Context, resourceSelector selector.PRRGet) (*porchapi.PackageRevisionResources, error) {
	if p.gitPR == nil {
		return p.GetResources(ctx)
	}
	return p.gitPR.GetFilteredResources(ctx, resourceSelector)
}

func (p *lgPackageRevision) GetUpstreamLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	if p.gitPR == nil {
		return kptfilev1.Upstream{}, kptfilev1.Locator{}, fmt.Errorf("package revision %s has no local resources", p.KubeObjectName())
	}
	return p.gitPR.GetUpstreamLock(ctx)
}

func (p *lgPackageRevision) GetKptfile(ctx context.Context) (kptfilev1.KptFile, error) {
	if p.gitPR == nil {
		return kptfilev1.KptFile{}, fmt.Errorf("package revision %s has no local resources", p.KubeObjectName())
	}
	return p.gitPR.GetKptfile(ctx)
}

func (p *lgPackageRevision) GetLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	if p.gitPR == nil {
		return kptfilev1.Upstream{}, kptfilev1.Locator{}, fmt.Errorf("package revision %s has no local resources", p.KubeObjectName())
	}
	return p.gitPR.GetLock(ctx)
}

func (p *lgPackageRevision) ResourceVersion() string {
	if p.crd != nil && p.crd.ResourceVersion != "" {
		return p.crd.ResourceVersion
	}
	if p.gitPR != nil {
		return p.gitPR.ResourceVersion()
	}
	return ""
}

func (p *lgPackageRevision) ToMainPackageRevision(ctx context.Context) repository.PackageRevision {
	if p.gitPR == nil {
		return p
	}
	return &lgPackageRevision{repo: p.repo, gitPR: p.gitPR.ToMainPackageRevision(ctx)}
}

func (p *lgPackageRevision) GetMeta() metav1.ObjectMeta {
	if p.crd != nil {
		return p.crd.ObjectMeta
	}
	if p.gitPR != nil {
		return p.gitPR.GetMeta()
	}
	return metav1.ObjectMeta{}
}

func (p *lgPackageRevision) SetMeta(ctx context.Context, meta metav1.ObjectMeta) error {
	if p.crd != nil && p.repo.meta != nil {
		updated := p.crd.DeepCopy()
		updated.Labels = meta.Labels
		updated.Annotations = meta.Annotations
		updated.Finalizers = meta.Finalizers
		updated.OwnerReferences = meta.OwnerReferences
		if err := p.repo.meta.Update(ctx, updated); err != nil {
			return err
		}
		p.crd = updated
	}
	if p.gitPR != nil {
		return p.gitPR.SetMeta(ctx, meta)
	}
	return nil
}

func (p *lgPackageRevision) GetCommitInfo() (time.Time, string) {
	if p.gitPR != nil {
		return p.gitPR.GetCommitInfo()
	}
	if p.crd != nil && p.crd.Status.PublishedAt != nil {
		return p.crd.Status.PublishedAt.Time, p.crd.Status.PublishedBy
	}
	return time.Time{}, ""
}

func (p *lgPackageRevision) IsLatestRevision() bool {
	if p.crd != nil && p.crd.Labels != nil {
		return p.crd.Labels[porchv1alpha2.LatestPackageRevisionKey] == porchv1alpha2.LatestPackageRevisionValue
	}
	if p.gitPR != nil {
		return p.gitPR.IsLatestRevision()
	}
	return false
}

type lgPackage struct {
	key    key.PackageKey
	latest int
}

var _ repository.Package = &lgPackage{}

func (p *lgPackage) KubeObjectNamespace() string { return p.key.RKey().Namespace }
func (p *lgPackage) KubeObjectName() string      { return p.key.K8SName() }
func (p *lgPackage) Key() key.PackageKey         { return p.key }

func (p *lgPackage) GetPackage(context.Context) *porchapi.PorchPackage {
	return &porchapi.PorchPackage{
		TypeMeta: metav1.TypeMeta{Kind: "Package", APIVersion: porchapi.SchemeGroupVersion.Identifier()},
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.KubeObjectName(),
			Namespace: p.KubeObjectNamespace(),
		},
		Spec: porchapi.PackageSpec{
			PackageName:    p.key.Package,
			RepositoryName: p.key.RKey().Name,
		},
	}
}

func (p *lgPackage) GetLatestRevision(context.Context) int { return p.latest }
