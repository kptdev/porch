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

package internal

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kptdev/kpt/pkg/fn/runtime"
	pb "github.com/kptdev/porch/func/evaluator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	reqCh := make(chan *connectionRequest, 1)
	pe := &podEvaluator{requestCh: reqCh,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	go func() {
		req := <-reqCh
		req.responseCh <- &connectionResponse{
			err: fmt.Errorf("fake pod allocation error"),
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
	reqCh := make(chan *connectionRequest, 1)
	pe := &podEvaluator{requestCh: reqCh,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	go func() {
		req := <-reqCh
		req.responseCh <- &connectionResponse{
			podData: podData{grpcConnection: nil},
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

	reqCh := make(chan *connectionRequest, 1)
	pe := &podEvaluator{requestCh: reqCh,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	go func() {
		req := <-reqCh
		req.responseCh <- &connectionResponse{
			podData:               podData{image: "test-image", grpcConnection: conn},
			concurrentEvaluations: counter,
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

	reqCh := make(chan *connectionRequest, 1)
	pe := &podEvaluator{requestCh: reqCh,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	go func() {
		req := <-reqCh
		req.responseCh <- &connectionResponse{
			podData:               podData{image: "test-image", grpcConnection: conn},
			concurrentEvaluations: counter,
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

	reqCh := make(chan *connectionRequest, 1)
	pe := &podEvaluator{requestCh: reqCh,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	go func() {
		req := <-reqCh
		req.responseCh <- &connectionResponse{
			podData:               podData{image: "test-image", grpcConnection: conn},
			concurrentEvaluations: counter,
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

	reqCh := make(chan *connectionRequest, 1)
	pe := &podEvaluator{requestCh: reqCh,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	go func() {
		req := <-reqCh
		req.responseCh <- &connectionResponse{
			podData:               podData{image: "test-image", grpcConnection: conn},
			concurrentEvaluations: counter,
		}
	}()

	_, err = pe.EvaluateFunction(t.Context(), &pb.EvaluateFunctionRequest{
		Image: "test-image",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(0), counter.Load(), "concurrentEvaluations should be decremented back to 0")
}

func TestEvaluateFunction_Unavailable_EvictsAndRetries(t *testing.T) {
	// First server returns Unavailable (simulates dead pod)
	unavailableAddr, unavailableCleanup := startFakeEvalServer(t, func(_ context.Context, _ *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
		return nil, status.Error(codes.Unavailable, "connection refused")
	})
	defer unavailableCleanup()

	unavailableConn, err := grpc.NewClient(unavailableAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer unavailableConn.Close()

	// Second server returns success (healthy pod)
	healthyAddr, healthyCleanup := startFakeEvalServer(t, func(_ context.Context, _ *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
		return &pb.EvaluateFunctionResponse{ResourceList: []byte("success")}, nil
	})
	defer healthyCleanup()

	healthyConn, err := grpc.NewClient(healthyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer healthyConn.Close()

	deadPodKey := client.ObjectKey{Namespace: "fn-ns", Name: "dead-pod"}
	healthyPodKey := client.ObjectKey{Namespace: "fn-ns", Name: "healthy-pod"}

	reqCh := make(chan *connectionRequest, 2)
	evictCh := make(chan *podEvictionRequest, 1)

	pe := &podEvaluator{
		requestCh:      reqCh,
		evictionCh:     evictCh,
		maxGrpcRetries: 2,
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	deadCounter := &atomic.Int32{}
	deadCounter.Store(1)
	healthyCounter := &atomic.Int32{}
	healthyCounter.Store(1)

	// Serve two requests: first returns dead pod, second returns healthy pod.
	// Between them, drain the eviction and ack it so the retry proceeds.
	capturedEvictionCh := make(chan *podEvictionRequest, 1)
	go func() {
		req1 := <-reqCh
		req1.responseCh <- &connectionResponse{
			podData:               podData{image: "test-image", grpcConnection: unavailableConn, podKey: &deadPodKey, serviceKey: &deadPodKey},
			concurrentEvaluations: deadCounter,
		}
		eviction := <-evictCh
		capturedEvictionCh <- eviction
		close(eviction.doneCh)
		req2 := <-reqCh
		req2.responseCh <- &connectionResponse{
			podData:               podData{image: "test-image", grpcConnection: healthyConn, podKey: &healthyPodKey, serviceKey: &healthyPodKey},
			concurrentEvaluations: healthyCounter,
		}
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	resp, err := pe.EvaluateFunction(ctx, &pb.EvaluateFunctionRequest{
		Image: "test-image",
	})

	// Should succeed on retry
	require.NoError(t, err)
	assert.Equal(t, []byte("success"), resp.ResourceList)
	var capturedEviction *podEvictionRequest
	select {
	case capturedEviction = <-capturedEvictionCh:
	case <-ctx.Done():
	}
	// Verify eviction was sent for the dead pod
	require.NotNil(t, capturedEviction, "expected eviction request but none was sent")
	assert.Equal(t, "test-image", capturedEviction.image)
	assert.Equal(t, deadPodKey, capturedEviction.podKey)
}

func TestEvaluateFunction_ExhaustsRetries(t *testing.T) {
	// With maxGrpcRetries=1, the function should fail after 2 attempts (initial + 1 retry)
	// when all pods return Unavailable.

	// Server that always returns Unavailable
	unavailableAddr, unavailableCleanup := startFakeEvalServer(t, func(_ context.Context, _ *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
		return nil, status.Error(codes.Unavailable, "connection refused")
	})
	defer unavailableCleanup()

	unavailableConn, err := grpc.NewClient(unavailableAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer unavailableConn.Close()

	deadPodKey1 := client.ObjectKey{Namespace: "fn-ns", Name: "dead-pod-1"}
	deadPodKey2 := client.ObjectKey{Namespace: "fn-ns", Name: "dead-pod-2"}

	reqCh := make(chan *connectionRequest, 2)
	evictCh := make(chan *podEvictionRequest, 2)

	pe := &podEvaluator{
		requestCh:      reqCh,
		evictionCh:     evictCh,
		maxGrpcRetries: 1, // only 1 retry allowed
		podCacheManager: &podCacheManager{
			podManager: &podManager{
				tagResolver: runtime.TagResolver{},
			},
		},
	}

	counter1 := &atomic.Int32{}
	counter1.Store(1)
	counter2 := &atomic.Int32{}
	counter2.Store(1)

	// Serve exactly 2 requests (initial + 1 retry), both return dead pods.
	// Drain evictions and ack them.
	go func() {
		req1 := <-reqCh
		req1.responseCh <- &connectionResponse{
			podData:               podData{image: "test-image", grpcConnection: unavailableConn, podKey: &deadPodKey1, serviceKey: &deadPodKey1},
			concurrentEvaluations: counter1,
		}
		eviction1 := <-evictCh
		close(eviction1.doneCh)

		req2 := <-reqCh
		req2.responseCh <- &connectionResponse{
			podData:               podData{image: "test-image", grpcConnection: unavailableConn, podKey: &deadPodKey2, serviceKey: &deadPodKey2},
			concurrentEvaluations: counter2,
		}
		eviction2 := <-evictCh
		close(eviction2.doneCh)
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	resp, err := pe.EvaluateFunction(ctx, &pb.EvaluateFunctionRequest{
		Image: "test-image",
	})

	// Should fail after exhausting retries
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "after retries")
}
