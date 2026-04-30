// Copyright 2025 The kpt and Nephio Authors
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

package reconciler

import (
	"context"
	"testing"
	"time"

	configapi "github.com/nephio-project/porch/api/porchconfig/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const defaultImagePrefix = "ghcr.io/kptdev/krm-functions-catalog/"
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
				},
			},
		},
	}

	preloadedFunctionConfigStore := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)
	preloadedFunctionConfigStore.UpsertFunctionConfig("set-image", sampleFunctionConfig)

	tests := []testcase{
		{
			name:     "FunctionConfig object is stored in FunctionStore after reconciliation",
			objs:     []client.Object{sampleFunctionConfig},
			requests: []string{"set-image"},
			check: func(t *testing.T, r *FunctionConfigReconciler) {
				// Check existence of the functionConfig in cluster
				got, exists := r.FunctionConfigStore.GetFunctionConfig("set-image")
				expectedNumberOfFunctions := 1
				expectedImage := "set-image"

				assert.True(t, exists, "FunctionConfig %s should exist in the store", expectedImage)
				assert.Equal(t, expectedImage, got.Spec.Image, "expected image %q, got %q", expectedImage, got.Spec.Image)
				assert.Equal(t, expectedNumberOfFunctions, len(r.FunctionConfigStore.List()), "expect %d function configs in the store, but got %d", expectedNumberOfFunctions, len(r.FunctionConfigStore.List()))
			},
		},
		{
			name:     "FunctionConfig object is deleted from FunctionStore after reconciliation",
			objs:     []client.Object{},
			requests: []string{"set-image"},
			check: func(t *testing.T, r *FunctionConfigReconciler) {
				// Check existence of the functionConfig in cluster
				_, exists := r.FunctionConfigStore.GetFunctionConfig("set-image")
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
				binary, exists := r.FunctionConfigStore.GetBinaryFromCache(expectedKey)
				assert.True(t, exists, "BinaryExecutorCache should have '%s'", expectedKey)
				assert.Equal(t, expectedPath, binary, "BinaryExecutorCache entry is %q, want %q", binary, expectedPath)
			},
		},
		{
			name:     "BuiltInExecutorCache is available for starlark",
			objs:     []client.Object{builtInSetNamespace, builtInApplyReplacements, builtInStarlarkWithId},
			requests: []string{"apply-replacements", "set-namespace", "starlark"},
			check: func(t *testing.T, r *FunctionConfigReconciler) {
				expectedStarlarkKey := "ghcr.io/kptdev/krm-functions-catalog/starlark-id:v0.4.3"
				execFunctions := r.FunctionConfigStore.GetExecCache()

				got, ok := execFunctions[expectedStarlarkKey]
				assert.True(t, ok, "BuiltInExecutorCache should have '%s'", expectedStarlarkKey)
				assert.NotNil(t, got, "BuiltInExecutorCache entry is not the expected processor function")

			},
		},
	}

	scheme := runtime.NewScheme()
	err := configapi.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("unable to add configapi to scheme: %v", err)
	}
	for _, tt := range tests {
		tt := tt // pin for closure
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithObjects(tt.objs...).WithScheme(scheme).Build()

			functionConfigStore := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)
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

			c := fake.NewClientBuilder().WithScheme(schemeWithFunctionConfig(t)).WithObjects(obj).Build()
			r := &FunctionConfigReconciler{
				Client:              c,
				FunctionConfigStore: NewFunctionConfigStore(defaultImagePrefix, functionCacheDir),
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

func TestGetProcessorFromCache(t *testing.T) {
	store := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)

	// Populate via UpdateExecCache (same path as reconciler)
	obj := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "set-namespace", Namespace: testNamespace},
		Spec: configapi.FunctionConfigSpec{
			Image:    "set-namespace",
			Prefixes: []string{""},
			GoExecutor: &configapi.GoExecutorConfig{
				Tags: []string{"v0.4.1"},
			},
		},
	}
	store.UpdateExecCache(obj.Name, obj)

	// Found with full prefix
	processor, found := store.GetProcessorFromCache("ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.1")
	assert.True(t, found)
	assert.NotNil(t, processor)

	// Found without prefix (short form)
	processor, found = store.GetProcessorFromCache("set-namespace:v0.4.1")
	assert.True(t, found)
	assert.NotNil(t, processor)

	// Not found for unknown tag
	_, found = store.GetProcessorFromCache("set-namespace:v9.9.9")
	assert.False(t, found)

	// Not found for unknown image
	_, found = store.GetProcessorFromCache("nonexistent:v1.0.0")
	assert.False(t, found)
}

func TestPrePopulationPattern(t *testing.T) {
	// Simulates what setupFunctionConfigReconciler does on cold start:
	// list all FunctionConfigs and populate the store synchronously
	// without going through the reconcile loop.
	store := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)

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
		store.UpsertFunctionConfig(obj.Name, obj)
		if obj.Spec.GoExecutor != nil {
			store.UpdateExecCache(obj.Name, obj)
		}
		if obj.Spec.BinaryExecutor != nil {
			store.UpdateBinaryCache(obj.Name, obj)
		}
	}

	// Verify exec cache is populated
	_, found := store.GetProcessorFromCache("ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.1")
	assert.True(t, found, "set-namespace should be in exec cache after pre-population")

	_, found = store.GetProcessorFromCache("ghcr.io/kptdev/krm-functions-catalog/apply-replacements:v0.1.1")
	assert.True(t, found, "apply-replacements should be in exec cache after pre-population")

	// Verify binary cache is populated
	path, found := store.GetBinaryFromCache("ghcr.io/kptdev/krm-functions-catalog/set-image:v0.1.4")
	assert.True(t, found, "set-image should be in binary cache after pre-population")
	assert.Equal(t, "/functions/set-image", path)

	// Verify function configs are stored
	assert.Len(t, store.List(), 3)
}

func TestConcurrentAccessSafety(t *testing.T) {
	// Verifies no data race when UpdateExecCache and GetProcessorFromCache
	// are called concurrently (the fix for the data race bug).
	store := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)

	obj := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "set-namespace", Namespace: testNamespace},
		Spec: configapi.FunctionConfigSpec{
			Image:      "set-namespace",
			Prefixes:   []string{""},
			GoExecutor: &configapi.GoExecutorConfig{Tags: []string{"v0.4.1"}},
		},
	}

	done := make(chan struct{})

	// Writer goroutine
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			store.UpdateExecCache(obj.Name, obj)
		}
	}()

	// Reader goroutine (concurrent with writer)
	for i := 0; i < 100; i++ {
		store.GetProcessorFromCache("ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.1")
	}

	<-done

	// After all writes complete, the entry should be present
	_, found := store.GetProcessorFromCache("ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.1")
	assert.True(t, found)
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

			c := fake.NewClientBuilder().WithScheme(schemeWithFunctionConfig(t)).WithObjects(obj).Build()
			store := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)
			store.UpsertFunctionConfig(objName, obj)

			r := &FunctionConfigReconciler{
				Client:              c,
				FunctionConfigStore: store,
				For:                 tc.forValue,
			}

			_, err := r.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Name: objName, Namespace: testNamespace},
			})
			require.NoError(t, err)

			got := &configapi.FunctionConfig{}
			err = c.Get(context.Background(), types.NamespacedName{Name: objName, Namespace: testNamespace}, got)
			require.NoError(t, err)
			assert.NotContains(t, got.Finalizers, tc.finalizer)
			assert.Contains(t, got.Finalizers, testFinalizer)

			_, exists := store.GetFunctionConfig(objName)
			assert.False(t, exists, "FunctionConfig should be removed from the store when deletion completes")
		})
	}
}
