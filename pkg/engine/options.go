// Copyright 2022, 2024-2025 The kpt Authors
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
	"fmt"

	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	"github.com/kptdev/porch/controllers/functionconfigs"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/repository"
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

func WithGRPCFunctionRuntime(options GRPCRuntimeOptions) EngineOption {
	return EngineOptionFunc(func(engine *cadEngine) error {
		runtime, err := newGRPCFunctionRuntime(options)
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

func WithFunctionRuntime(runtime fn.FunctionRuntime) EngineOption {
	return EngineOptionFunc(func(engine *cadEngine) error {
		engine.taskHandler.SetRuntime(runtime)
		return nil
	})
}

func WithRunnerOptions(options runneroptions.RunnerOptions) EngineOption {
	return WithRunnerOptionsResolver(func(namespace string) runneroptions.RunnerOptions { return options })
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
