// Copyright 2022, 2024-2026 The kpt Authors
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
	"context"
	"fmt"

	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/porch/controllers/functionconfigs"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/engine/podevaluator"
	"github.com/kptdev/porch/pkg/repository"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type EngineOption interface {
	apply(engine *cadEngine) error
}

type EngineOptionFunc func(engine *cadEngine) error

var _ EngineOption = EngineOptionFunc(nil)

func (f EngineOptionFunc) apply(engine *cadEngine) error {
	engine.taskHandler.SetRepoOpener(engine)
	return f(engine)
}

func WithCache(cache cachetypes.Cache) EngineOption {
	return EngineOptionFunc(func(engine *cadEngine) error {
		engine.cache = cache
		return nil
	})
}

func WithBuiltinFunctionRuntime(functionConfigStore *functionconfigs.FunctionConfigStore) EngineOption {
	return EngineOptionFunc(func(engine *cadEngine) error {
		runtime := newBuiltinRuntime(functionConfigStore)
		if engine.taskHandler.GetRuntime() == nil {
			engine.taskHandler.SetRuntime(runtime)
		} else if mr, ok := engine.taskHandler.GetRuntime().(*fn.MultiRuntime); ok {
			mr.Add(runtime)
		} else {
			engine.taskHandler.SetRuntime(fn.NewMultiRuntime([]fn.FunctionRuntime{engine.taskHandler.GetRuntime(), runtime}))
		}
		return nil
	})
}

func WithGRPCFunctionRuntime(options GRPCRuntimeOptions, functionConfigStore *functionconfigs.FunctionConfigStore) EngineOption {
	return EngineOptionFunc(func(engine *cadEngine) error {
		runtime, err := newGRPCFunctionRuntime(options, functionConfigStore)
		if err != nil {
			return fmt.Errorf("failed to create function runtime: %w", err)
		}
		if engine.taskHandler.GetRuntime() == nil {
			engine.taskHandler.SetRuntime(runtime)
		} else if mr, ok := engine.taskHandler.GetRuntime().(*fn.MultiRuntime); ok {
			mr.Add(runtime)
		} else {
			engine.taskHandler.SetRuntime(fn.NewMultiRuntime([]fn.FunctionRuntime{engine.taskHandler.GetRuntime(), runtime}))
		}
		return nil
	})
}

func WithPodEvaluatorRuntime(ctx context.Context, podEvaluatorOptions podevaluator.PodEvaluatorOptions, kubeClient client.WithWatch, functionConfigStore *functionconfigs.FunctionConfigStore) EngineOption {
	return EngineOptionFunc(func(engine *cadEngine) error {
		ev, err := podevaluator.NewEvaluator(ctx, podEvaluatorOptions, kubeClient, functionConfigStore)
		if err != nil {
			return fmt.Errorf("failed to create pod evaluator: %w", err)
		}
		port := podEvaluatorOptions.GRPCPort
		if port <= 0 {
			port = podevaluator.DefaultGRPCPort
		}
		addr := fmt.Sprintf(":%d", port)
		lis, err := ev.ListenGRPC(addr)
		if err != nil {
			return fmt.Errorf("failed to bind pod evaluator gRPC on %s: %w", addr, err)
		}
		go func() {
			if err := ev.ServeGRPC(ctx, lis, podEvaluatorOptions.MaxGrpcMessageSize); err != nil {
				klog.Errorf("pod evaluator gRPC server: %v", err)
			}
		}()
		return addFunctionRuntime(engine, ev.Runtime())
	})
}

func addFunctionRuntime(engine *cadEngine, runtime fn.FunctionRuntime) error {
	if runtime == nil {
		return nil
	}
	if engine.taskHandler.GetRuntime() == nil {
		engine.taskHandler.SetRuntime(runtime)
		return nil
	}
	if mr, ok := engine.taskHandler.GetRuntime().(*fn.MultiRuntime); ok {
		mr.Add(runtime)
		return nil
	}
	engine.taskHandler.SetRuntime(fn.NewMultiRuntime([]fn.FunctionRuntime{engine.taskHandler.GetRuntime(), runtime}))
	return nil
}

func WithRunnerOptionsResolver(fn func(namespace string) runneroptions.RunnerOptions) EngineOption {
	return EngineOptionFunc(func(engine *cadEngine) error {
		engine.taskHandler.SetRunnerOptionsResolver(fn)
		return nil
	})
}

func WithCredentialResolver(resolver repository.CredentialResolver) EngineOption {
	return EngineOptionFunc(func(engine *cadEngine) error {
		engine.taskHandler.SetCredentialResolver(resolver)
		return nil
	})
}

func WithReferenceResolver(resolver repository.ReferenceResolver) EngineOption {
	return EngineOptionFunc(func(engine *cadEngine) error {
		engine.taskHandler.SetReferenceResolver(resolver)
		return nil
	})
}

func WithUserInfoProvider(provider repository.UserInfoProvider) EngineOption {
	return EngineOptionFunc(func(engine *cadEngine) error {
		engine.userInfoProvider = provider
		return nil
	})
}

func WithWatcherManager(watcherManager *watcherManager) EngineOption {
	return EngineOptionFunc(func(engine *cadEngine) error {
		engine.watcherManager = watcherManager
		return nil
	})
}

func WithRepoOperationRetryAttempts(retryAttempts int) EngineOption {
	return EngineOptionFunc(func(engine *cadEngine) error {
		engine.taskHandler.SetRepoOperationRetryAttempts(retryAttempts)
		return nil
	})
}
