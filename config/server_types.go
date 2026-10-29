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

// Server mirrors the configuration fields of server.PorchServerOptions.
// RecommendedOptions, SharedInformerFactory, StdOut, and StdErr are runtime
// state and are not included. The generic apiserver flags porch-server sets
// are Serving.
type Server struct {
	CoreAPIKubeconfigPath string

	CacheDirectory string
	CacheType      string
	DB             DBCacheOptions
	Database       DatabaseConnection
	// DbPushDraftsToGit mirrors PorchServerOptions.DbPushDrafsToGit.
	DbPushDraftsToGit bool

	GoGitRepoCacheSize    int
	GoGitCacheMaxFileSize DataSize

	DefaultImagePrefix       string
	FunctionRunnerAddress    string
	LocalStandaloneDebugging bool

	Lists                      CRCacheOptions
	MaxRequestBodySize         DataSize
	RepoOperationRetryAttempts int
	RetryableGitErrors         []string

	UseUserDefinedCaBundle bool

	PodNamespace string

	PodEvaluator PodEvaluatorOptions
	// PodEvaluatorGRPCPort is copied into PodEvaluator.GRPCPort when the server starts.
	PodEvaluatorGRPCPort int

	Exec      ExecutableEvaluatorOptions
	ProbePort int

	HA HAConfig

	Serving Serving
}

// TODO: remove the original podevaluator.PodEvaluatorOptions in pkg/engine/podevaluator.
type PodEvaluatorOptions struct {
	PodNamespace               string   // Namespace to run KRM functions pods in
	WrapperServerImage         string   // Container image name of the wrapper server
	GRPCPort                   int      // FunctionEvaluator listen port; 0 uses DefaultGRPCPort
	GcScanInterval             Duration // Time interval between Garbage Collector scans
	PodTTL                     Duration // Time-to-live for pods before GC
	WarmUpPodCacheOnStartup    bool     // If true, pre-create pods for FunctionConfig podExecutor images at startup
	EnablePrivateRegistries    bool     // If true enables the use of private registries and their authentication
	RegistryAuthSecretPath     string   // The path of the secret used for authenticating to custom registries
	RegistryAuthSecretName     string   // The name of the secret used for authenticating to custom registries
	EnablePrivateRegistriesTls bool     // If enabled, will prioritize use of user provided TLS secret when accessing registries
	TlsSecretPath              string   // The path of the secret used in tls configuration
	MaxGrpcMessageSize         DataSize // Maximum size of grpc messages in bytes
	DefaultImagePrefix         string   // Default image prefix to use when no prefix is given for an image
	MaxWaitlistLength          int      // Maximum waitlist length per pod
	MaxParallelPodsPerFunction int      // Maximum parallel pods per function
	MaxGrpcRetries             int      // Maximum number of retries on gRPC Unavailable errors
}

// TODO: remove the original engine.ExecutableEvaluatorOptions in pkg/engine.
type ExecutableEvaluatorOptions struct {
	FunctionCacheDir string
}

// TODO: remove the original apiserver.HAConfig in pkg/apiserver.
type HAConfig struct {
	LeaderElection bool
	LeaseDuration  Duration
	RenewDeadline  Duration
	RetryPeriod    Duration
}

// Serving mirrors the generic apiserver flags porch-server sets.
// genericoptions.RecommendedOptions cannot be embedded: it holds codecs, a
// feature gate, and an initializer function, and porch clears its etcd options.
//
// BindPort is options.SecureServingOptions.BindPort.
// CertDirectory is options.ServerCert.CertDirectory, nested on SecureServingOptions.
// EnablePlugins and DisablePlugins are options.AdmissionOptions.
type Serving struct {
	BindPort       int
	CertDirectory  string
	EnablePlugins  []string
	DisablePlugins []string
}
