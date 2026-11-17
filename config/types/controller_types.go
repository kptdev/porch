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

package types

import "github.com/kptdev/porch/config/types/datasize"

// Controllers mirrors the flags and environment variables of the porch-controllers binary.
// packagevariants and packagevariantsets embed an empty Options struct and have no fields here.
type Controllers struct {
	// CertDir is the --cert-dir flag.
	CertDir string `default:"/etc/webhook/certs" usage:"directory containing TLS certs for the webhook server"`
	// Reconcilers is the --reconcilers flag: a comma-separated list of names, or "*".
	Reconcilers string `usage:"reconcilers that should be enabled; use * to mean 'enable all'"`
	// EnableRepositories is ENABLE_REPOSITORIES.
	EnableRepositories bool `flag:"-" env:"*"`
	// EnablePackageRevisions is ENABLE_PACKAGEREVISIONS.
	EnablePackageRevisions bool `flag:"-" env:"ENABLE_PACKAGEREVISIONS"`
	// EnablePackageVariants is ENABLE_PACKAGEVARIANTS.
	EnablePackageVariants bool `flag:"-" env:"ENABLE_PACKAGEVARIANTS"`
	// EnablePackageVariantSets is ENABLE_PACKAGEVARIANTSETS.
	EnablePackageVariantSets bool `flag:"-" env:"ENABLE_PACKAGEVARIANTSETS"`

	// flagPrefix keeps the repositories. flag names. The derived path would start with repository-.
	Repository RepositoryController `flagPrefix:"repositories"`
	// flagPrefix keeps the packagerevisions. flag names. The derived path would start with package-revision-.
	PackageRevision PackageRevisionController `flagPrefix:"packagerevisions"`
}

// RepositoryController is the configuration held by repository.RepositoryReconciler.
type RepositoryController struct {
	HealthCheckFrequency       Duration `default:"5m" usage:"Frequency of repository health checks"`
	FullSyncFrequency          Duration `default:"1h" usage:"Frequency of full repository sync"`
	MaxConcurrentReconciles    int      `default:"200" usage:"Maximum number of concurrent repository reconciles"`
	MaxConcurrentSyncs         int      `default:"100" usage:"Maximum number of concurrent sync operations"`
	SyncStaleTimeout           Duration `default:"20m" usage:"Timeout for considering a sync stale"`
	RepoOperationRetryAttempts int      `default:"3" usage:"Number of retry attempts for git operations"`

	// The derived leaf is create-v-1-alpha-2-rpkg.
	CreateV1Alpha2Rpkg bool `flag:"create-v1alpha2-rpkg" usage:"Create v1alpha2 PackageRevision resources during repository sync"`
	PushDraftsToGit    bool `usage:"Push draft and proposed branches to git when using DB cache"`

	// The derived leaf is go-git-repo-cache-size.
	GoGitRepoCacheSize int `flag:"gogit-repo-cache-size" default:"8" usage:"Size of the in-memory cache for git repositories when using gogit (in MiB)"`
	// 512KiB is the current flag default, 512 * datasize.KiB.
	// The derived leaf is go-git-cache-max-file-size.
	GoGitCacheMaxFileSize datasize.DataSize `flag:"gogit-cache-max-file-size" default:"512KiB" usage:"Maximum file size, for example 512KB or a number of bytes, that will be read into the in-memory cache for git repositories when using gogit; files larger than this will be streamed from disk"`

	CacheType string `default:"DB" usage:"Cache type (DB or CR)"`
	// TODO: This now does not have the default value of `/cache` so
	//       controllers/repositories/pkg/controllers/repository/cache.go:103 makes sense.
	//       Is that okay?
	CacheDirectory         string `env:"GIT_CACHE_DIR" usage:"Directory for git repository cache"`
	UseUserDefinedCaBundle bool   `usage:"Enable custom CA bundle support from secrets"`

	DB       DBCacheOptions     `json:",omitzero"`
	Database DatabaseConnection `json:",omitzero"`
}

// PackageRevisionController contains the configuration fields of packagerevision.PackageRevisionReconciler.
type PackageRevisionController struct {
	MaxConcurrentReconciles    int      `default:"50" usage:"Maximum number of concurrent PackageRevision reconciles"`
	MaxConcurrentRenders       int      `default:"20" usage:"Maximum number of concurrent renders (0 = unbounded)"`
	RenderRequeueDelay         Duration `default:"2s" usage:"Delay before requeuing when render concurrency limit is reached"`
	RepoOperationRetryAttempts int      `default:"3" usage:"Number of retry attempts for git operations"`
	// TransientErrorRetryDeadline bounds how long a transient failure (content cache,
	// lifecycle, render read) may keep requeuing before escalating to a terminal
	// Failed condition. Measured from when the PackageRevision first went Pending.
	TransientErrorRetryDeadline Duration `default:"1m" usage:"Time a transient failure may keep requeuing before escalating to a terminal failure"`
	// FunctionRunnerAddress is FUNCTION_RUNNER_ADDRESS for this controller.
	// Empty means builtin functions only. This is not the porch-server --function-runner flag.
	FunctionRunnerAddress string `env:"*" usage:"Address of the function runner. Empty means builtin functions only"`
	// PodEvaluatorAddress is POD_EVALUATOR_ADDRESS.
	PodEvaluatorAddress string `env:"*" usage:"Address of the pod evaluator"`
}
