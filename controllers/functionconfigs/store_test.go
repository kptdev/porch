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
	"testing"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetProcessorFromCache(t *testing.T) {
	store := NewStore(defaultImagePrefix, functionCacheDir)

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
	require.NoError(t, store.Store(obj))

	// Found with full prefix
	processor, found := store.GetProcessor(defaultImagePrefix + "/set-namespace:v0.4.1")
	assert.True(t, found)
	assert.NotNil(t, processor)

	// Found without prefix (short form)
	processor, found = store.GetProcessor("set-namespace:v0.4.1")
	assert.True(t, found)
	assert.NotNil(t, processor)

	// Not found for unknown tag
	_, found = store.GetProcessor("set-namespace:v9.9.9")
	assert.False(t, found)

	// Not found for unknown image
	_, found = store.GetProcessor("nonexistent:v1.0.0")
	assert.False(t, found)
}

func TestConcurrentAccessSafety(t *testing.T) {
	// Verifies no data race when Store and GetProcessor
	// are called concurrently (the fix for the data race bug).
	store := NewStore(defaultImagePrefix, functionCacheDir)

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
			// TODO: unsure if this can be replicated as intended with the new Store() method
			_ = store.Store(obj)
		}
	}()

	// Reader goroutine (concurrent with writer)
	for i := 0; i < 100; i++ {
		store.GetProcessor("ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.1")
	}

	<-done

	// After all writes complete, the entry should be present
	_, found := store.GetProcessor("ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.1")
	assert.True(t, found)
}
