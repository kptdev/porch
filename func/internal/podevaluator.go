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

package internal

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/kptdev/kpt/pkg/fn/runtime"
	fnconf "github.com/kptdev/porch/controllers/functionconfigs/reconciler"
	"github.com/kptdev/porch/func/evaluator"
	"github.com/kptdev/porch/pkg/util"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultWrapperServerPort          = "9446"
	volumeName                        = "wrapper-server-tools"
	volumeMountPath                   = "/wrapper-server-tools"
	wrapperServerBin                  = "wrapper-server"
	gRPCProbeBin                      = "grpc-health-probe"
	krmFunctionImageLabel             = "fn.kpt.dev/image"
	templateVersionAnnotation         = "fn.kpt.dev/template-version"
	fieldManagerName                  = "krm-function-runner"
	functionContainerName             = "function"
	defaultManagerNamespace           = "porch-system"
	defaultRegistry                   = "ghcr.io/kptdev/krm-functions-catalog/"
	serviceDnsNameSuffix              = ".svc.cluster.local"
	channelBufferSize                 = 128
	defaultMaxWaitlistLength          = 2
	defaultMaxParallelPodsPerFunction = 1
	defaultMaxGrpcRetries             = 2
)

type podEvaluator struct {
	requestCh  chan<- *connectionRequest
	evictionCh chan<- *podEvictionRequest

	podCacheManager *podCacheManager
	maxGrpcRetries  int
}

type PodEvaluatorOptions struct {
	PodNamespace               string        // Namespace to run KRM functions pods in
	WrapperServerImage         string        // Container image name of the wrapper server
	GcScanInterval             time.Duration // Time interval between Garbage Collector scans
	PodTTL                     time.Duration // Time-to-live for pods before GC
	WarmUpPodCacheOnStartup    bool          // If true, pod-cache-config image pods will be deployed at startup
	EnablePrivateRegistries    bool          // If true enables the use of private registries and their authentication
	RegistryAuthSecretPath     string        // The path of the secret used for authenticating to custom registries
	RegistryAuthSecretName     string        // The name of the secret used for authenticating to custom registries
	EnablePrivateRegistriesTls bool          // If enabled, will prioritize use of user provided TLS secret when accessing registries
	TlsSecretPath              string        // The path of the secret used in tls configuration
	MaxGrpcMessageSize         int           // Maximum size of grpc messages in bytes
	DefaultImagePrefix         string        // Default image prefix to use when no prefix is given for an image
	MaxWaitlistLength          int           // Maximum waitlist length per pod
	MaxParallelPodsPerFunction int           // Maximum parallel pods per function
	MaxGrpcRetries             int           // Maximum number of retries on gRPC Unavailable errors
}

var _ Evaluator = &podEvaluator{}

type podData struct {
	// the OCI image name of the KRM function
	image string
	// connection to the grpc server running in the fn evaluator pod
	grpcConnection *grpc.ClientConn
	// namespaced name of the pod
	podKey *client.ObjectKey
	// namespaced name of the service
	serviceKey *client.ObjectKey
}

type connectionRequest struct {
	// the OCI image name of the KRM function
	image string
	// responseCh is the channel to send the response back.
	responseCh chan<- *connectionResponse
}

type connectionResponse struct {
	podData
	// the number of currently ongoing and waiting fn evaluations in the pod
	concurrentEvaluations *atomic.Int32
	// err indicates the error that prevents us to allocate a pod for the fn evaluator
	err error
}

type podReadyResponse struct {
	podData
	// err indicates the error that prevents us to allocate a pod for the fn evaluator
	err error
}

func NewPodEvaluator(ctx context.Context, o PodEvaluatorOptions, cl client.Client, functionConfigStore *fnconf.FunctionConfigStore) (Evaluator, error) {
	maxWaitlist := o.MaxWaitlistLength
	if maxWaitlist <= 0 {
		maxWaitlist = defaultMaxWaitlistLength
	}
	maxPods := o.MaxParallelPodsPerFunction
	if maxPods <= 0 {
		maxPods = defaultMaxParallelPodsPerFunction
	}
	maxRetries := o.MaxGrpcRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxGrpcRetries
	}

	managerNs, err := util.GetInClusterNamespace()
	if err != nil {
		klog.Errorf("failed to get the namespace where the function-runner is running: %v", err)
		klog.Warningf("unable to get the namespace where the function-runner is running, assuming it's a test setup, defaulting to : %v", defaultManagerNamespace)
		managerNs = defaultManagerNamespace
	}

	reqCh := make(chan *connectionRequest, channelBufferSize)
	readyCh := make(chan *podReadyResponse, channelBufferSize)
	evictCh := make(chan *podEvictionRequest, channelBufferSize)

	pe := &podEvaluator{
		requestCh:      reqCh,
		evictionCh:     evictCh,
		maxGrpcRetries: maxRetries,
		podCacheManager: &podCacheManager{
			gcScanInterval:             o.GcScanInterval,
			podTTL:                     o.PodTTL,
			connectionRequestCh:        reqCh,
			podReadyCh:                 readyCh,
			evictionCh:                 evictCh,
			functions:                  map[string]*functionInfo{},
			maxWaitlistLength:          maxWaitlist,
			maxParallelPodsPerFunction: maxPods,
			functionConfigMap:          functionConfigStore,

			podManager: &podManager{
				kubeClient:         cl,
				namespace:          o.PodNamespace,
				wrapperServerImage: o.WrapperServerImage,
				podReadyCh:         readyCh,
				podReadyTimeout:    60 * time.Second,
				managerNamespace:   managerNs,
				maxGrpcMessageSize: o.MaxGrpcMessageSize,

				enablePrivateRegistries:    o.EnablePrivateRegistries,
				registryAuthSecretPath:     o.RegistryAuthSecretPath,
				registryAuthSecretName:     o.RegistryAuthSecretName,
				enablePrivateRegistriesTls: o.EnablePrivateRegistriesTls,
				tlsSecretPath:              o.TlsSecretPath,
				tagResolver:                runtime.TagResolver{},
			},
		},
	}
	go pe.podCacheManager.podCacheManager(ctx)

	err = pe.podCacheManager.retrieveFunctionPods(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve existing pods: %w", err)
	}

	if o.WarmUpPodCacheOnStartup {
		// TODO(mengqiy): add watcher that support reloading the cache when the config file was changed.
		err = pe.podCacheManager.warmupCache(o.DefaultImagePrefix)
		// If we can't warm up the cache, we can still proceed without it.
		if err != nil {
			klog.Warningf("unable to warm up the pod cache: %v", err)
		}
	}

	return pe, nil
}

func (pe *podEvaluator) EvaluateFunction(ctx context.Context, req *evaluator.EvaluateFunctionRequest) (*evaluator.EvaluateFunctionResponse, error) {
	starttime := time.Now()
	var image string
	defer func() {
		klog.Infof("evaluating %v in pod took %v", req.Image, time.Since(starttime))
	}()
	tagResolver := pe.podCacheManager.podManager.tagResolver
	var err error
	image, err = tagResolver.ResolveFunctionImage(ctx, req.Image, req.Tag)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve tag for image %q with constraint %q: %w", req.Image, req.Tag, err)
	}
	req.Image = image

	maxRetries := pe.maxGrpcRetries
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			klog.Warningf("Retrying function evaluation for %v (attempt %d/%d) after Unavailable error", req.Image, attempt+1, maxRetries+1)
		}

		responseChannel := make(chan *connectionResponse, 1)
		pe.requestCh <- &connectionRequest{
			image:      req.Image,
			responseCh: responseChannel,
		}

		select {
		case pod := <-responseChannel:
			if pod == nil {
				return nil, fmt.Errorf("unable to get the grpc client to the pod for %v: nil pod response", req.Image)
			}
			if pod.err != nil {
				return nil, fmt.Errorf("unable to get the grpc client to the pod for %v: %w", req.Image, pod.err)
			}
			if pod.grpcConnection == nil {
				return nil, fmt.Errorf("unable to get the grpc client to the pod for %v: missing grpc connection", req.Image)
			}

			decremented := false
			defer func() {
				if !decremented {
					pod.concurrentEvaluations.Add(-1)
				}
			}()

			// Pod is guaranteed to have an active gRPC connection (verified
			// during pod readiness via waitForGrpcReady). Unavailable means
			// the pod died after being connected.
			resp, err := evaluator.NewFunctionEvaluatorClient(pod.grpcConnection).EvaluateFunction(ctx, req)
			if err != nil {
				// Retry only on Unavailable — indicates the pod is dead/unreachable:
				// connection refused (pod deleted), connection reset (pod crashed),
				// DNS failure (service deleted), TCP timeout (pod IP unreachable).
				// Other codes (Internal, InvalidArgument, DeadlineExceeded) are real
				// function errors that should not be retried.
				if status.Code(err) == codes.Unavailable && ctx.Err() == nil {
					lastErr = err
					// Decrement immediately so the evicted pod's counter reflects reality
					// while we wait for the next attempt.
					pod.concurrentEvaluations.Add(-1)
					decremented = true
					// Wait for the cache manager to confirm eviction before retrying,
					// preventing re-allocation of the same dead pod.
					doneCh := make(chan struct{})
					if pod.podKey == nil {
						return nil, fmt.Errorf("unable to evict dead pod for %v: missing pod key", req.Image)
					}
					evictReq := &podEvictionRequest{image: pod.image, podKey: *pod.podKey, doneCh: doneCh}
					select {
					case pe.evictionCh <- evictReq:
					case <-ctx.Done():
						return nil, fmt.Errorf("function evaluation timed out for %v: %w", req.Image, ctx.Err())
					}
					select {
					case <-doneCh:
					case <-ctx.Done():
						return nil, fmt.Errorf("function evaluation timed out for %v: %w", req.Image, ctx.Err())
					}
					continue
				}
				klog.V(4).Infof("Resource List: %s", req.ResourceList)
				return nil, fmt.Errorf("unable to evaluate %v with pod evaluator: %w", req.Image, err)
			}
			if len(resp.Log) > 0 {
				klog.Warningf("evaluating %q succeeded, but stderr is: %v", req.Image, string(resp.Log))
			}
			return resp, nil
		case <-ctx.Done():
			return nil, fmt.Errorf("function evaluation timed out for %v: %w", req.Image, ctx.Err())
		}
	}

	return nil, fmt.Errorf("unable to evaluate %v with pod evaluator after retries: %w", req.Image, lastErr)
}
