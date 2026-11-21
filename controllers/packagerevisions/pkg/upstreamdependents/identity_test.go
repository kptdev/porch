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

package upstreamdependents

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
)

var nameShape = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?-[0-9a-f]{12}$`)

func locator(repo, dir, ref, commit string) *porchv1alpha2.Locator {
	return &porchv1alpha2.Locator{
		Type: porchv1alpha2.OriginType("git"),
		Git:  &porchv1alpha2.GitLock{Repo: repo, Directory: dir, Ref: ref, Commit: commit},
	}
}

func TestIdentityForLocatorIgnoresCommit(t *testing.T) {
	a := IdentityForLocator(locator("https://example.com/bp.git", "basens", "basens/v1", "aaa"))
	b := IdentityForLocator(locator("https://example.com/bp.git", "basens", "basens/v1", "bbb"))
	assert.Equal(t, a, b)
	assert.Equal(t, "https://example.com/bp.git|basens|basens/v1", a)
}

func TestIdentityForLocatorEmpty(t *testing.T) {
	assert.Equal(t, "", IdentityForLocator(nil))
	assert.Equal(t, "", IdentityForLocator(&porchv1alpha2.Locator{}))
	assert.Equal(t, "", IdentityForLocator(locator("", "dir", "ref", "")))
}

func TestIdentityForPackageRevisionIsName(t *testing.T) {
	assert.Equal(t, "repo.vendor.v1", IdentityForPackageRevision("repo.vendor.v1"))
}

func TestObjectNameShapeAndDeterminism(t *testing.T) {
	name := ObjectName("repo.vendor.v1", "repo.vendor.v1")
	assert.Regexp(t, nameShape, name)
	assert.Equal(t, name, ObjectName("repo.vendor.v1", "repo.vendor.v1"), "name must be deterministic")
	assert.True(t, strings.HasPrefix(name, "repo-vendor-v1-"), "readable prefix, got %q", name)
}

func TestObjectNameDiffersForDifferentIdentities(t *testing.T) {
	a := ObjectName("https://x|basens|basens/v1", "basens basens/v1")
	b := ObjectName("https://x|basens|basens/v2", "basens basens/v2")
	assert.NotEqual(t, a, b)
}

func TestObjectNameLengthCapped(t *testing.T) {
	long := strings.Repeat("a", 200)
	name := ObjectName(long, long)
	assert.LessOrEqual(t, len(name), prefixMaxLen+1+nameHashLen)
	assert.Regexp(t, nameShape, name)
}

func TestObjectNameFallbackPrefix(t *testing.T) {
	name := ObjectName("x", "!!!")
	assert.True(t, strings.HasPrefix(name, fallbackPrefix+"-"), "got %q", name)
	assert.Regexp(t, nameShape, name)
}

func TestSanitizeReplacesRunsAndTrims(t *testing.T) {
	assert.Equal(t, "basens-v1", sanitize("  Basens//V1!! "))
	assert.Equal(t, "a-b", sanitize("a___b"))
}

func TestReadableForLocator(t *testing.T) {
	assert.Equal(t, "basens basens/v1", ReadableForLocator(locator("r", "basens", "basens/v1", "c")))
	assert.Equal(t, "", ReadableForLocator(nil))
}

func TestCheckIdentity(t *testing.T) {
	require.NoError(t, CheckIdentity(nil, "a"))
	require.NoError(t, CheckIdentity(map[string]string{AnnotationUpstreamKey: "a"}, "a"))
	err := CheckIdentity(map[string]string{AnnotationUpstreamKey: "a"}, "b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manual review required")
}
