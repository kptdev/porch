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

package api

import (
	"fmt"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type LifecycleTestCase struct {
	Name         string
	RepoName     string
	PackageName  string
	Workspace    string
	Tasks        []porchapi.Task
	TargetState  porchapi.PackageRevisionLifecycle
	ShouldDelete bool
	Validate     func(*PorchSuite, *porchapi.PackageRevision)
}

func (t *PorchSuite) TestBasicLifecycle() {
	cases := []LifecycleTestCase{
		{
			Name:        "ProposeApprove",
			RepoName:    "lifecycle",
			PackageName: "test-package",
			Workspace:   defaultWorkspace,
			TargetState: porchapi.PackageRevisionLifecyclePublished,
			Validate: func(t *PorchSuite, pr *porchapi.PackageRevision) {
				if pr.Spec.Revision != 1 {
					t.Fatalf("Expected revision 1, got %d", pr.Spec.Revision)
				}
			},
		},
		{
			Name:         "DeleteDraft",
			RepoName:     "delete-draft",
			PackageName:  "test-delete-draft",
			Workspace:    "test-workspace",
			TargetState:  porchapi.PackageRevisionLifecycleDraft,
			ShouldDelete: true,
		},
		{
			Name:         "DeleteProposed",
			RepoName:     "delete-proposed",
			PackageName:  "test-delete-proposed",
			Workspace:    defaultWorkspace,
			TargetState:  porchapi.PackageRevisionLifecycleProposed,
			ShouldDelete: true,
		},
		{
			Name:        "DeleteFinal",
			RepoName:    "delete-final",
			PackageName: "test-delete-final",
			Workspace:   defaultWorkspace,
			TargetState: porchapi.PackageRevisionLifecyclePublished,
			Validate: func(t *PorchSuite, pr *porchapi.PackageRevision) {
				t.DeleteL(&porchapi.PackageRevision{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: t.Namespace,
						Name:      pr.Name,
					},
				})
				t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, pr)
			},
			ShouldDelete: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func() {
			t.runLifecycleTest(tc)
		})
	}
}

func (t *PorchSuite) runLifecycleTest(tc LifecycleTestCase) {
	// Register repository
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), tc.RepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create package
	var pr *porchapi.PackageRevision
	if len(tc.Tasks) == 0 {
		pr = t.CreatePackageDraftF(tc.RepoName, tc.PackageName, tc.Workspace)
	} else {
		pr = t.CreatePackageSkeleton(tc.RepoName, tc.PackageName, tc.Workspace)
		pr.Spec.Tasks = tc.Tasks
		t.CreateF(pr)
	}

	// Check the package exists
	var pkg porchapi.PackageRevision
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, &pkg)

	// Apply lifecycle transitions
	switch tc.TargetState {
	case porchapi.PackageRevisionLifecyclePublished:
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
		t.UpdateF(pr)
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
		pr = t.UpdateApprovalF(pr)
	case porchapi.PackageRevisionLifecycleProposed:
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
		t.UpdateF(pr)
	case porchapi.PackageRevisionLifecycleDraft:
		// Keep as draft - no transitions needed
	}

	// Run validation if provided
	if tc.Validate != nil {
		tc.Validate(t, pr)
	}

	// Handle deletion if required
	if tc.ShouldDelete {
		if tc.TargetState == porchapi.PackageRevisionLifecyclePublished {
			pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
			t.UpdateApprovalF(pr)
		}
		t.DeleteE(&porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: t.Namespace,
				Name:      pr.Name,
			},
		})
		t.MustNotExist(pr)
	}
}

func (t *PorchSuite) TestProposeDeleteAndUndo() {
	const (
		repository  = "test-propose-delete-and-undo"
		packageName = "test-propose-delete-and-undo"
		workspace   = defaultWorkspace
	)

	// Register the repository
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create a draft package
	created := t.CreatePackageDraftF(repository, packageName, workspace)

	// Check the package exists
	var pkg porchapi.PackageRevision
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: created.Name}, &pkg)

	// Propose the package revision to be finalized
	pkg.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(&pkg)

	pkg.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	t.UpdateApprovalF(&pkg)
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: created.Name}, &pkg)

	_ = t.WaitUntilPackageRevisionExists(repository, packageName, -1)

	var list porchapi.PackageRevisionList
	t.ListF(&list, client.InNamespace(t.Namespace))

	for i := range list.Items {
		pkgRev := list.Items[i]
		t.Run(fmt.Sprintf("revision %d", pkgRev.Spec.Revision), func() {
			// Propose deletion
			pkgRev.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
			pkgRev = *t.UpdateApprovalF(&pkgRev)

			// Undo proposal of deletion
			pkgRev.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
			pkgRev = *t.UpdateApprovalF(&pkgRev)

			// Try to delete the package. This should fail because the lifecycle should be changed back to Published.
			t.DeleteL(&porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: t.Namespace,
					Name:      pkgRev.Name,
				},
			})
			t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: pkgRev.Name}, &pkgRev)

			// Propose deletion and then delete the package
			pkgRev.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
			pkgRev = *t.UpdateApprovalF(&pkgRev)

			t.DeleteE(&porchapi.PackageRevision{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: t.Namespace,
					Name:      pkgRev.Name,
				},
			})

			t.MustNotExist(&pkgRev)
		})
	}
}

func (t *PorchSuite) TestDeleteAndRecreate() {
	const (
		repository  = "delete-and-recreate"
		packageName = "test-delete-and-recreate"
		revision    = 1
		workspace   = "work"
	)

	// Register the repository
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create a draft package
	created := t.CreatePackageDraftF(repository, packageName, workspace)

	// Check the package exists
	var pkg porchapi.PackageRevision
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: created.Name}, &pkg)

	t.Log("Propose the package revision to be finalized")
	pkg.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(&pkg)

	t.Log("Approve the package revision to be finalized")
	pkg.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	t.UpdateApprovalF(&pkg)

	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: created.Name}, &pkg)

	mainPkg := t.WaitUntilPackageRevisionExists(repository, packageName, -1)

	t.Log("Propose deletion and then delete the package with revision v1")
	pkg.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	t.UpdateApprovalF(&pkg)

	t.DeleteE(&porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.Namespace,
			Name:      created.Name,
		},
	})
	t.MustNotExist(&pkg)

	t.Log("Propose deletion and then delete the package with revision main")
	mainPkg.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	t.UpdateApprovalF(mainPkg)

	t.DeleteE(&porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.Namespace,
			Name:      mainPkg.Name,
		},
	})
	t.MustNotExist(mainPkg)

	// Recreate the package with the same name and workspace
	created = t.CreatePackageDraftF(repository, packageName, workspace)

	// Check the package exists
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: created.Name}, &pkg)

	// Ensure that there is only one init task in the package revision history
	foundInitTask := false
	for _, task := range pkg.Spec.Tasks {
		if task.Type == porchapi.TaskTypeInit {
			if foundInitTask {
				t.Fatalf("found two init tasks in recreated package revision")
			}
			foundInitTask = true
		}
	}
	t.Logf("successfully recreated package revision %q", packageName)
}

func (t *PorchSuite) TestDeleteFromMain() {
	const (
		repository        = "delete-main"
		packageNameFirst  = "test-delete-main-first"
		packageNameSecond = "test-delete-main-second"
		workspace         = defaultWorkspace
	)

	// Register the repository
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	t.Logf("Create and approve package: %s", packageNameFirst)
	createdFirst := t.CreatePackageDraftF(repository, packageNameFirst, workspace)

	// Check the package exists
	var pkgFirst porchapi.PackageRevision
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: createdFirst.Name}, &pkgFirst)

	// Propose the package revision to be finalized
	pkgFirst.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(&pkgFirst)

	pkgFirst.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	t.UpdateApprovalF(&pkgFirst)

	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: createdFirst.Name}, &pkgFirst)

	t.Logf("Create and approve package: %s", packageNameSecond)
	createdSecond := t.CreatePackageDraftF(repository, packageNameSecond, workspace)

	// Check the package exists
	var pkgSecond porchapi.PackageRevision
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: createdSecond.Name}, &pkgSecond)

	// Propose the package revision to be finalized
	pkgSecond.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(&pkgSecond)

	pkgSecond.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	t.UpdateApprovalF(&pkgSecond)

	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: createdSecond.Name}, &pkgSecond)

	t.Log("Wait for the 'main' revisions to get created")
	firstPkgRevFromMain := t.WaitUntilPackageRevisionExists(repository, packageNameFirst, -1)
	secondPkgRevFromMain := t.WaitUntilPackageRevisionExists(repository, packageNameSecond, -1)

	t.Log("Propose deletion of both main packages")
	firstPkgRevFromMain.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	t.UpdateApprovalF(firstPkgRevFromMain)
	secondPkgRevFromMain.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	t.UpdateApprovalF(secondPkgRevFromMain)

	t.Log("Delete the first package revision from main")
	t.DeleteE(&porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.Namespace,
			Name:      firstPkgRevFromMain.Name,
		},
	})

	t.Log("Delete the second package revision from main")
	t.DeleteE(&porchapi.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.Namespace,
			Name:      secondPkgRevFromMain.Name,
		},
	})

	// Propose and delete the original package revisions (cleanup)
	var list porchapi.PackageRevisionList
	t.ListE(&list, client.InNamespace(t.Namespace))
	for _, pkgrev := range list.Items {
		t.Logf("Propose deletion and delete package revision: %s", pkgrev.Name)
		pkgrev.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
		t.UpdateApprovalF(&pkgrev)
		t.DeleteE(&porchapi.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: t.Namespace,
				Name:      pkgrev.Name,
			},
		})
	}
}

func (t *PorchSuite) TestLatestVersionOnDelete() {
	const (
		repositoryName = "test-latest-on-delete-repository"
		workspacev1    = "test-latest-on-delete-workspace-v1"
		workspacev2    = "test-latest-on-delete-workspace-v2"
		packageName    = "test-latest-on-delete-package"
	)

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repositoryName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	pr1 := t.CreatePackageDraftF(repositoryName, packageName, workspacev1)

	pr1 = t.proposeAndPublish(pr1)

	//After approval of the first revision, the package should be labeled as latest
	t.MustHaveLabels(pr1.Name, map[string]string{
		porchapi.LatestPackageRevisionKey: porchapi.LatestPackageRevisionValue,
	})

	pr2 := t.CreatePackageSkeleton(repositoryName, packageName, workspacev2)
	pr2.Spec.Tasks = []porchapi.Task{{
		Type: porchapi.TaskTypeEdit,
		Edit: &porchapi.PackageEditTaskSpec{
			Source: &porchapi.PackageRevisionRef{
				Name: pr1.Name,
			},
		},
	}}
	t.CreateF(pr2)

	pr2 = t.proposeAndPublish(pr2)

	//After approval of the second revision, the latest label should migrate to the
	//v2 packageRevision
	t.MustNotHaveLabels(pr1.Name, []string{
		porchapi.LatestPackageRevisionKey,
	})

	t.MustHaveLabels(pr2.Name, map[string]string{
		porchapi.LatestPackageRevisionKey: porchapi.LatestPackageRevisionValue,
	})

	t.GetF(client.ObjectKeyFromObject(pr2), pr2)

	pr2.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	t.UpdateF(pr2)

	t.DeleteF(pr2)
	//After deletion of the v2 packageRevision,
	//the label should migrate back to the v2 packageRevision
	t.MustHaveLabels(pr1.Name, map[string]string{
		porchapi.LatestPackageRevisionKey: porchapi.LatestPackageRevisionValue,
	})

	t.GetF(client.ObjectKeyFromObject(pr1), pr1)

	pr1.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
	t.UpdateF(pr1)

	t.DeleteF(pr1)
	//After the removal of all versioned packageRevisions, the main branch
	//packageRevision should still not get the latest label.
	mainPr := t.GetPackageRevision(repositoryName, packageName, -1)
	t.MustNotHaveLabels(mainPr.Name, []string{
		porchapi.LatestPackageRevisionKey,
	})
}

func (t *PorchSuite) TestSubfolderPackageRevisionIncrementation() {
	const (
		repository           = "lifecycle"
		subfolderRepository  = "repo-in-subfolder"
		subfolderDirectory   = "random/repo/folder"
		normalPackageName    = "test-package"
		subfolderPackageName = "random/package/folder/test-package"
		workspace            = defaultWorkspace
		workspace2           = "workspace2"
		branch               = "subfolder"
	)

	// Create initial repository
	t.CreateF(&configapi.Repository{
		TypeMeta: metav1.TypeMeta{
			Kind:       configapi.TypeRepository.Kind,
			APIVersion: configapi.TypeRepository.APIVersion(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      repository,
			Namespace: t.Namespace,
		},
		Spec: configapi.RepositorySpec{
			Description: "Initial Repository",
			Type:        configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				Repo:         t.GetPorchTestRepoURL(),
				Branch:       branch,
				CreateBranch: true,
				SecretRef: configapi.SecretRef{
					Name: t.CreateOrUpdateSecret(repository, suiteutils.GiteaUser, suiteutils.GiteaPassword),
				},
			},
		},
	})
	t.WaitUntilRepositoryReady(repository, t.Namespace)

	// Create a new package (via init)
	subfolderPr := t.CreatePackageDraftF(repository, subfolderPackageName, workspace)
	// Propose and approve the package revision
	subfolderPr = t.proposeAndPublish(subfolderPr)

	assert.Equal(t, porchapi.PackageRevisionLifecyclePublished, subfolderPr.Spec.Lifecycle)
	assert.Equal(t, 1, subfolderPr.Spec.Revision)

	// Create new package revisions via edit/copy
	editedSubfolderPr := t.CreatePackageSkeleton(repository, subfolderPackageName, workspace2)
	editedSubfolderPr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeEdit,
			Edit: &porchapi.PackageEditTaskSpec{
				Source: &porchapi.PackageRevisionRef{
					Name: subfolderPr.Name,
				},
			},
		},
	}
	t.CreateF(editedSubfolderPr)
	// propose and approve the edit
	editedSubfolderPr = t.proposeAndPublish(editedSubfolderPr)

	assert.Equal(t, porchapi.PackageRevisionLifecyclePublished, editedSubfolderPr.Spec.Lifecycle)
	assert.Equal(t, 2, editedSubfolderPr.Spec.Revision)

	// Delete repo to avoid conflict
	t.DeleteL(&configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repository,
			Namespace: t.Namespace,
		},
	})

	t.WaitUntilRepositoryDeleted(repository, t.Namespace)

	// Register the second repository with directory random/repo/folder
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), subfolderRepository, subfolderDirectory, suiteutils.GiteaUser, suiteutils.GiteaPassword, suiteutils.RepositoryOptions{
		RepOpts: suiteutils.WithBranch(branch),
	})

	// Create a new package (via init)
	prInSubfolder := t.CreatePackageDraftF(subfolderRepository, normalPackageName, workspace)

	// Propose and approve the package revision
	prInSubfolder = t.proposeAndPublish(prInSubfolder)

	assert.Equal(t, porchapi.PackageRevisionLifecyclePublished, prInSubfolder.Spec.Lifecycle)
	assert.Equal(t, 1, prInSubfolder.Spec.Revision)

	editedPrInSubfolder := t.CreatePackageSkeleton(subfolderRepository, normalPackageName, workspace2)
	editedPrInSubfolder.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeEdit,
			Edit: &porchapi.PackageEditTaskSpec{
				Source: &porchapi.PackageRevisionRef{
					Name: prInSubfolder.Name,
				},
			},
		},
	}
	t.CreateF(editedPrInSubfolder)

	// Propose and approve these package revisions as well
	editedPrInSubfolder = t.proposeAndPublish(editedPrInSubfolder)

	assert.Equal(t, porchapi.PackageRevisionLifecyclePublished, editedPrInSubfolder.Spec.Lifecycle)
	assert.Equal(t, 2, editedPrInSubfolder.Spec.Revision)
}

func (t *PorchSuite) proposeAndPublish(pkg *porchapi.PackageRevision) *porchapi.PackageRevision {
	pkg.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(pkg)
	pkg.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	return t.UpdateApprovalF(pkg)
}
