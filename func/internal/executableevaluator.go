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
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	pb "github.com/kptdev/porch/func/evaluator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

type executableEvaluator struct {
	functionCacheDir string
}

var _ Evaluator = &executableEvaluator{}

func NewExecutableEvaluator(functionCacheDir string) (Evaluator, error) {
	return &executableEvaluator{functionCacheDir: functionCacheDir}, nil
}

func (e *executableEvaluator) EvaluateFunction(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
	if req.ExecPath == "" {
		return nil, &fn.NotFoundError{
			Function: kptfilev1.Function{Image: req.Image},
		}
	}

	execPath := filepath.Clean(req.ExecPath)
	base := filepath.Clean(e.functionCacheDir) + string(os.PathSeparator)
	if !strings.HasPrefix(execPath, base) {
		return nil, fmt.Errorf("exec_path %q is outside functions dir", req.ExecPath)
	}

	klog.Infof("Evaluating %q in executable mode", req.Image)
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, execPath) // #nosec G204 -- variables controlled internally
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

func (e *executableEvaluator) Name() string {
	return "exec"
}
