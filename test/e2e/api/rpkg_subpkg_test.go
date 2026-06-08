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

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	subpackageRepoOffRoot    = "subpackage-repo-off-root"
	subpackageRepoDownLevels = "subpackage-repo-down-levels"
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

func (t *PorchSuite) TestSubpackageCloneIntoRoot() {
	repo := "subpkg-clone-into-root"
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	parentPR := t.createPR(repo, parentPackageName, parentWorkspace)
	parentPR, err := t.cloneSubpackage(parentPR, parentPR, "")
	if err == nil || !strings.Contains(err.Error(), "subpackage directory") && !strings.Contains(err.Error(), "is invalid") {
		t.Fatalf("Clone of subpackage onto root gave an unexpected error %v", err)
	}

	t.deletePR(parentPR)
}

func (t *PorchSuite) TestSubpackageCloneIntoExisting() {
	const (
		repo           = "subpkg-clone-existing"
		subpackageDir1 = "level1/level2/my-subpackage-1"
		subpackageDir2 = "level1/level2/my-subpackage-1/my-subpackage-2"
		subpackageDir3 = "level1/level2/my-subpackage-1"
		subpackageDir4 = "level1/level2/my-subpackage-1/"
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePRV1 := t.createPR(repo, cloneePackageName, clonedWorkspaceV1)
	t.approvePR(cloneePRV1)

	parentPR := t.createPR(repo, parentPackageName, parentWorkspace)

	parentPR, err := t.cloneSubpackage(parentPR, cloneePRV1, subpackageDir1)
	if err != nil {
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePRV1, parentPR, subpackageDir1, err)
	}

	var parentPRResources porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "name: "+path.Base(subpackageDir1))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "ref: "+cloneePackageName+"/v1")

	assert.Equal(t, 1, len(parentPR.Spec.Tasks))

	parentPR, err = t.cloneSubpackage(parentPR, cloneePRV1, subpackageDir2)
	if err == nil ||
		!strings.Contains(err.Error(), "cannot clone subpackage into another subpackage, parent already has a subpackage at") &&
			!strings.Contains(err.Error(), "cannot clone subpackage into parent, parent already has content at") {
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePRV1, parentPR, subpackageDir2, err)
	}

	parentPR.Spec.Tasks = parentPR.Spec.Tasks[:len(parentPR.Spec.Tasks)-1]
	parentPR, err = t.cloneSubpackage(parentPR, cloneePRV1, subpackageDir3)
	if err == nil ||
		!strings.Contains(err.Error(), "cannot clone subpackage into another subpackage, parent already has a subpackage at") &&
			!strings.Contains(err.Error(), "cannot clone subpackage into parent, parent already has content at") {
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePRV1, parentPR, subpackageDir3, err)
	}

	parentPR.Spec.Tasks = parentPR.Spec.Tasks[:len(parentPR.Spec.Tasks)-1]
	parentPR, err = t.cloneSubpackage(parentPR, cloneePRV1, subpackageDir3)
	if err == nil ||
		!strings.Contains(err.Error(), "cannot clone subpackage into another subpackage, parent already has a subpackage at") &&
			!strings.Contains(err.Error(), "cannot clone subpackage into parent, parent already has content at") {
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePRV1, parentPR, subpackageDir3, err)
	}

	parentPR.Spec.Tasks = parentPR.Spec.Tasks[:len(parentPR.Spec.Tasks)-1]
	parentPR, err = t.cloneSubpackage(parentPR, cloneePRV1, subpackageDir4)
	if err == nil || !strings.Contains(err.Error(), "subpackageDir is invalid: subpackage directory \"level1/level2/my-subpackage-1/\" is invalid") {
		t.Fatalf("Clone of subpackage %v in parent PR %v subpackage directory %q failed: %v", cloneePRV1, parentPR, subpackageDir4, err)
	}

	t.deletePR(parentPR)
	t.deletePR(cloneePRV1)
}

func (t *PorchSuite) TestSubpackageUpgradeNonexisting() {
	const (
		repo           = "subpkg-upgrade-nonexisting"
		subpackageDir1 = "level1/level2/my-subpackage-1"
		subpackageDir2 = "level1/level2/my-subpackage-1/my-subpackage-2"
		subpackageDir3 = "level1/level2/my-subpackage-3"
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePRV1 := t.createPR(repo, cloneePackageName, clonedWorkspaceV1)
	t.approvePR(cloneePRV1)

	cloneePRV2 := t.copyPR(repo, cloneePRV1, clonedWorkspaceV2)
	t.approvePR(cloneePRV2)

	parentPR := t.createPR(repo, parentPackageName, parentWorkspace)

	parentPR, err := t.cloneSubpackage(parentPR, cloneePRV1, subpackageDir1)
	if err != nil {
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePRV1, parentPR, subpackageDir1, err)
	}

	var parentPRResources porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "name: "+path.Base(subpackageDir1))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "ref: "+cloneePackageName+"/v1")

	assert.Equal(t, 1, len(parentPR.Spec.Tasks))

	parentPR, err = t.upgradeSubpackage(parentPR, cloneePRV1, cloneePRV2, subpackageDir1)
	if err != nil {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePRV1, cloneePRV2, parentPR, subpackageDir1, err)
	}

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "name: "+path.Base(subpackageDir1))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "ref: "+cloneePackageName+"/v2")

	assert.Equal(t, 1, len(parentPR.Spec.Tasks))

	parentPR, err = t.upgradeSubpackage(parentPR, cloneePRV1, cloneePRV2, subpackageDir2)
	if err == nil || !strings.Contains(err.Error(), "not found in package") {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePRV1, cloneePRV2, parentPR, subpackageDir2, err)
	}

	parentPR.Spec.Tasks = parentPR.Spec.Tasks[:len(parentPR.Spec.Tasks)-1]
	parentPR, err = t.upgradeSubpackage(parentPR, cloneePRV1, cloneePRV2, subpackageDir3)
	if err == nil || !strings.Contains(err.Error(), "not found in package") {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePRV1, cloneePRV2, parentPR, subpackageDir3, err)
	}
	t.deletePR(parentPR)
	t.deletePR(cloneePRV2)
	t.deletePR(cloneePRV1)
}

func (t *PorchSuite) TestSubpackageCloneAndUpgradeNonOverlapping() {
	const (
		repo           = "subpkg-clone-overlapping"
		subpackageDir1 = "level1/level2/level3/my-subpackage-1"
		subpackageDir2 = "level1/level2/level3/my-subpackage-2"
		subpackageDir3 = "level1/my-subpackage-3"
		subpackageDir4 = "level1/level2/my-subpackage-4"
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePR1V1 := t.createPR(repo, cloneePackageName+"-1", clonedWorkspaceV1)
	t.approvePR(cloneePR1V1)

	cloneePR1V2 := t.copyPR(repo, cloneePR1V1, clonedWorkspaceV2)
	t.approvePR(cloneePR1V2)

	cloneePR1V3 := t.copyPR(repo, cloneePR1V2, clonedWorkspaceV3)
	t.approvePR(cloneePR1V3)

	cloneePR2V1 := t.createPR(repo, cloneePackageName+"-2", clonedWorkspaceV1)
	t.approvePR(cloneePR2V1)

	cloneePR2V2 := t.copyPR(repo, cloneePR2V1, clonedWorkspaceV2)
	t.approvePR(cloneePR2V2)

	cloneePR2V3 := t.copyPR(repo, cloneePR2V2, clonedWorkspaceV3)
	t.approvePR(cloneePR2V3)

	cloneePR3V1 := t.createPR(repo, cloneePackageName+"-3", clonedWorkspaceV1)
	t.approvePR(cloneePR3V1)

	cloneePR3V2 := t.copyPR(repo, cloneePR3V1, clonedWorkspaceV2)
	t.approvePR(cloneePR3V2)

	cloneePR3V3 := t.copyPR(repo, cloneePR3V2, clonedWorkspaceV3)
	t.approvePR(cloneePR3V3)

	cloneePR4V1 := t.createPR(repo, cloneePackageName+"-4", clonedWorkspaceV1)
	t.approvePR(cloneePR4V1)

	cloneePR4V2 := t.copyPR(repo, cloneePR4V1, clonedWorkspaceV2)
	t.approvePR(cloneePR4V2)

	cloneePR4V3 := t.copyPR(repo, cloneePR4V2, clonedWorkspaceV3)
	t.approvePR(cloneePR4V3)

	parentPR := t.createPR(repo, parentPackageName, parentWorkspace)

	parentPR, err := t.cloneSubpackage(parentPR, cloneePR1V1, subpackageDir1)
	if err != nil {
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePR1V1, parentPR, subpackageDir1, err)
	}

	parentPR, err = t.cloneSubpackage(parentPR, cloneePR2V1, subpackageDir2)
	if err != nil {
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePR2V1, parentPR, subpackageDir2, err)
	}

	parentPR, err = t.cloneSubpackage(parentPR, cloneePR3V1, subpackageDir3)
	if err != nil {
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePR3V1, parentPR, subpackageDir3, err)
	}

	parentPR, err = t.cloneSubpackage(parentPR, cloneePR4V1, subpackageDir4)
	if err != nil {
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePR4V1, parentPR, subpackageDir4, err)
	}

	var parentPRResources porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "name: "+path.Base(subpackageDir1))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "ref: "+cloneePackageName+"-1/v1")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/Kptfile"], "name: "+path.Base(subpackageDir2))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/Kptfile"], "ref: "+cloneePackageName+"-2/v1")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/Kptfile"], "name: "+path.Base(subpackageDir3))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/Kptfile"], "ref: "+cloneePackageName+"-3/v1")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir4+"/Kptfile"], "name: "+path.Base(subpackageDir4))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir4+"/Kptfile"], "ref: "+cloneePackageName+"-4/v1")

	assert.Equal(t, 1, len(parentPR.Spec.Tasks))

	parentPR, err = t.upgradeSubpackage(parentPR, cloneePR1V1, cloneePR1V2, subpackageDir1)
	if err != nil {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePR1V1, cloneePR1V2, parentPR, subpackageDir1, err)
	}
	parentPR, err = t.upgradeSubpackage(parentPR, cloneePR2V1, cloneePR2V2, subpackageDir2)
	if err != nil {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePR2V1, cloneePR2V2, parentPR, subpackageDir2, err)
	}
	parentPR, err = t.upgradeSubpackage(parentPR, cloneePR3V1, cloneePR3V2, subpackageDir3)
	if err != nil {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePR3V1, cloneePR3V2, parentPR, subpackageDir3, err)
	}
	parentPR, err = t.upgradeSubpackage(parentPR, cloneePR4V1, cloneePR4V2, subpackageDir4)
	if err != nil {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePR4V1, cloneePR4V2, parentPR, subpackageDir4, err)
	}

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "name: "+path.Base(subpackageDir1))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "ref: "+cloneePackageName+"-1/v2")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/Kptfile"], "name: "+path.Base(subpackageDir2))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/Kptfile"], "ref: "+cloneePackageName+"-2/v2")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/Kptfile"], "name: "+path.Base(subpackageDir3))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/Kptfile"], "ref: "+cloneePackageName+"-3/v2")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir4+"/Kptfile"], "name: "+path.Base(subpackageDir4))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir4+"/Kptfile"], "ref: "+cloneePackageName+"-4/v2")

	assert.Equal(t, 1, len(parentPR.Spec.Tasks))

	t.approvePR(parentPR)
	parentPRV2 := t.copyPR(repo, parentPR, parentWorkspaceV2)

	parentPRV2, err = t.upgradeSubpackage(parentPRV2, cloneePR1V2, cloneePR1V3, subpackageDir1)
	if err != nil {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePR1V2, cloneePR1V3, parentPRV2, subpackageDir1, err)
	}
	parentPRV2, err = t.upgradeSubpackage(parentPRV2, cloneePR2V2, cloneePR2V3, subpackageDir2)
	if err != nil {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePR2V2, cloneePR2V3, parentPRV2, subpackageDir2, err)
	}
	parentPRV2, err = t.upgradeSubpackage(parentPRV2, cloneePR3V2, cloneePR3V3, subpackageDir3)
	if err != nil {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePR3V2, cloneePR3V3, parentPRV2, subpackageDir3, err)
	}
	parentPRV2, err = t.upgradeSubpackage(parentPRV2, cloneePR4V2, cloneePR4V3, subpackageDir4)
	if err != nil {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePR4V2, cloneePR4V3, parentPRV2, subpackageDir4, err)
	}

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPRV2.Name,
	}, &parentPRResources)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "name: "+path.Base(subpackageDir1))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "ref: "+cloneePackageName+"-1/v3")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/Kptfile"], "name: "+path.Base(subpackageDir2))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/Kptfile"], "ref: "+cloneePackageName+"-2/v3")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/Kptfile"], "name: "+path.Base(subpackageDir3))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/Kptfile"], "ref: "+cloneePackageName+"-3/v3")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir4+"/Kptfile"], "name: "+path.Base(subpackageDir4))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir4+"/Kptfile"], "ref: "+cloneePackageName+"-4/v3")

	assert.Equal(t, 1, len(parentPRV2.Spec.Tasks))

	t.deletePR(parentPRV2)
	t.deletePR(parentPR)
	t.deletePR(cloneePR1V3)
	t.deletePR(cloneePR1V2)
	t.deletePR(cloneePR1V1)
	t.deletePR(cloneePR2V3)
	t.deletePR(cloneePR2V2)
	t.deletePR(cloneePR2V1)
	t.deletePR(cloneePR3V3)
	t.deletePR(cloneePR3V2)
	t.deletePR(cloneePR3V1)
	t.deletePR(cloneePR4V3)
	t.deletePR(cloneePR4V2)
	t.deletePR(cloneePR4V1)
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
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePRV1, parentPR, subpackageDir, err)
	}

	var parentPRResources porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "name: "+path.Base(subpackageDir))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "ref: "+cloneePackageName+"/v1")

	assert.Contains(t, parentPRResources.Spec.Resources["my-configmap.yaml"], "test-label-"+parentWorkspace+": "+parentWorkspace)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/my-configmap.yaml"], "test-label-"+parentWorkspace+": "+parentWorkspace)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/my-configmap.yaml"], "test-label-"+clonedWorkspaceV1+": "+clonedWorkspaceV1)

	assert.Equal(t, 1, len(parentPR.Spec.Tasks))

	parentPR, err = t.upgradeSubpackage(parentPR, cloneePRV1, cloneePRV2, subpackageDir)
	if err != nil {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePRV1, cloneePRV2, parentPR, subpackageDir, err)
	}

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "name: "+path.Base(subpackageDir))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "ref: "+cloneePackageName+"/v2")

	assert.Contains(t, parentPRResources.Spec.Resources["my-configmap.yaml"], "test-label-"+parentWorkspace+": "+parentWorkspace)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/my-configmap.yaml"], "test-label-"+parentWorkspace+": "+parentWorkspace)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/my-configmap.yaml"], "test-label-"+clonedWorkspaceV2+": "+clonedWorkspaceV2)

	assert.Equal(t, 1, len(parentPR.Spec.Tasks))

	t.approvePR(parentPR)

	parentPRV2 := t.copyPR(subpackageRepo, parentPR, parentWorkspaceV2)

	parentPRV2, err = t.upgradeSubpackage(parentPRV2, cloneePRV2, cloneePRV3, subpackageDir)
	if err != nil {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePRV2, cloneePRV3, parentPRV2, subpackageDir, err)
	}

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPRV2.Name,
	}, &parentPRResources)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "name: "+path.Base(subpackageDir))
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "ref: "+cloneePackageName+"/v3")

	assert.Contains(t, parentPRResources.Spec.Resources["my-configmap.yaml"], "test-label-"+parentWorkspaceV2+": "+parentWorkspaceV2)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/my-configmap.yaml"], "test-label-"+parentWorkspace+": "+parentWorkspace)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/my-configmap.yaml"], "test-label-"+parentWorkspaceV2+": "+parentWorkspaceV2)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/my-configmap.yaml"], "test-label-"+clonedWorkspaceV3+": "+clonedWorkspaceV3)

	assert.Equal(t, 1, len(parentPRV2.Spec.Tasks))

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

	t.addPipelineToPR(createdPR)

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

	t.addPipelineToPR(copiedPR)

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

func (t *PorchSuite) upgradeSubpackage(parentPR, oldCloneePR, newCloneePR *porchapi.PackageRevision, subpackage string) (*porchapi.PackageRevision, error) {
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

	err := t.Client.Update(t.GetContext(), parentPR)
	return parentPR, err
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
	t.UpdateApprovalF(pr)
}

func (t *PorchSuite) addPipelineToPR(pr *porchapi.PackageRevision) {
	var prResources porchapi.PackageRevisionResources

	t.GetF(client.ObjectKeyFromObject(pr), &prResources)
	kptfile := t.ParseKptfileF(&prResources)
	kptfile.Pipeline = &kptfilev1.Pipeline{
		Mutators: []kptfilev1.Function{
			{
				Image: "ghcr.io/kptdev/krm-functions-catalog/set-labels:latest",
				ConfigMap: map[string]string{
					"test-label-" + pr.Spec.WorkspaceName: pr.Spec.WorkspaceName},
			},
		},
	}
	t.SaveKptfileF(&prResources, kptfile)

	testConfigmapStr := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-configmap
data:
  someKey: someValue
`

	prResources.Spec.Resources["my-configmap.yaml"] = strings.ReplaceAll(testConfigmapStr, "name: my-configmap", "name: my-"+pr.Name+"-configmap")
	delete(prResources.Spec.Resources, "package-context.yaml")

	t.UpdateF(&prResources)
	t.GetF(client.ObjectKeyFromObject(pr), pr)
}
