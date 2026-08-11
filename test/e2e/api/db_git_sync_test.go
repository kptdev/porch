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

package api

import (
	"context"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	dbGitTestRepoName    = "db-git-test-repo"
	dbGitSyncWaitTimeout = 60 * time.Second
)

func (t *PorchSuite) updatePRR(_ string, prr *porchapi.PackageRevisionResources, resourceKeys ...string) {
	t.T().Helper()
	t.UpdateF(prr)

	prName := prr.Name
	err := wait.PollUntilContextTimeout(t.GetContext(), time.Second, dbGitSyncWaitTimeout, true, func(ctx context.Context) (bool, error) {
		var latest porchapi.PackageRevisionResources
		if err := t.Reader.Get(ctx, client.ObjectKey{Namespace: t.Namespace, Name: prName}, &latest); err != nil {
			return false, nil
		}
		for _, key := range resourceKeys {
			if _, ok := latest.Spec.Resources[key]; !ok {
				return false, nil
			}
		}
		if err := t.CheckRenderError(&latest.Status.RenderStatus); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("updatePRR: PRR %q did not reflect update (keys %v) within %v", prName, resourceKeys, dbGitSyncWaitTimeout)
	}
}

func (t *PorchSuite) TestSyncDraftSurvivesSyncWhenInGit() {
	const (
		repoName    = dbGitTestRepoName + "-s1"
		packageName = "pkg-survives-sync"
		workspace   = "v1"
		giteaRepo   = repoName + "-git"
	)

	repoURL := t.CreateGiteaRepo(giteaRepo)
	t.RegisterGitRepositoryF(repoURL, repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	pr := t.CreatePackageDraftF(repoName, packageName, workspace)
	t.Logf("created draft %s", pr.Name)

	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)

	branchName := suiteutils.DraftGitBranchName(packageName, workspace)
	t.WaitUntilGiteaBranchExists(giteaRepo, branchName, dbGitSyncWaitTimeout)
	commitSHA := t.GiteaGetBranchLatestCommitSHA(giteaRepo, branchName)
	t.Logf("draft pushed to git branch %s at %s", branchName, commitSHA)

	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)

	pr = t.GetPackageRevisionWithWS(repoName, packageName, workspace)
	t.Require().Equal(porchapi.PackageRevisionLifecycleDraft, pr.Spec.Lifecycle,
		"draft lifecycle must remain Draft after sync")
	t.Require().True(t.GiteaBranchExists(giteaRepo, branchName),
		"draft git branch %q must still exist after sync", branchName)
	t.Require().Equal(commitSHA, t.GiteaGetBranchLatestCommitSHA(giteaRepo, branchName),
		"draft git commit must be unchanged after an in-sync reconcile")
}

func (t *PorchSuite) TestSyncDraftSurvivesSyncWhenPushFails() {
	const (
		repoName    = dbGitTestRepoName + "-s2"
		packageName = "pkg-branch-deleted"
		workspace   = "v1"
		giteaRepo   = repoName + "-git"
	)

	// Create a dedicated Gitea repo for this test so we can archive it in isolation.
	repoURL := t.CreateGiteaRepo(giteaRepo)
	t.RegisterGitRepositoryF(repoURL, repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	// Archive the Gitea repo so any push attempt is rejected
	t.SetGiteaRepoArchived(giteaRepo, true)

	pr := t.CreatePackageDraftF(repoName, packageName, workspace)
	t.Logf("created draft %s (pushed_to_git=false expected because repo is archived)", pr.Name)

	branchName := suiteutils.DraftGitBranchName(packageName, workspace)
	t.Require().False(t.GiteaBranchExists(giteaRepo, branchName),
		"git branch %q must NOT exist because the push was expected to fail", branchName)

	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)

	pr = t.GetPackageRevisionWithWS(repoName, packageName, workspace)
	t.Require().Equal(porchapi.PackageRevisionLifecycleDraft, pr.Spec.Lifecycle)
}

func (t *PorchSuite) TestSyncProposedAndPublishedAfterPushToGitFailed() {
	const (
		repoName    = dbGitTestRepoName + "-s3"
		packageName = "pkg-lifecycle-recovery"
		workspace   = "v1"
		giteaRepo   = repoName + "-git"
	)

	repoURL := t.CreateGiteaRepo(giteaRepo)
	t.RegisterGitRepositoryF(repoURL, repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	// Archive the Gitea repo so the push fails → pushed_to_git=false.
	t.SetGiteaRepoArchived(giteaRepo, true)

	pr := t.CreatePackageDraftF(repoName, packageName, workspace)
	t.Logf("created draft %s (pushed_to_git=false – repo is archived)", pr.Name)

	branchName := suiteutils.DraftGitBranchName(packageName, workspace)
	t.Require().False(t.GiteaBranchExists(giteaRepo, branchName),
		"git branch %q must NOT exist because the push was expected to fail", branchName)

	// Unarchive so the re-push (triggered by resource update) can succeed.
	t.SetGiteaRepoArchived(giteaRepo, false)

	// Recover by updating resources (re-pushes the branch).
	var prr porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, &prr)
	prr.Spec.Resources["recovery.yaml"] = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: recovery
data:
  recovered: "true"
`
	t.updatePRR(repoName, &prr, "recovery.yaml")

	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)

	t.WaitUntilGiteaBranchExists(giteaRepo, branchName, dbGitSyncWaitTimeout)
	t.Logf("draft branch %s recovered into git after the failed push", branchName)

	tagsBeforePublish := t.GiteaRepoTagCount(giteaRepo)

	t.GetF(client.ObjectKeyFromObject(pr), pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(pr)
	t.GetF(client.ObjectKeyFromObject(pr), pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	published := t.UpdateApprovalF(pr)

	t.Require().NotNil(published)
	t.Require().Equal(porchapi.PackageRevisionLifecyclePublished, published.Spec.Lifecycle)
	t.Require().Greater(published.Spec.Revision, 0,
		"published revision must have a positive revision number")

	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)
	t.Require().Eventually(func() bool {
		return t.GiteaRepoTagCount(giteaRepo) > tagsBeforePublish
	}, dbGitSyncWaitTimeout, time.Second,
		"published revision must create a git tag after recovery")
}

func (t *PorchSuite) TestSyncDeleteDraftWithPushToGitFailedRemovedCleanly() {
	const (
		repoName    = dbGitTestRepoName + "-s4"
		packageName = "pkg-delete-push-failed"
		workspace   = "v1"
		giteaRepo   = repoName + "-git"
	)

	repoURL := t.CreateGiteaRepo(giteaRepo)
	t.RegisterGitRepositoryF(repoURL, repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	// Archive the Gitea repo so the push fails → pushed_to_git=false, branch never created.
	t.SetGiteaRepoArchived(giteaRepo, true)

	pr := t.CreatePackageDraftF(repoName, packageName, workspace)
	prName := pr.Name
	t.Logf("created draft %s (pushed_to_git=false – repo is archived)", prName)

	branchName := suiteutils.DraftGitBranchName(packageName, workspace)
	t.Require().False(t.GiteaBranchExists(giteaRepo, branchName),
		"git branch %q must NOT exist because the push was expected to fail", branchName)

	t.DeleteF(pr)
	t.Logf("deleted draft %s via Porch API", prName)

	t.WaitUntilObjectDeleted(suiteutils.PackageRevisionGVK, types.NamespacedName{Namespace: t.Namespace, Name: prName}, dbGitSyncWaitTimeout)

	t.SetGiteaRepoArchived(giteaRepo, false)
	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)

	var list porchapi.PackageRevisionList
	t.ListF(&list, client.InNamespace(t.Namespace))
	for _, item := range list.Items {
		if item.Spec.RepositoryName == repoName &&
			item.Spec.PackageName == packageName &&
			item.Spec.WorkspaceName == workspace {
			t.Errorf("deleted draft %s reappeared after sync", prName)
		}
	}
}

func (t *PorchSuite) TestSyncConcurrentModificationNotOverwrittenByRetry() {
	const (
		repoName    = dbGitTestRepoName + "-s5"
		packageName = "pkg-concurrent-mod"
		workspace   = "v1"
		giteaRepo   = repoName + "-git"
	)

	repoURL := t.CreateGiteaRepo(giteaRepo)
	t.RegisterGitRepositoryF(repoURL, repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	// Archive the Gitea repo so the push fails → pushed_to_git=false, async retry pending.
	t.SetGiteaRepoArchived(giteaRepo, true)

	pr := t.CreatePackageDraftF(repoName, packageName, workspace)
	t.Logf("created draft %s (pushed_to_git=false – async retry now pending)", pr.Name)

	branchName := suiteutils.DraftGitBranchName(packageName, workspace)
	t.Require().False(t.GiteaBranchExists(giteaRepo, branchName),
		"git branch %q must NOT exist because the push was expected to fail", branchName)

	// Update resources while the retry is pending
	var prr porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, &prr)
	const newFileKey = "concurrent-update.yaml"
	prr.Spec.Resources[newFileKey] = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: concurrent-update
data:
  updated-by: concurrent-test
`
	t.updatePRR(repoName, &prr, newFileKey)
	t.Logf("updated resources for draft %s (advances updated timestamp)", pr.Name)

	t.SetGiteaRepoArchived(giteaRepo, false)
	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)

	pr = t.GetPackageRevisionWithWS(repoName, packageName, workspace)
	t.Require().Equal(porchapi.PackageRevisionLifecycleDraft, pr.Spec.Lifecycle)

	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, &prr)
	_, hasConcurrentUpdate := prr.Spec.Resources[newFileKey]
	t.Require().True(hasConcurrentUpdate,
		"draft resources must contain concurrent update file %q; stale retry must not overwrite", newFileKey)
}

func (t *PorchSuite) TestSyncPullsGitChangeIntoDB() {
	const (
		repoName    = dbGitTestRepoName + "-s6"
		packageName = "pkg-git-pull"
		workspace   = "v1"
		giteaRepo   = repoName + "-git"
		newFileKey  = "from-git.yaml"
	)

	repoURL := t.CreateGiteaRepo(giteaRepo)
	t.RegisterGitRepositoryF(repoURL, repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	pr := t.CreatePackageDraftF(repoName, packageName, workspace)
	t.Logf("created draft %s", pr.Name)

	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)

	branchName := suiteutils.DraftGitBranchName(packageName, workspace)
	t.WaitUntilGiteaBranchExists(giteaRepo, branchName, dbGitSyncWaitTimeout)

	time.Sleep(5 * time.Second)

	// Commit a new file directly to the git branch, bypassing Porch.
	newFileContent := `apiVersion: v1
kind: ConfigMap
metadata:
  name: from-git
data:
  source: direct-git-commit
`

	gitFilePath := packageName + "/" + newFileKey
	t.GiteaCommitFileToBranch(giteaRepo, branchName, gitFilePath, newFileContent, "add from-git.yaml via test")

	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)

	var prr porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, &prr)
	_, hasFromGit := prr.Spec.Resources[newFileKey]
	t.Require().True(hasFromGit,
		"package resources must contain %q after the external git commit was pulled into DB", newFileKey)
}

func (t *PorchSuite) TestSyncPublishedPackageCachedFromExternalRepo() {
	const (
		repoName    = dbGitTestRepoName + "-s7"
		packageName = "pkg-git-cache"
		workspace   = "v1"
		giteaRepo   = repoName + "-git"
	)

	repoURL := t.CreateGiteaRepo(giteaRepo)
	secretName := t.CreateOrUpdateSecret(repoName, t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	// First porch registration: create manually so we can delete mid-test
	// without triggering a test failure from the cleanup registered by
	// RegisterGitRepositoryF.
	repo1 := t.BuildGitRepoObject(repoName, repoURL, secretName)
	// Safety-net cleanup in case the test fails before the explicit delete below.
	t.Cleanup(func() {
		t.DeleteL(repo1)
		t.WaitUntilRepositoryDeleted(repoName, t.Namespace)
		t.WaitUntilAllPackagesDeleted(repoName, t.Namespace)
	})
	t.CreateF(repo1)
	t.WaitUntilRepositoryReady(repoName, t.Namespace)

	// Create and publish a package so a git tag is created.
	pr := t.CreatePackageDraftF(repoName, packageName, workspace)
	t.GetF(client.ObjectKeyFromObject(pr), pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(pr)
	t.GetF(client.ObjectKeyFromObject(pr), pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	published := t.UpdateApprovalF(pr)
	t.Require().NotNil(published)
	t.Require().Equal(porchapi.PackageRevisionLifecyclePublished, published.Spec.Lifecycle)
	publishedRevision := published.Spec.Revision

	// Delete the first porch registration.  This clears the DB cache while
	// the git tag remains in the Gitea repository.
	t.DeleteF(repo1)
	t.WaitUntilRepositoryDeleted(repoName, t.Namespace)
	t.WaitUntilAllPackagesDeleted(repoName, t.Namespace)

	// Second porch registration pointing at the same git repo.
	// The initial sync will call cacheExternalPRs for the published tag.
	repo2 := t.BuildGitRepoObject(repoName, repoURL, secretName)
	t.Cleanup(func() {
		t.DeleteL(repo2)
		t.WaitUntilRepositoryDeleted(repoName, t.Namespace)
		t.WaitUntilAllPackagesDeleted(repoName, t.Namespace)
	})
	t.CreateF(repo2)
	// WaitUntilRepositoryReady blocks until the first sync completes, during
	// which cacheExternalPRs should have stored the published revision.
	t.WaitUntilRepositoryReady(repoName, t.Namespace)

	restoredPR := t.WaitUntilPackageRevisionExists(repoName, packageName, publishedRevision)
	t.Require().Equal(porchapi.PackageRevisionLifecyclePublished, restoredPR.Spec.Lifecycle,
		"published package revision must be re-cached from the git tag after re-registration")
}

func (t *PorchSuite) TestSyncReconcilesDBChangedAndPushesToGit() {
	const (
		repoName    = dbGitTestRepoName + "-s8"
		packageName = "pkg-db-push"
		workspace   = "v1"
		giteaRepo   = repoName + "-git"
		newFileKey  = "db-update.yaml"
	)

	repoURL := t.CreateGiteaRepo(giteaRepo)
	t.RegisterGitRepositoryF(repoURL, repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	pr := t.CreatePackageDraftF(repoName, packageName, workspace)
	t.Logf("created draft %s", pr.Name)

	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)

	branchName := suiteutils.DraftGitBranchName(packageName, workspace)
	t.WaitUntilGiteaBranchExists(giteaRepo, branchName, dbGitSyncWaitTimeout)
	initialSHA := t.GiteaGetBranchLatestCommitSHA(giteaRepo, branchName)
	t.Logf("initial branch commit SHA: %s", initialSHA)

	// Archive the repo so the next push attempt fails.
	t.SetGiteaRepoArchived(giteaRepo, true)

	// Update resources – the push triggered by this update will fail, leaving
	// the DB updated (updated > lastPushedDbUpdated) while git stays at initialSHA.
	var prr porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, &prr)
	prr.Spec.Resources[newFileKey] = `apiVersion: v1
kind: ConfigMap
metadata:
  name: db-update
data:
  updated-by: reconcile-test
`
	t.updatePRR(repoName, &prr, newFileKey)
	t.Logf("updated resources for draft %s (push expected to fail – repo is archived)", pr.Name)

	// Unarchive and trigger sync.  reconcileBothPRs detects dbChanged &&
	// !extChanged (git is still at initialSHA) and enqueues a push.
	t.SetGiteaRepoArchived(giteaRepo, false)
	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)

	newSHA := t.WaitUntilGiteaBranchHasNewCommit(giteaRepo, branchName, initialSHA, dbGitSyncWaitTimeout)
	t.Logf("branch advanced from %s to %s after reconcile push", initialSHA, newSHA)

	pr = t.GetPackageRevisionWithWS(repoName, packageName, workspace)
	t.Require().Equal(porchapi.PackageRevisionLifecycleDraft, pr.Spec.Lifecycle)

	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, &prr)
	_, hasUpdate := prr.Spec.Resources[newFileKey]
	t.Require().True(hasUpdate,
		"draft resources must still contain %q after the reconcile pushed DB content to git", newFileKey)
}

func (t *PorchSuite) TestSyncBothChangedDBWins() {
	const (
		repoName    = dbGitTestRepoName + "-s9"
		packageName = "pkg-db-wins"
		workspace   = "v1"
		giteaRepo   = repoName + "-git"
		dbFileKey   = "db-side.yaml"
		gitFileKey  = "git-side.yaml"
	)

	repoURL := t.CreateGiteaRepo(giteaRepo)
	t.RegisterGitRepositoryF(repoURL, repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	pr := t.CreatePackageDraftF(repoName, packageName, workspace)
	t.Logf("created draft %s", pr.Name)

	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)

	branchName := suiteutils.DraftGitBranchName(packageName, workspace)
	t.WaitUntilGiteaBranchExists(giteaRepo, branchName, dbGitSyncWaitTimeout)
	initialSHA := t.GiteaGetBranchLatestCommitSHA(giteaRepo, branchName)
	t.Logf("initial branch commit SHA: %s", initialSHA)

	// Archive the repo so pushes fail.
	t.SetGiteaRepoArchived(giteaRepo, true)

	// Change #1: update resources in the DB.  The resulting push fails, so the
	// DB advances (updated > lastPushedDbUpdated) while git stays at initialSHA.
	var prr porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, &prr)
	prr.Spec.Resources[dbFileKey] = `apiVersion: v1
kind: ConfigMap
metadata:
  name: db-side
data:
  origin: db
`
	t.updatePRR(repoName, &prr, dbFileKey)
	t.Logf("updated resources in DB (push expected to fail – repo is archived)")

	// Change #2: commit a different file directly to the git branch while the
	// repo is still archived.  This advances the external commit past
	// lastPushedCommitTimestamp, satisfying extChanged = true.
	t.SetGiteaRepoArchived(giteaRepo, false)
	gitFilePath := packageName + "/" + gitFileKey
	t.GiteaCommitFileToBranch(giteaRepo, branchName, gitFilePath,
		`apiVersion: v1
kind: ConfigMap
metadata:
  name: git-side
data:
  origin: git
`, "add git-side.yaml via test")
	externalOnlySHA := t.GiteaGetBranchLatestCommitSHA(giteaRepo, branchName)
	t.Logf("git-only commit SHA: %s", externalOnlySHA)

	t.TriggerRepoSync(repoName, dbGitSyncWaitTimeout)

	t.WaitUntilGiteaBranchHasNewCommit(giteaRepo, branchName, externalOnlySHA, dbGitSyncWaitTimeout)

	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, &prr)
	_, hasDBFile := prr.Spec.Resources[dbFileKey]
	t.Require().True(hasDBFile,
		"draft resources must contain %q (DB-side change) after DB wins reconcile", dbFileKey)

	_, hasGitFile := prr.Spec.Resources[gitFileKey]
	t.Require().False(hasGitFile,
		"draft resources must NOT contain %q (git-side change) – DB wins and git content is not pulled in", gitFileKey)
}
