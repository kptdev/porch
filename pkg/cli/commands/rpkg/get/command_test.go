// Copyright 2025-2026 The kpt Authors
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
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kptdev/kpt/pkg/lib/util/cmdutil"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetters(t *testing.T) {
	cells := make([]any, 1)
	cells[0] = int64(1234)

	_, retVal1 := getInt64Cell(cells, -1)
	assert.False(t, retVal1)

	val, retVal2 := getInt64Cell(cells, 0)
	assert.True(t, retVal2)
	assert.Equal(t, int64(1234), val)
}

func TestRunnerPreRunE(t *testing.T) {
	ns := "ns"
	ctx := context.Background()
	gcf := genericclioptions.ConfigFlags{Namespace: &ns}
	r := newRunner(ctx, &gcf)
	cmd := NewCommand(ctx, &gcf)

	err := r.preRunE(cmd, []string{})
	assert.Nil(t, err)

	if r.requestTable != true {
		t.Errorf("Expected requestTable to be true, got %v", r.requestTable)
	}
}

func TestGetCmd(t *testing.T) {
	ctx := context.Background()
	pkgRevName := "repo-testrevision"
	ns := "ns"

	scheme := runtime.NewScheme()
	if err := porchapi.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add porch API to scheme: %v", err)
	}

	testCases := map[string]struct {
		output  string
		args    []string
		wantErr bool
	}{
		"Get package": {
			output:  pkgRevName + "\n",
			args:    []string{pkgRevName},
			wantErr: true,
		},
		"Args not provided": {
			wantErr: true,
		},
	}

	for tn := range testCases {
		tc := testCases[tn]
		t.Run(tn, func(t *testing.T) {

			r := newRunner(ctx, &genericclioptions.ConfigFlags{Namespace: &ns})
			cmd := &cobra.Command{}

			go func() {
				err := r.runE(cmd, tc.args)
				if err != nil && !tc.wantErr {
					t.Errorf("unexpected error: %v", err)
				}
			}()

		})
	}
}

type MockRunner struct {
	*runner
}

func (r *MockRunner) packageRevisionMatches(o *unstructured.Unstructured) (bool, error) {
	return r.runner.packageRevisionMatches(o)
}

func TestPackageRevisionMatches(t *testing.T) {
	tests := []struct {
		name       string
		flags      runner
		object     *unstructured.Unstructured
		expected   bool
		shouldFail bool
	}{
		{
			name: "match all filters",
			flags: runner{
				packageName: "pkg1",
				revision:    1,
				workspace:   "ws1",
			},
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"packageName":   "pkg1",
						"revision":      int64(1),
						"workspaceName": "ws1",
					},
				},
			},
			expected: true,
		},
		{
			name: "package name mismatch",
			flags: runner{
				packageName: "pkg1",
				revision:    1,
				workspace:   "ws1",
			},
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"packageName":   "pkg2",
						"revision":      int64(1),
						"workspaceName": "ws1",
					},
				},
			},
			expected: false,
		},
		{
			name: "revision mismatch",
			flags: runner{
				packageName: "pkg1",
				revision:    2,
				workspace:   "ws1",
			},
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"packageName":   "pkg1",
						"revision":      int64(1),
						"workspaceName": "ws1",
					},
				},
			},
			expected: false,
		},
		{
			name: "workspace mismatch",
			flags: runner{
				packageName: "pkg1",
				revision:    1,
				workspace:   "ws2",
			},
			object: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"packageName":   "pkg1",
						"revision":      int64(1),
						"workspaceName": "ws1",
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &MockRunner{runner: &tt.flags}
			match, err := mockRunner.packageRevisionMatches(tt.object)

			if tt.shouldFail {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, match)
			}
		})
	}
}

func TestNamespaceFlagWithoutValue(t *testing.T) {
	ctx := context.Background()
	ns := ""
	gcf := genericclioptions.ConfigFlags{Namespace: &ns}
	r := newRunner(ctx, &gcf)

	parentCmd := &cobra.Command{Use: "rpkg"}
	gcf.AddFlags(parentCmd.PersistentFlags())
	getCmd := NewCommand(ctx, &gcf)
	parentCmd.AddCommand(getCmd)

	nsFlag := getCmd.Flag("namespace")
	if nsFlag == nil {
		t.Fatal("namespace flag not found")
	}

	nsFlag.Changed = true
	errSet := nsFlag.Value.Set("")
	if errSet != nil {
		t.Fatalf("unexpected error setting namespace flag: %v", errSet)
	}

	err := r.preRunE(getCmd, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "namespace flag specified without a value")
}

func TestNamespaceShortFlagWithoutValue(t *testing.T) {
	ctx := context.Background()
	ns := ""
	gcf := genericclioptions.ConfigFlags{Namespace: &ns}
	r := newRunner(ctx, &gcf)

	parentCmd := &cobra.Command{Use: "rpkg"}
	gcf.AddFlags(parentCmd.PersistentFlags())
	getCmd := NewCommand(ctx, &gcf)
	parentCmd.AddCommand(getCmd)

	nsFlag := getCmd.InheritedFlags().Lookup("namespace")
	if nsFlag == nil {
		t.Fatal("namespace flag not found in inherited flags")
	}
	if nsFlag.Shorthand != "n" {
		t.Fatalf("namespace flag does not have shorthand 'n', got '%s'", nsFlag.Shorthand)
	}
	nsFlag.Changed = true
	errSet := nsFlag.Value.Set("")
	if errSet != nil {
		t.Fatalf("unexpected error setting -n flag: %v", errSet)
	}

	err := r.preRunE(getCmd, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "namespace flag specified without a value")
}

func TestShowKptfilePreRunE(t *testing.T) {
	ctx := context.Background()
	ns := "default"
	gcf := genericclioptions.ConfigFlags{Namespace: &ns}
	cmd := NewCommand(ctx, &gcf)

	// no args should fail
	noArgsRunner := newRunner(ctx, &gcf)
	noArgsRunner.showKptfile = true
	err := noArgsRunner.preRunE(cmd, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--show-kptfile requires exactly one package revision name")

	// multiple args should fail
	multiArgsRunner := newRunner(ctx, &gcf)
	multiArgsRunner.showKptfile = true
	err = multiArgsRunner.preRunE(cmd, []string{"pkg1", "pkg2"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--show-kptfile requires exactly one package revision name")

	// --show-kptfile with --name should fail
	withNameRunner := newRunner(ctx, &gcf)
	withNameRunner.showKptfile = true
	withNameRunner.packageName = "some-pkg"
	err = withNameRunner.preRunE(cmd, []string{"pkg1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--show-kptfile cannot be combined with --name, --revision, or --workspace")

	// --show-kptfile with --revision should fail
	withRevisionRunner := newRunner(ctx, &gcf)
	withRevisionRunner.showKptfile = true
	withRevisionRunner.revision = 1
	err = withRevisionRunner.preRunE(cmd, []string{"pkg1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--show-kptfile cannot be combined with --name, --revision, or --workspace")

	// --show-kptfile with --workspace should fail
	withWorkspaceRunner := newRunner(ctx, &gcf)
	withWorkspaceRunner.showKptfile = true
	withWorkspaceRunner.workspace = "ws1"
	err = withWorkspaceRunner.preRunE(cmd, []string{"pkg1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--show-kptfile cannot be combined with --name, --revision, or --workspace")

	// --show-kptfile with --all-namespaces should fail
	withAllNsRunner := newRunner(ctx, &gcf)
	withAllNsRunner.showKptfile = true
	withAllNsRunner.getFlags.AllNamespaces = true
	err = withAllNsRunner.preRunE(cmd, []string{"pkg1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--show-kptfile cannot be combined with --all-namespaces")

	// happy path: pre-injected client, namespace provided
	happyRunner := newRunner(ctx, &gcf)
	happyRunner.showKptfile = true
	happyRunner.client = fake.NewClientBuilder().Build()
	err = happyRunner.preRunE(cmd, []string{"pkg1"})
	assert.NoError(t, err)
}

func TestShowKptfileContent(t *testing.T) {
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
info:
  description: test package
`

	newFakeClient := func(resources map[string]string) client.Client {
		return fake.NewClientBuilder().WithScheme(scheme).WithObjects(&porchapi.PackageRevisionResources{
			ObjectMeta: metav1.ObjectMeta{Name: "test-repo.test-pkg.v1", Namespace: ns},
			Spec:       porchapi.PackageRevisionResourcesSpec{Resources: resources},
		}).Build()
	}

	t.Run("shows root Kptfile", func(t *testing.T) {
		r := &runner{
			ctx:      ctx,
			getFlags: cmdutil.Options{ConfigFlags: &genericclioptions.ConfigFlags{Namespace: &ns}},
			client: newFakeClient(map[string]string{
				"Kptfile":        kptfileContent,
				"nested/Kptfile": "should not appear",
				"cm.yaml":        "should not appear",
			}),
		}
		out := &bytes.Buffer{}
		cmd := &cobra.Command{}
		cmd.SetOut(out)
		err := r.showKptfileContent(cmd, "test-repo.test-pkg.v1")
		assert.NoError(t, err)
		assert.Equal(t, kptfileContent, out.String())
	})

	t.Run("no Kptfile in package", func(t *testing.T) {
		r := &runner{
			ctx:      ctx,
			getFlags: cmdutil.Options{ConfigFlags: &genericclioptions.ConfigFlags{Namespace: &ns}},
			client:   newFakeClient(map[string]string{"cm.yaml": "apiVersion: v1\nkind: ConfigMap\n"}),
		}
		cmd := &cobra.Command{}
		err := r.showKptfileContent(cmd, "test-repo.test-pkg.v1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain a root Kptfile")
	})

	t.Run("package not found", func(t *testing.T) {
		r := &runner{
			ctx:      ctx,
			getFlags: cmdutil.Options{ConfigFlags: &genericclioptions.ConfigFlags{Namespace: &ns}},
			client:   fake.NewClientBuilder().WithScheme(scheme).Build(),
		}
		cmd := &cobra.Command{}
		err := r.showKptfileContent(cmd, "nonexistent-pkg")
		assert.Error(t, err)
	})

	t.Run("nil client", func(t *testing.T) {
		r := &runner{
			ctx:      ctx,
			getFlags: cmdutil.Options{ConfigFlags: &genericclioptions.ConfigFlags{Namespace: &ns}},
		}
		cmd := &cobra.Command{}
		err := r.showKptfileContent(cmd, "some-pkg")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client is not initialized")
	})

	t.Run("write error", func(t *testing.T) {
		r := &runner{
			ctx:      ctx,
			getFlags: cmdutil.Options{ConfigFlags: &genericclioptions.ConfigFlags{Namespace: &ns}},
			client:   newFakeClient(map[string]string{"Kptfile": "content"}),
		}
		cmd := &cobra.Command{}
		cmd.SetOut(&errWriter{})
		err := r.showKptfileContent(cmd, "test-repo.test-pkg.v1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "broken pipe")
	})

	// NOTE: No other command in this repo unit-tests kubeconfig namespace resolution;
	// they always supply an explicit namespace via ConfigFlags. This subtest is added
	// specifically to satisfy code coverage requirements for the --show-kptfile feature.
	t.Run("namespace resolved from kubeconfig", func(t *testing.T) {
		resolvedNs := "resolved-ns"
		kubeconfigPath := testKubeconfig(t, resolvedNs)
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&porchapi.PackageRevisionResources{
			ObjectMeta: metav1.ObjectMeta{Name: "test-repo.test-pkg.v1", Namespace: resolvedNs},
			Spec:       porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{"Kptfile": "content"}},
		}).Build()
		r := &runner{
			ctx:      ctx,
			getFlags: cmdutil.Options{ConfigFlags: &genericclioptions.ConfigFlags{KubeConfig: &kubeconfigPath}},
			client:   c,
		}
		out := &bytes.Buffer{}
		cmd := &cobra.Command{}
		cmd.SetOut(out)
		err := r.showKptfileContent(cmd, "test-repo.test-pkg.v1")
		assert.NoError(t, err)
		assert.Equal(t, "content", out.String())
	})
}

type errWriter struct{}

func (e *errWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("broken pipe")
}

func TestShowKptfileRunE(t *testing.T) {
	ctx := context.Background()
	ns := "ns"

	scheme := runtime.NewScheme()
	if err := porchapi.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}

	t.Run("no args", func(t *testing.T) {
		r := &runner{
			ctx:         ctx,
			showKptfile: true,
			getFlags:    cmdutil.Options{ConfigFlags: &genericclioptions.ConfigFlags{Namespace: &ns}},
		}
		cmd := &cobra.Command{}
		err := r.runE(cmd, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "--show-kptfile requires exactly one package revision name")
	})

	t.Run("happy path", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&porchapi.PackageRevisionResources{
			ObjectMeta: metav1.ObjectMeta{Name: "test-repo.test-pkg.v1", Namespace: ns},
			Spec:       porchapi.PackageRevisionResourcesSpec{Resources: map[string]string{"Kptfile": "content"}},
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
		err := r.runE(cmd, []string{"test-repo.test-pkg.v1"})
		assert.NoError(t, err)
		assert.Equal(t, "content", out.String())
	})
}

// testKubeconfig writes a minimal kubeconfig to a temp directory and returns its path.
// NOTE: Other commands in this repo do not unit-test kubeconfig namespace resolution;
// they always supply an explicit namespace via ConfigFlags. These tests are added
// specifically to satisfy code coverage requirements for the --show-kptfile feature.
func testKubeconfig(t *testing.T, namespace string) string {
	t.Helper()
	content := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: test
contexts:
- context:
    cluster: test
    namespace: %s
  name: test
clusters:
- cluster:
    server: https://localhost:6443
  name: test
`, namespace)
	p := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write test kubeconfig: %v", err)
	}
	return p
}

// NOTE: No other command in this repo unit-tests kubeconfig namespace resolution;
// added here specifically to satisfy code coverage requirements for --show-kptfile.
func TestPreRunShowKptfileNamespaceResolution(t *testing.T) {
	ctx := context.Background()
	kubeconfigPath := testKubeconfig(t, "resolved-ns")
	gcf := genericclioptions.ConfigFlags{KubeConfig: &kubeconfigPath}
	r := newRunner(ctx, &gcf)
	r.showKptfile = true
	cmd := NewCommand(ctx, &gcf)

	err := r.preRunE(cmd, []string{"pkg1"})
	assert.NoError(t, err)
	assert.Equal(t, "resolved-ns", *r.getFlags.ConfigFlags.Namespace)
	assert.NotNil(t, r.client)
}
