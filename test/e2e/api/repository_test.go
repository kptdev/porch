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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func (t *PorchSuite) TestRepositoryImmutability() {
	const repositoryName = "immutable-test-repo"

	// Create initial repository
	t.RegisterGitRepositoryF(t.GetPorchTestRepoURL(), repositoryName, "test-dir", suiteutils.GiteaUser, suiteutils.GiteaPassword)

	var repo configapi.Repository
	t.GetF(client.ObjectKey{
		Namespace: t.Namespace,
		Name:      repositoryName,
	}, &repo)

	originalRepo := repo.Spec.Git.Repo
	originalBranch := repo.Spec.Git.Branch
	originalDir := repo.Spec.Git.Directory
	originalType := repo.Spec.Type

	// Test 1: Attempt to modify spec.git.repo (should fail with CEL validation error)
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repositoryName}, &repo)
	repo.Spec.Git.Repo = "https://different-repo.git"
	err := t.Client.Update(t.GetContext(), &repo)
	if err == nil {
		t.Errorf("Expected CEL validation error when modifying spec.git.repo, but update succeeded")
	} else if !strings.Contains(err.Error(), "immutable") {
		t.Errorf("Expected CEL immutability error for spec.git.repo, got: %v", err)
	} else {
		t.Logf("Correctly rejected modification of spec.git.repo: %v", err)
	}

	// Verify field wasn't changed
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repositoryName}, &repo)
	if repo.Spec.Git.Repo != originalRepo {
		t.Errorf("spec.git.repo was modified despite CEL validation; got %q, want %q", repo.Spec.Git.Repo, originalRepo)
	}

	// Test 2: Attempt to modify spec.git.branch (should fail with CEL validation error)
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repositoryName}, &repo)
	repo.Spec.Git.Branch = "different-branch"
	err = t.Client.Update(t.GetContext(), &repo)
	if err == nil {
		t.Errorf("Expected CEL validation error when modifying spec.git.branch, but update succeeded")
	} else if !strings.Contains(err.Error(), "immutable") {
		t.Errorf("Expected CEL immutability error for spec.git.branch, got: %v", err)
	} else {
		t.Logf("Correctly rejected modification of spec.git.branch: %v", err)
	}

	// Verify field wasn't changed
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repositoryName}, &repo)
	if repo.Spec.Git.Branch != originalBranch {
		t.Errorf("spec.git.branch was modified despite CEL validation; got %q, want %q", repo.Spec.Git.Branch, originalBranch)
	}

	// Test 3: Attempt to modify spec.git.directory (should fail with CEL validation error)
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repositoryName}, &repo)
	repo.Spec.Git.Directory = "different-dir"
	err = t.Client.Update(t.GetContext(), &repo)
	if err == nil {
		t.Errorf("Expected CEL validation error when modifying spec.git.directory, but update succeeded")
	} else if !strings.Contains(err.Error(), "immutable") {
		t.Errorf("Expected CEL immutability error for spec.git.directory, got: %v", err)
	} else {
		t.Logf("Correctly rejected modification of spec.git.directory: %v", err)
	}

	// Verify field wasn't changed
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repositoryName}, &repo)
	if repo.Spec.Git.Directory != originalDir {
		t.Errorf("spec.git.directory was modified despite CEL validation; got %q, want %q", repo.Spec.Git.Directory, originalDir)
	}

	// Test 4: Attempt to modify spec.type (should fail with CEL validation error)
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repositoryName}, &repo)
	repo.Spec.Type = configapi.RepositoryTypeOCI
	err = t.Client.Update(t.GetContext(), &repo)
	if err == nil {
		t.Errorf("Expected CEL validation error when modifying spec.type, but update succeeded")
	} else if !strings.Contains(err.Error(), "immutable") {
		t.Errorf("Expected CEL immutability error for spec.type, got: %v", err)
	} else {
		t.Logf("Correctly rejected modification of spec.type: %v", err)
	}

	// Verify field wasn't changed
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repositoryName}, &repo)
	if repo.Spec.Type != originalType {
		t.Errorf("spec.type was modified despite CEL validation; got %q, want %q", repo.Spec.Type, originalType)
	}

	// Test 5: Verify mutable fields can still be updated (description)
	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repositoryName}, &repo)
	repo.Spec.Description = "Updated description for immutability test"
	err = t.Client.Update(t.GetContext(), &repo)
	if err != nil {
		t.Errorf("Failed to update mutable field (description): %v", err)
	} else {
		t.Logf("Successfully updated mutable field (description)")
	}

	t.GetF(client.ObjectKey{Namespace: t.Namespace, Name: repositoryName}, &repo)
	if repo.Spec.Description != "Updated description for immutability test" {
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
