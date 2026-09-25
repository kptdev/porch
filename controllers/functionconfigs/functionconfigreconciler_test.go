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

const defaultImagePrefix = "ghcr.io/kptdev/krm-functions-catalog/"
const functionCacheDir = "/functions"
const testNamespace = "porch-fn-system"

func TestFunctionConfigReconciler(t *testing.T) {
	starlarkExecutorID := "starlark-id"
	type testcase struct {
		name      string
		objs      []client.Object // input: objects to seed the fake client
		check     func(t *testing.T, reconciler *Reconciler)
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
			check: func(t *testing.T, r *Reconciler) {
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
			check: func(t *testing.T, r *Reconciler) {
				// Check existence of the functionConfig in cluster
				_, exists := r.FunctionConfigStore.GetFunctionConfig("set-image")
				assert.False(t, exists, "FunctionConfig 'set-image' should not exist in the store")
			},
		},
		{
			name:     "BinaryExecutorCache is available with image",
			objs:     []client.Object{sampleFunctionConfig},
			requests: []string{"set-image"},
			check: func(t *testing.T, r *Reconciler) {
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
			check: func(t *testing.T, r *Reconciler) {
				expectedStarlarkKey := "starlark-id"
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
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithObjects(tt.objs...).WithScheme(scheme).WithStatusSubresource(&configapi.FunctionConfig{}).Build()

			functionConfigStore := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)
			reconciler := &Reconciler{
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
			r := &Reconciler{
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

func TestGetBinaryFromCacheByConstraint(t *testing.T) {
	store := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)

	obj := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "set-image", Namespace: testNamespace},
		Spec: configapi.FunctionConfigSpec{
			Image:    "set-image",
			Prefixes: []string{""},
			BinaryExecutor: &configapi.BinaryExecutorConfig{
				Tags: []string{"v0.1.2", "v0.1.3"},
				Path: "set-image",
			},
		},
	}
	store.UpdateBinaryCache(&obj.Spec)

	const expectedPath = "/functions/set-image"
	const qualifiedImage = "ghcr.io/kptdev/krm-functions-catalog/set-image"

	tests := map[string]struct {
		image      string
		constraint string
		wantPath   string
		wantFound  bool
	}{
		"selects highest matching version": {
			image:      qualifiedImage,
			constraint: ">= 0.1.2 < 0.2.0",
			wantPath:   expectedPath,
			wantFound:  true,
		},
		"prefix mismatch": {
			image:      "evil.registry/set-image",
			constraint: ">= 0.1.2 < 0.2.0",
			wantFound:  false,
		},
		"unknown image basename": {
			image:      "ghcr.io/kptdev/krm-functions-catalog/nonexistent",
			constraint: ">= 0.1.0",
			wantFound:  false,
		},
		"invalid semver constraint": {
			image:      qualifiedImage,
			constraint: ">> 1.0.0",
			wantFound:  false,
		},
		"no matching version for valid constraint": {
			image:      qualifiedImage,
			constraint: "> 1.0.0",
			wantFound:  false,
		},
		"short form without registry prefix": {
			image:      "set-image",
			constraint: ">= 0.1.2",
			wantFound:  false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			path, found := store.GetBinaryFromCacheByConstraint(tc.image, tc.constraint)
			assert.Equal(t, tc.wantFound, found)
			if tc.wantFound {
				assert.Equal(t, tc.wantPath, path)
			} else {
				assert.Empty(t, path)
			}
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
			store.UpdateBinaryCache(&obj.Spec)
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
		for range 100 {
			store.UpdateExecCache(obj.Name, obj)
		}
	}()

	// Reader goroutine (concurrent with writer)
	for range 100 {
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

			c := fake.NewClientBuilder().WithScheme(schemeWithFunctionConfig(t)).WithObjects(obj).WithStatusSubresource(&configapi.FunctionConfig{}).Build()
			store := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)
			store.UpsertFunctionConfig(objName, obj)

			r := &Reconciler{
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

func TestDeduplicateStringSlice(t *testing.T) {
	cases := map[string]struct {
		input    []string
		expected []string
	}{
		"nil slice": {
			input:    nil,
			expected: nil,
		},
		"empty slice": {
			input:    []string{},
			expected: []string{},
		},
		"single element": {
			input:    []string{"v0.4.1"},
			expected: []string{"v0.4.1"},
		},
		"no duplicates": {
			input:    []string{"v0.4.1", "v0.4.2", "v0.5.0"},
			expected: []string{"v0.4.1", "v0.4.2", "v0.5.0"},
		},
		"exact duplicate removed": {
			input:    []string{"v0.4.1", "v0.4.1"},
			expected: []string{"v0.4.1"},
		},
		"multiple duplicates: first occurrence kept": {
			input:    []string{"v0.4.1", "v0.5.0", "v0.4.1", "v0.5.0"},
			expected: []string{"v0.4.1", "v0.5.0"},
		},
		"deduplicates to unique set": {
			input:    []string{"b", "a", "c", "a", "b"},
			expected: []string{"b", "a", "c"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := deduplicateStringSlice(tc.input)
			assert.ElementsMatch(t, tc.expected, got)
		})
	}
}

func TestReconcileDeduplicatesTags(t *testing.T) {
	obj := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "set-namespace",
			Namespace: testNamespace,
		},
		Spec: configapi.FunctionConfigSpec{
			Image:    "set-namespace",
			Prefixes: []string{"ghcr.io/kptdev", "ghcr.io/kptdev"},
			GoExecutor: &configapi.GoExecutorConfig{
				Tags: []string{"v0.4.1", "v0.4.1", ">= v0.5.0"},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(schemeWithFunctionConfig(t)).
		WithObjects(obj).
		WithStatusSubresource(&configapi.FunctionConfig{}).
		Build()

	r := &Reconciler{
		Client:              c,
		FunctionConfigStore: NewFunctionConfigStore(defaultImagePrefix, functionCacheDir),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: obj.Name, Namespace: testNamespace},
	})
	require.NoError(t, err)

	updated := &configapi.FunctionConfig{}
	err = c.Get(context.Background(), types.NamespacedName{Name: obj.Name, Namespace: testNamespace}, updated)
	require.NoError(t, err)

	assert.Equal(t, []string{"ghcr.io/kptdev"}, updated.Spec.Prefixes, "duplicate prefix should be removed")
	assert.ElementsMatch(t, []string{"v0.4.1", ">= v0.5.0"}, updated.Spec.GoExecutor.Tags, "duplicate tag should be removed")
}

func TestReconcileNoDuplicatesSpecUnchanged(t *testing.T) {
	originalPrefixes := []string{"ghcr.io/kptdev"}
	originalTags := []string{"v0.1.1", "v0.1.2"}

	obj := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "apply-replacements",
			Namespace: testNamespace,
		},
		Spec: configapi.FunctionConfigSpec{
			Image:    "apply-replacements",
			Prefixes: originalPrefixes,
			GoExecutor: &configapi.GoExecutorConfig{
				Tags: originalTags,
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(schemeWithFunctionConfig(t)).
		WithObjects(obj).
		WithStatusSubresource(&configapi.FunctionConfig{}).
		Build()

	r := &Reconciler{
		Client:              c,
		FunctionConfigStore: NewFunctionConfigStore(defaultImagePrefix, functionCacheDir),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: obj.Name, Namespace: testNamespace},
	})
	require.NoError(t, err)

	updated := &configapi.FunctionConfig{}
	err = c.Get(context.Background(), types.NamespacedName{Name: obj.Name, Namespace: testNamespace}, updated)
	require.NoError(t, err)
	assert.Equal(t, originalPrefixes, updated.Spec.Prefixes, "prefixes should be unchanged when no duplicates")
	assert.Equal(t, originalTags, updated.Spec.GoExecutor.Tags, "tags should be unchanged when no duplicates")
}

func TestValidateSemverConstraints(t *testing.T) {
	commonCases := map[string]struct {
		tags      []string
		expectErr bool
	}{
		"empty list is valid": {
			tags:      []string{},
			expectErr: false,
		},
		"wildcard star is valid": {
			tags:      []string{"*"},
			expectErr: false,
		},
		"exact version is valid": {
			tags:      []string{"v0.4.1"},
			expectErr: false,
		},
		"range constraint is valid": {
			tags:      []string{">= v0.4.0 < v0.5.0"},
			expectErr: false,
		},
		"mixed valid constraints": {
			tags:      []string{"v0.4.1", ">= v0.5.0 < v1.0.0", "*"},
			expectErr: false,
		},
		"invalid constraint returns error": {
			tags:      []string{"not-a-constraint"},
			expectErr: true,
		},
		"operator typo returns error": {
			tags:      []string{">> 1.0.0"},
			expectErr: true,
		},
	}

	t.Run("allowed wildcard star only", func(t *testing.T) {
		for name, tc := range commonCases {
			t.Run(name, func(t *testing.T) {
				err := validateSemverConstraints(tc.tags, "*")
				if tc.expectErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			})
		}

		t.Run("empty string is rejected", func(t *testing.T) {
			assert.Error(t, validateSemverConstraints([]string{""}, "*"))
		})
		t.Run("latest is rejected", func(t *testing.T) {
			assert.Error(t, validateSemverConstraints([]string{"latest"}, "*"))
		})
	})

	t.Run("allowed wildcards star empty latest", func(t *testing.T) {
		for name, tc := range commonCases {
			t.Run(name, func(t *testing.T) {
				err := validateSemverConstraints(tc.tags, "*", "", "latest")
				if tc.expectErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			})
		}

		t.Run("empty string is valid wildcard", func(t *testing.T) {
			assert.NoError(t, validateSemverConstraints([]string{""}, "*", "", "latest"))
		})
		t.Run("latest is valid wildcard", func(t *testing.T) {
			assert.NoError(t, validateSemverConstraints([]string{"latest"}, "*", "", "latest"))
		})
		t.Run("mix of empty string and range constraint", func(t *testing.T) {
			assert.NoError(t, validateSemverConstraints([]string{"", ">= v0.4.0"}, "*", "", "latest"))
		})
	})
}

func TestReconcileRejectsInvalidConstraints(t *testing.T) {
	obj := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bad-constraints",
			Namespace: testNamespace,
		},
		Spec: configapi.FunctionConfigSpec{
			Image: "bad-constraints",
			GoExecutor: &configapi.GoExecutorConfig{
				Tags: []string{"not-semver!!"},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(schemeWithFunctionConfig(t)).
		WithObjects(obj).
		WithStatusSubresource(&configapi.FunctionConfig{}).
		Build()

	r := &Reconciler{
		Client:              c,
		FunctionConfigStore: NewFunctionConfigStore(defaultImagePrefix, functionCacheDir),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: obj.Name, Namespace: testNamespace},
	})
	assert.Error(t, err, "Reconcile should return an error for invalid tag constraints")

	_, exists := r.FunctionConfigStore.GetFunctionConfig(obj.Name)
	assert.False(t, exists, "FunctionConfig with invalid constraints should not be cached")
}

func TestGetProcessorFromCacheWithSemverConstraint(t *testing.T) {
	store := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)

	obj := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "set-namespace", Namespace: testNamespace},
		Spec: configapi.FunctionConfigSpec{
			Image:    "set-namespace",
			Prefixes: []string{""},
			GoExecutor: &configapi.GoExecutorConfig{
				Tags: []string{">= v0.4.0 < v0.5.0"},
			},
		},
	}
	store.UpdateExecCache(obj.Name, obj)

	processor, found := store.GetProcessorFromCache("ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.2")
	assert.True(t, found, "v0.4.2 should satisfy constraint >= v0.4.0 < v0.5.0")
	assert.NotNil(t, processor)

	_, found = store.GetProcessorFromCache("ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.5.0")
	assert.False(t, found, "v0.5.0 should not satisfy constraint >= v0.4.0 < v0.5.0")

	_, found = store.GetProcessorFromCache("ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.3.9")
	assert.False(t, found, "v0.3.9 should not satisfy constraint >= v0.4.0 < v0.5.0")
}

func TestGetBinaryFromCacheByConstraintWithRangeTags(t *testing.T) {
	store := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)

	obj := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "set-image", Namespace: testNamespace},
		Spec: configapi.FunctionConfigSpec{
			Image:    "set-image",
			Prefixes: []string{""},
			BinaryExecutor: &configapi.BinaryExecutorConfig{
				Tags: []string{">= v0.1.0 < v0.2.0"},
				Path: "set-image",
			},
		},
	}
	store.UpdateBinaryCache(&obj.Spec)

	path, found := store.GetBinaryFromCacheByConstraint("ghcr.io/kptdev/krm-functions-catalog/set-image", "v0.1.5")
	assert.True(t, found)
	assert.Equal(t, "/functions/set-image", path)

	_, found = store.GetBinaryFromCacheByConstraint("ghcr.io/kptdev/krm-functions-catalog/set-image", "v0.2.0")
	assert.False(t, found)
}

func TestGetBinaryFromCacheWithSemverConstraint(t *testing.T) {
	store := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)

	obj := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "set-image", Namespace: testNamespace},
		Spec: configapi.FunctionConfigSpec{
			Image:    "set-image",
			Prefixes: []string{""},
			BinaryExecutor: &configapi.BinaryExecutorConfig{
				Tags: []string{"~0.1, != 0.1.1"},
				Path: "set-image",
			},
		},
	}
	store.UpdateBinaryCache(&obj.Spec)

	path, found := store.GetBinaryFromCache("ghcr.io/kptdev/krm-functions-catalog/set-image:v0.1.5")
	assert.True(t, found, "v0.1.5 should satisfy constraint ~0.1, != 0.1.1")
	assert.Equal(t, "/functions/set-image", path)

	_, found = store.GetBinaryFromCache("ghcr.io/kptdev/krm-functions-catalog/set-image:v0.1.1")
	assert.False(t, found, "v0.1.1 should be excluded by != 0.1.1")

	_, found = store.GetBinaryFromCache("ghcr.io/kptdev/krm-functions-catalog/set-image:v0.2.0")
	assert.False(t, found, "v0.2.0 should not satisfy ~0.1")
}

func TestGetBinaryFromCacheWithEmptyTagsMatchesNothing(t *testing.T) {
	store := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)

	obj := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "set-image", Namespace: testNamespace},
		Spec: configapi.FunctionConfigSpec{
			Image:    "set-image",
			Prefixes: []string{""},
			BinaryExecutor: &configapi.BinaryExecutorConfig{
				Tags: []string{},
				Path: "set-image",
			},
		},
	}
	store.UpdateBinaryCache(&obj.Spec)

	_, found := store.GetBinaryFromCache("ghcr.io/kptdev/krm-functions-catalog/set-image:v0.1.4")
	assert.False(t, found, "empty BinaryExecutor.Tags should not match any version")
}

func TestWildcardTagMatchesAnyVersion(t *testing.T) {
	store := NewFunctionConfigStore(defaultImagePrefix, functionCacheDir)

	obj := &configapi.FunctionConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "set-namespace", Namespace: testNamespace},
		Spec: configapi.FunctionConfigSpec{
			Image:    "set-namespace",
			Prefixes: []string{""},
			GoExecutor: &configapi.GoExecutorConfig{
				Tags: []string{"*"},
			},
		},
	}
	store.UpdateExecCache(obj.Name, obj)

	for _, version := range []string{"v0.1.0", "v99.99.99", "v0.0.1"} {
		processor, found := store.GetProcessorFromCache("ghcr.io/kptdev/krm-functions-catalog/set-namespace:" + version)
		assert.True(t, found, "wildcard should match version %s", version)
		assert.NotNil(t, processor)
	}
}
