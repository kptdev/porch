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

package v1alpha2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loc(repo, dir, ref string) *Locator {
	return &Locator{Type: "git", Git: &GitLock{Repo: repo, Directory: dir, Ref: ref, Commit: "deadbeef"}}
}

func TestUpstreamKey(t *testing.T) {
	assert.Equal(t, "https://v.com/v.git|net-bp|v2", UpstreamKey(loc("https://v.com/v.git", "net-bp", "v2")))
	assert.Equal(t, "", UpstreamKey(nil))
	assert.Equal(t, "", UpstreamKey(&Locator{}))
	assert.Equal(t, "", UpstreamKey(&Locator{Git: &GitLock{}})) // empty repo
}

func TestComputeUpstreamKeys(t *testing.T) {
	pr := &PackageRevision{
		Status: PackageRevisionStatus{
			UpstreamLock: loc("https://v.com/v.git", "root-bp", "v1"),
			SubpackageUpstreams: []SubpackageUpstream{
				{Path: "sub/net", Upstream: loc("https://v.com/v.git", "net-bp", "v2")},
				{Path: "sub/cmp", Upstream: loc("https://v.com/v.git", "cmp-bp", "v3")},
				{Path: "sub/net2", Upstream: loc("https://v.com/v.git", "net-bp", "v2")}, // dup
			},
		},
	}
	keys := pr.ComputeUpstreamKeys()
	require.Len(t, keys, 3) // duplicate collapsed
	assert.Contains(t, keys, "https://v.com/v.git|root-bp|v1")
	assert.Contains(t, keys, "https://v.com/v.git|net-bp|v2")
	assert.Contains(t, keys, "https://v.com/v.git|cmp-bp|v3")
	// Sorted for stable diff-then-write.
	assert.Equal(t, "https://v.com/v.git|cmp-bp|v3", keys[0])
}

// Two upstreams with the same repo|dir|ref but different commits collapse to a
// single key (commit is excluded so a query by ref/tag matches any commit).
func TestComputeUpstreamKeysSameRefDifferentCommit(t *testing.T) {
	pr := &PackageRevision{
		Status: PackageRevisionStatus{
			UpstreamLock: loc("https://v.com/v.git", "bp", "v2"),
			SubpackageUpstreams: []SubpackageUpstream{
				{Path: "sub/a", Upstream: &Locator{Type: "git", Git: &GitLock{
					Repo: "https://v.com/v.git", Directory: "bp", Ref: "v2", Commit: "different"}}},
			},
		},
	}
	keys := pr.ComputeUpstreamKeys()
	require.Len(t, keys, 1, "same repo|dir|ref collapses regardless of commit")
	assert.Equal(t, "https://v.com/v.git|bp|v2", keys[0])
}

func TestComputeUpstreamKeysNone(t *testing.T) {
	assert.Empty(t, (&PackageRevision{}).ComputeUpstreamKeys())
}
