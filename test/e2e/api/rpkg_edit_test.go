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
	"strings"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (t *PorchSuite) TestEditPackageRevision() {
	const (
		repository                    = "edit-test"
		packageName                   = "simple-package"
		otherPackageName              = "other-package"
		workspace                     = defaultWorkspace
		workspace2                    = "workspace2"
		workspaceToAvoidCreationClash = "workspace-to-avoid-creation-clash"
	)

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create PackageRevision from upstream repo
	pr := t.CreatePackageSkeleton(repository, packageName, workspace)
	t.CreateF(pr)

	// Create a new revision, but with a different package as the source.
	// This is not allowed.
	invalidEditPR := t.CreatePackageSkeleton(repository, otherPackageName, workspace2)
	invalidEditPR.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeEdit,
			Edit: &porchapi.PackageEditTaskSpec{
				Source: &porchapi.PackageRevisionRef{
					Name: pr.Name,
				},
			},
		},
	}

	if err := t.Client.Create(t.GetContext(), invalidEditPR); err == nil {
		t.Fatalf("Expected error for source revision being from different package")
	}

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

	// This invalid create will still create the draft for a small period of time until the error is discovered
	if err := t.Client.Create(t.GetContext(), editPR); err == nil {
		t.Fatalf("Expected error for source revision not being published")
	}

	// Publish the source package to make it a valid source for edit.
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(pr)

	// Approve the package
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	t.UpdateApprovalF(pr)

	// Changing the workspace of the EditPR to avoid clashing with invalid create negative test above
	editPR.Spec.WorkspaceName = workspaceToAvoidCreationClash

	// Create a new revision with the edit task.
	t.CreateF(editPR)

	// Check its task list
	var pkgRev porchapi.PackageRevision
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      editPR.Name,
	}, &pkgRev)
	tasks := pkgRev.Spec.Tasks
	for _, tsk := range tasks {
		t.Logf("Task: %s", tsk.Type)
	}
	assert.Len(t, tasks, 1)
	assert.Equal(t, porchapi.TaskTypeEdit, tasks[0].Type)
	assert.NotNil(t, tasks[0].Edit)
	assert.Equal(t, pr.Name, tasks[0].Edit.Source.Name)

	// Check its package size
	t.validatePackageResourcesSize(&pkgRev)

	// Create a new revision with a placeholder package revision as the source.
	// This is not allowed.
	editPlaceholderPR := t.CreatePackageSkeleton(repository, packageName, workspace2)
	editPlaceholderPR.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeEdit,
			Edit: &porchapi.PackageEditTaskSpec{
				Source: &porchapi.PackageRevisionRef{
					Name: strings.ReplaceAll(editPR.Name, workspaceToAvoidCreationClash, "main"),
				},
			},
		},
	}

	err := t.Client.Create(t.GetContext(), editPlaceholderPR)
	assert.ErrorContains(t, err, "placeholder package revision", "Expected error editing the placeholder package revision")
}

func (t *PorchSuite) TestUpdateResources() {
	const (
		repository  = "re-render-test"
		packageName = "simple-package"
		workspace   = defaultWorkspace
	)

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create a new package (via init)
	pr := t.CreatePackageDraftF(repository, packageName, workspace)

	// Check its package size
	t.validatePackageResourcesSize(pr)

	// Get the package resources
	var prResources porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      pr.Name,
	}, &prResources)

	// Add function into a pipeline
	kptfile := t.ParseKptfileF(&prResources)
	if kptfile.Pipeline == nil {
		kptfile.Pipeline = &kptfilev1.Pipeline{}
	}
	kptfile.Pipeline.Mutators = append(kptfile.Pipeline.Mutators, kptfilev1.Function{
		Image: t.KrmFunctionsRegistry + "/set-annotations:v0.1.4",
		ConfigMap: map[string]string{
			"color": "red",
			"fruit": "apple",
		},
		Name: "set-annotations",
	})

	t.SaveKptfileF(&prResources, kptfile)

	// Add a new resource
	addedResource := `apiVersion: v1
kind: ConfigMap
metadata:
  name: update-resources-configmap
  namespace: example
data:
  value: Update Resources and Render
`
	prResources.Spec.Resources["config-map.yaml"] = addedResource
	t.UpdateF(&prResources)

	// Re-fetch the resources to get the actual rendered result
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      pr.Name,
	}, &prResources)

	updated, ok := prResources.Spec.Resources["config-map.yaml"]
	if !ok {
		t.Fatalf("Updated config map config-map.yaml not found")
	}

	// Check the PR's package size again to ensure the ResourcesSizeBytes reflects the added resource
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      pr.Name,
	}, pr)
	t.validatePackageResourcesSize(pr)

	renderStatus := prResources.Status.RenderStatus
	assert.Empty(t, renderStatus.Err, "render error must be empty for successful render operation.")
	assert.Zero(t, renderStatus.Result.ExitCode, "exit code must be zero for successful render operation.")

	wantConfigMap := `apiVersion: v1
data:
  value: Update Resources and Render
kind: ConfigMap
metadata:
  annotations:
    color: red
    fruit: apple
  name: update-resources-configmap
  namespace: example
`
	if diff := t.CompareStringYAML(wantConfigMap, updated); diff != "" {
		t.Errorf("Unexpected updated config map contents: (-want,+got): %s", diff)
	}
	t.Logf("ConfigMap content matches expected result after KRM function processing")
}

func (t *PorchSuite) TestUpdateResourcesEmptyPatch() {
	const (
		repository  = "empty-patch-test"
		packageName = "simple-package"
		workspace   = defaultWorkspace
	)

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create a new package (via init)
	pr := t.CreatePackageDraftF(repository, packageName, workspace)

	// Get the package resources
	var prResources porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      pr.Name,
	}, &prResources)

	// Update with no changes (empty patch)
	t.UpdateF(&prResources)

	// Verify the update succeeded without errors
	renderStatus := prResources.Status.RenderStatus
	assert.Empty(t, renderStatus.Err, "render error must be empty for empty patch operation.")
	assert.Zero(t, renderStatus.Result.ExitCode, "exit code must be zero for empty patch operation.")
}
