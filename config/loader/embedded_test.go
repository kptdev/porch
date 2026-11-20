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

type SharedOptions struct {
	Size int `flag:"size" default:"8"`
}

type embeddingTop struct {
	SharedOptions
	Name string
}

type embeddingNested struct {
	Inner struct {
		SharedOptions
	} `flagPrefix:"inner"`
}

func TestEmbeddedStructFieldsUseParentFlagPrefixAndFileKeys(t *testing.T) {
	// given
	var cfg embeddingTop

	// when
	err := Load(&cfg, []byte(`{"size": 3}`), nil)

	// then
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.Size)

	// when
	cfg = embeddingTop{}
	err = Load(&cfg, []byte(`{"size": 3}`), []string{"--size=5"})

	// then
	require.NoError(t, err)
	assert.Equal(t, 5, cfg.Size)
}

func TestEmbeddedStructInsideNestedStructKeepsFlagPrefix(t *testing.T) {
	// given
	var cfg embeddingNested

	// when
	err := Load(&cfg, nil, []string{"--inner.size=7"})

	// then
	require.NoError(t, err)
	assert.Equal(t, 7, cfg.Inner.Size)
}

func TestEmbeddedStructFileKeyCollisionIsRejected(t *testing.T) {
	// given
	var cfg struct {
		SharedOptions
		Size int `flag:"other-size"`
	}

	// when
	err := Load(&cfg, nil, nil)

	// then
	require.ErrorContains(t, err, "share file key")
}
