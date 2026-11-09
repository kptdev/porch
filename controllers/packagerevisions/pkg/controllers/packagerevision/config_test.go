package packagerevision

import (
	"testing"

	"github.com/kptdev/porch/config/loader"
	configtypes "github.com/kptdev/porch/config/types"
	"github.com/kptdev/porch/controllers/functionconfigs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// init r only once
func newPrReconciler(t *testing.T) *PackageRevisionReconciler {
	t.Helper()

	cfg := configtypes.PackageRevisionController{}
	gcfg := configtypes.Global{}
	require.NoError(t, loader.ApplyDefaults(&cfg))
	require.NoError(t, loader.ApplyDefaults(&gcfg))

	return &PackageRevisionReconciler{
		Cfg:  cfg,
		GCfg: gcfg,

		FunctionConfigStore: functionconfigs.NewFunctionConfigStore("", ""),
	}
}

func TestInit_NilCache(t *testing.T) {
	// Cache guard is now in main.go's enableReconcilers, not in Init.
	// Init no longer checks for nil Cache — it only wires cred resolvers and renderer.
	client := fake.NewClientBuilder().Build()
	mgr := &fakeManager{client: client}

	r := newPrReconciler(t)

	err := r.Init(mgr)
	require.NoError(t, err)
}

func TestInit_SetsCredResolverAndFetcher(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	mgr := &fakeManager{client: client}

	r := newPrReconciler(t)

	err := r.Init(mgr)
	require.NoError(t, err)
	assert.NotNil(t, r.ExternalPackageFetcher, "ExternalPackageFetcher should be set")
	assert.NotNil(t, r.Renderer, "Renderer should be set (builtin-only when FUNCTION_RUNNER_ADDRESS is not set)")
}

func TestInit_RendererEnabledWithFnRunner(t *testing.T) {
	// We can't test a real gRPC connection, but we can verify Init attempts
	// to create the runtime and fails with a connection error.
	client := fake.NewClientBuilder().Build()
	mgr := &fakeManager{client: client}

	r := newPrReconciler(t)
	r.Cfg.FunctionRunnerAddress = "localhost:0"

	err := r.Init(mgr)
	// Init should succeed — NewMultiFunctionRuntime doesn't dial eagerly.
	require.NoError(t, err)
	assert.NotNil(t, r.Renderer, "Renderer should be set when FUNCTION_RUNNER_ADDRESS is provided")
}

func TestInit_RendererEnabledWithPodEvaluator(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	mgr := &fakeManager{client: client}

	r := newPrReconciler(t)
	r.Cfg.PodEvaluatorAddress = "localhost:0"

	err := r.Init(mgr)
	require.NoError(t, err)
	assert.NotNil(t, r.Renderer, "Renderer should be set when POD_EVALUATOR_ADDRESS is provided")
}
