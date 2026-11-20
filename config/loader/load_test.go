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

func TestLoadPrecedence(t *testing.T) {
	const file = "name: from-file\n"
	var fromFile struct {
		Name string `env:"*" default:"from-default"`
	}
	require.NoError(t, Load(&fromFile, []byte(file), nil))
	assert.Equal(t, "from-file", fromFile.Name)

	t.Setenv("NAME", "from-env")
	var fromEnv struct {
		Name string `env:"*" default:"from-default"`
	}
	require.NoError(t, Load(&fromEnv, []byte(file), nil))
	assert.Equal(t, "from-env", fromEnv.Name)

	var fromFlag struct {
		Name string `env:"*" default:"from-default"`
	}
	require.NoError(t, Load(&fromFlag, []byte(file), []string{"--name=from-flag"}))
	assert.Equal(t, "from-flag", fromFlag.Name)
}

func TestLoadRejectsBadDestinationAndUnknownFlag(t *testing.T) {
	assert.Error(t, Load(nil, nil, nil))
	var n int
	assert.Error(t, Load(&n, nil, nil))

	var cfg struct {
		Name string
	}
	assert.Error(t, Load(&cfg, nil, []string{"--nope"}))
}
