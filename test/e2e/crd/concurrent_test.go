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
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	porchapi "github.com/nephio-project/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// runInParallel executes fns concurrently and returns all results.
func runInParallel(fns ...func() error) []error {
	results := make([]error, len(fns))
	var wg sync.WaitGroup
	wg.Add(len(fns))
	for i, fn := range fns {
		go func(idx int, f func() error) {
			defer wg.Done()
			defer GinkgoRecover()
			results[idx] = f()
		}(i, fn)
	}
	wg.Wait()
	return results
}

var _ = Describe("Concurrency", Ordered, Label("concurrency"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	// These tests verify concurrency behavior: etcd optimistic concurrency
	// for lifecycle patches and per-package mutex for resource pushes.

	It("should handle concurrent lifecycle patches with etcd optimistic concurrency", func() {
		By("creating and readying a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "conc-lc", "v1", withInit("concurrent lifecycle"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("reading the package to get a consistent resourceVersion")
		pr1 := &porchv1alpha2.PackageRevision{}
		pr2 := &porchv1alpha2.PackageRevision{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr1)).To(Succeed())
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr2)).To(Succeed())

		By("concurrently patching lifecycle to Proposed from both copies")
		pr1.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleProposed
		pr2.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycleProposed

		results := runInParallel(
			func() error { return k8sClient.Update(env.Ctx, pr1) },
			func() error { return k8sClient.Update(env.Ctx, pr2) },
		)

		By("verifying one succeeded and one got a conflict")
		var successes, conflicts int
		for _, err := range results {
			if err == nil {
				successes++
			} else if apierrors.IsConflict(err) {
				conflicts++
			}
		}
		Expect(successes).To(Equal(1), "exactly one update should succeed")
		Expect(conflicts).To(Equal(1), "exactly one update should get a conflict")

		By("verifying the package is in Proposed state")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleProposed))
	})

	It("should handle concurrent PRR pushes to the same draft", func() {
		By("creating a draft package")
		pr := newPackageRevision(env.Namespace, env.RepoName, "conc-prr", "v1", withInit("concurrent PRR"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("reading PRR from two clients")
		prr1 := &porchapi.PackageRevisionResources{}
		prr2 := &porchapi.PackageRevisionResources{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: pr.Name}, prr1)).To(Succeed())
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: pr.Name}, prr2)).To(Succeed())

		By("concurrently pushing different content")
		prr1.Spec.Resources["file1.yaml"] = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: from-client-1\n"
		prr2.Spec.Resources["file2.yaml"] = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: from-client-2\n"

		results := runInParallel(
			func() error { return k8sClient.Update(env.Ctx, prr1) },
			func() error { return k8sClient.Update(env.Ctx, prr2) },
		)

		By("verifying one succeeded and one got a conflict")
		var successes, conflicts int
		for _, err := range results {
			if err == nil {
				successes++
			} else if apierrors.IsConflict(err) {
				conflicts++
			}
		}
		Expect(successes).To(Equal(1), "exactly one PRR push should succeed")
		Expect(conflicts).To(Equal(1), "exactly one PRR push should get a conflict")
	})
})
