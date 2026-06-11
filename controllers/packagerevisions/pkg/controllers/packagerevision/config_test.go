package packagerevision

import (
	"flag"
	"testing"

	"github.com/kptdev/porch/controllers/functionconfigs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestInitDefaults(t *testing.T) {
	r := &PackageRevisionReconciler{}
	r.InitDefaults()

	assert.Equal(t, defaultMaxConcurrentReconciles, r.MaxConcurrentReconciles)
	assert.Equal(t, defaultMaxConcurrentRenders, r.MaxConcurrentRenders)
	assert.Equal(t, defaultRepoOperationRetryAttempts, r.RepoOperationRetryAttempts)
	assert.Equal(t, defaultMaxGRPCMessageSize, r.MaxGRPCMessageSize)
}

func TestBindFlags(t *testing.T) {
	r := &PackageRevisionReconciler{}
	flags := flag.NewFlagSet("test", flag.ContinueOnError)

	r.BindFlags("pr-", flags)

	err := flags.Parse([]string{
		"--pr-max-concurrent-reconciles=10",
		"--pr-max-concurrent-renders=5",
		"--pr-repo-operation-retry-attempts=7",
		"--pr-max-grpc-message-size=10485760",
	})
	require.NoError(t, err)

	assert.Equal(t, 10, r.MaxConcurrentReconciles)
	assert.Equal(t, 5, r.MaxConcurrentRenders)
	assert.Equal(t, 7, r.RepoOperationRetryAttempts)
	assert.Equal(t, 10485760, r.MaxGRPCMessageSize)
}

func TestBindFlagsDefaults(t *testing.T) {
	r := &PackageRevisionReconciler{}
	flags := flag.NewFlagSet("test", flag.ContinueOnError)

	r.BindFlags("pr-", flags)
	require.NoError(t, flags.Parse([]string{}))

	assert.Equal(t, defaultMaxConcurrentReconciles, r.MaxConcurrentReconciles)
	assert.Equal(t, defaultMaxConcurrentRenders, r.MaxConcurrentRenders)
	assert.Equal(t, defaultRepoOperationRetryAttempts, r.RepoOperationRetryAttempts)
	assert.Equal(t, defaultMaxGRPCMessageSize, r.MaxGRPCMessageSize)
}

func TestInit_NilCache(t *testing.T) {
	// Cache guard is now in main.go's enableReconcilers, not in Init.
	// Init no longer checks for nil Cache — it only wires cred resolvers and renderer.
	client := fake.NewClientBuilder().Build()
	mgr := &fakeManager{client: client}

	r := &PackageRevisionReconciler{
		RepoOperationRetryAttempts: 3,
		MaxGRPCMessageSize:         defaultMaxGRPCMessageSize,
		FunctionConfigStore:        functionconfigs.NewStore("", ""),
	}
	err := r.Init(mgr)
	require.NoError(t, err)
}

func TestInit_SetsCredResolverAndFetcher(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	mgr := &fakeManager{client: client}

	r := &PackageRevisionReconciler{
		RepoOperationRetryAttempts: 3,
		MaxGRPCMessageSize:         defaultMaxGRPCMessageSize,
		FunctionConfigStore:        functionconfigs.NewStore("", ""),
	}

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

	r := &PackageRevisionReconciler{
		RepoOperationRetryAttempts: 3,
		MaxGRPCMessageSize:         defaultMaxGRPCMessageSize,
		FunctionConfigStore:        functionconfigs.NewStore("", ""),
	}

	t.Setenv("FUNCTION_RUNNER_ADDRESS", "localhost:0")

	err := r.Init(mgr)
	// Init should succeed — NewMultiFunctionRuntime doesn't dial eagerly.
	require.NoError(t, err)
	assert.NotNil(t, r.Renderer, "Renderer should be set when FUNCTION_RUNNER_ADDRESS is provided")
}
