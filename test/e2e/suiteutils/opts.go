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

package suiteutils

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DefaultRenderWaitTimeout is the default timeout used by WaitForRender.
const DefaultRenderWaitTimeout = time.Minute

type waitForRenderConfig struct {
	timeout time.Duration
}

type WaitForRenderOption interface {
	client.CreateOption
	client.UpdateOption
	client.PatchOption
	ApplyWaitForRender(*waitForRenderConfig)
}

type renderTimeoutOption time.Duration

func (o renderTimeoutOption) ApplyWaitForRender(cfg *waitForRenderConfig) {
	cfg.timeout = time.Duration(o)
}

func (renderTimeoutOption) ApplyToUpdate(*client.UpdateOptions) {}

func (renderTimeoutOption) ApplyToCreate(*client.CreateOptions) {}

func (renderTimeoutOption) ApplyToPatch(*client.PatchOptions) {}

// WithRenderTimeout configures how long WaitForRender polls for render completion.
// It can be passed directly to WaitForRender or alongside client create/update/patch
// options in CreateAndWaitForRender, UpdateAndWaitForRender, and PatchAndWaitForRender.
func WithRenderTimeout(timeout time.Duration) WaitForRenderOption {
	return renderTimeoutOption(timeout)
}

func defaultWaitForRenderConfig() waitForRenderConfig {
	return waitForRenderConfig{timeout: DefaultRenderWaitTimeout}
}

func applyWaitForRenderOptions(opts []WaitForRenderOption) waitForRenderConfig {
	cfg := defaultWaitForRenderConfig()
	for _, opt := range opts {
		opt.ApplyWaitForRender(&cfg)
	}
	return cfg
}

func SplitOptions[T, P any](opts []P) ([]T, []P) {
	tOpts := make([]T, 0, len(opts))
	pOpts := make([]P, 0, len(opts))
	for _, opt := range opts {
		if tOpt, ok := any(opt).(T); ok {
			tOpts = append(tOpts, tOpt)
		} else {
			pOpts = append(pOpts, opt)
		}
	}
	return tOpts, pOpts
}
