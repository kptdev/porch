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
package disaster

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/helper/chroot"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/filesystem"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/test/disaster/api/environment/gitea"
	"github.com/kptdev/porch/test/disaster/api/environment/kind"
	"github.com/kptdev/porch/test/disaster/api/environment/packagevariants"
	"github.com/kptdev/porch/test/disaster/api/environment/packagevariantsets"
	"github.com/kptdev/porch/test/disaster/api/environment/pods"
	repositories "github.com/kptdev/porch/test/disaster/api/environment/porchrepositories"
	"github.com/kptdev/porch/test/disaster/api/environment/postgres"
	"github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type PorchDisasterRecoverySuite struct {
	suiteutils.MultiClusterTestSuite

	skipPackageVariants, skipPackageVariantSets bool

	baselineCounts *suiteutils.PackageRevisionStatusCounts
}

var (
	porchResourceRestoreDescription = "restore [Repositories, PackageVariants, PackageVariantSets]"
)

func TestDisasterRecovery(t *testing.T) {
	// Skip if not running disaster-recovery tests
	if os.Getenv("DISASTER") == "" {
		t.Skip("Skipping disaster-recovery tests in non-disaster-recovery environment")
	}

	suite.Run(t, &PorchDisasterRecoverySuite{
		skipPackageVariants:    true,
		skipPackageVariantSets: true,
	})
}

func (t *PorchDisasterRecoverySuite) SetupSuite() {
	s := &t.MultiClusterTestSuite

	t.PorchRoot = func() string {

		repo, _ := git.PlainOpen("../../../")

		// Try to grab the repository Storer
		s := repo.Storer.(*filesystem.Storage)
		// Try to get the underlying billy.Filesystem
		fs := s.Filesystem().(*chroot.ChrootHelper)

		return strings.Replace(fs.Root(), "/.git", "", 1)
	}()

	if t.skipPackageVariantSets {
		os.Setenv("SKIP_PACKAGE_VARIANT_SETS", "true")
		porchResourceRestoreDescription = strings.ReplaceAll(porchResourceRestoreDescription, ", PackageVariantSets", "")
	}
	if t.skipPackageVariants {
		os.Setenv("SKIP_PACKAGE_VARIANTS", "true")
		porchResourceRestoreDescription = strings.ReplaceAll(porchResourceRestoreDescription, ", PackageVariants", "")
	}

	if os.Getenv("SETUP_ENV") == "true" {
		setupEnv(s)
	}

	s.SetupSuite()

	if err := kind.UseDBCacheCluster(s); err != nil {
		t.Logf("Unable to access DB cache cluster - assuming environment not set up")
		setupEnv(s)
		kind.ReconnectDBCacheCluster(s)
	}

	initialCounts, err := t.CountPackageRevisions()
	if err != nil {
		t.Logf("Unable to get initial count of package revisions - resetting environment")
		setupEnv(s)
		kind.ReconnectDBCacheCluster(s)
		initialCounts, _ = t.CountPackageRevisions()
	}
	t.baselineCounts = initialCounts
	t.Logf("Baseline package revision counts:\n\n%s\n", t.baselineCounts)
}

func (t *PorchDisasterRecoverySuite) SetupTest() {
	s := &t.MultiClusterTestSuite

	if err := kind.UseDBCacheCluster(s); err != nil {
		t.Logf("Unable to access DB cache cluster - assuming environment not set up")
		setupEnv(s)
	}

	actualCounts, err := t.CountPackageRevisions()
	environmentReady := func() bool {
		expectedCountsBefore := t.baselineCounts

		return err == nil &&
			!(actualCounts.Total == 0 ||
				actualCounts.Draft == 0 ||
				actualCounts.Proposed == 0 ||
				actualCounts.Published == 0 ||
				actualCounts.DeletionProposed == 0) &&
			(actualCounts.Total == expectedCountsBefore.Total &&
				actualCounts.Draft == expectedCountsBefore.Draft &&
				actualCounts.Proposed == expectedCountsBefore.Proposed &&
				actualCounts.Published == expectedCountsBefore.Published &&
				actualCounts.DeletionProposed == expectedCountsBefore.DeletionProposed)
	}

	if !environmentReady() {
		t.Logf("Environment not ready - package revision counts differ from baseline as follows:\n\n%s\n", actualCounts)
		if _, err := os.Stat("./dumped_db.sql"); err == nil {
			t.Logf("Found DB cache backup from previous test - restoring from it")
			postgres.Restore(s)
			kind.UseDBCacheCluster(s)
		} else {
			t.Logf("Resetting environment before test")
			resetEnv(s)
		}
	}

	actualCounts, err = t.CountPackageRevisions()
	if !environmentReady() {
		t.Logf("Environment not ready - package revision counts differ from baseline as follows:\n\n%s\n", actualCounts)
		t.Logf("DB cache restoration did not restore all expected package revisions - resetting environment")
		resetEnv(s)
	}

	t.Logf("Package revision counts before disaster:\n\n%s\n", actualCounts)
}

func (t *PorchDisasterRecoverySuite) TestCompleteDisaster() {
	s := &t.MultiClusterTestSuite

	gitea.Backup(s)
	postgres.Backup(s)

	kind.UseDBCacheCluster(s)
	repositoriesToRestore := repositories.Backup(s)
	variantsToRestore := packagevariants.Backup(s)
	variantSetsToRestore := packagevariantsets.Backup(s)

	kind.Wipe(s)
	gitea.Wipe(s)
	postgres.Wipe(s)

	kind.Reinstall(s)
	gitea.Restore(s)
	postgres.Restore(s)

	t.TimingHelper(porchResourceRestoreDescription, func(t *suiteutils.TestSuite) {
		t.T().Helper()
		kind.UseDBCacheCluster(s)
		repositories.Restore(s, repositoriesToRestore, 20)
		packagevariants.Restore(s, variantsToRestore, 20)
		packagevariantsets.Restore(s, variantSetsToRestore, 20)
	})

	expectedCountsAfter := t.baselineCounts
	t.Logf("Expected package revision counts after disaster-recovery:\n\n%s\n", expectedCountsAfter)
	t.PackageRevisionCountsMustMatch(expectedCountsAfter)
	t.T().Name()
}

func (t *PorchDisasterRecoverySuite) TestKubernetesClusterLoss() {
	s := &t.MultiClusterTestSuite

	kind.UseDBCacheCluster(s)
	repositoriesToRestore := repositories.Backup(s)
	variantsToRestore := packagevariants.Backup(s)
	variantSetsToRestore := packagevariantsets.Backup(s)

	kind.Wipe(s)

	kind.Reinstall(s)

	t.TimingHelper(porchResourceRestoreDescription, func(t *suiteutils.TestSuite) {
		t.T().Helper()
		kind.UseDBCacheCluster(s)
		repositories.Restore(s, repositoriesToRestore, 20)
		packagevariants.Restore(s, variantsToRestore, 20)
		packagevariantsets.Restore(s, variantSetsToRestore, 20)
	})

	expectedCountsAfter := t.baselineCounts
	t.Logf("Expected package revision counts after disaster-recovery:\n\n%s\n", expectedCountsAfter)
	t.PackageRevisionCountsMustMatch(expectedCountsAfter)
}

func (t *PorchDisasterRecoverySuite) TestDBCacheLossWithoutBackup() {
	s := &t.MultiClusterTestSuite

	//************************************************************
	// test case is "without a backup", but we need one afterward
	// to restore the environment to a known state for other tests
	//************************************************************
	postgres.Backup(s)

	kind.UseDBCacheCluster(s)
	repositoriesToRestore := repositories.Backup(s)
	variantsToRestore := packagevariants.Backup(s)
	variantSetsToRestore := packagevariantsets.Backup(s)

	postgres.Wipe(s)

	t.TimingHelper("restart porch-server and rebuild from Git", func(innerT *suiteutils.TestSuite) {
		innerT.T().Helper()
		kind.UseDBCacheCluster(s)
		repositories.Restore(s, repositoriesToRestore, 20)
		packagevariants.Restore(s, variantsToRestore, 20)
		packagevariantsets.Restore(s, variantSetsToRestore, 20)

		// no package revisions available with no DB cache
		expectedCountsAfter := &suiteutils.PackageRevisionStatusCounts{
			Total:            0,
			Draft:            0,
			Proposed:         0,
			Published:        0,
			DeletionProposed: 0,
		}
		t.Logf("Expected package revision counts after wiping DB cache:\n\n%s\n", expectedCountsAfter)
		s.PackageRevisionCountsMustMatch(expectedCountsAfter)

		// restarting porch-server microservice allows it to rebuild what it can from Git
		// Draft, Proposed, and DeletionProposed data is lost
		pods.RestartPorchServer(s)

		expectedTotalAfterServerRestart :=
			t.baselineCounts.Total - (t.baselineCounts.Draft + t.baselineCounts.Proposed)
		expectedCountsAfterServerRestart := &suiteutils.PackageRevisionStatusCounts{
			Total:            expectedTotalAfterServerRestart,
			Draft:            0,
			Proposed:         0,
			Published:        expectedTotalAfterServerRestart,
			DeletionProposed: 0,
		}
		t.Logf("Expected package revision counts after rebuilding DB cache from Git:\n\n%s\n", expectedCountsAfterServerRestart)
		s.PackageRevisionCountsMustMatch(expectedCountsAfterServerRestart)
	})

	//*******************************
	// restore environment after test
	//*******************************
	postgres.Restore(s)
}

func (t *PorchDisasterRecoverySuite) TestDBCacheLossWithBackup() {
	s := &t.MultiClusterTestSuite

	postgres.Backup(s)

	kind.UseDBCacheCluster(s)
	repositoriesToRestore := repositories.Backup(s)
	variantsToRestore := packagevariants.Backup(s)
	variantSetsToRestore := packagevariantsets.Backup(s)

	kind.UseDataCluster(s)
	postgres.Wipe(s)

	kind.UseDataCluster(s)
	postgres.Restore(s)

	t.TimingHelper(porchResourceRestoreDescription, func(t *suiteutils.TestSuite) {
		t.T().Helper()
		kind.UseDBCacheCluster(s)
		repositories.Restore(s, repositoriesToRestore, 20)
		packagevariants.Restore(s, variantsToRestore, 20)
		packagevariantsets.Restore(s, variantSetsToRestore, 20)
	})

	expectedCountsAfter := t.baselineCounts
	t.Logf("Expected package revision counts after disaster-recovery:\n\n%s\n", expectedCountsAfter)
	t.PackageRevisionCountsMustMatch(expectedCountsAfter)
}

func (t *PorchDisasterRecoverySuite) TestPorchPodsUngracefulRestart() {
	s := &t.MultiClusterTestSuite

	t.TimingHelper("recover after Porch pods restarted", func(t *suiteutils.TestSuite) {
		t.T().Helper()
		kind.UseDBCacheCluster(s)
		pods.RestartAllPorchPods(s)

		// wait a bit before trying to reconcile Porch objects
		// give porch-server some time to re-sync everything and get the API available again
		time.Sleep(1 * time.Minute)

		var repos configapi.RepositoryList
		t.ListF(&repos, client.InNamespace(t.Namespace))
		t.WaitUntilMultipleRepositoriesReady(repos.Items)

		wait.PollUntilContextTimeout(t.GetContext(), time.Second, 60*time.Second, true, func(ctx context.Context) (bool, error) {
			err := t.Reader.List(ctx, &porchapi.PackageRevisionList{}, client.InNamespace(t.Namespace), client.MatchingFields{"spec.repository": repos.Items[0].Name})
			if err != nil {
				if apierrors.IsTimeout(err) {
					return false, nil
				} else {
					return false, err
				}
			}
			return true, nil
		})
	})

	expectedCountsAfter := t.baselineCounts
	t.Logf("Expected package revision counts after disaster-recovery:\n\n%s\n", expectedCountsAfter)
	t.PackageRevisionCountsMustMatch(expectedCountsAfter)
}
