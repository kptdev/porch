package loader

import (
	"testing"
	"time"

	"github.com/kptdev/porch/config/types/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kptdev/porch/config/types"
)

func TestServerBackwardCompatibleFlags(t *testing.T) {
	var cfg types.Server
	require.NoError(t, Load(&cfg, nil, nil))
	assert.Equal(t, "CR", cfg.CacheType)
	assert.Equal(t, "pgx", cfg.DB.Driver)
	assert.Equal(t, 300, cfg.DB.MaxConnections)
	assert.Equal(t, 100, cfg.DB.MaxIdleConnections)
	assert.Equal(t, 3*time.Minute, cfg.DB.MaxConnLifetime.Duration)
	assert.Equal(t, 8, cfg.GoGitRepoCacheSize)
	assert.Equal(t, 512*datasize.KiB, cfg.GoGitCacheMaxFileSize)
	assert.Equal(t, "porch-fn-system", cfg.PodNamespace)
	assert.Equal(t, 9447, cfg.PodEvaluatorGRPCPort)
	assert.Equal(t, 3, cfg.RepoOperationRetryAttempts)
	assert.Equal(t, 10, cfg.Lists.MaxConcurrentLists)
	assert.Equal(t, 20*time.Second, cfg.Lists.ListTimeoutPerRepository.Duration)
	assert.Equal(t, time.Minute, cfg.PodEvaluator.GcScanInterval.Duration)
	assert.Equal(t, 30*time.Minute, cfg.PodEvaluator.PodTTL.Duration)
	assert.True(t, cfg.PodEvaluator.WarmUpPodCacheOnStartup)
	assert.Equal(t, 443, cfg.Serving.BindPort)
	assert.Equal(t, "apiserver.local.config/certificates", cfg.Serving.CertDir)

	t.Setenv("DB_MAX_CONNECTIONS", "9")
	t.Setenv("WRAPPER_SERVER_IMAGE", "wrapper:latest")
	var overridden types.Server
	args := []string{
		"--kubeconfig=/tmp/k",
		"--cache-type=DB",
		"--db-cache-driver=mysql",
		"--gogit-repo-cache-size=4",
		"--function-runner=127.0.0.1:9445",
		"--standalone-debug-mode=true",
		"--max-parallel-repo-lists=4",
		"--use-user-cabundle=true",
		"--scan-interval=2m",
		"--warm-up-pod-cache=false",
		"--leader-elect=true",
		"--secure-port=4443",
		"--pod-evaluator-port=9448",
	}
	require.NoError(t, Load(&overridden, nil, args))
	assert.Equal(t, "/tmp/k", overridden.CoreAPIKubeconfigPath)
	assert.Equal(t, "DB", overridden.CacheType)
	assert.Equal(t, "mysql", overridden.DB.Driver)
	assert.Equal(t, 9, overridden.DB.MaxConnections)
	assert.Equal(t, "wrapper:latest", overridden.PodEvaluator.WrapperServerImage)
	assert.Equal(t, 4, overridden.GoGitRepoCacheSize)
	assert.Equal(t, "127.0.0.1:9445", overridden.FunctionRunnerAddress)
	assert.True(t, overridden.LocalStandaloneDebugging)
	assert.Equal(t, 4, overridden.Lists.MaxConcurrentLists)
	assert.True(t, overridden.UseUserDefinedCaBundle)
	assert.Equal(t, 2*time.Minute, overridden.PodEvaluator.GcScanInterval.Duration)
	assert.False(t, overridden.PodEvaluator.WarmUpPodCacheOnStartup)
	assert.True(t, overridden.HA.LeaderElection)
	assert.Equal(t, 4443, overridden.Serving.BindPort)
	assert.Equal(t, 9448, overridden.PodEvaluatorGRPCPort)
}

func TestGlobalSharedFields(t *testing.T) {
	var cfg types.Global
	require.NoError(t, Load(&cfg, nil, nil))
	assert.Equal(t, "ghcr.io/kptdev/krm-functions-catalog", cfg.DefaultImagePrefix)
	assert.Equal(t, 6*datasize.MiB, cfg.MaxGrpcMessageSize)

	t.Setenv("DEFAULT_IMAGE_PREFIX", "example.io")
	require.NoError(t, Load(&cfg, nil, []string{"--max-request-body-size=1MiB"}))
	assert.Equal(t, "example.io", cfg.DefaultImagePrefix)
	assert.Equal(t, datasize.MiB, cfg.MaxGrpcMessageSize)
}

func TestWrapperAndTelemetryNames(t *testing.T) {
	var wrapper types.WrapperServer
	require.NoError(t, Load(&wrapper, nil, nil))
	assert.Equal(t, 9446, wrapper.Port)
	assert.Equal(t, 2, wrapper.LogLevel)

	require.NoError(t, Load(&wrapper, nil, []string{"--verbosity=4"}))
	assert.Equal(t, 4, wrapper.LogLevel)

	t.Setenv("OTEL_SERVICE_NAME", "porch")
	t.Setenv("PORCH_PPROF_PORT", "6060")
	t.Setenv("PORCH_METRICS_PACKAGE_SIZE_DETAILED", "true")
	var telemetry types.Telemetry
	require.NoError(t, Load(&telemetry, nil, nil))
	assert.Equal(t, "porch", telemetry.ServiceName)
	assert.Equal(t, 6060, telemetry.PprofPort)
	assert.True(t, telemetry.PackageSizeDetailed)
}
