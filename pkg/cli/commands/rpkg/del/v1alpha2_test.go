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

package del

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/util"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestV1Alpha2NewRunner(t *testing.T) {
	ns := "test-ns"
	ctx := context.Background()
	cfg := &genericclioptions.ConfigFlags{Namespace: &ns}

	r := newV1Alpha2Runner(ctx, cfg)

	if r == nil {
		t.Fatal("expected non-nil runner")
	}
	if r.ctx != ctx {
		t.Errorf("expected context to be set")
	}
	if r.cfg != cfg {
		t.Errorf("expected config to be set")
	}
}

func TestV1Alpha2PreRunE(t *testing.T) {
	ns := "test-ns"
	ctx := context.Background()
	cfg := &genericclioptions.ConfigFlags{Namespace: &ns}

	r := &v1alpha2Runner{
		ctx: ctx,
		cfg: cfg,
	}

	cmd := &cobra.Command{}
	err := r.preRunE(cmd, []string{})

	// preRunE should succeed (it just creates a client)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if r.client == nil {
		t.Error("expected client to be set")
	}
}

func TestV1Alpha2PreRunEClientError(t *testing.T) {
	ns := "test-ns"
	ctx := context.Background()
	// Use invalid kubeconfig to trigger client creation error
	cfg := &genericclioptions.ConfigFlags{
		Namespace:  &ns,
		KubeConfig: func() *string { s := "/nonexistent/kubeconfig"; return &s }(),
	}

	r := &v1alpha2Runner{
		ctx: ctx,
		cfg: cfg,
	}

	cmd := &cobra.Command{}
	err := r.preRunE(cmd, []string{})

	// preRunE should fail with client creation error
	if err == nil {
		t.Error("expected error for invalid kubeconfig")
	}
}

func TestV1Alpha2Cmd(t *testing.T) {
	pkgRevName := "test-fjdos9u2nfe2f32"
	scheme := util.V1Alpha2Scheme(t)
	testCases := map[string]struct {
		output  string
		wantErr bool
		ns      string
	}{
		"Package not found in ns": {
			output:  pkgRevName + " failed (packagerevisions.porch.kpt.dev \"" + pkgRevName + "\" not found)\n",
			ns:      "doesnotexist",
			wantErr: true,
		},
		"delete package": {
			output: pkgRevName + " deleted\n",
			ns:     "ns",
		},
	}

	for tn := range testCases {
		tc := testCases[tn]
		t.Run(tn, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(util.NewV1Alpha2PackageRevision("ns", pkgRevName)).
				Build()

			cmd := &cobra.Command{}
			o := os.Stdout
			e := os.Stderr
			read, write, _ := os.Pipe()
			os.Stdout = write
			os.Stderr = write

			r := &v1alpha2Runner{
				ctx: context.Background(),
				cfg: &genericclioptions.ConfigFlags{
					Namespace: &tc.ns,
				},
				client: c,
			}
			go func() {
				defer write.Close()
				err := r.runE(cmd, []string{pkgRevName})
				if err != nil && !tc.wantErr {
					t.Errorf("unexpected error: %v", err)
				}
			}()
			out, _ := io.ReadAll(read)
			os.Stdout = o
			os.Stderr = e

			if diff := cmp.Diff(tc.output, string(out)); diff != "" {
				t.Errorf("Unexpected result (-want, +got): %s", diff)
			}
		})
	}
}

func TestV1Alpha2DeleteActuallyRemoves(t *testing.T) {
	pkgRevName := "test-pkg"
	ns := "ns"
	scheme := util.V1Alpha2Scheme(t)

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(util.NewV1Alpha2PackageRevision(ns, pkgRevName)).
		Build()

	r := &v1alpha2Runner{
		ctx:    context.Background(),
		cfg:    &genericclioptions.ConfigFlags{Namespace: &ns},
		client: c,
	}

	err := r.runE(&cobra.Command{}, []string{pkgRevName})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the object is gone
	var pr porchv1alpha2.PackageRevision
	err = c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: pkgRevName}, &pr)
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
}
