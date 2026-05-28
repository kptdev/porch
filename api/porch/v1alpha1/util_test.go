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

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsValidSubpackageDir(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		expected bool
	}{
		// Invalid cases
		{name: "empty string", dir: "", expected: false},
		{name: "leading slash", dir: "/subpkg", expected: false},
		{name: "trailing slash", dir: "subpkg/", expected: false},
		{name: "double dots at start", dir: "../subpkg", expected: false},
		{name: "double dots in middle", dir: "sub/../pkg", expected: false},
		{name: "double dots at end", dir: "subpkg/..", expected: false},
		{name: "only double dots", dir: "..", expected: false},
		{name: "dot segment at start", dir: "./subpkg", expected: false},
		{name: "dot segment in middle", dir: "sub/./pkg", expected: false},
		{name: "only dot", dir: ".", expected: false},
		{name: "leading and trailing slash", dir: "/subpkg/", expected: false},
		{name: "spaces in path", dir: "sub pkg", expected: false},
		{name: "special characters", dir: "sub@pkg", expected: false},
		{name: "backslash", dir: "sub\\pkg", expected: false},
		{name: "colon in path", dir: "sub:pkg", expected: false},
		{name: "empty segment (double slash)", dir: "sub//pkg", expected: false},

		// Valid cases
		{name: "simple directory", dir: "subpkg", expected: true},
		{name: "nested directory", dir: "path/to/subpkg", expected: true},
		{name: "two levels", dir: "sub/pkg", expected: true},
		{name: "with hyphens", dir: "my-subpkg", expected: true},
		{name: "with underscores", dir: "my_subpkg", expected: true},
		{name: "with dots in name", dir: "my.subpkg", expected: true},
		{name: "numeric name", dir: "123", expected: true},
		{name: "mixed valid chars", dir: "my-sub_pkg.v1/nested-dir", expected: true},
		{name: "deeply nested", dir: "a/b/c/d/e", expected: true},
		{name: "single char segments", dir: "a/b/c", expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidSubpackageDir(tt.dir)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_getSubpackageDir(t *testing.T) {
	tests := []struct {
		name     string
		task     Task
		expected string
	}{
		{
			name:     "Clone task with SubpackageDir",
			task:     Task{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{SubpackageDir: "my-subpkg"}},
			expected: "my-subpkg",
		},
		{
			name:     "Clone task without SubpackageDir",
			task:     Task{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{}},
			expected: "",
		},
		{
			name:     "Upgrade task with SubpackageDir",
			task:     Task{Type: TaskTypeUpgrade, Upgrade: &PackageUpgradeTaskSpec{SubpackageDir: "nested/subpkg"}},
			expected: "nested/subpkg",
		},
		{
			name:     "Upgrade task without SubpackageDir",
			task:     Task{Type: TaskTypeUpgrade, Upgrade: &PackageUpgradeTaskSpec{}},
			expected: "",
		},
		{
			name:     "Init task returns empty",
			task:     Task{Type: TaskTypeInit, Init: &PackageInitTaskSpec{}},
			expected: "",
		},
		{
			name:     "Render task returns empty",
			task:     Task{Type: TaskTypeRender},
			expected: "",
		},
		{
			name:     "Edit task returns empty",
			task:     Task{Type: TaskTypeEdit, Edit: &PackageEditTaskSpec{}},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getSubpackageDir(tt.task)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetSubpackageDir(t *testing.T) {
	tests := []struct {
		name          string
		pkgRev        *PackageRevision
		expectedDir   string
		expectedError string
	}{
		{
			name: "Empty task list returns error",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{},
				},
			},
			expectedError: "task list must have at least one entry",
		},
		{
			name: "More than 2 tasks returns error",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{}},
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{SubpackageDir: "sub"}},
						{Type: TaskTypeRender},
					},
				},
			},
			expectedError: "task list may not have more than two entries",
		},
		{
			name: "SubpackageDir on first task returns error",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{SubpackageDir: "should-not-be-here"}},
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{SubpackageDir: "sub"}},
					},
				},
			},
			expectedError: "subpackage directory may not be specified as the first task",
		},
		{
			name: "Single task without SubpackageDir returns empty",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeInit, Init: &PackageInitTaskSpec{}},
					},
				},
			},
			expectedDir: "",
		},
		{
			name: "Single clone task without SubpackageDir returns empty",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{}},
					},
				},
			},
			expectedDir: "",
		},
		{
			name: "Two tasks with valid clone SubpackageDir",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{}},
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{SubpackageDir: "my-subpkg"}},
					},
				},
			},
			expectedDir: "my-subpkg",
		},
		{
			name: "Two tasks with valid nested clone SubpackageDir",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{}},
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{SubpackageDir: "path/to/subpkg"}},
					},
				},
			},
			expectedDir: "path/to/subpkg",
		},
		{
			name: "Two tasks with valid upgrade SubpackageDir",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{}},
						{Type: TaskTypeUpgrade, Upgrade: &PackageUpgradeTaskSpec{SubpackageDir: "my-subpkg"}},
					},
				},
			},
			expectedDir: "my-subpkg",
		},
		{
			name: "Two tasks with empty SubpackageDir on second clone task returns error",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{}},
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{SubpackageDir: ""}},
					},
				},
			},
			expectedError: "subpackage directory \"\" is invalid",
		},
		{
			name: "Two tasks with invalid SubpackageDir (leading slash)",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{}},
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{SubpackageDir: "/invalid"}},
					},
				},
			},
			expectedError: "subpackage directory \"/invalid\" is invalid",
		},
		{
			name: "Two tasks with invalid SubpackageDir (double dots)",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{}},
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{SubpackageDir: "../escape"}},
					},
				},
			},
			expectedError: "subpackage directory \"../escape\" is invalid",
		},
		{
			name: "Two tasks with invalid SubpackageDir (trailing slash)",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{}},
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{SubpackageDir: "subpkg/"}},
					},
				},
			},
			expectedError: "subpackage directory \"subpkg/\" is invalid",
		},
		{
			name: "Two tasks where second is init returns invalid error",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{}},
						{Type: TaskTypeInit, Init: &PackageInitTaskSpec{}},
					},
				},
			},
			expectedError: "subpackage directory \"\" is invalid",
		},
		{
			name: "Two tasks where second is render returns invalid error",
			pkgRev: &PackageRevision{
				Spec: PackageRevisionSpec{
					Tasks: []Task{
						{Type: TaskTypeClone, Clone: &PackageCloneTaskSpec{}},
						{Type: TaskTypeRender},
					},
				},
			},
			expectedError: "subpackage directory \"\" is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := GetSubpackageDir(tt.pkgRev)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedDir, dir)
			}
		})
	}
}
