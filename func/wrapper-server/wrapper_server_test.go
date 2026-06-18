// Copyright 2025 The kpt Authors
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
	"bytes"
	"context"
	"flag"
	"strings"
	"testing"

	pb "github.com/kptdev/porch/func/evaluator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/klog/v2"
	"sigs.k8s.io/kustomize/kyaml/kio"
)

func TestWrapperServerEvaluate(t *testing.T) {

	flagSet := flag.NewFlagSet("log-level", flag.ContinueOnError)
	klog.InitFlags(flagSet)
	_ = flagSet.Parse([]string{"--v", "5"})

	tests := []struct {
		name         string
		expectFail   bool
		skip         bool
		evaluator    singleFunctionEvaluator
		req          *pb.EvaluateFunctionRequest
		expectedResp *pb.EvaluateFunctionResponse
	}{
		{
			name:       "Successful Deployment evaluation",
			expectFail: false,
			skip:       false,
			evaluator: singleFunctionEvaluator{
				entrypoint: []string{"./testdata/search_replace_test.sh", "hello-server-namespace", "hello-server-namespace-new"},
			},
			req: &pb.EvaluateFunctionRequest{
				ResourceList: createMockResourceList("./testdata/deployment.yaml"),
				Image:        "search-and-replace",
			},
			expectedResp: &pb.EvaluateFunctionResponse{
				ResourceList: createMockResourceList("./testdata/replaced/deployment.yaml"),
			},
		},
		{
			name:       "Successful Config Map evaluation",
			expectFail: false,
			skip:       false,
			evaluator: singleFunctionEvaluator{
				entrypoint: []string{"./testdata/search_replace_test.sh", "configmap-namespace", "configmap-namespace-new"},
			},
			req: &pb.EvaluateFunctionRequest{
				ResourceList: createMockResourceList("./testdata/config-map.yaml"),
				Image:        "search-and-replace",
			},
			expectedResp: &pb.EvaluateFunctionResponse{
				ResourceList: createMockResourceList("./testdata/replaced/config-map.yaml"),
			},
		},
		{
			name:       "Successful Cron Job evaluation",
			expectFail: false,
			skip:       false,
			evaluator: singleFunctionEvaluator{
				entrypoint: []string{"./testdata/search_replace_test.sh", "cronjob-namespace", "cronjob-namespace-new"},
			},
			req: &pb.EvaluateFunctionRequest{
				ResourceList: createMockResourceList("./testdata/cron-job.yaml"),
				Image:        "search-and-replace",
			},
			expectedResp: &pb.EvaluateFunctionResponse{
				ResourceList: createMockResourceList("./testdata/replaced/cron-job.yaml"),
			},
		},
		{
			name:       "Incorrect evaluator entrypoint",
			expectFail: true,
			skip:       false,
			evaluator: singleFunctionEvaluator{
				entrypoint: []string{"./testdata/search_replace_test.sh"},
			},
			req: &pb.EvaluateFunctionRequest{
				ResourceList: createMockResourceList("./testdata/replaced/deployment.yaml"),
				Image:        "search-and-replace",
			},
			expectedResp: &pb.EvaluateFunctionResponse{
				ResourceList: nil,
			},
		},
		{
			name:       "Null resource list",
			expectFail: true,
			skip:       false,
			evaluator: singleFunctionEvaluator{
				entrypoint: []string{"./testdata/search_replace_test.sh", "hello-server-namespace", "hello-server-namespace-new"},
			},
			req: &pb.EvaluateFunctionRequest{
				ResourceList: nil,
				Image:        "search-and-replace",
			},
			expectedResp: &pb.EvaluateFunctionResponse{
				ResourceList: nil,
			},
		},
		{
			name:       "Invalid yaml format",
			expectFail: true,
			skip:       false,
			evaluator: singleFunctionEvaluator{
				entrypoint: []string{"./testdata/search_replace_test.sh", "hello-server-namespace", "hello-server-namespace-new"},
			},
			req: &pb.EvaluateFunctionRequest{
				ResourceList: []byte("apiVersion: apps/v1 kind: Deployment metadata: name: hello-server namespace: hello-server-namespace"),
				Image:        "search-and-replace",
			},
			expectedResp: &pb.EvaluateFunctionResponse{
				ResourceList: nil,
			},
		},
		{
			name:       "Failure to evaluate function with structured results",
			expectFail: true,
			skip:       false,
			evaluator: singleFunctionEvaluator{
				entrypoint: []string{"./testdata/search_replace_test_fail.sh"},
			},
			req: &pb.EvaluateFunctionRequest{
				ResourceList: createMockResourceList("./testdata/deployment.yaml"),
				Image:        "search-and-replace",
			},
			expectedResp: &pb.EvaluateFunctionResponse{
				ResourceList: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip {
				t.SkipNow()
			}

			resp, err := tt.evaluator.EvaluateFunction(context.Background(), tt.req)
			if err != nil && !tt.expectFail {
				t.Errorf("EvaluateFunction unexpected error: %v, Expect Fail %v", err, tt.expectFail)
			}
			if resp == nil && tt.expectFail {
				t.Logf("Expect Fail: %v, Evaluate Function expecteded error: %v", tt.expectFail, err)
			}
			if resp != nil && !tt.expectFail {
				assert.Equal(t, string(tt.expectedResp.ResourceList), string(resp.ResourceList))
			}
		})
	}
}

func createMockResourceList(pkg string) []byte {
	r := kio.LocalPackageReader{
		PackagePath:        pkg,
		IncludeSubpackages: true,
		WrapBareSeqNode:    true,
	}

	var b bytes.Buffer
	w := kio.ByteWriter{
		Writer:                &b,
		KeepReaderAnnotations: true,
		Style:                 0,
		FunctionConfig:        nil,
		WrappingKind:          kio.ResourceListKind,
		WrappingAPIVersion:    kio.ResourceListAPIVersion,
	}

	if err := (kio.Pipeline{Inputs: []kio.Reader{r}, Outputs: []kio.Writer{w}}).Execute(); err != nil {
		panic(err)
	}

	return b.Bytes()
}

func TestFlattenStderr(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single line no newline",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "multiple lines",
			input:    "Starting mutation\nReplacing value\nCompleted",
			expected: "Starting mutation | Replacing value | Completed",
		},
		{
			name:     "trailing newline",
			input:    "Starting mutation\nReplacing value\n",
			expected: "Starting mutation | Replacing value",
		},
		{
			name:     "leading and trailing newlines",
			input:    "\nStarting mutation\nReplacing value\n",
			expected: "Starting mutation | Replacing value",
		},
		{
			name:     "preserves leading spaces",
			input:    "  indented line\nnext line",
			expected: "  indented line | next line",
		},
		{
			name:     "preserves trailing spaces",
			input:    "line with trailing space \nanother line",
			expected: "line with trailing space  | another line",
		},
		{
			name:     "windows line endings",
			input:    "Starting mutation\r\nReplacing value\r\nCompleted\r\n",
			expected: "Starting mutation | Replacing value | Completed",
		},
		{
			name:     "mixed line endings",
			input:    "line1\r\nline2\nline3\r\n",
			expected: "line1 | line2 | line3",
		},
		{
			name:     "standalone carriage return",
			input:    "line1\rline2",
			expected: "line1 | line2",
		},
		{
			name:     "trailing carriage return",
			input:    "progress output\r",
			expected: "progress output",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "single newline",
			input:    "\n",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := flattenStderr(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEvaluateFunction_StderrLogIsFlattened(t *testing.T) {
	// Integration test: verifies that when --flatten-log is enabled,
	// EvaluateFunctionResponse.Log contains flattened single-line stderr.
	evaluator := singleFunctionEvaluator{
		entrypoint: []string{"./testdata/stderr_multiline_test.sh"},
		flattenLog: true,
	}
	req := &pb.EvaluateFunctionRequest{
		ResourceList: createMockResourceList("./testdata/deployment.yaml"),
		Image:        "test-stderr",
	}

	resp, err := evaluator.EvaluateFunction(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	logStr := string(resp.Log)
	assert.Equal(t, "Starting mutation | Replacing value | Completed", logStr)
	assert.NotContains(t, logStr, "\n", "resp.Log should be flattened, not contain raw newlines")
}

func TestEvaluateFunction_StderrLogPreservesRawByDefault(t *testing.T) {
	// Integration test: verifies that without --flatten-log,
	// EvaluateFunctionResponse.Log preserves the raw multi-line stderr.
	evaluator := singleFunctionEvaluator{
		entrypoint: []string{"./testdata/stderr_multiline_test.sh"},
		flattenLog: false,
	}
	req := &pb.EvaluateFunctionRequest{
		ResourceList: createMockResourceList("./testdata/deployment.yaml"),
		Image:        "test-stderr",
	}

	resp, err := evaluator.EvaluateFunction(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	logStr := string(resp.Log)
	assert.Contains(t, logStr, "Starting mutation\n")
	assert.Contains(t, logStr, "Replacing value\n")
	assert.Contains(t, logStr, "Completed\n")
	assert.NotContains(t, logStr, " | ", "resp.Log should preserve raw newlines when flatten-log is disabled")
}

func TestEvaluateFunction_StderrErrorContainsFlattenedMessage(t *testing.T) {
	// Integration test: verifies that on failure with --flatten-log enabled,
	// the gRPC error message contains flattened (single-line) stderr.
	evaluator := singleFunctionEvaluator{
		entrypoint: []string{"./testdata/stderr_multiline_fail_test.sh"},
		flattenLog: true,
	}
	req := &pb.EvaluateFunctionRequest{
		ResourceList: createMockResourceList("./testdata/deployment.yaml"),
		Image:        "test-stderr-fail",
	}

	resp, err := evaluator.EvaluateFunction(context.Background(), req)
	require.Error(t, err)
	require.Nil(t, resp)

	errMsg := err.Error()
	// The error string should contain pipe-separated (flattened) stderr lines.
	assert.True(t, strings.Contains(errMsg, " | "),
		"error message should contain flattened stderr with ' | ' separator, got: %s", errMsg)
	assert.NotContains(t, errMsg, "\n",
		"error message should not contain raw newlines")
}

func TestEvaluateFunction_StderrErrorPreservesRawByDefault(t *testing.T) {
	// Integration test: verifies that on failure without --flatten-log,
	// the gRPC error message contains raw multi-line stderr.
	evaluator := singleFunctionEvaluator{
		entrypoint: []string{"./testdata/stderr_multiline_fail_test.sh"},
		flattenLog: false,
	}
	req := &pb.EvaluateFunctionRequest{
		ResourceList: createMockResourceList("./testdata/deployment.yaml"),
		Image:        "test-stderr-fail",
	}

	resp, err := evaluator.EvaluateFunction(context.Background(), req)
	require.Error(t, err)
	require.Nil(t, resp)

	errMsg := err.Error()
	assert.Contains(t, errMsg, "\n", "error message should include raw newlines when flatten-log is disabled")
	assert.NotContains(t, errMsg, " | ", "error message should preserve raw newlines when flatten-log is disabled")
}
