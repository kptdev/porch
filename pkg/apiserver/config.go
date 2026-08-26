// Copyright 2022, 2024-2026 The kpt Authors
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

package apiserver

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/porch/api/porch/install"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/functionconfigs"
	repocache "github.com/kptdev/porch/controllers/repo-cache"
	"github.com/kptdev/porch/pkg/cache"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/engine"
	"github.com/kptdev/porch/pkg/engine/podevaluator"
	"github.com/kptdev/porch/pkg/registry/porch"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/api/option"
	"google.golang.org/api/sts/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/wait"
	genericapiserver "k8s.io/apiserver/pkg/server"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/component-base/compatibility"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const NameIndexKey = "metadata.name"

const LeaderElectionID = "porch-server"

var (
	// Scheme defines methods for serializing and deserializing API objects.
	Scheme = runtime.NewScheme()
	// Codecs provides methods for retrieving codecs and serializers for specific
	// versions and content types.
	Codecs = serializer.NewCodecFactory(Scheme)
	// completeScheme is a singleton for the complete scheme with all types
	completeScheme *runtime.Scheme
	schemeOnce     sync.Once
)

func init() {
	install.Install(Scheme)

	// we need to add the options to empty v1
	// TODO fix the server code to avoid this
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})

	// TODO: keep the generic API server from wanting this
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	Scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)
}

type HAConfig struct {
	LeaderElection bool
	LeaseDuration  time.Duration
	RenewDeadline  time.Duration
	RetryPeriod    time.Duration
}

// ExtraConfig holds custom apiserver config
type ExtraConfig struct {
	CoreAPIKubeconfigPath string

	GRPCRuntimeOptions engine.GRPCRuntimeOptions
	CacheOptions       cachetypes.CacheOptions

	PodEvaluatorOptions podevaluator.PodEvaluatorOptions

	ExecEvaluatorOptions engine.ExecutableEvaluatorOptions
	HAOptions            HAConfig

	PodNameSpace  string
	FunctionStore *functionconfigs.FunctionConfigStore

	ProbePort int
}

// Config defines the config for the apiserver
type Config struct {
	GenericConfig *genericapiserver.RecommendedConfig
	ExtraConfig   ExtraConfig
}

// serverDeps holds injectable construction dependencies for unit testing.
// Production uses defaultServerDeps(); tests override selected fields.
type serverDeps struct {
	newManager func(cfg *rest.Config, opts ctrl.Options) (manager.Manager, error)
	getCache   func(ctx context.Context, opts cachetypes.CacheOptions) (cachetypes.Cache, error)
	newSTS     func(ctx context.Context, opts ...option.ClientOption) (*sts.Service, error)
	newEngine  func(opts ...engine.EngineOption) (engine.CaDEngine, error)
	// registerFCController, when non-nil, replaces registerFunctionConfigController.
	registerFCController func(mgr manager.Manager) error
	// registerRCController, when non-nil, replaces registerRepoCacheController.
	registerRCController func(mgr manager.Manager) error
	cacheRetry           wait.Backoff
}

func defaultServerDeps() serverDeps {
	return serverDeps{
		newManager: ctrl.NewManager,
		getCache:   cache.GetCacheImpl,
		newSTS:     sts.NewService,
		newEngine:  engine.NewCaDEngine,
		cacheRetry: wait.Backoff{Duration: time.Second, Factor: 1.5, Steps: 20, Cap: 30 * time.Second},
	}
}

type completedConfig struct {
	GenericConfig genericapiserver.CompletedConfig
	ExtraConfig   *ExtraConfig
	deps          serverDeps
}

// CompletedConfig embeds a private pointer that cannot be instantiated outside of this package.
type CompletedConfig struct {
	*completedConfig
}

// Complete fills in any fields not set that are required to have valid data. It's mutating the receiver.
func (cfg *Config) Complete() CompletedConfig {
	cfg.GenericConfig.EffectiveVersion = compatibility.NewEffectiveVersionFromString("1.0", "1.0", "1.0")

	c := completedConfig{
		GenericConfig: cfg.GenericConfig.Complete(),
		ExtraConfig:   &cfg.ExtraConfig,
		deps:          defaultServerDeps(),
	}

	return CompletedConfig{&c}
}

// schemeBuilder builds a complete scheme with all necessary types
type schemeBuilder func(*runtime.Scheme) error

// buildSchemeWithTypes builds a scheme by applying all provided builders
func buildSchemeWithTypes(builders ...schemeBuilder) (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	for _, builder := range builders {
		if err := builder(scheme); err != nil {
			return nil, err
		}
	}
	return scheme, nil
}

// buildCompleteScheme returns a singleton runtime scheme with all necessary types registered
func buildCompleteScheme() (*runtime.Scheme, error) {
	var err error
	schemeOnce.Do(func() {
		completeScheme, err = buildSchemeWithTypes(
			func(s *runtime.Scheme) error {
				if e := configapi.AddToScheme(s); e != nil {
					return fmt.Errorf("error adding configapi to scheme: %w", e)
				}
				return nil
			},
			func(s *runtime.Scheme) error {
				if e := porchapi.AddToScheme(s); e != nil {
					return fmt.Errorf("error adding porchapi to scheme: %w", e)
				}
				return nil
			},
			func(s *runtime.Scheme) error {
				if e := porchv1alpha2.AddToScheme(s); e != nil {
					return fmt.Errorf("error adding porchv1alpha2 to scheme: %w", e)
				}
				return nil
			},
			func(s *runtime.Scheme) error {
				if e := corev1.AddToScheme(s); e != nil {
					return fmt.Errorf("error adding corev1 to scheme: %w", e)
				}
				return nil
			},
		)
	})
	return completeScheme, err
}

func (c *completedConfig) getRestConfig() (*rest.Config, error) {
	kubeconfig := c.ExtraConfig.CoreAPIKubeconfigPath
	if kubeconfig == "" {
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load in-cluster config (specify --kubeconfig if not running in-cluster): %w", err)
		}
		return config, nil
	}

	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})

	config, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load config %q: %w", kubeconfig, err)
	}

	config.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		return otelhttp.NewTransport(rt)
	}

	return config, nil
}

func (c *completedConfig) buildClient(restConfig *rest.Config, scheme *runtime.Scheme, reader client.Reader) (client.WithWatch, error) {
	withWatch, err := client.NewWithWatch(restConfig, client.Options{
		Scheme: scheme,
		Cache: &client.CacheOptions{
			Reader: reader,
			DisableFor: []client.Object{
				// The caching client should not cache resources served by porch-server
				&porchapi.PackageRevision{},
				&porchapi.PackageRevisionResources{},
				// PackageRev uses write-then-read patterns (Create then Get in
				// ClosePackageRevisionDraft/SetMeta). Since writes bypass the
				// informer cache, a subsequent Get can miss the just-created object.
				&configapi.PackageRev{},
				// v1alpha2 PackageRevision is a CRD patched by patchRenderRequestAnnotation
				// right after a write; bypass the cache to avoid stale reads and the
				// cluster-scope watch that the informer would require.
				&porchv1alpha2.PackageRevision{},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error building watching client: %w", err)
	}
	return withWatch, nil
}

func (c *completedConfig) buildManager(restConfig *rest.Config, scheme *runtime.Scheme, withIndex bool) (manager.Manager, error) {
	probePort := ""
	if c.ExtraConfig.ProbePort > 0 {
		probePort = fmt.Sprintf(":%d", c.ExtraConfig.ProbePort)
	}

	byObject := map[client.Object]ctrlcache.ByObject{
		// The informer should pre-cache all the repositories at startup
		&configapi.Repository{}: {},
	}

	mgr, err := c.deps.newManager(restConfig, ctrl.Options{
		Scheme:           scheme,
		LeaderElection:   c.ExtraConfig.HAOptions.LeaderElection,
		LeaderElectionID: LeaderElectionID,
		LeaseDuration:    zeroToNil(c.ExtraConfig.HAOptions.LeaseDuration),
		RenewDeadline:    zeroToNil(c.ExtraConfig.HAOptions.RenewDeadline),
		RetryPeriod:      zeroToNil(c.ExtraConfig.HAOptions.RetryPeriod),
		Cache: ctrlcache.Options{
			Scheme:   scheme,
			ByObject: byObject,
		},
		HealthProbeBindAddress: probePort,
		// Disable controller-runtime's default :8080 /metrics listener. Port 8080 is reserved for
		// optional pprof (PORCH_PPROF_PORT); controller-runtime metrics are exposed on :9464 via OpenTelemetry.
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error building manager: %w", err)
	}

	if withIndex {
		ctx := context.Background()
		if err := mgr.GetFieldIndexer().IndexField(ctx, &configapi.Repository{}, NameIndexKey, func(o client.Object) []string {
			repository := o.(*configapi.Repository)

			// Example: index by spec.ref.name (adjust to your schema)
			if repository.Name == "" {
				return nil
			}
			return []string{repository.Name}
		}); err != nil {
			return nil, fmt.Errorf("error indexing Repository by name: %w", err)
		}

		// Index for git repository location (URL+branch) to optimize conflict detection
		// This allows efficient querying of repositories by git location without listing all repos
		if err := mgr.GetFieldIndexer().IndexField(ctx, &configapi.Repository{}, "spec.git.repo", func(o client.Object) []string {
			repository := o.(*configapi.Repository)
			if repository.Spec.Git == nil || repository.Spec.Git.Repo == "" {
				return nil
			}
			return []string{repository.Spec.Git.Repo}
		}); err != nil {
			return nil, fmt.Errorf("error indexing Repository by git.repo: %w", err)
		}

		// Index for git repository branch to enable efficient conflict detection
		if err := mgr.GetFieldIndexer().IndexField(ctx, &configapi.Repository{}, "spec.git.branch", func(o client.Object) []string {
			repository := o.(*configapi.Repository)
			if repository.Spec.Git == nil || repository.Spec.Git.Branch == "" {
				return nil
			}
			return []string{repository.Spec.Git.Branch}
		}); err != nil {
			return nil, fmt.Errorf("error indexing Repository by git.branch: %w", err)
		}
	}

	return mgr, nil
}

func zeroToNil(duration time.Duration) *time.Duration {
	if duration == 0 {
		return nil
	}
	return &duration
}

func (c *completedConfig) registerFunctionConfigController(mgr manager.Manager) error {
	if c.deps.registerFCController != nil {
		return c.deps.registerFCController(mgr)
	}

	functionConfigStore := functionconfigs.NewFunctionConfigStore(
		c.ExtraConfig.GRPCRuntimeOptions.DefaultImagePrefix,
		c.ExtraConfig.ExecEvaluatorOptions.FunctionCacheDir,
	)

	controller := &functionconfigs.Reconciler{
		Client:              mgr.GetClient(),
		FunctionConfigStore: functionConfigStore,
		For:                 functionconfigs.ReconcilerForServer,
	}

	c.ExtraConfig.FunctionStore = functionConfigStore

	return controller.SetupWithManager(mgr)
}

func (c *completedConfig) registerRepoCacheController(mgr manager.Manager, cache cachetypes.Cache) error {
	if c.deps.registerRCController != nil {
		return c.deps.registerRCController(mgr)
	}
	maxConcurrency := c.ExtraConfig.CacheOptions.CRCacheOptions.MaxConcurrentLists

	if maxConcurrency <= 0 {
		maxConcurrency = 20
	}

	r := &repocache.Reconciler{
		Client:         mgr.GetClient(),
		Cache:          cache,
		MaxConcurrency: maxConcurrency,
	}

	return r.SetupWithManager(mgr)
}

func (c *completedConfig) getCoreV1Client(restConfig *rest.Config) (*corev1client.CoreV1Client, error) {
	corev1Client, err := corev1client.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("error building corev1 client: %w", err)
	}
	return corev1Client, nil
}

// New returns a new manager with PorchServer and FunctionConfigReconciler registered from the given config.
func (c *completedConfig) New(ctx context.Context) (manager.Manager, *PorchServer, error) {
	// TODO: REMOVE AFTER ASYNC IMPLEMENTATION IS READY.
	// Set the default request timeout just above hardcoded ctx timeout
	c.GenericConfig.RequestTimeout = 291 * time.Second
	genericServer, err := c.GenericConfig.New("porch-apiserver", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, nil, err
	}

	restConfig, err := c.getRestConfig()
	if err != nil {
		return nil, nil, err
	}

	// set high qps/burst limits since this will effectively limit API server responsiveness
	restConfig.QPS = 200
	restConfig.Burst = 400

	scheme, err := buildCompleteScheme()
	if err != nil {
		return nil, nil, fmt.Errorf("error building scheme: %w", err)
	}

	mgr, err := c.buildManager(restConfig, scheme, true)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build controller-runtime manager: %w", err)
	}

	if err = c.registerFunctionConfigController(mgr); err != nil {
		return nil, nil, fmt.Errorf("failed to register FunctionConfig controller: %w", err)
	}

	coreClient, err := c.buildClient(restConfig, scheme, mgr.GetCache())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build client for core apiserver: %w", err)
	}

	coreV1Client, err := c.getCoreV1Client(restConfig)
	if err != nil {
		return nil, nil, err
	}

	stsClient, err := c.deps.newSTS(context.Background(), option.WithoutAuthentication())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build sts client: %w", err)
	}

	resolverChain := []porch.Resolver{
		porch.NewBasicAuthResolver(),
		porch.NewBearerTokenAuthResolver(),
		porch.NewGcloudWIResolver(coreV1Client, stsClient),
	}

	credentialResolver := porch.NewCredentialResolver(coreClient, resolverChain)
	caBundleResolver := porch.NewCredentialResolver(coreClient, []porch.Resolver{porch.NewCaBundleResolver()})
	referenceResolver := porch.NewReferenceResolver(coreClient)
	userInfoProvider := &porch.ApiserverUserInfoProvider{}

	watcherMgr := engine.NewWatcherManager()

	c.ExtraConfig.CacheOptions.CoreClient = coreClient
	c.ExtraConfig.CacheOptions.RepoPRChangeNotifier = watcherMgr
	c.ExtraConfig.CacheOptions.ExternalRepoOptions.CredentialResolver = credentialResolver
	c.ExtraConfig.CacheOptions.ExternalRepoOptions.CaBundleResolver = caBundleResolver
	c.ExtraConfig.CacheOptions.ExternalRepoOptions.UserInfoProvider = userInfoProvider
	c.ExtraConfig.CacheOptions.ExternalRepoOptions.RepoOperationRetryAttempts = c.ExtraConfig.CacheOptions.RepoOperationRetryAttempts

	var cacheImpl cachetypes.Cache
	err = retry.OnError(
		c.deps.cacheRetry,
		func(err error) bool {
			klog.Warningf("failed to create repository cache: %v; wait a sec...", err)
			return true
		},
		func() error {
			var err error
			cacheImpl, err = c.deps.getCache(ctx, c.ExtraConfig.CacheOptions)
			return err
		})

	if err != nil {
		return nil, nil, fmt.Errorf("failed to create repository cache: %w", err)
	}

	if err = c.registerRepoCacheController(mgr, cacheImpl); err != nil {
		return nil, nil, fmt.Errorf("failed to setup repo cache controller: %w", err)
	}

	runnerOptionsResolver := func(namespace string) runneroptions.RunnerOptions {
		runnerOptions := runneroptions.RunnerOptions{}
		runnerOptions.InitDefaults(c.ExtraConfig.GRPCRuntimeOptions.DefaultImagePrefix)
		return runnerOptions
	}

	coreClientWithoutCache, err := client.NewWithWatch(restConfig, client.Options{
		Scheme: scheme,
	})

	if err != nil {
		return nil, nil, fmt.Errorf("error building watching client: %w", err)
	}

	cad, err := c.deps.newEngine(
		engine.WithCache(cacheImpl),
		engine.WithBuiltinFunctionRuntime(c.ExtraConfig.FunctionStore),
		engine.WithGRPCFunctionRuntime(c.ExtraConfig.GRPCRuntimeOptions, c.ExtraConfig.FunctionStore),
		engine.WithPodEvaluatorRuntime(ctx, c.ExtraConfig.PodEvaluatorOptions, coreClientWithoutCache, c.ExtraConfig.FunctionStore),
		engine.WithCredentialResolver(credentialResolver),
		engine.WithRunnerOptionsResolver(runnerOptionsResolver),
		engine.WithReferenceResolver(referenceResolver),
		engine.WithUserInfoProvider(userInfoProvider),
		engine.WithWatcherManager(watcherMgr),
		engine.WithRepoOperationRetryAttempts(c.ExtraConfig.CacheOptions.RepoOperationRetryAttempts),
	)
	if err != nil {
		return nil, nil, err
	}

	restStorageOptions := porch.RESTStorageOptions{
		Scheme:     Scheme,
		Codecs:     Codecs,
		CaD:        cad,
		CoreClient: coreClient,
	}
	porchGroup, err := restStorageOptions.NewRESTStorage()
	if err != nil {
		return nil, nil, err
	}

	porchServer := &PorchServer{
		GenericAPIServer: genericServer,
		coreClient:       coreClient,
		cache:            cacheImpl,
		leaderElect:      c.ExtraConfig.HAOptions.LeaderElection,
	}

	if err = porchServer.GenericAPIServer.InstallAPIGroups(&porchGroup); err != nil {
		return nil, nil, fmt.Errorf("failed to install Porch API group to PorchServer instance: %w", err)
	}

	if err = mgr.Add(porchServer); err != nil {
		return nil, nil, fmt.Errorf("failed to register PorchServer instance to manager: %w", err)
	}

	return mgr, porchServer, nil
}
