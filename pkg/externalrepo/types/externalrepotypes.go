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
	"crypto/sha256"
	"encoding/hex"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
)

// AnnotationKeyLastSyncedGitRepoHash stores the fingerprint of the last-synced Git URL.
const AnnotationKeyLastSyncedGitRepoHash = "config.porch.kpt.dev/last-synced-git-repo-hash"

// GitRepoURLHash returns a non-reversible fingerprint suitable for object metadata.
func GitRepoURLHash(repoURL string) string {
	hash := sha256.Sum256([]byte(repoURL))
	return hex.EncodeToString(hash[:])
}

type ExternalRepoFactory interface {
	NewRepositoryImpl(ctx context.Context, repositorySpec *configapi.Repository, options ExternalRepoOptions) (repository.Repository, error)
	CheckRepositoryConnection(ctx context.Context, repositorySpec *configapi.Repository, options ExternalRepoOptions) error
}

type ExternalRepoOptions struct {
	LocalDirectory             string
	UseUserDefinedCaBundle     bool
	CredentialResolver         repository.CredentialResolver
	CaBundleResolver           repository.CredentialResolver
	UserInfoProvider           repository.UserInfoProvider
	RepoOperationRetryAttempts int
	GoGitRepoCacheSize         int
	GoGitCacheMaxFileSize      int64
}
