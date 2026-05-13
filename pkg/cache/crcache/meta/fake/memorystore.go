// Copyright 2022, 2025 The kpt Authors
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

package fake

import (
	"context"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/crcache/meta"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// MemoryMetadataStore is an in-memory implementation of the MetadataStore interface. It
// means metadata about packagerevisions will be stored in memory, which is useful for testing.
type MemoryMetadataStore struct {
	Metas []metav1.ObjectMeta
}

var _ meta.MetadataStore = &MemoryMetadataStore{}

func (m *MemoryMetadataStore) Get(ctx context.Context, namespacedName types.NamespacedName) (metav1.ObjectMeta, error) {
	for _, meta := range m.Metas {
		if meta.Name == namespacedName.Name && meta.Namespace == namespacedName.Namespace {
			return meta, nil
		}
	}
	return metav1.ObjectMeta{}, apierrors.NewNotFound(
		schema.GroupResource{Group: "config.kpt.dev", Resource: "packagerevisions"},
		namespacedName.Name,
	)
}

func (m *MemoryMetadataStore) List(ctx context.Context, repo *configapi.Repository) ([]metav1.ObjectMeta, error) {
	return m.Metas, nil
}

func (m *MemoryMetadataStore) Create(ctx context.Context, pkgRevMeta metav1.ObjectMeta, repoName string, pkgRevUID types.UID) (metav1.ObjectMeta, error) {
	for _, meta := range m.Metas {
		if meta.Name == pkgRevMeta.Name && meta.Namespace == pkgRevMeta.Namespace {
			return m.Update(ctx, pkgRevMeta)
		}
	}
	m.Metas = append(m.Metas, pkgRevMeta)
	return pkgRevMeta, nil
}

func (m *MemoryMetadataStore) Update(ctx context.Context, pkgRevMeta metav1.ObjectMeta) (metav1.ObjectMeta, error) {
	i := -1
	for j, m := range m.Metas {
		if m.Name == pkgRevMeta.Name && m.Namespace == pkgRevMeta.Namespace {
			i = j
		}
	}
	if i < 0 {
		return metav1.ObjectMeta{}, apierrors.NewNotFound(
			schema.GroupResource{Group: "config.porch.kpt.dev", Resource: "packagerevisions"},
			pkgRevMeta.Name,
		)
	}
	m.Metas[i] = pkgRevMeta
	return pkgRevMeta, nil
}

func (m *MemoryMetadataStore) Delete(ctx context.Context, namespacedName types.NamespacedName, _ bool) (metav1.ObjectMeta, error) {
	var metas []metav1.ObjectMeta
	found := false
	var deletedMeta metav1.ObjectMeta
	for _, m := range m.Metas {
		if m.Name == namespacedName.Name && m.Namespace == namespacedName.Namespace {
			found = true
			deletedMeta = m
		} else {
			metas = append(metas, m)
		}
	}
	if !found {
		return metav1.ObjectMeta{}, apierrors.NewNotFound(
			schema.GroupResource{Group: "config.kpt.dev", Resource: "packagerevisions"},
			namespacedName.Name,
		)
	}
	m.Metas = metas
	return deletedMeta, nil
}
