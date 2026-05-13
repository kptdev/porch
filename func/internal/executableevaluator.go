// Copyright 2022, 2026 The kpt Authors
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
	"bytes"
	"context"
	"fmt"
	"os/exec"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/porch/controllers/functionconfigs/reconciler"
	pb "github.com/kptdev/porch/func/evaluator"
	regclientref "github.com/regclient/regclient/types/ref"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

type ExecutableEvaluatorOptions struct {
	FunctionCacheDir string // Path to cached functions
}

type executableEvaluator struct {
	// Fast-path function cache
	FunctionConfigStore *reconciler.FunctionConfigStore
}

var _ Evaluator = &executableEvaluator{}

func NewExecutableEvaluator(FunctionConfigStore *reconciler.FunctionConfigStore) (Evaluator, error) {
	return &executableEvaluator{
		FunctionConfigStore: FunctionConfigStore,
	}, nil
}

func (e *executableEvaluator) EvaluateFunction(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
	var selectedBinary string
	if req.Tag != "" {
		ref, err := regclientref.New(req.Image)
		if err != nil {
			return nil, fmt.Errorf("failed to parse image %q as reference: %w", req.Image, err)
		}
		ref.Tag = ""
		ref.Digest = ""
		req.Image = ref.CommonName()

		binary, exists := e.FunctionConfigStore.GetBinaryFromCacheByConstraint(req.Image, req.Tag)
		if !exists {
			return nil, &fn.NotFoundError{
				Function: kptfilev1.Function{Image: req.Image},
			}
		}
		selectedBinary = binary
	} else {
		klog.Infof("Image tag is empty, using the image with explicit tag: %q", req.Image)
		binary, exists := e.FunctionConfigStore.GetBinaryFromCache(req.Image)
		if !exists {
			return nil, &fn.NotFoundError{
				Function: kptfilev1.Function{Image: req.Image},
			}
		}
		selectedBinary = binary
	}

	klog.Infof("Evaluating %q in executable mode", req.Image)
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, selectedBinary) // #nosec G204 -- variables controlled internally
	cmd.Stdin = bytes.NewReader(req.ResourceList)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		klog.V(4).Infof("Resource List: %s", req.ResourceList)
		return nil, status.Errorf(codes.Internal, "Failed to execute function %q: %s (%s)", req.Image, err, stderr.String())
	}

	outbytes := stdout.Bytes()

	klog.Infof("Evaluated %q: stdout %d bytes, stderr:\n%s", req.Image, len(outbytes), stderr.String())

	// TODO: include stderr in the output?
	return &pb.EvaluateFunctionResponse{
		ResourceList: outbytes,
		Log:          stderr.Bytes(),
	}, nil
}
