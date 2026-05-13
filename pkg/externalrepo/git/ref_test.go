// Copyright 2022 The kpt Authors
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

package git

import (
	"context"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/stretchr/testify/assert"
)

func TestBranchNames(t *testing.T) {
	const main branchName = "main"

	if got, want := main.refInRemote(), "refs/heads/main"; string(got) != want {
		t.Errorf("%s in remote repository: got %s, wnat %s", main, got, want)
	}
	if got, want := main.refInLocal(), "refs/remotes/origin/main"; string(got) != want {
		t.Errorf("%s in local repository: got %s, wnat %s", main, got, want)
	}
}

func TestValidateRefSpecs(t *testing.T) {
	if err := branchRefSpec.Validate(); err != nil {
		t.Errorf("%s validation failed: %v", branchRefSpec, err)
	}
	if err := tagRefSpec.Validate(); err != nil {
		t.Errorf("%s validation failed: %v", tagRefSpec, err)
	}
}

func TestTranslate(t *testing.T) {
	for _, tc := range []struct {
		remote plumbing.ReferenceName
		local  plumbing.ReferenceName
	}{
		{
			remote: "refs/heads/drafts/bucket/v1",
			local:  "refs/remotes/origin/drafts/bucket/v1",
		},
		{
			remote: "refs/tags/bucket/v1",
			local:  "refs/tags/bucket/v1",
		},
		{
			remote: "refs/heads/main",
			local:  "refs/remotes/origin/main",
		},
	} {
		got, err := refInLocalFromRefInRemote(tc.remote)
		if err != nil {
			t.Errorf("refInLocalFromRefInRemote(%s) failed: %v", tc.remote, err)
		}
		if want := tc.local; got != want {
			t.Errorf("refInLocalFromRefInRemote(%s): got %s, want %s", tc.remote, got, want)
		}

		got, err = refInRemoteFromRefInLocal(tc.local)
		if err != nil {
			t.Errorf("refInRemoteFromRefInLocal(%s) failed: %v", tc.local, err)
		}
		if want := tc.remote; got != want {
			t.Errorf("refInRemoteFromRefInLocal(%s): got %s, want %s", tc.local, got, want)
		}
	}
}

func TestGetReferenceName(t *testing.T) {
	ctx := context.TODO()
	fakePR := fake.FakePackageRevision{}

	referenceName := getReferenceName(ctx, &fakePR)
	assert.Equal(t, "", referenceName)

	fakePR.PrKey.PkgKey.Package = "my-package"
	fakePR.PrKey.Revision = 1
	referenceName = getReferenceName(ctx, &fakePR)
	assert.Equal(t, "refs/tags/my-package/v1", referenceName)

	fakePR.PrKey.Revision = -1
	referenceName = getReferenceName(ctx, &fakePR)
	assert.Equal(t, "refs/remotes/origin/", referenceName)

	fakePR.PrKey.PkgKey.RepoKey.PlaceholderWSname = "some-branch"
	referenceName = getReferenceName(ctx, &fakePR)
	assert.Equal(t, "refs/remotes/origin/some-branch", referenceName)

	fakePR.PrKey.PkgKey.Path = "root/sub1/sub2/sub3"
	fakePR.PrKey.Revision = 1
	referenceName = getReferenceName(ctx, &fakePR)
	assert.Equal(t, "refs/tags/root/sub1/sub2/sub3/my-package/v1", referenceName)

	fakePR.PrKey.PkgKey.Path = ""
	fakePR.PrKey.Revision = 1
	referenceName = getReferenceName(ctx, &fakePR)
	assert.Equal(t, "refs/tags/my-package/v1", referenceName)

	fakePR.PrKey.Revision = 0
	fakePR.PackageLifecycle = porchapi.PackageRevisionLifecycleDraft
	referenceName = getReferenceName(ctx, &fakePR)
	assert.Equal(t, "refs/remotes/origin/drafts/my-package/", referenceName)

	fakePR.PrKey.WorkspaceName = "some-workspace"
	referenceName = getReferenceName(ctx, &fakePR)
	assert.Equal(t, "refs/remotes/origin/drafts/my-package/some-workspace", referenceName)

	fakePR.PrKey.PkgKey.Path = "root/sub1/sub2/sub3"
	referenceName = getReferenceName(ctx, &fakePR)
	assert.Equal(t, "refs/remotes/origin/drafts/root/sub1/sub2/sub3/my-package/some-workspace", referenceName)

	fakePR.PackageLifecycle = porchapi.PackageRevisionLifecycleProposed
	referenceName = getReferenceName(ctx, &fakePR)
	assert.Equal(t, "refs/remotes/origin/proposed/root/sub1/sub2/sub3/my-package/some-workspace", referenceName)

	fakePR.PrKey.PkgKey.Path = ""
	referenceName = getReferenceName(ctx, &fakePR)
	assert.Equal(t, "refs/remotes/origin/proposed/my-package/some-workspace", referenceName)
}

func TestRefs(t *testing.T) {
	_, ok := getProposedBranchNameInLocal(plumbing.ReferenceName("hello"))
	assert.False(t, ok)
}
