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

package packagerevision

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/webhooks"
	"github.com/kptdev/porch/pkg/cache/contentcache"
	"github.com/kptdev/porch/pkg/engine"
	"github.com/kptdev/porch/pkg/engine/podevaluator"
	porch "github.com/kptdev/porch/pkg/registry/porch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	defaultMaxConcurrentReconciles    = 50
	defaultMaxConcurrentRenders       = 20
	defaultRenderRequeueDelay         = 2 * time.Second
	defaultRepoOperationRetryAttempts = 3
	defaultMaxGRPCMessageSize         = 6 * 1024 * 1024 // 6MB
	defaultPodNamespace               = "porch-fn-system"
)

func (r *PackageRevisionReconciler) InitDefaults() {
	r.MaxConcurrentReconciles = defaultMaxConcurrentReconciles
	r.MaxConcurrentRenders = defaultMaxConcurrentRenders
	r.RenderRequeueDelay = defaultRenderRequeueDelay
	r.RepoOperationRetryAttempts = defaultRepoOperationRetryAttempts
	r.MaxGRPCMessageSize = defaultMaxGRPCMessageSize
}

func (r *PackageRevisionReconciler) BindFlags(prefix string, flags *flag.FlagSet) {
	flags.IntVar(&r.MaxConcurrentReconciles, prefix+"max-concurrent-reconciles", defaultMaxConcurrentReconciles, "Maximum number of concurrent PackageRevision reconciles")
	flags.IntVar(&r.MaxConcurrentRenders, prefix+"max-concurrent-renders", defaultMaxConcurrentRenders, "Maximum number of concurrent renders (0 = unbounded)")
	flags.DurationVar(&r.RenderRequeueDelay, prefix+"render-requeue-delay", defaultRenderRequeueDelay, "Delay before requeuing when render concurrency limit is reached")
	flags.IntVar(&r.RepoOperationRetryAttempts, prefix+"repo-operation-retry-attempts", defaultRepoOperationRetryAttempts, "Number of retry attempts for git operations")
	flags.IntVar(&r.MaxGRPCMessageSize, prefix+"max-grpc-message-size", defaultMaxGRPCMessageSize, "Maximum gRPC message size in bytes for fn-runner communication")
}

// Init wires runtime dependencies (credential resolvers, renderer)
// that require the manager. ContentCache must be set before calling Init.
func (r *PackageRevisionReconciler) Init(mgr ctrl.Manager) error {
	log := ctrl.Log.WithName(r.Name())
	log.Info("PackageRevision controller configuration",
		"maxConcurrentReconciles", r.MaxConcurrentReconciles,
		"maxConcurrentRenders", r.MaxConcurrentRenders,
		"renderRequeueDelay", r.RenderRequeueDelay,
		"repoOperationRetryAttempts", r.RepoOperationRetryAttempts,
		"maxGRPCMessageSize", r.MaxGRPCMessageSize,
	)

	coreClient := mgr.GetClient()
	credResolver := porch.NewCredentialResolver(coreClient, []porch.Resolver{
		porch.NewBasicAuthResolver(),
		porch.NewBearerTokenAuthResolver(),
	})
	caBundleResolver := porch.NewCredentialResolver(coreClient, []porch.Resolver{
		porch.NewCaBundleResolver(),
	})
	r.ExternalPackageFetcher = contentcache.NewExternalPackageFetcher(
		credResolver, caBundleResolver, r.RepoOperationRetryAttempts,
	)

	fnRunnerAddr := os.Getenv("FUNCTION_RUNNER_ADDRESS")
	wrapperServerImage := os.Getenv("WRAPPER_SERVER_IMAGE")

	prefix := os.Getenv("DEFAULT_IMAGE_PREFIX")
	if prefix == "" {
		prefix = runneroptions.GHCRImagePrefix
	}

	var podOpts *podevaluator.PodEvaluatorOptions
	var kubeClient client.WithWatch
	if wrapperServerImage != "" {
		podNamespace := os.Getenv("POD_NAMESPACE")
		if podNamespace == "" {
			podNamespace = defaultPodNamespace
		}
		var err error
		kubeClient, err = client.NewWithWatch(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme()})
		if err != nil {
			return fmt.Errorf("failed to create kube client for pod evaluator: %w", err)
		}
		podOpts = &podevaluator.PodEvaluatorOptions{
			PodNamespace:               podNamespace,
			WrapperServerImage:         wrapperServerImage,
			WarmUpPodCacheOnStartup:    true,
			MaxGrpcMessageSize:         r.MaxGRPCMessageSize,
			DefaultImagePrefix:         prefix,
			MaxWaitlistLength:          1,
			MaxParallelPodsPerFunction: 2,
		}
	}

	functionRuntime, err := engine.NewMultiFunctionRuntime(context.Background(), engine.MultiFunctionRuntimeOptions{
		GRPCAddress:         fnRunnerAddr,
		MaxGrpcMessageSize:  r.MaxGRPCMessageSize,
		FunctionConfigStore: r.FunctionConfigStore,
		PodEvaluator:        podOpts,
		KubeClient:          kubeClient,
		DefaultImagePrefix:  prefix,
	})
	if err != nil {
		return fmt.Errorf("failed to create function runtime: %w", err)
	}
	opts := runneroptions.RunnerOptions{}
	opts.InitDefaults(prefix)
	r.Renderer = newKptRenderer(functionRuntime, opts)
	switch {
	case fnRunnerAddr != "" && wrapperServerImage != "":
		log.Info("function runtime enabled (builtin + fn-runner + pod evaluator)",
			"fnRunner", fnRunnerAddr, "podNamespace", podOpts.PodNamespace)
	case fnRunnerAddr != "":
		log.Info("function runtime enabled (builtin + fn-runner)", "address", fnRunnerAddr)
	case wrapperServerImage != "":
		log.Info("function runtime enabled (builtin + pod evaluator)", "podNamespace", podOpts.PodNamespace)
	default:
		log.Info("function runtime enabled (builtin only)")
	}

	// Register PackageRevision validating webhook.
	// The validator implements admission.Handler interface via its Handle method.
	// Webhook TLS certificates are mounted from Secret at /etc/webhook/certs (see deployment).
	validator := webhooks.NewPackageRevisionValidator(coreClient)

	mgr.GetWebhookServer().Register(
		"/validate-porch-kpt-dev-v1alpha2-packagerevision",
		&admission.Webhook{
			Handler: validator,
		})
	log.Info("PackageRevision validating webhook registered")

	return nil
}
