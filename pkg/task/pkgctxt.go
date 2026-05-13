// Copyright 2022, 2024 The kpt Authors
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

package task

import (
	"context"
	"fmt"

	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/builtins"
	"github.com/kptdev/kpt/pkg/lib/builtins/builtintypes"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel/trace"
	"sigs.k8s.io/kustomize/kyaml/fn/runtime/runtimeutil"
	"sigs.k8s.io/kustomize/kyaml/kio"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

type builtinEvalMutation struct {
	function string
	runner   fn.FunctionRunner
}

func newPackageContextGeneratorMutation(packageConfig *builtintypes.PackageConfig) (mutation, error) {
	runner := builtins.GetBuiltinFn(packageConfig)

	return &builtinEvalMutation{
		function: runneroptions.FuncGenPkgContext,
		runner:   runner,
	}, nil
}

var _ mutation = &builtinEvalMutation{}

func (m *builtinEvalMutation) apply(ctx context.Context, resources repository.PackageResources) (repository.PackageResources, *porchapi.TaskResult, error) {
	_, span := tracer.Start(ctx, "builtinEvalMutation::Apply", trace.WithAttributes())
	defer span.End()

	ff := &runtimeutil.FunctionFilter{
		Run:     m.runner.Run,
		Results: &yaml.RNode{},
	}

	pr := &packageReader{
		input: resources,
		extra: map[string]string{},
	}

	result := repository.PackageResources{
		Contents: map[string]string{},
	}

	pipeline := kio.Pipeline{
		Inputs:  []kio.Reader{pr},
		Filters: []kio.Filter{ff},
		Outputs: []kio.Writer{&packageWriter{
			output: result,
		}},
	}

	if err := pipeline.Execute(); err != nil {
		return repository.PackageResources{}, nil, fmt.Errorf("failed to evaluate function %q: %w", m.function, err)
	}

	for k, v := range pr.extra {
		result.Contents[k] = v
	}

	return result, &porchapi.TaskResult{}, nil
}
