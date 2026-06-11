// Copyright 2024 The kpt Authors
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

package clone

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func createScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	for _, api := range (runtime.SchemeBuilder{
		porchapi.AddToScheme,
	}) {
		if err := api(scheme); err != nil {
			return nil, err
		}
	}
	scheme.AddKnownTypes(porchapi.SchemeGroupVersion, &porchapi.PackageRevision{})
	return scheme, nil
}

func TestCmd(t *testing.T) {
	repoName := "test-repo"
	ns := "ns"
	var scheme, err = createScheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}
	testCases := map[string]struct {
		output     string
		wantErr    bool
		ns         string
		fakeclient client.WithWatch
	}{
		"metadata.name required": {
			wantErr:    true,
			fakeclient: fake.NewClientBuilder().WithScheme(scheme).Build(),
		},
		"clone package": {
			wantErr: false,
			ns:      ns,
			output:  "pr-clone created\n",
			fakeclient: fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if obj.GetObjectKind().GroupVersionKind().Kind == "PackageRevision" {
						obj.SetName("pr-clone")
					}
					return nil
				},
			}).WithScheme(scheme).Build(),
		},
	}

	for tn := range testCases {
		tc := testCases[tn]
		t.Run(tn, func(t *testing.T) {

			cmd := &cobra.Command{}
			o := os.Stdout
			e := os.Stderr
			read, write, _ := os.Pipe()
			os.Stdout = write
			os.Stderr = write

			r := &runner{
				ctx: context.Background(),
				cfg: &genericclioptions.ConfigFlags{
					Namespace: &tc.ns,
				},
				client:     tc.fakeclient,
				Command:    cmd,
				repository: repoName,
			}
			go func() {
				defer write.Close()
				err := r.runE(cmd, []string{})
				if err != nil && !tc.wantErr {
					t.Errorf("unexpected error: %v", err.Error())
				}
			}()
			out, _ := io.ReadAll(read)
			os.Stdout = o
			os.Stderr = e

			if diff := cmp.Diff(string(tc.output), string(out)); diff != "" {
				t.Errorf("Unexpected result (-want, +got): %s", diff)
			}
		})
	}
}

func TestRunSubpackageClone(t *testing.T) {
	ns := "ns"
	scheme, err := createScheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	t.Run("Error when parent PR is not draft", func(t *testing.T) {
		parentPR := &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "parent-pr",
				Namespace: ns,
			},
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecyclePublished,
				Tasks: []porchapi.Task{
					{Type: porchapi.TaskTypeInit, Init: &porchapi.PackageInitTaskSpec{}},
				},
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(parentPR).Build()
		cmd := &cobra.Command{}
		cmd.SetOut(io.Discard)

		r := &runner{
			ctx:           context.Background(),
			cfg:           &genericclioptions.ConfigFlags{Namespace: &ns},
			client:        c,
			Command:       cmd,
			target:        "parent-pr",
			subpackageDir: "my-subpkg",
		}

		err := r.runSubpackageClone(cmd)
		assert.ErrorContains(t, err, "parent package must be in state draft")
	})

	t.Run("Error when parent PR has more than 1 task", func(t *testing.T) {
		parentPR := &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "parent-pr",
				Namespace: ns,
			},
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleDraft,
				Tasks: []porchapi.Task{
					{Type: porchapi.TaskTypeInit, Init: &porchapi.PackageInitTaskSpec{}},
					{Type: porchapi.TaskTypeRender},
				},
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(parentPR).Build()
		cmd := &cobra.Command{}
		cmd.SetOut(io.Discard)

		r := &runner{
			ctx:           context.Background(),
			cfg:           &genericclioptions.ConfigFlags{Namespace: &ns},
			client:        c,
			Command:       cmd,
			target:        "parent-pr",
			subpackageDir: "my-subpkg",
		}

		err := r.runSubpackageClone(cmd)
		assert.ErrorContains(t, err, "must have exactly 1 existing task")
	})

	t.Run("Successful subpackage clone", func(t *testing.T) {
		parentPR := &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "parent-pr",
				Namespace: ns,
			},
			Spec: porchapi.PackageRevisionSpec{
				Lifecycle: porchapi.PackageRevisionLifecycleDraft,
				Tasks: []porchapi.Task{
					{Type: porchapi.TaskTypeInit, Init: &porchapi.PackageInitTaskSpec{}},
				},
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(parentPR).Build()
		output := &bytes.Buffer{}
		cmd := &cobra.Command{}
		cmd.SetOut(output)

		r := &runner{
			ctx:           context.Background(),
			cfg:           &genericclioptions.ConfigFlags{Namespace: &ns},
			client:        c,
			Command:       cmd,
			target:        "parent-pr",
			subpackageDir: "my-subpkg",
			clone: porchapi.PackageCloneTaskSpec{
				SubpackageDir: "my-subpkg",
				Upstream: porchapi.UpstreamPackage{
					UpstreamRef: &porchapi.PackageRevisionRef{Name: "upstream.pkg.v1"},
				},
			},
		}

		err := r.runSubpackageClone(cmd)
		assert.NoError(t, err)
		assert.Contains(t, output.String(), "subpackage cloned into directory")
		assert.Contains(t, output.String(), "my-subpkg")
	})
}

func TestPreRunE(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		flags     map[string]string
		expectErr bool
		errMsg    string
	}{
		{
			name:      "Missing arguments",
			args:      []string{"source-package"},
			flags:     map[string]string{"repository": "test-repo", "workspace": "test-workspace"},
			expectErr: true,
		},
		{
			name:      "Missing repository flag",
			args:      []string{"source-package", "target-package"},
			flags:     map[string]string{"repository": "", "workspace": ""},
			expectErr: true,
		},
		{
			name:      "Missing workspace flag returns error",
			args:      []string{"source-package", "target-package"},
			flags:     map[string]string{"repository": "test-repo", "workspace": ""},
			expectErr: true,
			errMsg:    "--workspace is required",
		},
		{
			name:      "Subpackage clone with invalid subpackage-dir",
			args:      []string{"source-package", "target-package"},
			flags:     map[string]string{"subpackage-dir": "../invalid"},
			expectErr: true,
			errMsg:    "invalid --subpackage-dir",
		},
		{
			name:      "Subpackage clone with repository flag returns error",
			args:      []string{"source-package", "target-package"},
			flags:     map[string]string{"subpackage-dir": "my-subpkg", "repository": "some-repo"},
			expectErr: true,
			errMsg:    "--repository may not be specified on subpackage clones",
		},
		{
			name:      "Subpackage clone with workspace flag returns error",
			args:      []string{"source-package", "target-package"},
			flags:     map[string]string{"subpackage-dir": "my-subpkg", "workspace": "ws"},
			expectErr: true,
			errMsg:    "--workspace may not be specified on subpackage clones",
		},
		{
			name:      "Valid subpackage clone with upstream ref",
			args:      []string{"upstream-repo.pkg.v1", "target-pr"},
			flags:     map[string]string{"subpackage-dir": "my-subpkg"},
			expectErr: false,
		},
	}

	for _, test := range tests {
		cmd := NewCommand(context.Background(), &genericclioptions.ConfigFlags{})
		t.Run(test.name, func(t *testing.T) {
			r := &runner{
				ctx:           context.Background(),
				cfg:           &genericclioptions.ConfigFlags{},
				Command:       cmd,
				repository:    test.flags["repository"],
				workspace:     test.flags["workspace"],
				subpackageDir: test.flags["subpackage-dir"],
			}

			// Mark workspace flag as changed if explicitly set in test
			if ws, ok := test.flags["workspace"]; ok && ws != "" {
				_ = cmd.Flags().Set("workspace", ws)
			}

			err := r.preRunE(cmd, test.args)
			if test.expectErr {
				assert.Error(t, err)
				if test.errMsg != "" {
					assert.Contains(t, err.Error(), test.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
