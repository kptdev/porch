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
	"context"
	"io"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
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

func TestPreRunE(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		flags     map[string]string
		expectErr bool
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
	}

	for _, test := range tests {
		cmd := NewCommand(context.Background(), &genericclioptions.ConfigFlags{})
		t.Run(test.name, func(t *testing.T) {
			r := &runner{
				ctx:        context.Background(),
				cfg:        &genericclioptions.ConfigFlags{},
				repository: test.flags["repository"],
				workspace:  test.flags["workspace"],
			}

			err := r.preRunE(cmd, test.args)
			if test.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
