// Copyright 2022-2026 The kpt Authors
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

package main

//go:generate go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.21.0 rbac:headerFile=../scripts/boilerplate.yaml.txt,roleName=porch-controllers,year=$YEAR_GEN webhook paths="."

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"slices"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"

	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/porch/controllers/functionconfigs/reconciler"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/controllers/packagerevision"
	"github.com/kptdev/porch/controllers/packagevariants/pkg/controllers/packagevariant"
	"github.com/kptdev/porch/controllers/packagevariantsets/pkg/controllers/packagevariantset"
	"github.com/kptdev/porch/controllers/repositories/pkg/controllers/repository"
	"github.com/kptdev/porch/pkg/cache/contentcache"
	"github.com/kptdev/porch/pkg/controllerrestmapper"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/internal/telemetry"
	//+kubebuilder:scaffold:imports
)

const errInitScheme = "error initializing scheme: %w"

var (
	// repoReconciler and prReconciler are declared separately so main can
	// inject the shared cache: prReconciler.Cache = repoReconciler.Cache.
	// Repo must be set up first because it creates the cache.
	repoReconciler = &repository.RepositoryReconciler{}
	prReconciler   = &packagerevision.PackageRevisionReconciler{}

	reconcilers = buildReconcilerMap(
		repoReconciler,
		prReconciler,
		&packagevariant.PackageVariantReconciler{},
		&packagevariantset.PackageVariantSetReconciler{},
	)
)

// Reconciler is the interface implemented by (our) reconcilers, which includes some configuration and initialization.
type Reconciler interface {
	reconcile.Reconciler

	// Name returns the reconciler's unique name (used as map key, flag prefix, logger name).
	Name() string

	// InitDefaults populates default values into our options
	InitDefaults()

	// BindFlags binds options to flags
	BindFlags(prefix string, flags *flag.FlagSet)

	// SetupWithManager registers the reconciler to run under the specified manager
	SetupWithManager(ctrl.Manager) error
}

// Initializer is an optional interface for reconcilers that need to wire
// runtime dependencies (caches, credential resolvers, etc.) after the
// manager is created but before SetupWithManager.
type Initializer interface {
	Init(ctrl.Manager) error
}

// We include our lease / events permissions in the main RBAC role

//+kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	enabledReconcilersString := parseFlags()

	scheme, err := initScheme()
	if err != nil {
		return err
	}

	otel.SetLogger(klog.NewKlogr())
	otelResources, err := telemetry.SetupOpenTelemetry(ctx)
	if err != nil {
		return fmt.Errorf("error setting up OpenTelemetry: %w", err)
	}
	defer func() {
		if shutdownErr := otelResources.ShutdownWithTimeout(10 * time.Second); shutdownErr != nil {
			klog.Warningf("failed to gracefully shutdown OpenTelemetry: %v", shutdownErr)
		}
	}()

	mgr, err := newManager(scheme)
	if err != nil {
		return err
	}

	if err := enableReconcilers(mgr, enabledReconcilersString); err != nil {
		return err
	}

	//+kubebuilder:scaffold:builder
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("error adding health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("error adding ready check: %w", err)
	}

	klog.Infof("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		return fmt.Errorf("error running manager: %w", err)
	}
	return nil
}

// --- Flag parsing ---

func parseFlags() string {
	var enabledReconcilersString string

	for _, reconciler := range reconcilers {
		reconciler.InitDefaults()
	}

	klog.InitFlags(nil)

	flag.StringVar(&enabledReconcilersString, "reconcilers", "", "reconcilers that should be enabled; use * to mean 'enable all'")

	for name, reconciler := range reconcilers {
		reconciler.BindFlags(name+".", flag.CommandLine)
	}

	flag.Parse()

	return enabledReconcilersString
}

// --- Scheme ---

func initScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	for _, addToScheme := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		porchapi.AddToScheme,
		porchv1alpha2.AddToScheme,
		configapi.AddToScheme,
	} {
		if err := addToScheme(scheme); err != nil {
			return nil, fmt.Errorf(errInitScheme, err)
		}
	}
	return scheme, nil
}

// --- Manager ---

func newManager(scheme *runtime.Scheme) (ctrl.Manager, error) {
	config := textlogger.NewConfig(
		textlogger.Verbosity(4),
		textlogger.Output(os.Stdout),
	)
	ctrl.SetLogger(textlogger.NewLogger(config))

	cfg := ctrl.GetConfigOrDie()
	cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		klog.Infof("Wrapping client-go transport with OpenTelemetry")
		return otelhttp.NewTransport(rt)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    9443,
			CertDir: "/etc/webhook/certs",
		}),
		HealthProbeBindAddress:     ":8081",
		LeaderElection:             false,
		LeaderElectionID:           "porch-operators.config.porch.kpt.dev",
		LeaderElectionResourceLock: resourcelock.LeasesResourceLock,
		MapperProvider:             controllerrestmapper.New,
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{
					&porchapi.PackageRevisionResources{}},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating manager: %w", err)
	}
	return mgr, nil
}

// --- Reconciler setup ---

func enableReconcilers(mgr ctrl.Manager, enabledReconcilersString string) error {
	enabled := strings.Split(enabledReconcilersString, ",")
	var started []string

	// Set up repo controller first — it creates the shared cache.
	started, err := setupReconciler(mgr, enabled, repoReconciler, started)
	if err != nil {
		return err
	}

	// Wire shared cache into PR controller before setup.
	if reconcilerIsEnabled(enabled, prReconciler.Name()) {
		if repoReconciler.Cache == nil {
			return fmt.Errorf("%s reconciler requires %s reconciler (shared cache)", prReconciler.Name(), repoReconciler.Name())
		}
		prReconciler.ContentCache = contentcache.NewContentCache(repoReconciler.Cache)

		functionConfigStore, err := setupFunctionConfigReconciler(mgr)
		if err != nil {
			return err
		}
		prReconciler.FunctionConfigStore = functionConfigStore
	}
	started, err = setupReconciler(mgr, enabled, prReconciler, started)
	if err != nil {
		return err
	}

	// Set up remaining controllers (no ordering dependency).
	for _, reconciler := range reconcilers {
		if reconciler.Name() == repoReconciler.Name() || reconciler.Name() == prReconciler.Name() {
			continue
		}
		started, err = setupReconciler(mgr, enabled, reconciler, started)
		if err != nil {
			return err
		}
	}

	if len(started) == 0 {
		klog.Warningf("no reconcilers are enabled; did you forget to pass the --reconcilers flag?")
	} else {
		klog.Infof("enabled reconcilers: %v", strings.Join(started, ","))
	}
	return nil
}

func setupReconciler(mgr ctrl.Manager, enabled []string, r Reconciler, started []string) ([]string, error) {
	if !reconcilerIsEnabled(enabled, r.Name()) {
		return started, nil
	}
	name := r.Name()
	if init, ok := r.(Initializer); ok {
		if err := init.Init(mgr); err != nil {
			return started, fmt.Errorf("error initializing %s reconciler: %w", name, err)
		}
	}
	ctrl.Log.WithName(name).Info("setting up controller")
	if err := r.SetupWithManager(mgr); err != nil {
		return started, fmt.Errorf("error creating %s reconciler: %w", name, err)
	}
	return append(started, name), nil
}

func setupFunctionConfigReconciler(mgr ctrl.Manager) (*reconciler.FunctionConfigStore, error) {
	prefix := os.Getenv("DEFAULT_IMAGE_PREFIX")
	if prefix == "" {
		prefix = runneroptions.GHCRImagePrefix
	}
	functionConfigStore := reconciler.NewFunctionConfigStore(prefix, "")

	rec := &reconciler.FunctionConfigReconciler{
		Client:              mgr.GetClient(),
		FunctionConfigStore: functionConfigStore,
		For:                 reconciler.ReconcilerForController,
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&configapi.FunctionConfig{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(rec); err != nil {
		return nil, fmt.Errorf("error creating FunctionConfig controller: %w", err)
	}

	prePopulateFunctionConfigStore(mgr.GetAPIReader(), functionConfigStore)

	klog.Infof("FunctionConfig reconciler registered (for: %s)", reconciler.ReconcilerForController)
	return functionConfigStore, nil
}

// prePopulateFunctionConfigStore loads all FunctionConfigs into the store
// synchronously so the exec cache is ready before the PR controller starts.
// Without this, a pod restart leaves the cache empty until the async
// informer triggers reconciliation.
func prePopulateFunctionConfigStore(reader client.Reader, store *reconciler.FunctionConfigStore) {
	var fcList configapi.FunctionConfigList
	if err := reader.List(context.Background(), &fcList); err != nil {
		klog.Warningf("FunctionConfig pre-population failed (non-fatal): %v", err)
		return
	}
	for i := range fcList.Items {
		obj := &fcList.Items[i]
		store.UpsertFunctionConfig(obj.Name, obj)
		if obj.Spec.GoExecutor != nil {
			store.UpdateExecCache(obj.Name, obj)
		}
		if obj.Spec.BinaryExecutor != nil {
			store.UpdateBinaryCache(obj.Name, obj)
		}
	}
	klog.Infof("FunctionConfig store pre-populated with %d configs", len(fcList.Items))
}

// --- Helpers ---

func buildReconcilerMap(reconcilers ...Reconciler) map[string]Reconciler {
	m := make(map[string]Reconciler, len(reconcilers))
	for _, r := range reconcilers {
		m[r.Name()] = r
	}
	return m
}

func reconcilerIsEnabled(reconcilers []string, reconciler string) bool {
	if slices.Contains(reconcilers, "*") || slices.Contains(reconcilers, reconciler) {
		return true
	}
	envVar := fmt.Sprintf("ENABLE_%s", strings.ToUpper(reconciler))
	val := strings.ToLower(os.Getenv(envVar))
	return val == "true" || val == "1" || val == "yes"
}
