/*
 Copyright 2025 The kpt Authors

 Licensed under the Apache License, Version 2.0 (the "License");
 You may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package meta

import (
	"context"
	"errors"
	"testing"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/internal/api/porchinternal/v1alpha1"
	internalapi "github.com/kptdev/porch/internal/api/porchinternal/v1alpha1"
	mockclient "github.com/kptdev/porch/test/mockery/mocks/external/sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCreateUpdateDeleteOK(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	ctxt := context.TODO()

	store := NewCrdMetadataStore(mockClient)
	assert.Equal(t, mockClient, store.coreClient)

	repo := configapi.Repository{}
	pkgRevMeta := metav1.ObjectMeta{
		Name:      "my-name",
		Namespace: "my-namespace",
	}

	mockClient.EXPECT().Create(mock.Anything, mock.Anything).Return(nil)
	newPkgRevMeta, err := store.Create(ctxt, pkgRevMeta, repo.Name, uuid.NewUUID())
	assert.True(t, err == nil)
	assert.Equal(t, pkgRevMeta.Name, newPkgRevMeta.Name)

	prKey := types.NamespacedName{
		Name:      pkgRevMeta.Name,
		Namespace: pkgRevMeta.Namespace,
	}
	internalPkgRev := v1alpha1.PackageRev{}

	mockClient.EXPECT().
		Get(mock.Anything, prKey, &internalPkgRev).
		Return(nil).
		Run(func(_ context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			obj.(*v1alpha1.PackageRev).Name = prKey.Name
			obj.(*v1alpha1.PackageRev).Namespace = prKey.Namespace
		})
	gotPR, err := store.Get(ctxt, prKey)
	assert.True(t, err == nil)
	assert.Equal(t, gotPR.Name, prKey.Name)

	internalPkgRevList := internalapi.PackageRevList{}
	mockClient.EXPECT().
		List(mock.Anything, &internalPkgRevList, mock.Anything, mock.Anything).
		Return(nil).
		Run(func(_ context.Context, list client.ObjectList, opts ...client.ListOption) {
			list.(*v1alpha1.PackageRevList).Items = make([]v1alpha1.PackageRev, 1)
			list.(*v1alpha1.PackageRevList).Items[0] = v1alpha1.PackageRev{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pkgRevMeta.Namespace,
					Name:      pkgRevMeta.Name,
				},
			}
		})
	prList, err := store.List(ctxt, &repo)
	assert.True(t, err == nil)
	assert.Equal(t, prList[0].Name, prKey.Name)

	mockClient.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)
	updPkgRevMeta, err := store.Update(ctxt, newPkgRevMeta)
	assert.True(t, err == nil)
	assert.Equal(t, pkgRevMeta.Name, updPkgRevMeta.Name)

	mockClient.EXPECT().Delete(mock.Anything, mock.Anything).Return(nil)
	delPkgRevMeta, err := store.Delete(ctxt, prKey, true)
	assert.True(t, err == nil)
	assert.Equal(t, delPkgRevMeta.Name, prKey.Name)
}

func TestCreateAlreadyExists(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	ctxt := context.TODO()

	store := NewCrdMetadataStore(mockClient)
	assert.Equal(t, mockClient, store.coreClient)

	repo := configapi.Repository{}
	pkgRevMeta := metav1.ObjectMeta{
		Name:      "my-name",
		Namespace: "my-namespace",
	}

	expectedError := apierrors.NewAlreadyExists(schema.GroupResource{}, "any-name")
	mockClient.EXPECT().Create(mock.Anything, mock.Anything).Return(expectedError)
	mockClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("get error1"))
	_, err := store.Create(ctxt, pkgRevMeta, repo.Name, uuid.NewUUID())
	assert.True(t, err != nil)
}

func TestStoreErrors(t *testing.T) {
	mockClient := mockclient.NewMockClient(t)
	ctxt := context.TODO()

	store := NewCrdMetadataStore(mockClient)
	assert.Equal(t, mockClient, store.coreClient)

	nsn := types.NamespacedName{}
	mockClient.EXPECT().Get(mock.Anything, nsn, mock.Anything).Return(errors.New("get error1"))
	_, err := store.Get(ctxt, nsn)
	assert.True(t, err != nil)

	repo := configapi.Repository{}
	mockClient.EXPECT().List(mock.Anything, mock.Anything, client.InNamespace(""), mock.Anything).Return(errors.New("get error"))
	_, err = store.List(ctxt, &repo)
	assert.True(t, err != nil)

	pkgRevMeta := metav1.ObjectMeta{}
	mockClient.EXPECT().Create(mock.Anything, mock.Anything).Return(errors.New("get error2"))
	_, err = store.Create(ctxt, pkgRevMeta, repo.Name, uuid.NewUUID())
	assert.True(t, err != nil)

	mockClient.EXPECT().Get(mock.Anything, nsn, mock.Anything).Return(errors.New("get error3"))
	_, err = store.Delete(ctxt, nsn, true)
	assert.True(t, err != nil)
}
