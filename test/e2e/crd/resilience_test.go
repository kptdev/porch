// Copyright 2026 The Nephio Authors
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

package crd

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configapi "github.com/nephio-project/porch/api/porchconfig/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Resilience", Ordered, Label("infra"), func() {
	// Tests that the system recovers after a controller restart.
	// The restart nukes the in-memory git cache (globalDirectoryPool).
	// The controller's cold-start detection forces a full sync on the
	// first reconcile per repo, re-warming the cache.
	//
	// This is a post-restart smoke test: restart the controller, then
	// run a full create→push→render→publish cycle on a new package.

	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should function correctly after controller restart", func() {
		if !allInCluster {
			Skip("controller restart test requires in-cluster controllers")
		}

		By("restarting the controller pod")
		restartTime := time.Now()
		restartDeployment(env, "porch-controllers")

		By("waiting for repos to re-sync after restart (cold start)")
		// The cold start forces a full sync, but it's async. Wait for
		// LastFullSyncTime to be after the restart to confirm the new
		// controller pod actually completed the sync.
		for _, repoName := range []string{env.RepoName, testBlueprintsRepo} {
			Eventually(func(g Gomega) {
				repo := &configapi.Repository{}
				g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: repoName}, repo)).To(Succeed())
				g.Expect(repo.Status.LastFullSyncTime).NotTo(BeNil())
				g.Expect(repo.Status.LastFullSyncTime.Time.After(restartTime)).To(BeTrue(),
					"repo %s LastFullSyncTime should be after restart", repoName)
			}).WithTimeout(defaultTimeout).WithPolling(time.Second).Should(Succeed())
		}

		By("creating a new package on the restarted controller")
		pr := newPackageRevision(env.Namespace, env.RepoName, "post-restart", "v1", withInit("post-restart test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing content with a render pipeline")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: kpt.dev/v1\nkind: Kptfile\nmetadata:\n  name: post-restart\npipeline:\n  mutators:\n  - image: ghcr.io/kptdev/krm-functions-catalog/set-namespace:v0.4.1\n    configMap:\n      namespace: post-restart-ns\n",
			"cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: post-restart-cm\ndata:\n  key: value\n",
		})

		By("waiting for async render to complete")
		waitForRendered(env.Ctx, pr)

		By("verifying rendered output is correct")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources["cm.yaml"]).To(ContainSubstring("namespace: post-restart-ns"))

		By("publishing the package")
		publishPackage(env.Ctx, pr)

		By("verifying publish metadata")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		Expect(pr.Status.Revision).NotTo(Equal(0))
		Expect(pr.Status.PublishedBy).NotTo(BeEmpty())
	})
})

var _ = Describe("Webhook Validation", Ordered, Label("infra"), func() {
	// Placeholder specs for Issue 9 — webhook validation not yet implemented.

	PIt("should reject propose when render is in progress (renderingPrrResourceVersion set)")
	PIt("should reject propose when content is not yet rendered (observedPrrResourceVersion != annotation)")
	PIt("should reject approve when content is not yet rendered")
	PIt("should reject lifecycle transition from Draft directly to Published (must go through Proposed)")
	PIt("should reject modification of immutable fields (repository, packageName) after creation")
})

// restartDeployment triggers a rollout restart and waits for all replicas to be ready.
func restartDeployment(env *testEnv, name string) {
	deploy := &appsv1.Deployment{}
	Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: "porch-system", Name: name}, deploy)).To(Succeed())

	patch := client.MergeFrom(deploy.DeepCopy())
	if deploy.Spec.Template.Annotations == nil {
		deploy.Spec.Template.Annotations = map[string]string{}
	}
	deploy.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)
	Expect(k8sClient.Patch(env.Ctx, deploy, patch)).To(Succeed())

	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: "porch-system", Name: name}, deploy)).To(Succeed())
		g.Expect(deploy.Status.ReadyReplicas).To(Equal(*deploy.Spec.Replicas),
			fmt.Sprintf("deployment %s should have all replicas ready after restart", name))
		g.Expect(deploy.Status.UpdatedReplicas).To(Equal(*deploy.Spec.Replicas))
	}).WithTimeout(120 * time.Second).WithPolling(time.Second).Should(Succeed())
}
