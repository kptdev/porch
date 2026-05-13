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
	"fmt"
	"os"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/git"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
)

var _ repository.ExternalPackageFetcher = &externalPackageFetcher{}

// externalPackageFetcher implements ExternalPackageFetcher using git clone + credential resolution.
type externalPackageFetcher struct {
	credentialResolver         repository.CredentialResolver
	caBundleResolver           repository.CredentialResolver
	repoOperationRetryAttempts int
}

// NewExternalPackageFetcher creates an ExternalPackageFetcher with the given credential and CA bundle resolvers.
func NewExternalPackageFetcher(credentialResolver, caBundleResolver repository.CredentialResolver, repoOperationRetryAttempts int) repository.ExternalPackageFetcher {
	return &externalPackageFetcher{
		credentialResolver:         credentialResolver,
		caBundleResolver:           caBundleResolver,
		repoOperationRetryAttempts: repoOperationRetryAttempts,
	}
}

func (f *externalPackageFetcher) FetchExternalGitPackage(ctx context.Context, gitSpec *porchv1alpha2.GitPackage, namespace string) (map[string]string, kptfilev1.GitLock, error) {
	dir, err := os.MkdirTemp("", "clone-git-package-*")
	if err != nil {
		return nil, kptfilev1.GitLock{}, fmt.Errorf("cannot create temp directory: %w", err)
	}
	defer os.RemoveAll(dir)

	spec := configapi.GitRepository{
		Repo:      gitSpec.Repo,
		Directory: gitSpec.Directory,
		SecretRef: configapi.SecretRef{Name: gitSpec.SecretRef.Name},
	}

	repo, err := git.OpenRepository(ctx, gitSpec.Repo, namespace, &spec, false, dir, git.GitRepositoryOptions{
		ExternalRepoOptions: externalrepotypes.ExternalRepoOptions{
			CredentialResolver:         f.credentialResolver,
			CaBundleResolver:           f.caBundleResolver,
			UseUserDefinedCaBundle:     f.caBundleResolver != nil,
			RepoOperationRetryAttempts: f.repoOperationRetryAttempts,
		},
		MainBranchStrategy: git.SkipVerification,
	})
	if err != nil {
		return nil, kptfilev1.GitLock{}, fmt.Errorf("cannot clone git repository %s: %w", gitSpec.Repo, err)
	}

	revision, lock, err := repo.GetPackageRevision(ctx, gitSpec.Ref, gitSpec.Directory)
	if err != nil {
		return nil, kptfilev1.GitLock{}, fmt.Errorf("cannot find package %s@%s: %w", gitSpec.Directory, gitSpec.Ref, err)
	}

	resources, err := revision.GetResources(ctx)
	if err != nil {
		return nil, kptfilev1.GitLock{}, fmt.Errorf("cannot read package resources: %w", err)
	}

	return resources.Spec.Resources, lock, nil
}
