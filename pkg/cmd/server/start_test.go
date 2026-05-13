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
	"os"
	"testing"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/apiserver"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime/schema"
	genericoptions "k8s.io/apiserver/pkg/server/options"
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

func TestComplete(t *testing.T) {
	opts := NewPorchServerOptions(os.Stdout, os.Stderr)

	// Test with patterns that have leading/trailing spaces
	opts.RetryableGitErrors = []string{" git error 1 ", "git error 2", " git error 3"}
	assert.Len(t, opts.RetryableGitErrors, 3)

	err := opts.Complete()
	assert.NoError(t, err)
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
