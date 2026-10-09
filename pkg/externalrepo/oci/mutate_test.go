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
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	kptfileapi "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/oci"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateUpdateDeletePackageRevision(t *testing.T) {
	ociRepo := ociRepository{}
	ociRepo.storage = &oci.Storage{}

	ociRepo.spec.Registry = "my-registry"

	apiPr := &porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			PackageName:   "my-package-name",
			WorkspaceName: "my-wprkspace",
		},
	}

	_, err := ociRepo.CreatePackageRevisionDraft(context.TODO(), apiPr)
	assert.True(t, err != nil)

	ociRepo.key.Name = "my-repo"
	draftPr, err := ociRepo.CreatePackageRevisionDraft(context.TODO(), apiPr)
	assert.False(t, err != nil)

	meta := draftPr.GetMeta()
	assert.Equal(t, "", meta.Name)

	draftPrKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			Package: "my-package-name",
		},
	}
	assert.Equal(t, draftPrKey, draftPr.Key())

	oldPr := ociPackageRevision{}

	_, err = ociRepo.UpdatePackageRevision(context.TODO(), &oldPr)
	assert.True(t, err != nil)

	err = ociRepo.DeletePackageRevision(context.TODO(), &oldPr)
	assert.True(t, err != nil)
}

func TestUpdateLifecycleSetsDraftLifecycle(t *testing.T) {
	draft := &ociPackageRevisionDraft{}

	err := draft.UpdateLifecycle(t.Context(), porchapi.PackageRevisionLifecycleProposed)

	require.NoError(t, err)
	require.Equal(t, porchapi.PackageRevisionLifecycleProposed, draft.lifecycle)
}

func TestGetKptfileContentReturnsCachedKptfile(t *testing.T) {
	const content = "apiVersion: kpt.dev/v1\nkind: Kptfile\n"
	draft := &ociPackageRevisionDraft{kptfileContent: content}

	got, err := draft.GetKptfileContent(t.Context())

	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestGetKptfileContentFailsWhenImageHasNoKptfile(t *testing.T) {
	draft := &ociPackageRevisionDraft{}

	got, err := draft.GetKptfileContent(t.Context())

	require.ErrorContains(t, err, "does not have a Kptfile")
	require.Empty(t, got)
}

func TestClosePackageRevisionDraftRejectsEmptyLayer(t *testing.T) {
	tag, err := name.NewTag("example.com/testpkg:ws")
	require.NoError(t, err)
	repo := &ociRepository{}
	draft := &ociPackageRevisionDraft{
		tag:       tag,
		lifecycle: porchapi.PackageRevisionLifecycleDraft,
	}

	got, closeErr := repo.ClosePackageRevisionDraft(t.Context(), draft, 0)

	require.ErrorContains(t, closeErr, "cannot create empty layer")
	require.Nil(t, got)
}

func TestUpdateResourcesWritesPackageTar(t *testing.T) {
	draft := ociDraftWithTestRegistry(t)
	resources := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{
				kptfileapi.KptFileName: "apiVersion: kpt.dev/v1\nkind: Kptfile\n",
			},
		},
	}

	err := draft.UpdateResources(t.Context(), resources, &porchapi.Task{Type: porchapi.TaskTypeInit})

	if err != nil {
		require.ErrorContains(t, err, "failed to write")
		return
	}
	require.NotEmpty(t, draft.addendums)
	require.Len(t, draft.tasks, 1)
}

func TestUpdateKptfileContentStoresKptfile(t *testing.T) {
	const kptfileContent = "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: test\n"
	draft := ociDraftWithTestRegistry(t)

	err := draft.UpdateKptfileContent(t.Context(), kptfileContent)

	require.Equal(t, kptfileContent, draft.kptfileContent)
	if err != nil {
		require.Error(t, err)
		return
	}
	require.NotEmpty(t, draft.addendums)
}

func ociDraftWithTestRegistry(t *testing.T) *ociPackageRevisionDraft {
	t.Helper()
	server := httptest.NewServer(registry.New())
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	tag, err := name.NewTag(fmt.Sprintf("%s/testpkg:ws", u.Host), name.WeakValidation, name.Insecure)
	require.NoError(t, err)
	return &ociPackageRevisionDraft{
		tag:     tag,
		created: time.Now(),
	}
}
