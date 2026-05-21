// Copyright 2025-2026 The kpt Authors
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
	"slices"
	"strings"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	testWorkspaceConcurrent = "test-workspace"
	workspace2              = "workspace2"
	conflictErrorMessage    = "another request is already in progress"
)

func (t *PorchSuite) TestConcurrentClones() {
	const (
		upstreamRepository   = "upstream"
		upstreamPackage      = "basens"
		downstreamRepository = "downstream"
		downstreamPackage    = "istions-concurrent"
		workspace            = testWorkspaceConcurrent
	)

	// Register Upstream and Downstream Repositories
	t.RegisterGitRepositoryF(t.GetTestBlueprintsRepoURL(), upstreamRepository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), downstreamRepository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	var list porchapi.PackageRevisionList
	t.ListE(&list, client.InNamespace(t.Namespace))
	upstreamPr := t.MustFindPackageRevision(
		&list,
		repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Name: upstreamRepository,
				},
				Package: upstreamPackage,
			},
			Revision: 1})

	// Create PackageRevision from upstream repo
	clonedPr := t.CreatePackageSkeleton(downstreamRepository, downstreamPackage, workspace)
	clonedPr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeClone,
			Clone: &porchapi.PackageCloneTaskSpec{
				Upstream: porchapi.UpstreamPackage{
					UpstreamRef: &porchapi.PackageRevisionRef{
						Name: upstreamPr.Name,
					},
				},
			},
		},
	}

	// Two clients at the same time try to run the Create operation for the clone
	cloneFunction := func() any {
		return t.Client.Create(t.GetContext(), clonedPr)
	}
	results := suiteutils.RunInParallel(cloneFunction, cloneFunction)
	t.assertConcurrentResults(results, "clone")
}

func (t *PorchSuite) TestConcurrentInits() {
	// Create a new package via init, no task specified
	const (
		repository  = "git-concurrent"
		packageName = "empty-package-concurrent"
		revision    = 1
		workspace   = testWorkspaceConcurrent
	)

	// Register the repository
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Two clients try to create the same new draft package
	pr := t.CreatePackageSkeleton(repository, packageName, workspace)
	creationFunction := func() any {
		return t.Client.Create(t.GetContext(), pr)
	}
	results := suiteutils.RunInParallel(creationFunction, creationFunction)

	// one client succeeds; one receives a conflict error
	expectedResultCount := 2
	actualResultCount := len(results)
	assert.Equal(t, expectedResultCount, actualResultCount, "expected %d results but was %d", expectedResultCount, actualResultCount)

	t.assertConcurrentResults(results, "init")
}

func (t *PorchSuite) TestConcurrentEdits() {
	const (
		repository  = "concurrent-edit-test"
		packageName = "simple-package-concurrent"
		workspace   = defaultWorkspace
		workspace2  = workspace2
	)

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create a new package (via init)
	pr := t.CreatePackageDraftF(repository, packageName, workspace)

	// Publish and approve the source package
	t.proposeAndApprovePackage(pr)

	// Create a new revision of the package with a source that is a revision
	// of the same package.
	editPR := t.CreatePackageSkeleton(repository, packageName, workspace2)
	editPR.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeEdit,
			Edit: &porchapi.PackageEditTaskSpec{
				Source: &porchapi.PackageRevisionRef{
					Name: pr.Name,
				},
			},
		},
	}

	// Two clients try to create the new package at the same time
	editFunction := func() any {
		return t.Client.Create(t.GetContext(), editPR)
	}
	results := suiteutils.RunInParallel(
		editFunction,
		editFunction)

	t.assertConcurrentResults(results, "edit")

	// Check its task list
	var pkgRev porchapi.PackageRevision
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      editPR.Name,
	}, &pkgRev)
	tasks := pkgRev.Spec.Tasks
	assert.Equal(t, 1, len(tasks))
}

func (t *PorchSuite) TestConcurrentResourceUpdates() {
	const (
		repository  = "concurrent-update-test"
		packageName = "simple-package"
		workspace   = defaultWorkspace
	)

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create a new package (via init)
	pr := t.CreatePackageDraftF(repository, packageName, workspace)

	// Get the package resources
	var newPackageResources porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      pr.Name,
	}, &newPackageResources)

	// "Update" the package resources with two clients at the same time
	updateFunction := func() any {
		return t.Client.Update(t.GetContext(), newPackageResources.DeepCopy())
	}
	results := suiteutils.RunInParallel(updateFunction, updateFunction)
	t.assertConcurrentResults(results, "resource update")
}

func (t *PorchSuite) TestConcurrentProposeApprove() {
	const (
		repository  = "lifecycle"
		packageName = "test-package-concurrent"
		workspace   = defaultWorkspace
	)

	// Register the repository
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create a new package (via init)
	pr := t.CreatePackageDraftF(repository, packageName, workspace)

	var pkg porchapi.PackageRevision
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      pr.Name,
	}, &pkg)

	// Propose the package revision to be finalized
	pkg.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	proposeFunction := func() any {
		return t.Client.Update(t.GetContext(), pkg.DeepCopy())
	}
	proposeResults := suiteutils.RunInParallel(proposeFunction, proposeFunction)
	t.assertConcurrentResults(proposeResults, "propose")

	var proposed porchapi.PackageRevision
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      pr.Name,
	}, &proposed)

	// Approve the package
	proposed.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	approveFunction := func() any {
		return t.UpdateApprovalE(&proposed)
	}
	approveResults := suiteutils.RunInParallel(approveFunction, approveFunction)
	t.assertConcurrentResults(approveResults, "approve")
}

func (t *PorchSuite) TestConcurrentDeletes() {
	const (
		repository  = "delete-draft"
		packageName = "test-delete-draft-concurrent"
		revision    = 1
		workspace   = testWorkspaceConcurrent
	)

	// Register the repository and create a draft package
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)
	created := t.CreatePackageDraftF(repository, packageName, workspace)

	// Check the package exists
	var draft porchapi.PackageRevision
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: created.Name}, &draft)

	// Delete the same package with more than one client at the same time
	deleteFunction := func() any {
		return t.Client.Delete(t.GetContext(), &porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: t.Namespace,
				Name:      created.Name,
			},
		})
	}
	results := suiteutils.RunInParallel(
		deleteFunction,
		deleteFunction,
		deleteFunction,
		deleteFunction,
		deleteFunction,
		deleteFunction,
		deleteFunction,
		deleteFunction)

	assert.True(t, len(results) >= 7, "expected at least 7 results but was %d", len(results))
	t.assertConcurrentDeleteResults(results)
	t.MustNotExist(&draft)
}

func (t *PorchSuite) TestConcurrentProposeDeletes() {
	const (
		repository  = "delete-final"
		packageName = "test-delete-final-concurrent"
		workspace   = defaultWorkspace
	)

	// Register the repository and create a draft package
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)
	created := t.CreatePackageDraftF(repository, packageName, workspace)
	// Check the package exists
	var pkg porchapi.PackageRevision
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: created.Name}, &pkg)

	// Propose and approve the package revision to be finalized
	t.proposeAndApprovePackage(&pkg)

	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: created.Name}, &pkg)

	// Propose deletion with two clients at once
	pkg.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	proposeDeleteFunction := func() any {
		return t.UpdateApprovalE(&pkg)
	}
	proposeDeleteResults := suiteutils.RunInParallel(proposeDeleteFunction, proposeDeleteFunction)
	t.assertConcurrentResults(proposeDeleteResults, "propose-delete")
}

func (t *PorchSuite) TestConcurrentPackageUpdates() {
	const (
		gitRepository = "concurrent-package-update"
		packageName   = "testns-concurrent"
		workspace     = testWorkspaceConcurrent
	)

	// Register the upstream repository
	t.RegisterGitRepositoryF(t.GetTestBlueprintsRepoURL(), suiteutils.TestBlueprintsRepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	var list porchapi.PackageRevisionList
	t.ListE(&list, client.InNamespace(t.Namespace))

	basensV1 := t.MustFindPackageRevision(&list, repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "test-blueprints"}, Package: "basens"}, Revision: 1})
	basensV2 := t.MustFindPackageRevision(&list, repository.PackageRevisionKey{PkgKey: repository.PackageKey{RepoKey: repository.RepositoryKey{Name: "test-blueprints"}, Package: "basens"}, Revision: 2})

	// Register the repository as 'downstream'
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), gitRepository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create PackageRevision from upstream repo
	pr := t.CreatePackageSkeleton(gitRepository, packageName, workspace)
	pr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeClone,
			Clone: &porchapi.PackageCloneTaskSpec{
				Upstream: porchapi.UpstreamPackage{
					UpstreamRef: &porchapi.PackageRevisionRef{
						Name: basensV1.Name,
					},
				},
			},
		},
	}
	t.CreateF(pr)

	pr.Spec.Tasks[0].Clone.Upstream.UpstreamRef.Name = basensV2.Name

	// Two clients at the same time try to update the downstream package
	updateFunction := func() any {
		return t.Client.Update(t.GetContext(), pr.DeepCopy())
	}
	results := suiteutils.RunInParallel(updateFunction, updateFunction)
	t.assertConcurrentResults(results, "package update")
}

func (t *PorchSuite) assertConcurrentResults(results []any, operation string) {
	assert.Contains(t, results, nil, "expected one %s request to succeed, but did not happen - results: %v", operation, results)

	conflictFailurePresent := slices.ContainsFunc(results, func(eachResult any) bool {
		return eachResult != nil && strings.Contains(eachResult.(error).Error(), conflictErrorMessage)
	})
	assert.True(t, conflictFailurePresent, "expected one %s request to fail with a conflict, but did not happen - results: %v", operation, results)
}

func (t *PorchSuite) assertConcurrentDeleteResults(results []any) {
	assert.Contains(t, results, nil, "expected one delete request to succeed, but did not happen - results: %v", results)

	validFailurePresent := slices.ContainsFunc(results, func(eachResult any) bool {
		if eachResult == nil {
			return false
		}
		err := eachResult.(error).Error()
		return strings.Contains(err, conflictErrorMessage) ||
			strings.Contains(err, "not found") ||
			strings.Contains(err, "sql: no rows in result set")
	})

	assert.True(t, validFailurePresent, "expected losing delete requests to fail with conflict/not-found semantics - results: %v", results)
}

func (t *PorchSuite) proposeAndApprovePackage(pr *porchapi.PackageRevision) {
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	t.UpdateApprovalF(pr)
}
