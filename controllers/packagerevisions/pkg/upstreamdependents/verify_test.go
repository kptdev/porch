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
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
)

// fakeLookup answers per dependent name: exists, references, or an error.
type answer struct {
	exists, references bool
	err                error
}

func fakeLookup(answers map[string]answer) (Lookup, *[]string) {
	var asked []string
	return func(_ context.Context, e porchv1alpha2.UpstreamDependent) (bool, bool, error) {
		asked = append(asked, e.Name)
		a, ok := answers[e.Name]
		if !ok {
			return false, false, nil
		}
		return a.exists, a.references, a.err
	}, &asked
}

func entries(names ...string) []porchv1alpha2.UpstreamDependent {
	out := make([]porchv1alpha2.UpstreamDependent, 0, len(names))
	for _, n := range names {
		out = append(out, porchv1alpha2.UpstreamDependent{Name: n, Path: "sub/" + n})
	}
	return out
}

func TestVerifyNoEntriesAllows(t *testing.T) {
	lookup, asked := fakeLookup(nil)
	v, err := Verify(context.Background(), porchv1alpha2.UpstreamDependentsStatus{}, lookup)
	require.NoError(t, err)
	assert.False(t, v.Blocked)
	assert.Empty(t, *asked, "no entries means no reads")
}

func TestVerifyLiveMatchBlocks(t *testing.T) {
	status := porchv1alpha2.UpstreamDependentsStatus{Dependents: entries("dep-a")}
	lookup, _ := fakeLookup(map[string]answer{"dep-a": {exists: true, references: true}})

	v, err := Verify(context.Background(), status, lookup)
	require.NoError(t, err)
	assert.True(t, v.Blocked)
	require.NotNil(t, v.Blocker)
	assert.Equal(t, "dep-a", v.Blocker.Name)
}

func TestVerifyStaleAllowsWhenDependentMissing(t *testing.T) {
	status := porchv1alpha2.UpstreamDependentsStatus{Dependents: entries("gone")}
	lookup, _ := fakeLookup(map[string]answer{"gone": {exists: false}})

	v, err := Verify(context.Background(), status, lookup)
	require.NoError(t, err)
	assert.False(t, v.Blocked)
}

func TestVerifyStaleAllowsWhenDependentNoLongerReferences(t *testing.T) {
	status := porchv1alpha2.UpstreamDependentsStatus{Dependents: entries("reinstalled")}
	lookup, _ := fakeLookup(map[string]answer{"reinstalled": {exists: true, references: false}})

	v, err := Verify(context.Background(), status, lookup)
	require.NoError(t, err)
	assert.False(t, v.Blocked)
}

func TestVerifyStopsAtFirstLiveMatch(t *testing.T) {
	status := porchv1alpha2.UpstreamDependentsStatus{Dependents: entries("a", "b", "c")}
	lookup, asked := fakeLookup(map[string]answer{
		"a": {exists: true, references: true},
		"b": {exists: true, references: true},
		"c": {exists: true, references: true},
	})

	v, err := Verify(context.Background(), status, lookup)
	require.NoError(t, err)
	assert.True(t, v.Blocked)
	assert.Equal(t, []string{"a"}, *asked, "verification stops at the first live match")
}

func TestVerifyLookupErrorFailsClosed(t *testing.T) {
	status := porchv1alpha2.UpstreamDependentsStatus{Dependents: entries("a")}
	boom := errors.New("api unavailable")
	lookup, _ := fakeLookup(map[string]answer{"a": {err: boom}})

	v, err := Verify(context.Background(), status, lookup)
	assert.ErrorIs(t, err, boom)
	assert.True(t, v.Blocked, "a read error must block, not allow")
}

func TestVerifyTruncatedFailsClosedWithoutReads(t *testing.T) {
	status := porchv1alpha2.UpstreamDependentsStatus{Truncated: true, Dependents: entries("a")}
	lookup, asked := fakeLookup(nil)

	v, err := Verify(context.Background(), status, lookup)
	require.NoError(t, err)
	assert.True(t, v.Blocked)
	assert.Contains(t, v.Reason, "truncated")
	assert.Empty(t, *asked)
}

func TestMaxDependentsIsFourThousand(t *testing.T) {
	assert.Equal(t, 4000, MaxDependents)
}
