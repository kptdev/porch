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
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	pb "github.com/kptdev/porch/func/evaluator"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
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

func TestEvaluatorListenGRPCNil(t *testing.T) {
	var ev *Evaluator

	lis, err := ev.ListenGRPC("127.0.0.1:0")

	require.Nil(t, lis)
	require.ErrorContains(t, err, "not initialized")
}

func TestEvaluatorListenGRPCPortInUse(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })

	ev := &Evaluator{pe: &podEvaluator{}}
	lis, err := ev.ListenGRPC(held.Addr().String())

	require.Nil(t, lis)
	require.ErrorContains(t, err, "failed to listen")
}

func TestEvaluatorServeGRPCNil(t *testing.T) {
	var ev *Evaluator
	err := ev.ServeGRPC(t.Context(), nil, 1024)
	require.ErrorContains(t, err, "not initialized")
}

func TestEvaluatorServeGRPCNilListener(t *testing.T) {
	ev := &Evaluator{pe: &podEvaluator{}}

	err := ev.ServeGRPC(t.Context(), nil, 1024)

	require.ErrorContains(t, err, "listener is required")
}

func TestEvaluatorListenAndServeGRPCRoundTrip(t *testing.T) {
	addr := startServeGRPC(t, &Evaluator{pe: &podEvaluator{}}, 1024)
	conn := dialEvaluatorGRPC(t, addr)

	waitForHealth(t, conn)
}

func TestEvaluatorServeGRPCRoundTrip(t *testing.T) {
	ev := echoEvaluator()
	addr := startServeGRPC(t, ev, 1024)
	conn := dialEvaluatorGRPC(t, addr)
	waitForHealth(t, conn)

	client := pb.NewFunctionEvaluatorClient(conn)
	resp, err := client.EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{
		Image:        "example.com/fn:v1",
		ResourceList: []byte("resources"),
	})

	require.NoError(t, err)
	require.Equal(t, []byte("resources"), resp.ResourceList)
}

func TestEvaluatorServeGRPCRejectsOversizedRequest(t *testing.T) {
	addr := startServeGRPC(t, echoEvaluator(), 1024)
	conn := dialEvaluatorGRPC(t, addr)
	waitForHealth(t, conn)

	_, err := pb.NewFunctionEvaluatorClient(conn).EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{
		Image:        "example.com/fn:v1",
		ResourceList: make([]byte, 2048),
	})

	require.Equal(t, codes.ResourceExhausted, status.Code(err))
}

func TestEvaluatorServeGRPCDefaultMessageSizeAcceptsRequest(t *testing.T) {
	addr := startServeGRPC(t, echoEvaluator(), 0)
	conn := dialEvaluatorGRPC(t, addr)
	waitForHealth(t, conn)

	payload := make([]byte, 2048)
	resp, err := pb.NewFunctionEvaluatorClient(conn).EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{
		Image:        "example.com/fn:v1",
		ResourceList: payload,
	})

	require.NoError(t, err)
	require.Equal(t, payload, resp.ResourceList)
}

func echoEvaluator() *Evaluator {
	return &Evaluator{
		pe: &podEvaluator{},
		functionEval: evalFunc(func(_ context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
			return &pb.EvaluateFunctionResponse{ResourceList: req.ResourceList}, nil
		}),
	}
}

func startServeGRPC(t *testing.T, ev *Evaluator, maxMsgSize int) string {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	lis, err := ev.ListenGRPC("127.0.0.1:0")
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() {
		errCh <- ev.ServeGRPC(ctx, lis, maxMsgSize)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Fatal("ServeGRPC did not return after context cancel")
		}
	})
	return lis.Addr().String()
}

func dialEvaluatorGRPC(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func waitForHealth(t *testing.T, conn *grpc.ClientConn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	_, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{}, grpc.WaitForReady(true))
	require.NoError(t, err)
}

type evalFunc func(context.Context, *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error)

func (f evalFunc) EvaluateFunction(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
	return f(ctx, req)
}
