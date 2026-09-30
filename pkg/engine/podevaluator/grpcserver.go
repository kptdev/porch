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

package podevaluator

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/kptdev/kpt/pkg/fn"
	fnconf "github.com/kptdev/porch/controllers/functionconfigs"
	"github.com/kptdev/porch/func/evaluator"
	"github.com/kptdev/porch/func/healthchecker"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const defaultMaxGRPCMessageSize = 6 * 1024 * 1024

// functionEvaluator is the EvaluateFunction implementation ServeGRPC registers.
type functionEvaluator interface {
	EvaluateFunction(context.Context, *evaluator.EvaluateFunctionRequest) (*evaluator.EvaluateFunctionResponse, error)
}

// Evaluator is the in-process pod evaluator plus its FunctionEvaluator gRPC front-end.
// porch-server uses one instance for Engine renders and for the PackageRevision controller.
type Evaluator struct {
	pe *podEvaluator
	// functionEval, when set, is registered instead of pe so tests can exercise
	// ServeGRPC without a full pod evaluator.
	functionEval functionEvaluator
}

// NewEvaluator creates the pod evaluator used by porch-server.
func NewEvaluator(ctx context.Context, o PodEvaluatorOptions, cl client.WithWatch, functionConfigStore *fnconf.FunctionConfigStore) (*Evaluator, error) {
	pe, err := NewPodEvaluator(ctx, o, cl, functionConfigStore)
	if err != nil {
		return nil, err
	}
	return &Evaluator{pe: pe}, nil
}

// Runtime returns the Engine FunctionRuntime backed by this evaluator.
func (e *Evaluator) Runtime() fn.FunctionRuntime {
	if e == nil || e.pe == nil {
		return &podEvaluatorRuntime{}
	}
	return &podEvaluatorRuntime{pe: e.pe}
}

// ListenGRPC binds the FunctionEvaluator TCP address. Bind here so port
// collisions fail Engine startup instead of being logged from a goroutine.
func (e *Evaluator) ListenGRPC(addr string) (net.Listener, error) {
	if e == nil || e.pe == nil {
		return nil, fmt.Errorf("pod evaluator is not initialized")
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	return lis, nil
}

// ServeGRPC exposes the same FunctionEvaluator service Function Runner used to
// serve. The controller sends EvaluateFunction here (image + ResourceList, no
// exec_path) the way it previously sent requests to Function Runner.
// lis must already be bound (see ListenGRPC).
func (e *Evaluator) ServeGRPC(ctx context.Context, lis net.Listener, maxMsgSize int) error {
	if e == nil || e.pe == nil {
		return fmt.Errorf("pod evaluator is not initialized")
	}
	if lis == nil {
		return fmt.Errorf("listener is required")
	}
	if maxMsgSize <= 0 {
		maxMsgSize = defaultMaxGRPCMessageSize
	}
	go func() {
		<-ctx.Done()
		_ = lis.Close()
	}()

	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxMsgSize),
		grpc.MaxSendMsgSize(maxMsgSize),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)
	go func() {
		<-ctx.Done()
		server.Stop()
	}()

	evaluator.RegisterFunctionEvaluatorServer(server, &functionEvaluatorServer{eval: e.registeredEval()})
	grpc_health_v1.RegisterHealthServer(server, healthchecker.NewHealthChecker())
	klog.Infof("pod evaluator gRPC listening on %s", lis.Addr())
	if err := server.Serve(lis); err != nil {
		return fmt.Errorf("pod evaluator gRPC server failed: %w", err)
	}
	return nil
}

func (e *Evaluator) registeredEval() functionEvaluator {
	if e.functionEval != nil {
		return e.functionEval
	}
	return e.pe
}

type functionEvaluatorServer struct {
	evaluator.UnimplementedFunctionEvaluatorServer
	eval functionEvaluator
}

func (s *functionEvaluatorServer) EvaluateFunction(ctx context.Context, req *evaluator.EvaluateFunctionRequest) (*evaluator.EvaluateFunctionResponse, error) {
	if s.eval == nil {
		return nil, status.Error(codes.Internal, "pod evaluator is not initialized")
	}
	resp, err := s.eval.EvaluateFunction(ctx, req)
	if err != nil {
		var notFound *fn.NotFoundError
		if errors.As(err, &notFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, err
	}
	return resp, nil
}
