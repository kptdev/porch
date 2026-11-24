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
	"io"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kptdev/porch/config/types"
)

func TestLoadOmittedKeyKeepsDefault(t *testing.T) {
	var cfg struct {
		Warm bool `default:"true"`
	}
	require.NoError(t, Load(&cfg, []byte("{}\n"), nil))
	assert.True(t, cfg.Warm)

	require.NoError(t, Load(&cfg, []byte("warm: false\n"), nil))
	assert.False(t, cfg.Warm)
}

func TestBindOverlayKeepsExplicitFlag(t *testing.T) {
	var cfg struct {
		CacheType string `default:"CR" env:"*"`
	}
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	require.NoError(t, Bind(fs, &cfg))
	require.NoError(t, fs.Parse([]string{"--cache-type=CR"}))

	t.Setenv("CACHE_TYPE", "from-env")
	require.NoError(t, Overlay(fs, &cfg, []byte("cacheType: DB\n")))
	assert.Equal(t, "CR", cfg.CacheType)
}

func TestSectionReadsComponent(t *testing.T) {
	const doc = `
server:
  cacheType: DB
global:
  telemetry:
    serviceName: porch
`
	serverDoc, err := Section([]byte(doc), "server")
	require.NoError(t, err)

	var server types.Server
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	require.NoError(t, Bind(fs, &server))
	require.NoError(t, fs.Parse(nil))
	require.NoError(t, Overlay(fs, &server, serverDoc))
	assert.Equal(t, "DB", server.CacheType)

	missing, err := Section([]byte(doc), "functionRunner")
	require.NoError(t, err)
	assert.Nil(t, missing)

	telemetryDoc, err := Section([]byte(doc), "global", "telemetry")
	require.NoError(t, err)
	var telemetry types.Telemetry
	telemetryFlags := pflag.NewFlagSet("telemetry", pflag.ContinueOnError)
	telemetryFlags.SetOutput(io.Discard)
	require.NoError(t, Bind(telemetryFlags, &telemetry))
	require.NoError(t, telemetryFlags.Parse(nil))
	require.NoError(t, Overlay(telemetryFlags, &telemetry, telemetryDoc))
	assert.Equal(t, "porch", telemetry.ServiceName)
}

func TestOverlayRejectsUnknownKey(t *testing.T) {
	var cfg struct {
		Nested struct {
			Name string
		}
	}
	err := Load(&cfg, []byte("nested:\n  nmae: x\n"), nil)
	assert.ErrorContains(t, err, "nested.nmae: unknown field")
}

func TestEnvSkipsInvalidFileValue(t *testing.T) {
	var cfg struct {
		Count int `env:"COUNT"`
	}
	t.Setenv("COUNT", "7")
	require.NoError(t, Load(&cfg, []byte("count: [1]\n"), nil))
	assert.Equal(t, 7, cfg.Count)

	t.Setenv("COUNT", "")
	assert.Error(t, Load(&cfg, []byte("count: [1]\n"), nil))
}

func TestSplitListQuotedComma(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, splitList("a, b"))
	assert.Equal(t, []string{`x{1,2}`, "y"}, splitList(`"x{1,2}",y`))
	assert.Equal(t, []string{`a"b`, "c"}, splitList(`a"b,c`))
}

func TestOverlayRejectsNonObject(t *testing.T) {
	var cfg struct {
		Name string
	}
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	require.NoError(t, Bind(fs, &cfg))
	require.NoError(t, fs.Parse(nil))
	assert.Error(t, Overlay(fs, &cfg, []byte("- a\n")))
	assert.Error(t, Overlay(nil, &cfg, nil))
}

func TestSectionEdges(t *testing.T) {
	missing, err := Section([]byte("   \n"), "server")
	require.NoError(t, err)
	assert.Nil(t, missing)

	missing, err = Section([]byte("null\n"), "server")
	require.NoError(t, err)
	assert.Nil(t, missing)

	_, err = Section([]byte("name: porch\n"), "name", "child")
	assert.Error(t, err)
}
