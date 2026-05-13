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

package oci

import (
	"context"
	"fmt"
	"path/filepath"

	kptoci "github.com/kptdev/kpt/pkg/oci"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
)

var _ externalrepotypes.ExternalRepoFactory = &OciRepoFactory{}

type OciRepoFactory struct {
}

func (f *OciRepoFactory) NewRepositoryImpl(ctx context.Context, repositorySpec *configapi.Repository, options externalrepotypes.ExternalRepoOptions) (repository.Repository, error) {
	if repositorySpec.Spec.Oci == nil {
		return nil, fmt.Errorf("oci not configured")
	}

	ociSpec := repositorySpec.Spec.Oci
	localDir := filepath.Join(options.LocalDirectory, "oci")
	storage, err := kptoci.NewStorage(localDir)
	if err != nil {
		return nil, err
	}

	return &ociRepository{
		key: repository.RepositoryKey{
			Namespace:         repositorySpec.Namespace,
			Name:              repositorySpec.Name,
			Path:              "oci://" + ociSpec.Registry,
			PlaceholderWSname: "oci",
		},
		spec:       *ociSpec.DeepCopy(),
		deployment: repositorySpec.Spec.Deployment,
		storage:    storage,
	}, nil
}

func (f *OciRepoFactory) CheckRepositoryConnection(ctx context.Context, repositorySpec *configapi.Repository, options externalrepotypes.ExternalRepoOptions) error {
	// TODO: check if credentials are valid for the registry
	return nil
}
