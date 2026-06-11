// Copyright 2022 The kpt Authors
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

package engine

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	fnsdk "github.com/kptdev/krm-functions-sdk/go/fn"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/functionconfigs"
	imageutil "github.com/kptdev/porch/pkg/util/image"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const (
	customImagePrefix     = "test.io/kptdev/krm-functions-catalog"
	defaultKRMImagePrefix = runneroptions.GHCRImagePrefix
	testImageName         = "test-image"

	setNamespaceFunction      = "set-namespace"
	applyReplacementsFunction = "apply-replacements"
)

func TestNewBuiltinRuntime(t *testing.T) {
	t.Run("custom image prefix specified", func(t *testing.T) {
		functionConfig := configapi.FunctionConfig{
			ObjectMeta: v1.ObjectMeta{
				Name: applyReplacementsFunction,
			},
			Spec: configapi.FunctionConfigSpec{
				Image: applyReplacementsFunction,
				Prefixes: []string{
					"",
					customImagePrefix,
				},
				GoExecutor: &configapi.GoExecutorConfig{
					Tags: []string{"v0.1.1"},
				},
			},
		}

		functionConfigStore := functionconfigs.NewStore(defaultKRMImagePrefix, "")
		require.NoError(t, functionConfigStore.Store(&functionConfig))

		br := newBuiltinRuntime(functionConfigStore)

		require.NotNil(t, br)
		require.NotNil(t, br.store)

		// Verify that functions are registered with the custom image prefix
		processor, exists := functionConfigStore.GetProcessor(customImagePrefix + "/" + applyReplacementsFunction + ":v0.1.1")
		assert.True(t, exists,
			"Expected function to be registered with custom image prefix: %s", customImagePrefix)
		assert.NotNil(t, processor)

		// Verify that functions are also registered with default GHCR prefix
		processor, exists = functionConfigStore.GetProcessor(defaultKRMImagePrefix + "/" + applyReplacementsFunction + ":v0.1.1")
		assert.True(t, exists,
			"Expected function to be registered with default GHCR prefix")
		assert.NotNil(t, processor)
	})
	t.Run("custom image prefix is not specified", func(t *testing.T) {
		functionConfig := configapi.FunctionConfig{
			ObjectMeta: v1.ObjectMeta{
				Name: applyReplacementsFunction,
			},
			Spec: configapi.FunctionConfigSpec{
				Image: applyReplacementsFunction,
				Prefixes: []string{
					"",
				},
				GoExecutor: &configapi.GoExecutorConfig{
					Tags: []string{"v0.1.1"},
				},
			},
		}
		functionConfigStore := functionconfigs.NewStore(defaultKRMImagePrefix, "")
		require.NoError(t, functionConfigStore.Store(&functionConfig))
		br := newBuiltinRuntime(functionConfigStore)

		assert.NotNil(t, br)
		assert.NotNil(t, br.store)

		// Verify that functions are not registered with the custom image prefix
		processor, exists := functionConfigStore.GetProcessor(customImagePrefix + applyReplacementsFunction + ":v0.1.1")
		assert.False(t, exists,
			"Expected function to not be registered with custom image prefix: %s", customImagePrefix)
		assert.Nil(t, processor)

		// Verify that functions are registered with default GHCR prefix
		processor, exists = functionConfigStore.GetProcessor(defaultKRMImagePrefix + "/" + applyReplacementsFunction + ":v0.1.1")
		assert.True(t, exists,
			"Expected function to be registered with default GHCR prefix")
		assert.NotNil(t, processor)
	})
}

func TestBuiltinRuntime(t *testing.T) {
	flagSet := flag.NewFlagSet("log-level", flag.ContinueOnError)
	klog.InitFlags(flagSet)
	_ = flagSet.Parse([]string{"--v", "5"})

	t.Run("invalid semver constraint syntax", func(t *testing.T) {
		ctx := t.Context()
		functionConfig := configapi.FunctionConfig{
			ObjectMeta: v1.ObjectMeta{
				Name: setNamespaceFunction,
			},
			Spec: configapi.FunctionConfigSpec{
				Image: setNamespaceFunction,
				GoExecutor: &configapi.GoExecutorConfig{
					Tags: []string{"v0.4.1"},
				},
			},
		}
		functionConfigStore := functionconfigs.NewStore(defaultKRMImagePrefix, "")
		require.NoError(t, functionConfigStore.Store(&functionConfig))
		br := newBuiltinRuntime(functionConfigStore)
		funct := &kptfilev1.Function{
			Image: filepath.Join(defaultKRMImagePrefix, testImageName),
			// Invalid semver constraint, '>>' is not a valid operator
			// -> will cause a function not found error
			Tag: ">> 0.4.0 < 0.5.0",
		}
		_, err := br.GetRunner(ctx, funct)
		assert.Equal(t, &fn.NotFoundError{Function: *funct}, err)
	})
	t.Run("builtinrutime not found", func(t *testing.T) {
		ctx := t.Context()
		functionConfig := configapi.FunctionConfig{
			ObjectMeta: v1.ObjectMeta{
				Name: setNamespaceFunction,
			},
			Spec: configapi.FunctionConfigSpec{
				Image: setNamespaceFunction,
				GoExecutor: &configapi.GoExecutorConfig{
					Tags: []string{"v0.4.1"},
				},
			},
		}
		functionConfigStore := functionconfigs.NewStore(defaultKRMImagePrefix, "")
		require.NoError(t, functionConfigStore.Store(&functionConfig))
		br := newBuiltinRuntime(functionConfigStore)
		funct := &kptfilev1.Function{
			Image: filepath.Join(defaultKRMImagePrefix, testImageName),
			// The semver constraint is valid, however, there is no function with
			// the name 'test-image' -> will cause a function not found error
			Tag: ">= 0.4.0 < 0.5.0",
		}
		_, err := br.GetRunner(ctx, funct)
		assert.Equal(t, &fn.NotFoundError{Function: *funct}, err)
	})
	t.Run("function does not match the semantic version constraints", func(t *testing.T) {
		ctx := t.Context()
		functionConfig := configapi.FunctionConfig{
			ObjectMeta: v1.ObjectMeta{
				Name: setNamespaceFunction,
			},
			Spec: configapi.FunctionConfigSpec{
				Image: setNamespaceFunction,
				GoExecutor: &configapi.GoExecutorConfig{
					Tags: []string{"v0.4.1"},
				},
			},
		}
		functionConfigStore := functionconfigs.NewStore(defaultKRMImagePrefix, "")
		require.NoError(t, functionConfigStore.Store(&functionConfig))
		br := newBuiltinRuntime(functionConfigStore)
		funct := &kptfilev1.Function{
			Image: filepath.Join(defaultKRMImagePrefix, setNamespaceFunction),
			// The semver constraint is valid, however, there is no function with
			// the name 'apply-replacements' that matches the specified semver constraint
			// -> will cause a function not found error
			Tag: "> 0.2.0 < 0.3.0",
		}
		_, err := br.GetRunner(ctx, funct)
		assert.Equal(t, &fn.NotFoundError{Function: *funct}, err)
	})
	t.Run("function not found using explicit tagging", func(t *testing.T) {
		ctx := t.Context()
		functionConfig := configapi.FunctionConfig{
			ObjectMeta: v1.ObjectMeta{
				Name: setNamespaceFunction,
			},
			Spec: configapi.FunctionConfigSpec{
				Image: setNamespaceFunction,
				GoExecutor: &configapi.GoExecutorConfig{
					Tags: []string{"v0.4.1"},
				},
			},
		}
		functionConfigStore := functionconfigs.NewStore(defaultKRMImagePrefix, "")
		require.NoError(t, functionConfigStore.Store(&functionConfig))
		br := newBuiltinRuntime(functionConfigStore)
		funct := &kptfilev1.Function{
			Image: imageutil.Join(defaultKRMImagePrefix, setNamespaceFunction) + ":v0.4.2",
			// Image is explicitly tagged with v0.4.2, however,
			// there is no function with this explicit tag in the cache
		}
		_, err := br.GetRunner(ctx, funct)
		assert.Equal(t, &fn.NotFoundError{Function: *funct}, err)
	})
	t.Run("function execution error", func(t *testing.T) {
		ctx := t.Context()
		functionConfig := configapi.FunctionConfig{
			ObjectMeta: v1.ObjectMeta{
				Name: applyReplacementsFunction,
			},
			Spec: configapi.FunctionConfigSpec{
				Image: applyReplacementsFunction,
				GoExecutor: &configapi.GoExecutorConfig{
					Tags: []string{"v0.1.0"},
				},
			},
		}
		functionConfigStore := functionconfigs.NewStore(defaultKRMImagePrefix, "")
		require.NoError(t, functionConfigStore.Store(&functionConfig))
		br := newBuiltinRuntime(functionConfigStore)
		function := &kptfilev1.Function{
			// Wrong function is specified for namespace setting,
			// which will cause an execution error when the function tries to run
			Image: imageutil.Join(defaultKRMImagePrefix, applyReplacementsFunction),
			Tag:   ">= 0.1.0 < 0.2.0",
		}
		fr, err := br.GetRunner(ctx, function)
		require.NoError(t, err)
		reader := bytes.NewReader([]byte(`apiVersion: config.kubernetes.io/v1alpha1
kind: ResourceList
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-cm
      namespace: old
    data:
      foo: bar
functionConfig:
  apiVersion: v1
  kind: ConfigMap
  data:
    namespace: test-ns
`))
		var buf bytes.Buffer
		err = fr.Run(reader, &buf)
		assert.ErrorContains(t, err, "function failure")
	})
	t.Run("successful execution with semantic versioning", func(t *testing.T) {
		ctx := t.Context()
		functionConfig := configapi.FunctionConfig{
			ObjectMeta: v1.ObjectMeta{
				Name: setNamespaceFunction,
			},
			Spec: configapi.FunctionConfigSpec{
				Image: setNamespaceFunction,
				GoExecutor: &configapi.GoExecutorConfig{
					Tags: []string{"v0.4.1"},
				},
			},
		}
		functionConfigStore := functionconfigs.NewStore(defaultKRMImagePrefix, "")
		require.NoError(t, functionConfigStore.Store(&functionConfig))
		br := newBuiltinRuntime(functionConfigStore)
		function := &kptfilev1.Function{
			Image: filepath.Join(defaultKRMImagePrefix, setNamespaceFunction),
			// This semver constraint matches the version of the apply-replacements function in builtin runtime,
			// so it should successfully find the function and run it
			Tag: ">= 0.4.0 < 0.5.0",
		}

		// Capture klog output by redirecting stderr
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		fr, err := br.GetRunner(ctx, function)
		require.NoError(t, err)

		// Flush klog and restore stderr
		klog.Flush()
		w.Close()
		os.Stderr = oldStderr

		// Read captured output
		var logBuffer bytes.Buffer
		logBuffer.ReadFrom(r)
		logOutput := logBuffer.String()

		// Verify the klog message contains the expected version selection
		assert.Contains(t, logOutput, `Selected tag "v0.4.1"`)

		reader := bytes.NewReader([]byte(`apiVersion: config.kubernetes.io/v1alpha1
kind: ResourceList
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-cm
      namespace: old
    data:
      foo: bar
functionConfig:
  apiVersion: v1
  kind: ConfigMap
  data:
    namespace: test-ns
`))

		var buf bytes.Buffer
		err = fr.Run(reader, &buf)
		require.NoError(t, err)
		rl, err := fnsdk.ParseResourceList(buf.Bytes())
		require.NoError(t, err)
		require.Len(t, rl.Items, 1)
		ns := rl.Items[0].GetNamespace()
		assert.Equal(t, "test-ns", ns)
	})
	t.Run("successful execution with explicit tagging", func(t *testing.T) {
		ctx := t.Context()
		functionConfig := configapi.FunctionConfig{
			ObjectMeta: v1.ObjectMeta{
				Name: setNamespaceFunction,
			},
			Spec: configapi.FunctionConfigSpec{
				Image: setNamespaceFunction,
				Prefixes: []string{
					"",
				},
				GoExecutor: &configapi.GoExecutorConfig{
					Tags: []string{"v0.4.1"},
				},
			},
		}
		functionConfigStore := functionconfigs.NewStore(defaultKRMImagePrefix, "")
		require.NoError(t, functionConfigStore.Store(&functionConfig))
		br := newBuiltinRuntime(functionConfigStore)
		function := &kptfilev1.Function{
			Image: filepath.Join(defaultKRMImagePrefix, setNamespaceFunction) + ":v0.4.1",
		}

		// Capture klog output by redirecting stderr
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		fr, err := br.GetRunner(ctx, function)
		require.NoError(t, err)

		// Flush klog and restore stderr
		klog.Flush()
		w.Close()
		os.Stderr = oldStderr

		// Read captured output
		var logBuffer bytes.Buffer
		logBuffer.ReadFrom(r)
		logOutput := logBuffer.String()

		// Verify the klog message contains the expected version selection
		assert.Contains(t, logOutput, `Image tag is empty, using the image with explicit tag: "ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.1"`)

		reader := bytes.NewReader([]byte(`apiVersion: config.kubernetes.io/v1alpha1
kind: ResourceList
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-cm
      namespace: old
    data:
      foo: bar
functionConfig:
  apiVersion: v1
  kind: ConfigMap
  data:
    namespace: test-ns
`))

		var buf bytes.Buffer
		err = fr.Run(reader, &buf)
		require.NoError(t, err)
		rl, err := fnsdk.ParseResourceList(buf.Bytes())
		require.NoError(t, err)
		require.Len(t, rl.Items, 1)
		ns := rl.Items[0].GetNamespace()
		assert.Equal(t, "test-ns", ns)
	})
	t.Run("successful execution with explicit tagging + Tag field set", func(t *testing.T) {
		ctx := t.Context()
		functionConfig := configapi.FunctionConfig{
			ObjectMeta: v1.ObjectMeta{
				Name: setNamespaceFunction,
			},
			Spec: configapi.FunctionConfigSpec{
				Image: setNamespaceFunction,
				GoExecutor: &configapi.GoExecutorConfig{
					Tags: []string{"v0.4.1"},
				},
			},
		}
		functionConfigStore := functionconfigs.NewStore(defaultKRMImagePrefix, "")
		require.NoError(t, functionConfigStore.Store(&functionConfig))
		br := newBuiltinRuntime(functionConfigStore)
		function := &kptfilev1.Function{
			Image: filepath.Join(defaultKRMImagePrefix, setNamespaceFunction) + ":v0.3.0",
			Tag:   ">= 0.4.0 < 0.5.0",
		}

		// Capture klog output by redirecting stderr
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		fr, err := br.GetRunner(ctx, function)
		require.NoError(t, err)

		// Flush klog and restore stderr
		klog.Flush()
		w.Close()
		os.Stderr = oldStderr

		// Read captured output
		var logBuffer bytes.Buffer
		logBuffer.ReadFrom(r)
		logOutput := logBuffer.String()

		// Verify the klog message contains the expected version selection
		assert.Contains(t, logOutput, `Selected tag "v0.4.1"`)

		reader := bytes.NewReader([]byte(`apiVersion: config.kubernetes.io/v1alpha1
kind: ResourceList
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-cm
      namespace: old
    data:
      foo: bar
functionConfig:
  apiVersion: v1
  kind: ConfigMap
  data:
    namespace: test-ns
`))

		var buf bytes.Buffer
		err = fr.Run(reader, &buf)
		require.NoError(t, err)
		rl, err := fnsdk.ParseResourceList(buf.Bytes())
		require.NoError(t, err)
		require.Len(t, rl.Items, 1)
		ns := rl.Items[0].GetNamespace()
		assert.Equal(t, "test-ns", ns)
	})
}
