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

package podevaluator

import (
	"context"
	"testing"

	"github.com/kptdev/kpt/pkg/fn/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePodExecutorWarmupImage(t *testing.T) {
	const repo = "ghcr.io/kptdev/krm-functions-catalog/set-namespace"
	resolver := runtime.TagResolver{
		Listers: []runtime.TagLister{
			&fakeLister{
				tags: map[string][]string{
					repo: {"v0.3.0", "v0.4.0", "v0.4.1", "v0.4.2", "v0.5.0"},
				},
			},
		},
	}

	t.Run("strict semver does not list tags", func(t *testing.T) {
		got, err := resolvePodExecutorWarmupImage(context.Background(), resolver, repo, "v0.4.1")
		require.NoError(t, err)
		assert.Equal(t, repo+":v0.4.1", got)
	})

	t.Run("constraint picks highest matching tag", func(t *testing.T) {
		got, err := resolvePodExecutorWarmupImage(context.Background(), resolver, repo, ">= 0.4.0 < 0.5.0")
		require.NoError(t, err)
		assert.Equal(t, repo+":v0.4.2", got)
	})

	t.Run("wildcard picks highest semver tag", func(t *testing.T) {
		got, err := resolvePodExecutorWarmupImage(context.Background(), resolver, repo, "*")
		require.NoError(t, err)
		assert.Equal(t, repo+":v0.5.0", got)
	})

	t.Run("invalid tag", func(t *testing.T) {
		_, err := resolvePodExecutorWarmupImage(context.Background(), resolver, repo, "not-valid")
		assert.Error(t, err)
	})
}
