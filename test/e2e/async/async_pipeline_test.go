// Copyright 2025 The kpt and Nephio Authors
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

package async

import (
	"strconv"
	"sync"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/scheduler"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var backoff = wait.Backoff{
	Duration: 1 * time.Second,
	Factor:   1.1,
	Steps:    10,
	Cap:      5 * time.Second,
}

func (t *AsyncPipelineSuite) TestAlreadyRenderingPackageRevisionResources() {

	const (
		gitRepository = "test-already-rendering-pr"
		sleepDuration = 5 * time.Second
	)

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), gitRepository, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	pr := t.CreatePackageSkeleton(gitRepository, "test", "init")
	t.CreateF(pr)

	t.Logf("Adding sleep function to package revision pipeline...")
	err := t.AddSleepFunctionToPipeline(client.ObjectKeyFromObject(pr), sleepDuration)
	t.NoError(err, "Failed to add sleep function to package revision pipeline")

	t.Logf("Waiting until render pipeline is in progress...")
	t.WaitUntilRenderInProgress(pr)

	t.Logf("Adding sleep function while pipeline is still running...")
	secondRenderStart := time.Now()
	err = t.AddSleepFunctionToPipeline(client.ObjectKeyFromObject(pr), sleepDuration)
	t.NoError(err, "Failed to add sleep function to package revision pipeline")

	t.WaitForRender(pr)
	t.GreaterOrEqual(time.Since(secondRenderStart), sleepDuration,
		"expected the second render to last more than the pipeline sleep interval")

}

func (t *AsyncPipelineSuite) TestConflictError() {
	const (
		repoName = "conflict-error"
	)

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	pr := t.CreatePackageDraftF(repoName, "test", "init")
	pr2 := pr.DeepCopy()

	pr.Labels = map[string]string{
		"test": "conflict-error",
	}
	t.UpdateF(pr)

	pr.Labels = map[string]string{
		"test": "conflict-error-2",
	}
	err := t.Client.Update(t.GetContext(), pr2)

	t.Require().Error(err, "Expected an conflict (optimistic locking) error")
	t.True(apierrors.IsConflict(err), "The error must be of Conflict type")
	t.ErrorContains(err, "the object has been modified")
}

func (t *AsyncPipelineSuite) TestAsyncPipelineBasics() {
	const (
		repoName      = "async-pipeline-basics"
		sleepDuration = 5 * time.Second
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	pr := t.CreatePackageSkeleton(repoName, "test", "init")
	t.CreateF(pr)
	t.True(pr.IsStatusConditionTrue(scheduler.RenderFinishedConditionType),
		"Packages with an empty pipeline should be marked as rendered instantly")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderedConditionType),
		"Packages with an empty pipeline should have a successful Rendered condition")

	t.Logf("Adding sleep function to package revision pipeline...")
	err := t.AddSleepFunctionToPipeline(client.ObjectKeyFromObject(pr), sleepDuration)
	t.NoError(err, "Failed to add sleep function to package revision pipeline")

	t.Logf("Waiting until render pipeline is in progress...")
	t.WaitUntilRenderInProgress(pr)
	t.Nil(pr.FindStatusCondition(scheduler.RenderedConditionType),
		"Rendered status condition should be missing during render")

	t.Logf("Trying to propose while pipeline is still running...")
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	err = t.Client.Update(t.GetContext(), pr)
	t.Require().Error(err, "Expected an conflict (optimistic locking) error")
	t.True(apierrors.IsConflict(err), "The error must be of Conflict type")
	t.ErrorContains(err, "a Render operation is still in progress")

	t.WaitForRender(pr)
	t.Logf("Package rendered")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderFinishedConditionType),
		"Render should be finished")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderedConditionType),
		"The sleep KRM functions should run successfully")

	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	err = retry.RetryOnConflict(backoff, func() error {
		return t.Client.Update(t.GetContext(), pr)
	})
	t.NoError(err, "Expected no error on propose after Render finished")
	t.GetF(client.ObjectKeyFromObject(pr), pr)
	t.Equal(porchapi.PackageRevisionLifecycleProposed, pr.Spec.Lifecycle,
		"The package revision must be in Proposed lifecycle after propose")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderFinishedConditionType),
		"A new render mustn't be started on propose")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderedConditionType),
		"The sleep KRM functions should run successfully")
}

func (t *AsyncPipelineSuite) TestContentUpdateDuringRender() {
	const (
		repoName      = "async-pipeline-update-while-render"
		sleepDuration = 5 * time.Second
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	pr := t.CreatePackageSkeleton(repoName, "test", "init")
	t.CreateF(pr)
	t.True(pr.IsStatusConditionTrue(scheduler.RenderFinishedConditionType),
		"Packages with an empty pipeline should be marked as rendered instantly")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderedConditionType),
		"Packages with an empty pipeline should have a successful Rendered condition")

	t.Logf("Adding sleep function to package revision pipeline...")
	err := t.AddSleepFunctionToPipeline(client.ObjectKeyFromObject(pr), sleepDuration)
	t.NoError(err, "Failed to add sleep function to package revision pipeline")

	t.Logf("Waiting until render pipeline is in progress...")
	t.WaitUntilRenderInProgress(pr)
	t.Nil(pr.FindStatusCondition(scheduler.RenderedConditionType),
		"Rendered status condition should be missing during render")

	t.Logf("Updating content while pipeline is still running...")
	var prr porchapi.PackageRevisionResources
	t.GetF(client.ObjectKeyFromObject(pr), &prr)
	prr.Spec.Resources["test.yaml"] = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n"
	updateStart := time.Now()
	err = t.Client.Update(t.GetContext(), &prr)
	t.NoError(err)

	t.WaitForRender(pr)
	t.Logf("Package rendered")
	t.GreaterOrEqual(time.Since(updateStart), sleepDuration, "The update must have triggered a new render")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderFinishedConditionType),
		"Render should be finished")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderedConditionType),
		"The sleep KRM functions should run successfully")
}

func (t *AsyncPipelineSuite) TestMetadataUpdateDuringRender() {
	const (
		repoName      = "async-pipeline-empty-update-while-render"
		sleepDuration = 5 * time.Second
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	var cm v1.ConfigMap
	cm.Name = "test-cm"
	cm.Namespace = t.Namespace
	cm.Data = map[string]string{"test": "data"}
	t.CreateF(&cm)
	t.Cleanup(func() {
		t.DeleteF(&cm)
	})

	pr := t.CreatePackageSkeleton(repoName, "test", "init")
	t.CreateF(pr)
	t.True(pr.IsStatusConditionTrue(scheduler.RenderFinishedConditionType),
		"Packages with an empty pipeline should be marked as rendered instantly")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderedConditionType),
		"Packages with an empty pipeline should have a successful Rendered condition")

	t.Logf("Adding sleep function to package revision pipeline...")
	err := t.AddSleepFunctionToPipeline(client.ObjectKeyFromObject(pr), sleepDuration)
	t.Require().NoError(err, "Failed to add sleep function to package revision pipeline")

	t.Logf("Waiting until render pipeline is in progress...")
	t.WaitUntilRenderInProgress(pr)
	t.Nil(pr.FindStatusCondition(scheduler.RenderedConditionType),
		"Rendered status condition should be missing during render")

	t.Logf("Updating metadata while pipeline is still running...")
	baseline := t.RenderSchedulerSnapshot(pr.Name)
	ownerRef := metav1.NewControllerRef(&cm, v1.SchemeGroupVersion.WithKind("ConfigMap"))
	pr.OwnerReferences = []metav1.OwnerReference{*ownerRef}
	err = t.Client.Update(t.GetContext(), pr)
	t.Require().NoError(err)

	t.WaitForRender(pr)
	t.Logf("Package rendered")
	t.AssertNoAdditionalRenderScheduled(pr.Name, baseline)
	t.True(pr.IsStatusConditionTrue(scheduler.RenderFinishedConditionType),
		"Render should be finished")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderedConditionType),
		"The sleep KRM functions should run successfully")
	t.Contains(pr.OwnerReferences, *ownerRef)
}

func (t *AsyncPipelineSuite) TestPublishedMetadataUpdateAfterRender() {
	const (
		repoName      = "async-pipeline-published-update-after-render"
		sleepDuration = 5 * time.Second
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	t.Logf("Create skeleton...")
	pr := t.CreatePackageSkeleton(repoName, "test", "init")
	t.CreateF(pr)
	prKey := client.ObjectKeyFromObject(pr)

	t.Logf("Adding sleep function to package revision pipeline...")
	err := t.AddSleepFunctionToPipeline(client.ObjectKeyFromObject(pr), sleepDuration)
	t.Require().NoError(err, "Failed to add sleep function to package revision pipeline")
	t.WaitForRender(pr)

	t.Logf("Propose...")
	t.GetF(prKey, pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(pr)
	t.Logf("Approve to be published...")
	t.GetF(prKey, pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	t.UpdateApprovalF(pr)
	t.GetF(prKey, pr)

	t.Logf("Updating after rendered...")
	t.GetF(prKey, pr)
	pr.Annotations = map[string]string{"config.kubernetes.io/local-config": "true"}
	t.Require().NoError(t.Client.Update(t.GetContext(), pr))

	t.Logf("Assert after update...")
	t.GetF(prKey, pr)
	t.True(pr.IsStatusConditionTrue(scheduler.RenderFinishedConditionType),
		"Render should be finished: %+v", pr)
	t.True(pr.IsStatusConditionTrue(scheduler.RenderedConditionType),
		"It should be Rendered: %+v", pr)
}

func (t *AsyncPipelineSuite) TestDraftMetadataUpdateAfterRender() {
	const (
		repoName      = "async-pipeline-draft-update-after-render"
		sleepDuration = 5 * time.Second
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	t.Logf("Create skeleton...")
	pr := t.CreatePackageSkeleton(repoName, "test", "init")
	t.CreateF(pr)
	prKey := client.ObjectKeyFromObject(pr)

	t.Logf("Adding sleep function to package revision pipeline...")
	err := t.AddSleepFunctionToPipeline(client.ObjectKeyFromObject(pr), sleepDuration)
	t.Require().NoError(err, "Failed to add sleep function to package revision pipeline")
	t.WaitForRender(pr)

	t.Logf("Updating after rendered...")
	t.GetF(prKey, pr)
	annotationsBeforeUpdate := pr.GetAnnotations()
	t.NotContains(annotationsBeforeUpdate, "config.kubernetes.io/local-config", "Annotation must be persisted before update")
	pr.Annotations = map[string]string{"config.kubernetes.io/local-config": "true"}
	pr.Labels = map[string]string{"config.kubernetes.io/local-config-label": "true"}
	t.Require().NoError(t.Client.Update(t.GetContext(), pr))

	t.Logf("Assert after update...")
	t.GetF(prKey, pr)
	annotationsAfterUpdate := pr.GetAnnotations()
	t.Contains(annotationsAfterUpdate, "config.kubernetes.io/local-config", "Annotation must be persisted after update")
	labelsAfterUpdate := pr.GetLabels()
	t.Contains(labelsAfterUpdate, "config.kubernetes.io/local-config-label", "Label must be persisted after update")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderFinishedConditionType),
		"Render should be finished: %+v", pr)
	t.True(pr.IsStatusConditionTrue(scheduler.RenderedConditionType),
		"It should be Rendered: %+v", pr)
}

func (t *AsyncPipelineSuite) TestParallelMetadataUpdateDuringRender() {
	t.Skipf("This was added for debugging purposes, but might be useful in the future")
	const (
		repoName      = "parallel-ownerref-update-while-render"
		sleepDuration = 5 * time.Second
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	var cm v1.ConfigMap
	cm.Name = "test-cm"
	cm.Namespace = t.Namespace
	cm.Data = map[string]string{"test": "data"}
	t.CreateF(&cm)
	t.Cleanup(func() {
		t.DeleteF(&cm)
	})
	ownerRef := metav1.NewControllerRef(&cm, v1.SchemeGroupVersion.WithKind("ConfigMap"))

	wg := &sync.WaitGroup{}

	for i := range 10 {
		wg.Add(1)
		go func() {
			pr := t.CreatePackageSkeleton(repoName, "test", "v"+strconv.Itoa(i+1))
			t.CreateF(pr)
			err := t.AddSleepFunctionToPipeline(client.ObjectKeyFromObject(pr), sleepDuration)
			t.Require().NoError(err, "Failed to add sleep function to package revision pipeline")

			time.Sleep(sleepDuration / 2)

			t.GetF(client.ObjectKeyFromObject(pr), pr)
			pr.OwnerReferences = []metav1.OwnerReference{*ownerRef}
			err = t.Client.Update(t.GetContext(), pr)
			t.Require().NoError(err)
			wg.Done()
		}()
	}

	wg.Wait()

	prs := &porchapi.PackageRevisionList{}
	t.ListF(prs, client.MatchingFields{"spec.repository": repoName})
	t.Require().Len(prs.Items, 10)
	for _, pr := range prs.Items {
		t.Contains(pr.OwnerReferences, *ownerRef)
	}

	for _, pr := range prs.Items {
		wg.Add(1)
		go func() {
			t.WaitForRender(&pr)
			wg.Done()
		}()
	}

	wg.Wait()

	t.ListF(prs, client.MatchingFields{"spec.repository": repoName})
	t.Require().Len(prs.Items, 10)
	for _, pr := range prs.Items {
		t.Contains(pr.OwnerReferences, *ownerRef)
	}
}

func (t *AsyncPipelineSuite) TestRenderReschedule() {
	const (
		repoName      = "async-pipeline-reschedule"
		sleepDuration = 5 * time.Second
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", t.GiteaUser, suiteutils.Password(t.GiteaPassword))

	pr := t.CreatePackageSkeleton(repoName, "test", "init")
	t.CreateF(pr)
	t.True(pr.IsStatusConditionTrue(scheduler.RenderFinishedConditionType),
		"Packages with an empty pipeline should be marked as rendered instantly")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderedConditionType),
		"Packages with an empty pipeline should have a successful Rendered condition")

	t.Logf("Adding sleep function to package revision pipeline...")
	renderStart := time.Now()
	err := t.AddSleepFunctionToPipeline(client.ObjectKeyFromObject(pr), sleepDuration)
	t.NoError(err, "Failed to add sleep function to package revision pipeline")

	t.Logf("Waiting until render pipeline is in progress...")
	t.WaitUntilRenderInProgress(pr)
	t.Nil(pr.FindStatusCondition(scheduler.RenderedConditionType),
		"Rendered status condition should be missing during render")

	t.Logf("Introduce update conflict during render")
	var prr porchapi.PackageRevisionResources
	t.GetF(client.ObjectKeyFromObject(pr), &prr)
	prr.Spec.Resources["test.yaml"] = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n"
	prr.Spec.DisableRender = true
	err = t.Client.Update(t.GetContext(), &prr)
	t.NoError(err)

	t.WaitForRender(pr)
	t.Logf("Package rendered")
	t.GreaterOrEqual(time.Since(renderStart), 2*sleepDuration,
		"The update must have triggered a reschedule of render after Update conflict")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderFinishedConditionType),
		"Render should be finished")
	t.True(pr.IsStatusConditionTrue(scheduler.RenderedConditionType),
		"The sleep KRM functions should run successfully")
}
