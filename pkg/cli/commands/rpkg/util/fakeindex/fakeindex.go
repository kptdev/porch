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

// Package fakeindex is a test helper. It lives in its own package so the controller-runtime fake
// client is not linked into porchctl.
package fakeindex

import (
	"fmt"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/util"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// WithV1Alpha2FieldIndexes registers the PackageRevision selectable fields as indexes on a fake
// client builder, so field-selector Lists behave like the API server's CRD selectableFields.
// Registering an index needs a scheme, so it sets one with v1alpha2; a later WithScheme with
// another scheme that also has v1alpha2 keeps the indexes.
func WithV1Alpha2FieldIndexes(b *fake.ClientBuilder) *fake.ClientBuilder {
	scheme := runtime.NewScheme()
	if err := porchv1alpha2.AddToScheme(scheme); err != nil {
		panic(fmt.Sprintf("register v1alpha2 scheme: %v", err))
	}
	b = b.WithScheme(scheme)
	for _, sel := range porchv1alpha2.PackageRevisionSelectableFields {
		if sel == porchv1alpha2.PkgRevSelectorName || sel == porchv1alpha2.PkgRevSelectorNamespace {
			continue // handled natively by the fake client
		}
		field := string(sel)
		b = b.WithIndex(&porchv1alpha2.PackageRevision{}, field, func(o client.Object) []string {
			v, _ := util.V1Alpha2PackageRevisionFieldValue(o.(*porchv1alpha2.PackageRevision), field)
			return []string{v}
		})
	}
	return b
}
