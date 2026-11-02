// Copyright 2022-2026 The kpt Authors
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
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kptdev/porch/config/loader"
	configtypes "github.com/kptdev/porch/config/types"
	pb "github.com/kptdev/porch/func/evaluator"
	"github.com/kptdev/porch/func/healthchecker"
	"github.com/kptdev/porch/func/internal"
	"github.com/kptdev/porch/internal/telemetry"
	"github.com/spf13/pflag"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	contextsignal "k8s.io/apiserver/pkg/server"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	execRuntime = "exec"
)

type options struct {
	// The server port
	port int
	// The runtime(s) to disable. Multiple runtimes should separated by `,`.
	disableRuntimes string
	// The verbosity level of the logs (0-5)
	logLevel int

	MaxGrpcMessageSize int

	// Parameters of ExecEvaluator
	FunctionCacheDir string
}

func main() {
	o, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	if err := run(o); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
}

func parseOptions(args []string) (*options, error) {
	fs := pflag.NewFlagSet("function-runner", pflag.ContinueOnError)
	var cfg configtypes.FunctionRunner
	var global configtypes.Global
	var configPath string
	if err := loader.Bind(fs, &cfg); err != nil {
		return nil, err
	}
	if err := loader.Bind(fs, &global); err != nil {
		return nil, err
	}
	fs.StringVar(&configPath, "config", "", "Path to the porch configuration file.")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	doc, err := loader.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	if err := loader.OverlaySection(fs, &cfg, doc, "functionRunner"); err != nil {
		return nil, err
	}
	if err := loader.OverlaySection(fs, &global, doc, "global"); err != nil {
		return nil, err
	}
	if err := telemetry.ApplyEnv(global.Telemetry); err != nil {
		return nil, err
	}
	return &options{
		port:               cfg.Port,
		disableRuntimes:    cfg.DisableRuntimes,
		logLevel:           cfg.LogLevel,
		MaxGrpcMessageSize: int(global.MaxGrpcMessageSize),
		FunctionCacheDir:   cfg.FunctionCacheDir,
	}, nil
}

func run(o *options) error {
	ctx := contextsignal.SetupSignalContext()

	flagSet := flag.NewFlagSet("log-level", flag.ContinueOnError)
	klog.InitFlags(flagSet)
	_ = flagSet.Parse([]string{"--v", strconv.Itoa(o.logLevel)})

	ctrllog.SetLogger(textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(o.logLevel))))

	address := fmt.Sprintf(":%d", o.port)
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	go func() {
		<-ctx.Done()
		lis.Close()
	}()

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

	availableRuntimes := map[string]struct{}{
		execRuntime: {},
	}
	if o.disableRuntimes != "" {
		runtimesFromFlag := strings.SplitSeq(o.disableRuntimes, ",")
		for rt := range runtimesFromFlag {
			delete(availableRuntimes, rt)
		}
	}

	runtimes := []internal.Evaluator{}
	for rt := range availableRuntimes {
		switch rt {
		case execRuntime:
			execEval, err := internal.NewExecutableEvaluator(o.FunctionCacheDir)
			if err != nil {
				return fmt.Errorf("failed to initialize executable evaluator: %w", err)
			}
			runtimes = append(runtimes, execEval)
		}
	}
	if len(runtimes) == 0 {
		klog.Warning("no runtime is enabled in function-runner")
	}
	evaluator := internal.NewMultiEvaluator(runtimes...)

	klog.Infof("Listening on %s", address)

	// Start the gRPC server
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(o.MaxGrpcMessageSize),
		grpc.MaxSendMsgSize(o.MaxGrpcMessageSize),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)
	go func() {
		<-ctx.Done()
		server.Stop()
	}()
	pb.RegisterFunctionEvaluatorServer(server, evaluator)
	healthService := healthchecker.NewHealthChecker()
	grpc_health_v1.RegisterHealthServer(server, healthService)
	if err := server.Serve(lis); err != nil {
		return fmt.Errorf("server failed: %w", err)
	}
	return nil
}
