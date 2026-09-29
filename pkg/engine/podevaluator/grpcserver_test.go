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
	"net"
	"testing"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	pb "github.com/kptdev/porch/func/evaluator"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestFunctionEvaluatorServerSuccess(t *testing.T) {
	srv := &functionEvaluatorServer{
		eval: evalFunc(func(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
			return &pb.EvaluateFunctionResponse{ResourceList: []byte("ok")}, nil
		}),
	}

	resp, err := srv.EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{Image: "example.com/fn:v1"})

	require.NoError(t, err)
	require.Equal(t, []byte("ok"), resp.ResourceList)
}

func TestFunctionEvaluatorServerNotFound(t *testing.T) {
	srv := &functionEvaluatorServer{
		eval: evalFunc(func(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
			return nil, &fn.NotFoundError{Function: kptfilev1.Function{Image: req.Image}}
		}),
	}

	_, err := srv.EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{Image: "missing:v1"})

	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestFunctionEvaluatorServerNilEval(t *testing.T) {
	srv := &functionEvaluatorServer{}

	_, err := srv.EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{Image: "example.com/fn:v1"})

	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestEvaluatorServeGRPCNil(t *testing.T) {
	var ev *Evaluator
	err := ev.ServeGRPC(t.Context(), "127.0.0.1:0", 1024)
	require.ErrorContains(t, err, "not initialized")
}

func TestEvaluatorServeGRPCRoundTrip(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	pb.RegisterFunctionEvaluatorServer(server, &functionEvaluatorServer{
		eval: evalFunc(func(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
			return &pb.EvaluateFunctionResponse{ResourceList: req.ResourceList}, nil
		}),
	})
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewFunctionEvaluatorClient(conn)
	resp, err := client.EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{
		Image:        "example.com/fn:v1",
		ResourceList: []byte("resources"),
	})

	require.NoError(t, err)
	require.Equal(t, []byte("resources"), resp.ResourceList)
}

type evalFunc func(context.Context, *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error)

func (f evalFunc) EvaluateFunction(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
	return f(ctx, req)
}
