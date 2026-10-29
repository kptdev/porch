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

import (
	"github.com/kptdev/porch/pkg/apiserver"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/engine"
	"github.com/kptdev/porch/pkg/engine/podevaluator"
)

// Server mirrors the configuration fields of server.PorchServerOptions.
// RecommendedOptions, SharedInformerFactory, StdOut, and StdErr are runtime
// state and are not included. The generic apiserver flags porch-server sets
// are Serving.
//
// DB, Lists, PodEvaluator, Exec, and HA are existing porch types. Their
// time.Duration fields still unmarshal as nanosecond counts.
type Server struct {
	CoreAPIKubeconfigPath string

	CacheDirectory string
	CacheType      string
	DB             cachetypes.DBCacheOptions
	Database       DatabaseConnection
	// DbPushDraftsToGit mirrors PorchServerOptions.DbPushDrafsToGit.
	DbPushDraftsToGit bool

	GoGitRepoCacheSize    int
	GoGitCacheMaxFileSize int64

	DefaultImagePrefix       string
	FunctionRunnerAddress    string
	LocalStandaloneDebugging bool

	Lists                      cachetypes.CRCacheOptions
	MaxRequestBodySize         int
	RepoOperationRetryAttempts int
	RetryableGitErrors         []string

	UseUserDefinedCaBundle bool

	PodNamespace string

	PodEvaluator podevaluator.PodEvaluatorOptions
	// PodEvaluatorGRPCPort is copied into PodEvaluator.GRPCPort when the server starts.
	PodEvaluatorGRPCPort int

	Exec      engine.ExecutableEvaluatorOptions
	ProbePort int

	HA apiserver.HAConfig

	Serving Serving
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
