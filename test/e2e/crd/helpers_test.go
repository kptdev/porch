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
	"maps"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/gomega"
	porchv1alpha1 "github.com/nephio-project/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	configapi "github.com/nephio-project/porch/api/porchconfig/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	giteaUser     = "nephio"
	giteaPassword = "secret"

	giteaLBIP         = "172.18.255.200"
	giteaClusterHost  = "gitea.gitea.svc.cluster.local:3000"
	giteaLBHost       = giteaLBIP + ":3000"

	defaultTimeout  = 120 * time.Second
	defaultInterval = 50 * time.Millisecond
)

func giteaBaseURL() string {
	if allInCluster {
		return "http://" + giteaClusterHost
	}
	return "http://" + giteaLBHost
}

func giteaRepoURL(name string) string {
	return giteaBaseURL() + "/nephio/" + name + ".git"
}

func giteaAPIBaseURL() string {
	// API calls always go via LoadBalancer (from test runner)
	return "http://" + giteaLBHost
}

// --- Gitea helpers ---

func createGiteaRepo(name string) {
	url := fmt.Sprintf("%s/api/v1/user/repos", giteaAPIBaseURL())
	body := fmt.Sprintf(`{"name":"%s","auto_init":true}`, name)
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	Expect(err).NotTo(HaveOccurred())
	req.SetBasicAuth(giteaUser, giteaPassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	Expect(resp.StatusCode).To(SatisfyAny(Equal(201), Equal(409))) // created or already exists
}

func deleteGiteaRepo(name string) {
	url := fmt.Sprintf("%s/api/v1/repos/nephio/%s", giteaAPIBaseURL(), name)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return
	}
	req.SetBasicAuth(giteaUser, giteaPassword)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func recreateGiteaRepo(name string) {
	deleteGiteaRepo(name)
	createGiteaRepo(name)
}

// forkGiteaRepo forks an existing gitea repo into a new repo with the given name.
// The fork includes all branches and tags from the source.
func forkGiteaRepo(source, name string) {
	url := fmt.Sprintf("%s/api/v1/repos/nephio/%s/forks", giteaAPIBaseURL(), source)
	body := fmt.Sprintf(`{"name":"%s"}`, name)
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	Expect(err).NotTo(HaveOccurred())
	req.SetBasicAuth(giteaUser, giteaPassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	Expect(resp.StatusCode).To(SatisfyAny(Equal(202), Equal(409))) // accepted or already exists
}

func createGiteaAPIToken(name string) (string, error) {
	// Delete existing token first (idempotent)
	deleteGiteaAPIToken(name)

	url := fmt.Sprintf("%s/api/v1/users/nephio/tokens", giteaAPIBaseURL())
	body := fmt.Sprintf(`{"name":"%s","scopes":["read:repository"]}`, name)
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(giteaUser, giteaPassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		return "", fmt.Errorf("failed to create gitea token: %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	token, ok := result["sha1"].(string)
	if !ok {
		return "", fmt.Errorf("no sha1 in token response")
	}
	return token, nil
}

func deleteGiteaAPIToken(name string) {
	url := fmt.Sprintf("%s/api/v1/users/nephio/tokens/%s", giteaAPIBaseURL(), name)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return
	}
	req.SetBasicAuth(giteaUser, giteaPassword)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// --- Repository helpers ---

func registerV1Alpha2Repo(ctx context.Context, namespace, repoName string, opts ...repoOption) {
	secretName := repoName + "-auth"

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Immutable: ptr.To(true),
		Data: map[string][]byte{
			"username": []byte(giteaUser),
			"password": []byte(giteaPassword),
		},
		Type: corev1.SecretTypeBasicAuth,
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())

	repo := &configapi.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoName,
			Namespace: namespace,
			Annotations: map[string]string{
				"porch.kpt.dev/v1alpha2-migration": "true",
			},
		},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				Repo:   giteaRepoURL(repoName),
				Branch: "main",
				SecretRef: configapi.SecretRef{
					Name: secretName,
				},
			},
		},
	}
	for _, opt := range opts {
		opt(repo)
	}
	Expect(k8sClient.Create(ctx, repo)).To(Succeed())

	waitForRepoReady(ctx, namespace, repoName)
}

type repoOption func(*configapi.Repository)

func withDeployment() repoOption {
	return func(repo *configapi.Repository) {
		repo.Spec.Deployment = true
	}
}

func waitForRepoReady(ctx context.Context, namespace, name string) {
	repo := &configapi.Repository{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, repo)).To(Succeed())
		g.Expect(repo.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", Equal(configapi.RepositoryReady)),
			HaveField("Status", Equal(metav1.ConditionTrue)),
		)))
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
}

func triggerRepoSync(ctx context.Context, namespace, name string) {
	repo := &configapi.Repository{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, repo)).To(Succeed())
	if repo.Annotations == nil {
		repo.Annotations = map[string]string{}
	}
	repo.Annotations["config.porch.kpt.dev/run-once-at"] = time.Now().UTC().Format(time.RFC3339)
	Expect(k8sClient.Update(ctx, repo)).To(Succeed())
}

func cleanupRepo(ctx context.Context, namespace, repoName string) {
	k8sClient.Delete(ctx, &configapi.Repository{ObjectMeta: metav1.ObjectMeta{Name: repoName, Namespace: namespace}})
	k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: repoName + "-auth", Namespace: namespace}})
}

// --- PackageRevision helpers ---

func crdName(repo, pkg, ws string) string {
	return fmt.Sprintf("%s.%s.%s", repo, pkg, ws)
}

func newPackageRevision(namespace, repo, pkg, ws string, opts ...prOption) *porchv1alpha2.PackageRevision {
	pr := &porchv1alpha2.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crdName(repo, pkg, ws),
			Namespace: namespace,
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    pkg,
			RepositoryName: repo,
			WorkspaceName:  ws,
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
		},
	}
	for _, opt := range opts {
		opt(pr)
	}
	return pr
}

type prOption func(*porchv1alpha2.PackageRevision)

func withInit(description string) prOption {
	return func(pr *porchv1alpha2.PackageRevision) {
		pr.Spec.Source = &porchv1alpha2.PackageSource{
			Init: &porchv1alpha2.PackageInitSpec{
				Description: description,
			},
		}
	}
}

func withInitFull(description string, keywords []string, site string) prOption {
	return func(pr *porchv1alpha2.PackageRevision) {
		pr.Spec.Source = &porchv1alpha2.PackageSource{
			Init: &porchv1alpha2.PackageInitSpec{
				Description: description,
				Keywords:    keywords,
				Site:        site,
			},
		}
	}
}

func withCloneFromRef(upstreamCRDName string) prOption {
	return func(pr *porchv1alpha2.PackageRevision) {
		pr.Spec.Source = &porchv1alpha2.PackageSource{
			CloneFrom: &porchv1alpha2.UpstreamPackage{
				UpstreamRef: &porchv1alpha2.PackageRevisionRef{
					Name: upstreamCRDName,
				},
			},
		}
	}
}

func withCopyFrom(sourceCRDName string) prOption {
	return func(pr *porchv1alpha2.PackageRevision) {
		pr.Spec.Source = &porchv1alpha2.PackageSource{
			CopyFrom: &porchv1alpha2.PackageRevisionRef{
				Name: sourceCRDName,
			},
		}
	}
}

func withUpgrade(oldUpstream, newUpstream, currentPkg string) prOption {
	return func(pr *porchv1alpha2.PackageRevision) {
		pr.Spec.Source = &porchv1alpha2.PackageSource{
			Upgrade: &porchv1alpha2.PackageUpgradeSpec{
				OldUpstream:    porchv1alpha2.PackageRevisionRef{Name: oldUpstream},
				NewUpstream:    porchv1alpha2.PackageRevisionRef{Name: newUpstream},
				CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: currentPkg},
			},
		}
	}
}

func withUpgradeStrategy(oldUpstream, newUpstream, currentPkg string, strategy porchv1alpha2.PackageMergeStrategy) prOption {
	return func(pr *porchv1alpha2.PackageRevision) {
		pr.Spec.Source = &porchv1alpha2.PackageSource{
			Upgrade: &porchv1alpha2.PackageUpgradeSpec{
				OldUpstream:    porchv1alpha2.PackageRevisionRef{Name: oldUpstream},
				NewUpstream:    porchv1alpha2.PackageRevisionRef{Name: newUpstream},
				CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: currentPkg},
				Strategy:       strategy,
			},
		}
	}
}

// --- Wait helpers ---

func waitForReady(ctx context.Context, pr *porchv1alpha2.PackageRevision) {
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		g.Expect(pr.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", Equal(porchv1alpha2.ConditionReady)),
			HaveField("Status", Equal(metav1.ConditionTrue)),
			HaveField("ObservedGeneration", Equal(pr.Generation)),
		)))
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
}

func waitForReadyFalse(ctx context.Context, pr *porchv1alpha2.PackageRevision) {
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		g.Expect(pr.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", Equal(porchv1alpha2.ConditionReady)),
			HaveField("Status", Equal(metav1.ConditionFalse)),
		)))
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
}

func waitForRenderFailed(ctx context.Context, pr *porchv1alpha2.PackageRevision) {
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		renderReq := pr.Annotations[porchv1alpha2.AnnotationRenderRequest]
		observed := pr.Status.ObservedPrrResourceVersion
		if renderReq != "" && renderReq != observed {
			g.Expect(observed).To(Equal(renderReq), "render not yet processed")
		}
		g.Expect(pr.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", Equal(porchv1alpha2.ConditionRendered)),
			HaveField("Status", Equal(metav1.ConditionFalse)),
		)))
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
}

func waitForRendered(ctx context.Context, pr *porchv1alpha2.PackageRevision) {
	// Wait for the render-request annotation to be set (PRR handler sets it on push),
	// then wait for the controller to render and set Rendered=True with an
	// observedPrrResourceVersion matching the render-request.
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		renderReq := pr.Annotations[porchv1alpha2.AnnotationRenderRequest]
		observed := pr.Status.ObservedPrrResourceVersion
		// If there's a pending render request, wait for it to be processed
		if renderReq != "" && renderReq != observed {
			g.Expect(observed).To(Equal(renderReq), "render not yet processed")
		}
		g.Expect(pr.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", Equal(porchv1alpha2.ConditionRendered)),
			HaveField("Status", Equal(metav1.ConditionTrue)),
		)))
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
}

func waitForPublished(ctx context.Context, pr *porchv1alpha2.PackageRevision) {
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		g.Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
		g.Expect(pr.Status.Revision).NotTo(Equal(0))
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
}

func waitForDiscovery(ctx context.Context, namespace, name string) {
	pr := &porchv1alpha2.PackageRevision{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, pr)).To(Succeed())
		g.Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
}

// --- Lifecycle helpers ---

func patchLifecycle(ctx context.Context, pr *porchv1alpha2.PackageRevision, lifecycle porchv1alpha2.PackageRevisionLifecycle) {
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		pr.Spec.Lifecycle = lifecycle
		g.Expect(k8sClient.Update(ctx, pr)).To(Succeed())
	}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
}

func publishPackage(ctx context.Context, pr *porchv1alpha2.PackageRevision) {
	patchLifecycle(ctx, pr, porchv1alpha2.PackageRevisionLifecycleProposed)
	waitForReady(ctx, pr)
	patchLifecycle(ctx, pr, porchv1alpha2.PackageRevisionLifecyclePublished)
	waitForPublished(ctx, pr)
}

func deletePackage(ctx context.Context, pr *porchv1alpha2.PackageRevision) {
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
	if pr.Spec.Lifecycle == porchv1alpha2.PackageRevisionLifecyclePublished {
		patchLifecycle(ctx, pr, porchv1alpha2.PackageRevisionLifecycleDeletionProposed)
	}
	Expect(k8sClient.Delete(ctx, pr)).To(Succeed())
}

// --- Test environment helpers ---

// testEnv holds shared state available to all tests.
type testEnv struct {
	Ctx       context.Context
	Namespace string
	RepoName  string
}

// sharedEnv returns the shared test environment set up in BeforeSuite.
func sharedEnv() *testEnv {
	return &testEnv{Ctx: sharedCtx, Namespace: sharedNamespace, RepoName: porchTestRepo}
}

// --- PRR (PackageRevisionResources) helpers ---

func getPRRResources(ctx context.Context, namespace, name string) map[string]string {
	prr := &porchv1alpha1.PackageRevisionResources{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, prr)).To(Succeed())
	return prr.Spec.Resources
}

func updatePRRResources(ctx context.Context, namespace, name string, resources map[string]string) {
	prr := &porchv1alpha1.PackageRevisionResources{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, prr)).To(Succeed())
	maps.Copy(prr.Spec.Resources, resources)
	Expect(k8sClient.Update(ctx, prr)).To(Succeed())
}

// --- Gitea ref helpers ---

// giteaRef is the minimal struct for gitea tag/branch API responses.
type giteaRef struct {
	Name string `json:"name"`
}

func listGiteaTags(repoName string) []string {
	return listGiteaRefs(repoName, "tags")
}

func listGiteaBranches(repoName string) []string {
	return listGiteaRefs(repoName, "branches")
}

func listGiteaRefs(repoName, refType string) []string {
	url := fmt.Sprintf("%s/api/v1/repos/nephio/%s/%s", giteaAPIBaseURL(), repoName, refType)
	req, err := http.NewRequest("GET", url, nil)
	Expect(err).NotTo(HaveOccurred())
	req.SetBasicAuth(giteaUser, giteaPassword)
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil
	}
	Expect(resp.StatusCode).To(Equal(200))
	var refs []giteaRef
	Expect(json.NewDecoder(resp.Body).Decode(&refs)).To(Succeed())
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = r.Name
	}
	return names
}

// branchExistsWithin polls gitea for a branch containing substr, returning true
// if found within the timeout. Used to probe whether push-drafts-to-git is enabled.
func branchExistsWithin(repoName, substr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, b := range listGiteaBranches(repoName) {
			if strings.Contains(b, substr) {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// --- v1alpha1 helpers (for cross-version and migration tests) ---

type v1alpha1Published struct {
	name        string
	packageName string
	gitRef      string
}

func createAndPublishV1Alpha1Package(ctx context.Context, namespace, repoName, pkgName, workspace string) v1alpha1Published {
	pr := &porchv1alpha1.PackageRevision{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
		Spec: porchv1alpha1.PackageRevisionSpec{
			PackageName:    pkgName,
			WorkspaceName:  workspace,
			RepositoryName: repoName,
			Tasks: []porchv1alpha1.Task{
				{Type: porchv1alpha1.TaskTypeInit, Init: &porchv1alpha1.PackageInitTaskSpec{Description: pkgName}},
			},
		},
	}
	Expect(k8sClient.Create(ctx, pr)).To(Succeed())

	pr.Spec.Lifecycle = porchv1alpha1.PackageRevisionLifecycleProposed
	Expect(k8sClient.Update(ctx, pr)).To(Succeed())
	pr.Spec.Lifecycle = porchv1alpha1.PackageRevisionLifecyclePublished
	Expect(k8sClient.SubResource("approval").Update(ctx, pr)).To(Succeed())

	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		g.Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha1.PackageRevisionLifecyclePublished))
		g.Expect(pr.Spec.Revision).To(BeNumerically(">", 0))
	}).WithTimeout(defaultTimeout).Should(Succeed())

	return v1alpha1Published{
		name:        pr.Name,
		packageName: pkgName,
		gitRef:      pkgName + "/" + workspace,
	}
}
