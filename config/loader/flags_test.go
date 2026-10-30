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
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kptdev/porch/config/types"
)

func TestLoadDurationAndDataSize(t *testing.T) {
	var cfg struct {
		Wait   types.Duration
		Size   types.DataSize
		Errors []string `default:"a, b"`
	}
	require.NoError(t, Load(&cfg, []byte("wait: 5m\nsize: 1Gi\n"), nil))
	assert.Equal(t, 5*time.Minute, cfg.Wait.Duration)
	assert.Equal(t, types.GiB, cfg.Size)
	assert.Equal(t, []string{"a", "b"}, cfg.Errors)

	var listed struct {
		Errors []string
	}
	require.NoError(t, Load(&listed, []byte("errors: [c, d]\n"), nil))
	assert.Equal(t, []string{"c", "d"}, listed.Errors)

	require.NoError(t, Load(&listed, nil, []string{"--errors=e, f"}))
	assert.Equal(t, []string{"e", "f"}, listed.Errors)
}

func TestFlagValueContract(t *testing.T) {
	var cfg struct {
		Wait   types.Duration `default:"5m"`
		Errors []string       `default:"a, b"`
		Count  int64
	}
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	require.NoError(t, Bind(fs, &cfg))

	wait := fs.Lookup("wait")
	assert.Equal(t, "string", wait.Value.Type())
	assert.Equal(t, "5m0s", wait.Value.String())
	assert.Equal(t, "a,b", fs.Lookup("errors").Value.String())

	require.NoError(t, fs.Parse([]string{"--count=7"}))
	assert.Equal(t, int64(7), cfg.Count)
}

func TestBindRejectsBadFields(t *testing.T) {
	assert.Error(t, Bind(nil, &struct{ Name string }{}))

	var badDefault struct {
		Count int `default:"nope"`
	}
	fs := pflag.NewFlagSet("config", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	assert.Error(t, Bind(fs, &badDefault))

	var unsupported struct {
		Ratio float64
	}
	assert.Error(t, Bind(fs, &unsupported))

	var duplicated struct {
		Name  string `flag:"name"`
		Title string `flag:"name"`
	}
	assert.Error(t, Bind(fs, &duplicated))
}
