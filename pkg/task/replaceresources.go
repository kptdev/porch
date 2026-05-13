// Copyright 2024 The kpt Authors
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

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel/trace"
)

var _ mutation = &replaceResourcesMutation{}

type replaceResourcesMutation struct {
	newResources *porchapi.PackageRevisionResources
	oldResources *porchapi.PackageRevisionResources
}

func (m *replaceResourcesMutation) apply(ctx context.Context, resources repository.PackageResources) (repository.PackageResources, *porchapi.TaskResult, error) {
	_, span := tracer.Start(ctx, "mutationReplaceResources::apply", trace.WithAttributes())
	defer span.End()

	old := resources.Contents
	newRes, err := healConfig(old, m.newResources.Spec.Resources)
	if err != nil {
		return repository.PackageResources{}, nil, fmt.Errorf("failed to heal resources: %w", err)
	}
	return repository.PackageResources{Contents: newRes}, nil, nil
}
