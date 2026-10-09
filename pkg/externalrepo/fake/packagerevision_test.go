// Copyright 2025 The kpt Authors
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

package fake

import (
	"context"
	"fmt"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util/selector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestPackageRevisionGetters(t *testing.T) {
	fakePr := FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name:      "my-repo",
					Namespace: "my-namespace",
				},
				Package: "my-package",
			},
			WorkspaceName: "my-workspace",
		},
	}

	assert.Equal(t, "my-repo.my-package.my-workspace", fakePr.KubeObjectName())
	assert.Equal(t, "my-namespace", fakePr.KubeObjectNamespace())
	assert.Equal(t, types.UID("7007e8aa-0928-50f9-b980-92a44942f055"), fakePr.UID())
	assert.True(t, fakePr.UpdateResources(context.TODO(), nil, nil) == nil)

	meta := fakePr.GetMeta()
	assert.Equal(t, "", meta.Name)
	fakePr.Meta = &metav1.ObjectMeta{Name: "foo"}
	meta = fakePr.GetMeta()
	assert.Equal(t, "foo", meta.Name)

	assert.True(t, fakePr.SetMeta(context.TODO(), meta) == nil)
	assert.True(t, fakePr.IsLatestRevision())

	ts, author := fakePr.GetCommitInfo()
	assert.True(t, ts.IsZero())
	assert.Empty(t, author)
}

func TestToMainPackageRevisionIsUnimplemented(t *testing.T) {
	fpr := &FakePackageRevision{}

	require.Panics(t, func() {
		fpr.ToMainPackageRevision(t.Context())
	})
}

func TestResourceVersionReturnsPackageRevisionResourceVersion(t *testing.T) {
	const resourceVersion = "42"
	fpr := &FakePackageRevision{
		PackageRevision: &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{ResourceVersion: resourceVersion},
		},
	}

	got := fpr.ResourceVersion()

	require.Equal(t, resourceVersion, got)
	require.Contains(t, fpr.Ops, "ResourceVersion")
}

func TestLifecycleReturnsConfiguredLifecycle(t *testing.T) {
	fpr := &FakePackageRevision{PackageLifecycle: porchapi.PackageRevisionLifecycleProposed}

	got := fpr.Lifecycle(t.Context())

	require.Equal(t, porchapi.PackageRevisionLifecycleProposed, got)
}

func TestGetPackageRevisionReturnsConfiguredValueAndError(t *testing.T) {
	want := &porchapi.PackageRevision{ObjectMeta: metav1.ObjectMeta{Name: "pr"}}
	wantErr := fmt.Errorf("get pr failed")
	fpr := &FakePackageRevision{PackageRevision: want, Err: wantErr}

	got, err := fpr.GetPackageRevision(t.Context(), false)

	require.Equal(t, want, got)
	require.Equal(t, wantErr, err)
}

func TestGetResourcesReturnsConfiguredValueAndError(t *testing.T) {
	want := &porchapi.PackageRevisionResources{Spec: porchapi.PackageRevisionResourcesSpec{
		Resources: map[string]string{"a.yaml": "a"},
	}}
	fpr := &FakePackageRevision{Resources: want}

	got, err := fpr.GetResources(t.Context())

	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestGetFilteredResourcesReturnsConfiguredValue(t *testing.T) {
	want := &porchapi.PackageRevisionResources{Spec: porchapi.PackageRevisionResourcesSpec{
		Resources: map[string]string{"Kptfile": "kf"},
	}}
	fpr := &FakePackageRevision{Resources: want}

	got, err := fpr.GetFilteredResources(t.Context(), selector.KptFile)

	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestGetKptfileReturnsConfiguredValue(t *testing.T) {
	want := kptfilev1.KptFile{Info: &kptfilev1.PackageInfo{Description: "pkg"}}
	fpr := &FakePackageRevision{Kptfile: want}

	got, err := fpr.GetKptfile(t.Context())

	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestGetUpstreamLockAndGetLockReturnConfiguredLocks(t *testing.T) {
	upstream := kptfilev1.Upstream{Type: "git"}
	lock := kptfilev1.Locator{Git: &kptfilev1.GitLock{Commit: "abc"}}
	fpr := &FakePackageRevision{
		Kptfile: kptfilev1.KptFile{
			Upstream:     &upstream,
			UpstreamLock: &lock,
		},
	}

	gotUpstream, gotLock, err := fpr.GetUpstreamLock(t.Context())
	gotUpstream2, gotLock2, err2 := fpr.GetLock(t.Context())

	require.NoError(t, err)
	require.Equal(t, upstream, gotUpstream)
	require.Equal(t, lock, gotLock)
	require.NoError(t, err2)
	require.Equal(t, upstream, gotUpstream2)
	require.Equal(t, lock, gotLock2)
}

func TestUpdateLifecycleUpdatesDraftAndAPIObject(t *testing.T) {
	fpr := &FakePackageRevision{
		PackageRevision: &porchapi.PackageRevision{},
	}

	err := fpr.UpdateLifecycle(t.Context(), porchapi.PackageRevisionLifecyclePublished)

	require.NoError(t, err)
	require.Equal(t, porchapi.PackageRevisionLifecyclePublished, fpr.PackageLifecycle)
	require.Equal(t, porchapi.PackageRevisionLifecyclePublished, fpr.PackageRevision.Spec.Lifecycle)
}

func TestUpdateKptfileContentCreatesResourcesWhenMissing(t *testing.T) {
	const content = "apiVersion: kpt.dev/v1\nkind: Kptfile\n"
	fpr := &FakePackageRevision{}

	err := fpr.UpdateKptfileContent(t.Context(), content)

	require.NoError(t, err)
	got, err := fpr.GetKptfileContent(t.Context())
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestGetKptfileContentFailsWhenKptfileIsMissing(t *testing.T) {
	fpr := &FakePackageRevision{}

	got, err := fpr.GetKptfileContent(t.Context())

	require.ErrorContains(t, err, "does not have a Kptfile")
	require.Empty(t, got)
}
