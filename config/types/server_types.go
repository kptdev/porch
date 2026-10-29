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

// Server mirrors the configuration fields of server.PorchServerOptions.
// RecommendedOptions, SharedInformerFactory, StdOut, and StdErr are runtime
// state and are not included. The generic apiserver flags porch-server sets
// are Serving.
type Server struct {
	CoreAPIKubeconfigPath string `flag:"kubeconfig"`

	CacheDirectory string
	CacheType      string `default:"CR"`
	// The db-cache flags are not nested under db.
	DB       DBCacheOptions `flagPrefix:""`
	Database DatabaseConnection
	// DbPushDraftsToGit mirrors PorchServerOptions.DbPushDrafsToGit.
	DbPushDraftsToGit bool

	GoGitRepoCacheSize int `flag:"gogit-repo-cache-size" default:"8"`
	// 512KiB is the current flag default, 512 * datasize.KB.
	GoGitCacheMaxFileSize DataSize `flag:"gogit-cache-max-file-size" default:"512KiB"`

	DefaultImagePrefix       string `default:"ghcr.io/kptdev/krm-functions-catalog"`
	FunctionRunnerAddress    string `flag:"function-runner"`
	LocalStandaloneDebugging bool   `flag:"standalone-debug-mode"`

	// The list flags are not nested under lists.
	Lists                      CRCacheOptions `flagPrefix:""`
	MaxRequestBodySize         DataSize       `default:"6MiB"`
	RepoOperationRetryAttempts int            `default:"3"`
	RetryableGitErrors         []string

	UseUserDefinedCaBundle bool `flag:"use-user-cabundle"`

	PodNamespace string `default:"porch-fn-system"`

	// The pod evaluator flags are registered on the server flag set, not under pod-evaluator.
	PodEvaluator PodEvaluatorOptions `flagPrefix:""`
	// PodEvaluatorGRPCPort is copied into PodEvaluator.GRPCPort when the server starts.
	PodEvaluatorGRPCPort int `flag:"pod-evaluator-port" default:"9447"`

	// --functions is not nested under exec.
	Exec      ExecutableEvaluatorOptions `flagPrefix:""`
	ProbePort int

	// The leader-election flags are not nested under ha.
	HA HAConfig `flagPrefix:""`

	// The generic apiserver flags are not nested under serving.
	Serving Serving `flagPrefix:""`
}

// TODO: remove the original podevaluator.PodEvaluatorOptions in pkg/engine/podevaluator.
type PodEvaluatorOptions struct {
	// PodNamespace is copied from Server.PodNamespace. It has no flag of its own.
	PodNamespace string `flag:"-"`
	// WrapperServerImage is WRAPPER_SERVER_IMAGE.
	WrapperServerImage string `env:"*"`
	// GRPCPort is copied from Server.PodEvaluatorGRPCPort. Zero uses DefaultGRPCPort.
	GRPCPort int
	// GcScanInterval is the interval of GC between scans.
	GcScanInterval Duration `flag:"scan-interval" default:"1m"`
	// PodTTL is the time-to-live for pods before GC.
	PodTTL Duration `default:"30m"`
	// WarmUpPodCacheOnStartup pre-creates pods for FunctionConfig podExecutor images at startup.
	WarmUpPodCacheOnStartup bool `flag:"warm-up-pod-cache" default:"true"`
	// EnablePrivateRegistries enables private registries and their authentication.
	EnablePrivateRegistries bool
	// RegistryAuthSecretPath is the path of the secret used for authenticating to custom registries.
	RegistryAuthSecretPath string `default:"/var/tmp/config-secret/.dockerconfigjson"`
	// RegistryAuthSecretName is the name of the secret used for authenticating to custom registries.
	RegistryAuthSecretName string `default:"auth-secret"`
	// EnablePrivateRegistriesTls prioritizes a user-provided TLS secret when accessing registries.
	EnablePrivateRegistriesTls bool
	// TlsSecretPath is the path of the secret used in TLS configuration.
	TlsSecretPath string `default:"/var/tmp/tls-secret/"`
	// MaxGrpcMessageSize is copied from Server.MaxRequestBodySize.
	MaxGrpcMessageSize DataSize
	// DefaultImagePrefix is copied from Server.DefaultImagePrefix. It has no flag of its own.
	DefaultImagePrefix string `flag:"-"`
	// MaxWaitlistLength is the maximum waitlist length per pod.
	MaxWaitlistLength int `default:"2"`
	// MaxParallelPodsPerFunction is the maximum parallel pods per function.
	MaxParallelPodsPerFunction int `default:"1"`
	// MaxGrpcRetries is the maximum number of retries on gRPC Unavailable errors. Zero uses 2.
	MaxGrpcRetries int
}

// TODO: remove the original engine.ExecutableEvaluatorOptions in pkg/engine.
type ExecutableEvaluatorOptions struct {
	FunctionCacheDir string `flag:"functions" default:"./functions"`
}

// TODO: remove the original apiserver.HAConfig in pkg/apiserver.
type HAConfig struct {
	LeaderElection bool     `flag:"leader-elect"`
	LeaseDuration  Duration `flag:"leader-lease-duration"`
	RenewDeadline  Duration `flag:"leader-renew-deadline"`
	RetryPeriod    Duration `flag:"leader-retry-period"`
}

// Serving mirrors the generic apiserver flags porch-server sets.
// genericoptions.RecommendedOptions cannot be embedded: it holds codecs, a
// feature gate, and an initializer function, and porch clears its etcd options.
//
// BindPort is options.SecureServingOptions.BindPort.
// CertDirectory is options.ServerCert.CertDirectory, nested on SecureServingOptions.
// EnablePlugins and DisablePlugins are options.AdmissionOptions.
type Serving struct {
	BindPort       int      `flag:"secure-port" default:"443"`
	CertDirectory  string   `flag:"cert-dir" default:"apiserver.local.config/certificates"`
	EnablePlugins  []string `flag:"enable-admission-plugins"`
	DisablePlugins []string `flag:"disable-admission-plugins"`
}
