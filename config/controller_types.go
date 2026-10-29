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

package config

// Controllers mirrors the flags and environment variables of the porch-controllers binary.
// packagevariants and packagevariantsets embed an empty Options struct and have no fields here.
type Controllers struct {
	// CertDir is the --cert-dir flag.
	CertDir string
	// Reconcilers is the --reconcilers flag: a comma-separated list of names, or "*".
	Reconcilers string
	// EnableRepositories is ENABLE_REPOSITORIES.
	EnableRepositories bool
	// EnablePackageRevisions is ENABLE_PACKAGEREVISIONS.
	EnablePackageRevisions bool
	// EnablePackageVariants is ENABLE_PACKAGEVARIANTS.
	EnablePackageVariants bool
	// EnablePackageVariantSets is ENABLE_PACKAGEVARIANTSETS.
	EnablePackageVariantSets bool

	FunctionRuntime FunctionRuntime
	Repository      RepositoryController
	PackageRevision PackageRevisionController
}

// FunctionRuntime mirrors DEFAULT_IMAGE_PREFIX and FUNCTION_CACHE_DIR, read by
// setupFunctionConfigReconciler. The FunctionConfig reconciler has no configuration struct.
type FunctionRuntime struct {
	DefaultImagePrefix string
	FunctionCacheDir   string
}

// RepositoryController mirrors the configuration fields of repository.RepositoryReconciler.
// cacheType, cacheDirectory, and useUserDefinedCaBundle are unexported there.
// Client, Scheme, Cache, and the sync limiter are runtime state.
// GitCacheDir is GIT_CACHE_DIR, used when CacheDirectory is empty.
type RepositoryController struct {
	HealthCheckFrequency       Duration
	FullSyncFrequency          Duration
	MaxConcurrentReconciles    int
	MaxConcurrentSyncs         int
	SyncStaleTimeout           Duration
	RepoOperationRetryAttempts int

	CreateV1Alpha2Rpkg bool
	PushDraftsToGit    bool

	GoGitRepoCacheSize    int
	GoGitCacheMaxFileSize DataSize

	CacheType              string
	CacheDirectory         string
	UseUserDefinedCaBundle bool
	GitCacheDir            string

	DB       DBCacheOptions
	Database DatabaseConnection
}

// PackageRevisionController mirrors the configuration fields of
// packagerevision.PackageRevisionReconciler, plus the environment variables
// read in Init that are not fields on that struct.
// Client, Scheme, ContentCache, and the render limiter are runtime state.
type PackageRevisionController struct {
	MaxConcurrentReconciles     int
	MaxConcurrentRenders        int
	RenderRequeueDelay          Duration
	RepoOperationRetryAttempts  int
	MaxGRPCMessageSize          DataSize
	TransientErrorRetryDeadline Duration

	// FunctionRunnerAddress is FUNCTION_RUNNER_ADDRESS.
	FunctionRunnerAddress string
	// PodEvaluatorAddress is POD_EVALUATOR_ADDRESS.
	PodEvaluatorAddress string
	// DefaultImagePrefix is DEFAULT_IMAGE_PREFIX.
	DefaultImagePrefix string
}
