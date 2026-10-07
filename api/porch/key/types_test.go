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

package key

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryKey(t *testing.T) {
	repoKey := RepositoryKey{
		Namespace:         "my-ns",
		Name:              "my-repo",
		Path:              "my/dir/path",
		PlaceholderWSname: "my-ws-name",
	}

	assert.Equal(t, "my-ns:my-repo:my/dir/path:my-ws-name", repoKey.String())
	assert.Equal(t, repoKey, repoKey)

	otherRepoKey := RepositoryKey{}
	assert.True(t, otherRepoKey.Matches(repoKey))

	otherRepoKey.Namespace = "other-ns"
	otherRepoKey.Name = "other-repo"
	otherRepoKey.Path = "other/dir/path"
	otherRepoKey.PlaceholderWSname = "other-ws-name"
	assert.False(t, otherRepoKey.Matches(repoKey))

	otherRepoKey.Namespace = "my-ns"
	assert.False(t, otherRepoKey.Matches(repoKey))

	otherRepoKey.Name = "my-repo"
	assert.False(t, otherRepoKey.Matches(repoKey))

	otherRepoKey.Path = "my/dir/path"
	assert.False(t, otherRepoKey.Matches(repoKey))

	otherRepoKey.Path = "my/dir/path"
	otherRepoKey.PlaceholderWSname = "my-ws-name"
	assert.True(t, otherRepoKey.Matches(repoKey))
}

func TestPackageKey(t *testing.T) {
	pkgKey := PackageKey{
		Path:    "my/pkg/path",
		Package: "my-package-name",
	}

	assert.Equal(t, "::::my/pkg/path:my-package-name", pkgKey.String())
	assert.Equal(t, pkgKey, pkgKey)

	otherPkgKey := PackageKey{}
	assert.True(t, otherPkgKey.Matches(pkgKey))

	otherPkgKey.Path = "other/pkg/path"
	otherPkgKey.Package = "other-ws-name"
	assert.False(t, otherPkgKey.Matches(pkgKey))

	otherPkgKey.Path = "my/pkg/path"
	assert.False(t, otherPkgKey.Matches(pkgKey))

	otherPkgKey.Package = "my-package-name"
	assert.True(t, otherPkgKey.Matches(pkgKey))

	assert.Equal(t, "my/pkg/path/my-package-name", pkgKey.ToPkgPathname())
	assert.Equal(t, "my/pkg/path/my-package-name", pkgKey.ToFullPathname())

	pkgKey.RepoKey.Path = "dir/path"
	assert.Equal(t, "dir/path/my/pkg/path/my-package-name", pkgKey.ToFullPathname())

	testRepoKey := RepositoryKey{
		Namespace:         "ns",
		Name:              "repo",
		Path:              "dir/path",
		PlaceholderWSname: "ws-name",
	}
	pkgKey.RepoKey = testRepoKey
	assert.Equal(t, pkgKey, FromFullPathname(testRepoKey, pkgKey.ToPkgPathname()))
	assert.Equal(t, pkgKey, FromFullPathname(pkgKey.RKey(), pkgKey.ToPkgPathname()))

	assert.Equal(t, "ns", pkgKey.K8SNS())
	assert.Equal(t, "repo.my.pkg.path.my-package-name", pkgKey.K8SName())

	pkgKey.RepoKey = RepositoryKey{}
	assert.Equal(t, pkgKey, FromFullPathname(pkgKey.RKey(), pkgKey.ToPkgPathname()))

	pkgKey.Path = ""
	assert.Equal(t, pkgKey, FromFullPathname(pkgKey.RKey(), pkgKey.ToPkgPathname()))

	parPRKey, err := PkgK8sName2Key("my-ns", "repo.pkg.path.package-name.my-ws-name")
	assert.Nil(t, err)
	assert.Equal(t, "repo", parPRKey.RKey().Name)

	_, err = PkgK8sName2Key("my-ns", "aaa")
	assert.NotNil(t, err)

	assert.Equal(t, "pkg", K8SName2PkgName("repo.pkg"))
}

func TestPackageRevisionKey(t *testing.T) {
	pkgRevKey := PackageRevisionKey{
		Revision:      1,
		WorkspaceName: "my-ws-name",
	}

	assert.Equal(t, "::::::1:my-ws-name", pkgRevKey.String())
	assert.Equal(t, pkgRevKey, pkgRevKey)

	otherPkgRevKey := PackageRevisionKey{}
	assert.True(t, otherPkgRevKey.Matches(pkgRevKey))

	otherPkgRevKey.Revision = 2
	otherPkgRevKey.WorkspaceName = "other-ws-name"
	assert.False(t, otherPkgRevKey.Matches(pkgRevKey))

	otherPkgRevKey.Revision = 1
	assert.False(t, otherPkgRevKey.Matches(pkgRevKey))

	otherPkgRevKey.WorkspaceName = "my-ws-name"
	assert.True(t, otherPkgRevKey.Matches(pkgRevKey))

	testPkgKey := PackageKey{
		Path:    "pkg/path",
		Package: "package-name",
	}
	pkgRevKey.PkgKey = testPkgKey
	assert.Equal(t, testPkgKey, pkgRevKey.PKey())

	testRepoKey := RepositoryKey{
		Namespace:         "ns",
		Name:              "repo",
		Path:              "dir/path",
		PlaceholderWSname: "ws-name",
	}
	pkgRevKey.PkgKey.RepoKey = testRepoKey
	assert.Equal(t, testRepoKey, pkgRevKey.RKey())

	assert.Equal(t, "ns", pkgRevKey.K8SNS())
	assert.Equal(t, "repo.pkg.path.package-name.my-ws-name", pkgRevKey.K8SName())
	assert.Equal(t, "ws-name", K8SName2PkgRevWSName("pkg-name", "pkg-name.ws-name"))

	parPRKey, err := PkgRevK8sName2Key("my-ns", "repo.pkg.path.package-name.my-ws-name")
	assert.Nil(t, err)
	assert.Equal(t, "repo", parPRKey.RKey().Name)

	_, err = PkgRevK8sName2Key("my-ns", "")
	assert.NotNil(t, err)
}

func TestGetPRWorkspaceName(t *testing.T) {
	_, err := PkgRevK8sName2Key("my-ns", "")
	assert.NotNil(t, err)
	assert.True(t, strings.Contains(err.Error(), "package name part \"\" of object name invalid"))

	_, err = PkgRevK8sName2Key("my-ns", "hello")
	assert.NotNil(t, err)
	assert.True(t, strings.Contains(err.Error(), "package name part \"\" of object name invalid"))

	prKey, err := PkgRevK8sName2Key("my-ns", "repo.hello.there")
	assert.Nil(t, err)
	assert.Equal(t, "hello", prKey.PkgKey.Package)
	assert.Equal(t, "", prKey.PkgKey.Path)
	assert.Equal(t, "there", prKey.WorkspaceName)

	prKey, err = PkgRevK8sName2Key("my-ns", "repo..")
	assert.NotNil(t, err)
	assert.True(t, strings.Contains(err.Error(), "package name part \"\" of object name invalid"))

	prKey, err = PkgRevK8sName2Key("my-ns", "v.")
	assert.NotNil(t, err)
	assert.True(t, strings.Contains(err.Error(), "workspace name part \"\" of package revision name invalid"))

	prKey, err = PkgRevK8sName2Key("my-ns", "repo.hello.v1.2.3")
	assert.Nil(t, err)
	assert.Equal(t, "hello", prKey.PkgKey.Package)
	assert.Equal(t, "", prKey.PkgKey.Path)
	assert.Equal(t, "v1.2.3", prKey.WorkspaceName)

	prKey, err = PkgRevK8sName2Key("my-ns", "repo.hello.v1.2")
	assert.Nil(t, err)
	assert.Equal(t, "hello", prKey.PkgKey.Package)
	assert.Equal(t, "", prKey.PkgKey.Path)
	assert.Equal(t, "v1.2", prKey.WorkspaceName)

	prKey, err = PkgRevK8sName2Key("my-ns", "repo.hello.v1")
	assert.Nil(t, err)
	assert.Equal(t, "hello", prKey.PkgKey.Package)
	assert.Equal(t, "", prKey.PkgKey.Path)
	assert.Equal(t, "v1", prKey.WorkspaceName)

	prKey, err = PkgRevK8sName2Key("my-ns", "repo.hello.v1.v1")
	assert.Nil(t, err)
	assert.Equal(t, "v1", prKey.PkgKey.Package)
	assert.Equal(t, "hello", prKey.PkgKey.Path)
	assert.Equal(t, "v1", prKey.WorkspaceName)

	prKey, err = PkgRevK8sName2Key("my-ns", "repo.hello.v1.2.3.v4.5.6")
	assert.Nil(t, err)
	assert.Equal(t, "3", prKey.PkgKey.Package)
	assert.Equal(t, "hello/v1/2", prKey.PkgKey.Path)
	assert.Equal(t, "v4.5.6", prKey.WorkspaceName)

	prKey, err = PkgRevK8sName2Key("my-ns", "repo.hello.v1.2.3.end")
	assert.Nil(t, err)
	assert.Equal(t, "3", prKey.PkgKey.Package)
	assert.Equal(t, "hello/v1/2", prKey.PkgKey.Path)
	assert.Equal(t, "end", prKey.WorkspaceName)
}

func TestRepositoryKey_K8SNS(t *testing.T) {
	k := RepositoryKey{
		Namespace: "foo",
	}
	require.Equal(t, "foo", k.K8SNS())
}

func TestRepositoryKey_K8SName(t *testing.T) {
	k := RepositoryKey{
		Name: "foo",
	}
	require.Equal(t, "foo", k.K8SName())
}
