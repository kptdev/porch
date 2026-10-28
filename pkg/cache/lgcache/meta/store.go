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

package meta

import (
	"context"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Store struct {
	Client client.Client
}

func (s *Store) List(ctx context.Context, namespace, repoName string) ([]porchv1alpha2.PackageRevision, error) {
	if s == nil || s.Client == nil {
		return nil, nil
	}
	var list porchv1alpha2.PackageRevisionList
	if err := s.Client.List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{porchv1alpha2.RepositoryLabelKey: repoName},
	); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (s *Store) Get(ctx context.Context, namespace, name string) (*porchv1alpha2.PackageRevision, error) {
	if s == nil || s.Client == nil {
		return nil, nil
	}
	var pr porchv1alpha2.PackageRevision
	if err := s.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

func (s *Store) Update(ctx context.Context, pr *porchv1alpha2.PackageRevision) error {
	if s == nil || s.Client == nil || pr == nil {
		return nil
	}
	return s.Client.Update(ctx, pr)
}
