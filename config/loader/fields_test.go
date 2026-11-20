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

package loader

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDerivedNames(t *testing.T) {
	type derivedConfig struct {
		CacheDirectory     string `env:"*"`
		MaxGRPCMessageSize int    `env:"*"`
		DB                 struct {
			Host string `env:"*"`
		}
		PodTTL int `env:"*"`
	}

	const file = `
cacheDirectory: /cache
maxGRPCMessageSize: 4
db:
  host: dbhost
podTTL: 9
`
	var fromFile derivedConfig
	require.NoError(t, Load(&fromFile, []byte(file), nil))
	assert.Equal(t, "/cache", fromFile.CacheDirectory)
	assert.Equal(t, 4, fromFile.MaxGRPCMessageSize)
	assert.Equal(t, "dbhost", fromFile.DB.Host)
	assert.Equal(t, 9, fromFile.PodTTL)

	t.Setenv("CACHE_DIRECTORY", "/env")
	t.Setenv("MAX_GRPC_MESSAGE_SIZE", "8")
	t.Setenv("HOST", "envhost")
	t.Setenv("POD_TTL", "3")
	var fromEnv derivedConfig
	require.NoError(t, Load(&fromEnv, nil, nil))
	assert.Equal(t, "/env", fromEnv.CacheDirectory)
	assert.Equal(t, 8, fromEnv.MaxGRPCMessageSize)
	assert.Equal(t, "envhost", fromEnv.DB.Host)
	assert.Equal(t, 3, fromEnv.PodTTL)

	var fromFlags derivedConfig
	args := []string{
		"--cache-directory=/flag",
		"--max-grpc-message-size=5",
		"--db.host=flaghost",
		"--pod-ttl=2",
	}
	require.NoError(t, Load(&fromFlags, nil, args))
	assert.Equal(t, "/flag", fromFlags.CacheDirectory)
	assert.Equal(t, 5, fromFlags.MaxGRPCMessageSize)
	assert.Equal(t, "flaghost", fromFlags.DB.Host)
	assert.Equal(t, 2, fromFlags.PodTTL)
}

func TestLoadTagOverrides(t *testing.T) {
	type taggedConfig struct {
		FunctionCacheDir string `json:"funcs" yaml:"dir" flag:"functions" env:"FUNCTION_CACHE" default:"./functions" usage:"path to cached functions"`
		Count            int    `json:"count" default:"3"`
	}

	var defaults taggedConfig
	require.NoError(t, Load(&defaults, nil, nil))
	assert.Equal(t, "./functions", defaults.FunctionCacheDir)
	assert.Equal(t, 3, defaults.Count)

	var fromFile taggedConfig
	require.NoError(t, Load(&fromFile, []byte("dir: /data\ncount: 8\n"), nil))
	assert.Equal(t, "/data", fromFile.FunctionCacheDir)
	assert.Equal(t, 8, fromFile.Count)

	var ignoredJSON taggedConfig
	require.NoError(t, Load(&ignoredJSON, []byte("funcs: /no\n"), nil))
	assert.Equal(t, "./functions", ignoredJSON.FunctionCacheDir)

	t.Setenv("FUNCTION_CACHE", "/env")
	var fromEnv taggedConfig
	require.NoError(t, Load(&fromEnv, []byte("dir: /data\n"), nil))
	assert.Equal(t, "/env", fromEnv.FunctionCacheDir)

	var fromFlag taggedConfig
	require.NoError(t, Load(&fromFlag, []byte("dir: /data\n"), []string{"--functions=/bin"}))
	assert.Equal(t, "/bin", fromFlag.FunctionCacheDir)
}

func TestLoadEnvRequiresTag(t *testing.T) {
	t.Setenv("NAME", "from-env")
	t.Setenv("TITLE", "derived")
	var cfg struct {
		Name  string
		Title string `env:"*"`
	}
	require.NoError(t, Load(&cfg, nil, nil))
	assert.Empty(t, cfg.Name)
	assert.Equal(t, "derived", cfg.Title)
}

func TestLoadFlagPrefix(t *testing.T) {
	var cfg struct {
		Controllers struct {
			Repository struct {
				CacheType string `default:"DB"`
				DB        struct {
					Host string
				}
				// flag replaces the leaf. The parent prefix is still applied.
				GoGitRepoCacheSize int `flag:"gogit-repo-cache-size"`
			} `flagPrefix:"repositories"`
		}
	}
	args := []string{
		"--repositories.cache-type=CR",
		"--repositories.db.host=db.example",
		"--repositories.gogit-repo-cache-size=4",
	}
	require.NoError(t, Load(&cfg, nil, args))
	assert.Equal(t, "CR", cfg.Controllers.Repository.CacheType)
	assert.Equal(t, "db.example", cfg.Controllers.Repository.DB.Host)
	assert.Equal(t, 4, cfg.Controllers.Repository.GoGitRepoCacheSize)
}

func TestLoadNestedFlagNames(t *testing.T) {
	var cfg struct {
		PodNamespace string
		PodEvaluator struct {
			PodNamespace string
		}
	}
	args := []string{"--pod-namespace=top", "--pod-evaluator.pod-namespace=nested"}
	require.NoError(t, Load(&cfg, nil, args))
	assert.Equal(t, "top", cfg.PodNamespace)
	assert.Equal(t, "nested", cfg.PodEvaluator.PodNamespace)
}
