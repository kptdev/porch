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
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

const (
	bucketNamespace = "bucket-namespace"
	fooAnnotation   = "foo"
	barValue        = "bar"

	setNamespaceImage   = "set-namespace:v0.4.1"
	setAnnotationsImage = "set-annotations:v0.1.4"
	sleepImage          = "sleep:latest"
)

var podGVK = corev1.SchemeGroupVersion.WithKind("Pod")

func (t *PorchSuite) TestBuiltinFunctionEvaluator() {
	resources := t.setupFunctionTestPackage("git-builtin-fn-eval", "test-builtin-fn-bucket", "test-workspace")

	t.AddMutator(resources, t.KrmFunctionsRegistry+"/starlark:v0.4.3", suiteutils.WithConfigmap(map[string]string{
		"source": `for resource in ctx.resource_list["items"]:
		  resource["metadata"]["annotations"]["foo"] = "bar"`,
	}))
	t.AddMutator(resources, t.KrmFunctionsRegistry+"/"+setNamespaceImage, suiteutils.WithConfigmap(map[string]string{"namespace": bucketNamespace}))
	t.UpdateF(resources)

	t.validateBucketResource(resources, bucketNamespace, map[string]string{fooAnnotation: barValue})
}

func (t *PorchSuite) TestExecFunctionEvaluator() {
	resources := t.setupFunctionTestPackage("git-exec-fn-eval", "test-fn-bucket", "test-workspace")

	t.AddMutator(resources, t.KrmFunctionsRegistry+"/starlark:v0.3.0", suiteutils.WithConfigmap(map[string]string{
		"source": `# set the namespace on all resources

for resource in ctx.resource_list["items"]:

	  # mutate the resource
	  resource["metadata"]["namespace"] = "bucket-namespace"`,
	}))
	t.AddMutator(resources, t.KrmFunctionsRegistry+"/"+setAnnotationsImage, suiteutils.WithConfigmap(map[string]string{fooAnnotation: barValue}))
	t.UpdateF(resources)

	t.validateBucketResource(resources, bucketNamespace, map[string]string{fooAnnotation: barValue})
}

func (t *PorchSuite) TestPodFunctionEvaluatorWithDistrolessImage() {
	t.skipIfLocalPodEvaluator()

	resources := t.setupFunctionTestPackage("git-fn-distroless", "test-fn-distroless-bucket", "workspace-distroless", TestPackageSetupOptions{
		UpstreamRef: "redis-bucket/v1",
		UpstreamDir: "redis-bucket",
	})

	resources.Spec.Resources["configmap.yaml"] = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kptfile.kpt.dev
data:
  name: bucket-namespace
`

	t.AddMutator(resources, t.KrmFunctionsRegistry+"/"+setNamespaceImage, suiteutils.WithConfigPath("configmap.yaml"))
	t.UpdateF(resources)

	t.validateBucketResource(resources, bucketNamespace, nil)
}

func (t *PorchSuite) TestPodEvaluator() {
	t.skipIfLocalPodEvaluator()
	const repoName = "git-fn-pod-eval"

	setAnnotationsImg := t.KrmFunctionsRegistry + "/" + setAnnotationsImage
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create first package revision
	pr := t.CreatePackageCloneF(repoName, "test-fn-pod-bucket", "workspace-1", defaultBucketBpRef, "bucket")
	resources := t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})
	t.AddMutator(resources, setAnnotationsImg, suiteutils.WithConfigmap(map[string]string{"test-key": "test-val"}))
	t.UpdateF(resources)

	// Wait for the correct pod to be created and capture suffix
	var firstPodSuffix string
	var firstPod *corev1.Pod
	err := wait.PollUntilContextTimeout(t.GetContext(), time.Second, 30*time.Second, false, func(ctx context.Context) (bool, error) {
		podList := &corev1.PodList{}
		if err := t.Client.List(ctx, podList, client.InNamespace("porch-fn-system")); err != nil {
			return false, err
		}
		for _, pod := range podList.Items {
			if pod.Spec.Containers[0].Image == setAnnotationsImg {
				parts := strings.Split(pod.Name, "-")
				if len(parts) > 0 {
					firstPodSuffix = parts[len(parts)-1]
				}
				t.Logf("Found matching pod %s, captured suffix: %s", pod.Name, firstPodSuffix)
				t.DeleteF(&pod)
				firstPod = &pod
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("Failed to find pod with image %s: %v", setAnnotationsImg, err)
	}

	t.WaitUntilObjectDeleted(podGVK, client.ObjectKeyFromObject(firstPod), 30*time.Second)

	// Create second package revision
	pr2 := t.CreatePackageCloneF(repoName, "test-fn-pod-bucket-2", "workspace-2", defaultBucketBpRef, "bucket")
	resources = t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr2.Name})

	// Testing pod evaluator with golang function
	t.AddMutator(resources, setAnnotationsImg, suiteutils.WithConfigmap(map[string]string{"new-test-key": "new-test-val"}))
	t.UpdateF(resources)

	// Verify second pod has different suffix
	podList := &corev1.PodList{}
	t.ListF(podList, client.InNamespace("porch-fn-system"))
	var secondPodSuffix string
	for _, pod := range podList.Items {
		if pod.Spec.Containers[0].Image == setAnnotationsImg {
			parts := strings.Split(pod.Name, "-")
			if len(parts) > 0 {
				secondPodSuffix = parts[len(parts)-1]
			}
		}
	}
	if firstPodSuffix == secondPodSuffix {
		t.Errorf("Expected different fn pod suffixes for pr and pr2, but both had: %s", firstPodSuffix)
	} else {
		t.Logf("Successfully verified different fn pods: first=%s, second=%s", firstPodSuffix, secondPodSuffix)
	}
}

func (t *PorchSuite) TestPodEvaluatorWithFailure() {
	t.skipIfLocalPodEvaluator()

	resources := t.setupFunctionTestPackage("git-pod-eval-fn-failure", "test-fn-pod-bucket", defaultWorkspace, TestPackageSetupOptions{
		WaitForReady: true,
	})

	// Add invalid resource to make kubeconform fail
	resources.Spec.Resources["invalid.yaml"] = `apiVersion: v1
kind: Pod
metadata:
  name: invalid-resource
spec:
  replicas: "invalid"`

	t.AddMutator(resources, t.KrmFunctionsRegistry+"/kubeconform:v0.1.1")

	err := t.Client.Update(t.GetContext(), resources)
	if err == nil {
		t.Fatalf("expected error but got none")
	}
}

func (t *PorchSuite) TestFailedPodEvictionAndRecovery() {
	t.skipIfLocalPodEvaluator()

	// Define a bogus kpt function image that will fail (image doesn't exist)
	bogusFnImage := "quay.io/invalid/kpt-fn-broken:v0.0.1"

	prr := t.setupFunctionTestPackage("git-fn-failed-recovery", "test-fn-pod-eviction", defaultWorkspace)

	// Add an eval task that will fail
	t.AddMutator(prr, bogusFnImage)

	err := t.Client.Update(t.GetContext(), prr)

	// Assert: creation should fail, and the error should reflect evaluator pod failure
	t.Require().ErrorContains(err, "error rendering package in kpt function pipeline")

	// Optional: verify no stuck pods exist for the failed image
	pods := &corev1.PodList{}
	t.ListF(pods, client.InNamespace(t.Namespace))

	for _, pod := range pods.Items {
		if strings.Contains(pod.Spec.Containers[0].Image, "kpt-fn-broken") {
			if pod.Status.Phase == corev1.PodFailed {
				t.Logf("Found pod %s in Failed state (expected, cleanup should follow)", pod.Name)
			} else {
				t.Logf("Found pod %s in %s state", pod.Name, pod.Status.Phase)
			}
		}
	}
}

type TestPackageSetupOptions struct {
	UpstreamRef  string
	UpstreamDir  string
	WaitForReady bool
}

func (t *PorchSuite) setupFunctionTestPackage(repoName, packageName, workspace string, opts ...TestPackageSetupOptions) *porchapi.PackageRevisionResources {
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Set defaults
	upstreamRef := defaultBucketBpRef
	upstreamDir := "bucket"
	waitForReady := false

	// Apply options
	if len(opts) > 0 {
		if opts[0].UpstreamRef != "" {
			upstreamRef = opts[0].UpstreamRef
		}
		if opts[0].UpstreamDir != "" {
			upstreamDir = opts[0].UpstreamDir
		}
		waitForReady = opts[0].WaitForReady
	}

	// Wait for Repository Controller to sync
	t.WaitUntilRepositoryReady(repoName, t.Namespace)

	// Always create new package with clone
	pr := t.CreatePackageCloneF(repoName, packageName, workspace, upstreamRef, upstreamDir)

	if waitForReady {
		return t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})
	}

	resources := &porchapi.PackageRevisionResources{}
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: pr.Name}, resources)
	return resources
}

func (t *PorchSuite) validateBucketResource(resources *porchapi.PackageRevisionResources, expectedNamespace string, expectedAnnotations map[string]string) {
	bucket, ok := resources.Spec.Resources["bucket.yaml"]
	if !ok {
		t.Errorf("'bucket.yaml' not found among package resources")
		return
	}
	node, err := yaml.Parse(bucket)
	if err != nil {
		t.Errorf("yaml.Parse(\"bucket.yaml\") failed: %v", err)
		return
	}
	if got := node.GetNamespace(); got != expectedNamespace {
		t.Errorf("StorageBucket namespace: got %q, want %q", got, expectedNamespace)
	}
	if expectedAnnotations != nil {
		annotations := node.GetAnnotations()
		for key, expectedVal := range expectedAnnotations {
			if val, found := annotations[key]; !found || val != expectedVal {
				t.Errorf("StorageBucket annotations should contain %s=%s, but got %v", key, expectedVal, annotations)
			}
		}
	}
}

func (t *PorchSuite) TestPodEvaluatorSequentialParallel() {
	const (
		repoName = "git-fn-krm-sleep"
	)
	if t.TestRunnerIsLocal {
		t.Skipf("Skipping due to local mode without pod evaluator")
	}

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), "git-fn-krm-sleep", "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	makeResources := func(wsSuffix, packageSuffix string) *porchapi.PackageRevisionResources {
		pr := t.CreatePackageCloneF(repoName, "test-bucket"+packageSuffix, defaultWorkspace+wsSuffix, defaultBucketBpRef, "bucket")

		res := &porchapi.PackageRevisionResources{}
		t.GetF(client.ObjectKeyFromObject(pr), res)
		return res
	}

	const (
		sleepDur = 5 // seconds
		extra    = 3 // seconds
	)
	expectedSequential := (2*sleepDur + extra) * time.Second

	pods := &corev1.PodList{}

	// TODO: review
	// Parallel run
	prr1, prr2 := makeResources("par1", "par1"), makeResources("par2", "par2")
	t.AddMutator(prr1, t.KrmFunctionsRegistry+"/"+sleepImage, suiteutils.WithConfigmap(map[string]string{"sleepSeconds": strconv.Itoa(sleepDur)}))
	t.AddMutator(prr2, t.KrmFunctionsRegistry+"/"+sleepImage, suiteutils.WithConfigmap(map[string]string{"sleepSeconds": strconv.Itoa(sleepDur)}))

	var wg sync.WaitGroup
	wg.Add(2)
	startPar := time.Now()
	go func() {
		defer wg.Done()
		t.UpdateF(prr1)
	}()
	go func() {
		defer wg.Done()
		t.UpdateF(prr2)
	}()
	wg.Wait()
	timeTaken := time.Since(startPar)
	t.Logf("Parallel duration: %v", timeTaken)

	t.LessOrEqual(timeTaken, expectedSequential, "Parallel duration too high")
	t.GreaterOrEqual(timeTaken, sleepDur*time.Second, "Parallel duration too low (the sleep function did not work?)")

	t.ListF(pods, client.InNamespace(t.FnNamespaceName()))
	for _, p := range pods.Items {
		if strings.Contains(p.Spec.Containers[0].Image, "krm-sleeper") {
			t.DeleteF(&p)
		}
	}
}

func (t *PorchSuite) TestPodEvaluatorParallelExecution() {
	if t.TestRunnerIsLocal {
		t.Skipf("Skipping: pod evaluator is not enabled in local test mode.")
	}

	const (
		repoName               = "git-fn-parallel-sleep"
		parallelRequestCount   = 4
		maxWaitList            = 2 // this should be kept in sync with the max-wait-list argument of the function-runner
		expectedPodCount       = (parallelRequestCount + maxWaitList - 1) / maxWaitList
		sleepDuration          = 3 * time.Second
		singleFunctionTime     = sleepDuration
		expectedSequentialTime = parallelRequestCount * sleepDuration * 5 / 2 // add some buffer to account for overhead and slow ci
		pollTimeout            = expectedSequentialTime * 5 / 4               // +0.25 headroom
	)

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	var wg sync.WaitGroup
	wg.Add(parallelRequestCount)
	errChan := make(chan error, parallelRequestCount)

	startTime := time.Now()

	for i := range parallelRequestCount {
		go func(idx int) {
			defer wg.Done()
			packageName := fmt.Sprintf("parallel-pkg-%d", idx)
			workspaceName := fmt.Sprintf("workspace-%d", idx)

			pr := t.CreatePackageDraftF(repoName, packageName, workspaceName)
			t.Logf("PkgRev #%d: Adding sleep function to pipeline", idx)

			err := t.AddSleepFunctionToPipeline(client.ObjectKeyFromObject(pr), sleepDuration)
			if err != nil {
				t.Errorf("PkgRev #%d: Failed to add sleep function: %v", idx, err)
				errChan <- fmt.Errorf("PkgRev #%d: %w", idx, err)
				return
			}
			t.Logf("PkgRev #%d: successfully evaluated sleep function for %s", idx, packageName)
		}(i)
	}

	t.Logf("Waiting to observe %d parallel sleep function evaluator pods: %s", parallelRequestCount, t.KrmFunctionsRegistry+"/"+sleepImage)
	err := wait.PollUntilContextTimeout(t.GetContext(), 1*time.Second, pollTimeout, true, func(ctx context.Context) (bool, error) {
		select {
		case err := <-errChan:
			return true, err
		default:
		}

		podList := &corev1.PodList{}
		if err := t.Client.List(ctx, podList, client.InNamespace(t.FnNamespaceName())); err != nil {
			t.Logf("Error listing pods: %v", err)
			return false, nil
		}

		runningPods := 0
		for _, pod := range podList.Items {
			if len(pod.Spec.Containers) > 0 && strings.Contains(pod.Spec.Containers[0].Image, t.KrmFunctionsRegistry+"/"+sleepImage) && pod.DeletionTimestamp == nil && pod.Status.Phase == corev1.PodRunning {
				runningPods++
			}
		}
		t.Logf("Found %d running pods for the sleep function", runningPods)
		if runningPods == expectedPodCount {
			return true, nil
		}
		if runningPods > expectedPodCount {
			return false,
				fmt.Errorf("Found more than expected running pods for the sleep function: got: %d, want: %d",
					runningPods, expectedPodCount)
		}
		return false, nil
	})
	assert.NoError(t, err, "Failed to observe parallel pod creation. This indicates the podCacheManager did not scale up as expected.")

	t.Logf("Waiting for all %v sleep functions to complete.", parallelRequestCount)
	wg.Wait()

	totalDuration := time.Since(startTime)
	t.Logf("Total duration for parallel execution: %v", totalDuration)

	close(errChan)
	for e := range errChan {
		t.Errorf("An error occurred during parallel execution: %v", e)
	}

	assert.Less(t, totalDuration, expectedSequentialTime, "Total duration for parallel run was too long, suggesting sequential execution.")
	assert.Greater(t, totalDuration, singleFunctionTime, "Total duration was too fast, suggesting the sleep function did not wait long enough.")

	t.Log("All parallel evaluations completed, and duration check passed.")
}

func (t *PorchSuite) skipIfLocalPodEvaluator() {
	if t.TestRunnerIsLocal {
		t.Skipf("Skipping due to not having pod evaluator in local mode")
	}
}
