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

package approve

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/util"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/mock"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

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
		lc      porchv1alpha2.PackageRevisionLifecycle
		output  string
		wantErr bool
		ns      string
	}{
		"Package not found in ns": {
			output:  pkgRevName + " failed (packagerevisions.porch.kpt.dev \"" + pkgRevName + "\" not found)\n",
			ns:      "doesnotexist",
			wantErr: true,
		},
		"Approve proposed package": {
			output: pkgRevName + " approved\n",
			lc:     porchv1alpha2.PackageRevisionLifecycleProposed,
			ns:     "ns",
		},
		"Cannot approve draft package": {
			output:  pkgRevName + " failed (cannot change approval from Draft to Published)\n",
			lc:      porchv1alpha2.PackageRevisionLifecycleDraft,
			ns:      "ns",
			wantErr: true,
		},
	}

	for tn := range testCases {
		tc := testCases[tn]
		t.Run(tn, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(func() *porchv1alpha2.PackageRevision {
					pr := util.NewV1Alpha2PackageRevision("ns", pkgRevName)
					pr.Spec.Lifecycle = tc.lc
					return pr
				}()).Build()

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

func TestV1Alpha2RunEGetError(t *testing.T) {
	ns := "test-ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Get(ctx, mock.AnythingOfType("types.NamespacedName"), mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("package not found"))

	r := &v1alpha2Runner{
		ctx:    ctx,
		cfg:    &genericclioptions.ConfigFlags{Namespace: &ns},
		client: mockC,
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)
	cmd.SetErr(output)

	err := r.runE(cmd, []string{"test-pkg"})
	if err == nil {
		t.Error("expected error for package not found")
	}
}

func TestV1Alpha2ReadinessGates(t *testing.T) {
	pkgRevName := "test-pkg"
	ns := "ns"
	scheme := util.V1Alpha2Scheme(t)

	testCases := map[string]struct {
		gates      []porchv1alpha2.ReadinessGate
		conditions []porchv1alpha2.PackageCondition
		wantErr    bool
		wantLC     porchv1alpha2.PackageRevisionLifecycle
	}{
		"no gates - approve succeeds": {
			wantLC: porchv1alpha2.PackageRevisionLifecyclePublished,
		},
		"gate met - approve succeeds": {
			gates: []porchv1alpha2.ReadinessGate{{ConditionType: "foo.bar/Ready"}},
			conditions: []porchv1alpha2.PackageCondition{
				{Type: "foo.bar/Ready", Status: porchv1alpha2.PackageConditionTrue},
			},
			wantLC: porchv1alpha2.PackageRevisionLifecyclePublished,
		},
		"gate not met - approve fails": {
			gates: []porchv1alpha2.ReadinessGate{{ConditionType: "foo.bar/Ready"}},
			conditions: []porchv1alpha2.PackageCondition{
				{Type: "foo.bar/Ready", Status: porchv1alpha2.PackageConditionFalse},
			},
			wantErr: true,
			wantLC:  porchv1alpha2.PackageRevisionLifecycleProposed, // unchanged
		},
		"gate missing condition - approve fails": {
			gates:   []porchv1alpha2.ReadinessGate{{ConditionType: "foo.bar/Ready"}},
			wantErr: true,
			wantLC:  porchv1alpha2.PackageRevisionLifecycleProposed,
		},
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(func() *porchv1alpha2.PackageRevision {
					pr := util.NewV1Alpha2PackageRevision(ns, pkgRevName)
					pr.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleProposed
					pr.Spec.ReadinessGates = tc.gates
					pr.Status.PackageConditions = tc.conditions
					return pr
				}()).Build()

			r := &v1alpha2Runner{
				ctx:    context.Background(),
				cfg:    &genericclioptions.ConfigFlags{Namespace: &ns},
				client: c,
			}

			err := r.runE(&cobra.Command{}, []string{pkgRevName})
			if tc.wantErr && err == nil {
				t.Fatal("expected error but got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var pr porchv1alpha2.PackageRevision
			if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: pkgRevName}, &pr); err != nil {
				t.Fatalf("failed to get PR: %v", err)
			}
			if pr.Spec.Lifecycle != tc.wantLC {
				t.Errorf("expected lifecycle %s, got %s", tc.wantLC, pr.Spec.Lifecycle)
			}
		})
	}
}
