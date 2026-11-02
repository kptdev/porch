// Copyright 2025-2026 The kpt Authors
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

package api

import (
	"fmt"
	"strings"
	"sync"
	"time"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	suiteutils "github.com/kptdev/porch/test/e2e/suiteutils"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

func (t *PorchSuite) TestGitRepository() {
	const (
		repoName = "test-git-repo"
	)
	// Register the repository as 'git'
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create Package Revision
	pr := t.CreatePackageCloneF(repoName, "test-bucket", defaultWorkspace, defaultBucketBpRef, "bucket")

	// Get package resources
	var resources porchapi.PackageRevisionResources
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      pr.Name,
	}, &resources)

	bucket, ok := resources.Spec.Resources["bucket.yaml"]
	if !ok {
		t.Errorf("'bucket.yaml' not found among package resources")
	}
	node, err := yaml.Parse(bucket)
	if err != nil {
		t.Errorf("yaml.Parse(\"bucket.yaml\") failed: %v", err)
	}
	if got, want := node.GetName(), "blueprints-project-bucket2"; got != want {
		t.Errorf("StorageBucket name: got %q, want %q", got, want)
	}
}

func (t *PorchSuite) TestGitRepositoryWithReleaseTagsAndDirectory() {
	const (
		repoName  = "test-release-tag-dir"
		directory = "test-dir"
	)
	// Create repository and register with directory filter
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repoName, directory, suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create a package in the filtered repository
	pr := t.CreatePackageSkeleton(repoName, "test-package", "v1")
	pr.Spec.Tasks = []porchapi.Task{
		{
			Type: porchapi.TaskTypeInit,
			Init: &porchapi.PackageInitTaskSpec{
				Description: "Test package inside /test-dir",
			},
		},
	}
	t.CreateF(pr)

	// Approve the package to create a tag
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleProposed
	t.UpdateF(pr)
	pr.Spec.Lifecycle = porchapi.PackageRevisionLifecyclePublished
	t.UpdateApprovalF(pr)

	// Unregister and re-register the same repo to mock an exisiting repo
	// which contains a release tag and a directory
	repoKey := client.ObjectKey{
		Namespace: t.Namespace,
		Name:      repoName,
	}
	var repo configapi.Repository
	t.GetF(repoKey, &repo)
	t.DeleteE(&repo)
	t.WaitUntilRepositoryDeleted(repo.Name, t.Namespace)

	// Wait additional time for full cleanup of cached directory inside porch
	time.Sleep(2 * time.Second)

	repo.ResourceVersion = ""
	t.CreateE(&repo)
	t.WaitUntilRepositoryReady(repo.Name, t.Namespace)

	t.Log("Listing PackageRevisions in " + t.Namespace)
	var list porchapi.PackageRevisionList
	t.ListF(&list, client.InNamespace(t.Namespace))
	for _, pr := range list.Items {
		t.Logf("PackageRevision %s/%s found with package name %s", pr.Namespace, pr.Name, pr.Spec.PackageName)
		if strings.HasPrefix(pr.Spec.PackageName, directory) {
			t.Errorf("package name %q should not include repo directory %q as prefix", pr.Spec.PackageName, directory)
		}

		t.validatePackageResourcesSize(&pr)
	}
}

func (t *PorchSuite) TestRegisterRepository() {
	const (
		repository = "register"
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repository, "", suiteutils.GiteaUser,
		suiteutils.GiteaPassword, suiteutils.RepositoryOptions{RepOpts: suiteutils.WithType(configapi.RepositoryTypeGit)},
		suiteutils.RepositoryOptions{RepOpts: suiteutils.WithDeployment()})

	var repo configapi.Repository
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      repository,
	}, &repo)

	if got, want := repo.Spec.Type, configapi.RepositoryTypeGit; got != want {
		t.Errorf("Repo Type: got %q, want %q", got, want)
	}
	if got, want := repo.Spec.Deployment, true; got != want {
		t.Errorf("Repo Deployment: got %t, want %t", got, want)
	}
}

func (t *PorchSuite) TestRepositoryError() {
	const (
		repositoryName = "repo-with-error"
	)
	t.CreateF(&configapi.Repository{
		TypeMeta: metav1.TypeMeta{
			Kind:       configapi.TypeRepository.Kind,
			APIVersion: configapi.TypeRepository.APIVersion(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      repositoryName,
			Namespace: t.Namespace,
		},
		Spec: configapi.RepositorySpec{
			Description: "Repository With Error",
			Type:        configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				// Use `invalid` domain: https://www.rfc-editor.org/rfc/rfc6761#section-6.4
				Repo: "https://repo.invalid/repository.git",
			},
		},
	})
	t.Cleanup(func() {
		t.DeleteL(&configapi.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      repositoryName,
				Namespace: t.Namespace,
			},
		})
	})

	giveUp := time.Now().Add(180 * time.Second)

	for {
		now := time.Now()
		if now.After(giveUp) {
			t.Errorf("Timed out waiting for Repository Condition at %s", now.Format("15:04:05.000"))
			break
		}

		time.Sleep(5 * time.Second)

		var repository configapi.Repository
		t.GetF(client.ObjectKey{
			Namespace: t.Namespace,
			Name:      repositoryName,
		}, &repository)

		available := meta.FindStatusCondition(repository.Status.Conditions, configapi.RepositoryReady)
		if available == nil {
			// Condition not yet set
			t.Logf("[%s] Repository condition not yet available", now.Format("15:04:05.000"))
			continue
		}

		t.Logf("[%s] Repository condition: Status=%s, Reason=%s, Message=%s",
			now.Format("15:04:05.000"), available.Status, available.Reason, available.Message)

		if got, want := available.Status, metav1.ConditionFalse; got != want {
			t.Errorf("Repository Available Condition Status; got %q, want %q", got, want)
			break
		}
		if got, want := available.Reason, configapi.ReasonError; got != want {
			if available.Reason == configapi.ReasonReconciling {
				t.Logf("[%s] Repository still reconciling, waiting...", now.Format("15:04:05.000"))
				continue
			}
			t.Errorf("Repository Available Condition Reason: got %q, want %q", got, want)
		}
		break
	}
}

func (t *PorchSuite) TestRepositoryModify() {
	const (
		repositoryName = "repo-modify-test"
		newDescription = "Updated Repository Description"
	)
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repositoryName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Get the repository to modify
	var repository configapi.Repository
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      repositoryName,
	}, &repository)

	// Modify the repository
	repository.Spec.Description = newDescription
	t.UpdateF(&repository)

	// Wait for repository to be ready
	t.WaitUntilRepositoryReady(repositoryName, t.Namespace)

	// Verify description was updated
	var updatedRepo configapi.Repository
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      repositoryName,
	}, &updatedRepo)

	if updatedRepo.Spec.Description != newDescription {
		t.Errorf("Repository description not updated; got %q, want %q",
			updatedRepo.Spec.Description, newDescription)
	}

	t.Logf("Repository modify successful: description updated to %q", newDescription)
}

func (t *PorchSuite) TestPackageRevisionListWithHangingRepository() {
	const workingRepoName = "working-repo"

	// Create working repository
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), workingRepoName, "", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	// Create a PackageRevision in the working repo
	t.CreatePackageDraftF(workingRepoName, "test-package-hanging", defaultWorkspace)

	hangingURLs := []string{
		"http://10.255.255.1/test.git",
	}

	// Create hanging repositories in parallel
	var wg sync.WaitGroup
	for i, url := range hangingURLs {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			repoName := fmt.Sprintf("hanging-repo-%d", i+1)
			repo := &configapi.Repository{
				TypeMeta: metav1.TypeMeta{
					Kind:       configapi.TypeRepository.Kind,
					APIVersion: configapi.TypeRepository.APIVersion(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      repoName,
					Namespace: t.Namespace,
				},
				Spec: configapi.RepositorySpec{
					Description: "Hanging repo for blocking test",
					Type:        configapi.RepositoryTypeGit,
					Git: &configapi.GitRepository{
						Repo: url,
					},
				},
			}
			t.CreateF(repo)
			t.Cleanup(func() {
				// Force delete hanging repository by removing finalizers first
				var hangingRepo configapi.Repository
				if err := t.Client.Get(t.GetContext(), client.ObjectKey{Name: repoName, Namespace: t.Namespace}, &hangingRepo); err == nil {
					if len(hangingRepo.Finalizers) > 0 {
						hangingRepo.Finalizers = nil
						t.Client.Update(t.GetContext(), &hangingRepo)
					}
					t.Client.Delete(t.GetContext(), &hangingRepo)
					t.WaitUntilRepositoryDeleted(repoName, t.Namespace)
				}
			})
		}(i, url)
	}
	wg.Wait()

	found := false
	for i := range 5 {
		var list porchapi.PackageRevisionList
		// include spec.repository selector to make sure we don't time out trying to list the hanging repositories as well
		t.ListF(&list, client.InNamespace(t.Namespace), client.MatchingFields{"spec.repository": workingRepoName})

		for _, item := range list.Items {
			t.Logf("Found PackageRevision: %s (repo: %s)", item.Name, item.Spec.RepositoryName)
			if item.Spec.RepositoryName == workingRepoName {
				found = true
				break
			}
		}
		if found {
			break
		}
		t.Logf("Retry %d: PackageRevision from working repo not found yet", i+1)
		time.Sleep(2 * time.Second)
	}

	if !found {
		t.Errorf("Expected PackageRevisions from working repository, got none")
	}
}

func (t *PorchSuite) TestRepositoryMutability() {
	const repositoryName = "mutability-test-repo"

	// Create initial repository
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repositoryName, "test-dir", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	key := client.ObjectKey{Namespace: t.Namespace, Name: repositoryName}

	var repo configapi.Repository
	t.GetF(key, &repo)

	originalBranch := repo.Spec.Git.Branch
	originalDir := repo.Spec.Git.Directory
	originalType := repo.Spec.Type

	// tryUpdate re-reads the latest Repository, applies mutate, and updates,
	// retrying on optimistic-concurrency conflicts. The repository controller
	// writes status/finalizers concurrently, so a bare read-then-update races
	// and can surface a 409 Conflict instead of the CEL validation result we
	// want to assert on. RetryOnConflict only retries genuine conflicts; a CEL
	// "immutable" rejection is returned immediately.
	tryUpdate := func(mutate func(r *configapi.Repository)) error {
		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var latest configapi.Repository
			if err := t.Client.Get(t.GetContext(), key, &latest); err != nil {
				return err
			}
			mutate(&latest)
			return t.Client.Update(t.GetContext(), &latest)
		})
	}

	// assertImmutable updates an immutable field and expects a CEL rejection.
	assertImmutable := func(field string, mutate func(r *configapi.Repository)) {
		err := tryUpdate(mutate)
		if err == nil {
			t.Errorf("Expected CEL validation error when modifying %s, but update succeeded", field)
		} else if apierrors.IsConflict(err) {
			t.Errorf("Expected CEL immutability error for %s, but got a conflict after retries: %v", field, err)
		} else if !strings.Contains(err.Error(), "immutable") {
			t.Errorf("Expected CEL immutability error for %s, got: %v", field, err)
		} else {
			t.Logf("Correctly rejected modification of %s: %v", field, err)
		}
	}

	// Test 1: spec.git.repo is now mutable (repository rehoming). The URL change
	// must be accepted by the API server's CEL validation.
	newRepoURL := "https://github.com/MyOrganization/blueprints.git"
	if err := tryUpdate(func(r *configapi.Repository) { r.Spec.Git.Repo = newRepoURL }); err != nil {
		t.Errorf("Expected spec.git.repo to be mutable, but update was rejected: %v", err)
	} else {
		t.Logf("Correctly accepted modification of spec.git.repo")
	}

	t.GetF(key, &repo)
	if repo.Spec.Git.Repo != newRepoURL {
		t.Errorf("spec.git.repo was not updated; got %q, want %q", repo.Spec.Git.Repo, newRepoURL)
	}

	// Test 2: spec.git.branch is immutable.
	assertImmutable("spec.git.branch", func(r *configapi.Repository) { r.Spec.Git.Branch = "different-branch" })
	t.GetF(key, &repo)
	if repo.Spec.Git.Branch != originalBranch {
		t.Errorf("spec.git.branch was modified despite CEL validation; got %q, want %q", repo.Spec.Git.Branch, originalBranch)
	}

	// Test 3: spec.git.directory is immutable.
	assertImmutable("spec.git.directory", func(r *configapi.Repository) { r.Spec.Git.Directory = "different-dir" })
	t.GetF(key, &repo)
	if repo.Spec.Git.Directory != originalDir {
		t.Errorf("spec.git.directory was modified despite CEL validation; got %q, want %q", repo.Spec.Git.Directory, originalDir)
	}

	// Test 4: spec.type is immutable.
	assertImmutable("spec.type", func(r *configapi.Repository) { r.Spec.Type = configapi.RepositoryTypeOCI })
	t.GetF(key, &repo)
	if repo.Spec.Type != originalType {
		t.Errorf("spec.type was modified despite CEL validation; got %q, want %q", repo.Spec.Type, originalType)
	}

	// Test 5: mutable fields can still be updated (description).
	const newDescription = "Updated description for mutability test"
	if err := tryUpdate(func(r *configapi.Repository) { r.Spec.Description = newDescription }); err != nil {
		t.Errorf("Failed to update mutable field (description): %v", err)
	} else {
		t.Logf("Successfully updated mutable field (description)")
	}

	t.GetF(key, &repo)
	if repo.Spec.Description != newDescription {
		t.Errorf("Mutable field (description) was not updated; got %q", repo.Spec.Description)
	}
}

func (t *PorchSuite) TestRepositoryBranchDefault() {
	const repositoryName = "branch-default-test"

	// Create repository without specifying branch
	repo := &configapi.Repository{
		TypeMeta: metav1.TypeMeta{
			Kind:       configapi.TypeRepository.Kind,
			APIVersion: configapi.TypeRepository.APIVersion(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      repositoryName,
			Namespace: t.Namespace,
		},
		Spec: configapi.RepositorySpec{
			Type: configapi.RepositoryTypeGit,
			Git: &configapi.GitRepository{
				Repo: t.GetPorchTestRepoURL(),
				// Branch intentionally omitted to test default
			},
		},
	}

	t.CreateF(repo)
	t.Cleanup(func() {
		t.DeleteL(&configapi.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      repositoryName,
				Namespace: t.Namespace,
			},
		})
	})

	// Verify branch defaults to "main"
	var createdRepo configapi.Repository
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repositoryName}, &createdRepo)

	if createdRepo.Spec.Git.Branch != "main" {
		t.Errorf("Branch did not default to 'main'; got %q", createdRepo.Spec.Git.Branch)
	} else {
		t.Logf("Branch correctly defaulted to 'main'")
	}
}
