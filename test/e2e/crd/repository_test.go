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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	configapi "github.com/nephio-project/porch/api/porchconfig/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Repository", Ordered, Label("infra"), func() {
	var env *testEnv

	BeforeAll(func() {
		env = sharedEnv()
	})

	// --- Tests that use the shared porch-test repo registered in BeforeSuite ---

	It("should register a git repository and become ready", func() {
		By("verifying repository spec")
		repo := &configapi.Repository{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: porchTestRepo}, repo)).To(Succeed())
		Expect(repo.Spec.Type).To(Equal(configapi.RepositoryTypeGit))
	})

	It("should allow updating mutable fields", func() {
		By("updating the description")
		repo := &configapi.Repository{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: porchTestRepo}, repo)).To(Succeed())
		repo.Spec.Description = "updated description"
		Expect(k8sClient.Update(env.Ctx, repo)).To(Succeed())

		By("verifying the description was updated")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: porchTestRepo}, repo)).To(Succeed())
		Expect(repo.Spec.Description).To(Equal("updated description"))
	})

	It("should reject modification of immutable fields", func() {
		By("attempting to change repo URL")
		Eventually(func() error {
			repo := &configapi.Repository{}
			if err := k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: porchTestRepo}, repo); err != nil {
				return err
			}
			repo.Spec.Git.Repo = "https://different-repo.git"
			err := k8sClient.Update(env.Ctx, repo)
			if err == nil {
				return fmt.Errorf("expected update to be rejected")
			}
			if apierrors.IsConflict(err) {
				return err // retry on conflict
			}
			// Got a non-conflict error — should be the immutability rejection
			Expect(err.Error()).To(ContainSubstring("immutable"))
			return nil
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())

		By("verifying repo URL unchanged")
		repo := &configapi.Repository{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: porchTestRepo}, repo)).To(Succeed())
		Expect(repo.Spec.Git.Repo).To(Equal(giteaRepoURL(porchTestRepo)))

		By("attempting to change branch")
		Eventually(func() error {
			repo := &configapi.Repository{}
			if err := k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: porchTestRepo}, repo); err != nil {
				return err
			}
			repo.Spec.Git.Branch = "different-branch"
			err := k8sClient.Update(env.Ctx, repo)
			if err == nil {
				return fmt.Errorf("expected update to be rejected")
			}
			if apierrors.IsConflict(err) {
				return err // retry on conflict
			}
			Expect(err.Error()).To(ContainSubstring("immutable"))
			return nil
		}).WithTimeout(defaultTimeout).WithPolling(defaultInterval).Should(Succeed())
	})

	It("should preserve packages after repo re-sync", func() {
		By("creating and publishing a package")
		pr := newPackageRevision(env.Namespace, porchTestRepo, "resync-pkg", "v1", withInit("resync test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		publishPackage(env.Ctx, pr)

		By("recording the published state")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
		originalRevision := pr.Status.Revision
		originalWorkspace := pr.Spec.WorkspaceName

		By("triggering a repo re-sync")
		triggerRepoSync(env.Ctx, env.Namespace, porchTestRepo)

		By("waiting for sync to complete")
		waitForRepoReady(env.Ctx, env.Namespace, porchTestRepo)

		By("verifying the package still exists with correct metadata")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())
			g.Expect(pr.Status.Revision).To(Equal(originalRevision))
			g.Expect(pr.Spec.WorkspaceName).To(Equal(originalWorkspace))
			g.Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
		}).WithTimeout(defaultTimeout).Should(Succeed())
	})

	It("should not resurrect deleted packages after repo re-sync", func() {
		By("creating and publishing a package")
		pr := newPackageRevision(env.Namespace, porchTestRepo, "resync-del-pkg", "v1", withInit("delete resync test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		publishPackage(env.Ctx, pr)

		By("deleting the published package")
		deletePackage(env.Ctx, pr)
		Eventually(func() bool {
			err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)
			return apierrors.IsNotFound(err)
		}).WithTimeout(defaultTimeout).Should(BeTrue())

		By("triggering a repo re-sync")
		triggerRepoSync(env.Ctx, env.Namespace, porchTestRepo)
		waitForRepoReady(env.Ctx, env.Namespace, porchTestRepo)

		By("verifying the deleted package does not reappear")
		Consistently(func() bool {
			err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)
			return apierrors.IsNotFound(err)
		}).WithTimeout(5 * time.Second).Should(BeTrue())
	})

	It("should list packages from working repo even with a hanging repo present", func() {
		By("creating a package in the working repo")
		pr := newPackageRevision(env.Namespace, porchTestRepo, "hang-test-pkg", "v1", withInit("hanging repo test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)

		By("creating a hanging (unreachable) repo")
		hangingRepo := &configapi.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hanging-repo",
				Namespace: env.Namespace,
				Annotations: map[string]string{
					"porch.kpt.dev/v1alpha2-migration": "true",
				},
			},
			Spec: configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo: "http://10.255.255.1/hanging.git",
				},
			},
		}
		Expect(k8sClient.Create(env.Ctx, hangingRepo)).To(Succeed())
		DeferCleanup(func() {
			k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(hangingRepo), hangingRepo)
			hangingRepo.Finalizers = nil
			k8sClient.Update(env.Ctx, hangingRepo)
			k8sClient.Delete(env.Ctx, hangingRepo)
		})

		By("listing packages with field selector for working repo")
		var list porchv1alpha2.PackageRevisionList
		Expect(k8sClient.List(env.Ctx, &list, client.InNamespace(env.Namespace),
			client.MatchingFields{string(porchv1alpha2.PkgRevSelectorRepository): porchTestRepo},
		)).To(Succeed())

		By("verifying the working repo's package is returned")
		found := false
		for _, item := range list.Items {
			if item.Name == pr.Name {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "expected to find package from working repo")
	})

	It("should not prefix package names with repository directory", func() {
		repoName := "dir-test"
		createGiteaRepo(repoName)
		DeferCleanup(deleteGiteaRepo, repoName)

		By("creating auth secret")
		secretName := repoName + "-auth"
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: env.Namespace},
			Immutable:  ptr.To(true),
			Data:       map[string][]byte{"username": []byte(giteaUser), "password": []byte(giteaPassword)},
			Type:       corev1.SecretTypeBasicAuth,
		}
		Expect(k8sClient.Create(env.Ctx, secret)).To(Succeed())

		By("registering dir-test repo with directory filter")
		repo := &configapi.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      repoName,
				Namespace: env.Namespace,
				Annotations: map[string]string{
					"porch.kpt.dev/v1alpha2-migration": "true",
				},
			},
			Spec: configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo:      giteaRepoURL(repoName),
					Branch:    "main",
					Directory: "test-dir",
					SecretRef: configapi.SecretRef{Name: secretName},
				},
			},
		}
		Expect(k8sClient.Create(env.Ctx, repo)).To(Succeed())
		DeferCleanup(cleanupRepo, env.Ctx, env.Namespace, repoName)
		waitForRepoReady(env.Ctx, env.Namespace, repoName)

		By("creating a package in the directory-filtered repo")
		pr := newPackageRevision(env.Namespace, repoName, "dir-pkg", "v1", withInit("directory test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		publishPackage(env.Ctx, pr)

		By("verifying package name does not include directory prefix")
		Expect(pr.Spec.PackageName).NotTo(HavePrefix("test-dir"))
		Expect(strings.Contains(pr.Spec.PackageName, "test-dir")).To(BeFalse())
	})

	// --- Tests that need their own repo config (separate namespace to avoid URL conflicts) ---

	It("should report error for unreachable repository", func() {
		repoName := "error-test"

		By("creating a repository pointing to an invalid URL")
		repo := &configapi.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      repoName,
				Namespace: env.Namespace,
				Annotations: map[string]string{
					"porch.kpt.dev/v1alpha2-migration": "true",
				},
			},
			Spec: configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo: "https://repo.invalid/nonexistent.git",
				},
			},
		}
		Expect(k8sClient.Create(env.Ctx, repo)).To(Succeed())
		DeferCleanup(func() {
			k8sClient.Delete(env.Ctx, repo)
		})

		By("waiting for Ready=False condition")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: repoName}, repo)).To(Succeed())
			g.Expect(repo.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(configapi.RepositoryReady)),
				HaveField("Status", Equal(metav1.ConditionFalse)),
			)))
		}).WithTimeout(180 * time.Second).WithPolling(5 * time.Second).Should(Succeed()) // longer timeout — DNS resolution for invalid host
	})

	It("should default branch to main", func() {
		By("creating a separate gitea repo for branch-default test")
		createGiteaRepo("branch-default")
		DeferCleanup(deleteGiteaRepo, "branch-default")

		By("creating a repository without specifying branch")
		repo := &configapi.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "branch-default",
				Namespace: env.Namespace,
				Annotations: map[string]string{
					"porch.kpt.dev/v1alpha2-migration": "true",
				},
			},
			Spec: configapi.RepositorySpec{
				Type: configapi.RepositoryTypeGit,
				Git: &configapi.GitRepository{
					Repo: giteaRepoURL("branch-default"),
				},
			},
		}
		Expect(k8sClient.Create(env.Ctx, repo)).To(Succeed())
		DeferCleanup(func() {
			k8sClient.Delete(env.Ctx, repo)
		})

		By("verifying branch defaults to main")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: "branch-default"}, repo)).To(Succeed())
		Expect(repo.Spec.Git.Branch).To(Equal("main"))
	})

	It("should discover packages from a registered repository", func() {
		By("verifying basens/v1 discovered from test-blueprints (registered in BeforeSuite)")
		pr := &porchv1alpha2.PackageRevision{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
			Namespace: env.Namespace,
			Name:      crdName(testBlueprintsRepo, "basens", "v1"),
		}, pr)).To(Succeed())
		Expect(pr.Spec.PackageName).To(Equal("basens"))
		Expect(pr.Spec.RepositoryName).To(Equal(testBlueprintsRepo))
		Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
	})

	It("should seed lifecycle and revision for discovered published packages", func() {
		By("verifying basens/v1 from test-blueprints has lifecycle and revision set")
		pr := &porchv1alpha2.PackageRevision{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
				Namespace: env.Namespace,
				Name:      crdName(testBlueprintsRepo, "basens", "v1"),
			}, pr)).To(Succeed())
			g.Expect(pr.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
			g.Expect(pr.Status.Revision).To(Equal(1))
		}).WithTimeout(defaultTimeout).Should(Succeed())

		By("verifying basens/v2 has a higher revision")
		pr2 := &porchv1alpha2.PackageRevision{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(env.Ctx, client.ObjectKey{
				Namespace: env.Namespace,
				Name:      crdName(testBlueprintsRepo, "basens", "v2"),
			}, pr2)).To(Succeed())
			g.Expect(pr2.Spec.Lifecycle).To(Equal(porchv1alpha2.PackageRevisionLifecyclePublished))
			g.Expect(pr2.Status.Revision).To(Equal(2))
		}).WithTimeout(defaultTimeout).Should(Succeed())
	})

	It("should cascade-delete PackageRevision CRDs when repository is deleted", func() {
		repoName := "cascade-test"
		createGiteaRepo(repoName)
		DeferCleanup(deleteGiteaRepo, repoName)

		By("registering the repo")
		registerV1Alpha2Repo(env.Ctx, env.Namespace, repoName)

		By("creating and publishing a package")
		pr := newPackageRevision(env.Namespace, repoName, "cascade-pkg", "v1", withInit("cascade test"))
		Expect(k8sClient.Create(env.Ctx, pr)).To(Succeed())
		waitForReady(env.Ctx, pr)
		publishPackage(env.Ctx, pr)

		By("verifying the package exists")
		Expect(k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)).To(Succeed())

		By("deleting the repository")
		repo := &configapi.Repository{}
		Expect(k8sClient.Get(env.Ctx, client.ObjectKey{Namespace: env.Namespace, Name: repoName}, repo)).To(Succeed())
		Expect(k8sClient.Delete(env.Ctx, repo)).To(Succeed())

		By("verifying the PackageRevision CRD is garbage collected")
		Eventually(func() bool {
			err := k8sClient.Get(env.Ctx, client.ObjectKeyFromObject(pr), pr)
			return apierrors.IsNotFound(err)
		}).WithTimeout(defaultTimeout).Should(BeTrue())
	})

	It("should isolate packages across namespaces", func() {
		By("creating a second namespace")
		ns2 := env.Namespace + "-iso"
		Expect(k8sClient.Create(env.Ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns2}})).To(Succeed())
		DeferCleanup(func() {
			cleanupRepo(env.Ctx, ns2, testBlueprintsRepo)
			k8sClient.Delete(env.Ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns2}})
		})

		By("registering test-blueprints in the second namespace")
		registerV1Alpha2Repo(env.Ctx, ns2, testBlueprintsRepo)
		triggerRepoSync(env.Ctx, ns2, testBlueprintsRepo)
		waitForDiscovery(env.Ctx, ns2, crdName(testBlueprintsRepo, "basens", "v1"))

		By("verifying packages exist in both namespaces")
		var list1 porchv1alpha2.PackageRevisionList
		Expect(k8sClient.List(env.Ctx, &list1, client.InNamespace(env.Namespace))).To(Succeed())
		var list2 porchv1alpha2.PackageRevisionList
		Expect(k8sClient.List(env.Ctx, &list2, client.InNamespace(ns2))).To(Succeed())
		Expect(list1.Items).NotTo(BeEmpty())
		Expect(list2.Items).NotTo(BeEmpty())

		By("verifying each namespace only contains its own packages")
		for _, pr := range list1.Items {
			Expect(pr.Namespace).To(Equal(env.Namespace))
		}
		for _, pr := range list2.Items {
			Expect(pr.Namespace).To(Equal(ns2))
		}
	})
})
