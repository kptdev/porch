// Copyright 2022, 2024-2025 The kpt Authors
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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/porch/api/porch/install"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/functionconfigs"
	internalapi "github.com/kptdev/porch/internal/api/porchinternal/v1alpha1"
	"github.com/kptdev/porch/pkg/cache"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/engine"
	"github.com/kptdev/porch/pkg/registry/porch"
	pkgerrors "github.com/pkg/errors"
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
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const NameIndexKey = "metadata.name"

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

// ExtraConfig holds custom apiserver config
type ExtraConfig struct {
	CoreAPIKubeconfigPath string

	GRPCRuntimeOptions engine.GRPCRuntimeOptions
	CacheOptions       cachetypes.CacheOptions

	PodNameSpace  string
	FunctionStore *functionconfigs.FunctionConfigStore
}

// Config defines the config for the apiserver
type Config struct {
	GenericConfig *genericapiserver.RecommendedConfig
	ExtraConfig   ExtraConfig
}

// PorchServer contains state for a Kubernetes cluster master/api server.
type PorchServer struct {
	GenericAPIServer           *genericapiserver.GenericAPIServer
	coreClient                 client.WithWatch
	cache                      cachetypes.Cache
	periodicRepoSyncFrequency  time.Duration
	listTimeoutPerRepository   time.Duration
	repoOperationRetryAttempts int
	ExtraConfig                *ExtraConfig
}

type completedConfig struct {
	GenericConfig genericapiserver.CompletedConfig
	ExtraConfig   *ExtraConfig
}

// CompletedConfig embeds a private pointer that cannot be instantiated outside of this package.
type CompletedConfig struct {
	*completedConfig
}

// Complete fills in any fields not set that are required to have valid data. It's mutating the receiver.
func (cfg *Config) Complete() CompletedConfig {
	cfg.GenericConfig.EffectiveVersion = compatibility.NewEffectiveVersionFromString("1.0", "1.0", "1.0")

	c := completedConfig{
		cfg.GenericConfig.Complete(),
		&cfg.ExtraConfig,
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
			func(s *runtime.Scheme) error {
				if e := internalapi.AddToScheme(s); e != nil {
					return fmt.Errorf("error adding internalapi to scheme: %w", e)
				}
				return nil
			},
		)
	})
	return completeScheme, err
}

func (c completedConfig) getRestConfig() (*rest.Config, error) {
	kubeconfig := c.ExtraConfig.CoreAPIKubeconfigPath
	if kubeconfig == "" {
		icc, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load in-cluster config (specify --kubeconfig if not running in-cluster): %w", err)
		}
		return icc, nil
	} else {
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
		loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})

		cc, err := loader.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load config %q: %w", kubeconfig, err)
		}
		return cc, nil
	}
}

func (c completedConfig) buildClient(ctx context.Context) (client.WithWatch, error) {
	restConfig, err := c.getRestConfig()
	if err != nil {
		return nil, err
	}

	// set high qps/burst limits since this will effectively limit API server responsiveness
	restConfig.QPS = 200
	restConfig.Burst = 400

	scheme, err := buildCompleteScheme()
	if err != nil {
		return nil, err
	}

	informerCache, err := ctrlcache.New(restConfig, ctrlcache.Options{
		Scheme: scheme,
		ByObject: map[client.Object]ctrlcache.ByObject{
			//The informer should pre-cache all the repositories at startup
			&configapi.Repository{}: {},
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	go func() {
		if err := informerCache.Start(ctx); err != nil {
			klog.Errorf("informer cache stopped with error: %v", err)
		}
	}()

	if !informerCache.WaitForCacheSync(ctx) {
		return nil, fmt.Errorf("informer cache failed to sync")
	}

	// Register field index for metadata.name so that field selector queries on Repository work through the cache
	if err := informerCache.IndexField(ctx, &configapi.Repository{}, "metadata.name", func(obj client.Object) []string {
		return []string{obj.GetName()}
	}); err != nil {
		return nil, fmt.Errorf("failed to create index for metadata.name: %w", err)
	}

	return client.NewWithWatch(restConfig, client.Options{Scheme: scheme, Cache: &client.CacheOptions{
		Reader: informerCache,
		DisableFor: []client.Object{
			//The caching client should not cache resources served by porch-server
			&porchapi.PackageRevision{},
			&porchapi.PackageRevisionResources{},
			// PackageRev uses write-then-read patterns (Create then Get in
			// ClosePackageRevisionDraft/SetMeta). Since writes bypass the
			// informer cache, a subsequent Get can miss the just-created object.
			// This is not ideal, but crcache doesn't support a level of resources where caching makes a difference
			&internalapi.PackageRev{},
			// v1alpha2 PackageRevision is a CRD patched by patchRenderRequestAnnotation
			// right after a write; bypass the cache to avoid stale reads and the
			// cluster-scope watch that the informer would require.
			&porchv1alpha2.PackageRevision{},
		},
	}})
}

func (c completedConfig) getCoreV1Client() (*corev1client.CoreV1Client, error) {
	restConfig, err := c.getRestConfig()
	if err != nil {
		return nil, err
	}

	corev1Client, err := corev1client.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("error building corev1 client: %w", err)
	}
	return corev1Client, nil
}

func (c completedConfig) buildFunctionConfigReconciler(ctx context.Context, scheme *runtime.Scheme, withIndex bool) (*functionconfigs.FunctionConfigReconciler, error) {
	restConfig, err := c.getRestConfig()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	_ = cancel

	var cacheOpts ctrlcache.Options
	cacheOpts.Scheme = scheme
	//cacheOpts.DefaultNamespaces = map[string]cache.Config{o.exec.PodNamespace: {}}

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: scheme,
		Cache:  cacheOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("error at creating manager: %w", err)
	}

	if withIndex {
		if err := mgr.GetFieldIndexer().IndexField(ctx, &configapi.Repository{}, NameIndexKey, func(o client.Object) []string {
			repository := o.(*configapi.Repository)

			// Example: index by spec.ref.name (adjust to your schema)
			if repository.Name == "" {
				return nil
			}
			return []string{repository.Name}
		}); err != nil {
			return nil, err
		}
	}

	functionConfigStore := functionconfigs.NewStore(c.ExtraConfig.GRPCRuntimeOptions.DefaultImagePrefix, "")

	rec := &functionconfigs.FunctionConfigReconciler{
		Client:              mgr.GetClient(),
		FunctionConfigStore: functionConfigStore,
		For:                 functionconfigs.ReconcilerForServer,
	}

	c.ExtraConfig.FunctionStore = functionConfigStore

	err = ctrl.NewControllerManagedBy(mgr).
		For(&configapi.FunctionConfig{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(rec)
	if err != nil {
		return nil, fmt.Errorf("error at creating controller: %w", err)
	}

	go func() {
		if err := mgr.Start(ctx); err != nil {
			klog.Infof("manager stopped: %v", err)
		}
	}()

	return rec, nil
}

// New returns a new instance of PorchServer from the given config.
func (c completedConfig) New(ctx context.Context) (*PorchServer, error) {
	// TODO: REMOVE AFTER ASYNC IMPLEMENTATION IS READY.
	// Set the default request timeout just above hardcoded ctx timeout
	c.GenericConfig.RequestTimeout = 291 * time.Second
	genericServer, err := c.GenericConfig.New("porch-apiserver", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}

	coreClient, err := c.buildClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to build client for core apiserver: %w", err)
	}

	fnConfigReconciler, err := c.buildFunctionConfigReconciler(ctx, coreClient.Scheme(), true)
	if err != nil {
		return nil, fmt.Errorf("failed to build function config reconciler: %w", err)
	}

	c.ExtraConfig.FunctionStore = fnConfigReconciler.FunctionConfigStore

	coreV1Client, err := c.getCoreV1Client()
	if err != nil {
		return nil, err
	}

	stsClient, err := sts.NewService(context.Background(), option.WithoutAuthentication())
	if err != nil {
		return nil, fmt.Errorf("failed to build sts client: %w", err)
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
		wait.Backoff{Duration: time.Second, Factor: 1.5, Steps: 20, Cap: 30 * time.Second},
		func(err error) bool {
			klog.Warningf("failed to create repository cache: %v; wait a sec...", err)
			return true
		},
		func() error {
			var err error
			cacheImpl, err = cache.GetCacheImpl(ctx, c.ExtraConfig.CacheOptions)
			return err
		})

	if err != nil {
		return nil, pkgerrors.Wrap(err, "failed to create repository cache")
	}

	runnerOptionsResolver := func(namespace string) runneroptions.RunnerOptions {
		runnerOptions := runneroptions.RunnerOptions{}
		runnerOptions.InitDefaults(c.ExtraConfig.GRPCRuntimeOptions.DefaultImagePrefix)
		return runnerOptions
	}

	cad, err := engine.NewCaDEngine(
		engine.WithCache(cacheImpl),
		// The order of registering the function runtimes matters here. When
		// evaluating a function, the runtimes will be tried in the same
		// order as they are registered.
		engine.WithBuiltinFunctionRuntime(c.ExtraConfig.FunctionStore),
		engine.WithGRPCFunctionRuntime(c.ExtraConfig.GRPCRuntimeOptions),
		engine.WithCredentialResolver(credentialResolver),
		engine.WithRunnerOptionsResolver(runnerOptionsResolver),
		engine.WithReferenceResolver(referenceResolver),
		engine.WithUserInfoProvider(userInfoProvider),
		engine.WithWatcherManager(watcherMgr),
		engine.WithRepoOperationRetryAttempts(c.ExtraConfig.CacheOptions.RepoOperationRetryAttempts),
	)
	if err != nil {
		return nil, err
	}

	restStorageOptions := porch.RESTStorageOptions{
		Scheme:     Scheme,
		Codecs:     Codecs,
		CaD:        cad,
		CoreClient: coreClient,
	}
	porchGroup, err := restStorageOptions.NewRESTStorage()
	if err != nil {
		return nil, err
	}

	s := &PorchServer{
		GenericAPIServer: genericServer,
		coreClient:       coreClient,
		cache:            cacheImpl,
		// Set background job periodic frequency the same as repo sync frequency.
		periodicRepoSyncFrequency:  c.ExtraConfig.CacheOptions.RepoSyncFrequency,
		listTimeoutPerRepository:   c.ExtraConfig.CacheOptions.CRCacheOptions.ListTimeoutPerRepository,
		repoOperationRetryAttempts: c.ExtraConfig.CacheOptions.RepoOperationRetryAttempts,
	}

	// Install the groups.
	if err := s.GenericAPIServer.InstallAPIGroups(&porchGroup); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *PorchServer) Run(ctx context.Context) error {
	// TODO: Reconsider if the existence of CERT_STORAGE_DIR was a good inidcator for webhook setup,
	// but for now we keep backward compatiblity
	certStorageDir, found := os.LookupEnv("CERT_STORAGE_DIR")
	if found && strings.TrimSpace(certStorageDir) != "" {
		if err := setupWebhooks(ctx, s.coreClient); err != nil {
			klog.Errorf("%v\n", err)
			return err
		}
	} else {
		klog.Infoln("Cert storage dir not provided, skipping webhook setup")
	}
	return s.GenericAPIServer.PrepareRun().RunWithContext(ctx)
}
