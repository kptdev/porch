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

package fake

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
)

func TestFakeMemoryStore(t *testing.T) {

	fakeMetadataStore := MemoryMetadataStore{}

	meta1 := metav1.ObjectMeta{
		Name: "my-name",
	}
	objectMeta1, err := fakeMetadataStore.Create(context.TODO(), meta1, "repoName", uuid.NewUUID())
	assert.Equal(t, nil, err)
	assert.Equal(t, objectMeta1.Name, meta1.Name)

	meta2 := metav1.ObjectMeta{
		Name: "your-name",
	}
	objectMeta2, err := fakeMetadataStore.Create(context.TODO(), meta2, "repoName", uuid.NewUUID())
	assert.Equal(t, nil, err)
	assert.Equal(t, objectMeta2.Name, meta2.Name)

	objectMeta3, err := fakeMetadataStore.Create(context.TODO(), meta2, "repoName", uuid.NewUUID())
	assert.Equal(t, nil, err)
	assert.Equal(t, objectMeta3.Name, meta2.Name)

	objectMetaSlice, err := fakeMetadataStore.List(context.TODO(), nil)
	assert.Equal(t, nil, err)
	assert.Equal(t, len(objectMetaSlice), 2)

	objectMeta4, err := fakeMetadataStore.Update(context.TODO(), meta1)
	assert.Equal(t, nil, err)
	assert.Equal(t, objectMeta4.Name, meta1.Name)

	_, err = fakeMetadataStore.Update(context.TODO(), metav1.ObjectMeta{})
	assert.True(t, apierrors.IsNotFound(err))

	nsn1 := types.NamespacedName{
		Name: meta1.Name,
	}

	gotObjectMeta1, err := fakeMetadataStore.Get(context.TODO(), nsn1)
	assert.Equal(t, nil, err)
	assert.Equal(t, gotObjectMeta1.Name, meta1.Name)

	nsn2 := types.NamespacedName{
		Name: "i-dont-exist",
	}

	_, err = fakeMetadataStore.Get(context.TODO(), nsn2)
	assert.True(t, apierrors.IsNotFound(err))

	delObjectMeta1, err := fakeMetadataStore.Delete(context.TODO(), nsn1, true)
	assert.Equal(t, nil, err)
	assert.Equal(t, delObjectMeta1.Name, meta1.Name)

	_, err = fakeMetadataStore.Delete(context.TODO(), nsn2, true)
	assert.True(t, apierrors.IsNotFound(err))
}
