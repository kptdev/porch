// Copyright 2022, 2025-2026 The kpt Authors
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
	"context"
	"fmt"
	"io"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/kptops"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/porch/controllers/functionconfigs"
	"github.com/kptdev/porch/func/evaluator"
	"github.com/kptdev/porch/pkg/engine/podevaluator"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ExecutableEvaluatorOptions struct {
	FunctionCacheDir string
}

type GRPCRuntimeOptions struct {
	FunctionRunnerAddress string
	MaxGrpcMessageSize    int
	DefaultImagePrefix    string
	// SkipExecPath restores Function Runner-style client behavior: GetRunner
	// always returns a runner and EvaluateFunction is sent without exec_path.
	// Use this for porch-server's pod-evaluator gRPC service.
	SkipExecPath bool
}

type grpcRuntime struct {
	cc                  *grpc.ClientConn
	client              evaluator.FunctionEvaluatorClient
	functionConfigStore *functionconfigs.FunctionConfigStore
	skipExecPath        bool
}

func (gr *grpcRuntime) getExecutablePath(fn *kptfilev1.Function) (string, bool) {
	if fn.Tag != "" {
		execPath, _, exists := gr.functionConfigStore.GetBinaryFromCacheByConstraint(fn.Image, fn.Tag)
		return execPath, exists
	}
	klog.V(2).Infof("Image tag is empty, using the image with explicit tag: %q", fn.Image)
	return gr.functionConfigStore.GetBinaryFromCache(fn.Image)
}

func newGRPCFunctionRuntime(options GRPCRuntimeOptions, functionConfigStore *functionconfigs.FunctionConfigStore) (*grpcRuntime, error) {
	if options.FunctionRunnerAddress == "" {
		return nil, fmt.Errorf("address is required to instantiate gRPC function runtime")
	}

	klog.Infof("Dialing grpc function evaluator %q (skipExecPath=%v)", options.FunctionRunnerAddress, options.SkipExecPath)

	cc, err := grpc.NewClient(options.FunctionRunnerAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(options.MaxGrpcMessageSize),
			grpc.MaxCallSendMsgSize(options.MaxGrpcMessageSize),
		),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial grpc function evaluator: %w", err)
	}

	return &grpcRuntime{
		cc:                  cc,
		client:              evaluator.NewFunctionEvaluatorClient(cc),
		functionConfigStore: functionConfigStore,
		skipExecPath:        options.SkipExecPath,
	}, err
}

var _ kptops.FunctionRuntime = &grpcRuntime{}

func (gr *grpcRuntime) GetRunner(ctx context.Context, function *kptfilev1.Function) (fn.FunctionRunner, error) {
	klog.Infof("[grpcRuntime::GetRunner] Current state of client connection: %s", gr.cc.GetState().String())

	runner := &grpcRunner{
		ctx:    ctx,
		client: gr.client,
		image:  function.Image,
		tag:    function.Tag,
	}
	if gr.skipExecPath {
		return runner, nil
	}
	if execPath, exists := gr.getExecutablePath(function); exists {
		runner.execPath = execPath
		return runner, nil
	}
	return nil, &fn.NotFoundError{
		Function: *function,
	}
}

func (gr *grpcRuntime) Close() error {
	var err error
	if gr.cc != nil {
		if err = gr.cc.Close(); err != nil {
			klog.Warningf("Failed to close grpc client connection: %v", err)
		}
		gr.cc = nil
	}
	return err
}

type grpcRunner struct {
	ctx      context.Context
	client   evaluator.FunctionEvaluatorClient
	image    string
	tag      string
	execPath string
}

var _ fn.FunctionRunner = &grpcRunner{}

func (gr *grpcRunner) Run(r io.Reader, w io.Writer) error {
	in, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read function runner input: %w", err)
	}

	res, err := gr.client.EvaluateFunction(gr.ctx, &evaluator.EvaluateFunctionRequest{
		ResourceList: in,
		Image:        gr.image,
		Tag:          gr.tag,
		ExecPath:     gr.execPath,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return &fn.NotFoundError{
				Function: kptfilev1.Function{Image: gr.image, Tag: gr.tag},
			}
		}
		return fmt.Errorf("func eval %q failed: %w", gr.image, err)
	}
	if _, err := w.Write(res.ResourceList); err != nil {
		return fmt.Errorf("failed to write function runner output: %w", err)
	}
	return nil
}

// MultiFunctionRuntimeOptions configures the function runtime chain: builtin,
// optional gRPC fn-runner (exec), optional porch-server pod-evaluator gRPC,
// and optional in-process pod evaluator.
type MultiFunctionRuntimeOptions struct {
	GRPCAddress         string
	PodEvaluatorAddress string
	MaxGrpcMessageSize  int
	FunctionConfigStore *functionconfigs.FunctionConfigStore
	PodEvaluator        *podevaluator.PodEvaluatorOptions
	KubeClient          client.WithWatch
	DefaultImagePrefix  string
}

// NewMultiFunctionRuntime creates a FunctionRuntime that tries builtin functions
// first, then gRPC fn-runner (exec), then porch-server's pod evaluator over gRPC,
// then an in-process pod evaluator when configured.
func NewMultiFunctionRuntime(ctx context.Context, opts MultiFunctionRuntimeOptions) (fn.FunctionRuntime, error) {
	runtimes := []fn.FunctionRuntime{newBuiltinRuntime(opts.FunctionConfigStore)}

	if opts.GRPCAddress != "" {
		grpc, err := newGRPCFunctionRuntime(GRPCRuntimeOptions{
			FunctionRunnerAddress: opts.GRPCAddress,
			MaxGrpcMessageSize:    opts.MaxGrpcMessageSize,
		}, opts.FunctionConfigStore)
		if err != nil {
			return nil, err
		}
		runtimes = append(runtimes, grpc)
	}

	if opts.PodEvaluatorAddress != "" {
		podGRPC, err := newGRPCFunctionRuntime(GRPCRuntimeOptions{
			FunctionRunnerAddress: opts.PodEvaluatorAddress,
			MaxGrpcMessageSize:    opts.MaxGrpcMessageSize,
			SkipExecPath:          true,
		}, opts.FunctionConfigStore)
		if err != nil {
			return nil, err
		}
		runtimes = append(runtimes, podGRPC)
	}

	if opts.PodEvaluator != nil && opts.PodEvaluator.WrapperServerImage != "" {
		if opts.KubeClient == nil {
			return nil, fmt.Errorf("kube client is required for pod evaluator runtime")
		}
		podOpts := *opts.PodEvaluator
		if podOpts.DefaultImagePrefix == "" {
			podOpts.DefaultImagePrefix = opts.DefaultImagePrefix
			if podOpts.DefaultImagePrefix == "" {
				podOpts.DefaultImagePrefix = runneroptions.GHCRImagePrefix
			}
		}
		if podOpts.MaxGrpcMessageSize == 0 {
			podOpts.MaxGrpcMessageSize = opts.MaxGrpcMessageSize
		}
		runtimes = append(runtimes, podevaluator.NewPodEvaluatorRuntime(ctx, podOpts, opts.KubeClient, opts.FunctionConfigStore))
	}

	if len(runtimes) == 1 {
		return runtimes[0], nil
	}
	return fn.NewMultiRuntime(runtimes), nil
}
