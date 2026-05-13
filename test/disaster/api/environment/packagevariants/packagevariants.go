// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package packagevariants

import (
	"slices"

	"github.com/kptdev/porch/test/e2e/suiteutils"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pvapi "github.com/kptdev/porch/controllers/packagevariants/api/v1alpha1"
	"github.com/kptdev/porch/controllers/packagevariantsets/pkg/controllers/packagevariantset"
)

func Backup(t *suiteutils.MultiClusterTestSuite) *pvapi.PackageVariantList {
	t.T().Helper()

	var variants pvapi.PackageVariantList
	t.ListF(&variants, client.InNamespace(t.Namespace))
	variants.Items = slices.DeleteFunc(variants.Items, func(aVariant pvapi.PackageVariant) bool {
		_, createdByVariantSet := aVariant.Labels[packagevariantset.PackageVariantSetOwnerLabel]
		return createdByVariantSet
	})
	return &variants
}

func Restore(t *suiteutils.MultiClusterTestSuite, variants *pvapi.PackageVariantList, batchSize int) {
	t.T().Helper()

	for batch := range slices.Chunk(variants.Items, batchSize) {
		for _, each := range batch {
			t.CreateOrUpdateE(&each)
		}

		t.WaitUntilAllPackageVariantsReady()
	}
}
