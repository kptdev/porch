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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/kptdev/krm-functions-sdk/go/fn"
	pb "github.com/kptdev/porch/func/evaluator"
	"github.com/kptdev/porch/func/healthchecker"
	"github.com/kptdev/porch/internal/telemetry"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	contextsignal "k8s.io/apiserver/pkg/server"
	"k8s.io/klog/v2"
)

var tracer = otel.Tracer("wrapper-server")

func main() {
	op := &options{}
	cmd := &cobra.Command{
		Use:   "wrapper-server",
		Short: "wrapper-server is a gRPC server that fronts a KRM function",
		RunE: func(cmd *cobra.Command, args []string) error {
			argsLenAtDash := cmd.ArgsLenAtDash()
			if argsLenAtDash > -1 {
				op.entrypoint = args[argsLenAtDash:]
			}
			return op.run()
		},
	}
	cmd.Flags().IntVar(&op.port, "port", 9446, "The server port")
	cmd.Flags().IntVar(&op.maxGrpcMessageSize, "max-request-body-size", 6*1024*1024, "Maximum size of grpc messages in bytes.")
	cmd.Flags().IntVar(&op.logLevel, "verbosity", 2, "Verbosity of logs.")

	flagSet := flag.NewFlagSet("log-level", flag.ContinueOnError)
	klog.InitFlags(flagSet)
	_ = flagSet.Parse([]string{"--v", strconv.Itoa(op.logLevel)})

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
}

type options struct {
	port               int
	maxGrpcMessageSize int
	entrypoint         []string
	logLevel           int
}

func (o *options) run() error {
	ctx := contextsignal.SetupSignalContext()
	otelResources, err := telemetry.SetupOpenTelemetry(ctx)
	if err != nil {
		contextsignal.RequestShutdown()
		klog.Errorf("%v\n", err)
		return err
	}
	defer func() {
		if err := otelResources.ShutdownWithTimeout(10 * time.Second); err != nil {
			klog.Warningf("failed to gracefully shutdown OpenTelemetry: %v", err)
		}
	}()
	klog.Info("OpenTelemetry initialized")
	address := fmt.Sprintf(":%d", o.port)
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	evaluator := &singleFunctionEvaluator{
		entrypoint: o.entrypoint,
	}

	klog.Infof("Listening on %s", address)

	// Start the gRPC server
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(o.maxGrpcMessageSize),
		grpc.MaxSendMsgSize(o.maxGrpcMessageSize),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)
	pb.RegisterFunctionEvaluatorServer(server, evaluator)
	healthService := healthchecker.NewHealthChecker()
	grpc_health_v1.RegisterHealthServer(server, healthService)

	go func() {
		<-ctx.Done()
		klog.Info("Shutting down server...")
		server.GracefulStop()
	}()

	if err := server.Serve(lis); err != nil {
		return fmt.Errorf("server failed: %w", err)
	}
	return nil
}

type singleFunctionEvaluator struct {
	pb.UnimplementedFunctionEvaluatorServer

	entrypoint []string
}

func (e *singleFunctionEvaluator) EvaluateFunction(ctx context.Context, req *pb.EvaluateFunctionRequest) (*pb.EvaluateFunctionResponse, error) {
	var stdout, stderr bytes.Buffer
	ctx, span := tracer.Start(ctx, "EvaluateFunction")
	defer span.End()
	cmd := exec.CommandContext(ctx, e.entrypoint[0], e.entrypoint[1:]...) // #nosec G204 -- variables controlled internally
	cmd.Stdin = bytes.NewReader(req.ResourceList)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	var exitErr *exec.ExitError
	outbytes := stdout.Bytes()
	stderrStr := stderr.String()

	if err != nil {
		klog.V(4).Infof("Input Resource List: %s\nOutput Resource List: %s", req.ResourceList, outbytes)
		if errors.As(err, &exitErr) {
			// If the exit code is non-zero, we will try to embed the structured results and content from stderr into the error message.
			rl, pe := fn.ParseResourceList(outbytes)
			if pe != nil {
				// If we can't parse the output resource list, we only surface the content in stderr.
				return nil, status.Errorf(codes.Internal, "failed to parse the output of function %q with stderr '%v': %+v", req.Image, stderrStr, pe)
			}

			return nil, status.Errorf(codes.Internal, "failed to evaluate function %q with structured results: %v and stderr: %v", req.Image, rl.Results.Error(), stderrStr)
		} else {
			return nil, status.Errorf(codes.Internal, "Failed to execute function %q: %s (%s)", req.Image, err, stderrStr)
		}
	}

	klog.Infof("Evaluated %q: stdout length in bytes: %d", req.Image, len(outbytes))

	rl, pErr := fn.ParseResourceList(outbytes)
	if pErr != nil {
		klog.V(4).Infof("Input Resource List: %s\nOutput Resource List: %s", req.ResourceList, outbytes)
		// If we can't parse the output resource list, we only surface the content in stderr.
		return nil, status.Errorf(codes.Internal, "failed to parse the output of function %q with stderr '%v': %+v", req.Image, stderrStr, pErr)
	}
	if rl.Results.ExitCode() != 0 {
		jsonBytes, _ := json.Marshal(rl.Results)
		klog.Warningf("failed to evaluate function %q with structured results: %s and stderr: %v", req.Image, jsonBytes, stderrStr)
	}

	return &pb.EvaluateFunctionResponse{
		ResourceList: outbytes,
		Log:          []byte(stderrStr),
	}, nil
}
