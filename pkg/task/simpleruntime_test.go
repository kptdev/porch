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

package task

import (
	"context"
	"io"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/krm-functions-catalog/functions/go/apply-setters/applysetters"
	"sigs.k8s.io/kustomize/kyaml/fn/framework"
	"sigs.k8s.io/kustomize/kyaml/kio"
)

func NewSimpleFunctionRuntime() fn.FunctionRuntime {
	return &runtime{}
}

type runtime struct{}

var _ fn.FunctionRuntime = &runtime{}

var processors = map[string]framework.ResourceListProcessorFunc{
	"ghcr.io/kptdev/krm-functions-catalog/apply-setters:v0.2.4": applySetters,
}

func (*runtime) GetRunner(ctx context.Context, f *kptfilev1.Function) (fn.FunctionRunner, error) {
	p, ok := processors[f.Image]
	if !ok {
		return nil, &fn.NotFoundError{Function: *f}
	}
	return &runner{ctx: ctx, processor: p}, nil
}

type runner struct {
	ctx       context.Context
	processor framework.ResourceListProcessorFunc
}

func (r *runner) Run(in io.Reader, out io.Writer) error {
	rw := &kio.ByteReadWriter{Reader: in, Writer: out, KeepReaderAnnotations: true}
	return framework.Execute(r.processor, rw)
}

func applySetters(rl *framework.ResourceList) error {
	if rl.FunctionConfig == nil {
		return nil
	}
	var as applysetters.ApplySetters
	applysetters.Decode(rl.FunctionConfig, &as)
	items, err := as.Filter(rl.Items)
	if err != nil {
		return err
	}
	rl.Items = items
	return nil
}
