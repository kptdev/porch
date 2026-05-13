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

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
)

// Implementation of the repository.PackageRevision interface for testing.
type FakePackage struct {
	PkgKey           repository.PackageKey
	PackageRevisions []repository.PackageRevision
	Packages         []repository.Package
}

var _ repository.Package = &FakePackage{}

func (p *FakePackage) KubeObjectNamespace() string {
	return p.Key().K8SNS()
}

func (p *FakePackage) KubeObjectName() string {
	return p.PkgKey.K8SName()
}

func (p *FakePackage) Key() repository.PackageKey {
	return p.PkgKey
}

func (p *FakePackage) GetPackage(ctx context.Context) *porchapi.PorchPackage {
	return &porchapi.PorchPackage{}
}

func (p *FakePackage) GetLatestRevision(ctx context.Context) int {
	return 0
}
