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

package main

import (
	"context"
	"flag"
	"testing"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/functionconfigs"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// --- reconcilerIsEnabled ---

func TestReconcilerIsEnabled_Wildcard(t *testing.T) {
	assert.True(t, reconcilerIsEnabled([]string{"*"}, "anything"))
}

func TestReconcilerIsEnabled_ExactMatch(t *testing.T) {
	assert.True(t, reconcilerIsEnabled([]string{"repositories", "packagerevisions"}, "repositories"))
	assert.False(t, reconcilerIsEnabled([]string{"repositories"}, "packagerevisions"))
}

func TestReconcilerIsEnabled_Empty(t *testing.T) {
	assert.False(t, reconcilerIsEnabled([]string{""}, "repositories"))
	assert.False(t, reconcilerIsEnabled([]string{}, "repositories"))
}

func TestReconcilerIsEnabled_EnvVar(t *testing.T) {
	tests := []struct {
		name       string
		envVal     string
		reconciler string
		want       bool
	}{
		{"true", "true", "repositories", true},
		{"TRUE", "TRUE", "repositories", true},
		{"1", "1", "repositories", true},
		{"yes", "yes", "repositories", true},
		{"YES", "YES", "repositories", true},
		{"false", "false", "repositories", false},
		{"0", "0", "repositories", false},
		{"no", "no", "repositories", false},
		{"empty", "", "repositories", false},
		{"random", "random", "repositories", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envKey := "ENABLE_REPOSITORIES"
			t.Setenv(envKey, tt.envVal)
			got := reconcilerIsEnabled([]string{}, tt.reconciler)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReconcilerIsEnabled_FlagTakesPrecedenceOverEnv(t *testing.T) {
	t.Setenv("ENABLE_REPOSITORIES", "false")
	assert.True(t, reconcilerIsEnabled([]string{"repositories"}, "repositories"))
}

// --- buildReconcilerMap ---

type fakeReconciler struct {
	name string
}

func (f *fakeReconciler) Name() string { return f.name }
func (f *fakeReconciler) Reconcile(context.Context, reconcile.Request) (reconcile.Result, error) {
	return reconcile.Result{}, nil
}
func (f *fakeReconciler) InitDefaults()                       {}
func (f *fakeReconciler) BindFlags(string, *flag.FlagSet)     {}
func (f *fakeReconciler) SetupWithManager(ctrl.Manager) error { return nil }

func TestBuildReconcilerMap(t *testing.T) {
	a := &fakeReconciler{name: "alpha"}
	b := &fakeReconciler{name: "beta"}

	m := buildReconcilerMap(a, b)

	require.Len(t, m, 2)
	assert.Equal(t, a, m["alpha"])
	assert.Equal(t, b, m["beta"])
}

func TestBuildReconcilerMap_Empty(t *testing.T) {
	m := buildReconcilerMap()
	assert.Empty(t, m)
}

// --- initScheme ---

func TestInitScheme(t *testing.T) {
	scheme, err := initScheme()
	require.NoError(t, err)
	require.NotNil(t, scheme)

	expectedGVKs := []schema.GroupVersionKind{
		{Group: "porch.kpt.dev", Version: "v1alpha1", Kind: "PackageRevision"},
		{Group: "porch.kpt.dev", Version: "v1alpha2", Kind: "PackageRevision"},
		{Group: "config.porch.kpt.dev", Version: "v1alpha1", Kind: "Repository"},
	}
	for _, gvk := range expectedGVKs {
		assert.True(t, scheme.Recognizes(gvk), "scheme should recognize %s", gvk)
	}
}

// --- reconcilers map ---

func TestReconcilersMapContainsAllReconcilers(t *testing.T) {
	expected := []string{"repositories", "packagerevisions", "packagevariants", "packagevariantsets"}
	for _, name := range expected {
		_, ok := reconcilers[name]
		assert.True(t, ok, "reconcilers map should contain %q", name)
	}
	assert.Len(t, reconcilers, len(expected))
}

// --- prePopulateFunctionConfigStore ---

func TestPrePopulateFunctionConfigStore_Success(t *testing.T) {
	items := []configapi.FunctionConfig{
		{
			Spec: configapi.FunctionConfigSpec{
				Image: "set-namespace",
				GoExecutor: &configapi.GoExecutorConfig{
					Tags: []string{"v0.4.1"},
				},
			},
		},
		{
			Spec: configapi.FunctionConfigSpec{
				Image: "starlark",
				BinaryExecutor: &configapi.BinaryExecutorConfig{
					Tags: []string{"v1.0.0"},
					Path: "/usr/local/bin/starlark",
				},
			},
		},
	}
	items[0].Name = "set-namespace"
	items[1].Name = "starlark"

	mockReader := mockclient.NewMockReader(t)
	mockReader.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha1.FunctionConfigList"), mock.Anything).
		Run(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) {
			list.(*configapi.FunctionConfigList).Items = items
		}).Return(nil)

	store := functionconfigs.NewStore("ghcr.io/kptdev", "/tmp/bins")
	prePopulateFunctionConfigStore(mockReader, store)

	_, ok := store.Get("set-namespace:v0.4.1")
	assert.True(t, ok, "set-namespace should be in store")
	_, ok = store.Get("starlark:v1.0.0")
	assert.True(t, ok, "starlark should be in store")
}

func TestPrePopulateFunctionConfigStore_ListError(t *testing.T) {
	mockReader := mockclient.NewMockReader(t)
	mockReader.EXPECT().List(mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)

	store := functionconfigs.NewStore("ghcr.io/kptdev", "/tmp/bins")
	prePopulateFunctionConfigStore(mockReader, store)

	_, ok := store.Get("anything")
	assert.False(t, ok, "store should be empty after list error")
}

func TestPrePopulateFunctionConfigStore_EmptyList(t *testing.T) {
	mockReader := mockclient.NewMockReader(t)
	mockReader.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha1.FunctionConfigList"), mock.Anything).
		Return(nil)

	store := functionconfigs.NewStore("ghcr.io/kptdev", "/tmp/bins")
	prePopulateFunctionConfigStore(mockReader, store)

	assert.Equal(t, 0, store.Len())
}
