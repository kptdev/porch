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
	"context"
	"errors"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	mockcachetypes "github.com/kptdev/porch/test/mockery/mocks/porch/pkg/cache/types"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (t *DbTestSuite) TestDBPackageRevision() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	repoName := "my-repo-name"
	namespace := "my-ns"
	workspace := "my-workspace"
	branch := "my-branch"
	externalrepo.ExternalRepoInUnitTestMode = true

	ctx := t.Context()

	testRepo := t.createTestRepo(namespace, repoName)
	testRepo.spec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo: "https://aurl/repo.git",
			},
		},
	}
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)

	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: repoName,
			PackageName:    "my-package",
			WorkspaceName:  workspace,
		},
	}

	newPRDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)
	t.Require().NotNil(newPRDraft)

	dbPR, err := testRepo.ClosePackageRevisionDraft(ctx, newPRDraft, -1)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR)

	t.Equal("main", dbPR.ToMainPackageRevision(ctx).Key().WorkspaceName)
	dbPR.(*dbPackageRevision).pkgRevKey.PkgKey.RepoKey.PlaceholderWSname = branch
	t.Equal(branch, dbPR.ToMainPackageRevision(ctx).Key().WorkspaceName)

	meta := dbPR.GetMeta()
	t.Equal(meta.Name, "")

	t.Require().Nil(dbPR.SetMeta(ctx, metav1.ObjectMeta{}))

	prDef, err := dbPR.GetPackageRevision(ctx)
	t.Require().NoError(err)
	t.Equal("my-workspace", prDef.Spec.WorkspaceName)

	t.Equal("my-ns", dbPR.KubeObjectNamespace())
	t.Equal("my-repo-name.my-package.my-workspace", dbPR.KubeObjectName())
	prKey := repository.PackageRevisionKey{
		PkgKey: repository.PackageKey{
			RepoKey: repository.RepositoryKey{
				Namespace:         namespace,
				Name:              repoName,
				PlaceholderWSname: branch,
			},
			Package: "my-package",
		},
		WorkspaceName: workspace,
	}
	t.Equal(prKey, dbPR.Key())
	t.Equal(porchapi.PackageRevisionLifecycleDraft, dbPR.Lifecycle(ctx))

	newPrUp, newPrUpLock, err := dbPR.GetUpstreamLock(ctx)
	t.Require().NoError(err)
	t.Require().Nil(newPrUp.Git)
	t.Require().Nil(newPrUpLock.Git)

	newPrUp, newPrUpLock, err = dbPR.GetLock(ctx)
	t.Require().NoError(err)
	t.Require().NotNil(newPrUp.Git)
	t.Require().NotNil(newPrUpLock.Git)

	prResources, err := dbPR.GetResources(ctx)
	t.Require().NoError(err)
	t.Require().NotNil(prResources)
	t.Equal(0, len(prResources.Spec.Resources))

	newDBPR := dbPR.(*dbPackageRevision)

	prResources.Spec.Resources["Kptfile"] = `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: my-kptfile
  labels:
    app: my-app
    team: platform
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  site: https://kpt.dev
  description: some kpt package.`

	err = newDBPR.UpdateResources(ctx, prResources, &porchapi.Task{})
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR)

	gotKptFile, err := newDBPR.GetKptfile(ctx)
	t.Require().NoError(err)
	t.Equal("Kptfile", gotKptFile.Kind)

	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycleProposed)
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR)

	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished)
	t.Require().NoError(err)
	t.Require().True(dbPR.(*dbPackageRevision).latest, "expected latest to be true after publishing")

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR)

	// Verify that PackageMetadata (Kptfile labels) persists on the published revision itself
	prDef, err = dbPR.GetPackageRevision(ctx)
	t.Require().NoError(err)
	t.Require().NotNil(prDef.Spec.PackageMetadata, "PackageMetadata should be present on published revision")
	t.Equal("my-app", prDef.Spec.PackageMetadata.Labels["app"])
	t.Equal("platform", prDef.Spec.PackageMetadata.Labels["team"])

	// After publishing, the main revision is automatically created in the DB.
	// Verify that PackageMetadata (Kptfile labels) persists on it.
	mainKey := repository.PackageRevisionKey{
		PkgKey:        dbPR.Key().PKey(),
		Revision:      -1,
		WorkspaceName: branch,
	}
	mainFromDB, err := pkgRevReadFromDB(ctx, mainKey, false)
	t.Require().NoError(err)
	t.Require().NotNil(mainFromDB)

	mainPRDef, err := mainFromDB.GetPackageRevision(ctx)
	t.Require().NoError(err)
	t.Require().NotNil(mainPRDef.Spec.PackageMetadata, "PackageMetadata should be present on main revision after publish")
	t.Equal("my-app", mainPRDef.Spec.PackageMetadata.Labels["app"])
	t.Equal("platform", mainPRDef.Spec.PackageMetadata.Labels["team"])

	dbPRdb := dbPR.(*dbPackageRevision)
	dbPR2 := dbPackageRevision{
		repo: dbPRdb.repo,
		pkgRevKey: repository.PackageRevisionKey{
			PkgKey:        dbPR.Key().PKey(),
			Revision:      0,
			WorkspaceName: "my-workspace-2",
		},
		lifecycle: porchapi.PackageRevisionLifecycleDraft,
		tasks:     dbPRdb.tasks,
		resources: dbPRdb.resources,
	}

	err = dbPR2.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycleProposed)
	t.Require().NoError(err)

	dbPR2i, err := testRepo.ClosePackageRevisionDraft(ctx, &dbPR2, 0)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR2i)

	err = dbPR2i.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished)
	t.Require().NoError(err)

	dbPR2i, err = testRepo.ClosePackageRevisionDraft(ctx, &dbPR2, 0)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR2i)

	fakeRepo := testRepo.externalRepo.(*fake.Repository)
	fakeExtPR := fake.FakePackageRevision{
		PrKey: dbPR.Key(),
	}
	fakeRepo.PackageRevisions = append(fakeRepo.PackageRevisions, &fakeExtPR)

	dbPR.(*dbPackageRevision).lifecycle = porchapi.PackageRevisionLifecyclePublished
	dbPR.(*dbPackageRevision).pkgRevKey.Revision = 1
	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycleDeletionProposed)
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR)

	prDef, err = dbPR.GetPackageRevision(ctx)
	t.Require().NoError(err)
	t.Equal(porchapi.PackageRevisionLifecycleDeletionProposed, prDef.Spec.Lifecycle)

	prResources.Spec.Resources["Kptfile"] = `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: my-kptfile
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  site: https://kpt.dev
  description: some kpt package.
upstream:
  type: git
  git:
    repo: http://172.18.255.200:3000/porch/rpkg-update.git
    directory: basens-edit
    ref: drafts/basens-edit/update-1
upstreamLock:
  type: git
  git:
    repo: http://172.18.255.200:3000/porch/rpkg-update.git
    directory: basens-edit
    ref: drafts/basens-edit/update-1
    commit: 960e1b80b5245874e46ba2b3090b27deaa61eb9a`

	newDBPR2, err := pkgRevReadFromDB(ctx, dbPR.Key(), true)
	t.Require().NoError(err)

	err = newDBPR2.UpdateResources(ctx, prResources, &porchapi.Task{})
	t.Require().NoError(err)

	t.Require().False(newDBPR2.IsLatestRevision())

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, newDBPR2, 0)
	t.Require().NoError(err)
	t.Require().NotNil(dbPR)

	prDef, err = dbPR.GetPackageRevision(ctx)
	t.Require().NoError(err)
	t.Equal("basens-edit", prDef.Status.UpstreamLock.Git.Directory)

	// Kptfile has no labels, so PackageMetadata should have nil labels
	t.Require().NotNil(prDef.Spec.PackageMetadata)
	t.Len(prDef.Spec.PackageMetadata.Labels, 0, "expected no labels when Kptfile has none")

	err = testRepo.DeletePackageRevision(ctx, dbPR)
	t.Require().NoError(err)

	err = testRepo.Close(ctx)
	t.Require().NoError(err)
}

func (t *DbTestSuite) TestDBPackageRevisionDeleteWithNotFoundError() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	repoName := "test-repo"
	namespace := "test-ns"
	externalrepo.ExternalRepoInUnitTestMode = true

	ctx := t.Context()

	testRepo := t.createTestRepo(namespace, repoName)
	testRepo.spec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo: "https://example.com/repo.git",
			},
		},
	}
	mockCache.EXPECT().GetRepository(mock.Anything).Return(testRepo).Maybe()

	err := testRepo.OpenRepository(ctx, externalrepotypes.ExternalRepoOptions{})
	t.Require().NoError(err)

	// Create a published package revision
	newPRDef := porchapi.PackageRevision{
		Spec: porchapi.PackageRevisionSpec{
			RepositoryName: repoName,
			PackageName:    "test-package",
			WorkspaceName:  "test-workspace",
		},
	}

	newPRDraft, err := testRepo.CreatePackageRevisionDraft(ctx, &newPRDef)
	t.Require().NoError(err)

	dbPR, err := testRepo.ClosePackageRevisionDraft(ctx, newPRDraft, -1)
	t.Require().NoError(err)

	// Update to published lifecycle
	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecycleProposed)
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)

	err = dbPR.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished)
	t.Require().NoError(err)

	dbPR, err = testRepo.ClosePackageRevisionDraft(ctx, dbPR.(repository.PackageRevisionDraft), 0)
	t.Require().NoError(err)

	// Replace external repo with one that returns "not found" error
	testRepo.externalRepo = &fakeRepoWithDeleteError{}

	// Delete should succeed despite external repo error
	err = testRepo.DeletePackageRevision(ctx, dbPR)
	t.Require().NoError(err)

	err = testRepo.Close(ctx)
	t.Require().NoError(err)
}

type fakeRepoWithDeleteError struct {
	fake.Repository
}

func (r *fakeRepoWithDeleteError) DeletePackageRevision(context.Context, repository.PackageRevision) error {
	return errors.New("package not found")
}

func (t *DbTestSuite) TestDBDeleteLatestRevision() {
	// Test that the async notification logic is triggered
	ctx := t.Context()

	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache

	dbRepo := t.createTestRepo("test-ns", "test-repo")
	mockCache.EXPECT().GetRepository(mock.Anything).Return(dbRepo)
	dbPkg := t.createTestPkg(dbRepo.Key(), "test-package")
	dbPkg.repo = dbRepo

	// Create and write first package revision to database
	dbPR1 := dbPackageRevision{
		repo: dbRepo,
		pkgRevKey: repository.PackageRevisionKey{
			PkgKey:        dbPkg.Key(),
			WorkspaceName: "workspace-1",
			Revision:      1,
		},
		meta:      metav1.ObjectMeta{},
		spec:      &porchapi.PackageRevisionSpec{},
		updated:   time.Now().UTC(),
		updatedBy: "testuser",
		lifecycle: porchapi.PackageRevisionLifecyclePublished,
		latest:    false,
		resources: map[string]string{},
	}

	// Create and write main package revision to make sure len(prSlice) > 0
	dbPRMain := dbPackageRevision{
		repo: dbRepo,
		pkgRevKey: repository.PackageRevisionKey{
			PkgKey:        dbPkg.Key(),
			WorkspaceName: "main",
			Revision:      -1,
		},
		meta:      metav1.ObjectMeta{},
		spec:      &porchapi.PackageRevisionSpec{},
		updated:   time.Now().UTC(),
		updatedBy: "testuser",
		lifecycle: porchapi.PackageRevisionLifecyclePublished,
		resources: map[string]string{},
	}

	err := pkgRevWriteToDB(ctx, &dbPR1)
	t.Require().NoError(err)

	err = pkgRevWriteToDB(ctx, &dbPRMain)
	t.Require().NoError(err)

	// Create and write second package revision to database (this will be latest)
	dbPR2 := dbPackageRevision{
		repo: dbRepo,
		pkgRevKey: repository.PackageRevisionKey{
			PkgKey:        dbPkg.Key(),
			WorkspaceName: "workspace-2",
			Revision:      2,
		},
		meta:      metav1.ObjectMeta{},
		spec:      &porchapi.PackageRevisionSpec{},
		updated:   time.Now().UTC(),
		updatedBy: "testuser",
		lifecycle: porchapi.PackageRevisionLifecyclePublished,
		latest:    true,
		resources: map[string]string{},
	}

	err = pkgRevWriteToDB(ctx, &dbPR2)
	t.Require().NoError(err)

	// Delete the latest revision - should trigger async notification
	err = dbPkg.DeletePackageRevision(ctx, &dbPR2, false)
	t.Require().NoError(err)

	// wait for async call to finish
	time.Sleep(100 * time.Millisecond)

	dbPR1.latest = true
	err = dbPkg.DeletePackageRevision(ctx, &dbPR1, false)
	t.Require().NoError(err)

	// wait for async call to finish
	time.Sleep(100 * time.Millisecond)

	// After deletion, dbPRMain should remain in database so len(prSlice) > 0
	err = dbPkg.DeletePackageRevision(ctx, &dbPRMain, false)
	t.Require().NoError(err)

	// Clean up
	err = repoDeleteFromDB(ctx, dbRepo.Key())
	t.Require().NoError(err)
}

func (t *DbTestSuite) TestExtractFromKptfile() {
	t.Run("WithConditions", func() {
		resources := map[string]string{
			"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-pkg
  labels:
    app: web
  annotations:
    team: platform
info:
  readinessGates:
    - conditionType: Ready
    - conditionType: Available
status:
  conditions:
    - type: Ready
      status: "True"
      reason: AllGood
      message: everything is fine`,
		}
		s, gates, pkgMeta := extractFromKptfile(resources)
		t.Len(s.Conditions, 1)
		t.Equal("Ready", s.Conditions[0].Type)
		t.Equal(porchapi.ConditionTrue, s.Conditions[0].Status)
		t.Len(gates, 2)
		t.Equal("Ready", gates[0].ConditionType)
		t.Equal("Available", gates[1].ConditionType)
		t.Equal("web", pkgMeta.Labels["app"])
		t.Equal("platform", pkgMeta.Annotations["team"])
	})

	t.Run("NoKptfile", func() {
		resources := map[string]string{"other.yaml": "data: true"}
		s, gates, pkgMeta := extractFromKptfile(resources)
		t.Empty(s.Conditions)
		t.Nil(gates)
		t.Nil(pkgMeta)
	})

	t.Run("InvalidKptfile", func() {
		resources := map[string]string{"Kptfile": "not: valid: yaml: ["}
		s, _, _ := extractFromKptfile(resources)
		t.Empty(s.Conditions)
	})

	t.Run("MinimalKptfile", func() {
		resources := map[string]string{
			"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: minimal`,
		}
		s, gates, pkgMeta := extractFromKptfile(resources)
		t.Empty(s.Conditions)
		t.Nil(s.UpstreamLock)
		t.Empty(gates)
		t.Nil(pkgMeta.Labels)
		t.Nil(pkgMeta.Annotations)
	})

	t.Run("WithUpstreamLock", func() {
		resources := map[string]string{
			"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-pkg
upstreamLock:
  type: git
  git:
    repo: https://example.com/repo.git
    directory: my-pkg
    ref: main
    commit: abc123`,
		}
		s, _, _ := extractFromKptfile(resources)
		t.Require().NotNil(s.UpstreamLock)
		t.Equal("https://example.com/repo.git", s.UpstreamLock.Git.Repo)
		t.Equal("my-pkg", s.UpstreamLock.Git.Directory)
		t.Equal("abc123", s.UpstreamLock.Git.Commit)
	})
}

func (t *DbTestSuite) TestKptfileStatusRoundTrip() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(&dbRepository{})

	dbRepo := t.createTestRepo("kfmeta-ns", "kfmeta-repo")
	dbPkg := t.createTestPkg(dbRepo.Key(), "kfmeta-pkg")

	kfYAML := `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: test-pkg
  labels:
    app: web
  annotations:
    team: platform
info:
  readinessGates:
    - conditionType: Ready
status:
  conditions:
    - type: Ready
      status: "True"
      reason: Deployed
      message: all good`

	resources := map[string]string{"Kptfile": kfYAML}
	status, gates, pkgMeta := extractFromKptfile(resources)
	pr := dbPackageRevision{
		pkgRevKey: repository.PackageRevisionKey{
			PkgKey:        dbPkg.Key(),
			WorkspaceName: "ws-1",
			Revision:      1,
		},
		lifecycle:     "Published",
		spec:          &porchapi.PackageRevisionSpec{ReadinessGates: gates, PackageMetadata: pkgMeta},
		kptfileStatus: status,
	}
	t.Require().NoError(pkgRevWriteToDB(t.Context(), &pr))

	filter := repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: dbRepo.Key()}},
	}
	results, err := pkgRevListPRsFromDB(t.Context(), filter)
	t.Require().NoError(err)
	t.Require().Len(results, 1)

	readPR := results[0]
	t.Len(readPR.kptfileStatus.Conditions, 1)
	t.Equal("Ready", readPR.kptfileStatus.Conditions[0].Type)
	t.Equal(porchapi.ConditionTrue, readPR.kptfileStatus.Conditions[0].Status)
	t.Equal("Deployed", readPR.kptfileStatus.Conditions[0].Reason)
	t.Len(readPR.spec.ReadinessGates, 1)
	t.Equal("Ready", readPR.spec.ReadinessGates[0].ConditionType)
	t.Equal("web", readPR.spec.PackageMetadata.Labels["app"])
	t.Equal("platform", readPR.spec.PackageMetadata.Annotations["team"])

	t.deleteTestRepo(dbRepo.Key())
}

func (t *DbTestSuite) TestBackfillKptfileMeta() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(&dbRepository{})

	dbRepo := t.createTestRepo("backfill-ns", "backfill-repo")
	dbPkg := t.createTestPkg(dbRepo.Key(), "backfill-pkg")

	// Write a PR with empty kptfileMeta (simulating pre-migration row)
	pr := dbPackageRevision{
		pkgRevKey: repository.PackageRevisionKey{
			PkgKey:        dbPkg.Key(),
			WorkspaceName: "ws-1",
			Revision:      1,
		},
		lifecycle: "Published",
		resources: map[string]string{
			"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: backfill-test
  labels:
    app: backfilled
info:
  readinessGates:
    - conditionType: Ready
status:
  conditions:
    - type: Ready
      status: "True"
      reason: OK`,
		},
		// kptfileStatus intentionally left empty to simulate pre-migration state
	}
	t.Require().NoError(pkgRevWriteToDB(t.Context(), &pr))

	// Verify kptfile_status is empty in DB
	filter := repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: dbRepo.Key()}},
	}
	results, err := pkgRevListPRsFromDB(t.Context(), filter)
	t.Require().NoError(err)
	t.Require().Len(results, 1)
	t.Empty(results[0].kptfileStatus.Conditions)

	// Run backfill
	err = backfillKptfileMeta(t.Context())
	t.Require().NoError(err)

	// Verify kptfile_status (status) and spec are now populated
	results, err = pkgRevListPRsFromDB(t.Context(), filter)
	t.Require().NoError(err)
	t.Require().Len(results, 1)
	t.Equal("backfilled", results[0].spec.PackageMetadata.Labels["app"])
	t.Len(results[0].spec.ReadinessGates, 1)
	t.Equal("Ready", results[0].spec.ReadinessGates[0].ConditionType)
	t.Len(results[0].kptfileStatus.Conditions, 1)
	t.Equal("Ready", results[0].kptfileStatus.Conditions[0].Type)

	// Run backfill again — should be a no-op (kptfile_status is no longer '{}')
	err = backfillKptfileMeta(t.Context())
	t.Require().NoError(err)

	t.deleteTestRepo(dbRepo.Key())
}

func (t *DbTestSuite) TestBackfillUpstreamRefName() {
	mockCache := mockcachetypes.NewMockCache(t.T())
	cachetypes.CacheInstance = mockCache
	mockCache.EXPECT().GetRepository(mock.Anything).Return(&dbRepository{})

	dbRepo := t.createTestRepo("backfill-ns", "upstream-backfill-repo")
	dbPkg := t.createTestPkg(dbRepo.Key(), "downstream-pkg")

	upstreamPRName := "upstream-backfill-repo.basepkg.v1"

	// Write a PR with clone task — pkgRevWriteToDB will set upstream_ref_name automatically.
	pr := dbPackageRevision{
		pkgRevKey: repository.PackageRevisionKey{
			PkgKey:        dbPkg.Key(),
			WorkspaceName: "ws-clone",
			Revision:      1,
		},
		lifecycle: "Published",
		tasks: []porchapi.Task{
			{
				Type: porchapi.TaskTypeClone,
				Clone: &porchapi.PackageCloneTaskSpec{
					Upstream: porchapi.UpstreamPackage{
						UpstreamRef: &porchapi.PackageRevisionRef{Name: upstreamPRName},
					},
				},
			},
		},
	}
	t.Require().NoError(pkgRevWriteToDB(t.Context(), &pr))

	// Simulate a pre-migration row by clearing the upstream_ref_name column directly.
	_, err := GetDB().db.Exec(t.Context(),
		`UPDATE package_revisions SET upstream_ref_name = '' WHERE k8s_name_space = $1 AND k8s_name = $2`,
		pr.Key().K8SNS(), pr.Key().K8SName())
	t.Require().NoError(err)

	// Verify that findUpstreamRefsFromDB finds nothing before backfill.
	found, err := findUpstreamRefsFromDB(t.Context(), "backfill-ns", upstreamPRName)
	t.Require().NoError(err)
	t.Empty(found)

	// Run backfill.
	err = backfillUpstreamRefName(t.Context())
	t.Require().NoError(err)

	// Verify upstream_ref_name is now populated.
	found, err = findUpstreamRefsFromDB(t.Context(), "backfill-ns", upstreamPRName)
	t.Require().NoError(err)
	t.Equal(pr.Key().K8SName(), found)

	// Run backfill again — should be a no-op.
	err = backfillUpstreamRefName(t.Context())
	t.Require().NoError(err)

	// Also test with an upgrade task.
	newUpstreamName := "upstream-backfill-repo.basepkg.v2"
	pr2 := dbPackageRevision{
		pkgRevKey: repository.PackageRevisionKey{
			PkgKey:        dbPkg.Key(),
			WorkspaceName: "ws-upgrade",
			Revision:      2,
		},
		lifecycle: "Published",
		tasks: []porchapi.Task{
			{
				Type: porchapi.TaskTypeUpgrade,
				Upgrade: &porchapi.PackageUpgradeTaskSpec{
					NewUpstream: porchapi.PackageRevisionRef{Name: newUpstreamName},
				},
			},
		},
	}
	t.Require().NoError(pkgRevWriteToDB(t.Context(), &pr2))

	// Clear upstream_ref_name to simulate pre-migration state.
	_, err = GetDB().db.Exec(t.Context(),
		`UPDATE package_revisions SET upstream_ref_name = '' WHERE k8s_name_space = $1 AND k8s_name = $2`,
		pr2.Key().K8SNS(), pr2.Key().K8SName())
	t.Require().NoError(err)

	// Run backfill.
	err = backfillUpstreamRefName(t.Context())
	t.Require().NoError(err)

	// Verify.
	found, err = findUpstreamRefsFromDB(t.Context(), "backfill-ns", newUpstreamName)
	t.Require().NoError(err)
	t.Equal(pr2.Key().K8SName(), found)

	t.deleteTestRepo(dbRepo.Key())
}
