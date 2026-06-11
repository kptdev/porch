// Copyright 2022, 2025-2026 The kpt Authors
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
	"io"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/kptops"
	fnsdk "github.com/kptdev/krm-functions-sdk/go/fn"
	"github.com/kptdev/porch/controllers/functionconfigs"
	regclientref "github.com/regclient/regclient/types/ref"
	"k8s.io/klog/v2"
)

type builtinRuntime struct {
	store *functionconfigs.FunctionConfigStore
}

func newBuiltinRuntime(functionConfigStore *functionconfigs.FunctionConfigStore) *builtinRuntime {
	return &builtinRuntime{
		store: functionConfigStore,
	}
}

var _ kptops.FunctionRuntime = &builtinRuntime{}

func (br *builtinRuntime) GetRunner(ctx context.Context, funct *kptfilev1.Function) (fn.FunctionRunner, error) {
	builtinRunner := &builtinRunner{
		ctx: ctx,
	}

	if funct.Tag != "" {
		ref, err := regclientref.New(funct.Image)
		if err != nil {
			return nil, fmt.Errorf("failed to parse image %q as reference: %w", funct.Image, err)
		}
		// If the image already carries an inline tag, strip it
		// so FindBestSemverMatch gets a bare repository name, and
		// we don't produce a double-tag
		if ref.Tag != "" {
			if stripped := strings.TrimSuffix(funct.Image, ":"+ref.Tag); stripped != funct.Image {
				klog.Infof("Image %q already contains tag %q; stripping it in favor of Tag constraint %q", funct.Image, ref.Tag, funct.Tag)
				funct.Image = stripped
			}
		}

		processor, ok := br.store.GetProcessorByConstraint(funct.Image, funct.Tag)
		if !ok {
			return nil, &fn.NotFoundError{
				Function: *funct,
			}
		}
		builtinRunner.processor = processor
	} else {
		klog.Infof("Image tag is empty, using the image with explicit tag: %q", funct.Image)
		processor, found := br.store.GetProcessor(funct.Image)
		if !found {
			return nil, &fn.NotFoundError{Function: *funct}
		}
		builtinRunner.processor = processor
	}

	return builtinRunner, nil
}

func (br *builtinRuntime) Close() error {
	return nil
}

type builtinRunner struct {
	ctx       context.Context
	processor fnsdk.ResourceListProcessor
}

var _ fn.FunctionRunner = &builtinRunner{}

func (br *builtinRunner) Run(r io.Reader, w io.Writer) (err error) {
	// KRM functions often panic on input validation errors, so we need to convert panics to errors
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("KRM function panicked with: %v", p)
		}
	}()
	return fnsdk.Execute(br.processor, r, w)
}
