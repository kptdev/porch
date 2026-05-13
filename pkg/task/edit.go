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

package task

import (
	"context"
	"fmt"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
)

type editPackageMutation struct {
	task              *porchapi.Task
	namespace         string
	repositoryName    string
	packageName       string
	repoOpener        repository.RepositoryOpener
	referenceResolver repository.ReferenceResolver
}

var _ mutation = &editPackageMutation{}

func (m *editPackageMutation) apply(ctx context.Context, resources repository.PackageResources) (repository.PackageResources, *porchapi.TaskResult, error) {
	ctx, span := tracer.Start(ctx, "editPackageMutation::apply", trace.WithAttributes())
	defer span.End()

	sourceRef := m.task.Edit.Source

	sourceRevision, err := (&repository.PackageFetcher{
		RepoOpener:        m.repoOpener,
		ReferenceResolver: m.referenceResolver,
	}).FetchRevision(ctx, sourceRef, m.namespace)
	if err != nil {
		return repository.PackageResources{}, nil, fmt.Errorf("failed to fetch package %q: %w", sourceRef.Name, err)
	}

	// We only allow edit to create new revision from the same package.
	if sourceRevision.Key().PkgKey.ToPkgPathname() != m.packageName ||
		sourceRevision.Key().RKey().Name != m.repositoryName {
		return repository.PackageResources{}, nil, fmt.Errorf("source revision must be from same package %s/%s", m.repositoryName, m.packageName)
	}

	sourceIsPlaceholder, err := repository.PackageRevisionIsPlaceholder(ctx, m.namespace, m.referenceResolver, sourceRevision)
	if err != nil {
		return repository.PackageResources{}, nil, pkgerrors.Wrap(err, "error checking for placeholder package revision")
	}
	if sourceIsPlaceholder {
		// We only allow edit to create new revisions from non-placeholder package revisions
		return repository.PackageResources{}, nil, fmt.Errorf("source revision may not be the placeholder package revision %s/%s", sourceRevision.Key().RKey().Name, sourceRevision.KubeObjectName())
	}

	// We only allow edit to create new revisions from published package revisions.
	if !porchapi.LifecycleIsPublished(sourceRevision.Lifecycle(ctx)) {
		return repository.PackageResources{}, nil, fmt.Errorf("source revision must be published")
	}

	sourceResources, err := sourceRevision.GetResources(ctx)
	if err != nil {
		return repository.PackageResources{}, nil, fmt.Errorf("cannot read contents of package %q: %w", sourceRef.Name, err)
	}

	return repository.PackageResources{
		Contents: sourceResources.Spec.Resources,
	}, &porchapi.TaskResult{Task: m.task}, nil
}
