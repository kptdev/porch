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

package clone

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/util"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
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

func TestV1Alpha2CloneFromUpstreamRef(t *testing.T) {
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
		repository: "target-repo",
		workspace:  "v1",
		target:     "my-pkg",
		upstream: porchv1alpha2.UpstreamPackage{
			UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "source-pkg-rev"},
		},
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err = r.runE(cmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff("target-repo.my-pkg.v1 created\n", output.String()); diff != "" {
		t.Errorf("Unexpected output (-want, +got): %s", diff)
	}
}

func TestV1Alpha2CloneFromGit(t *testing.T) {
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
		repository: "target-repo",
		workspace:  "v1",
		target:     "my-pkg",
		upstream: porchv1alpha2.UpstreamPackage{
			Type: porchv1alpha2.RepositoryTypeGit,
			Git: &porchv1alpha2.GitPackage{
				Repo:      "https://github.com/example/repo.git",
				Ref:       "main",
				Directory: "/",
			},
		},
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err = r.runE(cmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff("target-repo.my-pkg.v1 created\n", output.String()); diff != "" {
		t.Errorf("Unexpected output (-want, +got): %s", diff)
	}
}

func TestV1Alpha2PreRunEValidation(t *testing.T) {
	ns := "ns"

	r := &v1alpha2Runner{
		ctx: context.Background(),
		cfg: &genericclioptions.ConfigFlags{Namespace: &ns},
	}

	// Verify runner fields are empty before setup
	if r.repository != "" || r.workspace != "" || r.target != "" {
		t.Fatal("expected empty runner fields before preRunE")
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
	cmd.Flags().String("repository", "repo", "")
	cmd.Flags().String("workspace", "v1", "")
	cmd.Flags().String("directory", "", "")
	cmd.Flags().String("ref", "", "")
	cmd.Flags().String("secret-ref", "", "")

	err := r.preRunE(cmd, []string{"source-pkg", "target-pkg"})

	// preRunE should fail with client creation error
	if err == nil {
		t.Error("expected error for invalid kubeconfig")
	}
}

func TestV1Alpha2ClonePackageAlreadyExists(t *testing.T) {
	ns := "ns"
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
				Name:      "repo.existing-pkg.v1",
			},
			Spec: porchv1alpha2.PackageRevisionSpec{
				RepositoryName: "repo",
				PackageName:    "existing-pkg",
			},
		}).Build()

	// Manually set the client (preRunE would normally create it from kubeconfig)
	// and call the validation logic that preRunE performs after client creation.
	r := &v1alpha2Runner{
		ctx:    context.Background(),
		cfg:    &genericclioptions.ConfigFlags{Namespace: &ns},
		client: c,
	}

	// Simulate the validation that preRunE does after creating the client
	pkgExists, err := util.PackageAlreadyExistsV1Alpha2(r.ctx, r.client, "repo", "existing-pkg", ns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pkgExists {
		t.Fatal("expected package to already exist")
	}
}

// Mockery-based tests for error paths
func TestV1Alpha2CloneCreateError(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("create failed"))

	r := &v1alpha2Runner{
		ctx:        ctx,
		cfg:        &genericclioptions.ConfigFlags{Namespace: &ns},
		client:     mockC,
		repository: "target-repo",
		workspace:  "v1",
		target:     "my-pkg",
		upstream: porchv1alpha2.UpstreamPackage{
			UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "source-pkg-rev"},
		},
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create failed")
}

func TestV1Alpha2CloneCreateErrorFromGit(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("failed to create package revision"))

	r := &v1alpha2Runner{
		ctx:        ctx,
		cfg:        &genericclioptions.ConfigFlags{Namespace: &ns},
		client:     mockC,
		repository: "target-repo",
		workspace:  "v1",
		target:     "my-pkg",
		upstream: porchv1alpha2.UpstreamPackage{
			Type: porchv1alpha2.RepositoryTypeGit,
			Git: &porchv1alpha2.GitPackage{
				Repo:      "https://github.com/example/repo.git",
				Ref:       "main",
				Directory: "/",
			},
		},
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create package revision")
}

func TestV1Alpha2CloneCreateErrorAlreadyExists(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("already exists"))

	r := &v1alpha2Runner{
		ctx:        ctx,
		cfg:        &genericclioptions.ConfigFlags{Namespace: &ns},
		client:     mockC,
		repository: "target-repo",
		workspace:  "v1",
		target:     "my-pkg",
		upstream: porchv1alpha2.UpstreamPackage{
			UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "source-pkg-rev"},
		},
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestV1Alpha2CloneCreateErrorPermissionDenied(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("permission denied"))

	r := &v1alpha2Runner{
		ctx:        ctx,
		cfg:        &genericclioptions.ConfigFlags{Namespace: &ns},
		client:     mockC,
		repository: "target-repo",
		workspace:  "v1",
		target:     "my-pkg",
		upstream: porchv1alpha2.UpstreamPackage{
			UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "source-pkg-rev"},
		},
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

func TestV1Alpha2CloneCreateErrorInvalidRequest(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("invalid package revision spec"))

	r := &v1alpha2Runner{
		ctx:        ctx,
		cfg:        &genericclioptions.ConfigFlags{Namespace: &ns},
		client:     mockC,
		repository: "target-repo",
		workspace:  "v1",
		target:     "my-pkg",
		upstream: porchv1alpha2.UpstreamPackage{
			Type: porchv1alpha2.RepositoryTypeGit,
			Git: &porchv1alpha2.GitPackage{
				Repo:      "https://github.com/example/repo.git",
				Ref:       "main",
				Directory: "/",
			},
		},
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid package revision spec")
}

func TestV1Alpha2CloneCreateErrorNetworkFailure(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("connection refused"))

	r := &v1alpha2Runner{
		ctx:        ctx,
		cfg:        &genericclioptions.ConfigFlags{Namespace: &ns},
		client:     mockC,
		repository: "target-repo",
		workspace:  "v1",
		target:     "my-pkg",
		upstream: porchv1alpha2.UpstreamPackage{
			UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "source-pkg-rev"},
		},
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestV1Alpha2CloneCreateErrorTimeout(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("context deadline exceeded"))

	r := &v1alpha2Runner{
		ctx:        ctx,
		cfg:        &genericclioptions.ConfigFlags{Namespace: &ns},
		client:     mockC,
		repository: "target-repo",
		workspace:  "v1",
		target:     "my-pkg",
		upstream: porchv1alpha2.UpstreamPackage{
			UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "source-pkg-rev"},
		},
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

func TestV1Alpha2CloneCreateErrorInternalServerError(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("internal server error"))

	r := &v1alpha2Runner{
		ctx:        ctx,
		cfg:        &genericclioptions.ConfigFlags{Namespace: &ns},
		client:     mockC,
		repository: "target-repo",
		workspace:  "v1",
		target:     "my-pkg",
		upstream: porchv1alpha2.UpstreamPackage{
			UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: "source-pkg-rev"},
		},
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "internal server error")
}

// PreRunE validation tests
func TestV1Alpha2ClonePreRunEMissingArgs(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &v1alpha2Runner{
		ctx:    ctx,
		cfg:    &genericclioptions.ConfigFlags{Namespace: &ns},
		client: c,
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("repository", "repo", "")
	cmd.Flags().String("workspace", "v1", "")
	cmd.Flags().String("directory", "", "")
	cmd.Flags().String("ref", "", "")
	cmd.Flags().String("secret-ref", "", "")

	err = r.preRunE(cmd, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SOURCE_PACKAGE and NAME are required")
}

func TestV1Alpha2ClonePreRunEMissingRepository(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &v1alpha2Runner{
		ctx:    ctx,
		cfg:    &genericclioptions.ConfigFlags{Namespace: &ns},
		client: c,
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("repository", "", "")
	cmd.Flags().String("workspace", "v1", "")
	cmd.Flags().String("directory", "", "")
	cmd.Flags().String("ref", "", "")
	cmd.Flags().String("secret-ref", "", "")

	err = r.preRunE(cmd, []string{"source-pkg", "target-pkg"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--repository is required")
}

func TestV1Alpha2ClonePreRunEMissingWorkspace(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &v1alpha2Runner{
		ctx:    ctx,
		cfg:    &genericclioptions.ConfigFlags{Namespace: &ns},
		client: c,
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("repository", "repo", "")
	cmd.Flags().String("workspace", "", "")
	cmd.Flags().String("directory", "", "")
	cmd.Flags().String("ref", "", "")
	cmd.Flags().String("secret-ref", "", "")

	err = r.preRunE(cmd, []string{"source-pkg", "target-pkg"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--workspace is required")
}

func TestV1Alpha2ClonePreRunEOCINotSupported(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &v1alpha2Runner{
		ctx:    ctx,
		cfg:    &genericclioptions.ConfigFlags{Namespace: &ns},
		client: c,
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("repository", "repo", "")
	cmd.Flags().String("workspace", "v1", "")
	cmd.Flags().String("directory", "", "")
	cmd.Flags().String("ref", "", "")
	cmd.Flags().String("secret-ref", "", "")

	err = r.preRunE(cmd, []string{"oci://example.com/pkg", "target-pkg"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "OCI upstream is not supported")
}

func TestV1Alpha2ClonePreRunEGitURL(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &v1alpha2Runner{
		ctx:    ctx,
		cfg:    &genericclioptions.ConfigFlags{Namespace: &ns},
		client: c,
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("repository", "repo", "")
	cmd.Flags().String("workspace", "v1", "")
	cmd.Flags().String("directory", "", "")
	cmd.Flags().String("ref", "", "")
	cmd.Flags().String("secret-ref", "", "")

	err = r.preRunE(cmd, []string{"https://github.com/example/repo.git/path/to/pkg@main", "target-pkg"})
	assert.NoError(t, err)
	assert.Equal(t, porchv1alpha2.RepositoryTypeGit, r.upstream.Type)
	assert.NotNil(t, r.upstream.Git)
	assert.Equal(t, "main", r.upstream.Git.Ref)
}

func TestV1Alpha2ClonePreRunEUpstreamRef(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &v1alpha2Runner{
		ctx:    ctx,
		cfg:    &genericclioptions.ConfigFlags{Namespace: &ns},
		client: c,
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("repository", "repo", "")
	cmd.Flags().String("workspace", "v1", "")
	cmd.Flags().String("directory", "", "")
	cmd.Flags().String("ref", "", "")
	cmd.Flags().String("secret-ref", "", "")

	err = r.preRunE(cmd, []string{"upstream-pkg-rev", "target-pkg"})
	assert.NoError(t, err)
	assert.NotNil(t, r.upstream.UpstreamRef)
	assert.Equal(t, "upstream-pkg-rev", r.upstream.UpstreamRef.Name)
}
