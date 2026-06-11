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

package functionconfigs

import (
	"context"
	"testing"
	"time"

	"github.com/kptdev/krm-functions-catalog/functions/go/starlark/starlark"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const defaultImagePrefix = "ghcr.io/kptdev/krm-functions-catalog"
const functionCacheDir = "/functions"
const testNamespace = "porch-fn-system"

func TestFunctionConfigReconciler(t *testing.T) {
	starlarkExecutorID := "starlark-id"
	type testcase struct {
		name      string
		objs      []client.Object // input: objects to seed the fake client
		check     func(t *testing.T, reconciler *FunctionConfigReconciler)
		requests  []string // name set in the reconcile request
		expectErr bool
	}

	sampleFunctionConfig := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "set-image",
			Namespace: testNamespace,
		},
		Spec: configapi.FunctionConfigSpec{
			Image: "set-image",
			Prefixes: []string{
				"",
			},
			PodExecutor: &configapi.PodExecutorConfig{
				Tags: []string{
					"v0.1.1",
					"",
				},
				TimeToLive:              metav1.Duration{Duration: 30 * time.Second},
				MaxParallelExecutions:   2,
				PreferredMaxQueueLength: 2,
			},
			BinaryExecutor: &configapi.BinaryExecutorConfig{
				Tags: []string{
					"v0.1.4",
				},
				Path: "set-image",
			},
		},
	}

	builtInSetNamespace := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "set-namespace",
			Namespace: testNamespace,
		},
		Spec: configapi.FunctionConfigSpec{
			Image: "set-namespace",
			Prefixes: []string{
				"",
			},
			GoExecutor: &configapi.GoExecutorConfig{
				Tags: []string{
					"v0.4.1",
					"v0.4",
				},
			},
		},
	}

	builtInApplyReplacements := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "apply-replacements",
			Namespace: testNamespace,
		},
		Spec: configapi.FunctionConfigSpec{
			Image: "apply-replacements",
			Prefixes: []string{
				"",
			},
			GoExecutor: &configapi.GoExecutorConfig{
				Tags: []string{
					"v0.1.1",
					"v0.1",
				},
			},
		},
	}

	builtInStarlarkWithId := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "starlark",
			Namespace: testNamespace,
		},
		Spec: configapi.FunctionConfigSpec{
			Image: "starlark",
			Prefixes: []string{
				"",
			},
			GoExecutor: &configapi.GoExecutorConfig{
				ID: &starlarkExecutorID,
				Tags: []string{
					"v0.4.3",
					"v0.4",
					"",
				},
			},
		},
	}

	tests := []testcase{
		{
			name:     "FunctionConfig object is stored in FunctionStore after reconciliation",
			objs:     []client.Object{sampleFunctionConfig},
			requests: []string{"set-image"},
			check: func(t *testing.T, r *FunctionConfigReconciler) {
				// Check existence of the functionConfig in cluster
				got, exists := r.FunctionConfigStore.Get("set-image")
				expectedImage := "set-image"

				assert.Truef(t, exists, "FunctionConfig %s should exist in the store", expectedImage)
				assert.Equalf(t, expectedImage, got.Image, "expected image %q, got %q", expectedImage, got.Image)
				assert.Equalf(t, 1, r.FunctionConfigStore.Len(), "expect %d function configs in the store, but got %d", 1, r.FunctionConfigStore.Len())
			},
		},
		{
			name:     "FunctionConfig object is deleted from FunctionStore after reconciliation",
			objs:     []client.Object{},
			requests: []string{"set-image"},
			check: func(t *testing.T, r *FunctionConfigReconciler) {
				// Check existence of the functionConfig in cluster
				_, exists := r.FunctionConfigStore.Get("set-image")
				assert.False(t, exists, "FunctionConfig 'set-image' should not exist in the store")
			},
		},
		{
			name:     "BinaryExecutorCache is available with image",
			objs:     []client.Object{sampleFunctionConfig},
			requests: []string{"set-image"},
			check: func(t *testing.T, r *FunctionConfigReconciler) {
				expectedKey := "ghcr.io/kptdev/krm-functions-catalog/set-image:v0.1.4"
				expectedPath := "/functions/set-image"
				config, exists := r.FunctionConfigStore.Get(expectedKey)
				require.True(t, exists, "BinaryExecutorCache should have '%s'", expectedKey)
				assert.Equal(t, expectedPath, config.BinaryExecutor.Path,
					"BinaryExecutorCache entry is %q, want %q", config.BinaryExecutor.Path, expectedPath)
			},
		},
		{
			name:     "BuiltInExecutorCache is available for starlark",
			objs:     []client.Object{builtInSetNamespace, builtInApplyReplacements, builtInStarlarkWithId},
			requests: []string{"apply-replacements", "set-namespace", "starlark"},
			check: func(t *testing.T, r *FunctionConfigReconciler) {
				proc, ok := r.FunctionConfigStore.GetProcessor("starlark")

				assert.Truef(t, ok, "BuiltInExecutorCache should have %q", starlarkExecutorID)
				assert.NotNil(t, proc, "BuiltInExecutorCache entry is not the expected processor function")
			},
		},
	}

	scheme := runtime.NewScheme()
	err := configapi.AddToScheme(scheme)
	require.NoError(t, err)
	if err != nil {
		t.Fatalf("unable to add configapi to scheme: %v", err)
	}
	for _, tt := range tests {
		tt := tt // pin for closure
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithObjects(tt.objs...).WithScheme(scheme).WithStatusSubresource(&configapi.FunctionConfig{}).Build()

			functionConfigStore := NewStore(defaultImagePrefix, functionCacheDir)
			functionConfigStore.processorMapping[starlarkExecutorID] = starlark.Process
			reconciler := &FunctionConfigReconciler{
				Client:              c,
				FunctionConfigStore: functionConfigStore,
			}
			for _, reqName := range tt.requests {
				_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name: reqName, Namespace: testNamespace,
					},
				})
				assert.Nil(t, err, "Reconcile() should not return an error for request %s", reqName)
			}
			if tt.check != nil {
				tt.check(t, reconciler)
			}
		})
	}
}

func schemeWithFunctionConfig(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := configapi.AddToScheme(scheme); err != nil {
		t.Fatalf("unable to add configapi to scheme: %v", err)
	}
	return scheme
}

func TestFinalizersAdded(t *testing.T) {
	cases := map[string]struct {
		forValue  ReconcilerFor
		finalizer string
	}{
		string(ReconcilerForFunctionRunner): {
			forValue:  ReconcilerForFunctionRunner,
			finalizer: FunctionRunnerFinalizer,
		},
		string(ReconcilerForServer): {
			forValue:  ReconcilerForServer,
			finalizer: ServerFinalizer,
		},
		string(ReconcilerForController): {
			forValue:  ReconcilerForController,
			finalizer: ControllerFinalizer,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			objName := "fn-add-" + string(tc.forValue)
			obj := &configapi.FunctionConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:       objName,
					Namespace:  testNamespace,
					Generation: 1,
				},
				Spec: configapi.FunctionConfigSpec{
					Image:    objName,
					Prefixes: []string{""},
				},
			}

			c := fake.NewClientBuilder().WithScheme(schemeWithFunctionConfig(t)).WithObjects(obj).WithStatusSubresource(&configapi.FunctionConfig{}).Build()
			r := &FunctionConfigReconciler{
				Client:              c,
				FunctionConfigStore: NewStore(defaultImagePrefix, functionCacheDir),
				For:                 tc.forValue,
			}

			_, err := r.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: objName, Namespace: testNamespace},
			})
			require.NoError(t, err)

			got := &configapi.FunctionConfig{}
			err = c.Get(context.Background(), types.NamespacedName{Name: objName, Namespace: testNamespace}, got)
			require.NoError(t, err)
			assert.Contains(t, got.Finalizers, tc.finalizer)
		})
	}
}

func TestFinalizersRemoved(t *testing.T) {
	now := metav1.Now()
	const testFinalizer = "config.porch.kpt.dev/test-hold"

	cases := map[string]struct {
		forValue  ReconcilerFor
		finalizer string
	}{
		string(ReconcilerForFunctionRunner): {
			forValue:  ReconcilerForFunctionRunner,
			finalizer: FunctionRunnerFinalizer,
		},
		string(ReconcilerForServer): {
			forValue:  ReconcilerForServer,
			finalizer: ServerFinalizer,
		},
		string(ReconcilerForController): {
			forValue:  ReconcilerForController,
			finalizer: ControllerFinalizer,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			objName := "fn-del-" + string(tc.forValue)
			obj := &configapi.FunctionConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:              objName,
					Namespace:         testNamespace,
					DeletionTimestamp: &now,
					// Keep a second finalizer so the fake client retains the object, and we can assert metadata.
					Finalizers: []string{tc.finalizer, testFinalizer},
				},
				Spec: configapi.FunctionConfigSpec{
					Image:    objName,
					Prefixes: []string{""},
				},
			}

			c := fake.NewClientBuilder().WithScheme(schemeWithFunctionConfig(t)).WithObjects(obj).WithStatusSubresource(&configapi.FunctionConfig{}).Build()
			store := NewStore(defaultImagePrefix, functionCacheDir)
			err := store.Store(obj)
			require.NoError(t, err)

			r := &FunctionConfigReconciler{
				Client:              c,
				FunctionConfigStore: store,
				For:                 tc.forValue,
			}

			_, err = r.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: objName, Namespace: testNamespace},
			})
			require.NoError(t, err)

			got := &configapi.FunctionConfig{}
			err = c.Get(context.Background(), types.NamespacedName{Name: objName, Namespace: testNamespace}, got)
			require.NoError(t, err)
			assert.NotContains(t, got.Finalizers, tc.finalizer)
			assert.Contains(t, got.Finalizers, testFinalizer)

			_, exists := store.Get(objName)
			assert.False(t, exists, "FunctionConfig should be removed from the store when deletion completes")
		})
	}
}

func TestPrePopulationPattern(t *testing.T) {
	// Simulates what setupFunctionConfigReconciler does on cold start:
	// list all FunctionConfigs and populate the store synchronously
	// without going through the reconcile loop.
	store := NewStore(defaultImagePrefix, functionCacheDir)

	configs := []configapi.FunctionConfig{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "set-namespace", Namespace: testNamespace},
			Spec: configapi.FunctionConfigSpec{
				Image:      "set-namespace",
				Prefixes:   []string{""},
				GoExecutor: &configapi.GoExecutorConfig{Tags: []string{"v0.4.1"}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "apply-replacements", Namespace: testNamespace},
			Spec: configapi.FunctionConfigSpec{
				Image:      "apply-replacements",
				Prefixes:   []string{""},
				GoExecutor: &configapi.GoExecutorConfig{Tags: []string{"v0.1.1"}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "set-image", Namespace: testNamespace},
			Spec: configapi.FunctionConfigSpec{
				Image:    "set-image",
				Prefixes: []string{""},
				BinaryExecutor: &configapi.BinaryExecutorConfig{
					Tags: []string{"v0.1.4"},
					Path: "set-image",
				},
			},
		},
	}

	// Pre-populate (mirrors the code in setupFunctionConfigReconciler)
	for i := range configs {
		obj := &configs[i]
		err := store.Store(obj)
		require.NoError(t, err)
	}

	// Verify exec cache is populated
	_, found := store.GetProcessor("ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.1")
	assert.True(t, found, "set-namespace should be in exec cache after pre-population")

	_, found = store.GetProcessor("ghcr.io/kptdev/krm-functions-catalog/apply-replacements:v0.1.1")
	assert.True(t, found, "apply-replacements should be in exec cache after pre-population")

	// Verify binary cache is populated
	config, found := store.Get("ghcr.io/kptdev/krm-functions-catalog/set-image:v0.1.4")
	require.True(t, found, "set-image should be in binary cache after pre-population")
	assert.Equal(t, "/functions/set-image", config.BinaryExecutor.Path)

	// Verify function configs are stored
	assert.Equal(t, 3, store.Len(), "unexpected amount of function configs in cache")
}
