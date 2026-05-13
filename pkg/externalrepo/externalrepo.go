// Copyright 2025 The kpt Authors
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

/*
Package externalrepo provides an interface and implementations to external repositories that store packages.
*/
package externalrepo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/externalrepo/git"
	"github.com/kptdev/porch/pkg/externalrepo/oci"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracer                          = otel.Tracer("externalrepo")
	ExternalRepoInUnitTestMode bool = false
)

func CreateRepositoryImpl(ctx context.Context, repositorySpec *configapi.Repository, options externalrepotypes.ExternalRepoOptions) (repository.Repository, error) {
	ctx, span := tracer.Start(ctx, "Repository::RepositoryFactory", trace.WithAttributes())
	defer span.End()

	var repoFactory externalrepotypes.ExternalRepoFactory

	switch repositoryType := repositorySpec.Spec.Type; repositoryType {
	case configapi.RepositoryTypeOCI:
		repoFactory = new(oci.OciRepoFactory)

	case configapi.RepositoryTypeGit:
		repoFactory = new(git.GitRepoFactory)

	default:
		if ExternalRepoInUnitTestMode {
			return &fake.Repository{}, nil
		} else {
			return nil, fmt.Errorf("type %q not supported", repositoryType)
		}
	}

	return repoFactory.NewRepositoryImpl(ctx, repositorySpec, options)
}

func CheckRepositoryConnection(ctx context.Context, repo *configapi.Repository, options externalrepotypes.ExternalRepoOptions) error {
	ctx, span := tracer.Start(ctx, "Repository::CheckRepositoryConnection", trace.WithAttributes())
	defer span.End()

	var repoFactory externalrepotypes.ExternalRepoFactory

	switch repo.Spec.Type {
	case configapi.RepositoryTypeOCI:
		repoFactory = new(oci.OciRepoFactory)
	case configapi.RepositoryTypeGit:
		repoFactory = new(git.GitRepoFactory)
	default:
		return fmt.Errorf("unsupported repository type: %q", repo.Spec.Type)
	}

	return repoFactory.CheckRepositoryConnection(ctx, repo, options)

}

func RepositoryKey(repositorySpec *configapi.Repository) (repository.RepositoryKey, error) {
	switch repositoryType := repositorySpec.Spec.Type; repositoryType {
	case configapi.RepositoryTypeOCI:
		ociSpec := repositorySpec.Spec.Oci
		if ociSpec == nil {
			return repository.RepositoryKey{}, fmt.Errorf("oci not configured")
		}
		return repository.RepositoryKey{
			Namespace:         repositorySpec.Namespace,
			Name:              repositorySpec.Name,
			Path:              "oci://" + ociSpec.Registry,
			PlaceholderWSname: "OCI",
		}, nil

	case configapi.RepositoryTypeGit:
		gitSpec := repositorySpec.Spec.Git
		if gitSpec == nil {
			return repository.RepositoryKey{}, errors.New("git property is required")
		}
		if gitSpec.Repo == "" {
			return repository.RepositoryKey{}, errors.New("git.repo property is required")
		}
		return repository.RepositoryKey{
			Namespace:         repositorySpec.Namespace,
			Name:              repositorySpec.Name,
			Path:              strings.Trim(gitSpec.Directory, "/"),
			PlaceholderWSname: gitSpec.Branch,
		}, nil

	default:
		if ExternalRepoInUnitTestMode {
			return repository.RepositoryKey{
				Namespace: repositorySpec.Namespace,
				Name:      repositorySpec.Name,
			}, nil
		} else {
			return repository.RepositoryKey{}, fmt.Errorf("repository type %q not supported", repositoryType)
		}
	}
}
