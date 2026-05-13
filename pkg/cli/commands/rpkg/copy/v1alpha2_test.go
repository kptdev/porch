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

package copy

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func createV1Alpha2Scheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	for _, api := range (runtime.SchemeBuilder{
		porchv1alpha2.AddToScheme,
	}) {
		if err := api(scheme); err != nil {
			return nil, err
		}
	}
	return scheme, nil
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

func TestV1Alpha2PreRunE(t *testing.T) {
	ns := "test-ns"
	ctx := context.Background()
	cfg := &genericclioptions.ConfigFlags{Namespace: &ns}

	r := &v1alpha2Runner{
		ctx: ctx,
		cfg: cfg,
	}

	cmd := &cobra.Command{}
	err := r.preRunE(cmd, []string{"source-pkg"})

	// preRunE should succeed (it just creates a client)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if r.client == nil {
		t.Error("expected client to be set")
	}
	if r.sourceName != "source-pkg" {
		t.Errorf("expected sourceName to be set to 'source-pkg', got %q", r.sourceName)
	}
}

func TestV1Alpha2PreRunEMissingArgs(t *testing.T) {
	ns := "test-ns"
	ctx := context.Background()
	cfg := &genericclioptions.ConfigFlags{Namespace: &ns}

	r := &v1alpha2Runner{
		ctx: ctx,
		cfg: cfg,
	}

	cmd := &cobra.Command{}
	err := r.preRunE(cmd, []string{})

	// preRunE should fail with missing args
	if err == nil {
		t.Error("expected error for missing SOURCE_PACKAGE argument")
	}
}

func TestV1Alpha2CopyCmd(t *testing.T) {
	ns := "ns"
	sourceName := "repo--my-pkg-v1"
	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&porchv1alpha2.PackageRevision{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PackageRevision",
				APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns,
				Name:      sourceName,
			},
			Spec: porchv1alpha2.PackageRevisionSpec{
				RepositoryName: "repo",
				PackageName:    "my-pkg",
				WorkspaceName:  "v1",
				Lifecycle:      porchv1alpha2.PackageRevisionLifecyclePublished,
			},
		}).Build()

	r := &v1alpha2Runner{
		ctx:        context.Background(),
		cfg:        &genericclioptions.ConfigFlags{Namespace: &ns},
		client:     c,
		sourceName: sourceName,
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.Flags().String("workspace", "v2", "")
	cmd.SetOut(output)

	err = r.runE(cmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff("repo.my-pkg.v2 created\n", output.String()); diff != "" {
		t.Errorf("Unexpected output (-want, +got): %s", diff)
	}
}

func TestV1Alpha2CopyMissingWorkspace(t *testing.T) {
	ns := "ns"
	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &v1alpha2Runner{
		ctx:        context.Background(),
		cfg:        &genericclioptions.ConfigFlags{Namespace: &ns},
		client:     c,
		sourceName: "some-source",
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("workspace", "", "")

	err = r.runE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing --workspace")
	}
}

func TestV1Alpha2CopySourceNotFound(t *testing.T) {
	ns := "ns"
	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &v1alpha2Runner{
		ctx:        context.Background(),
		cfg:        &genericclioptions.ConfigFlags{Namespace: &ns},
		client:     c,
		sourceName: "nonexistent-source",
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("workspace", "v2", "")

	err = r.runE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent source")
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
	cmd.Flags().String("workspace", "v2", "")

	err := r.runE(cmd, nil)
	if err == nil {
		t.Error("expected error for package not found")
	}
}

func TestV1Alpha2PreRunEValidation(t *testing.T) {
	ns := "ns"

	r := &v1alpha2Runner{
		ctx: context.Background(),
		cfg: &genericclioptions.ConfigFlags{Namespace: &ns},
	}

	// Verify runner fields are empty before setup
	if r.sourceName != "" {
		t.Fatal("expected empty sourceName before preRunE")
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
	err := r.preRunE(cmd, []string{"source-pkg"})

	// preRunE should fail with client creation error
	if err == nil {
		t.Error("expected error for invalid kubeconfig")
	}
}
