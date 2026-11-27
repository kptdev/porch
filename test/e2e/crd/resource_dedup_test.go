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

package crd

import (
	"os"
	"time"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// sweepWaitEnv tells the test how long to wait for the Porch blob sweep to have run, as a Go duration. Set it
// together with short sweep timings on the Porch deployments, for example:
//
//	kubectl -n porch-system set env deploy/porch-server deploy/porch-controllers \
//	    PORCH_DB_BLOB_SWEEP_INTERVAL=5s PORCH_DB_BLOB_SWEEP_GRACE=10s
//	E2E_BLOB_SWEEP_WAIT=30s make test-e2e-crd
//
// Without it, the test still checks content right after the delete but not after a sweep.
const sweepWaitEnv = "E2E_BLOB_SWEEP_WAIT"

// File content is stored once per distinct content and shared by every package revision that has it. These specs
// check that removing one package revision never takes content away from another that shares it.
var _ = Describe("Resource Content Deduplication", Ordered, Label("resources"), func() {
	var (
		env      *testEnv
		upstream string
		survivor *porchv1alpha2.PackageRevision
		kept     map[string]string
	)

	BeforeAll(func() {
		env = sharedEnv()
		upstream = crdName(testBlueprintsRepo, "basens", "v1")
	})

	// The survivor is read again by the second spec, so it is only removed once every spec has run.
	AfterAll(func() {
		if survivor != nil {
			deletePackage(env.Ctx, survivor)
		}
	})

	It("should keep shared content when a package revision sharing it is deleted", func() {
		By("cloning the same upstream twice, so both clones share the upstream's file content")
		removed := newPackageRevision(env.Namespace, env.RepoName, "dedup-removed", "v1", withCloneFromRef(upstream))
		Expect(k8sClient.Create(env.Ctx, removed)).To(Succeed())
		waitForReady(env.Ctx, removed)

		survivor = newPackageRevision(env.Namespace, env.RepoName, "dedup-survivor", "v1", withCloneFromRef(upstream))
		Expect(k8sClient.Create(env.Ctx, survivor)).To(Succeed())
		waitForReady(env.Ctx, survivor)

		kept = getPRRResources(env.Ctx, env.Namespace, survivor.Name)
		Expect(kept).To(HaveKey("Kptfile"))

		By("verifying the clones carry the upstream's files unchanged (the Kptfile differs per clone)")
		upstreamFiles := getPRRResources(env.Ctx, env.Namespace, upstream)
		expectSameFilesExceptKptfile(kept, upstreamFiles)

		By("deleting one clone")
		deletePackage(env.Ctx, removed)

		By("verifying the other clone still has all of its content")
		Expect(getPRRResources(env.Ctx, env.Namespace, survivor.Name)).To(Equal(kept))
	})

	It("should keep shared content after the unreferenced content sweep runs", func() {
		raw := os.Getenv(sweepWaitEnv)
		if raw == "" {
			Skip(sweepWaitEnv + " is not set; set it with short sweep timings on the Porch deployments to run this spec")
		}
		wait, err := time.ParseDuration(raw)
		Expect(err).NotTo(HaveOccurred(), "%s must be a Go duration", sweepWaitEnv)

		By("waiting for the sweep to have run at least once after the delete")
		time.Sleep(wait)

		By("verifying the surviving clone's content is still complete and unchanged")
		Expect(survivor).NotTo(BeNil(), "the previous spec creates the surviving clone")
		Expect(getPRRResources(env.Ctx, env.Namespace, survivor.Name)).To(Equal(kept))
	})
})

func expectSameFilesExceptKptfile(got, want map[string]string) {
	GinkgoHelper()
	for name, content := range want {
		if name == "Kptfile" {
			continue
		}
		Expect(got).To(HaveKeyWithValue(name, content), "file %s", name)
	}
}
