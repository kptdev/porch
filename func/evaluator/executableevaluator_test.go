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

package evaluator

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/functionconfigs"
	pb "github.com/kptdev/porch/func/proto"
	imageutil "github.com/kptdev/porch/pkg/util/image"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/klog/v2"
)

const (
	defaultKRMImagePrefix = runneroptions.GHCRImagePrefix
	setImageFunction      = "set-image"
	starlarkFunction      = "starlark"
)

func getFunctionConfigStore(t *testing.T, binaryDir string) *functionconfigs.FunctionConfigStore {
	t.Helper()
	starlarkConfig := &configapi.FunctionConfig{
		Spec: configapi.FunctionConfigSpec{
			Image: starlarkFunction,
			Prefixes: []string{
				"",
			},
			BinaryExecutor: &configapi.BinaryExecutorConfig{
				Tags: []string{
					"v0.5.2",
					"v0.5.3",
				},
				Path: starlarkFunction,
			},
		},
	}
	setImageConfig := &configapi.FunctionConfig{
		Spec: configapi.FunctionConfigSpec{
			Image: setImageFunction,
			Prefixes: []string{
				"",
			},
			BinaryExecutor: &configapi.BinaryExecutorConfig{
				Tags: []string{
					"v0.1.2",
					"v0.1.3",
				},
				Path: setImageFunction,
			},
		},
	}
	fstore := functionconfigs.NewStore(defaultKRMImagePrefix, binaryDir)
	require.NoError(t, fstore.Store(starlarkConfig))
	require.NoError(t, fstore.Store(setImageConfig))
	return fstore
}

func TestNewExecutableEvaluator(t *testing.T) {
	const tempCacheDir = "/tmp/func_cache"
	t.Run("no errors", func(t *testing.T) {
		executableEvaluatorOptions := ExecutableEvaluatorOptions{
			FunctionCacheDir: tempCacheDir,
		}
		fStore := getFunctionConfigStore(t, executableEvaluatorOptions.FunctionCacheDir)
		_, err := NewExecutableEvaluator(fStore)
		assert.NoError(t, err)
	})
}

func TestEvaluateExecutableFunction(t *testing.T) {
	flagSet := flag.NewFlagSet("log-level", flag.ContinueOnError)
	klog.InitFlags(flagSet)
	_ = flagSet.Parse([]string{"--v", "5"})

	const tempCacheDir = "/tmp/func_cache"
	t.Run("invalid semver constraint will cause function not found error", func(t *testing.T) {
		ctx := t.Context()
		executableEvaluatorOptions := ExecutableEvaluatorOptions{
			FunctionCacheDir: tempCacheDir,
		}

		req := &pb.EvaluateFunctionRequest{
			ResourceList: []byte("req-rl"),
			Image:        imageutil.Join(defaultKRMImagePrefix, testImageName),
			Tag:          ">> 0.1.3 < 0.2.0", // Invalid semver constraint, '>>' is not a valid operator
		}

		fStore := getFunctionConfigStore(t, executableEvaluatorOptions.FunctionCacheDir)
		evaluator, _ := NewExecutableEvaluator(fStore)
		resp, err := evaluator.EvaluateFunction(ctx, req)

		assert.Nil(t, resp)
		assert.Equal(t, &fn.NotFoundError{
			Function: kptfilev1.Function{Image: req.Image},
		}, err)
	})
	t.Run("function is not included in the config", func(t *testing.T) {
		ctx := t.Context()
		executableEvaluatorOptions := ExecutableEvaluatorOptions{
			FunctionCacheDir: tempCacheDir,
		}

		req := &pb.EvaluateFunctionRequest{
			ResourceList: []byte("req-rl"),
			// This image is not included in the config.yaml -> function not found
			Image: imageutil.Join(defaultKRMImagePrefix, testImageName),
			Tag:   "> 0.1.3 < 0.2.0", // This is a valid semver constraint syntax
		}

		fStore := getFunctionConfigStore(t, executableEvaluatorOptions.FunctionCacheDir)
		evaluator, _ := NewExecutableEvaluator(fStore)
		_, err := evaluator.EvaluateFunction(ctx, req)
		assert.Equal(t, fmt.Sprintf("function \"%s\" not found", req.Image), err.Error())
	})
	t.Run("function does not match the semantic version constraints", func(t *testing.T) {
		ctx := t.Context()
		executableEvaluatorOptions := ExecutableEvaluatorOptions{
			FunctionCacheDir: tempCacheDir,
		}

		req := &pb.EvaluateFunctionRequest{
			ResourceList: []byte("req-rl"),
			Image:        imageutil.Join(defaultKRMImagePrefix, setImageFunction),
			Tag:          "> 0.1.3 < 0.2.0",
		}

		fStore := getFunctionConfigStore(t, executableEvaluatorOptions.FunctionCacheDir)
		evaluator, _ := NewExecutableEvaluator(fStore)
		_, err := evaluator.EvaluateFunction(ctx, req)
		assert.ErrorContains(t, err, fmt.Sprintf("function \"%s\" not found", req.Image), err.Error())
	})
	t.Run("failed to execute function", func(t *testing.T) {
		ctx := t.Context()
		executableEvaluatorOptions := ExecutableEvaluatorOptions{
			FunctionCacheDir: tempCacheDir, // function cache dir is not exist
		}

		req := &pb.EvaluateFunctionRequest{
			ResourceList: []byte("req-rl"),
			Image:        imageutil.Join(defaultKRMImagePrefix, setImageFunction),
			Tag:          ">= 0.1.2 < 0.2.0",
		}

		fStore := getFunctionConfigStore(t, executableEvaluatorOptions.FunctionCacheDir)

		evaluator, _ := NewExecutableEvaluator(fStore)
		_, err := evaluator.EvaluateFunction(ctx, req)
		assert.ErrorContains(t, err, fmt.Sprintf("Failed to execute function \"%s\":", req.Image))
	})
	t.Run("successful function execution with semantic versioning", func(t *testing.T) {
		ctx := t.Context()

		// Create a temporary directory for the function cache
		tmpDir := t.TempDir()

		// Create a simple test executable that echoes input as a valid KRM function
		testBinary := filepath.Join(tmpDir, setImageFunction)
		const testScript = `#!/bin/sh
# Emulating the KRM function execution by running this shell script
cat
exit 0
`
		err := os.WriteFile(testBinary, []byte(testScript), 0755)
		require.NoError(t, err)

		executableEvaluatorOptions := ExecutableEvaluatorOptions{
			FunctionCacheDir: tmpDir,
		}

		// Use a valid KRM ResourceList format
		const resourceList = `apiVersion: config.kubernetes.io/v1
kind: ResourceList
items: []
`

		// This constraint matches both v0.1.2 and v0.1.3 from config.yaml
		// We expect v0.1.3 to be selected as it's the greatest version
		req := &pb.EvaluateFunctionRequest{
			ResourceList: []byte(resourceList),
			Image:        imageutil.Join(defaultKRMImagePrefix, setImageFunction),
			Tag:          ">= 0.1.2 < 0.2.0",
		}

		// Capture klog output by redirecting stderr
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		fStore := getFunctionConfigStore(t, executableEvaluatorOptions.FunctionCacheDir)
		evaluator, err := NewExecutableEvaluator(fStore)
		require.NoError(t, err)

		resp, err := evaluator.EvaluateFunction(ctx, req)

		// Flush klog and restore stderr
		klog.Flush()
		w.Close()
		os.Stderr = oldStderr

		// Read captured output
		var logBuffer bytes.Buffer
		logBuffer.ReadFrom(r)
		logOutput := logBuffer.String()

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify the klog message contains the expected version selection
		assert.Contains(t, logOutput, `Selected tag "v0.1.3"`)
	})
	t.Run("successful function execution with explicit tagging", func(t *testing.T) {
		ctx := t.Context()

		// Create a temporary directory for the function cache
		tmpDir := t.TempDir()

		// Create a simple test executable that echoes input as a valid KRM function
		testBinary := filepath.Join(tmpDir, setImageFunction)
		const testScript = `#!/bin/sh
# Emulating the KRM function execution by running this shell script
cat
exit 0
`
		err := os.WriteFile(testBinary, []byte(testScript), 0755)
		require.NoError(t, err)

		executableEvaluatorOptions := ExecutableEvaluatorOptions{
			FunctionCacheDir: tmpDir,
		}

		// Use a valid KRM ResourceList format
		const resourceList = `apiVersion: config.kubernetes.io/v1
kind: ResourceList
items: []
`

		// Explicit tagging
		req := &pb.EvaluateFunctionRequest{
			ResourceList: []byte(resourceList),
			Image:        imageutil.Join(defaultKRMImagePrefix, setImageFunction) + ":v0.1.3",
		}

		// Capture klog output by redirecting stderr
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		fStore := getFunctionConfigStore(t, executableEvaluatorOptions.FunctionCacheDir)
		evaluator, err := NewExecutableEvaluator(fStore)
		require.NoError(t, err)

		resp, err := evaluator.EvaluateFunction(ctx, req)

		// Flush klog and restore stderr
		klog.Flush()
		w.Close()
		os.Stderr = oldStderr

		// Read captured output
		var logBuffer bytes.Buffer
		logBuffer.ReadFrom(r)
		logOutput := logBuffer.String()

		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify the klog message contains the expected version selection
		assert.Contains(t, logOutput, `Image tag is empty, using the image with explicit tag: "ghcr.io/kptdev/krm-functions-catalog/set-image:v0.1.3"`)
	})
	t.Run("successful execution with explicit tagging + Tag field set", func(t *testing.T) {
		ctx := t.Context()

		// Create a temporary directory for the function cache
		tmpDir := t.TempDir()

		// Create a simple test executable that echoes input as a valid KRM function
		testBinary := filepath.Join(tmpDir, setImageFunction)
		const testScript = `#!/bin/sh
# Emulating the KRM function execution by running this shell script
cat
exit 0
`
		err := os.WriteFile(testBinary, []byte(testScript), 0755)
		require.NoError(t, err)

		executableEvaluatorOptions := ExecutableEvaluatorOptions{
			FunctionCacheDir: tmpDir,
		}

		// Use a valid KRM ResourceList format
		const resourceList = `apiVersion: config.kubernetes.io/v1
kind: ResourceList
items: []
`

		req := &pb.EvaluateFunctionRequest{
			ResourceList: []byte(resourceList),
			Image:        imageutil.Join(defaultKRMImagePrefix, setImageFunction) + ":v0.0.1",
			Tag:          ">= 0.1.2 < 0.2.0",
		}

		// Capture klog output by redirecting stderr
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		fStore := getFunctionConfigStore(t, executableEvaluatorOptions.FunctionCacheDir)
		evaluator, err := NewExecutableEvaluator(fStore)
		require.NoError(t, err)

		resp, err := evaluator.EvaluateFunction(ctx, req)

		// Flush klog and restore stderr
		klog.Flush()
		w.Close()
		os.Stderr = oldStderr

		// Read captured output
		var logBuffer bytes.Buffer
		logBuffer.ReadFrom(r)
		logOutput := logBuffer.String()

		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify the klog message contains the expected version selection
		assert.Contains(t, logOutput, `Selected tag "v0.1.3"`)
	})
}
