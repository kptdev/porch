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

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/porch/api/porch/key"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
)

// PackageContent provides version-neutral read access to package revision
// state and content.
type PackageContent interface {
	Key() key.PackageRevisionKey
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
	Key() key.PackageRevisionKey
	UpdateResources(ctx context.Context, resources map[string]string, commitMsg string) error
	UpdateLifecycle(ctx context.Context, lifecycle string) error
}

// PackageContentAccess gives the PR controller version-neutral access to package
// content and lifecycle, hiding git-internal state so the controller passes plain strings.
type PackageContentAccess interface {
	GetPackageContent(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace string) (PackageContent, error)
	// GetMainPackageContent returns the "main" branch placeholder content (Revision -1)
	// derived from a published revision, letting the controller manage the main CR
	// on publish without depending on repository sync.
	GetMainPackageContent(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace string) (PackageContent, error)
	// UpdateLifecycle updates the package lifecycle. isUserCreated indicates whether
	// the package was explicitly created (has Spec.Source set) or was discovered from
	// the repository. Discovered packages skip git push to avoid corrupting shared
	// upstream repositories.
	UpdateLifecycle(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace, desired string, isUserCreated bool) (PackageContent, error)
	CreateNewDraft(ctx context.Context, repoKey key.RepositoryKey, pkgName, workspace, lifecycle string) (PackageRevisionDraftSlim, error)
	CreateDraftFromExisting(ctx context.Context, repoKey key.RepositoryKey, pkgName, workspace string) (PackageRevisionDraftSlim, error)
	CloseDraft(ctx context.Context, repoKey key.RepositoryKey, draft PackageRevisionDraftSlim, version int) error
	DeletePackage(ctx context.Context, repoKey key.RepositoryKey, pkg, workspace string) error
}

// ExternalPackageFetcher fetches package content from sources outside the registered repo cache.
// Used by clone-from-git and potentially future external sources (e.g. DB, OCI).
type ExternalPackageFetcher interface {
	FetchExternalGitPackage(ctx context.Context, gitSpec *porchv1alpha2.GitPackage, namespace string) (map[string]string, kptfilev1.GitLock, error)
}
