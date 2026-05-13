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

package init

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
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func TestV1Alpha2InitCmd(t *testing.T) {
	ns := "ns"
	scheme, err := createV1Alpha2Scheme()
	if err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &v1alpha2Runner{
		ctx:         context.Background(),
		cfg:         &genericclioptions.ConfigFlags{Namespace: &ns},
		client:      c,
		name:        "new-pkg",
		repository:  "repo",
		workspace:   "v1",
		Description: "test package",
		Keywords:    []string{"test"},
		Site:        "https://example.com",
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err = r.runE(cmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff := cmp.Diff("repo.new-pkg.v1 created\n", output.String()); diff != "" {
		t.Errorf("Unexpected output (-want, +got): %s", diff)
	}

	// Verify the object was created with correct fields
	var pr porchv1alpha2.PackageRevision
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "repo.new-pkg.v1"}, &pr); err != nil {
		t.Fatalf("failed to get created PR: %v", err)
	}
	if pr.Spec.PackageName != "new-pkg" {
		t.Errorf("expected packageName 'new-pkg', got %q", pr.Spec.PackageName)
	}
	if pr.Spec.Lifecycle != porchv1alpha2.PackageRevisionLifecycleDraft {
		t.Errorf("expected lifecycle Draft, got %s", pr.Spec.Lifecycle)
	}
	if pr.Spec.Source == nil || pr.Spec.Source.Init == nil {
		t.Fatal("expected source.init to be set")
	}
	if pr.Spec.Source.Init.Description != "test package" {
		t.Errorf("expected description 'test package', got %q", pr.Spec.Source.Init.Description)
	}
}

func TestV1Alpha2InitPackageAlreadyExists(t *testing.T) {
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

	// Directly test the validation logic (preRunE tries to create a real k8s client)
	pkgExists, err := util.PackageAlreadyExistsV1Alpha2(context.Background(), c, "repo", "existing-pkg", ns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pkgExists {
		t.Fatal("expected package to already exist")
	}
}

func TestV1Alpha2InitCreateError(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("create failed"))

	r := &v1alpha2Runner{
		ctx:         ctx,
		cfg:         &genericclioptions.ConfigFlags{Namespace: &ns},
		client:      mockC,
		name:        "new-pkg",
		repository:  "repo",
		workspace:   "v1",
		Description: "test package",
		Keywords:    []string{"test"},
		Site:        "https://example.com",
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create failed")
}

func TestV1Alpha2InitCreateErrorAlreadyExists(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("already exists"))

	r := &v1alpha2Runner{
		ctx:         ctx,
		cfg:         &genericclioptions.ConfigFlags{Namespace: &ns},
		client:      mockC,
		name:        "existing-pkg",
		repository:  "repo",
		workspace:   "v1",
		Description: "test package",
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestV1Alpha2InitCreateErrorPermissionDenied(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("permission denied"))

	r := &v1alpha2Runner{
		ctx:         ctx,
		cfg:         &genericclioptions.ConfigFlags{Namespace: &ns},
		client:      mockC,
		name:        "new-pkg",
		repository:  "repo",
		workspace:   "v1",
		Description: "test package",
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

func TestV1Alpha2InitCreateErrorInvalidRequest(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("invalid package revision spec"))

	r := &v1alpha2Runner{
		ctx:         ctx,
		cfg:         &genericclioptions.ConfigFlags{Namespace: &ns},
		client:      mockC,
		name:        "new-pkg",
		repository:  "repo",
		workspace:   "v1",
		Description: "test package",
		Keywords:    []string{"test", "init"},
		Site:        "https://example.com",
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid package revision spec")
}

func TestV1Alpha2InitCreateErrorNetworkFailure(t *testing.T) {
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
		name:       "new-pkg",
		repository: "repo",
		workspace:  "v1",
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestV1Alpha2InitCreateErrorTimeout(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("context deadline exceeded"))

	r := &v1alpha2Runner{
		ctx:         ctx,
		cfg:         &genericclioptions.ConfigFlags{Namespace: &ns},
		client:      mockC,
		name:        "new-pkg",
		repository:  "repo",
		workspace:   "v1",
		Description: "test package",
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

func TestV1Alpha2InitCreateErrorInternalServerError(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("internal server error"))

	r := &v1alpha2Runner{
		ctx:         ctx,
		cfg:         &genericclioptions.ConfigFlags{Namespace: &ns},
		client:      mockC,
		name:        "new-pkg",
		repository:  "repo",
		workspace:   "v1",
		Description: "test package",
		Keywords:    []string{"test"},
		Site:        "https://example.com",
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "internal server error")
}

func TestV1Alpha2InitCreateErrorQuotaExceeded(t *testing.T) {
	ns := "ns"
	ctx := context.Background()

	mockC := mockclient.NewMockClient(t)
	mockC.EXPECT().
		Create(ctx, mock.AnythingOfType("*v1alpha2.PackageRevision")).
		Return(fmt.Errorf("quota exceeded"))

	r := &v1alpha2Runner{
		ctx:        ctx,
		cfg:        &genericclioptions.ConfigFlags{Namespace: &ns},
		client:     mockC,
		name:       "new-pkg",
		repository: "repo",
		workspace:  "v1",
	}

	output := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(output)

	err := r.runE(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "quota exceeded")
}

// PreRunE validation tests
func TestV1Alpha2InitPreRunEMissingArgs(t *testing.T) {
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
	cmd.Flags().String("description", "", "")
	cmd.Flags().String("site", "", "")
	cmd.Flags().StringSlice("keywords", nil, "")

	err = r.preRunE(cmd, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "PACKAGE_NAME is a required positional argument")
}

func TestV1Alpha2InitPreRunEMissingRepository(t *testing.T) {
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
	cmd.Flags().String("description", "", "")
	cmd.Flags().String("site", "", "")
	cmd.Flags().StringSlice("keywords", nil, "")

	err = r.preRunE(cmd, []string{"my-pkg"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--repository is required")
}

func TestV1Alpha2InitPreRunEMissingWorkspace(t *testing.T) {
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
	cmd.Flags().String("description", "", "")
	cmd.Flags().String("site", "", "")
	cmd.Flags().StringSlice("keywords", nil, "")

	err = r.preRunE(cmd, []string{"my-pkg"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--workspace is required")
}

func TestV1Alpha2InitPreRunEValidFlags(t *testing.T) {
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
	cmd.Flags().String("description", "test package", "")
	cmd.Flags().String("site", "https://example.com", "")
	cmd.Flags().StringSlice("keywords", []string{"test", "pkg"}, "")

	err = r.preRunE(cmd, []string{"my-pkg"})
	assert.NoError(t, err)
	assert.Equal(t, "my-pkg", r.name)
	assert.Equal(t, "repo", r.repository)
	assert.Equal(t, "v1", r.workspace)
	assert.Equal(t, "test package", r.Description)
	assert.Equal(t, "https://example.com", r.Site)
	assert.Equal(t, []string{"test", "pkg"}, r.Keywords)
}
