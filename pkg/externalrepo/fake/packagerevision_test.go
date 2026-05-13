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
