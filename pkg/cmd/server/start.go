// Copyright 2022, 2025 The kpt Authors
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

package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	clientset "github.com/kptdev/porch/api/generated/clientset/versioned"
	informers "github.com/kptdev/porch/api/generated/informers/externalversions"
	sampleopenapi "github.com/kptdev/porch/api/generated/openapi"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/apiserver"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/engine"
	"github.com/kptdev/porch/pkg/externalrepo/git"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	netutils "k8s.io/utils/net"
)

const (
	defaultEtcdPathPrefix = "/registry/porch.kpt.dev"
	OpenAPITitle          = "Porch"
	OpenAPIVersion        = "0.1"
)

// PorchServerOptions contains state for master/api server
type PorchServerOptions struct {
	RecommendedOptions *genericoptions.RecommendedOptions

	CoreAPIKubeconfigPath string

	CacheDirectory       string
	CacheType            string
	DbCacheDriver        string
	DbCacheDataSource    string
	DbMaxConnections     int
	DbMaxIdleConnections int
	DbMaxConnLifetime    time.Duration
	DbPushDrafsToGit     bool

	GoGitRepoCacheSize    int
	GoGitCacheMaxFileSize int64

	DefaultImagePrefix       string
	FunctionRunnerAddress    string
	LocalStandaloneDebugging bool // Enables local standalone running/debugging of the apiserver.

	ListTimeoutPerRepository   time.Duration
	MaxConcurrentLists         int
	MaxRequestBodySize         int
	RepoOperationRetryAttempts int
	RetryableGitErrors         []string // Additional retryable git error patterns

	SharedInformerFactory informers.SharedInformerFactory

	StdOut io.Writer
	StdErr io.Writer

	UseUserDefinedCaBundle bool

	PodNamespace string

	ProbePort int

	HAOptions apiserver.HAConfig
}

// NewPorchServerOptions returns a new PorchServerOptions
func NewPorchServerOptions(out, errOut io.Writer) *PorchServerOptions {
	//
	// GroupVersions served by this server
	//
	versions := schema.GroupVersions{
		porchapi.SchemeGroupVersion,
	}

	o := &PorchServerOptions{
		RecommendedOptions: genericoptions.NewRecommendedOptions(
			defaultEtcdPathPrefix,
			apiserver.Codecs.LegacyCodec(versions...),
		),

		StdOut: out,
		StdErr: errOut,
	}
	o.RecommendedOptions.Etcd.StorageConfig.EncodeVersioner = versions
	o.RecommendedOptions.Etcd = nil
	return o
}

// NewCommandStartPorchServer provides a CLI handler for 'start master' command
// with a default PorchServerOptions.
func NewCommandStartPorchServer(ctx context.Context, defaults *PorchServerOptions) *cobra.Command {
	o := *defaults
	cmd := &cobra.Command{
		Short: "Launch a porch API server",
		Long:  "Launch a porch API server",
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.Complete(); err != nil {
				return err
			}
			if err := o.Validate(args); err != nil {
				return err
			}
			if err := o.RunPorchServer(ctx); err != nil {
				return err
			}
			return nil
		},
	}

	flags := cmd.Flags()
	o.AddFlags(flags)

	return cmd
}

// Validate validates PorchServerOptions
func (o PorchServerOptions) Validate(args []string) error {
	errors := []error{}
	errors = append(errors, o.RecommendedOptions.Validate()...)

	if !cachetypes.IsACacheType(o.CacheType) {
		errors = append(errors, fmt.Errorf("specified cache-type %s is not supported", o.CacheType))
	}

	if o.MaxConcurrentLists < 0 {
		return fmt.Errorf("invalid value for max-parallel-repo-lists: 0 for no limit; > 0 for set limit")
	}

	return utilerrors.NewAggregate(errors)
}

// Complete fills in fields required to have valid data
func (o *PorchServerOptions) Complete() error {
	o.CoreAPIKubeconfigPath = o.RecommendedOptions.CoreAPI.CoreAPIKubeconfigPath

	if o.LocalStandaloneDebugging {
		if os.Getenv("KUBERNETES_SERVICE_HOST") != "" || os.Getenv("KUBERNETES_SERVICE_PORT") != "" {
			klog.Fatalf("--standalone-debug-mode must not be used when running in k8s")
		} else {
			o.RecommendedOptions.Authorization = nil
			o.RecommendedOptions.Admission = genericoptions.NewAdmissionOptions()
			o.RecommendedOptions.Authentication.RemoteKubeConfigFileOptional = true
		}
	} else {
		// This is needed in case the porch-server runs outside of the cluster, but without the --standalone-debug-mode flag.
		o.RecommendedOptions.Authentication.RemoteKubeConfigFile = o.CoreAPIKubeconfigPath
		o.RecommendedOptions.Authorization.RemoteKubeConfigFile = o.CoreAPIKubeconfigPath
	}

	if o.CacheDirectory == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			cache = os.TempDir()
			klog.Warningf("Cannot find user cache directory, using temporary directory %q", cache)
		}
		o.CacheDirectory = cache + "/porch"
	}

	o.CacheType = strings.ToUpper(o.CacheType)

	if o.CacheType == string(cachetypes.DBCacheType) {
		if err := o.setupDBCacheConn(); err != nil {
			return err
		}
		// Set connection pool defaults from environment variables
		if maxConns := os.Getenv("DB_MAX_CONNECTIONS"); maxConns != "" {
			if n, err := fmt.Sscanf(maxConns, "%d", &o.DbMaxConnections); err != nil || n != 1 {
				klog.Warningf("Invalid DB_MAX_CONNECTIONS value %q, using default 300", maxConns)
				o.DbMaxConnections = 300
			}
		} else {
			o.DbMaxConnections = 300
		}
		if maxIdle := os.Getenv("DB_MAX_IDLE_CONNECTIONS"); maxIdle != "" {
			if n, err := fmt.Sscanf(maxIdle, "%d", &o.DbMaxIdleConnections); err != nil || n != 1 {
				klog.Warningf("Invalid DB_MAX_IDLE_CONNECTIONS value %q, using default 100", maxIdle)
				o.DbMaxIdleConnections = 100
			}
		} else {
			o.DbMaxIdleConnections = 100
		}
		if maxLifetime := os.Getenv("DB_MAX_CONN_LIFETIME"); maxLifetime != "" {
			if d, err := time.ParseDuration(maxLifetime); err != nil {
				klog.Warningf("Invalid DB_MAX_CONN_LIFETIME value %q, using default 3m", maxLifetime)
				o.DbMaxConnLifetime = 3 * time.Minute
			} else {
				o.DbMaxConnLifetime = d
			}
		} else {
			o.DbMaxConnLifetime = 3 * time.Minute
		}
	}

	// Parse and append additional retryable git errors
	if len(o.RetryableGitErrors) > 0 {
		git.AppendRetryableErrors(o.RetryableGitErrors)
	}

	return nil
}

func (o *PorchServerOptions) setupDBCacheConn() error {
	dbDriver := os.Getenv("DB_DRIVER")
	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbName := os.Getenv("DB_NAME")
	dbUser := os.Getenv("DB_USER")
	dbUserPass := os.Getenv("DB_PASSWORD")
	// DB_SSL_MODE is optional - Default is "disable"
	dbSSLMode := os.Getenv("DB_SSL_MODE")
	dbSSLMode = strings.ToLower(dbSSLMode)

	missingVars := []string{}
	if dbDriver == "" {
		dbDriver = "pgx"
		klog.Infof("DB_DRIVER not provided, defaulting to use db driver: %v", dbDriver)
	}
	if dbHost == "" {
		missingVars = append(missingVars, "DB_HOST")
	}
	if dbPort == "" {
		missingVars = append(missingVars, "DB_PORT")
	}
	if dbName == "" {
		missingVars = append(missingVars, "DB_NAME")
	}
	if dbUser == "" {
		missingVars = append(missingVars, "DB_USER")
	}
	// DB_PASSWORD is not needed if SSL mode is set.
	if dbSSLMode == "" || dbSSLMode == "disable" {
		if dbUserPass == "" {
			missingVars = append(missingVars, "DB_PASSWORD")
		}
	}

	if len(missingVars) > 0 {
		return fmt.Errorf("missing required environment variables: %v", missingVars)
	}

	// Build connection string based on the DB type
	var connStr string
	switch dbDriver {
	case "pgx":
		if dbSSLMode == "" || dbSSLMode == "disable" {
			connStr = fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", dbUser, dbUserPass, net.JoinHostPort(dbHost, dbPort), dbName)
		} else {
			connStr = fmt.Sprintf("postgres://%s@%s/%s?sslmode=%s", dbUser, net.JoinHostPort(dbHost, dbPort), dbName, dbSSLMode)
		}

	case "mysql":
		connStr = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", dbUser, dbUserPass, dbHost, dbPort, dbName)
	default:
		return fmt.Errorf("unsupported DB driver: %s", dbDriver)
	}

	// Set the DB cache options
	o.DbCacheDriver = dbDriver
	o.DbCacheDataSource = connStr

	return nil
}

// Config returns config for the api server given PorchServerOptions
func (o *PorchServerOptions) Config() (*apiserver.Config, error) {
	// TODO have a "real" external address
	if err := o.RecommendedOptions.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{netutils.ParseIPSloppy("127.0.0.1")}); err != nil {
		return nil, fmt.Errorf("error creating self-signed certificates: %w", err)
	}

	o.RecommendedOptions.ExtraAdmissionInitializers = func(c *genericapiserver.RecommendedConfig) ([]admission.PluginInitializer, error) {
		client, err := clientset.NewForConfig(c.LoopbackClientConfig)
		if err != nil {
			return nil, err
		}
		informerFactory := informers.NewSharedInformerFactory(client, c.LoopbackClientConfig.Timeout)
		o.SharedInformerFactory = informerFactory
		return []admission.PluginInitializer{}, nil
	}

	serverConfig := genericapiserver.NewRecommendedConfig(apiserver.Codecs)

	serverConfig.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(sampleopenapi.GetOpenAPIDefinitions, openapi.NewDefinitionNamer(apiserver.Scheme))
	serverConfig.OpenAPIConfig.Info.Title = OpenAPITitle
	serverConfig.OpenAPIConfig.Info.Version = OpenAPIVersion

	serverConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(sampleopenapi.GetOpenAPIDefinitions, openapi.NewDefinitionNamer(apiserver.Scheme))
	serverConfig.OpenAPIConfig.Info.Title = OpenAPITitle
	serverConfig.OpenAPIConfig.Info.Version = OpenAPIVersion
	serverConfig.MaxRequestBodyBytes = int64(o.MaxRequestBodySize)

	if err := o.RecommendedOptions.ApplyTo(serverConfig); err != nil {
		return nil, err
	}

	return &apiserver.Config{
		GenericConfig: serverConfig,
		ExtraConfig:   o.buildExtraConfig(),
	}, nil
}

func (o *PorchServerOptions) buildExtraConfig() apiserver.ExtraConfig {
	return apiserver.ExtraConfig{
		CoreAPIKubeconfigPath: o.CoreAPIKubeconfigPath,
		GRPCRuntimeOptions: engine.GRPCRuntimeOptions{
			FunctionRunnerAddress: o.FunctionRunnerAddress,
			MaxGrpcMessageSize:    o.MaxRequestBodySize,
			DefaultImagePrefix:    o.DefaultImagePrefix,
		},
		CacheOptions: cachetypes.CacheOptions{
			ExternalRepoOptions: externalrepotypes.ExternalRepoOptions{
				LocalDirectory:         o.CacheDirectory,
				UseUserDefinedCaBundle: o.UseUserDefinedCaBundle,
				GoGitRepoCacheSize:     o.GoGitRepoCacheSize,
				GoGitCacheMaxFileSize:  o.GoGitCacheMaxFileSize,
			},
			RepoOperationRetryAttempts: o.RepoOperationRetryAttempts,
			CacheType:                  cachetypes.CacheType(o.CacheType),
			CRCacheOptions: cachetypes.CRCacheOptions{
				MaxConcurrentLists:       o.MaxConcurrentLists,
				ListTimeoutPerRepository: o.ListTimeoutPerRepository,
			},
			DBCacheOptions: cachetypes.DBCacheOptions{
				Driver:             o.DbCacheDriver,
				DataSource:         o.DbCacheDataSource,
				MaxConnections:     o.DbMaxConnections,
				MaxIdleConnections: o.DbMaxIdleConnections,
				MaxConnLifetime:    o.DbMaxConnLifetime,
			},
			DbPushDraftsToGit: o.DbPushDrafsToGit,
		},
		PodNameSpace: o.PodNamespace,
		ProbePort:    o.ProbePort,
		HAOptions:    o.HAOptions,
	}
}

// RunPorchServer starts a new PorchServer given PorchServerOptions
func (o PorchServerOptions) RunPorchServer(ctx context.Context) error {
	config, err := o.Config()
	if err != nil {
		return err
	}

	mgr, server, err := config.Complete().New(ctx)
	if err != nil {
		return err
	}

	if config.GenericConfig.SharedInformerFactory != nil {
		server.GenericAPIServer.AddPostStartHookOrDie("start-sample-server-informers", func(context genericapiserver.PostStartHookContext) error {
			config.GenericConfig.SharedInformerFactory.Start(context.Done())
			o.SharedInformerFactory.Start(context.Done())
			return nil
		})
	}

	if o.ProbePort > 0 {
		loopback := config.GenericConfig.LoopbackClientConfig
		if loopback == nil {
			return fmt.Errorf("loopback client config is required to proxy health checks")
		}
		client, err := rest.HTTPClientFor(loopback)
		if err != nil {
			return fmt.Errorf("failed to create loopback health client: %w", err)
		}
		client.Timeout = 3 * time.Second
		if err := proxyHealthChecks(mgr, client, loopback.Host); err != nil {
			return err
		}
	}

	return mgr.Start(ctx)
}

type probeManager interface {
	AddHealthzCheck(name string, check healthz.Checker) error
	AddReadyzCheck(name string, check healthz.Checker) error
	Elected() <-chan struct{}
}

func proxyHealthChecks(mgr probeManager, client *http.Client, baseURL string) error {
	healthzDelegate := delegateAPIServerHealth(mgr, client, baseURL, "healthz", true)
	if err := mgr.AddHealthzCheck("healthz", healthzDelegate); err != nil {
		return err
	}

	// there is no livez on the controller-runtime manager, only healthz, so we proxy both to healthz
	livezDelegate := delegateAPIServerHealth(mgr, client, baseURL, "livez", true)
	if err := mgr.AddHealthzCheck("livez", livezDelegate); err != nil {
		return err
	}

	readyzDelegate := delegateAPIServerHealth(mgr, client, baseURL, "readyz", false)
	if err := mgr.AddReadyzCheck("readyz", readyzDelegate); err != nil {
		return err
	}

	return nil
}

type electedManager interface {
	Elected() <-chan struct{}
}

func delegateAPIServerHealth(mgr electedManager, client *http.Client, baseURL, path string, okWhenStandby bool) healthz.Checker {
	url := strings.TrimRight(baseURL, "/") + "/" + path

	return func(*http.Request) error {
		select {
		case <-mgr.Elected():
			resp, err := client.Get(url)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("apiserver %s returned %d", path, resp.StatusCode)
			}
			return nil
		default:
			if okWhenStandby {
				return nil
			}
			return fmt.Errorf("not leader")
		}
	}
}

func (o *PorchServerOptions) AddFlags(fs *pflag.FlagSet) {
	// Add base flags
	o.RecommendedOptions.AddFlags(fs)
	utilfeature.DefaultMutableFeatureGate.AddFlag(fs)

	// Debugging flags
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" && os.Getenv("KUBERNETES_SERVICE_PORT") == "" {
		fs.BoolVar(&o.LocalStandaloneDebugging, "standalone-debug-mode", false,
			"Under the local-debug mode the apiserver will allow all access to its resources without "+
				"authorizing the requests, this flag is only intended for debugging in your workstation.")
	}

	// Cache configuration
	fs.StringVar(&o.CacheDirectory, "cache-directory", "", "Directory where Porch server stores repository and package caches.")
	fs.StringVar(&o.CacheType, "cache-type", string(cachetypes.DefaultCacheType), "Type of cache to use for cacheing repos, supported types are \"CR\" (Custom Resource) and \"DB\" (DataBase)")
	fs.StringVar(&o.DbCacheDriver, "db-cache-driver", cachetypes.DefaultDBCacheDriver, "Database driver to use when for the database cache")
	fs.StringVar(&o.DbCacheDataSource, "db-cache-data-source", "", "Address of the database, for example \"postgresql://user:pass@hostname:port/database\"")
	fs.BoolVar(&o.DbPushDrafsToGit, "db-push-drafts-to-git", false, "If true, Porch will push draft package revisions to git when using the DB cache")

	//GoGit configuration
	fs.IntVar(&o.GoGitRepoCacheSize, "gogit-repo-cache-size", 8, "Size of the in-memory cache for git repositories when using gogit (in MiB)")
	fs.Int64Var(&o.GoGitCacheMaxFileSize, "gogit-cache-max-file-size", 1*1024*512, "Maximum file size (in bytes) that will be read into the in-memory cache for git repositories when using gogit; files larger than this will be streamed from disk to avoid memory pressure")

	// Function runner configuration
	fs.StringVar(&o.DefaultImagePrefix, "default-image-prefix", runneroptions.GHCRImagePrefix, "Default prefix for unqualified function names")
	fs.StringVar(&o.FunctionRunnerAddress, "function-runner", "", "Address of the function runner gRPC service.")
	fs.IntVar(&o.MaxRequestBodySize, "max-request-body-size", 6*1024*1024, "Maximum size of the request body in bytes. Keep this in sync with function-runner's corresponding argument.")
	fs.StringVar(&o.PodNamespace, "pod-namespace", "porch-fn-system", "Namespace get FunctionConfig objects for krm functions")

	// Repository operations configuration
	fs.BoolVar(&o.UseUserDefinedCaBundle, "use-user-cabundle", false, "Determine whether to use a user-defined CaBundle for TLS towards the repository system.")
	fs.IntVar(&o.RepoOperationRetryAttempts, "repo-operation-retry-attempts", 3, "Number of retry attempts for repository operations.")
	fs.StringSliceVar(&o.RetryableGitErrors, "retryable-git-errors", nil, "Additional retryable git error patterns. Can be specified multiple times or as comma-separated values.")
	fs.DurationVar(&o.ListTimeoutPerRepository, "list-timeout-per-repo", 20*time.Second, "Maximum amount of time to wait for a repository list request.")
	fs.IntVar(&o.MaxConcurrentLists, "max-parallel-repo-lists", 10, "Maximum number of repositories to list in parallel.")

	fs.IntVar(&o.ProbePort, "probe-port", 0, "If > 0, start serving controller-runtime /healthz and /readyz on this port (in addition to the API server's built-in probes at `--secure-port`); a liveness-style check is available as /healthz/livez")

	fs.BoolVar(&o.HAOptions.LeaderElection, "leader-elect", false, "If true, the porch-server will attempt to acquire leader election lock")
	fs.DurationVar(&o.HAOptions.LeaseDuration, "leader-lease-duration", 0, "The duration that non-leader candidates will wait to force acquire leadership")
	fs.DurationVar(&o.HAOptions.RenewDeadline, "leader-renew-deadline", 0, "The duration that the acting controlplane will retry refreshing leadership before giving up")
	fs.DurationVar(&o.HAOptions.RetryPeriod, "leader-retry-period", 0, "The duration the LeaderElector clients should wait between tries of actions")
}
