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

package repository

import (
	"context"
	"fmt"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
)

type PackageFetcher struct {
	RepoOpener        RepositoryOpener
	ReferenceResolver ReferenceResolver
}

func (p *PackageFetcher) FetchRevision(ctx context.Context, packageRevisionRef *porchapi.PackageRevisionRef, namespace string) (PackageRevision, error) {
	prKey, err := PkgRevK8sName2Key(namespace, packageRevisionRef.Name)
	if err != nil {
		return nil, err
	}

	var resolved configapi.Repository
	if err := p.ReferenceResolver.ResolveReference(ctx, namespace, prKey.RKey().Name, &resolved); err != nil {
		return nil, fmt.Errorf("cannot find repository %+v: %w", prKey.RKey(), err)
	}

	repo, err := p.RepoOpener.OpenRepository(ctx, &resolved)
	if err != nil {
		return nil, err
	}

	revisions, err := repo.ListPackageRevisions(ctx, ListPackageRevisionFilter{Key: prKey})
	if err != nil {
		return nil, err
	}

	if len(revisions) != 1 {
		return nil, fmt.Errorf("cannot find package revision %q", packageRevisionRef.Name)
	}

	return revisions[0], nil
}

func (p *PackageFetcher) FetchResources(ctx context.Context, packageRevisionRef *porchapi.PackageRevisionRef, namespace string) (*porchapi.PackageRevisionResources, error) {
	revision, err := p.FetchRevision(ctx, packageRevisionRef, namespace)
	if err != nil {
		return nil, err
	}

	resources, err := revision.GetResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot read contents of package %q: %w", packageRevisionRef.Name, err)
	}
	return resources, nil
}
