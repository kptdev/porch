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

package repository

import (
	"context"
	"time"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
)

// PackageContent provides version-neutral read access to package revision
// state and content.
type PackageContent interface {
	Key() PackageRevisionKey
	Lifecycle(ctx context.Context) string
	GetResourceContents(ctx context.Context) (map[string]string, error)
	GetKptfile(ctx context.Context) (kptfilev1.KptFile, error)
	GetUpstreamLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error)
	GetLock(ctx context.Context) (kptfilev1.Upstream, kptfilev1.Locator, error)
	GetCommitInfo() (commitTime time.Time, commitAuthor string)
}

// PackageRevisionDraftSlim is a version-neutral draft interface using plain
// strings instead of v1alpha1 types.
type PackageRevisionDraftSlim interface {
	Key() PackageRevisionKey
	UpdateResources(ctx context.Context, resources map[string]string, commitMsg string) error
	UpdateLifecycle(ctx context.Context, lifecycle string) error
}

// ContentCache provides the PR controller with version-neutral access to the
// shared cache. It encapsulates git-internal state lookup so the controller
// only passes plain strings.
type ContentCache interface {
	GetPackageContent(ctx context.Context, repoKey RepositoryKey, pkg, workspace string) (PackageContent, error)
	UpdateLifecycle(ctx context.Context, repoKey RepositoryKey, pkg, workspace, desired string) (PackageContent, error)
	CreateNewDraft(ctx context.Context, repoKey RepositoryKey, pkgName, workspace, lifecycle string) (PackageRevisionDraftSlim, error)
	CreateDraftFromExisting(ctx context.Context, repoKey RepositoryKey, pkgName, workspace string) (PackageRevisionDraftSlim, error)
	CloseDraft(ctx context.Context, repoKey RepositoryKey, draft PackageRevisionDraftSlim, version int) error
	DeletePackage(ctx context.Context, repoKey RepositoryKey, pkg, workspace string) error
}

// ExternalPackageFetcher fetches package content from sources outside the registered repo cache.
// Used by clone-from-git and potentially future external sources (e.g. DB, OCI).
type ExternalPackageFetcher interface {
	FetchExternalGitPackage(ctx context.Context, gitSpec *porchv1alpha2.GitPackage, namespace string) (map[string]string, kptfilev1.GitLock, error)
}
