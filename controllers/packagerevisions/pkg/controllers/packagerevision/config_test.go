package packagerevision

import (
	"context"
	"errors"
	"flag"
	"io"
	"testing"
	"time"

	"github.com/kptdev/porch/controllers/functionconfigs"
	"github.com/kptdev/porch/pkg/util/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestInitDefaults(t *testing.T) {
	r := &PackageRevisionReconciler{}
	r.InitDefaults()

	assert.Equal(t, defaultMaxConcurrentReconciles, r.MaxConcurrentReconciles)
	assert.Equal(t, defaultMaxConcurrentRenders, r.MaxConcurrentRenders)
	assert.Equal(t, defaultRepoOperationRetryAttempts, r.RepoOperationRetryAttempts)
	assert.Equal(t, defaultMaxGRPCMessageSize, r.MaxGRPCMessageSize)
	assert.Equal(t, defaultTransientErrorRetryDeadline, r.TransientErrorRetryDeadline)
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
		"--pr-transient-error-retry-deadline=30s",
	})
	require.NoError(t, err)

	assert.Equal(t, 10, r.MaxConcurrentReconciles)
	assert.Equal(t, 5, r.MaxConcurrentRenders)
	assert.Equal(t, 7, r.RepoOperationRetryAttempts)
	assert.Equal(t, 10485760, r.MaxGRPCMessageSize)
	assert.Equal(t, 30*time.Second, r.TransientErrorRetryDeadline)
}

func TestBindFlagsMaxGRPCMessageSize(t *testing.T) {
	testCases := map[string]struct {
		value       string
		expected    int
		expectedErr string
	}{
		"size with unit":  {value: "10MB", expected: 10 * datasize.MB},
		"number of bytes": {value: "10485760", expected: 10 * datasize.MB},
		"invalid size":    {value: "large", expectedErr: `invalid data size "large"`},
	}
	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			r := &PackageRevisionReconciler{}
			flags := flag.NewFlagSet("test", flag.ContinueOnError)
			flags.SetOutput(io.Discard)
			r.BindFlags("pr-", flags)

			err := flags.Parse([]string{"--pr-max-grpc-message-size=" + tc.value})
			if tc.expectedErr != "" {
				assert.ErrorContains(t, err, tc.expectedErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expected, r.MaxGRPCMessageSize)
		})
	}
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
	assert.Equal(t, defaultTransientErrorRetryDeadline, r.TransientErrorRetryDeadline)
}

func TestInit_NilCache(t *testing.T) {
	// Cache guard is now in main.go's enableReconcilers, not in Init.
	// Init no longer checks for nil Cache — it only wires cred resolvers and renderer.
	client := fake.NewClientBuilder().Build()
	mgr := &fakeManager{client: client}

	r := &PackageRevisionReconciler{
		RepoOperationRetryAttempts: 3,
		MaxGRPCMessageSize:         defaultMaxGRPCMessageSize,
		FunctionConfigStore:        functionconfigs.NewFunctionConfigStore("", ""),
	}
	err := r.Init(mgr)
	require.NoError(t, err)
}

func TestInit_StartupWebhookCheckIsNonFatal(t *testing.T) {
	fail := admissionregistrationv1.Fail
	tests := []struct {
		name string
		objs []client.Object
	}{
		{
			name: "registration present",
			objs: []client.Object{webhookConfig(packageRevisionValidatorName, &fail)},
		},
		{
			name: "registration missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := &fakeManager{client: fake.NewClientBuilder().WithObjects(tt.objs...).Build()}
			r := &PackageRevisionReconciler{
				RepoOperationRetryAttempts: 3,
				MaxGRPCMessageSize:         defaultMaxGRPCMessageSize,
				FunctionConfigStore:        functionconfigs.NewFunctionConfigStore("", ""),
			}
			require.NoError(t, r.Init(mgr))
			require.Len(t, mgr.runnables, 1, "Init registers one startup check")

			// A missing registration is logged, not returned, so the manager keeps running.
			assert.NoError(t, mgr.runnables[0].Start(context.Background()))
		})
	}
}

func TestInit_StartupRunnableRegistrationErrorIsReturned(t *testing.T) {
	mgr := &fakeManager{client: fake.NewClientBuilder().Build(), addErr: errors.New("manager stopped")}
	r := &PackageRevisionReconciler{
		RepoOperationRetryAttempts: 3,
		MaxGRPCMessageSize:         defaultMaxGRPCMessageSize,
		FunctionConfigStore:        functionconfigs.NewFunctionConfigStore("", ""),
	}
	assert.ErrorContains(t, r.Init(mgr), "manager stopped")
}

func TestInit_SetsCredResolverAndFetcher(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	mgr := &fakeManager{client: client}

	r := &PackageRevisionReconciler{
		RepoOperationRetryAttempts: 3,
		MaxGRPCMessageSize:         defaultMaxGRPCMessageSize,
		FunctionConfigStore:        functionconfigs.NewFunctionConfigStore("", ""),
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
		FunctionConfigStore:        functionconfigs.NewFunctionConfigStore("", ""),
	}

	t.Setenv("FUNCTION_RUNNER_ADDRESS", "localhost:0")

	err := r.Init(mgr)
	// Init should succeed — NewMultiFunctionRuntime doesn't dial eagerly.
	require.NoError(t, err)
	assert.NotNil(t, r.Renderer, "Renderer should be set when FUNCTION_RUNNER_ADDRESS is provided")
}

func TestInit_RendererEnabledWithPodEvaluator(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	mgr := &fakeManager{client: client}

	r := &PackageRevisionReconciler{
		RepoOperationRetryAttempts: 3,
		MaxGRPCMessageSize:         defaultMaxGRPCMessageSize,
		FunctionConfigStore:        functionconfigs.NewFunctionConfigStore("", ""),
	}

	t.Setenv("POD_EVALUATOR_ADDRESS", "localhost:0")

	err := r.Init(mgr)
	require.NoError(t, err)
	assert.NotNil(t, r.Renderer, "Renderer should be set when POD_EVALUATOR_ADDRESS is provided")
}
