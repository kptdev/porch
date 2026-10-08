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

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/kptops"
	fnsdk "github.com/kptdev/krm-functions-sdk/go/fn"
	"github.com/kptdev/porch/controllers/functionconfigs"
	imageutil "github.com/kptdev/porch/pkg/util/image"
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

	cache := br.store.GetExecCache()

	if funct.Tag != "" {
		parsedImage := imageutil.Parse(funct.Image)
		// If the image already carries an inline tag, strip it
		// so lookup uses a bare repository name, and we don't produce a double-tag.
		if parsedImage.Tag != "" {
			klog.V(3).Infof("Image %q already contains tag %q; stripping it in favor of Tag constraint %q",
				funct.Image, parsedImage.Tag, funct.Tag)
			parsedImage.Tag = ""
			funct.Image = parsedImage.Full()
		}

		builtinEntry := cache[parsedImage.BaseName]
		if !imageutil.MatchesConfigTags(funct.Tag, builtinEntry.Tags) {
			return nil, &fn.NotFoundError{Function: *funct}
		}
		builtinRunner.processor = builtinEntry.Process
	} else {
		klog.V(3).Infof("Image tag is empty, using the image with explicit tag: %q", funct.Image)
		processor, found := br.store.GetProcessorFromCache(funct.Image)
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
