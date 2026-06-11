// Copyright 2025 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package evaluator

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	"github.com/kptdev/kpt/pkg/fn/runtime"
	pb "github.com/kptdev/porch/func/proto"
	. "github.com/kptdev/porch/func/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// startFakeEvalServer starts a gRPC function evaluator server on a dynamic port.
// It returns the listener address and a cleanup function.
func startFakeEvalServer(t *testing.T, evalFunc func(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error)) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	pb.RegisterFunctionEvaluatorServer(server, &fakeFunctionEvalServer{evalFunc: evalFunc})
	//nolint:errcheck
	go server.Serve(lis)

	return lis.Addr().String(), func() {
		server.GracefulStop()
	}
}

func TestEvaluateFunction_ErrorInResponse(t *testing.T) {
	reqCh := make(chan *ConnectionRequest, 1)
	pe := &podEvaluator{requestCh: reqCh,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	go func() {
		req := <-reqCh
		req.ResponseCh <- &ConnectionResponse{
			Err: fmt.Errorf("fake pod allocation error"),
		}
	}()

	_, err := pe.EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{
		Image: "test-image",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to get the grpc client")
	assert.Contains(t, err.Error(), "fake pod allocation error")
}

func TestEvaluateFunction_NilGrpcConnection(t *testing.T) {
	reqCh := make(chan *ConnectionRequest, 1)
	pe := &podEvaluator{requestCh: reqCh,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	go func() {
		req := <-reqCh
		req.ResponseCh <- &ConnectionResponse{
			PodData: PodData{GrpcConnection: nil},
		}
	}()

	_, err := pe.EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{
		Image: "test-image",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to get the grpc client")
}

func TestEvaluateFunction_GrpcCallFails(t *testing.T) {
	addr, cleanup := startFakeEvalServer(t, func(_ context.Context, _ *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
		return nil, fmt.Errorf("fake grpc eval error")
	})
	defer cleanup()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	counter := &atomic.Int32{}
	counter.Store(1)

	reqCh := make(chan *ConnectionRequest, 1)
	pe := &podEvaluator{requestCh: reqCh,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	go func() {
		req := <-reqCh
		req.ResponseCh <- &ConnectionResponse{
			PodData:               PodData{Image: "test-image", GrpcConnection: conn},
			ConcurrentEvaluations: counter,
		}
	}()

	_, err = pe.EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{
		Image: "test-image",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to evaluate")
}

func TestEvaluateFunction_SuccessWithStderr(t *testing.T) {
	addr, cleanup := startFakeEvalServer(t, func(_ context.Context, _ *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
		return &pb.EvaluateFunctionResponse{
			ResourceList: []byte("result-data"),
			Log:          []byte("some stderr output"),
		}, nil
	})
	defer cleanup()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	counter := &atomic.Int32{}
	counter.Store(1)

	reqCh := make(chan *ConnectionRequest, 1)
	pe := &podEvaluator{requestCh: reqCh,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	go func() {
		req := <-reqCh
		req.ResponseCh <- &ConnectionResponse{
			PodData:               PodData{Image: "test-image", GrpcConnection: conn},
			ConcurrentEvaluations: counter,
		}
	}()

	resp, err := pe.EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{
		Image: "test-image",
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("result-data"), resp.ResourceList)
	assert.Equal(t, []byte("some stderr output"), resp.Log)
}

func TestEvaluateFunction_SuccessClean(t *testing.T) {
	addr, cleanup := startFakeEvalServer(t, func(_ context.Context, _ *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
		return &pb.EvaluateFunctionResponse{
			ResourceList: []byte("clean-result"),
		}, nil
	})
	defer cleanup()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	counter := &atomic.Int32{}
	counter.Store(1)

	reqCh := make(chan *ConnectionRequest, 1)
	pe := &podEvaluator{requestCh: reqCh,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	go func() {
		req := <-reqCh
		req.ResponseCh <- &ConnectionResponse{
			PodData:               PodData{Image: "test-image", GrpcConnection: conn},
			ConcurrentEvaluations: counter,
		}
	}()

	resp, err := pe.EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{
		Image: "test-image",
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("clean-result"), resp.ResourceList)
	assert.Empty(t, resp.Log)
}

func TestEvaluateFunction_CounterDecrement(t *testing.T) {
	addr, cleanup := startFakeEvalServer(t, func(_ context.Context, _ *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
		return &pb.EvaluateFunctionResponse{ResourceList: []byte("ok")}, nil
	})
	defer cleanup()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	counter := &atomic.Int32{}
	counter.Store(1)

	reqCh := make(chan *ConnectionRequest, 1)
	pe := &podEvaluator{requestCh: reqCh,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	go func() {
		req := <-reqCh
		req.ResponseCh <- &ConnectionResponse{
			PodData:               PodData{Image: "test-image", GrpcConnection: conn},
			ConcurrentEvaluations: counter,
		}
	}()

	_, err = pe.EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{
		Image: "test-image",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(0), counter.Load(), "concurrentEvaluations should be decremented back to 0")
}
