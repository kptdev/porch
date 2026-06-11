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

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/kptops"
	"github.com/kptdev/porch/controllers/functionconfigs"
	"github.com/kptdev/porch/func/proto"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/klog/v2"
)

type GRPCRuntimeOptions struct {
	FunctionRunnerAddress string
	MaxGrpcMessageSize    int
	DefaultImagePrefix    string
}

type grpcRuntime struct {
	cc     *grpc.ClientConn
	client proto.FunctionEvaluatorClient
}

func newGRPCFunctionRuntime(options GRPCRuntimeOptions) (*grpcRuntime, error) {
	if options.FunctionRunnerAddress == "" {
		return nil, fmt.Errorf("address is required to instantiate gRPC function runtime")
	}

	klog.Infof("Dialing grpc function runner %q", options.FunctionRunnerAddress)

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
		cc:     cc,
		client: proto.NewFunctionEvaluatorClient(cc),
	}, err
}

var _ kptops.FunctionRuntime = &grpcRuntime{}

func (gr *grpcRuntime) GetRunner(ctx context.Context, fn *kptfilev1.Function) (fn.FunctionRunner, error) {
	// TODO: Check if the function is actually available?
	return &grpcRunner{
		ctx:    ctx,
		client: gr.client,
		image:  fn.Image,
		tag:    fn.Tag,
	}, nil
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
	ctx    context.Context
	client proto.FunctionEvaluatorClient
	image  string
	tag    string
}

var _ fn.FunctionRunner = &grpcRunner{}

func (gr *grpcRunner) Run(r io.Reader, w io.Writer) error {
	in, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read function runner input: %w", err)
	}

	res, err := gr.client.EvaluateFunction(gr.ctx, &proto.EvaluateFunctionRequest{
		ResourceList: in,
		Image:        gr.image,
		Tag:          gr.tag,
	})
	if err != nil {
		return fmt.Errorf("func eval %q failed: %w", gr.image, err)
	}
	if _, err := w.Write(res.ResourceList); err != nil {
		return fmt.Errorf("failed to write function runner output: %w", err)
	}
	return nil
}

// NewMultiFunctionRuntime creates a FunctionRuntime that tries builtin functions
// first, then falls back to the gRPC fn-runner.
func NewMultiFunctionRuntime(grpcAddress string, maxGrpcMessageSize int, functionConfigStore *functionconfigs.FunctionConfigStore) (fn.FunctionRuntime, error) {
	builtin := newBuiltinRuntime(functionConfigStore)

	if grpcAddress == "" {
		return builtin, nil
	}

	grpc, err := newGRPCFunctionRuntime(GRPCRuntimeOptions{
		FunctionRunnerAddress: grpcAddress,
		MaxGrpcMessageSize:    maxGrpcMessageSize,
	})
	if err != nil {
		return nil, err
	}

	return fn.NewMultiRuntime([]fn.FunctionRuntime{builtin, grpc}), nil
}
