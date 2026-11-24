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
	"maps"
	"strings"
	"time"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

var _ = Describe("Migration with Drafts", Ordered, Label("migration", "drafts"), func() {
	// Tests draft package migration during v1alpha2 adoption.
	// Drafts are DB-only in v1alpha1 and must be discovered separately from git packages.

	const (
		forkName = "mig-drafts"
	)

	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()

		By("cleaning up any leftover resources from prior test runs")
		cleanupMigrationResources(env.Ctx, env.Namespace, forkName)

		By("forking test-blueprints for draft tests")
		deleteGiteaRepo(forkName)
		forkGiteaRepo(testBlueprintsRepo, forkName)
	})

	AfterAll(func() {
		cleanupMigrationResources(env.Ctx, env.Namespace, forkName)
		deleteGiteaRepo(forkName)
	})

	It("should create drafts in v1alpha1 registry", func() {
		By("registering repo WITHOUT v1alpha2 annotation")
		registerV1Alpha1Repo(env.Ctx, env.Namespace, forkName)

		By("creating a draft package via v1alpha1 API")
		draft := &porchv1alpha1.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      forkName + ".draft-test.drafts",
				Namespace: env.Namespace,
			},
			Spec: porchv1alpha1.PackageRevisionSpec{
				PackageName:    "draft-test",
				RepositoryName: forkName,
				WorkspaceName:  "drafts",
				Lifecycle:      porchv1alpha1.PackageRevisionLifecycleDraft,
			},
		}
		Expect(k8sClient.Create(env.Ctx, draft)).To(Succeed())

		By("verifying draft exists in v1alpha1 API")
		Eventually(func(g Gomega) {
			fetched := &porchv1alpha1.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: forkName + ".draft-test.drafts"}, fetched)).To(Succeed())
			g.Expect(fetched.Spec.Lifecycle).To(Equal(porchv1alpha1.PackageRevisionLifecycleDraft))
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("creating a proposed package (draft transitioning to proposed)")
		proposed := &porchv1alpha1.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      forkName + ".proposed-test.review",
				Namespace: env.Namespace,
			},
			Spec: porchv1alpha1.PackageRevisionSpec{
				PackageName:    "proposed-test",
				RepositoryName: forkName,
				WorkspaceName:  "review",
				Lifecycle:      porchv1alpha1.PackageRevisionLifecycleDraft,
			},
		}
		Expect(k8sClient.Create(env.Ctx, proposed)).To(Succeed())

		By("transitioning proposed package to Proposed lifecycle")
		Eventually(func(g Gomega) {
			fetched := &porchv1alpha1.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: forkName + ".proposed-test.review"}, fetched)).To(Succeed())
			fetched.Spec.Lifecycle = porchv1alpha1.PackageRevisionLifecycleProposed
			g.Expect(k8sClient.Update(env.Ctx, fetched)).To(Succeed())
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("verifying proposed package exists in v1alpha1 API")
		Eventually(func(g Gomega) {
			fetched := &porchv1alpha1.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: forkName + ".proposed-test.review"}, fetched)).To(Succeed())
			g.Expect(fetched.Spec.Lifecycle).To(Equal(porchv1alpha1.PackageRevisionLifecycleProposed))
		}).WithTimeout(defaultTimeout).Should(Succeed())
	})

	It("should migrate draft packages to v1alpha2 CRDs", func() {
		By("enabling v1alpha2 migration annotation")
		Eventually(func(g Gomega) {
			repo := &configapi.Repository{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: forkName}, repo)).To(Succeed())
			if repo.Annotations == nil {
				repo.Annotations = map[string]string{}
			}
			repo.Annotations["porch.kpt.dev/v1alpha2-migration"] = "true"
			now := metav1.Now()
			if repo.Spec.Sync == nil {
				repo.Spec.Sync = &configapi.RepositorySync{}
			}
			repo.Spec.Sync.RunOnceAt = &now
			g.Expect(k8sClient.Update(env.Ctx, repo)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("waiting for draft to be migrated to v1alpha2")
		Eventually(func(g Gomega) {
			var v2List porchv1alpha2.PackageRevisionList
			g.Expect(k8sClient.List(env.Ctx, &v2List, client.InNamespace(env.Namespace))).To(Succeed())
			found := false
			for _, pr := range v2List.Items {
				if pr.Spec.RepositoryName == forkName && pr.Spec.PackageName == "draft-test" && pr.Spec.WorkspaceName == "drafts" {
					g.Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleDraft),
						"draft lifecycle should be preserved")
					found = true
					break
				}
			}
			g.Expect(found).To(BeTrue(), "draft should appear as v1alpha2 CRD after migration")
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("verifying draft no longer appears in v1alpha1 API")
		Eventually(func(g Gomega) {
			var v1List porchv1alpha1.PackageRevisionList
			g.Expect(k8sClient.List(env.Ctx, &v1List, client.InNamespace(env.Namespace))).To(Succeed())
			for _, pr := range v1List.Items {
				if pr.Spec.RepositoryName == forkName {
					g.Expect(pr.Spec.PackageName).NotTo(Equal("draft-test"),
						"draft should not appear in v1alpha1 after migration")
				}
			}
		}).WithTimeout(defaultTimeout).Should(Succeed())
	})

	It("should migrate proposed packages to v1alpha2 CRDs", func() {
		By("verifying proposed package was migrated along with draft (same sync)")
		Eventually(func(g Gomega) {
			var v2List porchv1alpha2.PackageRevisionList
			g.Expect(k8sClient.List(env.Ctx, &v2List, client.InNamespace(env.Namespace))).To(Succeed())
			found := false
			for _, pr := range v2List.Items {
				if pr.Spec.RepositoryName == forkName && pr.Spec.PackageName == "proposed-test" && pr.Spec.WorkspaceName == "review" {
					g.Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecycleProposed),
						"proposed lifecycle should be preserved after migration")
					found = true
					break
				}
			}
			g.Expect(found).To(BeTrue(), "proposed package should appear as v1alpha2 CRD after migration")
		}).WithTimeout(defaultTimeout).Should(Succeed())
	})
})

// --- Zero-data-loss helpers ---

// kptfileLabelPrefix is the prefix used when promoting Kptfile labels to
// object.metadata.labels on v1alpha2 CRDs ("/" in keys is escaped to "__").
const kptfileLabelPrefix = "porch.kpt.dev/kptfile-label__"

// v1Snapshot captures everything a v1alpha1 Draft/Proposed revision carries
// before migration, so it can be compared field-by-field afterwards.
type v1Snapshot struct {
	pr        *porchv1alpha1.PackageRevision
	resources map[string]string
}

// findV1 returns the v1alpha1 PackageRevision matching repo/pkg/ws, or nil.
func findV1(g Gomega, ctx context.Context, namespace, repo, pkg, ws string) *porchv1alpha1.PackageRevision {
	var list porchv1alpha1.PackageRevisionList
	g.Expect(k8sClient.List(ctx, &list, client.InNamespace(namespace))).To(Succeed())
	for i := range list.Items {
		s := list.Items[i].Spec
		if s.RepositoryName == repo && s.PackageName == pkg && s.WorkspaceName == ws {
			return &list.Items[i]
		}
	}
	return nil
}

// findV2 returns the v1alpha2 PackageRevision matching repo/pkg/ws, or nil.
func findV2(g Gomega, ctx context.Context, namespace, repo, pkg, ws string) *porchv1alpha2.PackageRevision {
	var list porchv1alpha2.PackageRevisionList
	g.Expect(k8sClient.List(ctx, &list, client.InNamespace(namespace))).To(Succeed())
	for i := range list.Items {
		s := list.Items[i].Spec
		if s.RepositoryName == repo && s.PackageName == pkg && s.WorkspaceName == ws {
			return &list.Items[i]
		}
	}
	return nil
}

// takeV1Snapshot records the v1alpha1 PackageRevision and its full PRR content.
func takeV1Snapshot(ctx context.Context, namespace, repo, pkg, ws string) v1Snapshot {
	var snap v1Snapshot
	Eventually(func(g Gomega) {
		pr := findV1(g, ctx, namespace, repo, pkg, ws)
		g.Expect(pr).NotTo(BeNil(), "v1alpha1 %s/%s not found", pkg, ws)
		prr := &porchv1alpha1.PackageRevisionResources{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: pr.Name}, prr)).To(Succeed())
		g.Expect(prr.Spec.Resources).To(HaveKey("Kptfile"))
		snap = v1Snapshot{pr: pr.DeepCopy(), resources: maps.Clone(prr.Spec.Resources)}
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	return snap
}

// userLabels drops controller-managed labels so only user data is compared.
func userLabels(labels map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range labels {
		switch {
		case k == porchv1alpha1.LatestPackageRevisionKey,
			k == porchv1alpha2.LatestPackageRevisionKey,
			k == porchv1alpha2.RepositoryLabelKey,
			strings.HasPrefix(k, kptfileLabelPrefix):
			continue
		}
		out[k] = v
	}
	return out
}

// orEmpty normalises nil maps so nil and empty compare equal.
func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func v1MetaLabels(pr *porchv1alpha1.PackageRevision) map[string]string {
	if pr.Spec.PackageMetadata == nil {
		return map[string]string{}
	}
	return orEmpty(pr.Spec.PackageMetadata.Labels)
}

func v1MetaAnnotations(pr *porchv1alpha1.PackageRevision) map[string]string {
	if pr.Spec.PackageMetadata == nil {
		return map[string]string{}
	}
	return orEmpty(pr.Spec.PackageMetadata.Annotations)
}

func parseKptfile(g Gomega, content string) kptfilev1.KptFile {
	var kf kptfilev1.KptFile
	g.Expect(yaml.Unmarshal([]byte(content), &kf)).To(Succeed())
	return kf
}

// expectResourcesPreserved checks every file in the snapshot is present with
// identical content. The Kptfile is compared semantically because rendering
// may reformat it: labels/annotations against spec.packageMetadata (which may
// have been edited since the snapshot), and info, upstream, upstreamLock,
// pipeline and status.conditions against the snapshot Kptfile.
func expectResourcesPreserved(g Gomega, snap v1Snapshot, got map[string]string) {
	for path, want := range snap.resources {
		g.Expect(got).To(HaveKey(path), "file %q lost", path)
		if path == "Kptfile" {
			continue
		}
		g.Expect(got[path]).To(Equal(want), "file %q content changed", path)
	}

	wantKf := parseKptfile(g, snap.resources["Kptfile"])
	gotKf := parseKptfile(g, got["Kptfile"])
	g.Expect(orEmpty(gotKf.Labels)).To(Equal(v1MetaLabels(snap.pr)), "Kptfile labels")
	g.Expect(orEmpty(gotKf.Annotations)).To(Equal(v1MetaAnnotations(snap.pr)), "Kptfile annotations")
	g.Expect(gotKf.Info).To(Equal(wantKf.Info), "Kptfile info (incl. readinessGates)")
	g.Expect(gotKf.Upstream).To(Equal(wantKf.Upstream), "Kptfile upstream")
	g.Expect(gotKf.UpstreamLock).To(Equal(wantKf.UpstreamLock), "Kptfile upstreamLock")
	g.Expect(gotKf.Pipeline).To(Equal(wantKf.Pipeline), "Kptfile pipeline")
	if wantKf.Status != nil {
		g.Expect(gotKf.Status).NotTo(BeNil(), "Kptfile status lost")
		for _, c := range wantKf.Status.Conditions {
			g.Expect(gotKf.Status.Conditions).To(ContainElement(c), "Kptfile status condition %q lost", c.Type)
		}
	}
}

// expectV2MatchesSnapshot checks every v1alpha1 data mechanism landed on the v1alpha2 CRD.
func expectV2MatchesSnapshot(g Gomega, snap v1Snapshot, v2 *porchv1alpha2.PackageRevision, deployment bool) {
	v1 := snap.pr

	// identity and lifecycle
	g.Expect(v2.Spec.RepositoryName).To(Equal(v1.Spec.RepositoryName))
	g.Expect(v2.Spec.PackageName).To(Equal(v1.Spec.PackageName))
	g.Expect(v2.Spec.WorkspaceName).To(Equal(v1.Spec.WorkspaceName))
	g.Expect(string(v2.Spec.Lifecycle)).To(Equal(string(v1.Spec.Lifecycle)))
	g.Expect(v2.Status.Revision).To(Equal(v1.Spec.Revision), "unpublished revisions keep revision 0")
	g.Expect(v2.Spec.Source).To(BeNil(), "migrated CRDs are repo-controller managed")

	// object labels and annotations
	for k, v := range userLabels(v1.Labels) {
		g.Expect(v2.Labels).To(HaveKeyWithValue(k, v), "object label %q", k)
	}
	for k, v := range v1.Annotations {
		g.Expect(v2.Annotations).To(HaveKeyWithValue(k, v), "object annotation %q", k)
	}
	g.Expect(v2.Labels).To(HaveKeyWithValue(porchv1alpha2.RepositoryLabelKey, v1.Spec.RepositoryName))

	// spec.packageMetadata and promoted labels
	wantLabels, wantAnnos := v1MetaLabels(v1), v1MetaAnnotations(v1)
	if len(wantLabels) == 0 && len(wantAnnos) == 0 {
		if v2.Spec.PackageMetadata != nil {
			g.Expect(v2.Spec.PackageMetadata.Labels).To(BeEmpty())
			g.Expect(v2.Spec.PackageMetadata.Annotations).To(BeEmpty())
		}
	} else {
		g.Expect(v2.Spec.PackageMetadata).NotTo(BeNil(), "spec.packageMetadata not seeded")
		g.Expect(orEmpty(v2.Spec.PackageMetadata.Labels)).To(Equal(wantLabels))
		g.Expect(orEmpty(v2.Spec.PackageMetadata.Annotations)).To(Equal(wantAnnos))
	}
	for k, v := range wantLabels {
		g.Expect(v2.Labels).To(HaveKeyWithValue(kptfileLabelPrefix+strings.ReplaceAll(k, "/", "__"), v),
			"promoted Kptfile label %q", k)
	}

	// spec.readinessGates
	var wantGates, gotGates []string
	for _, rg := range v1.Spec.ReadinessGates {
		wantGates = append(wantGates, rg.ConditionType)
	}
	for _, rg := range v2.Spec.ReadinessGates {
		gotGates = append(gotGates, rg.ConditionType)
	}
	g.Expect(gotGates).To(ConsistOf(wantGates))

	// status.packageConditions
	for _, c := range v1.Status.Conditions {
		g.Expect(v2.Status.PackageConditions).To(ContainElement(SatisfyAll(
			HaveField("Type", Equal(c.Type)),
			HaveField("Status", BeEquivalentTo(string(c.Status))),
			HaveField("Reason", Equal(c.Reason)),
			HaveField("Message", Equal(c.Message)),
		)), "package condition %q", c.Type)
	}

	// status.upstreamLock
	if v1.Status.UpstreamLock != nil && v1.Status.UpstreamLock.Git != nil {
		g.Expect(v2.Status.UpstreamLock).NotTo(BeNil(), "upstreamLock lost")
		g.Expect(v2.Status.UpstreamLock.Git).NotTo(BeNil())
		g.Expect(v2.Status.UpstreamLock.Git.Repo).To(Equal(v1.Status.UpstreamLock.Git.Repo))
		g.Expect(v2.Status.UpstreamLock.Git.Directory).To(Equal(v1.Status.UpstreamLock.Git.Directory))
		g.Expect(v2.Status.UpstreamLock.Git.Ref).To(Equal(v1.Status.UpstreamLock.Git.Ref))
		g.Expect(v2.Status.UpstreamLock.Git.Commit).To(Equal(v1.Status.UpstreamLock.Git.Commit))
	}

	// status.deployment and resource size
	g.Expect(v2.Status.Deployment).To(Equal(deployment))
	g.Expect(v2.Status.ResourcesSizeBytes).To(BeNumerically(">", 0))
}

// expectV1MatchesSnapshot checks a v1alpha1 revision served after rollback still carries the snapshot data.
func expectV1MatchesSnapshot(g Gomega, snap v1Snapshot, got *porchv1alpha1.PackageRevision) {
	want := snap.pr
	g.Expect(got.Spec.Lifecycle).To(Equal(want.Spec.Lifecycle))
	for k, v := range userLabels(want.Labels) {
		g.Expect(got.Labels).To(HaveKeyWithValue(k, v), "object label %q", k)
	}
	for k, v := range want.Annotations {
		g.Expect(got.Annotations).To(HaveKeyWithValue(k, v), "object annotation %q", k)
	}
	g.Expect(v1MetaLabels(got)).To(Equal(v1MetaLabels(want)))
	g.Expect(v1MetaAnnotations(got)).To(Equal(v1MetaAnnotations(want)))
	g.Expect(got.Spec.ReadinessGates).To(ConsistOf(want.Spec.ReadinessGates))
	for _, c := range want.Status.Conditions {
		g.Expect(got.Status.Conditions).To(ContainElement(c))
	}
	g.Expect(got.Status.UpstreamLock).To(Equal(want.Status.UpstreamLock))
	for _, ref := range want.OwnerReferences {
		g.Expect(got.OwnerReferences).To(ContainElement(HaveField("UID", ref.UID)), "ownerReference %s/%s", ref.Kind, ref.Name)
	}
	for _, f := range want.Finalizers {
		g.Expect(got.Finalizers).To(ContainElement(f), "finalizer %q", f)
	}
}

func getPRRResourcesG(g Gomega, ctx context.Context, namespace, name string) map[string]string {
	prr := &porchv1alpha1.PackageRevisionResources{}
	g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, prr)).To(Succeed())
	return prr.Spec.Resources
}

var _ = Describe("Migration Drafts: zero data loss", Ordered, Label("migration", "drafts"), func() {
	// Every way a v1alpha1 Draft/Proposed revision can carry data must survive
	// migration to v1alpha2, repeat syncs, post-migration lifecycle changes, and
	// rollback. Each fixture below exercises a different mechanism; all are
	// snapshotted from the v1alpha1 API before migration and compared afterwards.
	//
	// Mechanisms covered:
	//   - v1alpha1 spec.packageMetadata / spec.readinessGates set on create (written to Kptfile)
	//   - v1alpha1 spec.packageMetadata updated on an existing draft
	//   - Kptfile labels/annotations/info.readinessGates/status.conditions written via PRR
	//   - object labels/annotations (incl. "/" keys)
	//   - extra files, nested directories
	//   - Proposed lifecycle
	//   - clone (Kptfile upstream + status.upstreamLock)
	//   - edit (new draft revision of an already-published package)
	//   - nested package path
	//   - DeletionProposed (published revision with a deletionProposed branch in git)
	//   - object ownerReferences and finalizers (e.g. PackageVariant-owned drafts)
	//   - repository spec.deployment -> status.deployment

	const (
		repoName = "mig-draft-zdl"
		ws       = "ws1"
		upstream = "upstream"
	)

	type fixture struct {
		pkg, ws string
	}

	var (
		apiMeta     = fixture{"api-meta", ws}
		kptfileMeta = fixture{"kptfile-meta", ws}
		proposed    = fixture{"proposed-meta", ws}
		cloned      = fixture{"cloned", ws}
		nested      = fixture{"nested/child", ws}
		edited      = fixture{upstream, "v2"}
		doomed      = fixture{"doomed", "v1"}
		owned       = fixture{"owned", ws}
		fixtures    = []fixture{apiMeta, kptfileMeta, proposed, cloned, nested, edited, doomed, owned}
	)

	const (
		ownerCMName   = "mig-draft-zdl-owner"
		userFinalizer = "example.com/keep"
	)

	var (
		env          *testEnv
		upstreamName string
		snaps        = map[fixture]v1Snapshot{}
	)

	createV1 := func(pr *porchv1alpha1.PackageRevision) string {
		pr.Namespace = env.Namespace
		pr.Spec.RepositoryName = repoName
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		Eventually(func(g Gomega) {
			got := &porchv1alpha1.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), got)).To(Succeed())
			g.Expect(got.Spec.Lifecycle).To(Equal(porchv1alpha1.PackageRevisionLifecycleDraft))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		return pr.Name
	}

	initTask := func(desc string) []porchv1alpha1.Task {
		return []porchv1alpha1.Task{{Type: porchv1alpha1.TaskTypeInit, Init: &porchv1alpha1.PackageInitTaskSpec{Description: desc}}}
	}

	BeforeAll(func() {
		env = sharedEnv()

		cleanupMigrationResources(env.Ctx, env.Namespace, repoName)
		deleteGiteaRepo(repoName)
		createGiteaRepo(repoName)
		registerV1Alpha1Repo(env.Ctx, env.Namespace, repoName, withDeployment())

		By("publishing an upstream package (clone source and base for an edit draft)")
		upstreamName = createAndPublishV1Alpha1Package(env.Ctx, env.Namespace, repoName, upstream, "v1").name

		By("fixture api-meta: packageMetadata + readinessGates via v1alpha1 spec, then updated")
		apiName := createV1(&porchv1alpha1.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      map[string]string{"owner": "team-a", "example.com/team": "net"},
				Annotations: map[string]string{"contact": "a@example.com", "example.com/ticket": "T-1"},
			},
			Spec: porchv1alpha1.PackageRevisionSpec{
				PackageName:   apiMeta.pkg,
				WorkspaceName: apiMeta.ws,
				Tasks:         initTask(apiMeta.pkg),
				PackageMetadata: &porchv1alpha1.PackageMetadata{
					Labels:      map[string]string{"app": "api-meta", "kpt.dev/tier": "gold"},
					Annotations: map[string]string{"note": "from-create"},
				},
				ReadinessGates: []porchv1alpha1.ReadinessGate{{ConditionType: "example.com/ApiReady"}},
			},
		})
		Eventually(func(g Gomega) {
			pr := &porchv1alpha1.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: apiName}, pr)).To(Succeed())
			g.Expect(pr.Spec.PackageMetadata).NotTo(BeNil())
			pr.Spec.PackageMetadata.Labels["updated"] = "via-v1alpha1-update"
			pr.Spec.PackageMetadata.Annotations["note2"] = "from-update"
			g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		updatePRRResources(env.Ctx, env.Namespace, apiName, map[string]string{
			"data.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: api-meta\ndata:\n  k: v\n",
		})

		By("fixture kptfile-meta: full Kptfile (labels, annotations, readinessGates, conditions) + nested files via PRR")
		kfName := createV1(&porchv1alpha1.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"owner": "team-b"}},
			Spec: porchv1alpha1.PackageRevisionSpec{
				PackageName:   kptfileMeta.pkg,
				WorkspaceName: kptfileMeta.ws,
				Tasks:         initTask(kptfileMeta.pkg),
			},
		})
		updatePRRResources(env.Ctx, env.Namespace, kfName, map[string]string{
			"Kptfile": `apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: kptfile-meta
  annotations:
    config.kubernetes.io/local-config: "true"
    description: kptfile-written
  labels:
    app: kptfile-meta
    kpt.dev/version: v1beta1
info:
  description: kptfile-meta
  readinessGates:
  - conditionType: example.com/KptfileReady
status:
  conditions:
  - type: example.com/KptfileReady
    status: "True"
    reason: Seeded
    message: set before migration
`,
			"data.yaml":       "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: kptfile-meta\ndata:\n  k: v\n",
			"sub/nested.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: nested\ndata:\n  deep: \"true\"\n",
			"README.md":       "# kptfile-meta\n\nnon-KRM file must survive migration\n",
		})

		By("fixture proposed-meta: packageMetadata + object metadata, moved to Proposed")
		propName := createV1(&porchv1alpha1.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      map[string]string{"owner": "team-c"},
				Annotations: map[string]string{"contact": "c@example.com"},
			},
			Spec: porchv1alpha1.PackageRevisionSpec{
				PackageName:     proposed.pkg,
				WorkspaceName:   proposed.ws,
				Tasks:           initTask(proposed.pkg),
				PackageMetadata: &porchv1alpha1.PackageMetadata{Labels: map[string]string{"app": "proposed"}},
			},
		})
		updatePRRResources(env.Ctx, env.Namespace, propName, map[string]string{
			"data.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: proposed\ndata:\n  k: v\n",
		})
		Eventually(func(g Gomega) {
			pr := &porchv1alpha1.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: propName}, pr)).To(Succeed())
			pr.Spec.Lifecycle = porchv1alpha1.PackageRevisionLifecycleProposed
			g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("fixture cloned: clone of the published upstream (upstream + upstreamLock)")
		createV1(&porchv1alpha1.PackageRevision{
			Spec: porchv1alpha1.PackageRevisionSpec{
				PackageName:   cloned.pkg,
				WorkspaceName: cloned.ws,
				Tasks: []porchv1alpha1.Task{{
					Type: porchv1alpha1.TaskTypeClone,
					Clone: &porchv1alpha1.PackageCloneTaskSpec{
						Upstream: porchv1alpha1.UpstreamPackage{UpstreamRef: &porchv1alpha1.PackageRevisionRef{Name: upstreamName}},
					},
				}},
			},
		})

		By("fixture nested/child: package in a sub-directory path")
		createV1(&porchv1alpha1.PackageRevision{
			Spec: porchv1alpha1.PackageRevisionSpec{
				PackageName:   nested.pkg,
				WorkspaceName: nested.ws,
				Tasks:         initTask("nested"),
			},
		})

		By("fixture upstream/v2: new draft revision of the published package (edit task)")
		editName := createV1(&porchv1alpha1.PackageRevision{
			Spec: porchv1alpha1.PackageRevisionSpec{
				PackageName:   edited.pkg,
				WorkspaceName: edited.ws,
				Tasks: []porchv1alpha1.Task{{
					Type: porchv1alpha1.TaskTypeEdit,
					Edit: &porchv1alpha1.PackageEditTaskSpec{Source: &porchv1alpha1.PackageRevisionRef{Name: upstreamName}},
				}},
			},
		})
		updatePRRResources(env.Ctx, env.Namespace, editName, map[string]string{
			"v2-only.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: v2-only\ndata:\n  k: v\n",
		})

		By("fixture doomed/v1: published revision with object labels, then proposed for deletion")
		doomedName := createAndPublishV1Alpha1Package(env.Ctx, env.Namespace, repoName, doomed.pkg, doomed.ws).name
		Eventually(func(g Gomega) {
			pr := &porchv1alpha1.PackageRevision{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: doomedName}, pr)).To(Succeed())
			if pr.Labels == nil {
				pr.Labels = map[string]string{}
			}
			pr.Labels["owner"] = "team-d"
			pr.Spec.Lifecycle = porchv1alpha1.PackageRevisionLifecycleDeletionProposed
			g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("fixture owned: draft with a non-repository ownerReference and a user finalizer")
		// Stands in for a PackageVariant-owned draft: PV sets an ownerReference on the drafts it creates.
		ownerCM := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: ownerCMName, Namespace: env.Namespace}}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(env.Ctx, ownerCM))).To(Succeed())
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(ownerCM), ownerCM)).To(Succeed())
		createV1(&porchv1alpha1.PackageRevision{
			ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "v1", Kind: "ConfigMap", Name: ownerCM.Name, UID: ownerCM.UID,
				}},
				Finalizers: []string{userFinalizer},
			},
			Spec: porchv1alpha1.PackageRevisionSpec{
				PackageName:   owned.pkg,
				WorkspaceName: owned.ws,
				Tasks:         initTask(owned.pkg),
			},
		})

		By("snapshotting every fixture from the v1alpha1 API")
		for _, f := range fixtures {
			snaps[f] = takeV1Snapshot(env.Ctx, env.Namespace, repoName, f.pkg, f.ws)
		}

		By("sanity-checking that each fixture actually exercises its mechanism")
		Expect(snaps[proposed].pr.Spec.Lifecycle).To(Equal(porchv1alpha1.PackageRevisionLifecycleProposed))
		Expect(v1MetaLabels(snaps[apiMeta].pr)).To(HaveKeyWithValue("updated", "via-v1alpha1-update"))
		Expect(snaps[apiMeta].pr.Spec.ReadinessGates).NotTo(BeEmpty())
		Expect(snaps[apiMeta].pr.Labels).To(HaveKeyWithValue("example.com/team", "net"))
		Expect(snaps[apiMeta].pr.Annotations).To(HaveKeyWithValue("example.com/ticket", "T-1"))
		Expect(snaps[kptfileMeta].pr.Spec.ReadinessGates).NotTo(BeEmpty())
		Expect(snaps[kptfileMeta].pr.Status.Conditions).NotTo(BeEmpty())
		Expect(snaps[kptfileMeta].resources).To(HaveKey("sub/nested.yaml"))
		Expect(snaps[cloned].pr.Status.UpstreamLock).NotTo(BeNil())
		Expect(snaps[edited].resources).To(HaveKey("v2-only.yaml"))
		Expect(snaps[doomed].pr.Spec.Lifecycle).To(Equal(porchv1alpha1.PackageRevisionLifecycleDeletionProposed))
		Expect(snaps[doomed].pr.Spec.Revision).To(BeNumerically(">", 0))
		Expect(snaps[doomed].pr.Labels).To(HaveKeyWithValue("owner", "team-d"))
		Expect(snaps[owned].pr.OwnerReferences).To(ContainElement(HaveField("Name", ownerCMName)))
		Expect(snaps[owned].pr.Finalizers).To(ContainElement(userFinalizer))
		for _, f := range fixtures {
			if f == doomed {
				continue
			}
			Expect(snaps[f].pr.Spec.Revision).To(Equal(0), "%s/%s should be unpublished", f.pkg, f.ws)
		}
	})

	AfterAll(func() {
		cleanupMigrationResources(env.Ctx, env.Namespace, repoName)
		deleteGiteaRepo(repoName)
		k8sClient.Delete(env.Ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: ownerCMName, Namespace: env.Namespace}}) //nolint:errcheck
	})

	It("should create a v1alpha2 CRD for every revision", func() {
		enableV1Alpha2Migration(env.Ctx, env.Namespace, repoName)

		Eventually(func(g Gomega) {
			for _, f := range fixtures {
				g.Expect(findV2(g, env.Ctx, env.Namespace, repoName, f.pkg, f.ws)).NotTo(BeNil(), "%s/%s not migrated", f.pkg, f.ws)
			}
			g.Expect(findV2(g, env.Ctx, env.Namespace, repoName, upstream, "v1")).NotTo(BeNil(), "published upstream not migrated")
		}).WithTimeout(defaultTimeout * 3).WithPolling(defaultInterval).Should(Succeed())
		waitForRepoReady(env.Ctx, env.Namespace, repoName)
	})

	for _, f := range fixtures {
		It("should preserve all data for "+f.pkg+"/"+f.ws, func() {
			Eventually(func(g Gomega) {
				v2 := findV2(g, env.Ctx, env.Namespace, repoName, f.pkg, f.ws)
				g.Expect(v2).NotTo(BeNil())
				expectV2MatchesSnapshot(g, snaps[f], v2, true)
				expectResourcesPreserved(g, snaps[f], getPRRResourcesG(g, env.Ctx, env.Namespace, v2.Name))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		})
	}

	It("should keep latest-revision on the published revision, not its draft", func() {
		pub := findV2(Default, env.Ctx, env.Namespace, repoName, upstream, "v1")
		Expect(pub).NotTo(BeNil())
		Expect(pub.Labels).To(HaveKeyWithValue(porchv1alpha2.LatestPackageRevisionKey, porchv1alpha2.LatestPackageRevisionValue))

		draft := findV2(Default, env.Ctx, env.Namespace, repoName, edited.pkg, edited.ws)
		Expect(draft).NotTo(BeNil())
		Expect(draft.Labels[porchv1alpha2.LatestPackageRevisionKey]).NotTo(Equal(porchv1alpha2.LatestPackageRevisionValue))
	})

	// Known gap: buildPackageRevision sets only the Repository ownerReference and
	// sourceMetadata copies only labels/annotations, so v1alpha1 ownerReferences
	// (e.g. PackageVariant) and user finalizers are not carried onto the CRD. The
	// v1alpha1 row keeps them (verified in the rollback spec). Un-pend once fixed.
	PIt("should carry v1alpha1 ownerReferences and finalizers onto the v1alpha2 CRD", func() {
		v2 := findV2(Default, env.Ctx, env.Namespace, repoName, owned.pkg, owned.ws)
		Expect(v2).NotTo(BeNil())
		Expect(v2.OwnerReferences).To(ContainElement(HaveField("Name", ownerCMName)))
		Expect(v2.Finalizers).To(ContainElement(userFinalizer))
	})

	It("should not delete or alter migrated revisions on repeat syncs", func() {
		for range 2 {
			triggerRepoSync(env.Ctx, env.Namespace, repoName)
			waitForRepoReady(env.Ctx, env.Namespace, repoName)
		}
		Consistently(func(g Gomega) {
			for _, f := range fixtures {
				v2 := findV2(g, env.Ctx, env.Namespace, repoName, f.pkg, f.ws)
				g.Expect(v2).NotTo(BeNil(), "%s/%s deleted by re-sync", f.pkg, f.ws)
				expectV2MatchesSnapshot(g, snaps[f], v2, true)
			}
		}).WithTimeout(15 * time.Second).WithPolling(3 * time.Second).Should(Succeed())
	})

	It("should keep v1alpha2 packageMetadata edits on a migrated draft across syncs", func() {
		pr := findV2(Default, env.Ctx, env.Namespace, repoName, apiMeta.pkg, apiMeta.ws)
		Expect(pr).NotTo(BeNil())
		waitForReady(env.Ctx, pr)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
			g.Expect(pr.Spec.PackageMetadata).NotTo(BeNil())
			pr.Spec.PackageMetadata.Labels["edited"] = "after-migration"
			g.Expect(k8sClient.Update(env.Ctx, pr)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		waitForMetadataRenderSettled(env.Ctx, pr)

		// The edit is now part of the expected state for later checks (incl. rollback).
		snaps[apiMeta].pr.Spec.PackageMetadata.Labels["edited"] = "after-migration"

		triggerRepoSync(env.Ctx, env.Namespace, repoName)
		waitForRepoReady(env.Ctx, env.Namespace, repoName)

		Consistently(func(g Gomega) {
			v2 := findV2(g, env.Ctx, env.Namespace, repoName, apiMeta.pkg, apiMeta.ws)
			g.Expect(v2).NotTo(BeNil())
			expectV2MatchesSnapshot(g, snaps[apiMeta], v2, true)
			expectResourcesPreserved(g, snaps[apiMeta], getPRRResourcesG(g, env.Ctx, env.Namespace, v2.Name))
		}).WithTimeout(15 * time.Second).WithPolling(3 * time.Second).Should(Succeed())
	})

	It("should publish a migrated Draft via v1alpha2 without losing content", func() {
		pr := findV2(Default, env.Ctx, env.Namespace, repoName, kptfileMeta.pkg, kptfileMeta.ws)
		Expect(pr).NotTo(BeNil())
		waitForReady(env.Ctx, pr)
		publishPackage(env.Ctx, pr)

		Eventually(func(g Gomega) {
			expectResourcesPreserved(g, snaps[kptfileMeta], getPRRResourcesG(g, env.Ctx, env.Namespace, pr.Name))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	It("should approve a migrated Proposed revision via v1alpha2 without losing content", func() {
		pr := findV2(Default, env.Ctx, env.Namespace, repoName, proposed.pkg, proposed.ws)
		Expect(pr).NotTo(BeNil())
		waitForReady(env.Ctx, pr)
		patchLifecycle(env.Ctx, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
		waitForPublished(env.Ctx, pr)

		Eventually(func(g Gomega) {
			expectResourcesPreserved(g, snaps[proposed], getPRRResourcesG(g, env.Ctx, env.Namespace, pr.Name))
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	It("should serve every revision via v1alpha1 after rollback with no data loss", func() {
		Eventually(func(g Gomega) {
			repo := &configapi.Repository{}
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: repoName}, repo)).To(Succeed())
			delete(repo.Annotations, configapi.AnnotationKeyV1Alpha2Migration)
			now := metav1.Now()
			if repo.Spec.Sync == nil {
				repo.Spec.Sync = &configapi.RepositorySync{}
			}
			repo.Spec.Sync.RunOnceAt = &now
			g.Expect(k8sClient.Update(env.Ctx, repo)).To(Succeed())
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		for _, f := range fixtures {
			snap := snaps[f]
			Eventually(func(g Gomega) {
				got := findV1(g, env.Ctx, env.Namespace, repoName, f.pkg, f.ws)
				g.Expect(got).NotTo(BeNil(), "%s/%s not served by v1alpha1 after rollback", f.pkg, f.ws)
				switch f {
				case kptfileMeta, proposed:
					// Published via v1alpha2 above; content and Kptfile metadata live in git.
					g.Expect(got.Spec.Lifecycle).To(Equal(porchv1alpha1.PackageRevisionLifecyclePublished))
					g.Expect(v1MetaLabels(got)).To(Equal(v1MetaLabels(snap.pr)))
				default:
					expectV1MatchesSnapshot(g, snap, got)
				}
				expectResourcesPreserved(g, snap, getPRRResourcesG(g, env.Ctx, env.Namespace, got.Name))
			}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
		}
	})
})
