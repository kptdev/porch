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
	"fmt"
	"sort"
)

// UpstreamKey returns a canonical repo|dir|ref key for an upstream git blueprint.
// Commit is excluded so a query by ref/tag (e.g. "v2") matches any resolved
// commit. Returns "" if loc is not a resolvable git locator.
func UpstreamKey(loc *Locator) string {
	if loc == nil || loc.Git == nil || loc.Git.Repo == "" {
		return ""
	}
	g := loc.Git
	return fmt.Sprintf("%s|%s|%s", g.Repo, g.Directory, g.Ref)
}

// ComputeUpstreamKeys returns the deduplicated, sorted upstream keys for a PR,
// combining UpstreamLock with all SubpackageUpstreams. Sorted for stable
// diff-then-write; nil when there are none.
func (pr *PackageRevision) ComputeUpstreamKeys() []string {
	seen := map[string]struct{}{}
	add := func(loc *Locator) {
		if k := UpstreamKey(loc); k != "" {
			seen[k] = struct{}{}
		}
	}
	add(pr.Status.UpstreamLock)
	for i := range pr.Status.SubpackageUpstreams {
		add(pr.Status.SubpackageUpstreams[i].Upstream)
	}
	if len(seen) == 0 {
		return nil
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
