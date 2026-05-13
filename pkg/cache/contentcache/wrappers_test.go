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

package contentcache

import (
	"context"
	"fmt"
	"testing"
	"time"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	mockrepository "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- packageContentWrapper ---

func TestPackageContentWrapper_GetCommitInfo(t *testing.T) {
	mockPkgRev := mockrepository.NewMockPackageRevision(t)
	now := time.Now()
	mockPkgRev.EXPECT().GetCommitInfo().Return(now, "user@example.com")

	w := &packageContentWrapper{inner: mockPkgRev}
	ts, author := w.GetCommitInfo()
	assert.Equal(t, now, ts)
	assert.Equal(t, "user@example.com", author)
}

func TestPackageContentWrapper_GetResourceContents(t *testing.T) {
	mockPkgRev := mockrepository.NewMockPackageRevision(t)
	mockPkgRev.EXPECT().GetResources(mock.Anything).Return(&porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: map[string]string{"Kptfile": "data"},
		},
	}, nil)

	w := &packageContentWrapper{inner: mockPkgRev}
	res, err := w.GetResourceContents(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "data", res["Kptfile"])
}

func TestPackageContentWrapper_GetResourceContents_Error(t *testing.T) {
	mockPkgRev := mockrepository.NewMockPackageRevision(t)
	mockPkgRev.EXPECT().GetResources(mock.Anything).Return(nil, fmt.Errorf("io error"))

	w := &packageContentWrapper{inner: mockPkgRev}
	_, err := w.GetResourceContents(context.Background())
	assert.ErrorContains(t, err, "io error")
}

func TestPackageContentWrapper_GetKptfile(t *testing.T) {
	mockPkgRev := mockrepository.NewMockPackageRevision(t)
	kf := kptfilev1.KptFile{}
	kf.Name = "test"
	mockPkgRev.EXPECT().GetKptfile(mock.Anything).Return(kf, nil)

	w := &packageContentWrapper{inner: mockPkgRev}
	result, err := w.GetKptfile(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "test", result.Name)
}

func TestPackageContentWrapper_GetUpstreamLock(t *testing.T) {
	mockPkgRev := mockrepository.NewMockPackageRevision(t)
	mockPkgRev.EXPECT().GetUpstreamLock(mock.Anything).Return(
		kptfilev1.Upstream{Type: kptfilev1.GitOrigin},
		kptfilev1.Locator{Type: kptfilev1.GitOrigin},
		nil,
	)

	w := &packageContentWrapper{inner: mockPkgRev}
	u, ul, err := w.GetUpstreamLock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, kptfilev1.GitOrigin, u.Type)
	assert.Equal(t, kptfilev1.GitOrigin, ul.Type)
}

func TestPackageContentWrapper_GetLock(t *testing.T) {
	mockPkgRev := mockrepository.NewMockPackageRevision(t)
	mockPkgRev.EXPECT().GetLock(mock.Anything).Return(
		kptfilev1.Upstream{Type: kptfilev1.GitOrigin},
		kptfilev1.Locator{Type: kptfilev1.GitOrigin},
		nil,
	)

	w := &packageContentWrapper{inner: mockPkgRev}
	u, ul, err := w.GetLock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, kptfilev1.GitOrigin, u.Type)
	assert.Equal(t, kptfilev1.GitOrigin, ul.Type)
}

// --- draftSlimWrapper ---

func TestDraftSlimWrapper_Key(t *testing.T) {
	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	key := repository.PackageRevisionKey{
		PkgKey:        repository.PackageKey{Package: testPkg},
		WorkspaceName: testWS,
	}
	mockDraft.EXPECT().Key().Return(key)

	w := &draftSlimWrapper{inner: mockDraft}
	assert.Equal(t, key, w.Key())
}

func TestDraftSlimWrapper_UpdateLifecycle(t *testing.T) {
	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockDraft.EXPECT().UpdateLifecycle(mock.Anything, porchapi.PackageRevisionLifecycleProposed).Return(nil)

	w := &draftSlimWrapper{inner: mockDraft}
	err := w.UpdateLifecycle(context.Background(), "Proposed")
	assert.NoError(t, err)
}

func TestDraftSlimWrapper_UpdateResources(t *testing.T) {
	mockDraft := mockrepository.NewMockPackageRevisionDraft(t)
	mockDraft.EXPECT().UpdateResources(mock.Anything, mock.Anything, mock.Anything).Return(nil)

	w := &draftSlimWrapper{inner: mockDraft}
	err := w.UpdateResources(context.Background(), map[string]string{"f.yaml": "data"}, "commit msg")
	assert.NoError(t, err)
}
