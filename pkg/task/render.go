// Copyright 2022, 2024, 2026 The kpt Authors
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
	"os"

	kptfileapi "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/kptops"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/scheduler"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/klog/v2"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

type renderPackageMutation struct {
	runtime       fn.FunctionRuntime
	runnerOptions runneroptions.RunnerOptions
}

var _ mutation = &renderPackageMutation{}

func (m *renderPackageMutation) apply(ctx context.Context, resources repository.PackageResources) (repository.PackageResources, *porchapi.TaskResult, error) {
	ctx, span := tracer.Start(ctx, "renderPackageMutation::apply", trace.WithAttributes())
	defer span.End()

	tempDir, err := os.MkdirTemp("", "porch-render-*")
	if err != nil {
		return repository.PackageResources{}, nil, fmt.Errorf("couldn't create render temp directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(tempDir); removeErr != nil {
			klog.Warningf("failed to remove render temp directory %q: %v", tempDir, removeErr)
		}
	}()

	fs := filesys.MakeFsOnDisk()
	taskResult := &porchapi.TaskResult{
		RenderStatus: &kptfileapi.RenderStatus{},
	}
	pkgPath, err := scheduler.WriteResources(fs, resources, tempDir)
	if err != nil {
		return repository.PackageResources{}, nil, err
	}

	if pkgPath == "" {
		// We need this for the no-resources case
		// TODO: we should handle this better
		klog.Warningf("skipping render as no package was found")
	} else {
		renderer := kptops.NewRenderer(m.runnerOptions)
		_, err := renderer.Render(ctx, fs, fn.RenderOptions{
			PkgPath: pkgPath,
			Runtime: m.runtime,
		})
		if err != nil {
			// Read back whatever kpt wrote to the filesystem.
			// If the Kptfile has kpt.dev/save-on-render-failure annotation,
			// kpt writes partially-rendered resources; otherwise fs has the original unrendered resources.
			renderedResources, readErr := scheduler.ReadResources(fs, tempDir)
			if readErr != nil {
				klog.Warningf("failed to read resources after render: %v", readErr)
				taskResult.RenderStatus.ErrorSummary = err.Error()
				// Fall back to pre-render resources to avoid wiping package contents
				return resources, taskResult, err
			}
			applyKptfileRenderStatus(taskResult, renderedResources.Contents, err)
			return renderedResources, taskResult, err
		}
	}

	renderedResources, err := scheduler.ReadResources(fs, tempDir)
	if err != nil {
		return repository.PackageResources{}, taskResult, err
	}
	applyKptfileRenderStatus(taskResult, renderedResources.Contents, nil)

	// TODO: There are internal tasks not represented in the API; Update the Apply interface to enable them.
	return renderedResources, taskResult, nil
}

func applyKptfileRenderStatus(taskResult *porchapi.TaskResult, contents map[string]string, renderErr error) {
	if status := kptfileRenderStatus(contents); status != nil {
		taskResult.RenderStatus = status
	}
	if renderErr != nil && taskResult.RenderStatus.ErrorSummary == "" {
		taskResult.RenderStatus.ErrorSummary = renderErr.Error()
	}
}

func kptfileRenderStatus(contents map[string]string) *kptfileapi.RenderStatus {
	raw, ok := contents[kptfileapi.KptFileName]
	if !ok {
		return nil
	}
	kptfile := &kptfileapi.KptFile{}
	if err := yaml.Unmarshal([]byte(raw), kptfile); err != nil || kptfile.Status == nil {
		return nil
	}
	return kptfile.Status.RenderStatus
}
