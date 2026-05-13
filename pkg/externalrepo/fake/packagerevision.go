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

package fake

import (
	"context"
	"time"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Implementation of the repository.PackageRevision interface for testing.
type FakePackageRevision struct {
	PrKey            repository.PackageRevisionKey
	Meta             *metav1.ObjectMeta
	Uid              types.UID
	PackageLifecycle porchapi.PackageRevisionLifecycle
	PackageRevision  *porchapi.PackageRevision
	Resources        *porchapi.PackageRevisionResources
	Kptfile          kptfilev1.KptFile
	Ops              []string
	Err              error
}

var _ repository.PackageRevision = &FakePackageRevision{}
var _ repository.PackageRevisionDraft = &FakePackageRevision{}

func (fpr *FakePackageRevision) KubeObjectName() string {
	fpr.Ops = append(fpr.Ops, "KubeObjectName")
	return repository.ComposePkgRevObjName(fpr.Key())
}

func (fpr *FakePackageRevision) KubeObjectNamespace() string {
	fpr.Ops = append(fpr.Ops, "KubeObjectNamespace")
	return fpr.Key().RKey().Namespace
}

func (fpr *FakePackageRevision) UID() types.UID {
	fpr.Ops = append(fpr.Ops, "UID")
	return util.GenerateUid("packagerevision:", fpr.KubeObjectNamespace(), fpr.KubeObjectName())
}

var _ repository.PackageRevision = &FakePackageRevision{}

// ToMainPackageRevision implements repository.PackageRevision.
func (fpr *FakePackageRevision) ToMainPackageRevision(context.Context) repository.PackageRevision {
	panic("unimplemented")
}

func (fpr *FakePackageRevision) ResourceVersion() string {
	fpr.Ops = append(fpr.Ops, "ResourceVersion")
	return fpr.PackageRevision.ResourceVersion
}

func (fpr *FakePackageRevision) Key() repository.PackageRevisionKey {
	fpr.Ops = append(fpr.Ops, "Key")
	return fpr.PrKey
}

func (fpr *FakePackageRevision) Lifecycle(ctx context.Context) porchapi.PackageRevisionLifecycle {
	fpr.Ops = append(fpr.Ops, "Lifecycle")
	return fpr.PackageLifecycle
}

func (fpr *FakePackageRevision) GetPackageRevision(context.Context) (*porchapi.PackageRevision, error) {
	fpr.Ops = append(fpr.Ops, "GetPackageRevision")
	return fpr.PackageRevision, fpr.Err
}

func (fpr *FakePackageRevision) GetResources(context.Context) (*porchapi.PackageRevisionResources, error) {
	fpr.Ops = append(fpr.Ops, "GetResources")
	return fpr.Resources, fpr.Err
}

func (fpr *FakePackageRevision) GetKptfile(ctx context.Context) (kptfilev1.KptFile, error) {
	fpr.Ops = append(fpr.Ops, "GetKptfile")
	return fpr.Kptfile, fpr.Err
}

func (fpr *FakePackageRevision) GetUpstreamLock(context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	fpr.Ops = append(fpr.Ops, "GetUpstreamLock")
	return *fpr.Kptfile.Upstream, *fpr.Kptfile.UpstreamLock, fpr.Err
}

func (fpr *FakePackageRevision) GetLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error) {
	fpr.Ops = append(fpr.Ops, "GetLock")
	return *fpr.Kptfile.Upstream, *fpr.Kptfile.UpstreamLock, fpr.Err
}

func (fpr *FakePackageRevision) UpdateLifecycle(_ context.Context, lifecycle porchapi.PackageRevisionLifecycle) error {
	fpr.Ops = append(fpr.Ops, "UpdateLifecycle")
	fpr.PackageLifecycle = lifecycle
	if fpr.PackageRevision != nil {
		fpr.PackageRevision.Spec.Lifecycle = lifecycle
	}
	return fpr.Err
}

func (fpr *FakePackageRevision) GetMeta() metav1.ObjectMeta {
	fpr.Ops = append(fpr.Ops, "GetMeta")
	if fpr.Meta != nil {
		return *fpr.Meta
	}

	return metav1.ObjectMeta{}
}

func (fpr *FakePackageRevision) IsLatestRevision() bool {
	return true
}

func (fpr *FakePackageRevision) GetCommitInfo() (time.Time, string) {
	return time.Time{}, ""
}

func (fpr *FakePackageRevision) SetMeta(context.Context, metav1.ObjectMeta) error {
	fpr.Ops = append(fpr.Ops, "SetMeta")
	return fpr.Err
}

func (fpr *FakePackageRevision) UpdateResources(ctx context.Context, new *porchapi.PackageRevisionResources, change *porchapi.Task) error {
	fpr.Ops = append(fpr.Ops, "UpdateResources")
	fpr.Resources = new
	return fpr.Err
}
