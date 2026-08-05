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
	"fmt"
	"maps"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch"
	porchapiv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

	require.Error(t.T(), err, "Clone of subpackage into root should have given an error")
	assert.Condition(t.T(), func() bool {
		return strings.Contains(err.Error(), "subpackage directory") || strings.Contains(err.Error(), "is invalid")
	}, "Clone of subpackage into root gave an unexpected error %v", err)

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
	require.NoErrorf(t.T(), err, "Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePRV1, parentPR, subpackageDir1, err)

	var parentPRResources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	expectedSubpackageName1, _ := porchapi.ComposeSubpkgObjName(subpackageDir1)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "name: "+expectedSubpackageName1)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "ref: "+cloneePackageName+"/v1")

	assert.Equal(t, 1, len(parentPR.Spec.Tasks))

	parentPR, err = t.cloneSubpackage(parentPR, cloneePRV1, subpackageDir2)
	if err == nil ||
		!strings.Contains(err.Error(), "cannot clone subpackage into another subpackage, parent already has a subpackage at") &&
			!strings.Contains(err.Error(), "cannot clone subpackage into parent, parent already has content at") {
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePRV1, parentPR, subpackageDir2, err)
	}

	parentPR, err = t.cloneSubpackage(parentPR, cloneePRV1, subpackageDir3)
	if err == nil ||
		!strings.Contains(err.Error(), "cannot clone subpackage into another subpackage, parent already has a subpackage at") &&
			!strings.Contains(err.Error(), "cannot clone subpackage into parent, parent already has content at") {
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePRV1, parentPR, subpackageDir3, err)
	}

	parentPR, err = t.cloneSubpackage(parentPR, cloneePRV1, subpackageDir3)
	if err == nil ||
		!strings.Contains(err.Error(), "cannot clone subpackage into another subpackage, parent already has a subpackage at") &&
			!strings.Contains(err.Error(), "cannot clone subpackage into parent, parent already has content at") {
		t.Fatalf("Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePRV1, parentPR, subpackageDir3, err)
	}

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

	var parentPRResources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	expectedSubpackageName1, _ := porchapi.ComposeSubpkgObjName(subpackageDir1)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "name: "+expectedSubpackageName1)
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

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "name: "+expectedSubpackageName1)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "ref: "+cloneePackageName+"/v2")

	assert.Equal(t, 1, len(parentPR.Spec.Tasks))

	parentPR, err = t.upgradeSubpackage(parentPR, cloneePRV1, cloneePRV2, subpackageDir2)
	if err == nil || !strings.Contains(err.Error(), "not found in package") {
		t.Fatalf("Upgrade of subpackage %v to %v in parent PR %v subpackage directory %q failed: %v", cloneePRV1, cloneePRV2, parentPR, subpackageDir2, err)
	}

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

	var parentPRResources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	expectedSubpackageName1, _ := porchapi.ComposeSubpkgObjName(subpackageDir1)
	expectedSubpackageName2, _ := porchapi.ComposeSubpkgObjName(subpackageDir2)
	expectedSubpackageName3, _ := porchapi.ComposeSubpkgObjName(subpackageDir3)
	expectedSubpackageName4, _ := porchapi.ComposeSubpkgObjName(subpackageDir4)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "name: "+expectedSubpackageName1)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "ref: "+cloneePackageName+"-1/v1")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/Kptfile"], "name: "+expectedSubpackageName2)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/Kptfile"], "ref: "+cloneePackageName+"-2/v1")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/Kptfile"], "name: "+expectedSubpackageName3)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/Kptfile"], "ref: "+cloneePackageName+"-3/v1")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir4+"/Kptfile"], "name: "+expectedSubpackageName4)
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

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "name: "+expectedSubpackageName1)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "ref: "+cloneePackageName+"-1/v2")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/Kptfile"], "name: "+expectedSubpackageName2)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/Kptfile"], "ref: "+cloneePackageName+"-2/v2")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/Kptfile"], "name: "+expectedSubpackageName3)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/Kptfile"], "ref: "+cloneePackageName+"-3/v2")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir4+"/Kptfile"], "name: "+expectedSubpackageName4)
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

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "name: "+expectedSubpackageName1)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/Kptfile"], "ref: "+cloneePackageName+"-1/v3")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/Kptfile"], "name: "+expectedSubpackageName2)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir2+"/Kptfile"], "ref: "+cloneePackageName+"-2/v3")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/Kptfile"], "name: "+expectedSubpackageName3)
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir3+"/Kptfile"], "ref: "+cloneePackageName+"-3/v3")
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir4+"/Kptfile"], "name: "+expectedSubpackageName4)
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

func (t *PorchSuite) TestSubpackageModifyRenameAndRemove() {
	const (
		repo           = "subpkg-file-operations"
		subpackageDir1 = "my-subpackage-1"
		subpackageDir2 = "my-subpackage-2"
		subpackageDir3 = "my-subpackage-3"

		newLabel         = "test-subpkg-content-modify"
		renamedSubpkgDir = "test-rename-subpkg"
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePR1V1 := t.createPR(repo, cloneePackageName+"-1", clonedWorkspaceV1)
	t.approvePR(cloneePR1V1)

	cloneePR1V2 := t.copyPR(repo, cloneePR1V1, clonedWorkspaceV2)
	t.approvePR(cloneePR1V2)

	cloneePR2V1 := t.createPR(repo, cloneePackageName+"-2", clonedWorkspaceV1)
	t.approvePR(cloneePR2V1)

	cloneePR2V2 := t.copyPR(repo, cloneePR2V1, clonedWorkspaceV2)
	t.approvePR(cloneePR2V2)

	cloneePR3V1 := t.createPR(repo, cloneePackageName+"-3", clonedWorkspaceV1)
	t.approvePR(cloneePR3V1)

	cloneePR3V2 := t.copyPR(repo, cloneePR3V1, clonedWorkspaceV2)
	t.approvePR(cloneePR3V2)

	parentPR := t.createPR(repo, parentPackageName, parentWorkspace)

	parentPR, err := t.cloneSubpackage(parentPR, cloneePR1V1, subpackageDir1)
	require.NoError(t.T(), err, "Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePR1V1, parentPR, subpackageDir1, err)

	parentPR, err = t.cloneSubpackage(parentPR, cloneePR2V1, subpackageDir2)
	require.NoError(t.T(), err, "Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePR2V1, parentPR, subpackageDir2, err)

	parentPR, err = t.cloneSubpackage(parentPR, cloneePR3V1, subpackageDir3)
	require.NoError(t.T(), err, "Clone of subpackage %v into parent PR %v subpackage directory %q failed: %v", cloneePR3V1, parentPR, subpackageDir3, err)

	var parentPRResources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)
	require.Contains(t.T(), parentPRResources.Spec.Resources, subpackageDir1+"/Kptfile")
	require.Contains(t.T(), parentPRResources.Spec.Resources, subpackageDir2+"/Kptfile")
	require.Contains(t.T(), parentPRResources.Spec.Resources, subpackageDir3+"/Kptfile")

	// Modify files in my-subpackage-1
	parentPRResources.Spec.Resources[subpackageDir1+"/my-configmap.yaml"] = strings.ReplaceAll(
		parentPRResources.Spec.Resources[subpackageDir1+"/my-configmap.yaml"],
		clonedWorkspaceV1, newLabel)
	parentPRResources.Spec.Resources[subpackageDir1+"/README.md.bak"] = parentPRResources.Spec.Resources[subpackageDir1+"/README.md"]
	delete(parentPRResources.Spec.Resources, subpackageDir1+"/README.md")

	// Rename my-subpackage-2
	maps.DeleteFunc(parentPRResources.Spec.Resources, func(k, v string) bool {
		if after, ok := strings.CutPrefix(k, subpackageDir2); ok {
			parentPRResources.Spec.Resources[renamedSubpkgDir+after] = v
			return true
		}
		return false
	})

	// Remove my-subpackage-3
	maps.DeleteFunc(parentPRResources.Spec.Resources, func(k, v string) bool {
		return strings.HasPrefix(k, subpackageDir3)
	})

	t.UpdateF(&parentPRResources)

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	// Check modifications were saved in my-subpackage-1
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/my-configmap.yaml"],
		"test-label-"+newLabel+": "+newLabel)
	assert.Contains(t, parentPRResources.Spec.Resources, subpackageDir1+"/README.md.bak")
	assert.NotContains(t, parentPRResources.Spec.Resources, subpackageDir1+"/README.md")

	// Check my-subpackage-2 is now test-rename-subpkg
	assert.NotContains(t, parentPRResources.Spec.Resources, subpackageDir2+"/Kptfile")
	assert.NotContains(t, parentPRResources.Spec.Resources, subpackageDir2+"/my-configmap.yaml")
	assert.NotContains(t, parentPRResources.Spec.Resources, subpackageDir2+"/README.md")
	assert.Contains(t, parentPRResources.Spec.Resources, renamedSubpkgDir+"/Kptfile")
	assert.Contains(t, parentPRResources.Spec.Resources, renamedSubpkgDir+"/my-configmap.yaml")
	assert.Contains(t, parentPRResources.Spec.Resources, renamedSubpkgDir+"/README.md")

	// Check my-subpackage-3 is removed
	assert.NotContains(t, parentPRResources.Spec.Resources, subpackageDir3+"/Kptfile")
	assert.NotContains(t, parentPRResources.Spec.Resources, subpackageDir3+"/my-configmap.yaml")
	assert.NotContains(t, parentPRResources.Spec.Resources, subpackageDir3+"/README.md")

	// Check upgrades succeed and fail as expected
	parentPR.ResourceVersion = parentPRResources.ResourceVersion
	parentPR, err = t.upgradeSubpackage(parentPR, cloneePR1V1, cloneePR1V2, subpackageDir1)
	assert.NoError(t, err)
	parentPR, err = t.upgradeSubpackage(parentPR, cloneePR2V1, cloneePR2V2, subpackageDir2)
	assert.ErrorContains(t, err, fmt.Sprintf("subpackage \"%s\" not found in package", subpackageDir2))
	parentPR, err = t.upgradeSubpackage(parentPR, cloneePR2V1, cloneePR2V2, renamedSubpkgDir)
	assert.NoError(t, err)
	parentPR, err = t.upgradeSubpackage(parentPR, cloneePR3V1, cloneePR3V2, subpackageDir3)
	assert.ErrorContains(t, err, fmt.Sprintf("subpackage \"%s\" not found in package", subpackageDir3))

	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	// Check modifications to my-subpackage-1 persisted through the upgrade
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/my-configmap.yaml"],
		"test-label-"+newLabel+": "+newLabel)
	assert.Contains(t, parentPRResources.Spec.Resources, subpackageDir1+"/README.md.bak")
	// Check previous state of my-subpackage-1 was merged in by the upgrade
	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir1+"/my-configmap.yaml"],
		"test-label-"+cloneePR1V1.Spec.WorkspaceName+": "+cloneePR1V1.Spec.WorkspaceName)
	assert.Contains(t, parentPRResources.Spec.Resources, subpackageDir1+"/README.md")

	// Check the renamed subpackage persisted through the upgrade
	assert.Contains(t, parentPRResources.Spec.Resources, renamedSubpkgDir+"/Kptfile")
	assert.Contains(t, parentPRResources.Spec.Resources, renamedSubpkgDir+"/my-configmap.yaml")
	assert.Contains(t, parentPRResources.Spec.Resources, renamedSubpkgDir+"/README.md")

	// Check my-subpackage-3 is still gone
	assert.NotContains(t, parentPRResources.Spec.Resources, subpackageDir3+"/Kptfile")
	assert.NotContains(t, parentPRResources.Spec.Resources, subpackageDir3+"/my-configmap.yaml")
	assert.NotContains(t, parentPRResources.Spec.Resources, subpackageDir3+"/README.md")

	t.deletePR(parentPR)
	t.deletePR(cloneePR1V2)
	t.deletePR(cloneePR1V1)
	t.deletePR(cloneePR2V2)
	t.deletePR(cloneePR2V1)
	t.deletePR(cloneePR3V2)
	t.deletePR(cloneePR3V1)
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

	var parentPRResources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      parentPR.Name,
	}, &parentPRResources)

	expectedSubpackageName, _ := porchapi.ComposeSubpkgObjName(subpackageDir)

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "name: "+expectedSubpackageName)
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

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "name: "+expectedSubpackageName)
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

	assert.Contains(t, parentPRResources.Spec.Resources[subpackageDir+"/Kptfile"], "name: "+expectedSubpackageName)
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

func (t *PorchSuite) createPR(subpackageRepo, packageName, workspace string) *porchapiv1alpha1.PackageRevision {
	// Create PackageRevision from upstream repo
	createdPR := t.CreatePackageSkeleton(subpackageRepo, packageName, workspace)
	createdPR.Spec.Tasks = []porchapiv1alpha1.Task{
		{
			Type: porchapiv1alpha1.TaskTypeInit,
			Init: &porchapiv1alpha1.PackageInitTaskSpec{
				Description: description,
			},
		},
	}
	t.CreateF(createdPR)

	// Check the package exists
	var pkg porchapiv1alpha1.PackageRevision
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: createdPR.Name}, &pkg)

	t.addPipelineToPR(createdPR)

	return createdPR
}

func (t *PorchSuite) copyPR(subpackageRepo string, sourcePr *porchapiv1alpha1.PackageRevision, workspace string) *porchapiv1alpha1.PackageRevision {
	// Copy PackageRevision from another packagerevision
	copiedPR := t.CreatePackageSkeleton(subpackageRepo, sourcePr.Spec.PackageName, workspace)
	copiedPR.Spec.Tasks = []porchapiv1alpha1.Task{
		{
			Type: porchapiv1alpha1.TaskTypeEdit,
			Edit: &porchapiv1alpha1.PackageEditTaskSpec{
				Source: &porchapiv1alpha1.PackageRevisionRef{
					Name: sourcePr.Name,
				},
			},
		},
	}
	t.CreateF(copiedPR)

	// Check the package exists
	var pkg porchapiv1alpha1.PackageRevision
	t.MustExist(client.ObjectKey{Namespace: t.Namespace, Name: copiedPR.Name}, &pkg)

	t.addPipelineToPR(copiedPR)

	return copiedPR
}

func (t *PorchSuite) cloneSubpackage(parentPR, cloneePR *porchapiv1alpha1.PackageRevision, subpackage string) (*porchapiv1alpha1.PackageRevision, error) {
	parentPR.Spec.Tasks = append(parentPR.Spec.Tasks, porchapiv1alpha1.Task{
		Type: porchapiv1alpha1.TaskTypeClone,
		Clone: &porchapiv1alpha1.PackageCloneTaskSpec{
			Upstream: porchapiv1alpha1.UpstreamPackage{
				Type: porchapiv1alpha1.RepositoryTypeGit,
				UpstreamRef: &porchapiv1alpha1.PackageRevisionRef{
					Name: cloneePR.Name,
				},
			},
			SubpackageDir: subpackage,
		},
	})

	err := t.Client.Update(t.GetContext(), parentPR)
	t.refreshPR(parentPR)

	return parentPR, err
}

func (t *PorchSuite) upgradeSubpackage(parentPR, oldCloneePR, newCloneePR *porchapiv1alpha1.PackageRevision, subpackage string) (*porchapiv1alpha1.PackageRevision, error) {
	parentPR.Spec.Tasks = append(parentPR.Spec.Tasks, porchapiv1alpha1.Task{
		Type: porchapiv1alpha1.TaskTypeUpgrade,
		Upgrade: &porchapiv1alpha1.PackageUpgradeTaskSpec{
			OldUpstream: porchapiv1alpha1.PackageRevisionRef{
				Name: oldCloneePR.Name,
			},
			NewUpstream: porchapiv1alpha1.PackageRevisionRef{
				Name: newCloneePR.Name,
			},
			LocalPackageRevisionRef: porchapiv1alpha1.PackageRevisionRef{
				Name: parentPR.Name,
			},
			SubpackageDir: subpackage,
		},
	})

	err := t.Client.Update(t.GetContext(), parentPR)
	t.refreshPR(parentPR)

	return parentPR, err
}

func (t *PorchSuite) deletePR(pr *porchapiv1alpha1.PackageRevision) {
	// Handle deletion if required
	if pr.Spec.Lifecycle == porchapiv1alpha1.PackageRevisionLifecyclePublished {
		pr.Spec.Lifecycle = porchapiv1alpha1.PackageRevisionLifecycleDeletionProposed
		t.UpdateApprovalF(pr)
	}
	t.DeleteE(&porchapiv1alpha1.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.Namespace,
			Name:      pr.Name,
		},
	})
	t.MustNotExist(pr)
}

func (t *PorchSuite) approvePR(pr *porchapiv1alpha1.PackageRevision) {
	pr.Spec.Lifecycle = porchapiv1alpha1.PackageRevisionLifecycleProposed
	t.UpdateF(pr)
	t.refreshPR(pr)

	pr.Spec.Lifecycle = porchapiv1alpha1.PackageRevisionLifecyclePublished
	t.UpdateApprovalF(pr)
}

// refreshPR refreshes a local cached PR so that its resourceVersion matches the resourceVersion of the PR in the cluster
func (t *PorchSuite) refreshPR(pr *porchapiv1alpha1.PackageRevision) {
	prKey := client.ObjectKey{
		Namespace: pr.Namespace,
		Name:      pr.Name,
	}

	t.GetF(prKey, pr)
}

// createParentWithBrokenPipeline creates a parent PR whose root Kptfile has a
// broken pipeline.
func (t *PorchSuite) createParentWithBrokenPipeline(repo, packageName, workspace string, withPushAnnotation bool) *porchapiv1alpha1.PackageRevision {
	parentPR := t.CreatePackageDraftF(repo, packageName, workspace)

	if withPushAnnotation {
		// Set push-on-render-failure annotation on the PackageRevision
		if parentPR.Annotations == nil {
			parentPR.Annotations = make(map[string]string)
		}
		parentPR.Annotations[porchapi.PushOnFnRenderFailureKey] = "true"
		t.UpdateF(parentPR)
		t.refreshPR(parentPR)
	}

	// Get resources and add a broken mutator
	parentResources := t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: parentPR.Name})
	t.AddMutator(parentResources, "quay.io/invalid/nonexistent-fn:v0.0.1")

	// Update should fail
	err := t.Client.Update(t.GetContext(), parentResources)
	t.Require().ErrorContains(err, "error rendering package in kpt function pipeline")

	// Refresh the parent PR so that the resource version is updated locally
	t.refreshPR(parentPR)
	return parentPR
}

// updateParentWithBrokenPipeline updates a parent PR with a root Kptfile that has a
// broken pipeline.
func (t *PorchSuite) updateParentWithBrokenPipeline(parentPR *porchapiv1alpha1.PackageRevision) {
	// Get resources and add a broken mutator
	parentResources := t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: parentPR.Name})
	t.AddMutator(parentResources, "quay.io/invalid/nonexistent-fn:v0.0.1")

	// Update should fail
	err := t.Client.Update(t.GetContext(), parentResources)
	t.Require().ErrorContains(err, "error rendering package in kpt function pipeline")

	// Refresh the parent PR so that the resource version is updated locally
	t.refreshPR(parentPR)
}

// ensureRenderIsPassing checks that the render is passing on the resources of a PR.
func (t *PorchSuite) ensureRenderIsPassing(pr *porchapiv1alpha1.PackageRevision) {

	resources := t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

	resources.Spec.Resources["configmap.yaml"] = `apiVersion: v1
kind: ConfigMap
metadata:
  name: test-cm
data:
  key: before-render
`
	err := t.Client.Update(t.GetContext(), resources)
	t.Require().NoError(err, "expected no error on render")

	delete(resources.Spec.Resources, "configmap.yaml")

	err = t.Client.Update(t.GetContext(), resources)
	t.Require().NoError(err, "expected no error on render")

	t.refreshPR(pr)
}

// ensureRenderIsFailing checks that the render is failing on the resources of a PR.
func (t *PorchSuite) ensureRenderIsFailing(pr *porchapiv1alpha1.PackageRevision) {

	resources := t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

	resources.Spec.Resources["configmap.yaml"] = `apiVersion: v1
kind: ConfigMap
metadata:
  name: test-cm
data:
  key: before-render
`
	err := t.Client.Update(t.GetContext(), resources)
	t.Require().Error(err, "expected render failure error")

	delete(resources.Spec.Resources, "configmap.yaml")

	err = t.Client.Update(t.GetContext(), resources)
	t.Require().Error(err, "expected render failure error")
	t.refreshPR(pr)
}

// TestSubpackageCloneRenderFailureNoPush verifies that when a subpackage clone
// to a parent PR where the parent PR had render failures and does NOT have the
// push-on-render-failure annotation, the update does not return an error and the
// subpackage resources are persisted. The render pipeline on the parent PR should always pass.
func (t *PorchSuite) TestSubpackageCloneRenderFailureNoPush() {
	t.skipIfLocalPodEvaluator()

	const (
		repo          = "subpkg-clone-render-fail-no-push"
		subpackageDir = "my-subpackage"
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePR := t.createPR(repo, "clonee", clonedWorkspaceV1)
	t.approvePR(cloneePR)

	parentPR := t.createParentWithBrokenPipeline(repo, parentPackageName, parentWorkspace, false)
	t.ensureRenderIsPassing(parentPR)

	// Annotation "porch.kpt.dev/push-on-render-failure" not set so the parent PR contains its initial
	// resources. The clone should work and the render after the clone should work as well.
	_, err := t.cloneSubpackage(parentPR, cloneePR, subpackageDir)
	t.Require().NoError(err, "subpackage clone into a parent with render failing should work if push-on-render-failure annotation not set")

	var resources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: parentPR.Name}, &resources)
	_, hasSubpkgKptfile := resources.Spec.Resources[subpackageDir+"/Kptfile"]
	t.True(hasSubpkgKptfile, "subpackage Kptfile should be persisted when push-on-render-failure is not set")

	t.ensureRenderIsPassing(parentPR)

	t.deletePR(parentPR)
	t.deletePR(cloneePR)
}

// TestSubpackageCloneRenderFailureWithPush verifies that when a subpackage clone
// to a parent PR where the parent PR has render failures and HAS the
// push-on-render-failure annotation, the update does not return an error and the
// subpackage resources are persisted. The render pipeline on the PR should always fail.
func (t *PorchSuite) TestSubpackageCloneRenderFailureWithPush() {
	t.skipIfLocalPodEvaluator()

	const (
		repo          = "subpkg-clone-render-fail-push"
		subpackageDir = "my-subpackage"
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePR := t.createPR(repo, "clonee", clonedWorkspaceV1)
	t.approvePR(cloneePR)

	parentPR := t.createParentWithBrokenPipeline(repo, parentPackageName, parentWorkspace, true)

	t.ensureRenderIsFailing(parentPR)

	_, err := t.cloneSubpackage(parentPR, cloneePR, subpackageDir)
	t.Require().NoError(err, "subpackage clone into a parent with render failing should work if push-on-render-failure annotation set")

	var resources porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: parentPR.Name}, &resources)
	_, hasSubpkgKptfile := resources.Spec.Resources[subpackageDir+"/Kptfile"]
	t.True(hasSubpkgKptfile, "subpackage Kptfile should be persisted when push-on-render-failure annotation is set")

	t.ensureRenderIsFailing(parentPR)

	t.deletePR(parentPR)
	t.deletePR(cloneePR)
}

// TestSubpackageUpgradeRenderFailureNoPush verifies that when a subpackage upgrade
// to a parent PR where the parent PR had render failures and does NOT have the
// push-on-render-failure annotation, the update does not return an error and the
// subpackage resources are persisted. The render pipeline on the parent PR should always pass.
func (t *PorchSuite) TestSubpackageUpgradeRenderFailureNoPush() {
	t.skipIfLocalPodEvaluator()

	const (
		repo          = "subpkg-upgrade-render-fail-no-push"
		subpackageDir = "my-subpackage"
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePRV1 := t.createPR(repo, "clonee", clonedWorkspaceV1)
	t.approvePR(cloneePRV1)
	cloneePRV2 := t.copyPR(repo, cloneePRV1, clonedWorkspaceV2)
	t.approvePR(cloneePRV2)

	parentPR := t.createParentWithBrokenPipeline(repo, parentPackageName, parentWorkspace, false)
	t.ensureRenderIsPassing(parentPR)

	// Annotation "porch.kpt.dev/push-on-render-failure" not set so the parent PR contains its initial
	// resources. The clone should work and the render after the clone should work as well.
	_, err := t.cloneSubpackage(parentPR, cloneePRV1, subpackageDir)
	t.Require().NoError(err, "subpackage clone into a parent with render failing should work if push-on-render-failure annotation not set")

	// Capture v1 subpackage Kptfile.
	var resourcesBefore porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: parentPR.Name}, &resourcesBefore)
	v1Kptfile, ok := resourcesBefore.Spec.Resources[subpackageDir+"/Kptfile"]
	t.Require().True(ok, "subpackage Kptfile should be present after clone with push-on-render-failure not set")
	t.Contains(resourcesBefore.Spec.Resources[subpackageDir+"/Kptfile"], "test-label-clonee-v1: clonee-v1", "subpackage Kptfile should contain \"test-label-clonee-v1: clonee-v1\"")

	t.updateParentWithBrokenPipeline(parentPR)
	t.ensureRenderIsPassing(parentPR)

	_, err = t.upgradeSubpackage(parentPR, cloneePRV1, cloneePRV2, subpackageDir)
	t.Require().NoError(err, "subpackage upgrade into a parent with render failing should work if push-on-render-failure annotation not set")

	var resourcesAfter porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: parentPR.Name}, &resourcesAfter)
	t.NotEqual(v1Kptfile, resourcesAfter.Spec.Resources[subpackageDir+"/Kptfile"],
		"subpackage Kptfile should be updated when push-on-render-failure is not set")
	t.Contains(resourcesAfter.Spec.Resources[subpackageDir+"/Kptfile"], "test-label-clonee-v2: clonee-v2", "subpackage Kptfile should contain \"test-label-clonee-v2: clonee-v2\"")

	t.ensureRenderIsPassing(parentPR)

	t.deletePR(parentPR)
	t.deletePR(cloneePRV2)
	t.deletePR(cloneePRV1)
}

// TestSubpackageUpgradeRenderFailureWithPush verifies that when a subpackage upgrade
// to a parent PR where the parent PR has render failures and HAS the
// push-on-render-failure annotation, the update does not return an error and the
// subpackage resources are persisted. The render pipeline on the PR should always fail.
func (t *PorchSuite) TestSubpackageUpgradeRenderFailureWithPush() {
	t.skipIfLocalPodEvaluator()

	const (
		repo          = "subpkg-upgrade-render-fail-push"
		subpackageDir = "my-subpackage"
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repo, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	cloneePRV1 := t.createPR(repo, "clonee", clonedWorkspaceV1)
	t.approvePR(cloneePRV1)
	cloneePRV2 := t.copyPR(repo, cloneePRV1, clonedWorkspaceV2)
	t.approvePR(cloneePRV2)

	parentPR := t.createParentWithBrokenPipeline(repo, parentPackageName, parentWorkspace, true)
	t.ensureRenderIsFailing(parentPR)

	// Annotation "porch.kpt.dev/push-on-render-failure" not set so the parent PR contains its initial
	// resources. The clone should work and the render after the clone should work as well.
	_, err := t.cloneSubpackage(parentPR, cloneePRV1, subpackageDir)
	t.Require().NoError(err, "subpackage clone into a parent with render failing should work if push-on-render-failure annotation not set")

	// Capture v1 subpackage Kptfile.
	var resourcesBefore porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: parentPR.Name}, &resourcesBefore)
	v1Kptfile, ok := resourcesBefore.Spec.Resources[subpackageDir+"/Kptfile"]
	t.Require().True(ok, "subpackage Kptfile should be present after clone with push-on-render-failure not set")
	t.Contains(resourcesBefore.Spec.Resources[subpackageDir+"/Kptfile"], "test-label-clonee-v1: clonee-v1", "subpackage Kptfile should contain \"test-label-clonee-v1: clonee-v1\"")

	t.updateParentWithBrokenPipeline(parentPR)
	t.ensureRenderIsFailing(parentPR)

	_, err = t.upgradeSubpackage(parentPR, cloneePRV1, cloneePRV2, subpackageDir)
	t.Require().NoError(err, "subpackage upgrade into a parent with render failing should work if push-on-render-failure annotation not set")

	var resourcesAfter porchapiv1alpha1.PackageRevisionResources
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: parentPR.Name}, &resourcesAfter)
	t.NotEqual(v1Kptfile, resourcesAfter.Spec.Resources[subpackageDir+"/Kptfile"],
		"subpackage Kptfile should be updated when push-on-render-failure is not set")
	t.Contains(resourcesAfter.Spec.Resources[subpackageDir+"/Kptfile"], "test-label-clonee-v2: clonee-v2", "subpackage Kptfile should contain \"test-label-clonee-v2: clonee-v2\"")

	t.ensureRenderIsFailing(parentPR)

	t.deletePR(parentPR)
	t.deletePR(cloneePRV2)
	t.deletePR(cloneePRV1)
}

func (t *PorchSuite) addPipelineToPR(pr *porchapiv1alpha1.PackageRevision) {
	var prResources porchapiv1alpha1.PackageRevisionResources

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
