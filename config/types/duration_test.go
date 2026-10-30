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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestDurationJSON(t *testing.T) {
	var got Duration
	require.NoError(t, json.Unmarshal([]byte(`"5m"`), &got))
	assert.Equal(t, 5*time.Minute, got.Duration)

	out, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, `"5m0s"`, string(out))

	assert.Error(t, json.Unmarshal([]byte(`5`), &got))
}

func TestDurationYAML(t *testing.T) {
	var got struct {
		Wait Duration
	}
	require.NoError(t, yaml.Unmarshal([]byte("wait: 30s\n"), &got))
	assert.Equal(t, 30*time.Second, got.Wait.Duration)

	out, err := yaml.Marshal(got)
	require.NoError(t, err)
	var again struct {
		Wait Duration
	}
	require.NoError(t, yaml.Unmarshal(out, &again))
	assert.Equal(t, got.Wait.Duration, again.Wait.Duration)
}

func TestDurationYAMLMethods(t *testing.T) {
	var got Duration
	require.NoError(t, got.UnmarshalYAML(func(v any) error {
		*(v.(*string)) = "5m"
		return nil
	}))
	assert.Equal(t, 5*time.Minute, got.Duration)

	formatted, err := got.MarshalYAML()
	require.NoError(t, err)
	assert.Equal(t, "5m0s", formatted)

	assert.Error(t, got.UnmarshalYAML(func(v any) error {
		*(v.(*string)) = "nope"
		return nil
	}))
}
