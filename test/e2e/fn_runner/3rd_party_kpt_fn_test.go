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

package fn_runner

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kptdev/krm-functions-sdk/go/fn"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

type FunctionRunnerSuite struct {
	suiteutils.TestSuiteWithGit
}

type functionImage struct {
	image string
}

type FunctionVersion struct {
	Skip   bool
	Latest string
	Minus1 string
	Other  string
}

type functionVersions struct {
	Versions map[string]FunctionVersion `yaml:"versions,omitempty"`
}

func TestE2E(t *testing.T) {
	// https://github.com/kptdev/porch/pull/256
	// Updating 3rd party dependencies may break existing kpt functions because of api incompatibility.
	// This suite goes though all the used base kpt functions.
	if os.Getenv("TPP") == "" {
		t.Skip("set TPP to run this test")
	}
	suite.Run(t, &FunctionRunnerSuite{
		TestSuiteWithGit: suiteutils.TestSuiteWithGit{
			UseGitea: true,
		},
	})
}

func (t *FunctionRunnerSuite) TestApplySetters() {
	testCases := t.loadTestCases("apply-setters")

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), "test-apply-setters", "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	for tn, tc := range testCases {
		t.Run(tn, func() {
			pr := t.createTestPackage("test-apply-setters")
			t.Cleanup(func() {
				t.doCleanup(pr, tc.image)
			})

			// Get package resources
			var resources = t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

			t.AddResourceToPackage(resources, "testdata/project.yaml", "project.yaml")

			t.AddMutator(resources, tc.image, suiteutils.WithConfigmap(map[string]string{
				"projects-namespace": "updated-projects",
			}))

			t.UpdateF(resources)
			t.failOnRenderError(resources)

			for name, obj := range resources.Spec.Resources {
				if strings.HasPrefix(name, "project") {
					node, err := yaml.Parse(obj)
					if err != nil {
						t.Errorf("failed to parse Folder object: %v", err)
					}
					namespace := node.GetNamespace()
					if namespace != "updated-projects" {
						t.Errorf("Project should contain namespace `updated-projects`, the namespace we got: %v", namespace)
					}
				}
			}
		})

	}
}

func (t *FunctionRunnerSuite) TestSetNamespace() {
	testCases := t.loadTestCases("set-namespace")

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), "test-set-namespace", "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	for tn, tc := range testCases {
		t.Run(tn, func() {
			pr := t.createTestPackage("test-set-namespace")
			t.Cleanup(func() {
				t.doCleanup(pr, tc.image)
			})

			// Get package resources
			var resources = t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

			t.AddResourceToPackage(resources, "testdata/bucket.yaml", "bucket.yaml")

			t.AddMutator(resources, tc.image, suiteutils.WithConfigmap(map[string]string{
				"namespace": "updated-namespace",
			}))

			t.UpdateF(resources)
			t.failOnRenderError(resources)

			bucket, ok := resources.Spec.Resources["bucket.yaml"]
			if !ok {
				t.Errorf("'bucket.yaml' not found among package resources")
			}

			node, err := yaml.Parse(bucket)
			if err != nil {
				t.Errorf("yaml.Parse(\"bucket.yaml\") failed: %v", err)
			}
			namespace := node.GetNamespace()
			if namespace != "updated-namespace" {
				t.Errorf("Project should contain namespace `updated-namespace`, the namespace we got: %v", namespace)
			}
		})

	}
}

func (t *FunctionRunnerSuite) TestSetLabels() {
	testCases := t.loadTestCases("set-labels")

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), "test-set-labels", "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	for tn, tc := range testCases {
		t.Run(tn, func() {
			pr := t.createTestPackage("test-set-labels")
			t.Cleanup(func() {
				t.doCleanup(pr, tc.image)
			})

			// Get package resources
			var resources = t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

			t.AddResourceToPackage(resources, "testdata/daemonset.yaml", "daemonset.yaml")

			t.AddMutator(resources, tc.image, suiteutils.WithConfigmap(map[string]string{
				"app": "updated-cloud-sql-auth-proxy",
			}))

			t.UpdateF(resources)
			t.failOnRenderError(resources)

			// Get package resources again
			resources = t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

			daemonset, ok := resources.Spec.Resources["daemonset.yaml"]
			if !ok {
				t.Errorf("'daemonset.yaml' not found among package resources")
			}

			node, err := yaml.Parse(daemonset)
			if err != nil {
				t.Errorf("yaml.Parse(\"daemonset.yaml\") failed: %v", err)
			}
			labels := node.GetLabels()
			if labels["app"] != "updated-cloud-sql-auth-proxy" {
				t.Errorf("Project should contain label `app: updated-cloud-sql-auth-proxy`, the labels we got: %v", labels)
			}
		})

	}
}

func (t *FunctionRunnerSuite) TestSetAnnotations() {
	testCases := t.loadTestCases("set-annotations")

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), "test-set-annotations", "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	for tn, tc := range testCases {
		t.Run(tn, func() {
			pr := t.createTestPackage("test-set-annotations")
			t.Cleanup(func() {
				t.doCleanup(pr, tc.image)
			})

			// Get package resources
			var resources = t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

			t.AddResourceToPackage(resources, "testdata/daemonset.yaml", "daemonset.yaml")

			t.AddMutator(resources, tc.image, suiteutils.WithConfigmap(map[string]string{
				"cnrm.cloud.google.com/blueprint": "updated-cnrm/sql/auth-proxy/v0.2.0",
			}))

			t.UpdateF(resources)
			t.failOnRenderError(resources)

			daemonset, ok := resources.Spec.Resources["daemonset.yaml"]
			if !ok {
				t.Errorf("'daemonset.yaml' not found among package resources")
			}

			node, err := yaml.Parse(daemonset)
			if err != nil {
				t.Errorf("yaml.Parse(\"daemonset.yaml\") failed: %v", err)
			}
			annotations := node.GetAnnotations()
			if val, found := annotations["cnrm.cloud.google.com/blueprint"]; !found || val != "updated-cnrm/sql/auth-proxy/v0.2.0" {
				t.Errorf("Project should contain annotation `cnrm.cloud.google.com/blueprint: updated-cloud-sql-auth-proxy`, the annotations we got: %v", annotations)
			}
		})

	}
}

func (t *FunctionRunnerSuite) TestSearchReplace() {
	testCases := t.loadTestCases("search-replace")

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), "test-search-replace", "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	for tn, tc := range testCases {
		t.Run(tn, func() {
			pr := t.createTestPackage("test-search-replace")
			t.Cleanup(func() {
				t.doCleanup(pr, tc.image)
			})

			// Get package resources
			var resources = t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

			t.AddResourceToPackage(resources, "testdata/service.yaml", "service.yaml")

			t.AddMutator(resources, tc.image, suiteutils.WithConfigmap(map[string]string{
				"by-value":  "cloud-sql-auth-proxy",
				"put-value": "updated-cloud-sql-auth-proxy",
			}))

			t.UpdateF(resources)
			t.failOnRenderError(resources)

			service, ok := resources.Spec.Resources["service.yaml"]
			if !ok {
				t.Errorf("'service.yaml' not found among package resources")
			}

			node, err := yaml.Parse(service)
			if err != nil {
				t.Errorf("yaml.Parse(\"service.yaml\") failed: %v", err)
			}
			selector_value, err := node.GetFieldValue("spec.selector.app")
			if err != nil {
				t.Errorf("failed to parse external object: %v", err)
			}
			if selector_value != "updated-cloud-sql-auth-proxy" {
				t.Errorf("Project should contain selector.app with value `updated-cloud-sql-auth-proxy`, the value we got: %v", selector_value)
			}
		})

	}
}

func (t *FunctionRunnerSuite) TestStarlark() {
	testCases := t.loadTestCases("starlark")

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), "test-starlark", "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	for tn, tc := range testCases {
		t.Run(tn, func() {
			pr := t.createTestPackage("test-starlark")
			t.Cleanup(func() {
				t.doCleanup(pr, tc.image)
			})

			// Get package resources
			var resources = t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

			t.AddResourceToPackage(resources, "testdata/bucket.yaml", "bucket.yaml")

			t.AddMutator(resources, tc.image, suiteutils.WithConfigmap(map[string]string{
				"source": `for resource in ctx.resource_list["items"]:
  resource["metadata"]["annotations"]["foo"] = "bar"`,
			}))

			t.UpdateF(resources)
			t.failOnRenderError(resources)

			bucket, ok := resources.Spec.Resources["bucket.yaml"]
			if !ok {
				t.Errorf("'bucket.yaml' not found among package resources")
			}

			node, err := yaml.Parse(bucket)
			if err != nil {
				t.Errorf("yaml.Parse(\"bucket.yaml\") failed: %v", err)
			}
			annotations := node.GetAnnotations()
			if val, found := annotations["foo"]; !found || val != "bar" {
				t.Errorf("StorageBucket annotations should contain foo=bar, but got %v", annotations)
			}
		})

	}
}

func (t *FunctionRunnerSuite) TestEnsureNameSubstring() {
	testCases := t.loadTestCases("ensure-name-substring")

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), "test-ensure-name-substring", "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	for tn, tc := range testCases {
		t.Run(tn, func() {
			pr := t.createTestPackage("test-ensure-name-substring")
			t.Cleanup(func() {
				t.doCleanup(pr, tc.image)
			})

			// Get package resources
			var resources = t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

			t.AddResourceToPackage(resources, "testdata/service.yaml", "service.yaml")

			t.AddMutator(resources, tc.image, suiteutils.WithConfigmap(map[string]string{
				"append": "-test",
			}))

			t.UpdateF(resources)
			t.failOnRenderError(resources)

			service := resources.Spec.Resources["service.yaml"]

			node, err := yaml.Parse(service)
			if err != nil {
				t.Errorf("yaml.Parse(\"service.yaml\") failed: %v", err)
			}
			resourceName := node.GetName()
			expectedResourceName := "cloud-sql-auth-proxy-test"
			if resourceName != expectedResourceName {
				t.Errorf("Project should contain selector.app with value `%s`, the value we got: %s", expectedResourceName, resourceName)
			}
		})

	}
}

func (t *FunctionRunnerSuite) TestSetImage() {
	testCases := t.loadTestCases("set-image")

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), "test-set-image", "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	for tn, tc := range testCases {
		t.Run(tn, func() {
			pr := t.createTestPackage("test-set-image")
			t.Cleanup(func() {
				t.doCleanup(pr, tc.image)
			})

			// Get package resources
			var resources = t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

			t.AddResourceToPackage(resources, "testdata/daemonset.yaml", "daemonset.yaml")

			t.AddMutator(resources, tc.image, suiteutils.WithConfigmap(map[string]string{
				"name":    "gcr.io/cloud-sql-connectors/cloud-sql-proxy",
				"newName": "bitnami/nginx-updated",
				"newTag":  "1.22.0",
			}))

			t.UpdateF(resources)
			t.failOnRenderError(resources)

			daemonset, ok := resources.Spec.Resources["daemonset.yaml"]
			if !ok {
				t.Errorf("'daemonset.yaml' not found among package resources")
			}

			node, err := yaml.Parse(daemonset)
			if err != nil {
				t.Errorf("yaml.Parse(\"daemonset.yaml\") failed: %v", err)
			}
			containerNode, err := node.Pipe(yaml.Lookup("spec", "template", "spec", "containers"))
			if err != nil {
				t.Errorf("failed to parse containers: %v", err)
			}
			containers, err := containerNode.Elements()
			if err != nil {
				t.Errorf("failed to get elements: %v", err)
			}
			imageNode, err := containers[0].Pipe(yaml.Lookup("image"))
			if err != nil {
				t.Errorf("failed to parse image node: %v", err)
			}
			imageName, err := imageNode.String()
			if err != nil {
				t.Errorf("failed to parse image name: %v", err)
			}
			expectedName := "bitnami/nginx-updated:1.22.0"
			if strings.TrimSpace(imageName) != expectedName {
				t.Errorf("Daemonset should contain image with value `%s`, the value we got: %s", expectedName, imageName)
			}
		})

	}
}

func (t *FunctionRunnerSuite) TestApplyReplacements() {
	testCases := t.loadTestCases("apply-replacements")

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), "test-apply-replacements", "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	for tn, tc := range testCases {
		t.Run(tn, func() {
			pr := t.createTestPackage("test-apply-replacements")
			t.Cleanup(func() {
				t.doCleanup(pr, tc.image)
			})

			// Get package resources
			var resources = t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

			t.AddResourceToPackage(resources, "testdata/applyreplacement/job.yaml", "job.yaml")
			t.AddResourceToPackage(resources, "testdata/applyreplacement/resources.yaml", "resources.yaml")
			t.AddResourceToPackage(resources, "testdata/applyreplacement/applyreplacement.yaml", "applyreplacement.yaml")

			t.AddMutator(resources, tc.image, suiteutils.WithConfigPath("applyreplacement.yaml"))

			t.UpdateF(resources)
			t.failOnRenderError(resources)

			job, ok := resources.Spec.Resources["job.yaml"]
			if !ok {
				t.Errorf("'job.yaml' not found among package resources")
			}

			node, err := yaml.Parse(job)
			if err != nil {
				t.Errorf("yaml.Parse(\"job.yaml\") failed: %v", err)
			}
			restartPolicy, err := node.GetFieldValue("spec.template.spec.restartPolicy")
			if err != nil {
				t.Errorf("Cannot get the restartPolicy field: %v", err)
			}
			if restartPolicy == nil {
				t.Errorf("Job should contain the restartPolicy field!")
			}
		})

	}
}

func (t *FunctionRunnerSuite) TestCreateSetters() {
	testCases := t.loadTestCases("create-setters")

	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), "test-create-setters", "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	for tn, tc := range testCases {
		t.Run(tn, func() {
			pr := t.createTestPackage("test-create-setters")
			t.Cleanup(func() {
				t.doCleanup(pr, tc.image)
			})

			// Get package resources
			var resources = t.WaitUntilPackageRevisionResourcesExists(types.NamespacedName{Namespace: t.Namespace, Name: pr.Name})

			t.AddResourceToPackage(resources, "testdata/createsetters/setters.yaml", "setters.yaml")
			t.AddResourceToPackage(resources, "testdata/createsetters/resources.yaml", "resources.yaml")

			t.AddMutator(resources, tc.image, suiteutils.WithConfigPath("setters.yaml"))

			t.AddMutator(resources, t.TestSuiteWithGit.KrmFunctionsRegistry+"/apply-setters:v0.2.0", suiteutils.WithConfigmap(map[string]string{
				"nginx-replicas": "5",
			}))

			t.UpdateF(resources)
			t.failOnRenderError(resources)

			packageResources, ok := resources.Spec.Resources["resources.yaml"]
			if !ok {
				t.Errorf("'resources.yaml' not found among package resources")
			}

			node, err := yaml.Parse(packageResources)
			if err != nil {
				t.Errorf("yaml.Parse(\"resources.yaml\") failed: %v", err)
			}

			replicas, err := node.GetFieldValue("spec.replicas")
			if err != nil {
				t.Errorf("Cannot get replicas field: %v", err)
			}
			expectedReplicas := 5
			if replicas != expectedReplicas {
				t.Errorf("Deployment should contain replicas with value `%s`, the value we got: %s", expectedReplicas, replicas)
			}
		})

	}
}

// Utility functions

func (t *FunctionRunnerSuite) failOnRenderError(resources *porchapi.PackageRevisionResources) {
	if resources.Status.RenderStatus.Err != "" {
		t.Fatalf("failed to render package: %v", resources.Status.RenderStatus.Err)
	}
	for _, result := range resources.Status.RenderStatus.Result.Items {
		if result.ExitCode != 0 {
			t.Fatalf("failed to render package: non-zero exit code for %v", result.Image)
		}
		for _, resultItem := range result.Results {
			if resultItem.Severity == string(fn.Error) {
				t.Fatalf("failed to render package: error in %v: %v", result.Image, resultItem.Message)
			}
		}
	}
}

func (t *FunctionRunnerSuite) doCleanup(pr *porchapi.PackageRevision, mutatorImage string) {
	t.DeleteF(pr)
	t.WaitUntilObjectDeleted(
		porchapi.SchemeGroupVersion.WithKind("PackageRevision"),
		types.NamespacedName{
			Name:      pr.Name,
			Namespace: pr.Namespace,
		},
		10*time.Second,
	)

	podList := &corev1.PodList{}
	t.ListF(podList, client.InNamespace("porch-fn-system"))
	for _, pod := range podList.Items {
		img := pod.Spec.Containers[0].Image
		if img == mutatorImage {
			t.DeleteF(&pod)
			time.Sleep(1 * time.Second)
		}
	}
}

func createWorkspaceUUID(t *testing.T) string {
	t.Helper()
	workspace, err := uuid.NewRandom()
	if err != nil {
		t.Fatalf("failed to create workspace UUID: %v", err)
	}
	return workspace.String()
}

func (t *FunctionRunnerSuite) createTestPackage(repoName string) *porchapi.PackageRevision {
	workspace := createWorkspaceUUID(t.T())
	return t.CreatePackageCloneF(repoName, "test-fn-pod", workspace, "empty/v1", "empty")
}

func (t *FunctionRunnerSuite) loadTestCases(functionName string) map[string]functionImage {
	versionBA, err := os.ReadFile("testdata/function-versions.yaml")
	if err != nil {
		t.Fatalf("Failed to read test config file %q: %v", "testdata/function-versions.yaml", err)
	}

	var fnVersions functionVersions

	err = yaml.Unmarshal(versionBA, &fnVersions)
	if err != nil {
		t.Fatalf("Failed to unmarshal test config file %q: %v", "testdata/function-versions.yaml", err)
	}

	fnVersion, ok := fnVersions.Versions[functionName]
	if !ok {
		t.Fatalf("Version entries for function %q not found in test config file %q: %v", functionName, "testdata/function-versions.yaml")
	}

	var imageMap = make(map[string]functionImage)

	if fnVersion.Skip {
		t.T().Skipf("Skipping tests for function %q as configured in %q", functionName, "testdata/function-versions.yaml")
		return imageMap
	}

	imageMap[functionName+" absolute"] = functionImage{
		image: t.TestSuiteWithGit.KrmFunctionsRegistry + "/" + functionName,
	}
	imageMap[functionName+" relative"] = functionImage{
		image: functionName,
	}

	imageMap[functionName+":latest absolute"] = functionImage{
		image: t.TestSuiteWithGit.KrmFunctionsRegistry + "/" + functionName + ":latest",
	}
	imageMap[functionName+":latest relative"] = functionImage{
		image: functionName + ":latest",
	}

	imageMap[functionName+":"+fnVersion.Latest+" absolute"] = functionImage{
		image: t.TestSuiteWithGit.KrmFunctionsRegistry + "/" + functionName + ":" + fnVersion.Latest,
	}
	imageMap[functionName+":"+fnVersion.Latest+" relative"] = functionImage{
		image: functionName + ":" + fnVersion.Latest,
	}

	imageMap[functionName+":"+fnVersion.Minus1+" absolute"] = functionImage{
		image: t.TestSuiteWithGit.KrmFunctionsRegistry + "/" + functionName + ":" + fnVersion.Minus1,
	}
	imageMap[functionName+":"+fnVersion.Minus1+" relative"] = functionImage{
		image: functionName + ":" + fnVersion.Minus1,
	}

	if fnVersion.Other != "" {
		imageMap[functionName+":"+fnVersion.Other+" absolute"] = functionImage{
			image: t.TestSuiteWithGit.KrmFunctionsRegistry + "/" + functionName + ":" + fnVersion.Other,
		}
		imageMap[functionName+":"+fnVersion.Other+" relative"] = functionImage{
			image: functionName + ":" + fnVersion.Other,
		}
	}

	minorLatest := fnVersion.Latest[:strings.LastIndex(fnVersion.Latest, ".")]
	imageMap[functionName+":"+minorLatest+" absolute"] = functionImage{
		image: t.TestSuiteWithGit.KrmFunctionsRegistry + "/" + functionName + ":" + minorLatest,
	}
	imageMap[functionName+":"+minorLatest+" relative"] = functionImage{
		image: functionName + ":" + minorLatest,
	}

	majorLatest := fnVersion.Latest[:strings.LastIndex(minorLatest, ".")]
	imageMap[functionName+":"+majorLatest+" absolute"] = functionImage{
		image: t.TestSuiteWithGit.KrmFunctionsRegistry + "/" + functionName + ":" + majorLatest,
	}
	imageMap[functionName+":"+majorLatest+" relative"] = functionImage{
		image: functionName + ":" + majorLatest,
	}

	return imageMap
}
