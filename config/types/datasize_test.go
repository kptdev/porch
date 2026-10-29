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

package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestDataSizeJSON(t *testing.T) {
	var got DataSize
	require.NoError(t, json.Unmarshal([]byte(`"6MB"`), &got))
	assert.Equal(t, 6*MB, got)

	out, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, `"6MB"`, string(out))

	assert.Error(t, json.Unmarshal([]byte(`6291456`), &got))
}

func TestDataSizeUnits(t *testing.T) {
	cases := map[string]DataSize{
		`"1Gi"`:   GiB,
		`"2GiB"`:  2 * GiB,
		`"512Ki"`: 512 * KiB,
		`"4Mi"`:   4 * MiB,
		`"8K"`:    8 * KB,
	}
	for input, want := range cases {
		var got DataSize
		if assert.NoError(t, json.Unmarshal([]byte(input), &got), input) {
			assert.Equal(t, want, got, input)
		}
	}
}

func TestDataSizeYAML(t *testing.T) {
	var got struct {
		Limit DataSize
	}
	require.NoError(t, yaml.Unmarshal([]byte("limit: 1Gi\n"), &got))
	assert.Equal(t, GiB, got.Limit)

	out, err := yaml.Marshal(got)
	require.NoError(t, err)
	var again struct {
		Limit DataSize
	}
	require.NoError(t, yaml.Unmarshal(out, &again))
	assert.Equal(t, got.Limit, again.Limit)
}
