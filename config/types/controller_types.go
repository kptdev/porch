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

// Controllers mirrors the flags and environment variables of the porch-controllers binary.
// packagevariants and packagevariantsets embed an empty Options struct and have no fields here.
type Controllers struct {
	// CertDir is the --cert-dir flag.
	CertDir string `default:"/etc/webhook/certs"`
	// Reconcilers is the --reconcilers flag: a comma-separated list of names, or "*".
	Reconcilers string
	// EnableRepositories is ENABLE_REPOSITORIES.
	EnableRepositories bool `env:"*"`
	// EnablePackageRevisions is ENABLE_PACKAGEREVISIONS.
	EnablePackageRevisions bool `env:"ENABLE_PACKAGEREVISIONS"`
	// EnablePackageVariants is ENABLE_PACKAGEVARIANTS.
	EnablePackageVariants bool `env:"ENABLE_PACKAGEVARIANTS"`
	// EnablePackageVariantSets is ENABLE_PACKAGEVARIANTSETS.
	EnablePackageVariantSets bool `env:"ENABLE_PACKAGEVARIANTSETS"`

	FunctionRuntime FunctionRuntime
	// flagPrefix keeps the repositories. flag names. The derived path would start with repository-.
	Repository RepositoryController `flagPrefix:"repositories"`
	// flagPrefix keeps the packagerevisions. flag names. The derived path would start with package-revision-.
	PackageRevision PackageRevisionController `flagPrefix:"packagerevisions"`
}

// FunctionRuntime mirrors DEFAULT_IMAGE_PREFIX and FUNCTION_CACHE_DIR, read by
// setupFunctionConfigReconciler. The FunctionConfig reconciler has no configuration struct.
type FunctionRuntime struct {
	DefaultImagePrefix string `env:"*" default:"ghcr.io/kptdev/krm-functions-catalog"`
	FunctionCacheDir   string `env:"*" default:"/home/nonroot/functions"`
}

// RepositoryController mirrors the configuration fields of repository.RepositoryReconciler.
// cacheType, cacheDirectory, and useUserDefinedCaBundle are unexported there.
// Client, Scheme, Cache, and the sync limiter are runtime state.
// GitCacheDir is GIT_CACHE_DIR, used when CacheDirectory is empty.
type RepositoryController struct {
	HealthCheckFrequency       Duration `default:"5m"`
	FullSyncFrequency          Duration `default:"1h"`
	MaxConcurrentReconciles    int      `default:"200"`
	MaxConcurrentSyncs         int      `default:"100"`
	SyncStaleTimeout           Duration `default:"20m"`
	RepoOperationRetryAttempts int      `default:"3"`

	// The derived leaf is create-v-1-alpha-2-rpkg.
	CreateV1Alpha2Rpkg bool `flag:"create-v1alpha2-rpkg"`
	PushDraftsToGit    bool

	// The derived leaf is go-git-repo-cache-size.
	GoGitRepoCacheSize int `flag:"gogit-repo-cache-size" default:"8"`
	// 512KiB is the current flag default, 512 * datasize.KB.
	// The derived leaf is go-git-cache-max-file-size.
	GoGitCacheMaxFileSize DataSize `flag:"gogit-cache-max-file-size" default:"512KiB"`

	CacheType              string `default:"DB"`
	CacheDirectory         string `default:"/cache"`
	UseUserDefinedCaBundle bool
	GitCacheDir            string `env:"*"`

	DB       DBCacheOptions
	Database DatabaseConnection
}

// PackageRevisionController mirrors the configuration fields of
// packagerevision.PackageRevisionReconciler, plus the environment variables
// read in Init that are not fields on that struct.
// Client, Scheme, ContentCache, and the render limiter are runtime state.
type PackageRevisionController struct {
	MaxConcurrentReconciles     int      `default:"50"`
	MaxConcurrentRenders        int      `default:"20"`
	RenderRequeueDelay          Duration `default:"2s"`
	RepoOperationRetryAttempts  int      `default:"3"`
	MaxGRPCMessageSize          DataSize `default:"6MiB"`
	TransientErrorRetryDeadline Duration `default:"1m"`

	// FunctionRunnerAddress is FUNCTION_RUNNER_ADDRESS.
	FunctionRunnerAddress string `env:"*"`
	// PodEvaluatorAddress is POD_EVALUATOR_ADDRESS.
	PodEvaluatorAddress string `env:"*"`
	// DefaultImagePrefix is DEFAULT_IMAGE_PREFIX.
	DefaultImagePrefix string `env:"*" default:"ghcr.io/kptdev/krm-functions-catalog"`
}
