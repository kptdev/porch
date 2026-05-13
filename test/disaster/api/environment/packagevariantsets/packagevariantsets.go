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
package packagevariantsets

import (
	"slices"

	"github.com/kptdev/porch/test/e2e/suiteutils"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pvsetapi "github.com/kptdev/porch/controllers/packagevariantsets/api/v1alpha2"
)

func Backup(t *suiteutils.MultiClusterTestSuite) *pvsetapi.PackageVariantSetList {
	t.T().Helper()

	var variantSets pvsetapi.PackageVariantSetList
	t.ListF(&variantSets, client.InNamespace(t.Namespace))
	return &variantSets
}

func Restore(t *suiteutils.MultiClusterTestSuite, variants *pvsetapi.PackageVariantSetList, batchSize int) {
	t.T().Helper()

	for batch := range slices.Chunk(variants.Items, batchSize) {
		for _, each := range batch {
			t.CreateOrUpdateE(&each)
		}

		t.WaitUntilAllPackageVariantsReady()
	}
}
