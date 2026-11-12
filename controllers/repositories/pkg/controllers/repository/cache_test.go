// Copyright 2026 The kpt Authors
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

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/repository"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSetupDBCacheOptionsFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
		wantErr string
	}{
		{
			name: "valid pgx configuration",
			envVars: map[string]string{
				"DB_DRIVER":   "pgx",
				"DB_HOST":     "localhost",
				"DB_PORT":     "5432",
				"DB_NAME":     "porch",
				"DB_USER":     "testuser",
				"DB_PASSWORD": "testpass",
			},
		},
		{
			name: "valid mysql configuration",
			envVars: map[string]string{
				"DB_DRIVER":   "mysql",
				"DB_HOST":     "localhost",
				"DB_PORT":     "3306",
				"DB_NAME":     "porch",
				"DB_USER":     "testuser",
				"DB_PASSWORD": "testpass",
			},
		},
		{
			name: "pgx with SSL mode",
			envVars: map[string]string{
				"DB_DRIVER":   "pgx",
				"DB_HOST":     "localhost",
				"DB_PORT":     "5432",
				"DB_NAME":     "porch",
				"DB_USER":     "testuser",
				"DB_SSL_MODE": "require",
			},
		},
		{
			name: "missing DB_HOST",
			envVars: map[string]string{
				"DB_PORT":     "5432",
				"DB_NAME":     "porch",
				"DB_USER":     "testuser",
				"DB_PASSWORD": "testpass",
			},
			wantErr: "DB_HOST",
		},
		{
			name: "missing DB_PORT",
			envVars: map[string]string{
				"DB_HOST":     "localhost",
				"DB_NAME":     "porch",
				"DB_USER":     "testuser",
				"DB_PASSWORD": "testpass",
			},
			wantErr: "DB_PORT",
		},
		{
			name: "missing DB_NAME",
			envVars: map[string]string{
				"DB_HOST":     "localhost",
				"DB_PORT":     "5432",
				"DB_USER":     "testuser",
				"DB_PASSWORD": "testpass",
			},
			wantErr: "DB_NAME",
		},
		{
			name: "missing DB_USER",
			envVars: map[string]string{
				"DB_HOST":     "localhost",
				"DB_PORT":     "5432",
				"DB_NAME":     "porch",
				"DB_PASSWORD": "testpass",
			},
			wantErr: "DB_USER",
		},
		{
			name: "missing DB_PASSWORD without SSL",
			envVars: map[string]string{
				"DB_HOST": "localhost",
				"DB_PORT": "5432",
				"DB_NAME": "porch",
				"DB_USER": "testuser",
			},
			wantErr: "DB_PASSWORD",
		},
		{
			name: "unsupported driver",
			envVars: map[string]string{
				"DB_DRIVER":   "sqlite",
				"DB_HOST":     "localhost",
				"DB_PORT":     "5432",
				"DB_NAME":     "porch",
				"DB_USER":     "testuser",
				"DB_PASSWORD": "testpass",
			},
			wantErr: "unsupported DB driver",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all DB env vars first
			os.Unsetenv("DB_DRIVER")
			os.Unsetenv("DB_HOST")
			os.Unsetenv("DB_PORT")
			os.Unsetenv("DB_NAME")
			os.Unsetenv("DB_USER")
			os.Unsetenv("DB_PASSWORD")
			os.Unsetenv("DB_SSL_MODE")

			// Set test env vars
			for k, v := range tt.envVars {
				os.Setenv(k, v)
			}

			r := &RepositoryReconciler{}
			opts, err := r.setupDBCacheOptionsFromEnv()

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, opts.Driver)
				assert.NotEmpty(t, opts.DataSource)
			}
		})
	}
}

func TestSimpleUserInfoProvider(t *testing.T) {
	provider := &simpleUserInfoProvider{}
	ctx := context.Background()

	userInfo := provider.GetUserInfo(ctx)

	require.NotNil(t, userInfo)
	assert.Equal(t, "porch-controller", userInfo.Name)
	assert.Equal(t, "porch-controller@kpt.dev", userInfo.Email)
}

func TestSimpleUserInfoProviderReturnsConsistentInfo(t *testing.T) {
	provider := &simpleUserInfoProvider{}
	ctx := context.Background()

	info1 := provider.GetUserInfo(ctx)
	info2 := provider.GetUserInfo(ctx)

	assert.Equal(t, info1.Name, info2.Name)
	assert.Equal(t, info1.Email, info2.Email)
}

// Ensure simpleUserInfoProvider implements the interface
var _ repository.UserInfoProvider = (*simpleUserInfoProvider)(nil)

func TestValidateCacheType(t *testing.T) {
	tests := []struct {
		name        string
		cacheType   string
		expectError bool
	}{
		{
			name:        "valid DB cache type",
			cacheType:   "db",
			expectError: false,
		},
		{
			name:        "valid DB cache type uppercase",
			cacheType:   "DB",
			expectError: false,
		},
		{
			name:        "valid CR cache type",
			cacheType:   "cr",
			expectError: false,
		},
		{
			name:        "valid CR cache type uppercase",
			cacheType:   "CR",
			expectError: false,
		},
		{
			name:        "invalid cache type",
			cacheType:   "invalid",
			expectError: true,
		},
		{
			name:        "empty cache type",
			cacheType:   "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RepositoryReconciler{cacheType: tt.cacheType}
			err := r.validateCacheType()

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDetermineCacheDirectory(t *testing.T) {
	tests := []struct {
		name           string
		gitCacheDirEnv string
		expectSuffix   string
	}{
		{
			name:           "uses GIT_CACHE_DIR env var",
			gitCacheDirEnv: "/custom/cache/dir",
			expectSuffix:   "",
		},
		{
			name:           "falls back to user cache dir",
			gitCacheDirEnv: "",
			expectSuffix:   "/porch-repo-controller",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore env var
			oldEnv := os.Getenv("GIT_CACHE_DIR")
			defer os.Setenv("GIT_CACHE_DIR", oldEnv)

			if tt.gitCacheDirEnv != "" {
				os.Setenv("GIT_CACHE_DIR", tt.gitCacheDirEnv)
			} else {
				os.Unsetenv("GIT_CACHE_DIR")
			}

			r := &RepositoryReconciler{}
			cacheDir := r.determineCacheDirectory()

			assert.NotEmpty(t, cacheDir)

			if tt.gitCacheDirEnv != "" {
				assert.Equal(t, tt.gitCacheDirEnv, cacheDir)
			} else if tt.expectSuffix != "" {
				assert.Contains(t, cacheDir, tt.expectSuffix)
			}
		})
	}
}

func TestBuildCacheOptions(t *testing.T) {
	r := &RepositoryReconciler{
		cacheType:                  "DB",
		useUserDefinedCaBundle:     true,
		RepoOperationRetryAttempts: 3,
	}

	dbOptions := cachetypes.DBCacheOptions{
		Driver:     "pgx",
		DataSource: "postgres://test",
	}

	userInfoProvider := &simpleUserInfoProvider{}

	options := r.buildCacheOptions(nil, dbOptions, "/test/cache", nil, nil, userInfoProvider)

	assert.Equal(t, cachetypes.CacheType("DB"), options.CacheType)
	assert.Equal(t, "pgx", options.DBCacheOptions.Driver)
	assert.Equal(t, "/test/cache", options.ExternalRepoOptions.LocalDirectory)
	assert.True(t, options.ExternalRepoOptions.UseUserDefinedCaBundle)
	assert.Equal(t, 3, options.ExternalRepoOptions.RepoOperationRetryAttempts)
	assert.Equal(t, userInfoProvider, options.ExternalRepoOptions.UserInfoProvider)
	assert.NotNil(t, options.RepoPRChangeNotifier)
}

func TestCreateCredentialResolvers(t *testing.T) {
	// Use the existing mockery client
	mockClient := mockclient.NewMockClient(t)

	r := &RepositoryReconciler{}
	credResolver, caResolver := r.createCredentialResolvers(mockClient)

	assert.NotNil(t, credResolver)
	assert.NotNil(t, caResolver)
}

func TestDetermineCacheDirectory_WithFlagSet(t *testing.T) {
	r := &RepositoryReconciler{cacheDirectory: "/flag/cache/dir"}
	result := r.determineCacheDirectory()
	assert.Equal(t, "/flag/cache/dir", result)
}

func TestDetermineCacheDirectory_PrefersFlagOverEnv(t *testing.T) {
	old := os.Getenv("GIT_CACHE_DIR")
	defer func() {
		if old != "" {
			os.Setenv("GIT_CACHE_DIR", old)
		} else {
			os.Unsetenv("GIT_CACHE_DIR")
		}
	}()

	os.Setenv("GIT_CACHE_DIR", "/env/cache")
	r := &RepositoryReconciler{cacheDirectory: "/flag/cache"}
	result := r.determineCacheDirectory()
	assert.Equal(t, "/flag/cache", result)
}

func TestDetermineCacheDirectory_UsesEnvWhenNoFlag(t *testing.T) {
	old := os.Getenv("GIT_CACHE_DIR")
	defer func() {
		if old != "" {
			os.Setenv("GIT_CACHE_DIR", old)
		} else {
			os.Unsetenv("GIT_CACHE_DIR")
		}
	}()

	os.Unsetenv("GIT_CACHE_DIR")
	os.Setenv("GIT_CACHE_DIR", "/env/cache/dir")
	r := &RepositoryReconciler{cacheDirectory: ""}
	result := r.determineCacheDirectory()
	assert.Equal(t, "/env/cache/dir", result)
}

func TestDetermineCacheDirectory_FallbackIncludesControllerName(t *testing.T) {
	old := os.Getenv("GIT_CACHE_DIR")
	defer func() {
		if old != "" {
			os.Setenv("GIT_CACHE_DIR", old)
		} else {
			os.Unsetenv("GIT_CACHE_DIR")
		}
	}()

	os.Unsetenv("GIT_CACHE_DIR")
	r := &RepositoryReconciler{cacheDirectory: ""}
	result := r.determineCacheDirectory()

	// Should include the porch-repo-controller suffix
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "porch-repo-controller")
}

// Test initPRRStore - basic validation
func TestInitPRRStore_WithNonDBCache(t *testing.T) {
	r := &RepositoryReconciler{
		cacheType: "CR",
		CacheOptions: cachetypes.CacheOptions{
			CacheType: cachetypes.CRCacheType,
		},
	}

	err := r.initPRRStore(context.Background())
	// Should return nil for non-DB cache types
	assert.NoError(t, err)
}

func TestInitPRRStore_WithEmptyDataSource(t *testing.T) {
	r := &RepositoryReconciler{
		cacheType: "DB",
		CacheOptions: cachetypes.CacheOptions{
			CacheType: cachetypes.DBCacheType,
			DBCacheOptions: cachetypes.DBCacheOptions{
				DataSource: "",
			},
		},
	}

	err := r.initPRRStore(context.Background())
	// Should return nil when DataSource is empty (DB not configured)
	assert.NoError(t, err)
}

func TestInitPRRStore_WithInvalidConnectionString(t *testing.T) {
	r := &RepositoryReconciler{
		cacheType: "DB",
		CacheOptions: cachetypes.CacheOptions{
			CacheType: cachetypes.DBCacheType,
			DBCacheOptions: cachetypes.DBCacheOptions{
				DataSource: "invalid-connection-string",
			},
		},
	}

	err := r.initPRRStore(context.Background())
	// Should error on invalid connection string
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse DB connection string")
}

// Test createCacheFromEnv - basic validation without full K8s setup
func TestCreateCacheFromEnv_ValidateCacheTypeCheck(t *testing.T) {
	r := &RepositoryReconciler{
		cacheType: "INVALID",
	}

	err := r.validateCacheType()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cache type")
}

func TestCreateCacheFromEnv_ValidateDBCacheType(t *testing.T) {
	r := &RepositoryReconciler{
		cacheType: "DB",
	}

	err := r.validateCacheType()
	assert.NoError(t, err)
}

func TestCreateCacheFromEnv_ValidateCRCacheType(t *testing.T) {
	r := &RepositoryReconciler{
		cacheType: "CR",
	}

	err := r.validateCacheType()
	assert.NoError(t, err)
}
