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
)

var _ = Describe("Init", Ordered, Label("lifecycle"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	It("should init an empty package", func() {
		By("creating a package with source.init")
		pr := newPackageRevision(env.Namespace, env.RepoName, "empty-pkg", "v1", withInit("empty package"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("verifying creationSource is init")
		Expect(pr.Status.CreationSource).To(Equal("init"))

		By("verifying Kptfile exists in package content")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		Expect(resources).To(HaveKey("Kptfile"))
	})

	It("should init a package with full metadata", func() {
		By("creating a package with description, keywords, and site")
		pr := newPackageRevision(env.Namespace, env.RepoName, "full-pkg", "v1",
			withInitFull("full description", []string{"test", "e2e"}, "https://example.com"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("verifying creationSource is init")
		Expect(pr.Status.CreationSource).To(Equal("init"))

		By("verifying Kptfile contains the metadata")
		resources := getPRRResources(env.Ctx, env.Namespace, pr.Name)
		kptfile := resources["Kptfile"]
		Expect(kptfile).To(ContainSubstring("full description"))
		Expect(kptfile).To(ContainSubstring("https://example.com"))
	})
})
