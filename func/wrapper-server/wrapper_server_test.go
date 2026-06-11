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
	"testing"

	pb "github.com/kptdev/porch/func/proto"
	"github.com/stretchr/testify/assert"
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
