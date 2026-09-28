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

package deps

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testConfigFlags(ns string) *genericclioptions.ConfigFlags {
	return &genericclioptions.ConfigFlags{Namespace: &ns}
}

func loc(repo, dir, ref, commit string) *porchv1alpha2.Locator {
	return &porchv1alpha2.Locator{Type: "git", Git: &porchv1alpha2.GitLock{
		Repo: repo, Directory: dir, Ref: ref, Commit: commit}}
}

func pr(name string, status porchv1alpha2.PackageRevisionStatus) *porchv1alpha2.PackageRevision {
	return &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns1"},
		Status:     status,
	}
}

func newTestRunner(t *testing.T, dependents, canDelete bool, objs ...client.Object) (*v1alpha2Runner, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, porchv1alpha2.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	ns := "ns1"
	cmd := &cobra.Command{}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	r := &v1alpha2Runner{
		ctx:        context.Background(),
		client:     c,
		cmd:        cmd,
		dependents: dependents,
		canDelete:  canDelete,
	}
	// EnsureNamespace reads cfg; inject a minimal cfg with namespace set.
	r.cfg = testConfigFlags(ns)
	return r, out, errOut
}

func TestReportUpstreams(t *testing.T) {
	target := pr("custom.v3", porchv1alpha2.PackageRevisionStatus{
		UpstreamLock: loc("https://v.com/v.git", "root-bp", "v1", "aaa"),
		SubpackageUpstreams: []porchv1alpha2.SubpackageUpstream{
			{Path: "sub/net", Upstream: loc("https://v.com/v.git", "net-bp", "v2", "bbb")},
		},
	})
	r, out, _ := newTestRunner(t, false, false, target)

	require.NoError(t, r.runE(r.cmd, []string{"custom.v3"}))
	s := out.String()
	assert.Contains(t, s, "custom.v3 depends on:")
	assert.Contains(t, s, "root:")
	assert.Contains(t, s, "root-bp")
	assert.Contains(t, s, "subpackage sub/net")
	assert.Contains(t, s, "net-bp")
}

func TestReportUpstreamsNone(t *testing.T) {
	target := pr("standalone.v1", porchv1alpha2.PackageRevisionStatus{})
	r, out, _ := newTestRunner(t, false, false, target)

	require.NoError(t, r.runE(r.cmd, []string{"standalone.v1"}))
	assert.Contains(t, out.String(), "no upstream dependencies")
}

func TestReportDependentsFound(t *testing.T) {
	self := loc("https://v.com/v.git", "vendor-bp", "v2", "ccc")
	vendor := pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{SelfLock: self})
	// Two dependents referencing vendor via upstreamKeys, one unrelated.
	depA := pr("custom-a", porchv1alpha2.PackageRevisionStatus{
		UpstreamKeys: []string{porchv1alpha2.UpstreamKey(self)}})
	depB := pr("deploy-b", porchv1alpha2.PackageRevisionStatus{
		UpstreamKeys: []string{porchv1alpha2.UpstreamKey(self)}})
	other := pr("unrelated", porchv1alpha2.PackageRevisionStatus{
		UpstreamKeys: []string{"https://v.com/v.git|other|v9"}})

	r, out, _ := newTestRunner(t, true, false, vendor, depA, depB, other)
	require.NoError(t, r.runE(r.cmd, []string{"vendor.v2"}))

	s := out.String()
	assert.Contains(t, s, "depended on by 2 package(s)")
	assert.Contains(t, s, "ns1/custom-a")
	assert.Contains(t, s, "ns1/deploy-b")
	assert.NotContains(t, s, "unrelated")
}

func TestReportDependentsNone(t *testing.T) {
	vendor := pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{
		SelfLock: loc("https://v.com/v.git", "vendor-bp", "v2", "ccc")})
	r, out, _ := newTestRunner(t, true, false, vendor)

	require.NoError(t, r.runE(r.cmd, []string{"vendor.v2"}))
	assert.Contains(t, out.String(), "has no dependents")
}

func TestCanDeleteBlocked(t *testing.T) {
	self := loc("https://v.com/v.git", "vendor-bp", "v2", "ccc")
	vendor := pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{SelfLock: self})
	dep := pr("custom-a", porchv1alpha2.PackageRevisionStatus{
		UpstreamKeys: []string{porchv1alpha2.UpstreamKey(self)}})

	r, _, _ := newTestRunner(t, false, true, vendor, dep)
	err := r.runE(r.cmd, []string{"vendor.v2"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be deleted")
	assert.Contains(t, err.Error(), "custom-a")
}

func TestCanDeleteClear(t *testing.T) {
	vendor := pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{
		SelfLock: loc("https://v.com/v.git", "vendor-bp", "v2", "ccc")})
	r, out, _ := newTestRunner(t, false, true, vendor)

	require.NoError(t, r.runE(r.cmd, []string{"vendor.v2"}))
	assert.Contains(t, out.String(), "can be deleted")
}

func TestDependentsTruncatedWarning(t *testing.T) {
	self := loc("https://v.com/v.git", "vendor-bp", "v2", "ccc")
	vendor := pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{SelfLock: self})
	// A package with a truncated projection — results may be incomplete.
	trunc := pr("big", porchv1alpha2.PackageRevisionStatus{DependencyTruncated: true})

	r, _, errOut := newTestRunner(t, true, false, vendor, trunc)
	require.NoError(t, r.runE(r.cmd, []string{"vendor.v2"}))
	assert.Contains(t, errOut.String(), "truncated")
}

func TestDependentsNoSelfLocator(t *testing.T) {
	// No SelfLock resolved: no locator match is possible, and no name-based
	// dependent exists, so the result is "has no dependents".
	vendor := pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{})
	r, out, _ := newTestRunner(t, true, false, vendor)

	require.NoError(t, r.runE(r.cmd, []string{"vendor.v2"}))
	assert.Contains(t, out.String(), "has no dependents")
}

func TestCanDeleteNoSelfLocator(t *testing.T) {
	// No SelfLock resolved and no name-based dependents: deletable.
	vendor := pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{})
	r, out, _ := newTestRunner(t, false, true, vendor)

	require.NoError(t, r.runE(r.cmd, []string{"vendor.v2"}))
	assert.Contains(t, out.String(), "can be deleted")
}

// A package with no SelfLock can still be blocked by a name-based dependent.
func TestDependentsNameBasedNoSelfLocator(t *testing.T) {
	vendor := pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{})
	dep := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "custom-a", Namespace: "ns1"},
		Spec: porchv1alpha2.PackageRevisionSpec{
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &porchv1alpha2.UpstreamPackage{
					UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "vendor.v2"},
				},
			},
		},
	}
	r, out, _ := newTestRunner(t, true, false, vendor, dep)
	require.NoError(t, r.runE(r.cmd, []string{"vendor.v2"}))
	assert.Contains(t, out.String(), "custom-a")
}

func TestCanDeleteBlockedTruncatesList(t *testing.T) {
	self := loc("https://v.com/v.git", "vendor-bp", "v2", "ccc")
	selfKey := porchv1alpha2.UpstreamKey(self)
	objs := []client.Object{pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{SelfLock: self})}
	// More dependents than maxListed so joinCapped elides the tail.
	for i := 0; i < maxListed+5; i++ {
		objs = append(objs, pr(fmt.Sprintf("dep-%03d", i), porchv1alpha2.PackageRevisionStatus{
			UpstreamKeys: []string{selfKey}}))
	}

	r, _, _ := newTestRunner(t, false, true, objs...)
	err := r.runE(r.cmd, []string{"vendor.v2"})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "cannot be deleted")
	assert.Contains(t, msg, fmt.Sprintf("referenced by %d downstream package(s)", maxListed+5))
	assert.Contains(t, msg, "... (5 more)")
}

func TestJoinCapped(t *testing.T) {
	// Under the cap: full list, no elision.
	assert.Equal(t, "a, b, c", joinCapped([]string{"a", "b", "c"}, 5))
	// At the cap boundary: still full list.
	assert.Equal(t, "a, b, c", joinCapped([]string{"a", "b", "c"}, 3))
	// Over the cap: elided with remainder count.
	assert.Equal(t, "a, b, ... (2 more)", joinCapped([]string{"a", "b", "c", "d"}, 2))
}

// A non-existent package returns the Get error, not a panic or empty success.
func TestDepsNonExistentPackage(t *testing.T) {
	r, _, _ := newTestRunner(t, false, false) // no objects seeded
	err := r.runE(r.cmd, []string{"does-not-exist"})
	assert.Error(t, err)
}

// The target must not count itself as a dependent (self-reference guard).
func TestDependentsSkipsSelf(t *testing.T) {
	self := loc("https://v.com/v.git", "vendor-bp", "v2", "ccc")
	// A package whose own upstreamKeys happen to contain its own selfKey.
	vendor := pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{
		SelfLock:     self,
		UpstreamKeys: []string{porchv1alpha2.UpstreamKey(self)},
	})
	r, out, _ := newTestRunner(t, true, false, vendor)

	require.NoError(t, r.runE(r.cmd, []string{"vendor.v2"}))
	assert.Contains(t, out.String(), "has no dependents")
}

// A dependent that references the target via a SUB-PACKAGE key (not top-level)
// is still found by the reverse query.
func TestDependentsViaSubpackageKey(t *testing.T) {
	self := loc("https://v.com/v.git", "vendor-bp", "v2", "ccc")
	vendor := pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{SelfLock: self})
	// Dependent's own root upstream is something else; the match is a sub-package key.
	dep := pr("custom-a", porchv1alpha2.PackageRevisionStatus{
		UpstreamLock: loc("https://v.com/v.git", "other-bp", "v1", "xxx"),
		UpstreamKeys: []string{
			porchv1alpha2.UpstreamKey(loc("https://v.com/v.git", "other-bp", "v1", "")),
			porchv1alpha2.UpstreamKey(self),
		},
	})
	r, out, _ := newTestRunner(t, true, false, vendor, dep)

	require.NoError(t, r.runE(r.cmd, []string{"vendor.v2"}))
	assert.Contains(t, out.String(), "custom-a")
}

// can-delete blocks when a dependent's projection is truncated (fail closed),
// even if that dependent's known keys don't list the target.
func TestCanDeleteBlockedByTruncatedDependent(t *testing.T) {
	self := loc("https://v.com/v.git", "vendor-bp", "v2", "ccc")
	vendor := pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{SelfLock: self})
	trunc := pr("big", porchv1alpha2.PackageRevisionStatus{DependencyTruncated: true})

	r, _, _ := newTestRunner(t, false, true, vendor, trunc)
	err := r.runE(r.cmd, []string{"vendor.v2"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be deleted")
	assert.Contains(t, err.Error(), "big")
}

// Multiple dependents are all listed, sorted.
func TestDependentsMultiple(t *testing.T) {
	self := loc("https://v.com/v.git", "vendor-bp", "v2", "ccc")
	key := porchv1alpha2.UpstreamKey(self)
	vendor := pr("vendor.v2", porchv1alpha2.PackageRevisionStatus{SelfLock: self})
	d1 := pr("zeta", porchv1alpha2.PackageRevisionStatus{UpstreamKeys: []string{key}})
	d2 := pr("alpha", porchv1alpha2.PackageRevisionStatus{UpstreamKeys: []string{key}})

	r, out, _ := newTestRunner(t, true, false, vendor, d1, d2)
	require.NoError(t, r.runE(r.cmd, []string{"vendor.v2"}))

	s := out.String()
	assert.Contains(t, s, "depended on by 2 package(s)")
	// Sorted: alpha before zeta.
	assert.Less(t, indexOf(s, "alpha"), indexOf(s, "zeta"))
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
