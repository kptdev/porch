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

	"github.com/kptdev/kpt/pkg/fn"
	"github.com/kptdev/kpt/pkg/lib/builtins/builtintypes"
	"github.com/kptdev/kpt/pkg/lib/runneroptions"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel"
)

var tracer = otel.Tracer("task")

type TaskHandler interface {
	GetRuntime() fn.FunctionRuntime

	SetRunnerOptionsResolver(func(namespace string) runneroptions.RunnerOptions)
	SetRuntime(fn.FunctionRuntime)
	SetRepoOpener(repository.RepositoryOpener)
	SetCredentialResolver(repository.CredentialResolver)
	SetReferenceResolver(repository.ReferenceResolver)
	SetRepoOperationRetryAttempts(int)

	ApplyTask(ctx context.Context, draft repository.PackageRevisionDraft, repositoryObj *configapi.Repository, obj *porchapi.PackageRevision, packageConfig *builtintypes.PackageConfig) error
	DoPRMutations(ctx context.Context, repoPR repository.PackageRevision, oldObj *porchapi.PackageRevision, newObj *porchapi.PackageRevision, draft repository.PackageRevisionDraft) error
	DoPRResourceMutations(ctx context.Context, pr2Update repository.PackageRevision, draft repository.PackageRevisionDraft, oldRes, newRes *porchapi.PackageRevisionResources) (*porchapi.RenderStatus, error)
}

type mutation interface {
	apply(ctx context.Context, resources repository.PackageResources) (repository.PackageResources, *porchapi.TaskResult, error)
}

func GetDefaultTaskHandler() TaskHandler {
	return &genericTaskHandler{}
}
