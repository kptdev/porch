// Copyright 2022, 2026 The kpt and Nephio Authors
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
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	v1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/nephio-project/porch/controllers/functionconfigs/reconciler"
	"github.com/nephio-project/porch/func/evaluator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testImage = "test-image:latest"

func TestNewGRPCFunctionRuntimeSuccess(t *testing.T) {
	addr, stop := startMockServer(t)
	defer stop()

	options := GRPCRuntimeOptions{
		FunctionRunnerAddress: addr,
		MaxGrpcMessageSize:    1024,
		DefaultImagePrefix:    "gcr.io/",
	}

	runtime, err := newGRPCFunctionRuntime(options)
	require.NoError(t, err)
	require.NotNil(t, runtime)
	assert.NotNil(t, runtime.cc)
	assert.NotNil(t, runtime.client)
	runtime.Close()
}

func TestNewGRPCFunctionRuntimeEmptyAddress(t *testing.T) {
	options := GRPCRuntimeOptions{
		MaxGrpcMessageSize: 1024,
	}
	runtime, err := newGRPCFunctionRuntime(options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "address is required")
	if runtime != nil {
		runtime.Close()
	}
}

func TestGRPCRuntimeGetRunner(t *testing.T) {
	addr, stop := startMockServer(t)
	defer stop()

	options := GRPCRuntimeOptions{
		FunctionRunnerAddress: addr,
		MaxGrpcMessageSize:    1024,
	}

	runtime, err := newGRPCFunctionRuntime(options)
	require.NoError(t, err)
	defer runtime.Close()

	fn := &v1.Function{
		Image: testImage,
	}

	runner, err := runtime.GetRunner(t.Context(), fn)
	require.NoError(t, err)
	require.NotNil(t, runner)

	grpcRunner, ok := runner.(*grpcRunner)
	require.True(t, ok)
	assert.Equal(t, fn.Image, grpcRunner.image)
	assert.NotNil(t, grpcRunner.ctx)
	assert.NotNil(t, grpcRunner.client)
}

func TestGRPCRuntimeCloseWithConnection(t *testing.T) {
	addr, stop := startMockServer(t)
	defer stop()

	options := GRPCRuntimeOptions{
		FunctionRunnerAddress: addr,
		MaxGrpcMessageSize:    1024,
	}

	runtime, err := newGRPCFunctionRuntime(options)
	require.NoError(t, err)

	err = runtime.Close()
	assert.NoError(t, err)
	assert.Nil(t, runtime.cc)
}

func TestGRPCRuntimeCloseMultipleCalls(t *testing.T) {
	addr, stop := startMockServer(t)
	defer stop()

	options := GRPCRuntimeOptions{
		FunctionRunnerAddress: addr,
		MaxGrpcMessageSize:    1024,
	}

	runtime, err := newGRPCFunctionRuntime(options)
	require.NoError(t, err)

	err = runtime.Close()
	assert.NoError(t, err)
	assert.Nil(t, runtime.cc)

	err = runtime.Close()
	assert.NoError(t, err)
}

func TestGRPCRunnerRunSuccess(t *testing.T) {
	client := &mockClient{
		evaluateFunc: func(ctx context.Context, req *evaluator.EvaluateFunctionRequest) (*evaluator.EvaluateFunctionResponse, error) {
			return &evaluator.EvaluateFunctionResponse{
				ResourceList: []byte(`apiVersion: config.kubernetes.io/v1alpha1
kind: ResourceList
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-cm
      namespace: test-ns
    data:
      foo: bar
`),
			}, nil
		},
	}
	runner := &grpcRunner{
		ctx:    t.Context(),
		client: client,
		image:  testImage,
	}

	reader := strings.NewReader(`apiVersion: config.kubernetes.io/v1alpha1
kind: ResourceList
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-cm
      namespace: old
    data:
      foo: bar
functionConfig:
  apiVersion: v1
  kind: ConfigMap
  data:
    namespace: test-ns
`)
	var writer bytes.Buffer

	err := runner.Run(reader, &writer)
	require.NoError(t, err)
	assert.Contains(t, writer.String(), "test-ns")
}

func TestGRPCRunnerRunEvaluationError(t *testing.T) {
	client := &mockClient{
		evaluateFunc: func(ctx context.Context, req *evaluator.EvaluateFunctionRequest) (*evaluator.EvaluateFunctionResponse, error) {
			return nil, status.Error(codes.Internal, "evaluation failed")
		},
	}

	runner := &grpcRunner{
		ctx:    t.Context(),
		client: client,
		image:  testImage,
	}

	reader := strings.NewReader(`apiVersion: config.kubernetes.io/v1alpha1
kind: ResourceList
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-cm
      namespace: old
    data:
      foo: bar
`)
	var writer bytes.Buffer

	err := runner.Run(reader, &writer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "func eval")
}

func TestGRPCRunnerRunReadError(t *testing.T) {
	runner := &grpcRunner{
		ctx:    t.Context(),
		client: nil,
		image:  testImage,
	}

	reader := &errorReader{}
	var writer bytes.Buffer

	err := runner.Run(reader, &writer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read function runner input")
}

func TestGRPCRunnerRunWriteError(t *testing.T) {
	client := &mockClient{
		evaluateFunc: func(ctx context.Context, req *evaluator.EvaluateFunctionRequest) (*evaluator.EvaluateFunctionResponse, error) {
			return &evaluator.EvaluateFunctionResponse{
				ResourceList: []byte(`apiVersion: config.kubernetes.io/v1alpha1
kind: ResourceList
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-cm
      namespace: test-ns
    data:
      foo: bar
`),
			}, nil
		},
	}

	runner := &grpcRunner{
		ctx:    t.Context(),
		client: client,
		image:  testImage,
	}

	reader := strings.NewReader(`apiVersion: config.kubernetes.io/v1alpha1
kind: ResourceList
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: my-cm
      namespace: old
    data:
      foo: bar
`)
	writer := &errorWriter{}

	err := runner.Run(reader, writer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write function runner output")
}

type mockClient struct {
	evaluateFunc func(context.Context, *evaluator.EvaluateFunctionRequest) (*evaluator.EvaluateFunctionResponse, error)
}

func (m *mockClient) EvaluateFunction(ctx context.Context, req *evaluator.EvaluateFunctionRequest, opts ...grpc.CallOption) (*evaluator.EvaluateFunctionResponse, error) {
	return m.evaluateFunc(ctx, req)
}

type mockFunctionEvaluator struct {
	evaluator.UnimplementedFunctionEvaluatorServer
	evaluateFunc func(context.Context, *evaluator.EvaluateFunctionRequest) (*evaluator.EvaluateFunctionResponse, error)
}

func (m *mockFunctionEvaluator) EvaluateFunction(ctx context.Context, req *evaluator.EvaluateFunctionRequest) (*evaluator.EvaluateFunctionResponse, error) {
	return m.evaluateFunc(ctx, req)
}

func startMockServer(t *testing.T) (string, func()) {
	mock := &mockFunctionEvaluator{}
	server := grpc.NewServer()
	evaluator.RegisterFunctionEvaluatorServer(server, mock)
	lis, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	go func() {
		server.Serve(lis)
	}()
	return lis.Addr().String(), server.Stop
}

type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("read error")
}

type errorWriter struct{}

func (e *errorWriter) Write(p []byte) (n int, err error) {
	return 0, errors.New("write error")
}

func TestNewMultiFunctionRuntime_BuiltinOnly(t *testing.T) {
	store := newTestFunctionConfigStore()
	runtime, err := NewMultiFunctionRuntime("", 1024, store)
	require.NoError(t, err)
	require.NotNil(t, runtime)

	// Should be a builtinRuntime (no gRPC fallback)
	_, ok := runtime.(*builtinRuntime)
	assert.True(t, ok, "expected builtinRuntime when no gRPC address is provided")
}

func TestNewMultiFunctionRuntime_WithGRPC(t *testing.T) {
	addr, stop := startMockServer(t)
	defer stop()

	store := newTestFunctionConfigStore()
	runtime, err := NewMultiFunctionRuntime(addr, 1024, store)
	require.NoError(t, err)
	require.NotNil(t, runtime)

	// Should be a MultiRuntime wrapping builtin + gRPC
	_, ok := runtime.(*builtinRuntime)
	assert.False(t, ok, "expected MultiRuntime when gRPC address is provided")
}

func TestNewMultiFunctionRuntime_NilStorePanics(t *testing.T) {
	runtime, err := NewMultiFunctionRuntime("", 1024, nil)
	require.NoError(t, err)

	// Panic occurs on lookup, not construction
	assert.Panics(t, func() {
		_, _ = runtime.GetRunner(t.Context(), &v1.Function{Image: "test:v1"})
	})
}

func newTestFunctionConfigStore() *reconciler.FunctionConfigStore {
	return reconciler.NewFunctionConfigStore("", "")
}
