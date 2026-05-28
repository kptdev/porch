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
	"path"
	"strings"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	subpackageRepoOffRoot    = "subpackage-repo-off-root"
	subpackageRepoDownLevels = "subpackage-repo-down-levels"
	subpackageRepoErrors     = "subpackage-repo-errors"
	subpackageDirOffRoot     = "my-subpackage"
	subpackageDirDownLevels  = "level1/level2/level3/level4/my-subpackage"
	parentPackageName        = "parent-package"
	parentWorkspace          = "parent-workspace"
	parentWorkspaceV2        = "parent-workspace-2"
	cloneePackageName        = "cloned-package"
	clonedWorkspaceV1        = "clonee-v1"
	clonedWorkspaceV2        = "clonee-v2"
	clonedWorkspaceV3        = "clonee-v3"
	description              = "This is a description"
)

func (t *PorchSuite) TestSimpleSubpackageCloneAndUpgradeOffRoot() {
	t.SimpleSubpackageCloneAndUpgradeScenario(subpackageRepoOffRoot, subpackageDirOffRoot)
}

func (t *PorchSuite) TestSimpleSubpackageCloneAndUpgradeDownLevels() {
	t.SimpleSubpackageCloneAndUpgradeScenario(subpackageRepoDownLevels, subpackageDirDownLevels)
}

func (t *PorchSuite) TestSubpackageCloneAndUpgradeErrors() {
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), subpackageRepoErrors, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePRV1 := t.createPR(subpackageRepoErrors, cloneePackageName, clonedWorkspaceV1)
	t.approvePR(cloneePRV1)

	cloneePRV2 := t.copyPR(subpackageRepoErrors, cloneePRV1, clonedWorkspaceV2)
	t.approvePR(cloneePRV2)

	cloneePRV3 := t.copyPR(subpackageRepoErrors, cloneePRV2, clonedWorkspaceV3)
	t.approvePR(cloneePRV3)

	parentPR := t.createPR(subpackageRepoErrors, parentPackageName, parentWorkspace)
	parentPR, err := t.cloneSubpackage(parentPR, parentPR, "")
	if !strings.Contains(err.Error(), "subpackage directory") && !strings.Contains(err.Error(), "is invalid") {
		t.Fatalf("Clone of subpackage %v into parent PR %v supackage directiry %q failed", parentPR, cloneePRV1, "")
	}

	t.deletePR(parentPR)
	t.deletePR(cloneePRV3)
	t.deletePR(cloneePRV2)
	t.deletePR(cloneePRV1)
}

func (t *PorchSuite) SimpleSubpackageCloneAndUpgradeScenario(subpackageRepo, subpackageDir string) {
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), subpackageRepo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePRV1 := t.createPR(subpackageRepo, cloneePackageName, clonedWorkspaceV1)
	t.approvePR(cloneePRV1)

	cloneePRV2 := t.copyPR(subpackageRepo, cloneePRV1, clonedWorkspaceV2)
	t.approvePR(cloneePRV2)

	cloneePRV3 := t.copyPR(subpackageRepo, cloneePRV2, clonedWorkspaceV3)
	t.approvePR(cloneePRV3)

	parentPR := t.createPR(subpackageRepo, parentPackageName, parentWorkspace)
	parentPR, err := t.cloneSubpackage(parentPR, cloneePRV1, subpackageDir)
	if err != nil {
		t.Fatalf("Clone of subpackage %v into parent PR %v supackage directiry %q failed", parentPR, cloneePRV1, subpackageDir)
	}

	var parentPRResources porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "name: "+path.Base(subpackageDir))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "ref: "+cloneePackageName+"/v1")

	assert.Equal(t, 1, len(parentPR.Spec.Tasks))

	parentPR = t.upgradeSubpackage(parentPR, cloneePRV1, cloneePRV2, subpackageDir)

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "name: "+path.Base(subpackageDir))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "ref: "+cloneePackageName+"/v2")

	assert.Equal(t, 1, len(parentPR.Spec.Tasks))

	t.approvePR(parentPR)

	parentPRV2 := t.copyPR(subpackageRepo, parentPR, parentWorkspaceV2)

	parentPRV2 = t.upgradeSubpackage(parentPRV2, cloneePRV2, cloneePRV3, subpackageDir)

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPRV2.Name,
	}, &parentPRResources)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "name: "+path.Base(subpackageDir))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "ref: "+cloneePackageName+"/v3")

	assert.Equal(t, 1, len(parentPR.Spec.Tasks))

	t.deletePR(parentPRV2)
	t.deletePR(parentPR)
	t.deletePR(cloneePRV3)
	t.deletePR(cloneePRV2)
	t.deletePR(cloneePRV1)
}

func (t *PorchSuite) createPR(subpackageRepo, packageName, workspace string) *porchapi.PackageRevision {
	// Create PackageRevision from upstream repo
	createdPR := t.CreatePackageSkeleton(subpackageRepo, packageName, workspace)
	createdPR.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeInit,
			Init: &porchapi.PackageInitTaskSpec{
				Description: description,
			},
		},
	}
	t.CreateF(createdPR)

	// Check the package exists
	var pkg porchapi.PackageRevision
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: createdPR.Name}, &pkg)

	return createdPR
}

func (t *PorchSuite) copyPR(subpackageRepo string, sourcePr *porchapi.PackageRevision, workspace string) *porchapi.PackageRevision {
	// Copy PackageRevision from another packagerevision
	copiedPR := t.CreatePackageSkeleton(subpackageRepo, sourcePr.Spec.PackageName, workspace)
	copiedPR.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeEdit,
			Edit: &porchapi.PackageEditTaskSpec{
				Source: &porchapi.PackageRevisionRef{
					Name: sourcePr.Name,
				},
			},
		},
	}
	t.CreateF(copiedPR)

	// Check the package exists
	var pkg porchapi.PackageRevision
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: copiedPR.Name}, &pkg)

	return copiedPR
}

func (t *PorchSuite) cloneSubpackage(parentPR, cloneePR *porchapi.PackageRevision, subpackage string) (*porchapi.PackageRevision, error) {
	parentPR.Spec.Tasks = append(parentPR.Spec.Tasks, porchapi.Task{
		Type: porchapi.TaskTypeClone,
		Clone: &porchapi.PackageCloneTaskSpec{
			Upstream: porchapi.UpstreamPackage{
				Type: porchapi.RepositoryTypeGit,
				UpstreamRef: &porchapi.PackageRevisionRef{
					Name: cloneePR.Name,
				},
			},
			SubpackageDir: subpackage,
		},
	})

	err := t.Client.Update(t.GetContext(), parentPR)
	return parentPR, err
}

func (t *PorchSuite) upgradeSubpackage(parentPR, oldCloneePR, newCloneePR *porchapi.PackageRevision, subpackage string) *porchapi.PackageRevision {
	parentPR.Spec.Tasks = append(parentPR.Spec.Tasks, porchapi.Task{
		Type: porchapi.TaskTypeUpgrade,
		Upgrade: &porchapi.PackageUpgradeTaskSpec{
			OldUpstream: porchapi.PackageRevisionRef{
				Name: oldCloneePR.Name,
			},
			NewUpstream: porchapi.PackageRevisionRef{
				Name: newCloneePR.Name,
			},
			LocalPackageRevisionRef: porchapi.PackageRevisionRef{
				Name: parentPR.Name,
			},
			SubpackageDir: subpackage,
		},
	})

	t.UpdateF(parentPR)
	return parentPR
}

func (t *PorchSuite) deletePR(pr *porchapi.PackageRevision) {
	// Handle deletion if required
	if pr.Spec.Lifecycle == porchapi.PackageRevisionLifecyclePublished {
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

func (t *PorchSuite) approvePR(pr *porchapi.PackageRevision) {
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	pr = t.UpdateApprovalF(pr)
}
