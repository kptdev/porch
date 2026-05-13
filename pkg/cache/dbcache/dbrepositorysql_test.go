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

package dbcache

import (
	"time"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (t *DbTestSuite) TestRepoDBWriteRead() {

	dbRepo := dbRepository{
		repoKey: repository.RepositoryKey{
			Namespace:         "my-ns",
			Name:              "my-repo",
			Path:              "my-path",
			PlaceholderWSname: "my-placeholder-ws",
		},
		meta:       metav1.ObjectMeta{},
		spec:       nil,
		updated:    time.Now().UTC(),
		updatedBy:  "porchuser",
		deployment: false,
	}

	dbRepoUpdate := dbRepository{
		repoKey: dbRepo.Key(),
		meta: metav1.ObjectMeta{
			Name:      "meta-new-name",
			Namespace: "meta-new-namespace",
		},
		spec:       &configapi.Repository{},
		updated:    time.Now().UTC(),
		updatedBy:  "porchuser2",
		deployment: false,
	}

	t.repoDBWriteReadTest(&dbRepo, &dbRepoUpdate)

	dbRepo.repoKey.Path = ""
	dbRepo.repoKey.PlaceholderWSname = ""
	dbRepoUpdate.repoKey.Path = ""
	dbRepoUpdate.repoKey.PlaceholderWSname = ""
	t.repoDBWriteReadTest(&dbRepo, &dbRepoUpdate)

	dbRepoUpdate = dbRepository{}
	dbRepoUpdate.repoKey = dbRepo.repoKey
	t.repoDBWriteReadTest(&dbRepo, &dbRepoUpdate)
}

func (t *DbTestSuite) TestRepoDBSchema() {
	dbRepo := dbRepository{}
	err := repoWriteToDB(t.Context(), &dbRepo)
	t.Require().ErrorContains(err, "violates check constraint")

	dbRepo.repoKey = repository.RepositoryKey{
		Namespace: "my-namespace",
	}

	err = repoWriteToDB(t.Context(), &dbRepo)
	t.Require().ErrorContains(err, "violates check constraint")

	dbRepo.repoKey.Name = "my-name"
	err = repoWriteToDB(t.Context(), &dbRepo)
	t.Require().NoError(err)

	dbRepo.repoKey.Path = "my-path"
	err = repoUpdateDB(t.Context(), &dbRepo)
	t.Require().ErrorContains(err, "update not allowed on immutable columns")

	dbRepo.repoKey.Path = ""
	dbRepo.updatedBy = "homer"
	err = repoUpdateDB(t.Context(), &dbRepo)
	t.Require().NoError(err)

	dbRepo.repoKey.PlaceholderWSname = "my-ws"
	err = repoUpdateDB(t.Context(), &dbRepo)
	t.Require().ErrorContains(err, "update not allowed on immutable columns")

	dbRepo.repoKey.PlaceholderWSname = ""
	dbRepo.updatedBy = "lisa"
	err = repoUpdateDB(t.Context(), &dbRepo)
	t.Require().NoError(err)

	err = repoDeleteFromDB(t.Context(), dbRepo.Key())
	t.Require().NoError(err)
}

func (t *DbTestSuite) repoDBWriteReadTest(dbRepo, dbRepoUpdate *dbRepository) {
	err := repoWriteToDB(t.Context(), dbRepo)
	t.Require().NoError(err)

	readRepo, err := repoReadFromDB(t.Context(), dbRepo.Key())
	t.Require().NoError(err)
	t.assertReposEqual(dbRepo, readRepo)

	err = repoWriteToDB(t.Context(), dbRepoUpdate)
	t.Require().ErrorContains(err, "violates unique constraint")

	err = repoUpdateDB(t.Context(), dbRepoUpdate)
	t.Require().NoError(err)

	readRepo, err = repoReadFromDB(t.Context(), dbRepo.Key())
	t.Require().NoError(err)
	t.assertReposEqual(dbRepoUpdate, readRepo)

	err = repoDeleteFromDB(t.Context(), dbRepo.Key())
	t.Require().NoError(err)

	_, err = repoReadFromDB(t.Context(), dbRepo.Key())
	t.Require().ErrorContains(err, "no rows in result set")

	err = repoUpdateDB(t.Context(), dbRepoUpdate)
	t.Require().ErrorContains(err, "no rows or multiple rows found for updating")

	err = repoDeleteFromDB(t.Context(), dbRepo.Key())
	t.Require().NoError(err)
}

func (t *DbTestSuite) assertReposEqual(left, right *dbRepository) {
	t.Equal(left.Key(), right.Key())
	t.Equal(left.meta.Namespace, right.meta.Namespace)
	t.Equal(left.meta.Name, right.meta.Name)
	t.Equal(left.spec, right.spec)
	t.Equal(left.updatedBy, right.updatedBy)
}
