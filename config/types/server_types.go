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

// Server mirrors the configuration fields of server.PorchServerOptions.
// RecommendedOptions, SharedInformerFactory, StdOut, and StdErr are runtime
// state and are not included. The generic apiserver flags porch-server sets
// are Serving.
type Server struct {
	CoreAPIKubeconfigPath string `flag:"kubeconfig" usage:"kubeconfig file pointing at the 'core' kubernetes server."`

	CacheDirectory string `usage:"Directory where Porch server stores repository and package caches."`
	CacheType      string `default:"CR" usage:"Type of cache to use for cacheing repos, supported types are \"CR\" (Custom Resource) and \"DB\" (DataBase)"`
	// The db-cache flags are not nested under db.
	DB       DBCacheOptions     `flagPrefix:"" json:",omitzero"`
	Database DatabaseConnection `json:",omitzero"`
	// DbPushDraftsToGit mirrors PorchServerOptions.DbPushDrafsToGit.
	DbPushDraftsToGit bool `usage:"If true, Porch will push draft package revisions to git when using the DB cache"`

	GoGitRepoCacheSize int `flag:"gogit-repo-cache-size" default:"8" usage:"Size of the in-memory cache for git repositories when using gogit (in MiB)"`
	// 512KiB is the current flag default, 512 * datasize.KB.
	GoGitCacheMaxFileSize datasize.DataSize `flag:"gogit-cache-max-file-size" default:"512KiB" usage:"Maximum file size, for example 512KB or a number of bytes, that will be read into the in-memory cache for git repositories when using gogit; files larger than this will be streamed from disk to avoid memory pressure"`

	// FunctionRunnerAddress is the function-runner dial address for this process.
	FunctionRunnerAddress    string `flag:"function-runner" usage:"Address of the function runner gRPC service."`
	LocalStandaloneDebugging bool   `flag:"standalone-debug-mode" usage:"Under the local-debug mode the apiserver will allow all access to its resources without authorizing the requests, this flag is only intended for debugging in your workstation."`

	// The list flags are not nested under lists.
	Lists                      CRCacheOptions `flagPrefix:""`
	RepoOperationRetryAttempts int            `default:"3" usage:"Number of retry attempts for repository operations."`
	RetryableGitErrors         []string       `usage:"Additional retryable git error patterns. Can be specified multiple times or as comma-separated values."`

	UseUserDefinedCaBundle bool `flag:"use-user-cabundle" usage:"Determine whether to use a user-defined CaBundle for TLS towards the repository system."`

	PodNamespace string `default:"porch-fn-system" usage:"Namespace get FunctionConfig objects for krm functions"`

	// The pod evaluator flags are registered on the server flag set, not under pod-evaluator.
	PodEvaluator PodEvaluatorOptions `flagPrefix:""`
	// PodEvaluatorGRPCPort is copied into PodEvaluator.GRPCPort when the server starts.
	PodEvaluatorGRPCPort int `flag:"pod-evaluator-port" default:"9447" usage:"TCP port for the FunctionEvaluator gRPC service (pod evaluator). The PackageRevision controller sends EvaluateFunction here."`

	// --functions is not nested under exec.
	Exec      ExecutableEvaluatorOptions `flagPrefix:""`
	ProbePort int                        `usage:"If > 0, start serving controller-runtime /healthz and /readyz on this port (in addition to the API server's built-in probes at --secure-port); a liveness-style check is available as /healthz/livez"`

	// The leader-election flags are not nested under ha.
	HA HAConfig `flagPrefix:""`

	// The generic apiserver flags are not nested under serving.
	Serving Serving `flagPrefix:""`
}

type PodEvaluatorOptions struct {
	// PodNamespace is copied from Server.PodNamespace. It has no flag of its own.
	PodNamespace string `flag:"-"`
	// WrapperServerImage is WRAPPER_SERVER_IMAGE.
	WrapperServerImage string `env:"*" usage:"Image of the wrapper server"`
	// GRPCPort is copied from Server.PodEvaluatorGRPCPort. Zero uses DefaultGRPCPort.
	GRPCPort int `usage:"TCP port for the FunctionEvaluator gRPC service (pod evaluator). Zero uses the default port"`
	// GcScanInterval is the interval of GC between scans.
	GcScanInterval Duration `flag:"scan-interval" default:"1m" usage:"The interval of GC between scans."`
	// PodTTL is the time-to-live for pods before GC.
	PodTTL Duration `default:"30m" usage:"TTL for pods before GC."`
	// WarmUpPodCacheOnStartup pre-creates pods for FunctionConfig podExecutor images at startup.
	WarmUpPodCacheOnStartup bool `flag:"warm-up-pod-cache" default:"true" usage:"if true, pod-cache-config image pods will be deployed at startup"`
	// EnablePrivateRegistries enables private registries and their authentication.
	EnablePrivateRegistries bool `usage:"if true enables the use of private registries and their authentication"`
	// RegistryAuthSecretPath is the path of the secret used for authenticating to custom registries.
	RegistryAuthSecretPath string `default:"/var/tmp/config-secret/.dockerconfigjson" usage:"The path of the secret used for authenticating to custom registries"`
	// RegistryAuthSecretName is the name of the secret used for authenticating to custom registries.
	RegistryAuthSecretName string `default:"auth-secret" usage:"The name of the secret used for authenticating to custom registries"`
	// EnablePrivateRegistriesTls prioritizes a user-provided TLS secret when accessing registries.
	EnablePrivateRegistriesTls bool `usage:"if enabled, will prioritize use of user provided TLS secret when accessing registries"`
	// TlsSecretPath is the path of the secret used in TLS configuration.
	TlsSecretPath string `default:"/var/tmp/tls-secret/" usage:"The path of the secret used in tls configuration"`
	// MaxWaitlistLength is the maximum waitlist length per pod.
	MaxWaitlistLength int `default:"2" usage:"Maximum waitlist length per pod"`
	// MaxParallelPodsPerFunction is the maximum parallel pods per function.
	MaxParallelPodsPerFunction int `default:"1" usage:"Maximum parallel pods per function"`
	// MaxGrpcRetries is the maximum number of retries on gRPC Unavailable errors. Zero uses 2.
	MaxGrpcRetries int `usage:"Maximum number of retries on gRPC Unavailable errors. Zero uses 2"`
}

type ExecutableEvaluatorOptions struct {
	// TODO: why is this not in /home/nonroot/functions?
	FunctionCacheDir string `flag:"functions" default:"./functions" usage:"Path to cached functions."`
}

type HAConfig struct {
	LeaderElection bool     `flag:"leader-elect" usage:"If true, the porch-server will attempt to acquire leader election lock"`
	LeaseDuration  Duration `flag:"leader-lease-duration" usage:"The duration that non-leader candidates will wait to force acquire leadership"`
	RenewDeadline  Duration `flag:"leader-renew-deadline" usage:"The duration that the acting controlplane will retry refreshing leadership before giving up"`
	RetryPeriod    Duration `flag:"leader-retry-period" usage:"The duration the LeaderElector clients should wait between tries of actions"`
}

// Serving mirrors the generic apiserver flags porch-server sets.
// genericoptions.RecommendedOptions cannot be embedded: it holds codecs, a
// feature gate, and an initializer function, and porch clears its etcd options.
//
// BindPort is options.SecureServingOptions.BindPort.
// CertDirectory is options.ServerCert.CertDirectory, nested on SecureServingOptions.
// EnablePlugins and DisablePlugins are options.AdmissionOptions.
type Serving struct {
	BindPort       int      `flag:"secure-port" default:"443" usage:"The port on which to serve HTTPS with authentication and authorization. If 0, don't serve HTTPS at all."`
	CertDirectory  string   `flag:"cert-dir" default:"apiserver.local.config/certificates" usage:"The directory where the TLS certs are located. If --tls-cert-file and --tls-private-key-file are provided, this flag will be ignored."`
	EnablePlugins  []string `flag:"enable-admission-plugins" usage:"admission plugins that should be enabled in addition to default enabled ones. Comma-delimited list of admission plugins. The order of plugins in this flag does not matter."`
	DisablePlugins []string `flag:"disable-admission-plugins" usage:"admission plugins that should be disabled although they are in the default enabled plugins list. Comma-delimited list of admission plugins. The order of plugins in this flag does not matter."`
}
