// Copyright 2022, 2024 The kpt Authors
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

package repository

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestPkgUpdate(t *testing.T) {
	dfUpdater := &DefaultPackageUpdater{}

	testdata, err := filepath.Abs(filepath.Join(".", "testdata", "update"))
	if err != nil {
		t.Fatalf("Failed to find testdata: %v", err)
	}

	localResources, err := loadResourcesFromDirectory(filepath.Join(testdata, "local"))
	if err != nil {
		t.Fatalf("failed to read local resources: %v", err)
	}

	originalResources, err := loadResourcesFromDirectory(filepath.Join(testdata, "original"))
	if err != nil {
		t.Fatalf("failed to read original resources: %v", err)
	}

	upstreamResources, err := loadResourcesFromDirectory(filepath.Join(testdata, "upstream"))
	if err != nil {
		t.Fatalf("failed to read upstream resources: %v", err)
	}

	expectedResources, err := loadResourcesFromDirectory(filepath.Join(testdata, "updated"))
	if err != nil {
		t.Fatalf("failed to read expected updated resources: %v", err)
	}

	updatedResources, err := dfUpdater.Update(context.Background(), localResources, originalResources, upstreamResources, "")
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}

	for k, v := range updatedResources.Contents {
		want := expectedResources.Contents[k]
		if diff := cmp.Diff(want, v); diff != "" && k != "Kptfile" {
			// TODO(droot): figure out correct expectation for Kptfile
			t.Errorf("file: %s unexpected result (-want, +got): %s", k, diff)
		}
	}
}

func performUpdateAndValidate(t *testing.T, strategy string, expectedContent string, expectError bool) {
	updater := &DefaultPackageUpdater{}
	ctx := context.Background()

	localResources := PackageResources{
		Contents: map[string]string{
			"file1.txt": "local content",
		},
	}
	originalResources := PackageResources{
		Contents: map[string]string{
			"file1.txt": "original content",
		},
	}
	upstreamResources := PackageResources{
		Contents: map[string]string{
			"file1.txt": "upstream content",
		},
	}

	updatedResources, err := updater.Update(ctx, localResources, originalResources, upstreamResources, strategy)
	if expectError {
		assert.Error(t, err)
	} else {
		assert.NoError(t, err)
		assert.NotNil(t, updatedResources)
		assert.Equal(t, expectedContent, updatedResources.Contents["file1.txt"])
	}
}

func TestDefaultPackageUpdaterUpdateCopyMerge(t *testing.T) {
	performUpdateAndValidate(t, "copy-merge", "upstream content", false)
}

func TestDefaultPackageUpdaterUpdateFastForward(t *testing.T) {
	performUpdateAndValidate(t, "fast-forward", "", true)
}

func TestDefaultPackageUpdaterUpdateForceDeleteReplace(t *testing.T) {
	performUpdateAndValidate(t, "force-delete-replace", "upstream content", false)
}

func TestDefaultPackageUpdaterUpdateResourceMerge(t *testing.T) {
	performUpdateAndValidate(t, "resource-merge", "local content", false)
}

func TestDefaultPackageUpdaterdo(t *testing.T) {
	updater := &DefaultPackageUpdater{}
	ctx := context.Background()

	localPkgDir := t.TempDir()
	originalPkgDir := t.TempDir()
	upstreamPkgDir := t.TempDir()

	// Create test files
	err := writeResourcesToDirectory(localPkgDir, PackageResources{
		Contents: map[string]string{
			"file1.txt": "local content",
		},
	})
	assert.NoError(t, err)

	err = writeResourcesToDirectory(originalPkgDir, PackageResources{
		Contents: map[string]string{
			"file1.txt": "original content",
		},
	})
	assert.NoError(t, err)

	err = writeResourcesToDirectory(upstreamPkgDir, PackageResources{
		Contents: map[string]string{
			"file1.txt": "upstream content",
		},
	})
	assert.NoError(t, err)

	strategy := "copy-merge"
	err = updater.do(ctx, localPkgDir, originalPkgDir, upstreamPkgDir, strategy)
	assert.NoError(t, err)

	updatedResources, err := loadResourcesFromDirectory(localPkgDir)
	assert.NoError(t, err)
	assert.Equal(t, "upstream content", updatedResources.Contents["file1.txt"])
}
