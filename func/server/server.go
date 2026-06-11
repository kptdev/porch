// Copyright 2022-2025 The kpt Authors
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
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/controllers/functionconfigs"
	"github.com/kptdev/porch/func/evaluator"
	"github.com/kptdev/porch/func/healthchecker"
	pb "github.com/kptdev/porch/func/proto"
	porchotel "github.com/kptdev/porch/internal/otel"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	contextsignal "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	execRuntime = "exec"
	podRuntime  = "pod"

	wrapperServerImageEnv = "WRAPPER_SERVER_IMAGE"
)

type options struct {
	// The server port
	port int
	// The runtime(s) to disable. Multiple runtimes should separated by `,`.
	disableRuntimes string
	// The verbosity level of the logs (0-5)
	logLevel int

	defaultImagePrefix string

	// Parameters of ExecEvaluator
	exec evaluator.ExecutableEvaluatorOptions
	// Parameters of PodEvaluator
	pod evaluator.PodEvaluatorOptions
}

func main() {
	o := &options{}
	// generic flags
	flag.IntVar(&o.port, "port", 9445, "The server port")
	flag.StringVar(&o.disableRuntimes, "disable-runtimes", "", fmt.Sprintf("The runtime(s) to disable. Multiple runtimes should separated by `,`. Available runtimes: `%v`, `%v`.", execRuntime, podRuntime))
	flag.IntVar(&o.logLevel, "log-level", 2, "The verbosity level of the logs (0-5)")
	flag.StringVar(&o.defaultImagePrefix, "default-image-prefix", runneroptions.GHCRImagePrefix, "Default prefix for unqualified function names")
	// flags for the exec runtime
	flag.StringVar(&o.exec.FunctionCacheDir, "functions", "./functions", "Path to cached functions.")
	// flags for the pod runtime
	flag.BoolVar(&o.pod.WarmUpPodCacheOnStartup, "warm-up-pod-cache", true, "if true, pod-cache-config image pods will be deployed at startup")
	flag.StringVar(&o.pod.PodNamespace, "pod-namespace", "porch-fn-system", "Namespace to run KRM functions pods.")
	flag.DurationVar(&o.pod.PodTTL, "pod-ttl", 30*time.Minute, "TTL for pods before GC.")
	flag.DurationVar(&o.pod.GcScanInterval, "scan-interval", time.Minute, "The interval of GC between scans.")
	flag.BoolVar(&o.pod.EnablePrivateRegistries, "enable-private-registries", false, "if true enables the use of private registries and their authentication")
	flag.StringVar(&o.pod.RegistryAuthSecretPath, "registry-auth-secret-path", "/var/tmp/config-secret/.dockerconfigjson", "The path of the secret used for authenticating to custom registries")
	flag.StringVar(&o.pod.RegistryAuthSecretName, "registry-auth-secret-name", "auth-secret", "The name of the secret used for authenticating to custom registries")
	flag.BoolVar(&o.pod.EnablePrivateRegistriesTls, "enable-private-registries-tls", false, "if enabled, will prioritize use of user provided TLS secret when accessing registries")
	flag.StringVar(&o.pod.TlsSecretPath, "tls-secret-path", "/var/tmp/tls-secret/", "The path of the secret used in tls configuration")
	flag.IntVar(&o.pod.MaxGrpcMessageSize, "max-request-body-size", 6*1024*1024, "Maximum size of grpc messages in bytes. Keep this in sync with porch-server's corresponding argument.")
	flag.IntVar(&o.pod.MaxWaitlistLength, "max-waitlist-length", 2, "Maximum waitlist length per pod")
	flag.IntVar(&o.pod.MaxParallelPodsPerFunction, "max-parallel-pods-per-function", 1, "Maximum parallel pods per function")

	flag.Parse()

	if err := run(o); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
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

	err = porchotel.SetupOpenTelemetry(ctx)
	if err != nil {
		contextsignal.RequestShutdown()
		klog.Errorf("%v\n", err)
		return err
	}

	availableRuntimes := map[string]struct{}{
		execRuntime: {},
		podRuntime:  {},
	}
	if o.disableRuntimes != "" {
		runtimesFromFlag := strings.Split(o.disableRuntimes, ",")
		for _, rt := range runtimesFromFlag {
			delete(availableRuntimes, rt)
		}
	}

	scheme, err := buildScheme()
	if err != nil {
		return err
	}
	fnConfigReconciler, err := buildFnConfigReconciler(o, scheme)
	if err != nil {
		return err
	}

	runtimes := []evaluator.Evaluator{}
	for rt := range availableRuntimes {
		switch rt {
		case execRuntime:
			execEval, err := evaluator.NewExecutableEvaluator(fnConfigReconciler.FunctionConfigStore)
			if err != nil {
				return fmt.Errorf("failed to initialize executable evaluator: %w", err)
			}
			runtimes = append(runtimes, execEval)
		case podRuntime:
			o.pod.WrapperServerImage = os.Getenv(wrapperServerImageEnv)
			if o.pod.WrapperServerImage == "" {
				return fmt.Errorf("environment variable %v must be set to use pod function evaluator runtime", wrapperServerImageEnv)
			}
			o.pod.DefaultImagePrefix = o.defaultImagePrefix
			podEval, err := evaluator.NewPodEvaluator(ctx, o.pod, fnConfigReconciler.Client, fnConfigReconciler.FunctionConfigStore)
			if err != nil {
				return fmt.Errorf("failed to initialize pod evaluator: %w", err)
			}
			runtimes = append(runtimes, podEval)
		}
	}
	if len(runtimes) == 0 {
		klog.Warning("no runtime is enabled in function-runner")
	}
	evaluator := evaluator.NewMultiEvaluator(runtimes...)

	klog.Infof("Listening on %s", address)

	// Start the gRPC server
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(o.pod.MaxGrpcMessageSize),
		grpc.MaxSendMsgSize(o.pod.MaxGrpcMessageSize),
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

func getRestConfig() (*rest.Config, error) {
	restCfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	// Give it a slightly higher QPS to prevent unnecessary client-side throttling.
	if restCfg.QPS < 30 {
		restCfg.QPS = 30.0
		restCfg.Burst = 45
	}

	return restCfg, nil
}

func buildScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := configapi.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	return scheme, nil
}

func buildFnConfigReconciler(o *options, scheme *runtime.Scheme) (*functionconfigs.FunctionConfigReconciler, error) {
	restCfg, err := getRestConfig()
	if err != nil {
		return nil, err
	}

	var cacheOpts cache.Options

	cacheOpts.Scheme = scheme
	cacheOpts.DefaultNamespaces = map[string]cache.Config{
		o.pod.PodNamespace: {},
	}

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme: scheme,
		Cache:  cacheOpts,
	})
	if err != nil {
		return nil, err
	}

	functionConfigStore := functionconfigs.NewStore(o.defaultImagePrefix, o.exec.FunctionCacheDir)

	rec := &functionconfigs.FunctionConfigReconciler{
		Client:              mgr.GetClient(),
		FunctionConfigStore: functionConfigStore,
		For:                 functionconfigs.ReconcilerForFunctionRunner,
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&configapi.FunctionConfig{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(rec); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	_ = cancel

	go func() {
		if err := mgr.Start(ctx); err != nil {
			klog.Infof("manager stopped: %v", err)
		}
	}()

	if ok := mgr.GetCache().WaitForCacheSync(ctx); !ok {
		return nil, fmt.Errorf("cache didn't sync: %w", err)
	}

	return rec, nil
}
