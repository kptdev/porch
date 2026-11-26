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
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestSubpackageBlocksUpstreamDeletion checks a subpackage blocks its upstream until deleted.
func (t *PorchSuite) TestSubpackageBlocksUpstreamDeletion() {
	const (
		repo          = "delete-guard-subpkg"
		subpackageDir = "network"
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	upstream := t.createPR(repo, cloneePackageName, clonedWorkspaceV1)
	t.approvePR(upstream)

	parentPR := t.createPR(repo, parentPackageName, parentWorkspace)
	parentPR, err := t.cloneSubpackage(parentPR, upstream, subpackageDir)
	t.Require().NoError(err)
	t.approvePR(parentPR)

	t.assertProposeDeleteRefused(upstream, parentPR.Name+" (subpackage "+subpackageDir+")")

	t.deletePR(parentPR)
	t.refreshPR(upstream)
	t.deletePR(upstream)
}

// TestCopyOfCloneBlocksUpstreamDeletion checks a copy of a clone blocks the upstream after the clone is deleted.
func (t *PorchSuite) TestCopyOfCloneBlocksUpstreamDeletion() {
	const repo = "delete-guard-copy"
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	upstream := t.createPR(repo, cloneePackageName, clonedWorkspaceV1)
	t.approvePR(upstream)

	clone := t.CreatePackageSkeleton(repo, "consumer", "v1")
	clone.Spec.Tasks = []porchapi.Task{{
		Type: porchapi.TaskTypeClone,
		Clone: &porchapi.PackageCloneTaskSpec{
			Upstream: porchapi.UpstreamPackage{
				Type:        porchapi.RepositoryTypeGit,
				UpstreamRef: &porchapi.PackageRevisionRef{Name: upstream.Name},
			},
		},
	}}
	t.CreateF(clone)
	t.approvePR(clone)

	copied := t.copyPR(repo, clone, "v2")
	t.approvePR(copied)
	t.deletePR(clone)

	t.assertProposeDeleteRefused(upstream, copied.Name)

	t.deletePR(copied)
	t.refreshPR(upstream)
	t.deletePR(upstream)
}

// TestCloneBlocksUpstreamDeleteAfterProposal checks DELETE is refused for an upstream cloned after
// its deletion was proposed.
func (t *PorchSuite) TestCloneBlocksUpstreamDeleteAfterProposal() {
	const repo = "delete-guard-delete"
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	upstream := t.createPR(repo, cloneePackageName, clonedWorkspaceV1)
	t.approvePR(upstream)
	upstream.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	t.UpdateApprovalF(upstream)

	clone := t.CreatePackageSkeleton(repo, "consumer", "v1")
	clone.Spec.Tasks = []porchapi.Task{{
		Type: porchapi.TaskTypeClone,
		Clone: &porchapi.PackageCloneTaskSpec{
			Upstream: porchapi.UpstreamPackage{
				Type:        porchapi.RepositoryTypeGit,
				UpstreamRef: &porchapi.PackageRevisionRef{Name: upstream.Name},
			},
		},
	}}
	t.CreateF(clone)

	err := t.Client.Delete(t.GetContext(), &porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Namespace: t.Namespace, Name: upstream.Name},
	})
	t.Require().Error(err, "deleting a referenced upstream must be refused")
	t.True(apierrors.IsForbidden(err), "want Forbidden, got %v", err)
	t.Contains(err.Error(), t.Namespace+"/"+clone.Name)

	t.deletePR(clone)
	t.refreshPR(upstream)
	t.deletePR(upstream)
}

// assertProposeDeleteRefused expects propose-delete to be refused, naming blocker, with upstream left Published.
func (t *PorchSuite) assertProposeDeleteRefused(upstream *porchapi.PackageRevision, blocker string) {
	t.T().Helper()
	t.refreshPR(upstream)
	upstream.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	err := t.UpdateApprovalE(upstream)
	t.Require().Error(err, "proposing deletion of a referenced upstream must be refused")
	t.True(apierrors.IsForbidden(err), "want Forbidden, got %v", err)
	t.Contains(err.Error(), t.Namespace+"/"+blocker)

	var current porchapi.PackageRevision
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: upstream.Name}, &current)
	t.Equal(porchapi.PackageRevisionLifecyclePublished, current.Spec.Lifecycle, "the refused upstream stays Published")
	*upstream = current
}
