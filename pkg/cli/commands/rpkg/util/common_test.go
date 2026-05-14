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

package util

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// -- InitClient tests --

func TestInitClient_EmptyNamespaceReturnsError(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("namespace", "", "")
	_ = cmd.Flags().Set("namespace", "")

	_, err := InitClient(cmd, nil)
	assert.ErrorContains(t, err, "namespace flag specified without a value")
}

func TestInitClient_NamespaceNotSetPassesValidation(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("namespace", "default", "") // flag defined but never .Set, so .Changed is false

	kubeconfig := createTempKubeconfig(t)
	cfg := genericclioptions.NewConfigFlags(false)
	cfg.KubeConfig = &kubeconfig

	_, err := InitClient(cmd, cfg)
	assert.NoError(t, err, "an unchanged namespace flag must pass the namespace-empty check")
}

func TestInitClient_ValidConfigReturnsClient(t *testing.T) {
	cmd := &cobra.Command{}
	kubeconfig := createTempKubeconfig(t)
	cfg := genericclioptions.NewConfigFlags(false)
	cfg.KubeConfig = &kubeconfig

	c, err := InitClient(cmd, cfg)
	assert.NoError(t, err)
	assert.NotNil(t, c)
}

func TestInitClient_ShorthandFlagSharesEntryWithLongName(t *testing.T) {
	// Declaring with StringP creates a single flag entry indexed by the long
	// name "namespace"; the shorthand -n does not get its own entry. This
	// test guards against accidentally re-introducing a separate cmd.Flag("n")
	// lookup, which would always return nil.
	cmd := &cobra.Command{}
	cmd.Flags().StringP("namespace", "n", "", "")
	_ = cmd.Flags().Set("namespace", "")

	_, err := InitClient(cmd, nil)
	assert.ErrorContains(t, err, "namespace flag specified without a value")
	assert.Nil(t, cmd.Flag("n"),
		"pflag does not register the shorthand as a separate flag entry")
}

func TestInitClient_NilCfgReturnsError(t *testing.T) {
	cmd := &cobra.Command{}
	// No namespace flag is changed, so namespace validation passes and the
	// nil-cfg branch is reached.
	_, err := InitClient(cmd, nil)
	assert.ErrorContains(t, err, "cfg must not be nil")
}

// -- MakePreRunE tests --

func TestMakePreRunE_SetsClient(t *testing.T) {
	kubeconfig := createTempKubeconfig(t)
	cfg := genericclioptions.NewConfigFlags(false)
	cfg.KubeConfig = &kubeconfig

	var c client.Client
	preRunE := MakePreRunE("test.preRunE", cfg, &c)

	err := preRunE(&cobra.Command{}, nil)
	assert.NoError(t, err)
	assert.NotNil(t, c)
}

func TestMakePreRunE_EmptyNamespaceReturnsError(t *testing.T) {
	var c client.Client
	preRunE := MakePreRunE("test.preRunE", &genericclioptions.ConfigFlags{}, &c)

	cmd := &cobra.Command{}
	cmd.Flags().String("namespace", "", "")
	_ = cmd.Flags().Set("namespace", "")

	err := preRunE(cmd, nil)
	assert.ErrorContains(t, err, "namespace flag specified without a value")
}

// -- CreateScheme tests --

func TestCreateScheme_RegistersPorchTypes(t *testing.T) {
	scheme, err := CreateScheme()
	require.NoError(t, err)
	assert.NotNil(t, scheme)

	gvk := porchapi.SchemeGroupVersion.WithKind("PackageRevision")
	assert.True(t, scheme.Recognizes(gvk), "scheme should recognize PackageRevision")
}

func TestCreateScheme_RegistersConfigAndCoreTypes(t *testing.T) {
	scheme, err := CreateScheme()
	require.NoError(t, err)

	configGVK := configapi.GroupVersion.WithKind("Repository")
	assert.True(t, scheme.Recognizes(configGVK), "scheme should recognize porchconfig Repository")

	coreGVK := corev1.SchemeGroupVersion.WithKind("ConfigMap")
	assert.True(t, scheme.Recognizes(coreGVK), "scheme should recognize core/v1 ConfigMap")
}

func TestBuildScheme_ReturnsErrorWhenAdderFails(t *testing.T) {
	wantErr := fmt.Errorf("boom")
	scheme, err := buildScheme([]func(*runtime.Scheme) error{
		func(*runtime.Scheme) error { return wantErr },
	})
	assert.Nil(t, scheme)
	assert.ErrorIs(t, err, wantErr)
}

func TestBuildScheme_StopsAtFirstError(t *testing.T) {
	called := 0
	first := func(*runtime.Scheme) error { called++; return fmt.Errorf("first") }
	second := func(*runtime.Scheme) error { called++; return nil }
	_, err := buildScheme([]func(*runtime.Scheme) error{first, second})
	assert.ErrorContains(t, err, "first")
	assert.Equal(t, 1, called, "should not call subsequent adders after error")
}

// -- NewTestRunner tests --

func TestNewTestRunner_PopulatesAllFields(t *testing.T) {
	cmd := &cobra.Command{}
	r := NewTestRunner("kube-system", nil, cmd)

	require.NotNil(t, r.Ctx, "Ctx should be set to a non-nil context")
	require.NotNil(t, r.Cfg, "Cfg should be initialised")
	require.NotNil(t, r.Cfg.Namespace, "Cfg.Namespace should be a pointer to a heap-escaped string")
	assert.Equal(t, "kube-system", *r.Cfg.Namespace)
	assert.Same(t, cmd, r.Command, "Command should reference the caller's *cobra.Command")
}

// -- RunForEachPackage tests --

func TestRunForEachPackage_AllSucceed(t *testing.T) {
	cmd, stdout, stderr := setupCmdBuffers()

	fc := setupFakeClient(t,
		newPR("pkg-a", "ns", porchapi.PackageRevisionLifecycleDraft),
		newPR("pkg-b", "ns", porchapi.PackageRevisionLifecycleDraft),
	)
	opts := RunForEachOpts{CmdName: "cmdrpkgtest", WithRetry: true}
	err := RunForEachPackage(context.Background(), fc, cmd, "ns",
		[]string{"pkg-a", "pkg-b"}, opts,
		func(_ context.Context, _ client.Client, pr *porchapi.PackageRevision) (string, error) {
			return fmt.Sprintf("%s done", pr.Name), nil
		})
	assert.NoError(t, err)
	assert.Contains(t, stdout.String(), "pkg-a done")
	assert.Contains(t, stdout.String(), "pkg-b done")
	assert.Empty(t, stderr.String())
}

func TestRunForEachPackage_PartialFailure(t *testing.T) {
	cmd, stdout, stderr := setupCmdBuffers()

	fc := setupFakeClient(t,
		newPR("pkg-a", "ns", porchapi.PackageRevisionLifecycleDraft),
		newPR("pkg-b", "ns", porchapi.PackageRevisionLifecycleDraft),
	)
	opts := RunForEachOpts{CmdName: "cmdrpkgtest", WithRetry: true}
	err := RunForEachPackage(context.Background(), fc, cmd, "ns",
		[]string{"pkg-a", "pkg-b"}, opts,
		func(_ context.Context, _ client.Client, pr *porchapi.PackageRevision) (string, error) {
			if pr.Name == "pkg-b" {
				return "", fmt.Errorf("something went wrong")
			}
			return fmt.Sprintf("%s done", pr.Name), nil
		})
	assert.Error(t, err)
	assert.Contains(t, stdout.String(), "pkg-a done")
	assert.Contains(t, stderr.String(), "pkg-b failed")
}

func TestRunForEachPackage_NotFoundReportsError(t *testing.T) {
	cmd, _, stderr := setupCmdBuffers()

	fc := setupFakeClient(t)
	opts := RunForEachOpts{CmdName: "cmdrpkgtest", WithRetry: true}
	err := RunForEachPackage(context.Background(), fc, cmd, "ns",
		[]string{"missing-pkg"}, opts,
		func(_ context.Context, _ client.Client, pr *porchapi.PackageRevision) (string, error) {
			return fmt.Sprintf("%s done", pr.Name), nil
		})
	assert.Error(t, err)
	assert.Contains(t, stderr.String(), "missing-pkg failed")
}

func TestRunForEachPackage_WithoutRetry(t *testing.T) {
	cmd, stdout, _ := setupCmdBuffers()

	fc := setupFakeClient(t,
		newPR("pkg-a", "ns", porchapi.PackageRevisionLifecycleDraft),
	)
	opts := RunForEachOpts{CmdName: "cmdrpkgtest"}
	err := RunForEachPackage(context.Background(), fc, cmd, "ns",
		[]string{"pkg-a"}, opts,
		func(_ context.Context, _ client.Client, pr *porchapi.PackageRevision) (string, error) {
			return fmt.Sprintf("%s deleted", pr.Name), nil
		})
	assert.NoError(t, err)
	assert.Contains(t, stdout.String(), "pkg-a deleted")
}

func TestRunForEachPackage_EmptySuccessMessageSkipsPrint(t *testing.T) {
	cmd, stdout, stderr := setupCmdBuffers()

	fc := setupFakeClient(t,
		newPR("pkg-a", "ns", porchapi.PackageRevisionLifecycleDraft),
	)
	opts := RunForEachOpts{CmdName: "cmdrpkgtest", WithRetry: true}
	err := RunForEachPackage(context.Background(), fc, cmd, "ns",
		[]string{"pkg-a"}, opts,
		func(_ context.Context, _ client.Client, _ *porchapi.PackageRevision) (string, error) {
			return "", nil
		})
	assert.NoError(t, err)
	assert.Empty(t, stdout.String(), "empty success message should not print anything")
	assert.Empty(t, stderr.String())
}

func TestRunForEachPackage_WithoutRetryGetFails(t *testing.T) {
	cmd, _, stderr := setupCmdBuffers()

	fc := setupFakeClient(t)
	opts := RunForEachOpts{CmdName: "cmdrpkgtest"}
	err := RunForEachPackage(context.Background(), fc, cmd, "ns",
		[]string{"missing"}, opts,
		func(_ context.Context, _ client.Client, pr *porchapi.PackageRevision) (string, error) {
			return fmt.Sprintf("%s done", pr.Name), nil
		})
	assert.Error(t, err)
	assert.Contains(t, stderr.String(), "missing failed")
}

func TestRunForEachPackage_ReadinessCheckFails(t *testing.T) {
	cmd, _, stderr := setupCmdBuffers()

	fc := setupFakeClient(t,
		newPR("pkg-a", "ns", porchapi.PackageRevisionLifecycleDraft),
	)
	// Set a readiness gate that is not met
	pr := &porchapi.PackageRevision{}
	require.NoError(t, fc.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "pkg-a"}, pr))
	pr.Spec.ReadinessGates = []porchapi.ReadinessGate{{ConditionType: "test.Ready"}}
	require.NoError(t, fc.Update(context.Background(), pr))

	opts := RunForEachOpts{CmdName: "cmdrpkgtest", WithRetry: true, CheckReadiness: true}
	err := RunForEachPackage(context.Background(), fc, cmd, "ns",
		[]string{"pkg-a"}, opts,
		func(_ context.Context, _ client.Client, pr *porchapi.PackageRevision) (string, error) {
			return fmt.Sprintf("%s done", pr.Name), nil
		})
	assert.Error(t, err)
	assert.Contains(t, stderr.String(), "readiness conditions not met")
}

func TestRunForEachPackage_RequiresAtLeastOneArg(t *testing.T) {
	cmd, _, _ := setupCmdBuffers()
	fc := setupFakeClient(t)

	err := RunForEachPackage(context.Background(), fc, cmd, "ns",
		nil, // no args
		RunForEachOpts{CmdName: "cmdrpkgtest"},
		func(_ context.Context, _ client.Client, _ *porchapi.PackageRevision) (string, error) {
			return "", nil
		})
	assert.ErrorContains(t, err, "PACKAGE",
		"empty args must fail with a clear error rather than silently returning success")
}

func TestRunForEachPackage_RequiresCmdName(t *testing.T) {
	cmd, _, _ := setupCmdBuffers()
	fc := setupFakeClient(t)

	err := RunForEachPackage(context.Background(), fc, cmd, "ns",
		[]string{"pkg-a"},
		RunForEachOpts{}, // CmdName intentionally empty
		func(_ context.Context, _ client.Client, _ *porchapi.PackageRevision) (string, error) {
			return "", nil
		})
	assert.ErrorContains(t, err, "RunForEachOpts.CmdName must not be empty",
		"empty CmdName must fail fast so error.Op tags are well-formed")
}

func TestRetryOnConflict_ReturnsErrorWhenFnFails(t *testing.T) {
	called := 0
	err := retryOnConflict(func() error {
		called++
		return fmt.Errorf("connection refused")
	})
	assert.ErrorContains(t, err, "connection refused")
	assert.Equal(t, 1, called, "non-conflict error should not retry")
}

func TestRetryOnConflict_ReturnsNilOnSuccess(t *testing.T) {
	err := retryOnConflict(func() error {
		return nil
	})
	assert.NoError(t, err)
}

// -- test helpers --

func setupCmdBuffers() (cmd *cobra.Command, stdout *bytes.Buffer, stderr *bytes.Buffer) {
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	cmd = &cobra.Command{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return
}

func newPR(name, ns string, lifecycle porchapi.PackageRevisionLifecycle) *porchapi.PackageRevision {
	return &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       porchapi.PackageRevisionSpec{Lifecycle: lifecycle},
	}
}

func setupFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme, err := CreateScheme()
	require.NoError(t, err)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func createTempKubeconfig(t *testing.T) string {
	t.Helper()
	content := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: fake-token
`
	f, err := os.CreateTemp(t.TempDir(), "kubeconfig-*")
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}
