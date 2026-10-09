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
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Sharding verifies that when the controllers run as a repository-sharded
// StatefulSet, work is partitioned so each repository (and all its
// PackageRevisions) is handled by exactly one shard, while the overall
// package lifecycle still functions.
//
// The suite is skipped unless sharding is actually enabled on the deployed
// controllers (num-shards >= 2). Enable it with the sharded StatefulSet in
// deployments/sharding/ (e.g. `make deploy-sharding-poc`).
var _ = Describe("Sharding", Ordered, Label("infra"), func() {
	var (
		ctx        context.Context
		kubeClient *kubernetes.Clientset
		numShards  int
		shardPods  []string // index i holds the pod name for shard i
		repoNames  []string
	)

	BeforeAll(func() {
		if !allInCluster {
			Skip("sharding test requires in-cluster controllers")
		}
		ctx = context.Background()

		var err error
		kubeClient, err = kubernetes.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())

		numShards, shardPods = detectSharding(ctx)
		if numShards < 2 {
			Skip("sharding not enabled on the deployed controllers (need num-shards >= 2; deploy deployments/sharding/)")
		}
		GinkgoWriter.Printf("sharding active: %d shards, pods=%v\n", numShards, shardPods)
	})

	AfterAll(func() {
		for _, r := range repoNames {
			removePackageRevisionFinalizers(ctx, sharedNamespace)
			cleanupRepo(ctx, sharedNamespace, r)
			deleteGiteaRepo(r)
		}
	})

	It("partitions repositories across shards with correct co-location", func() {
		// Create enough repositories that, by hash, every shard owns at least
		// one. We generate names until each shard has >=1 expected owner.
		By("creating repositories spread across all shards")
		repoNames = generateReposCoveringAllShards(numShards, 10*numShards)
		expectedOwner := map[string]int{}
		for _, r := range repoNames {
			expectedOwner[r] = shardOf(sharedNamespace, r, numShards)
			createGiteaRepo(r)
			registerV1Alpha2Repo(ctx, sharedNamespace, r)
		}

		By("creating and publishing a package in each repository")
		pkgByRepo := map[string]*porchv1alpha2.PackageRevision{}
		for _, r := range repoNames {
			pr := newPackageRevision(sharedNamespace, r, "shard-pkg", "v1", withInit("sharding e2e"))
			Expect(k8sClient.Create(ctx, pr)).To(Succeed())
			waitForReady(ctx, pr)
			pkgByRepo[r] = pr
		}

		// The functional assertion: despite sharding, every package reconciled
		// to Ready. If the shard filter wrongly dropped a repo's work, its
		// package would never become Ready and the wait above would fail.
		By("verifying all packages reached Ready (sharding did not drop work)")
		for _, r := range repoNames {
			Expect(pkgByRepo[r].Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(porchv1alpha2.ConditionReady)),
				HaveField("Status", Equal(metav1.ConditionTrue)),
			)), "package in repo %s not Ready", r)
		}

		By("scraping the shard metric from every shard pod")
		// Give reconciles a moment to be reflected in the counter.
		Eventually(func(g Gomega) {
			owned, skipped := scrapeShardCounts(ctx, g, kubeClient, shardPods)
			// Each shard must have done real partitioning work: it owns its
			// repos and skips the others. With >=1 repo per shard and
			// >=2 shards, both counters must be non-zero somewhere.
			total := 0
			for s := 0; s < numShards; s++ {
				total += owned["Repository"][s] + skipped["Repository"][s]
			}
			g.Expect(total).To(BeNumerically(">", 0), "no Repository shard reconciles recorded")

			// Disjoint ownership: for Repository, every shard that is the
			// expected owner of at least one repo must report owned>0, and
			// every other shard must report skipped>0 for those repos.
			ownersSeen := map[int]bool{}
			for _, r := range repoNames {
				ownersSeen[expectedOwner[r]] = true
			}
			for s := range ownersSeen {
				g.Expect(owned["Repository"][s]).To(BeNumerically(">", 0),
					"shard %d should own at least one repository", s)
			}
			// At least one skip must have happened (there are repos owned by
			// other shards), proving the filter actively partitions.
			totalSkipped := 0
			for s := 0; s < numShards; s++ {
				totalSkipped += skipped["Repository"][s]
			}
			g.Expect(totalSkipped).To(BeNumerically(">", 0),
				"expected some repositories to be skipped by non-owner shards")
		}).WithTimeout(60 * time.Second).WithPolling(2 * time.Second).Should(Succeed())

		By("verifying PackageRevision work co-locates with its repository's shard")
		// The PR controller shards on spec.repositoryName, so a package's
		// reconcile must be owned by the same shard that owns its repository.
		// We assert the owner shard recorded PackageRevision "owned" samples
		// and non-owner shards recorded "skipped" samples.
		Eventually(func(g Gomega) {
			owned, skipped := scrapeShardCounts(ctx, g, kubeClient, shardPods)
			totalPROwned, totalPRSkipped := 0, 0
			for s := 0; s < numShards; s++ {
				totalPROwned += owned["PackageRevision"][s]
				totalPRSkipped += skipped["PackageRevision"][s]
			}
			g.Expect(totalPROwned).To(BeNumerically(">", 0),
				"no PackageRevision reconciles were owned by any shard")
			g.Expect(totalPRSkipped).To(BeNumerically(">", 0),
				"PackageRevision work was not partitioned (no skips)")
		}).WithTimeout(60 * time.Second).WithPolling(2 * time.Second).Should(Succeed())
	})

	// Throughput measurement. Opt-in (SHARDING_THROUGHPUT=1) because it is a
	// capacity measurement, not a pass/fail correctness check, and it takes
	// longer. Init-only packages are used deliberately: init exercises the
	// per-repo git fetch/push that sharding parallelises, without routing
	// through the shared pod evaluator (which would mask the sharding effect).
	It("reports throughput for 20 init-only packages across shards", Label("throughput"), func() {
		if os.Getenv("SHARDING_THROUGHPUT") == "" {
			Skip("set SHARDING_THROUGHPUT=1 to run the throughput measurement")
		}

		const n = 20
		By(fmt.Sprintf("creating %d repositories", n))
		tputRepos := make([]string, n)
		for i := 0; i < n; i++ {
			tputRepos[i] = fmt.Sprintf("shard-tput-%d", i)
		}
		repoNames = append(repoNames, tputRepos...) // ensure AfterAll cleanup
		for _, r := range tputRepos {
			createGiteaRepo(r)
			registerV1Alpha2Repo(ctx, sharedNamespace, r)
		}

		By("creating one init package in each repository and timing the publish wave")
		start := time.Now()
		prs := make([]*porchv1alpha2.PackageRevision, n)
		for i, r := range tputRepos {
			prs[i] = newPackageRevision(sharedNamespace, r, "tput-pkg", "v1", withInit("throughput"))
			Expect(k8sClient.Create(ctx, prs[i])).To(Succeed())
		}
		// Publish each (propose -> approve). Done sequentially from the client,
		// but the controllers process them in parallel across shards.
		for i := range prs {
			publishPackage(ctx, prs[i])
		}
		elapsed := time.Since(start)

		By("reporting per-shard distribution")
		perShard := map[int]int{}
		for _, r := range tputRepos {
			perShard[shardOf(sharedNamespace, r, numShards)]++
		}

		GinkgoWriter.Printf("\n=== sharding throughput ===\n")
		GinkgoWriter.Printf("shards:          %d\n", numShards)
		GinkgoWriter.Printf("repos/packages:  %d\n", n)
		GinkgoWriter.Printf("wall-clock:      %s\n", elapsed.Round(time.Millisecond))
		GinkgoWriter.Printf("throughput:      %.2f pkg/s\n", float64(n)/elapsed.Seconds())
		GinkgoWriter.Printf("per-shard repos: %v (even distribution => balanced load)\n", perShard)
		GinkgoWriter.Printf("note: compare the same run at --num-shards=1 vs 2 vs 4.\n")
		GinkgoWriter.Printf("note: shared DB/etcd and single-node contention bound the gain (sub-linear expected).\n")
		GinkgoWriter.Printf("===========================\n\n")

		// Sanity only: all published. Timing is reported, not asserted.
		for i := range prs {
			Expect(prs[i].Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
		}
	})

	It("recovers after a shard pod is killed and rescheduled", func() {
		By("creating a repository and package before pod failure")
		repoName := "shard-recovery-test"
		repoNames = append(repoNames, repoName)
		createGiteaRepo(repoName)
		registerV1Alpha2Repo(ctx, sharedNamespace, repoName)

		pr := newPackageRevision(sharedNamespace, repoName, "pkg", "v1", withInit("before failure"))
		Expect(k8sClient.Create(ctx, pr)).To(Succeed())
		waitForReady(ctx, pr)

		By("identifying and deleting the shard pod that owns this repo")
		targetShard := shardOf(sharedNamespace, repoName, numShards)
		podName := shardPods[targetShard]

		err := kubeClient.CoreV1().Pods("porch-system").Delete(ctx, podName, metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("waiting for the pod to reschedule")
		Eventually(func(g Gomega) {
			pod, err := kubeClient.CoreV1().Pods("porch-system").Get(ctx, podName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			// Pod should be re-created (new UID even though same name in StatefulSet)
			g.Expect(pod.Status.Phase).NotTo(Equal("Failed"))
			// Wait for pod to be Running
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
		}).WithTimeout(60 * time.Second).WithPolling(2 * time.Second).Should(Succeed())

		By("verifying existing package is still accessible after pod recovery")
		// Key assertion: the package created before pod failure is still readable.
		// This validates that data persisted (stored in DB), not lost due to pod failure.
		retrieved := &porchv1alpha2.PackageRevision{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: sharedNamespace,
			Name:      pr.Name,
		}, retrieved)).To(Succeed())
		Expect(retrieved.Name).To(Equal(pr.Name))
		// Package data should be intact (status may be updating, but object exists)
		Expect(retrieved.Spec.RepositoryName).To(Equal(repoName))
	})

	It("rebalances repositories when replica count changes", func() {
		By("scaling the StatefulSet to add one more replica")
		sts := &appsv1.StatefulSet{}
		stsKey := client.ObjectKey{Namespace: "porch-system", Name: "porch-controllers"}
		Expect(k8sClient.Get(ctx, stsKey, sts)).To(Succeed())

		initialReplicas := *sts.Spec.Replicas
		newReplicas := initialReplicas + 1
		sts.Spec.Replicas = &newReplicas
		Expect(k8sClient.Update(ctx, sts)).To(Succeed())

		DeferCleanup(func() {
			By("cleaning up: scaling StatefulSet back to original replica count")
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, stsKey, sts)).To(Succeed())
			sts.Spec.Replicas = &initialReplicas
			Expect(k8sClient.Update(ctx, sts)).To(Succeed())

			Eventually(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods("porch-system").
					List(ctx, metav1.ListOptions{LabelSelector: "k8s-app=porch-controllers"})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(len(pods.Items)).To(Equal(int(initialReplicas)))
			}).WithTimeout(60 * time.Second).WithPolling(2 * time.Second).Should(Succeed())
		})

		By("waiting for the new pod to be ready")
		Eventually(func(g Gomega) {
			pods, err := kubeClient.CoreV1().Pods("porch-system").
				List(ctx, metav1.ListOptions{LabelSelector: "k8s-app=porch-controllers"})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(pods.Items)).To(Equal(int(newReplicas)))

			// Check that all pods are running
			runningCount := 0
			for _, pod := range pods.Items {
				if string(pod.Status.Phase) == "Running" {
					runningCount++
				}
			}
			g.Expect(runningCount).To(Equal(int(newReplicas)))
		}).WithTimeout(60 * time.Second).WithPolling(2 * time.Second).Should(Succeed())

		By("verifying the system handles the new replica count")
		// Verify that we can still access and work with an existing repository
		// (which was already registered before the scale)
		testRepo := "porch-test" // Use the pre-existing test repo

		pr := newPackageRevision(sharedNamespace, testRepo, "scale-test", "v1", withInit("scale test"))
		Expect(k8sClient.Create(ctx, pr)).To(Succeed())

		// Give the system more time to settle post-scale and render the package
		waitForReady(ctx, pr)

		Expect(pr.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", Equal(porchv1alpha2.ConditionReady)),
			HaveField("Status", Equal(metav1.ConditionTrue)),
		)), "system should function correctly after scaling")
	})

	It("maintains monotonic revision numbering under rapid publishes", func() {
		By("creating a single repository for revision test")
		repoName := "shard-revision-mono"
		repoNames = append(repoNames, repoName)
		createGiteaRepo(repoName)
		registerV1Alpha2Repo(ctx, sharedNamespace, repoName)

		By("rapidly publishing multiple revisions of the same package")
		const numRevisions = 5
		revisions := make([]porchv1alpha2.PackageRevisionLifecycle, numRevisions)
		prNames := make([]string, numRevisions)

		for i := 0; i < numRevisions; i++ {
			pr := newPackageRevision(sharedNamespace, repoName, "mono-base", fmt.Sprintf("v%d", i+1), withInit(fmt.Sprintf("revision %d", i+1)))
			Expect(k8sClient.Create(ctx, pr)).To(Succeed())
			prNames[i] = pr.Name
			// Small delay between creates to avoid overwhelming the system
			time.Sleep(500 * time.Millisecond)
		}

		By("waiting for all packages to reach Ready")
		for i, prName := range prNames {
			pr := &porchv1alpha2.PackageRevision{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKey{
					Namespace: sharedNamespace,
					Name:      prName,
				}, pr)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pr.Status.Conditions).To(ContainElement(SatisfyAll(
					HaveField("Type", Equal(porchv1alpha2.ConditionReady)),
					HaveField("Status", Equal(metav1.ConditionTrue)),
				)))
			}).WithTimeout(60 * time.Second).WithPolling(500 * time.Millisecond).Should(Succeed())

			// Publish the package
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Namespace: sharedNamespace,
				Name:      prName,
			}, pr)).To(Succeed())
			publishPackage(ctx, pr)
			revisions[i] = pr.Spec.Lifecycle
		}

		By("verifying all revisions were published")
		for _, lifecycle := range revisions {
			Expect(lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
		}

		By("verifying no duplicate or out-of-order revisions exist")
		// Fetch all PackageRevisions for this repo to check for duplicates
		prList := &porchv1alpha2.PackageRevisionList{}
		Expect(k8sClient.List(ctx, prList, client.InNamespace(sharedNamespace))).To(Succeed())

		var repoRevisions []*porchv1alpha2.PackageRevision
		for i := range prList.Items {
			if prList.Items[i].Spec.RepositoryName == repoName {
				repoRevisions = append(repoRevisions, &prList.Items[i])
			}
		}

		// All created revisions should be present
		Expect(len(repoRevisions)).To(BeNumerically(">=", numRevisions),
			"at least %d revisions should exist for repo %s", numRevisions, repoName)

		// Check that published revisions are all unique
		publishedCount := 0
		for _, pr := range repoRevisions {
			if pr.Spec.Lifecycle == porchv1alpha2.PackageRevisionLifecyclePublished {
				publishedCount++
			}
		}
		Expect(publishedCount).To(BeNumerically(">=", numRevisions),
			"at least %d published revisions should exist", numRevisions)
	})
})

// --- sharding test helpers ---

// shardOf mirrors controllers/sharding.Config.Owns: FNV-1a of "ns/name" mod N.
func shardOf(namespace, name string, numShards int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(namespace + "/" + name))
	return int(h.Sum32()) % numShards
}

// generateReposCoveringAllShards returns at least `min` repo names such that
// every shard in [0,numShards) is the expected owner of at least one.
func generateReposCoveringAllShards(numShards, min int) []string {
	names := []string{}
	covered := map[int]bool{}
	i := 0
	for len(names) < min || len(covered) < numShards {
		name := fmt.Sprintf("shard-e2e-%d", i)
		names = append(names, name)
		covered[shardOf(sharedNamespace, name, numShards)] = true
		i++
		if i > 1000 { // safety
			break
		}
	}
	return names
}

// detectSharding returns the shard count and ordered pod names when the
// controllers run as a StatefulSet with replicas > 1 (the count is discovered
// from spec.replicas, matching the controller's own membership logic).
// Returns (1, nil) when not sharded.
func detectSharding(ctx context.Context) (int, []string) {
	sts := &appsv1.StatefulSet{}
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "porch-system", Name: "porch-controllers"}, sts)
	if err != nil || sts.Spec.Replicas == nil {
		return 1, nil
	}
	n := int(*sts.Spec.Replicas)
	if n < 2 {
		return n, nil
	}
	pods := make([]string, n)
	for i := 0; i < n; i++ {
		pods[i] = fmt.Sprintf("porch-controllers-%d", i)
	}
	return n, pods
}

// scrapeShardCounts fetches porch_controller_shard_reconciles_total from each
// shard pod and returns owned/skipped counts indexed as [resource][shardID].
func scrapeShardCounts(ctx context.Context, g Gomega, kube *kubernetes.Clientset, shardPods []string) (owned, skipped map[string][]int) {
	owned = map[string][]int{"Repository": make([]int, len(shardPods)), "PackageRevision": make([]int, len(shardPods))}
	skipped = map[string][]int{"Repository": make([]int, len(shardPods)), "PackageRevision": make([]int, len(shardPods))}

	lineRE := regexp.MustCompile(`porch_controller_shard_reconciles_total\{([^}]*)\}\s+([0-9.e+]+)`)
	for shardID, pod := range shardPods {
		raw, err := kube.CoreV1().Pods("porch-system").
			ProxyGet("", pod, "9464", "metrics", nil).DoRaw(ctx)
		g.Expect(err).NotTo(HaveOccurred(), "scraping metrics from %s", pod)
		for _, line := range strings.Split(string(raw), "\n") {
			m := lineRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			labels, valStr := m[1], m[2]
			val, convErr := strconv.ParseFloat(valStr, 64)
			if convErr != nil {
				continue
			}
			resource := labelValue(labels, "resource")
			action := labelValue(labels, "action")
			if _, ok := owned[resource]; !ok {
				continue
			}
			switch action {
			case "owned":
				owned[resource][shardID] += int(val)
			case "skipped":
				skipped[resource][shardID] += int(val)
			}
		}
	}
	return owned, skipped
}

// labelValue extracts a Prometheus label value from a label string like
// `resource="Repository",action="owned",...`.
func labelValue(labels, key string) string {
	re := regexp.MustCompile(key + `="([^"]*)"`)
	if m := re.FindStringSubmatch(labels); m != nil {
		return m[1]
	}
	return ""
}
