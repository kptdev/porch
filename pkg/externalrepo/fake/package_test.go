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
	"testing"

	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
)

func TestPackageGetters(t *testing.T) {
	fakePkg := FakePackage{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Name:      "my-repo",
				Namespace: "my-namespace",
			},
			Package: "my-package",
		},
	}

	assert.Equal(t, "my-repo.my-package", fakePkg.KubeObjectName())
	assert.Equal(t, "my-package", fakePkg.Key().Package)
	assert.True(t, fakePkg.GetPackage(context.TODO()) != nil)
	assert.Equal(t, 0, fakePkg.GetLatestRevision(context.TODO()))

}
