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

package push

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/kptdev/kpt/pkg/printer"
	fakeprint "github.com/kptdev/kpt/pkg/printer/fake"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	rpkgutil "github.com/kptdev/porch/pkg/cli/commands/rpkg/util"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestPreRunE_PopulatesClientAndPrinter covers the helper wiring inside
// preRunE so the cfg.ToRESTConfig / rpkgutil.CreateScheme / client.New /
// printer.FromContextOrDie path is exercised. The kubeconfig points at an
// unreachable host, which is fine since client.New is lazy.
func TestPreRunE_PopulatesClientAndPrinter(t *testing.T) {
	kubeconfig := rpkgutil.WriteTempKubeconfig(t)
	cfg := genericclioptions.NewConfigFlags(false)
	cfg.KubeConfig = &kubeconfig

	var buf bytes.Buffer
	ctx := fakeprint.CtxWithPrinter(&buf, &buf)

	r := &runner{Runner: rpkgutil.Runner{Ctx: ctx, Cfg: cfg}}
	if err := r.preRunE(&cobra.Command{}, nil); err != nil {
		t.Fatalf("preRunE returned error: %v", err)
	}
	if r.Client == nil {
		t.Error("preRunE must populate r.Client")
	}
	if r.printer == nil {
		t.Error("preRunE must populate r.printer")
	}
}

func TestCmd(t *testing.T) {
	pkgRevName := "test-fjdos9u2nfe2f32"
	ns := "ns"
	pkgDir := "testdata/test-fjdos9u2nfe2f32"

	scheme, err := rpkgutil.CreateScheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	testCases := map[string]struct {
		output  string
		args    []string
		wantErr bool
	}{
		"Push package": {
			output: pkgRevName + " pushed\n",
			args:   []string{pkgRevName, pkgDir},
		},
		"Args not provided": {
			wantErr: true,
		},
		"Pkgdir not provided": {
			args:    []string{pkgRevName},
			wantErr: true,
		},
		".KptRevisionMetadata missing": {
			args:    []string{pkgRevName, "dummy"},
			wantErr: true,
		},
	}

	for tn := range testCases {
		tc := testCases[tn]
		t.Run(tn, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(&porchapi.PackageRevisionResources{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pkgRevName,
						Namespace: ns,
					},
				}).
				Build()
			output := &bytes.Buffer{}
			o := os.Stdout
			e := os.Stderr
			read, write, _ := os.Pipe()
			os.Stdout = write
			os.Stderr = write
			ctx := fakeprint.CtxWithPrinter(output, output)
			r := &runner{
				Runner: rpkgutil.Runner{
					Ctx:    ctx,
					Cfg:    &genericclioptions.ConfigFlags{Namespace: &ns},
					Client: c,
				},
				printer: printer.FromContextOrDie(ctx),
			}
			cmd := &cobra.Command{}

			go func() {
				defer write.Close()
				err := r.runE(cmd, tc.args)
				if err != nil && !tc.wantErr {
					t.Errorf("unexpected error: %v", err)
				}
			}()
			//nolint:errcheck
			io.Copy(output, read)
			os.Stdout = o
			os.Stderr = e

			if diff := cmp.Diff(string(tc.output), output.String()); diff != "" {
				t.Errorf("Unexpected result (-want, +got): %s", diff)
			}
		})
	}
}

func TestPrintFnResult(t *testing.T) {
	var buf bytes.Buffer
	ctx := fakeprint.CtxWithPrinter(&buf, &buf)
	p := printer.FromContextOrDie(ctx)

	r := &runner{
		printer: p,
	}

	opt := printer.NewOpt()

	tests := []struct {
		name     string
		result   *porchapi.Result
		expected string // exact expected output
	}{
		{
			name: "Multiple info messages",
			result: &porchapi.Result{
				Results: []porchapi.ResultItem{
					{Message: "Validation passed", Severity: "info", Field: &porchapi.Field{Path: "spec"}},
					{Message: "Writing to file: configurations/initial-config/secure/kustomization.yaml, secretGenerator key", Severity: "info"},
				},
			},
			expected: "[Results]: [info] spec: Validation passed, [info]: Writing to file: configurations/initial-config/secure/kustomization.yaml, secretGenerator key",
		},
		{
			name: "Single warning message",
			result: &porchapi.Result{
				Results: []porchapi.ResultItem{
					{Message: "Deprecated field used", Severity: "warning"},
				},
			},
			expected: "[Results]: [warning]: Deprecated field used",
		},
		{
			name: "Single error message",
			result: &porchapi.Result{
				Results: []porchapi.ResultItem{
					{Message: "Failed to apply patch", Severity: "error"},
				},
			},
			expected: "[Results]: [error]: Failed to apply patch",
		},
		{
			name: "Empty message and severity",
			result: &porchapi.Result{
				Results: []porchapi.ResultItem{
					{Message: "", Severity: ""},
				},
			},
			expected: "[Results]: [info]:",
		},
		{
			name: "Mixed severities",
			result: &porchapi.Result{
				Results: []porchapi.ResultItem{
					{Message: "Info message", Severity: "info"},
					{Message: "Warning message", Severity: "warning"},
					{Message: "Error message", Severity: "error"},
				},
			},
			expected: "[Results]: [info]: Info message, [warning]: Warning message, [error]: Error message",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			r.printFnResult(tc.result, opt)
			got := strings.TrimSpace(buf.String())

			if got != tc.expected {
				t.Errorf("unexpected output for %q:\nGot: %q\nWant: %q", tc.name, got, tc.expected)
			}
		})
	}
}
