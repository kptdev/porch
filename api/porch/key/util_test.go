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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComposePkgObjName(t *testing.T) {
	pkgKey := PackageKey{
		RepoKey: RepositoryKey{
			Namespace:         "the-ns",
			Name:              "the-repo",
			Path:              "the/dir/path",
			PlaceholderWSname: "the-placeholder-ws-name",
		},
		Path:    "the/pkg/path",
		Package: "the-package-name",
	}

	assert.Equal(t, "the-repo.the.pkg.path.the-package-name", ComposePkgObjName(pkgKey))

	pkgKey.Path = ""
	assert.Equal(t, "the-repo.the-package-name", ComposePkgObjName(pkgKey))
}

func TestComposePkgRevObjName(t *testing.T) {
	pkgRevKey := PackageRevisionKey{
		PkgKey: PackageKey{
			RepoKey: RepositoryKey{
				Namespace:         "the-ns",
				Name:              "the-repo",
				Path:              "the/dir/path",
				PlaceholderWSname: "the-placeholder-ws-name",
			},
			Path:    "the/pkg/path",
			Package: "the-package-name",
		},
		Revision:      123,
		WorkspaceName: "the-ws-name",
	}

	assert.Equal(t, "the-repo.the.pkg.path.the-package-name.the-ws-name", ComposePkgRevObjName(pkgRevKey))

	pkgRevKey.Revision = -1
	assert.Equal(t, "the-repo.the.pkg.path.the-package-name.the-ws-name", ComposePkgRevObjName(pkgRevKey))
}
