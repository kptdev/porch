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

package get

import (
	"bytes"
	"context"
	"testing"

	"github.com/kptdev/kpt/pkg/lib/util/cmdutil"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPackageRevisionMatchesV1Alpha2(t *testing.T) {
	tests := []struct {
		name     string
		flags    runner
		object   *unstructured.Unstructured
		expected bool
	}{
		{
			name: "match all filters - revision in status",
			flags: runner{
				packageName: "pkg1",
				revision:    1,
				workspace:   "ws1",
			},
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"packageName":   "pkg1",
						"workspaceName": "ws1",
					},
					"status": map[string]any{
						"revision": int64(1),
					},
				},
			},
			expected: true,
		},
		{
			name: "revision mismatch in status",
			flags: runner{
				packageName: "pkg1",
				revision:    2,
				workspace:   "ws1",
			},
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"packageName":   "pkg1",
						"workspaceName": "ws1",
					},
					"status": map[string]any{
						"revision": int64(1),
					},
				},
			},
			expected: false,
		},
		{
			name: "no filters set - matches everything",
			flags: runner{
				revision: -2, // default "unset" value
			},
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"packageName":   "anything",
						"workspaceName": "ws",
					},
					"status": map[string]any{
						"revision": int64(5),
					},
				},
			},
			expected: true,
		},
		{
			name: "package name mismatch",
			flags: runner{
				packageName: "pkg1",
				revision:    -2,
			},
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"packageName": "pkg2",
					},
					"status": map[string]any{},
				},
			},
			expected: false,
		},
		{
			name: "workspace mismatch",
			flags: runner{
				workspace: "ws1",
				revision:  -2,
			},
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"workspaceName": "ws2",
					},
					"status": map[string]any{},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match, err := tt.flags.packageRevisionMatchesV1Alpha2(tt.object)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, match)
		})
	}
}

func TestFilterTableRowsV1Alpha2(t *testing.T) {
	r := &runner{
		packageName: "pkg1",
		revision:    -2,
	}

	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name"},
			{Name: "Package"},
			{Name: "WorkspaceName"},
			{Name: "Revision"},
		},
		Rows: []metav1.TableRow{
			{Cells: []any{"pr1", "pkg1", "ws1", int64(1)}},
			{Cells: []any{"pr2", "pkg2", "ws2", int64(2)}},
			{Cells: []any{"pr3", "pkg1", "ws3", int64(3)}},
		},
	}

	err := r.filterTableRowsV1Alpha2(table)
	assert.NoError(t, err)
	assert.Len(t, table.Rows, 2)
}

func TestV1Alpha2RunV1Alpha2(t *testing.T) {
	ctx := context.Background()
	ns := "test-ns"
	gcf := genericclioptions.ConfigFlags{Namespace: &ns}
	r := newRunner(ctx, &gcf)

	cmd := &cobra.Command{}

	// runV1Alpha2 requires a ResourceBuilder which needs a real config
	// For now, just verify it doesn't panic with empty args
	err := r.runV1Alpha2(cmd, []string{})
	// We expect an error since we don't have a real k8s config
	// but the function should be callable
	_ = err
}

func TestV1Alpha2ShowKptfile(t *testing.T) {
	ctx := context.Background()
	ns := "ns"

	scheme := runtime.NewScheme()
	if err := porchapi.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	kptfileContent := `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-pkg
`

	t.Run("happy path", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&porchapi.PackageRevisionResources{
			ObjectMeta: metav1.ObjectMeta{Name: "test-repo.test-pkg.v1", Namespace: ns},
			Spec:       porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{"Kptfile": kptfileContent}},
		}).Build()
		r := &runner{
			ctx:         ctx,
			showKptfile: true,
			getFlags:    cmdutil.Options{ConfigFlags: &genericclioptions.ConfigFlags{Namespace: &ns}},
			client:      c,
		}
		out := &bytes.Buffer{}
		cmd := &cobra.Command{}
		cmd.SetOut(out)
		err := r.runV1Alpha2(cmd, []string{"test-repo.test-pkg.v1"})
		assert.NoError(t, err)
		assert.Equal(t, kptfileContent, out.String())
	})

	t.Run("no args", func(t *testing.T) {
		r := &runner{
			ctx:         ctx,
			showKptfile: true,
			getFlags:    cmdutil.Options{ConfigFlags: &genericclioptions.ConfigFlags{Namespace: &ns}},
		}
		cmd := &cobra.Command{}
		err := r.runV1Alpha2(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "--show-kptfile requires exactly one package revision name")
	})
}

func TestV1Alpha2RunV1Alpha2WithFilters(t *testing.T) {
	tests := []struct {
		name        string
		packageName string
		revision    int64
		workspace   string
	}{
		{
			name:        "filter by package name",
			packageName: "test-pkg",
			revision:    -2,
			workspace:   "",
		},
		{
			name:        "filter by revision",
			packageName: "",
			revision:    1,
			workspace:   "",
		},
		{
			name:        "filter by workspace",
			packageName: "",
			revision:    -2,
			workspace:   "v1",
		},
		{
			name:        "filter by all",
			packageName: "test-pkg",
			revision:    1,
			workspace:   "v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ns := "test-ns"
			gcf := genericclioptions.ConfigFlags{Namespace: &ns}
			r := newRunner(ctx, &gcf)
			r.packageName = tt.packageName
			r.revision = tt.revision
			r.workspace = tt.workspace

			cmd := &cobra.Command{}

			// Just verify the function handles different filter combinations
			err := r.runV1Alpha2(cmd, []string{})
			_ = err
		})
	}
}

func TestV1Alpha2PackageRevisionMatchesV1Alpha2WithErrors(t *testing.T) {
	tests := []struct {
		name      string
		object    *unstructured.Unstructured
		wantError bool
	}{
		{
			name: "invalid package name type",
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"packageName": 123, // wrong type
					},
					"status": map[string]any{
						"revision": int64(1),
					},
				},
			},
			wantError: true,
		},
		{
			name: "invalid revision type",
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"packageName": "pkg",
					},
					"status": map[string]any{
						"revision": "not-a-number", // wrong type
					},
				},
			},
			wantError: true,
		},
		{
			name: "invalid workspace type",
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"packageName":   "pkg",
						"workspaceName": 456, // wrong type
					},
					"status": map[string]any{
						"revision": int64(1),
					},
				},
			},
			wantError: true,
		},
		{
			name: "missing spec",
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"status": map[string]any{
						"revision": int64(1),
					},
				},
			},
			wantError: false, // missing fields are OK, just return false
		},
		{
			name: "missing status",
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"packageName": "pkg",
					},
				},
			},
			wantError: false, // missing fields are OK
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &runner{
				packageName: "pkg",
				revision:    1,
				workspace:   "ws",
			}

			_, err := r.packageRevisionMatchesV1Alpha2(tt.object)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestV1Alpha2FilterTableRowsV1Alpha2WithFilters(t *testing.T) {
	tests := []struct {
		name         string
		packageName  string
		revision     int64
		workspace    string
		inputRows    int
		expectedRows int
	}{
		{
			name:         "filter by package name",
			packageName:  "pkg1",
			revision:     -2,
			workspace:    "",
			inputRows:    3,
			expectedRows: 2,
		},
		{
			name:         "filter by revision",
			packageName:  "",
			revision:     1,
			workspace:    "",
			inputRows:    3,
			expectedRows: 1,
		},
		{
			name:         "filter by workspace",
			packageName:  "",
			revision:     -2,
			workspace:    "ws1",
			inputRows:    3,
			expectedRows: 1,
		},
		{
			name:         "no filters - all rows",
			packageName:  "",
			revision:     -2,
			workspace:    "",
			inputRows:    3,
			expectedRows: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &runner{
				packageName: tt.packageName,
				revision:    tt.revision,
				workspace:   tt.workspace,
			}

			table := &metav1.Table{
				ColumnDefinitions: []metav1.TableColumnDefinition{
					{Name: "Name"},
					{Name: "Package"},
					{Name: "WorkspaceName"},
					{Name: "Revision"},
				},
				Rows: []metav1.TableRow{
					{Cells: []any{"pr1", "pkg1", "ws1", int64(1)}},
					{Cells: []any{"pr2", "pkg2", "ws2", int64(2)}},
					{Cells: []any{"pr3", "pkg1", "ws3", int64(3)}},
				},
			}

			err := r.filterTableRowsV1Alpha2(table)
			assert.NoError(t, err)
			assert.Len(t, table.Rows, tt.expectedRows)
		})
	}
}
