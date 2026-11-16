// Copyright 2022, 2025-2026 The kpt Authors
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

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	clientset "github.com/kptdev/porch/api/generated/clientset/versioned"
	informers "github.com/kptdev/porch/api/generated/informers/externalversions"
	sampleopenapi "github.com/kptdev/porch/api/generated/openapi"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/config/loader"
	configtypes "github.com/kptdev/porch/config/types"
	"github.com/kptdev/porch/internal/telemetry"
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

// PorchServerOptions contains state for master/api server.
// Server and Global are the configuration. RecommendedOptions is what the
// apiserver reads, so the overlapping serving and kubeconfig values are copied
// across after the file is loaded.
type PorchServerOptions struct {
	RecommendedOptions *genericoptions.RecommendedOptions

	SharedInformerFactory informers.SharedInformerFactory

	StdOut io.Writer
	StdErr io.Writer

	Server     configtypes.Server
	Global     configtypes.Global
	configPath string
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
			if err := o.loadConfig(c.Flags()); err != nil {
				return err
			}
			otelResources, err := telemetry.SetupOpenTelemetry(ctx)
			if err != nil {
				genericapiserver.RequestShutdown()
				return err
			}
			defer func() {
				if err := otelResources.ShutdownWithTimeout(10 * time.Second); err != nil {
					klog.Warningf("failed to gracefully shutdown OpenTelemetry: %v", err)
				}
			}()
			if err := o.Complete(); err != nil {
				return err
			}
			if err := o.Validate(); err != nil {
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
func (o PorchServerOptions) Validate() error {
	errors := []error{}
	errors = append(errors, o.RecommendedOptions.Validate()...)

	if !cachetypes.IsACacheType(o.Server.CacheType) {
		errors = append(errors, fmt.Errorf("specified cache-type %s is not supported", o.Server.CacheType))
	}

	if o.Server.Lists.MaxConcurrentLists < 0 {
		return fmt.Errorf("invalid value for max-parallel-repo-lists: 0 for no limit; > 0 for set limit")
	}

	return utilerrors.NewAggregate(errors)
}

// Complete fills in fields required to have valid data
func (o *PorchServerOptions) Complete() error {
	o.Server.CoreAPIKubeconfigPath = o.RecommendedOptions.CoreAPI.CoreAPIKubeconfigPath

	if o.Server.LocalStandaloneDebugging {
		if os.Getenv("KUBERNETES_SERVICE_HOST") != "" || os.Getenv("KUBERNETES_SERVICE_PORT") != "" {
			klog.Fatalf("--standalone-debug-mode must not be used when running in k8s")
		} else {
			o.RecommendedOptions.Authorization = nil
			o.RecommendedOptions.Admission = genericoptions.NewAdmissionOptions()
			o.RecommendedOptions.Authentication.RemoteKubeConfigFileOptional = true
		}
	} else {
		// This is needed in case the porch-server runs outside of the cluster, but without the --standalone-debug-mode flag.
		o.RecommendedOptions.Authentication.RemoteKubeConfigFile = o.Server.CoreAPIKubeconfigPath
		o.RecommendedOptions.Authorization.RemoteKubeConfigFile = o.Server.CoreAPIKubeconfigPath
	}

	if o.Server.CacheDirectory == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			cache = os.TempDir()
			klog.Warningf("Cannot find user cache directory, using temporary directory %q", cache)
		}
		o.Server.CacheDirectory = cache + "/porch"
	}

	o.Server.CacheType = strings.ToUpper(o.Server.CacheType)

	if o.Server.CacheType == string(cachetypes.DBCacheType) {
		if err := o.setupDBCacheConnFromConfig(); err != nil {
			return err
		}
	}

	// Parse and append additional retryable git errors
	if len(o.Server.RetryableGitErrors) > 0 {
		git.AppendRetryableErrors(o.Server.RetryableGitErrors)
	}

	return nil
}

func (o *PorchServerOptions) setupDBCacheConnFromConfig() error {
	return o.assignDBCache(
		o.Server.DB.Driver,
		o.Server.DB.DataSource,
		o.Server.Database.Host,
		o.Server.Database.Port,
		o.Server.Database.Name,
		o.Server.Database.User,
		o.Server.Database.Password,
		o.Server.Database.SSLMode,
	)
}

func (o *PorchServerOptions) assignDBCache(dbDriver, dataSource, dbHost, dbPort, dbName, dbUser, dbUserPass, dbSSLMode string) error {
	if dataSource != "" {
		if dbDriver == "" {
			dbDriver = cachetypes.DefaultDBCacheDriver
			klog.Infof("DB_DRIVER not provided, defaulting to use db driver: %v", dbDriver)
		}
		o.Server.DB.Driver = dbDriver
		o.Server.DB.DataSource = dataSource
		return nil
	}
	// DB_SSL_MODE is optional - Default is "disable"
	dbSSLMode = strings.ToLower(dbSSLMode)

	missingVars := []string{}
	if dbDriver == "" {
		dbDriver = cachetypes.DefaultDBCacheDriver
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
	o.Server.DB.Driver = dbDriver
	o.Server.DB.DataSource = connStr

	return nil
}

// Config returns config for the api server given PorchServerOptions
func (o *PorchServerOptions) Config() (*apiserver.Config, error) {
	// TODO have a "real" external address
	if err := o.RecommendedOptions.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{netutils.ParseIPSloppy("127.0.0.1")}); err != nil {
		return nil, fmt.Errorf("error creating self-signed certificates: %w", err)
	}

	if o.Server.PodEvaluator.WrapperServerImage == "" {
		return nil, fmt.Errorf("wrapper server image is not set; porch-server cannot start")
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
	serverConfig.MaxRequestBodyBytes = int64(o.Global.MaxGrpcMessageSize)

	if err := o.RecommendedOptions.ApplyTo(serverConfig); err != nil {
		return nil, err
	}

	return &apiserver.Config{
		GenericConfig: serverConfig,
		ExtraConfig:   o.buildExtraConfig(),
	}, nil
}

func (o *PorchServerOptions) buildExtraConfig() apiserver.ExtraConfig {
	podEval := o.Server.PodEvaluator
	podEval.PodNamespace = o.Server.PodNamespace
	podEval.GRPCPort = o.Server.PodEvaluatorGRPCPort
	return apiserver.ExtraConfig{
		CoreAPIKubeconfigPath: o.Server.CoreAPIKubeconfigPath,
		GRPCRuntimeOptions: engine.GRPCRuntimeOptions{
			FunctionRunnerAddress: o.Server.FunctionRunnerAddress,
			MaxGrpcMessageSize:    o.Global.MaxGrpcMessageSize,
		},
		CacheOptions: cachetypes.CacheOptions{
			ExternalRepoOptions: externalrepotypes.ExternalRepoOptions{
				LocalDirectory:         o.Server.CacheDirectory,
				UseUserDefinedCaBundle: o.Server.UseUserDefinedCaBundle,
				GoGitRepoCacheSize:     o.Server.GoGitRepoCacheSize,
				GoGitCacheMaxFileSize:  o.Server.GoGitCacheMaxFileSize,
			},
			RepoOperationRetryAttempts: o.Server.RepoOperationRetryAttempts,
			CacheType:                  cachetypes.CacheType(o.Server.CacheType),
			CRCacheOptions:             o.Server.Lists,
			DBCacheOptions:             o.Server.DB,
			DbPushDraftsToGit:          o.Server.DbPushDraftsToGit,
		},
		PodNameSpace:        o.Server.PodNamespace,
		PodEvaluatorOptions: podEval,
		Global:              o.Global,
		ProbePort:           o.Server.ProbePort,
		HAOptions:           o.Server.HA,
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

	if o.Server.ProbePort > 0 {
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
	// Bind first. RecommendedOptions registers five of the same names, and
	// AddFlagSet keeps the flag that is already present.
	if err := loader.Bind(fs, &o.Server); err != nil {
		panic(err)
	}
	if err := loader.Bind(fs, &o.Global); err != nil {
		panic(err)
	}
	fs.StringVar(&o.configPath, "config", "", "Path to the porch configuration file.")

	base := pflag.NewFlagSet("apiserver", pflag.ContinueOnError)
	o.RecommendedOptions.AddFlags(base)
	fs.AddFlagSet(base)
	utilfeature.DefaultMutableFeatureGate.AddFlag(fs)
}

func (o *PorchServerOptions) loadConfig(fs *pflag.FlagSet) error {
	doc, err := loader.ReadFile(o.configPath)
	if err != nil {
		return err
	}
	if err := loader.OverlaySection(fs, &o.Server, doc, "server"); err != nil {
		return err
	}
	if err := loader.OverlaySection(fs, &o.Global, doc, "global"); err != nil {
		return err
	}
	if err := telemetry.ApplyEnv(o.Global.Telemetry); err != nil {
		return err
	}
	o.applyLoaded()
	return nil
}

func (o *PorchServerOptions) applyLoaded() {
	s := o.Server
	o.RecommendedOptions.CoreAPI.CoreAPIKubeconfigPath = s.CoreAPIKubeconfigPath
	if o.RecommendedOptions.SecureServing != nil {
		o.RecommendedOptions.SecureServing.BindPort = s.Serving.BindPort
		o.RecommendedOptions.SecureServing.ServerCert.CertDirectory = s.Serving.CertDir
	}
	if o.RecommendedOptions.Admission != nil {
		o.RecommendedOptions.Admission.EnablePlugins = s.Serving.EnableAdmissionPlugins
		o.RecommendedOptions.Admission.DisablePlugins = s.Serving.DisableAdmissionPlugins
	}
}
