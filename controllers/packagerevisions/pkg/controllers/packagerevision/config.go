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
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/webhooks"
	"github.com/kptdev/porch/pkg/cache/contentcache"
	"github.com/kptdev/porch/pkg/engine"
	porch "github.com/kptdev/porch/pkg/registry/porch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	defaultMaxConcurrentReconciles    = 50
	defaultMaxConcurrentRenders       = 20
	defaultRenderRequeueDelay         = 2 * time.Second
	defaultRepoOperationRetryAttempts = 3
	defaultMaxGRPCMessageSize         = 6 * 1024 * 1024 // 6MB
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
	functionRuntime, err := engine.NewMultiFunctionRuntime(fnRunnerAddr, r.MaxGRPCMessageSize, r.FunctionConfigStore)
	if err != nil {
		return fmt.Errorf("failed to create function runtime: %w", err)
	}
	opts := runneroptions.RunnerOptions{}
	prefix := os.Getenv("DEFAULT_IMAGE_PREFIX")
	if prefix == "" {
		prefix = runneroptions.GHCRImagePrefix
	}
	opts.InitDefaults(prefix)
	r.Renderer = newKptRenderer(functionRuntime, opts)
	if fnRunnerAddr != "" {
		ctrl.Log.WithName(r.Name()).Info("function runtime enabled (builtin + fn-runner)", "address", fnRunnerAddr)
	} else {
		ctrl.Log.WithName(r.Name()).Info("function runtime enabled (builtin only, FUNCTION_RUNNER_ADDRESS not set)")
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
