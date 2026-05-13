// Copyright 2025-2026 The kpt Authors
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

package testutil

import (
	"context"
	"fmt"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"
	k8sfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// FakeClientWithStatusUpdate is a fake client that supports status updates
type FakeClientWithStatusUpdate struct {
	client.Client
	statusStore map[types.NamespacedName]configapi.RepositoryStatus
}

func NewFakeClientWithStatus(scheme *runtime.Scheme, objs ...client.Object) *FakeClientWithStatusUpdate {
	baseClient := k8sfake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &FakeClientWithStatusUpdate{
		Client:      baseClient,
		statusStore: make(map[types.NamespacedName]configapi.RepositoryStatus),
	}
}

func (f *FakeClientWithStatusUpdate) Status() client.StatusWriter {
	return &fakeStatusWriter{f}
}

type fakeStatusWriter struct {
	f *FakeClientWithStatusUpdate
}

func (w *fakeStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	repo, ok := obj.(*configapi.Repository)
	if !ok {
		return fmt.Errorf("status update only supported for Repository objects")
	}
	key := types.NamespacedName{Name: repo.Name, Namespace: repo.Namespace}
	w.f.statusStore[key] = repo.Status
	return nil
}

func (w *fakeStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return nil
}

func (w *fakeStatusWriter) Create(ctx context.Context, obj client.Object, subresource client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}

func (f *FakeClientWithStatusUpdate) Watch(ctx context.Context, list client.ObjectList, opts ...client.ListOption) (watch.Interface, error) {
	return watch.NewEmptyWatch(), nil
}

func (f *FakeClientWithStatusUpdate) GetStatusStore() map[types.NamespacedName]configapi.RepositoryStatus {
	return f.statusStore
}
