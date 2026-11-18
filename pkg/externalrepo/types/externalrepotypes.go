// Copyright 2025 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or ExternalRepoied.
// See the License for the specific language governing permissions and
// limitations under the License.

package externalrepotypes

import (
	"context"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	configtypes "github.com/kptdev/porch/config/types"
	"github.com/kptdev/porch/pkg/repository"
)

type ExternalRepoFactory interface {
	NewRepositoryImpl(ctx context.Context, repositorySpec *configapi.Repository, options ExternalRepoOptions) (repository.Repository, error)
	CheckRepositoryConnection(ctx context.Context, repositorySpec *configapi.Repository, options ExternalRepoOptions) error
	// ValidateRepositoryHistory verifies, on a full sync, that the remote still
	// contains the commit Porch last synced (status.gitCommitHash). It guards
	// repository rehoming against a URL pointing at a different/divergent repo.
	ValidateRepositoryHistory(ctx context.Context, repositorySpec *configapi.Repository, options ExternalRepoOptions) error
}

type ExternalRepoOptions struct {
	// TODO: LocalDirectory, UseUserDefinedCaBundle and RepoOperationRetryAttempts mirror the
	//       config. Share them with the config types once they are moved to the global config.
	LocalDirectory             string
	UseUserDefinedCaBundle     bool
	CredentialResolver         repository.CredentialResolver
	CaBundleResolver           repository.CredentialResolver
	UserInfoProvider           repository.UserInfoProvider
	RepoOperationRetryAttempts int
	configtypes.GitCacheOptions
}
