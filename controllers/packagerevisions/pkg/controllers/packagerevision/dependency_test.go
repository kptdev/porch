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

package packagerevision

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
)

func kptfileWithUpstream(name, repo, dir, ref, commit string) string {
	return fmt.Sprintf(`apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: %s
upstream:
  type: git
  git:
    repo: %s
    directory: %s
    ref: %s
upstreamLock:
  type: git
  git:
    repo: %s
    directory: %s
    ref: %s
    commit: %s
`, name, repo, dir, ref, repo, dir, ref, commit)
}

func TestExtractSubpackageUpstreams(t *testing.T) {
	resources := map[string]string{
		// Root Kptfile is ignored (reported via UpstreamLock).
		"Kptfile": kptfileWithUpstream("root", "https://ex.com/bp.git", "root-bp", "v1", "aaa"),
		// Nested sub-packages with resolved upstreams.
		"subpackages/network/Kptfile": kptfileWithUpstream("net", "https://vendor.com/v.git", "network-bp", "v2", "bbb"),
		"subpackages/compute/Kptfile": kptfileWithUpstream("cmp", "https://vendor.com/v.git", "compute-bp", "v3", "ccc"),
		// Nested Kptfile with no upstreamLock — skipped.
		"subpackages/local/Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: local\n",
		// Non-Kptfile content — ignored.
		"subpackages/network/deployment.yaml": "kind: Deployment\n",
	}

	got, truncated, parseErrs := extractSubpackageUpstreams(resources)

	assert.False(t, truncated)
	assert.Empty(t, parseErrs)
	require.Len(t, got, 2)
	// Sorted by path: compute before network.
	assert.Equal(t, "subpackages/compute", got[0].Path)
	assert.Equal(t, "compute-bp", got[0].Upstream.Git.Directory)
	assert.Equal(t, "subpackages/network", got[1].Path)
	assert.Equal(t, "v2", got[1].Upstream.Git.Ref)
}

// Kptfiles at varying depths are each recorded as an independent edge anchored
// at their path — no tree walking, no transitive attribution. This shape can
// arise from a git-discovered package with nested sub-packages.
func TestExtractSubpackageUpstreamsDeeplyNested(t *testing.T) {
	resources := map[string]string{
		"Kptfile":           kptfileWithUpstream("root", "https://ex.com/bp.git", "root-bp", "v1", "r"),
		"a/Kptfile":         kptfileWithUpstream("a", "https://v.com/v.git", "a-bp", "v1", "aa"),
		"a/b/Kptfile":       kptfileWithUpstream("b", "https://v.com/v.git", "b-bp", "v2", "bb"),
		"a/b/c/Kptfile":     kptfileWithUpstream("c", "https://v.com/v.git", "c-bp", "v3", "cc"),
		"a/b/resource.yaml": "kind: ConfigMap\n",
	}
	got, truncated, parseErrs := extractSubpackageUpstreams(resources)
	assert.False(t, truncated)
	assert.Empty(t, parseErrs)
	require.Len(t, got, 3, "each nested Kptfile is a distinct edge regardless of depth")
	assert.Equal(t, "a", got[0].Path)
	assert.Equal(t, "a/b", got[1].Path)
	assert.Equal(t, "a/b/c", got[2].Path)
	assert.Equal(t, "c-bp", got[2].Upstream.Git.Directory)
}

// A nested Kptfile with upstream but no resolved upstreamLock is skipped.
func TestExtractSubpackageUpstreamsUnresolvedSkipped(t *testing.T) {
	resources := map[string]string{
		"sub/pending/Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: pending\nupstream:\n  type: git\n  git:\n    repo: https://v.com/v.git\n    directory: p\n    ref: v1\n",
	}
	got, _, parseErrs := extractSubpackageUpstreams(resources)
	assert.Empty(t, got, "no upstreamLock -> not recorded")
	assert.Empty(t, parseErrs)
}

func TestExtractSubpackageUpstreamsMalformedSkipped(t *testing.T) {
	resources := map[string]string{
		"subpackages/good/Kptfile": kptfileWithUpstream("good", "https://v.com/v.git", "good-bp", "v1", "a"),
		"subpackages/bad/Kptfile":  "this: is: not: valid: {{{",
	}
	got, _, parseErrs := extractSubpackageUpstreams(resources)
	assert.Len(t, got, 1)
	assert.Equal(t, []string{"subpackages/bad/Kptfile"}, parseErrs)
}

func TestExtractSubpackageUpstreamsTruncation(t *testing.T) {
	resources := map[string]string{}
	for i := 0; i < porchv1alpha2.MaxSubpackageUpstreams+25; i++ {
		resources[fmt.Sprintf("sub/pkg%03d/Kptfile", i)] =
			kptfileWithUpstream(fmt.Sprintf("p%d", i), "https://v.com/v.git", fmt.Sprintf("bp%d", i), "v1", "c")
	}
	got, truncated, _ := extractSubpackageUpstreams(resources)
	assert.True(t, truncated)
	assert.Len(t, got, porchv1alpha2.MaxSubpackageUpstreams)
}

func TestExtractSubpackageUpstreamsEmpty(t *testing.T) {
	got, truncated, parseErrs := extractSubpackageUpstreams(map[string]string{
		"Kptfile": kptfileWithUpstream("root", "https://ex.com/bp.git", "root-bp", "v1", "aaa"),
	})
	assert.Nil(t, got)
	assert.False(t, truncated)
	assert.Empty(t, parseErrs)
}
