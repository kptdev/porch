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
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/kptdev/kpt/api/runneroptions"
	"github.com/kptdev/porch/controllers/packagerevisions/pkg/webhooks"
	"github.com/kptdev/porch/pkg/cache/contentcache"
	"github.com/kptdev/porch/pkg/engine"
	"github.com/kptdev/porch/pkg/registry/porch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Init wires runtime dependencies (credential resolvers, renderer)
// that require the manager. ContentCache must be set before calling Init.
func (r *PackageRevisionReconciler) Init(mgr ctrl.Manager) error {
	log := ctrl.Log.WithName(r.Name())

	r.logConfig(&log)

	coreClient := mgr.GetClient()
	credResolver := porch.NewCredentialResolver(coreClient, []porch.Resolver{
		porch.NewBasicAuthResolver(),
		porch.NewBearerTokenAuthResolver(),
	})
	caBundleResolver := porch.NewCredentialResolver(coreClient, []porch.Resolver{
		porch.NewCaBundleResolver(),
	})
	r.ExternalPackageFetcher = contentcache.NewExternalPackageFetcher(
		credResolver, caBundleResolver, r.Cfg.RepoOperationRetryAttempts,
	)

	functionRuntime, err := engine.NewMultiFunctionRuntime(context.Background(), engine.MultiFunctionRuntimeOptions{
		GRPCAddress:         r.Cfg.FunctionRunnerAddress,
		PodEvaluatorAddress: r.Cfg.PodEvaluatorAddress,
		FunctionConfigStore: r.FunctionConfigStore,
		Global:              r.GCfg,
	})
	if err != nil {
		return fmt.Errorf("failed to create function runtime: %w", err)
	}
	opts := runneroptions.RunnerOptions{}
	opts.InitDefaults(r.GCfg.DefaultImagePrefix)
	r.Renderer = newKptRenderer(functionRuntime, opts)
	switch {
	case r.Cfg.FunctionRunnerAddress != "" && r.Cfg.PodEvaluatorAddress != "":
		log.Info("function runtime enabled (builtin + fn-runner + porch-server pod evaluator)",
			"fnRunner", r.Cfg.FunctionRunnerAddress, "podEvaluator", r.Cfg.PodEvaluatorAddress)
	case r.Cfg.FunctionRunnerAddress != "":
		log.Info("function runtime enabled (builtin + fn-runner)", "address", r.Cfg.FunctionRunnerAddress)
	case r.Cfg.PodEvaluatorAddress != "":
		log.Info("function runtime enabled (builtin + porch-server pod evaluator)", "podEvaluator", r.Cfg.PodEvaluatorAddress)
	default:
		log.Info("function runtime enabled (builtin only)")
	}

	// Register PackageRevision validating webhook.
	// The validator implements admission.Handler interface via its Handle method.
	// Webhook TLS certificates are mounted from Secret at /etc/webhook/certs (see deployment).
	validator := webhooks.NewPackageRevisionValidator(coreClient, r.GCfg.LifecyclePolicy)

	mgr.GetWebhookServer().Register(
		"/validate-porch-kpt-dev-v1alpha2-packagerevision",
		&admission.Webhook{
			Handler: validator,
		})
	log.Info("PackageRevision validating webhook registered")

	// Verify the registration once the manager has started. A missing registration is
	// logged, not fatal: the controller still serves, but deletes are unguarded.
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		if err := verifyWebhookRegistration(ctx, mgr.GetAPIReader()); err != nil {
			log.Error(err, "PackageRevision deletion guard is not verified")
			return nil
		}
		log.Info("PackageRevision validating webhook configuration verified")
		return nil
	})); err != nil {
		return err
	}

	return nil
}

func (r *PackageRevisionReconciler) logConfig(log *logr.Logger) {
	confstr, _ := json.MarshalIndent(r.Cfg, "", "  ")
	gconfstr, _ := json.MarshalIndent(r.GCfg, "", "  ")
	log.Info(fmt.Sprintf("PackageRevision controller configuration:\n%s\n%s", confstr, gconfstr))
}
