// Copyright 2023, 2025 The kpt Authors
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
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/apiserver"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

func TestAddFlags(t *testing.T) {
	versions := schema.GroupVersions{
		porchapi.SchemeGroupVersion,
	}
	o := PorchServerOptions{
		RecommendedOptions: genericoptions.NewRecommendedOptions(
			defaultEtcdPathPrefix,
			apiserver.Codecs.LegacyCodec(versions...),
		),
	}
	assert.NotPanics(t, func() {
		o.AddFlags(&pflag.FlagSet{})
	})
	// Test passes if AddFlags doesn't panic
}

func TestValidate(t *testing.T) {
	opts := NewPorchServerOptions(os.Stdout, os.Stderr)

	err := opts.Validate(nil)
	assert.Error(t, err)

	opts.CacheType = "CR"
	err = opts.Validate(nil)
	assert.NoError(t, err)

	opts.CacheType = "cr"
	err = opts.Validate(nil)
	assert.NoError(t, err)

	opts.CacheType = "DB"
	err = opts.Validate(nil)
	assert.NoError(t, err)

	opts.CacheType = ""
	err = opts.Validate(nil)
	assert.Error(t, err)
}

func TestSetupDBCacheConn(t *testing.T) {
	// Default expected connection pooling values
	defaultMaxConns := 300
	defaultMaxIdleConns := 100
	defaultMaxConnLifetime := 3 * time.Minute

	tests := []struct {
		name                    string
		envVars                 map[string]string
		expectError             bool
		errorContains           string
		expectedDriver          string
		expectedDataSource      string
		expectedMaxConns        int
		expectedMaxIdleConns    int
		expectedMaxConnLifetime time.Duration
	}{
		{
			name: "missing required environment variables",
			envVars: map[string]string{
				"DB_HOST": "", // Empty value should trigger missing var error
				"DB_PORT": "",
				"DB_NAME": "",
			},
			expectError:   true,
			errorContains: "missing required environment variables",
		},
		{
			name: "unsupported DB driver",
			envVars: map[string]string{
				"DB_DRIVER": "db-driver",
			},
			expectError:   true,
			errorContains: "unsupported DB driver: db-driver",
		},
		{
			name:                    "pgx driver with defaults",
			envVars:                 map[string]string{},
			expectError:             false,
			expectedDriver:          "pgx",
			expectedDataSource:      "postgres://db-user:db-password@db-host:db-port/db-name?sslmode=disable",
			expectedMaxConns:        defaultMaxConns,
			expectedMaxIdleConns:    defaultMaxIdleConns,
			expectedMaxConnLifetime: defaultMaxConnLifetime,
		},
		{
			name: "mysql driver",
			envVars: map[string]string{
				"DB_DRIVER": "mysql",
			},
			expectError:             false,
			expectedDriver:          "mysql",
			expectedDataSource:      "db-user:db-password@tcp(db-host:db-port)/db-name",
			expectedMaxConns:        defaultMaxConns,
			expectedMaxIdleConns:    defaultMaxIdleConns,
			expectedMaxConnLifetime: defaultMaxConnLifetime,
		},
		{
			name: "pgx with SSL mode",
			envVars: map[string]string{
				"DB_SSL_MODE": "verify-full",
			},
			expectError:             false,
			expectedDriver:          "pgx",
			expectedDataSource:      "postgres://db-user@db-host:db-port/db-name?sslmode=verify-full",
			expectedMaxConns:        defaultMaxConns,
			expectedMaxIdleConns:    defaultMaxIdleConns,
			expectedMaxConnLifetime: defaultMaxConnLifetime,
		},
		{
			name: "custom connection pooling values",
			envVars: map[string]string{
				"DB_MAX_CONNECTIONS":      "50",
				"DB_MAX_IDLE_CONNECTIONS": "25",
				"DB_MAX_CONN_LIFETIME":    "5m",
			},
			expectError:             false,
			expectedDriver:          "pgx",
			expectedDataSource:      "postgres://db-user:db-password@db-host:db-port/db-name?sslmode=disable",
			expectedMaxConns:        50,
			expectedMaxIdleConns:    25,
			expectedMaxConnLifetime: 5 * time.Minute,
		},
		{
			name: "invalid connection pooling values use defaults",
			envVars: map[string]string{
				"DB_MAX_CONNECTIONS":      "invalid",
				"DB_MAX_IDLE_CONNECTIONS": "not-a-number",
				"DB_MAX_CONN_LIFETIME":    "bad-duration",
			},
			expectError:             false,
			expectedDriver:          "pgx",
			expectedDataSource:      "postgres://db-user:db-password@db-host:db-port/db-name?sslmode=disable",
			expectedMaxConns:        defaultMaxConns,
			expectedMaxIdleConns:    defaultMaxIdleConns,
			expectedMaxConnLifetime: defaultMaxConnLifetime,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all DB-related environment variables
			dbEnvVars := []string{
				"DB_DRIVER", "DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD", "DB_SSL_MODE",
				"DB_MAX_CONNECTIONS", "DB_MAX_IDLE_CONNECTIONS", "DB_MAX_CONN_LIFETIME",
			}
			for _, envVar := range dbEnvVars {
				os.Unsetenv(envVar)
			}

			// Set default required environment variables
			os.Setenv("DB_HOST", "db-host")
			os.Setenv("DB_PORT", "db-port")
			os.Setenv("DB_NAME", "db-name")
			os.Setenv("DB_USER", "db-user")
			os.Setenv("DB_PASSWORD", "db-password")

			// Set test-specific environment variables (can override defaults)
			for key, value := range tt.envVars {
				os.Setenv(key, value)
			}

			opts := NewPorchServerOptions(os.Stdout, os.Stderr)
			opts.CacheType = "DB"

			err := opts.Complete()

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedDriver, opts.DbCacheDriver)
				assert.Equal(t, tt.expectedDataSource, opts.DbCacheDataSource)
				assert.Equal(t, tt.expectedMaxConns, opts.DbMaxConnections)
				assert.Equal(t, tt.expectedMaxIdleConns, opts.DbMaxIdleConnections)
				assert.Equal(t, tt.expectedMaxConnLifetime, opts.DbMaxConnLifetime)
			}

			// Clean up
			for _, envVar := range dbEnvVars {
				os.Unsetenv(envVar)
			}
		})
	}
}

func TestComplete(t *testing.T) {
	opts := NewPorchServerOptions(os.Stdout, os.Stderr)

	opts.RetryableGitErrors = []string{" git error 1 ", "git error 2", " git error 3"}
	assert.Len(t, opts.RetryableGitErrors, 3)

	err := opts.Complete()
	assert.NoError(t, err)
}

func TestHAFlagParsing(t *testing.T) {
	opts := NewPorchServerOptions(os.Stdout, os.Stderr)
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	opts.AddFlags(fs)
	require.NoError(t, fs.Parse([]string{
		"--probe-port=4453",
		"--leader-elect=true",
		"--leader-lease-duration=15s",
	}))
	assert.Equal(t, 4453, opts.ProbePort)
	assert.True(t, opts.HAOptions.LeaderElection)
	assert.Equal(t, 15*time.Second, opts.HAOptions.LeaseDuration)
}

func TestDelegateAPIServerHealthStandby(t *testing.T) {
	mgr := &stubProbeManager{elected: make(chan struct{})}
	client := &http.Client{}

	liveness := delegateAPIServerHealth(mgr, client, "https://example.invalid", "livez", true)
	assert.NoError(t, liveness(nil))

	readiness := delegateAPIServerHealth(mgr, client, "https://example.invalid", "readyz", false)
	assert.ErrorContains(t, readiness(nil), "not leader")
}

func TestValidateMaxConcurrentLists(t *testing.T) {
	opts := NewPorchServerOptions(os.Stdout, os.Stderr)
	opts.CacheType = "CR"
	opts.MaxConcurrentLists = -1

	err := opts.Validate(nil)
	assert.ErrorContains(t, err, "invalid value for max-parallel-repo-lists")
}

func TestCompleteSetsDefaultCacheDirectory(t *testing.T) {
	opts := NewPorchServerOptions(os.Stdout, os.Stderr)
	opts.CacheDirectory = ""

	require.NoError(t, opts.Complete())
	assert.NotEmpty(t, opts.CacheDirectory)
	assert.True(t, strings.HasSuffix(opts.CacheDirectory, "/porch"))
}

func TestCompleteUppercasesCacheType(t *testing.T) {
	opts := NewPorchServerOptions(os.Stdout, os.Stderr)
	opts.CacheType = "cr"

	require.NoError(t, opts.Complete())
	assert.Equal(t, "CR", opts.CacheType)
}

func TestNewPorchServerOptionsDefaults(t *testing.T) {
	opts := NewPorchServerOptions(os.Stdout, os.Stderr)

	assert.Nil(t, opts.RecommendedOptions.Etcd)
	assert.Equal(t, 0, opts.ProbePort)
	assert.False(t, opts.HAOptions.LeaderElection)
}

func TestBuildExtraConfig(t *testing.T) {
	opts := NewPorchServerOptions(os.Stdout, os.Stderr)
	opts.CoreAPIKubeconfigPath = "/tmp/kubeconfig"
	opts.FunctionRunnerAddress = "function-runner:9445"
	opts.MaxRequestBodySize = 1234
	opts.DefaultImagePrefix = "example.com/"
	opts.CacheDirectory = "/cache"
	opts.UseUserDefinedCaBundle = true
	opts.GoGitRepoCacheSize = 16
	opts.GoGitCacheMaxFileSize = 1024
	opts.RepoOperationRetryAttempts = 5
	opts.CacheType = "DB"
	opts.MaxConcurrentLists = 7
	opts.ListTimeoutPerRepository = 30 * time.Second
	opts.DbCacheDriver = "pgx"
	opts.DbCacheDataSource = "postgres://x"
	opts.DbMaxConnections = 10
	opts.DbMaxIdleConnections = 5
	opts.DbMaxConnLifetime = time.Minute
	opts.DbPushDrafsToGit = true
	opts.PodNamespace = "test-ns"
	opts.ProbePort = 4453
	opts.HAOptions = apiserver.HAConfig{LeaderElection: true, LeaseDuration: 15 * time.Second}

	extra := opts.buildExtraConfig()

	assert.Equal(t, "/tmp/kubeconfig", extra.CoreAPIKubeconfigPath)
	assert.Equal(t, "function-runner:9445", extra.GRPCRuntimeOptions.FunctionRunnerAddress)
	assert.Equal(t, 1234, extra.GRPCRuntimeOptions.MaxGrpcMessageSize)
	assert.Equal(t, "example.com/", extra.GRPCRuntimeOptions.DefaultImagePrefix)
	assert.Equal(t, "/cache", extra.CacheOptions.ExternalRepoOptions.LocalDirectory)
	assert.True(t, extra.CacheOptions.ExternalRepoOptions.UseUserDefinedCaBundle)
	assert.Equal(t, 16, extra.CacheOptions.ExternalRepoOptions.GoGitRepoCacheSize)
	assert.Equal(t, int64(1024), extra.CacheOptions.ExternalRepoOptions.GoGitCacheMaxFileSize)
	assert.Equal(t, 5, extra.CacheOptions.RepoOperationRetryAttempts)
	assert.Equal(t, cachetypes.CacheType("DB"), extra.CacheOptions.CacheType)
	assert.Equal(t, 7, extra.CacheOptions.CRCacheOptions.MaxConcurrentLists)
	assert.Equal(t, 30*time.Second, extra.CacheOptions.CRCacheOptions.ListTimeoutPerRepository)
	assert.Equal(t, "pgx", extra.CacheOptions.DBCacheOptions.Driver)
	assert.Equal(t, "postgres://x", extra.CacheOptions.DBCacheOptions.DataSource)
	assert.Equal(t, 10, extra.CacheOptions.DBCacheOptions.MaxConnections)
	assert.Equal(t, 5, extra.CacheOptions.DBCacheOptions.MaxIdleConnections)
	assert.Equal(t, time.Minute, extra.CacheOptions.DBCacheOptions.MaxConnLifetime)
	assert.True(t, extra.CacheOptions.DbPushDraftsToGit)
	assert.Equal(t, "test-ns", extra.PodNameSpace)
	assert.Equal(t, 4453, extra.ProbePort)
	assert.True(t, extra.HAOptions.LeaderElection)
	assert.Equal(t, 15*time.Second, extra.HAOptions.LeaseDuration)
}

func TestConfigMapsExtraConfig(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	opts := NewPorchServerOptions(os.Stdout, os.Stderr)
	opts.RecommendedOptions.SecureServing.Listener = ln
	opts.RecommendedOptions.SecureServing.ServerCert.CertDirectory = t.TempDir()
	opts.LocalStandaloneDebugging = true
	opts.CacheType = "CR"
	opts.ProbePort = 4453
	opts.HAOptions = apiserver.HAConfig{LeaderElection: true}
	opts.PodNamespace = "test-ns"
	opts.MaxRequestBodySize = 1234
	opts.CoreAPIKubeconfigPath = writeTempKubeconfig(t, "http://127.0.0.1:1")
	opts.RecommendedOptions.CoreAPI.CoreAPIKubeconfigPath = opts.CoreAPIKubeconfigPath

	require.NoError(t, opts.Complete())

	config, err := opts.Config()
	require.NoError(t, err)
	assert.Equal(t, 4453, config.ExtraConfig.ProbePort)
	assert.True(t, config.ExtraConfig.HAOptions.LeaderElection)
	assert.Equal(t, "test-ns", config.ExtraConfig.PodNameSpace)
	assert.Equal(t, 1234, config.ExtraConfig.GRPCRuntimeOptions.MaxGrpcMessageSize)
	assert.Equal(t, int64(1234), config.GenericConfig.MaxRequestBodyBytes)
}

func writeTempKubeconfig(t *testing.T, server string) string {
	t.Helper()
	content := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user: {}
`, server)
	path := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestProxyHealthChecks(t *testing.T) {
	mgr := &stubProbeManager{elected: make(chan struct{})}
	require.NoError(t, proxyHealthChecks(mgr, &http.Client{}, "https://example.invalid"))
	assert.Equal(t, []string{"healthz", "livez"}, mgr.healthz)
	assert.Equal(t, []string{"readyz"}, mgr.readyz)
}

func TestProxyHealthChecksErrors(t *testing.T) {
	t.Run("healthz registration fails", func(t *testing.T) {
		mgr := &stubProbeManager{
			elected:    make(chan struct{}),
			failHealth: true,
		}
		assert.ErrorContains(t, proxyHealthChecks(mgr, &http.Client{}, "https://example.invalid"), "healthz error")
	})
	t.Run("readyz registration fails", func(t *testing.T) {
		mgr := &stubProbeManager{
			elected:   make(chan struct{}),
			failReady: true,
		}
		assert.ErrorContains(t, proxyHealthChecks(mgr, &http.Client{}, "https://example.invalid"), "readyz error")
	})
}

func TestDelegateAPIServerHealthLeader(t *testing.T) {
	tests := map[string]struct {
		statusCode  int
		path        string
		expectedErr string
	}{
		"healthy": {
			statusCode: http.StatusOK,
			path:       "healthz",
		},
		"not ready": {
			statusCode:  http.StatusServiceUnavailable,
			path:        "readyz",
			expectedErr: "apiserver readyz returned 503",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			t.Cleanup(srv.Close)

			mgr := &stubProbeManager{elected: make(chan struct{})}
			close(mgr.elected)

			// srv.Client() trusts the test server certificate (same pattern as LoopbackClientConfig).
			checker := delegateAPIServerHealth(mgr, srv.Client(), srv.URL, tc.path, true)
			err := checker(nil)
			if tc.expectedErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestNewCommandStartPorchServer(t *testing.T) {
	opts := NewPorchServerOptions(os.Stdout, os.Stderr)
	cmd := NewCommandStartPorchServer(context.Background(), opts)

	assert.Equal(t, "Launch a porch API server", cmd.Short)
	assert.NotNil(t, cmd.Flags().Lookup("cache-type"))
}

type stubProbeManager struct {
	elected    chan struct{}
	healthz    []string
	readyz     []string
	failHealth bool
	failReady  bool
}

func (m *stubProbeManager) Elected() <-chan struct{} {
	return m.elected
}

func (m *stubProbeManager) AddHealthzCheck(name string, _ healthz.Checker) error {
	if m.failHealth {
		return fmt.Errorf("healthz error")
	}
	m.healthz = append(m.healthz, name)
	return nil
}

func (m *stubProbeManager) AddReadyzCheck(name string, _ healthz.Checker) error {
	if m.failReady {
		return fmt.Errorf("readyz error")
	}
	m.readyz = append(m.readyz, name)
	return nil
}
