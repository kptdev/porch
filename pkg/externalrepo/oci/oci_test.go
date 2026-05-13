// Copyright 2022, 2025 The kpt Authors
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

package oci

import (
	"context"
	"testing"
	"time"

	"github.com/kptdev/kpt/pkg/oci"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestBuildPackageRevision(t *testing.T) {
	ociRepo := ociRepository{}
	ociRepo.storage = &oci.Storage{}

	digestName := oci.ImageDigestName{
		Image:  "",
		Digest: "my-digest",
	}
	_, err := ociRepo.buildPackageRevision(context.TODO(), digestName, "package-name", "workspace_name", 1, time.Now())
	assert.True(t, err != nil)

	_, err = ociRepo.buildPackageRevision(context.TODO(), digestName, "package-name", "", 1, time.Now())
	assert.True(t, err != nil)
}

func TestRepoGettersAndSetters(t *testing.T) {
	fakeRepo := ociRepository{}

	assert.Equal(t, nil, fakeRepo.Close(context.TODO()))
}

func TestPackageGettersAndSetters(t *testing.T) {
	fakePr := ociPackageRevision{
		prKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: "my-namespace",
					Name:      "oci-repo-name",
				},
				Package: "my-package",
			},
			WorkspaceName: "my-workspace",
		},
		parent: &ociRepository{
			key: repository.RepositoryKey{
				Namespace: "my-namespace",
				Name:      "oci-repo-name",
			},
		},
	}

	assert.Equal(t, "oci-repo-name.my-package.my-workspace", fakePr.KubeObjectName())
	assert.Equal(t, "my-namespace", fakePr.KubeObjectNamespace())
	assert.Equal(t, types.UID("fd358d1c-04bc-5038-9727-044129e97880"), fakePr.UID())

	inMeta := metav1.ObjectMeta{}
	inMeta.Name = fakePr.Key().RKey().Name

	err := fakePr.SetMeta(context.TODO(), inMeta)
	assert.Equal(t, nil, err)
	outMeta := fakePr.GetMeta()
	assert.Equal(t, outMeta.Name, inMeta.Name)

	assert.Equal(t, "oci-repo-name", fakePr.parent.key.Name)

	assert.Panics(t, func() { fakePr.ToMainPackageRevision(context.TODO()) }, "The code did not panic")

	assert.False(t, fakePr.IsLatestRevision())
	ts, author := fakePr.GetCommitInfo()
	assert.True(t, ts.IsZero())
	assert.Empty(t, author)
}
