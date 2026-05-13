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

package util

import (
	"context"
	"testing"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPackageAlreadyExistsV1Alpha2(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := porchv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&porchv1alpha2.PackageRevision{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PackageRevision",
				APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns",
				Name:      "repo.my-pkg.v1",
			},
			Spec: porchv1alpha2.PackageRevisionSpec{
				RepositoryName: "repo",
				PackageName:    "my-pkg",
			},
		}).Build()

	exists, err := PackageAlreadyExistsV1Alpha2(context.Background(), c, "repo", "my-pkg", "ns")
	assert.NoError(t, err)
	assert.True(t, exists)

	exists, err = PackageAlreadyExistsV1Alpha2(context.Background(), c, "repo", "other-pkg", "ns")
	assert.NoError(t, err)
	assert.False(t, exists)

	exists, err = PackageAlreadyExistsV1Alpha2(context.Background(), c, "other-repo", "my-pkg", "ns")
	assert.NoError(t, err)
	assert.False(t, exists)

	exists, err = PackageAlreadyExistsV1Alpha2(context.Background(), c, "repo", "my-pkg", "other-ns")
	assert.NoError(t, err)
	assert.False(t, exists)
}
