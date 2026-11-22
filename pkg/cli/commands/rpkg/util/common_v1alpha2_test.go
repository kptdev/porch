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
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestPackageAlreadyExistsV1Alpha2(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := porchv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("error creating scheme: %v", err)
	}

	c := withFieldIndexes(fake.NewClientBuilder()).
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

// withFieldIndexes mirrors fakeindex.WithV1Alpha2FieldIndexes; that package imports util, so it
// cannot be used here.
func withFieldIndexes(b *fake.ClientBuilder) *fake.ClientBuilder {
	scheme := runtime.NewScheme()
	if err := porchv1alpha2.AddToScheme(scheme); err != nil {
		panic(err)
	}
	b = b.WithScheme(scheme)
	for _, sel := range porchv1alpha2.PackageRevisionSelectableFields {
		if sel == porchv1alpha2.PkgRevSelectorName || sel == porchv1alpha2.PkgRevSelectorNamespace {
			continue
		}
		field := string(sel)
		b = b.WithIndex(&porchv1alpha2.PackageRevision{}, field, func(o client.Object) []string {
			v, _ := V1Alpha2PackageRevisionFieldValue(o.(*porchv1alpha2.PackageRevision), field)
			return []string{v}
		})
	}
	return b
}

func newTestPR(ns, name, repo, pkg string, lifecycle porchv1alpha2.PackageRevisionLifecycle, revision int) *porchv1alpha2.PackageRevision {
	pr := NewV1Alpha2PackageRevision(ns, name)
	pr.Spec.RepositoryName = repo
	pr.Spec.PackageName = pkg
	pr.Spec.Lifecycle = lifecycle
	pr.Status.Revision = revision
	return pr
}

func TestListV1Alpha2PackageRevisionsByFields(t *testing.T) {
	scheme := V1Alpha2Scheme(t)
	objs := []client.Object{
		newTestPR("ns", "repo.pkg.v1", "repo", "pkg", porchv1alpha2.PackageRevisionLifecyclePublished, 1),
		newTestPR("ns", "repo.pkg.v2", "repo", "pkg", porchv1alpha2.PackageRevisionLifecyclePublished, 2),
		newTestPR("ns", "repo.other.v1", "repo", "other", porchv1alpha2.PackageRevisionLifecyclePublished, 1),
		newTestPR("ns2", "repo.pkg.v1", "repo", "pkg", porchv1alpha2.PackageRevisionLifecyclePublished, 1),
	}
	byRepoPkg := client.MatchingFields{
		string(porchv1alpha2.PkgRevSelectorRepository):  "repo",
		string(porchv1alpha2.PkgRevSelectorPackageName): "pkg",
	}

	t.Run("server-side field selector", func(t *testing.T) {
		c := withFieldIndexes(fake.NewClientBuilder()).WithScheme(scheme).WithObjects(objs...).Build()
		got, err := ListV1Alpha2PackageRevisionsByFields(context.Background(), c, "ns", byRepoPkg, 0)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"repo.pkg.v1", "repo.pkg.v2"}, prNames(got))
	})

	t.Run("revision selector", func(t *testing.T) {
		c := withFieldIndexes(fake.NewClientBuilder()).WithScheme(scheme).WithObjects(objs...).Build()
		got, err := ListV1Alpha2PackageRevisionsByFields(context.Background(), c, "ns", client.MatchingFields{
			string(porchv1alpha2.PkgRevSelectorPackageName): "pkg",
			string(porchv1alpha2.PkgRevSelectorRevision):    "2",
		}, 0)
		require.NoError(t, err)
		assert.Equal(t, []string{"repo.pkg.v2"}, prNames(got))
	})

	t.Run("falls back to a local filter when the server rejects the selector", func(t *testing.T) {
		base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
		c := interceptor.NewClient(base, interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				lo := &client.ListOptions{}
				lo.ApplyOptions(opts)
				if lo.FieldSelector != nil && !lo.FieldSelector.Empty() {
					return apierrors.NewBadRequest(`field label not supported: spec.repository`)
				}
				return cl.List(ctx, list, opts...)
			},
		})
		got, err := ListV1Alpha2PackageRevisionsByFields(context.Background(), c, "ns", byRepoPkg, 1)
		require.NoError(t, err)
		assert.Len(t, got, 1)
		assert.Contains(t, []string{"repo.pkg.v1", "repo.pkg.v2"}, got[0].Name)
	})

	t.Run("other list errors are returned", func(t *testing.T) {
		base := fake.NewClientBuilder().WithScheme(scheme).Build()
		c := interceptor.NewClient(base, interceptor.Funcs{
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return apierrors.NewUnauthorized("nope")
			},
		})
		_, err := ListV1Alpha2PackageRevisionsByFields(context.Background(), c, "ns", byRepoPkg, 0)
		assert.True(t, apierrors.IsUnauthorized(err))
	})
}

func prNames(prs []porchv1alpha2.PackageRevision) []string {
	names := make([]string, 0, len(prs))
	for _, pr := range prs {
		names = append(names, pr.Name)
	}
	return names
}
