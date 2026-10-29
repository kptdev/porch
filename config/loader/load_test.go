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
	"time"

	"github.com/kptdev/porch/config/types"
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
	if err := Load(&fromFile, []byte(file), nil); err != nil {
		t.Fatalf("file: %v", err)
	}
	if fromFile.CacheDirectory != "/cache" || fromFile.MaxGRPCMessageSize != 4 || fromFile.DB.Host != "dbhost" || fromFile.PodTTL != 9 {
		t.Fatalf("file values = %+v", fromFile)
	}

	t.Setenv("CACHE_DIRECTORY", "/env")
	t.Setenv("MAX_GRPC_MESSAGE_SIZE", "8")
	t.Setenv("HOST", "envhost")
	t.Setenv("POD_TTL", "3")
	var fromEnv derivedConfig
	if err := Load(&fromEnv, nil, nil); err != nil {
		t.Fatalf("env: %v", err)
	}
	if fromEnv.CacheDirectory != "/env" || fromEnv.MaxGRPCMessageSize != 8 || fromEnv.DB.Host != "envhost" || fromEnv.PodTTL != 3 {
		t.Fatalf("env values = %+v", fromEnv)
	}

	var fromFlags derivedConfig
	args := []string{
		"--cache-directory=/flag",
		"--max-grpc-message-size=5",
		"--db.host=flaghost",
		"--pod-ttl=2",
	}
	if err := Load(&fromFlags, nil, args); err != nil {
		t.Fatalf("flags: %v", err)
	}
	if fromFlags.CacheDirectory != "/flag" || fromFlags.MaxGRPCMessageSize != 5 || fromFlags.DB.Host != "flaghost" || fromFlags.PodTTL != 2 {
		t.Fatalf("flag values = %+v", fromFlags)
	}
}

func TestLoadTagOverrides(t *testing.T) {
	type taggedConfig struct {
		FunctionCacheDir string `json:"funcs" yaml:"dir" flag:"functions" env:"FUNCTION_CACHE" default:"./functions" usage:"path to cached functions"`
		Count            int    `json:"count" default:"3"`
	}

	var defaults taggedConfig
	if err := Load(&defaults, nil, nil); err != nil {
		t.Fatalf("default: %v", err)
	}
	if defaults.FunctionCacheDir != "./functions" || defaults.Count != 3 {
		t.Fatalf("defaults = %+v", defaults)
	}

	var fromFile taggedConfig
	if err := Load(&fromFile, []byte("dir: /data\ncount: 8\n"), nil); err != nil {
		t.Fatalf("file: %v", err)
	}
	if fromFile.FunctionCacheDir != "/data" || fromFile.Count != 8 {
		t.Fatalf("file values = %+v", fromFile)
	}

	var ignoredJSON taggedConfig
	if err := Load(&ignoredJSON, []byte("funcs: /no\n"), nil); err != nil {
		t.Fatalf("json key: %v", err)
	}
	if ignoredJSON.FunctionCacheDir != "./functions" {
		t.Fatalf("yaml tag did not win: %q", ignoredJSON.FunctionCacheDir)
	}

	t.Setenv("FUNCTION_CACHE", "/env")
	var fromEnv taggedConfig
	if err := Load(&fromEnv, []byte("dir: /data\n"), nil); err != nil {
		t.Fatalf("env: %v", err)
	}
	if fromEnv.FunctionCacheDir != "/env" {
		t.Fatalf("env = %q", fromEnv.FunctionCacheDir)
	}

	var fromFlag taggedConfig
	if err := Load(&fromFlag, []byte("dir: /data\n"), []string{"--functions=/bin"}); err != nil {
		t.Fatalf("flag: %v", err)
	}
	if fromFlag.FunctionCacheDir != "/bin" {
		t.Fatalf("flag = %q", fromFlag.FunctionCacheDir)
	}
}

func TestLoadOmittedKeyKeepsDefault(t *testing.T) {
	var cfg struct {
		Warm bool `default:"true"`
	}
	if err := Load(&cfg, []byte("{}\n"), nil); err != nil {
		t.Fatalf("omit: %v", err)
	}
	if !cfg.Warm {
		t.Fatal("omitted key cleared the default")
	}

	if err := Load(&cfg, []byte("warm: false\n"), nil); err != nil {
		t.Fatalf("explicit: %v", err)
	}
	if cfg.Warm {
		t.Fatal("explicit false did not override the default")
	}
}

func TestLoadPrecedence(t *testing.T) {
	const file = "name: from-file\n"
	var fromFile struct {
		Name string `env:"*" default:"from-default"`
	}
	if err := Load(&fromFile, []byte(file), nil); err != nil {
		t.Fatalf("file: %v", err)
	}
	if fromFile.Name != "from-file" {
		t.Fatalf("file = %q", fromFile.Name)
	}

	t.Setenv("NAME", "from-env")
	var fromEnv struct {
		Name string `env:"*" default:"from-default"`
	}
	if err := Load(&fromEnv, []byte(file), nil); err != nil {
		t.Fatalf("env: %v", err)
	}
	if fromEnv.Name != "from-env" {
		t.Fatalf("env = %q", fromEnv.Name)
	}

	var fromFlag struct {
		Name string `env:"*" default:"from-default"`
	}
	if err := Load(&fromFlag, []byte(file), []string{"--name=from-flag"}); err != nil {
		t.Fatalf("flag: %v", err)
	}
	if fromFlag.Name != "from-flag" {
		t.Fatalf("flag = %q", fromFlag.Name)
	}
}

func TestLoadDurationAndDataSize(t *testing.T) {
	var cfg struct {
		Wait   types.Duration
		Size   types.DataSize
		Errors []string `default:"a, b"`
	}
	if err := Load(&cfg, []byte("wait: 5m\nsize: 1Gi\n"), nil); err != nil {
		t.Fatalf("file: %v", err)
	}
	if cfg.Wait.Duration != 5*time.Minute {
		t.Fatalf("wait = %s", cfg.Wait.Duration)
	}
	if cfg.Size != types.GiB {
		t.Fatalf("size = %d", cfg.Size)
	}
	if len(cfg.Errors) != 2 || cfg.Errors[0] != "a" || cfg.Errors[1] != "b" {
		t.Fatalf("default list = %#v", cfg.Errors)
	}

	var listed struct {
		Errors []string
	}
	if err := Load(&listed, []byte("errors: [c, d]\n"), nil); err != nil {
		t.Fatalf("list file: %v", err)
	}
	if len(listed.Errors) != 2 || listed.Errors[0] != "c" || listed.Errors[1] != "d" {
		t.Fatalf("file list = %#v", listed.Errors)
	}
	if err := Load(&listed, nil, []string{"--errors=e, f"}); err != nil {
		t.Fatalf("list flag: %v", err)
	}
	if len(listed.Errors) != 2 || listed.Errors[0] != "e" || listed.Errors[1] != "f" {
		t.Fatalf("flag list = %#v", listed.Errors)
	}
}

func TestLoadEnvRequiresTag(t *testing.T) {
	t.Setenv("NAME", "from-env")
	t.Setenv("TITLE", "derived")
	var cfg struct {
		Name  string
		Title string `env:"*"`
	}
	if err := Load(&cfg, nil, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "" || cfg.Title != "derived" {
		t.Fatalf("name %q title %q", cfg.Name, cfg.Title)
	}
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
	if err := Load(&cfg, nil, args); err != nil {
		t.Fatalf("flags: %v", err)
	}
	if cfg.Controllers.Repository.CacheType != "CR" || cfg.Controllers.Repository.DB.Host != "db.example" || cfg.Controllers.Repository.GoGitRepoCacheSize != 4 {
		t.Fatalf("values = %+v", cfg.Controllers.Repository)
	}
}

func TestLoadNestedFlagNames(t *testing.T) {
	var cfg struct {
		PodNamespace string
		PodEvaluator struct {
			PodNamespace string
		}
	}
	args := []string{"--pod-namespace=top", "--pod-evaluator.pod-namespace=nested"}
	if err := Load(&cfg, nil, args); err != nil {
		t.Fatalf("flags: %v", err)
	}
	if cfg.PodNamespace != "top" || cfg.PodEvaluator.PodNamespace != "nested" {
		t.Fatalf("values = %#v / %#v", cfg.PodNamespace, cfg.PodEvaluator.PodNamespace)
	}
}
