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

package internal

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	pb "github.com/kptdev/porch/func/evaluator"
	imageutil "github.com/kptdev/porch/pkg/util/image"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/klog/v2"
)

const (
	defaultKRMImagePrefix = "ghcr.io/kptdev/krm-functions-catalog/"
	setImageFunction      = "set-image"
	starlarkFunction      = "starlark"
	testImageName         = "test-image"
)

func TestNewExecutableEvaluator(t *testing.T) {
	const tempCacheDir = "/tmp/func_cache"
	t.Run("no errors", func(t *testing.T) {
		_, err := NewExecutableEvaluator("/tmp/function-cache")
		assert.NoError(t, err)
	})
}

func TestEvaluateExecutableFunction(t *testing.T) {
	flagSet := flag.NewFlagSet("log-level", flag.ContinueOnError)
	klog.InitFlags(flagSet)
	_ = flagSet.Parse([]string{"--v", "3"})

	const tempCacheDir = "/tmp/func_cache"
	t.Run("no exec_path returns function not found error", func(t *testing.T) {
		ctx := t.Context()

		req := &pb.EvaluateFunctionRequest{
			ResourceList: []byte("req-rl"),
			Image:        imageutil.Join(defaultKRMImagePrefix, testImageName),
			Tag:          ">> 0.1.3 < 0.2.0", // Invalid semver constraint, '>>' is not a valid operator
		}

		evaluator, _ := NewExecutableEvaluator("/tmp/function-cache")
		resp, err := evaluator.EvaluateFunction(ctx, req)

		assert.Nil(t, resp)
		assert.Equal(t, &fn.NotFoundError{
			Function: kptfilev1.Function{Image: req.Image},
		}, err)
	})
	t.Run("failed to execute function", func(t *testing.T) {
		ctx := t.Context()

		req := &pb.EvaluateFunctionRequest{
			ResourceList: []byte("req-rl"),
			Image:        imageutil.Join(defaultKRMImagePrefix, setImageFunction),
			ExecPath:     "/tmp/function-cache/nonexistent-binary",
		}

		evaluator, _ := NewExecutableEvaluator("/tmp/function-cache")
		_, err := evaluator.EvaluateFunction(ctx, req)
		assert.ErrorContains(t, err, fmt.Sprintf("Failed to execute function \"%s\":", req.Image))
	})
	t.Run("successful function execution", func(t *testing.T) {
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

		// Use a valid KRM ResourceList format
		const resourceList = `apiVersion: config.kubernetes.io/v1
kind: ResourceList
items: []
`

		req := &pb.EvaluateFunctionRequest{
			ResourceList: []byte(resourceList),
			Image:        imageutil.Join(defaultKRMImagePrefix, setImageFunction),
			ExecPath:     testBinary,
		}

		evaluator, err := NewExecutableEvaluator(tmpDir)
		require.NoError(t, err)

		resp, err := evaluator.EvaluateFunction(ctx, req)

		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, []byte(resourceList), resp.ResourceList)
	})
}
