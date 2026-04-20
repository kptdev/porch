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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	porchapi "github.com/nephio-project/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("PRR Edge Cases", Ordered, Label("content"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should delete a file by removing it from the resources map", func() {
		By("creating a draft and pushing two files")
		pr := newPackageRevision(env.Namespace, env.RepoName, "del-file", "v1", withInit("delete file test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"keep.yaml":   "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keep\n",
			"remove.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: remove\n",
		})
		waitForRendered(env.Ctx, pr)

		By("verifying both files exist")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources).To(HaveKey("keep.yaml"))
		Expect(resources).To(HaveKey("remove.yaml"))

		By("removing one file from the resources map")
		prr := &porchapi.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: pr.Name}, prr)).To(Succeed())
		delete(prr.Spec.Resources, "remove.yaml")
		Expect(k8sClient.Update(env.Ctx, prr)).To(Succeed())
		waitForRendered(env.Ctx, pr)

		By("verifying the file is gone")
		resources = getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources).To(HaveKey("keep.yaml"))
		Expect(resources).NotTo(HaveKey("remove.yaml"))
	})

	It("should set Rendered=False when Kptfile has wrong apiVersion", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "bad-kptfile", "v1", withInit("bad kptfile test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing a Kptfile with wrong apiVersion")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "apiVersion: wrong/v1\nkind: Kptfile\nmetadata:\n  name: bad-kptfile\n",
		})

		By("waiting for Rendered=False")
		waitForRenderFailed(env.Ctx, pr)

		By("verifying error message is useful")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		renderedCond := findCondition(pr.Status.Conditions, porchv1alpha2.ConditionRendered)
		Expect(renderedCond).NotTo(BeNil())
		Expect(renderedCond.Message).NotTo(BeEmpty())
	})

	It("should set Rendered=False when Kptfile contains invalid YAML", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "bad-yaml", "v1", withInit("bad yaml test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("pushing a Kptfile with unparseable YAML")
		updatePRRResources(env.Ctx, env.Namespace, pr.Name, map[string]string{
			"Kptfile": "this: is: not: valid: yaml: [[[",
		})

		By("waiting for Rendered=False")
		waitForRenderFailed(env.Ctx, pr)
	})
})
