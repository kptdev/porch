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
package repositories

import (
	"slices"

	"github.com/kptdev/porch/test/e2e/suiteutils"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
)

func Backup(t *suiteutils.MultiClusterTestSuite) *configapi.RepositoryList {
	t.T().Helper()

	var repos configapi.RepositoryList
	t.ListF(&repos, client.InNamespace(t.Namespace))
	t.Logf("Backed up %d Porch Repository objects", len(repos.Items))
	return &repos
}

func Restore(t *suiteutils.MultiClusterTestSuite, repos *configapi.RepositoryList, batchSize int) {
	t.T().Helper()

	t.Logf("Restoring %d Porch Repository objects in batches of %d", len(repos.Items), batchSize)
	for batch := range slices.Chunk(repos.Items, batchSize) {
		for _, each := range batch {
			t.CreateOrUpdateE(&each)
		}

		t.WaitUntilMultipleRepositoriesReady(batch)
	}
}
