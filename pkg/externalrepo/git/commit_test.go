// Copyright 2022 The kpt Authors
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

package git

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/kptdev/porch/pkg/repository"
)

func TestPackageCommitEmptyRepo(t *testing.T) {
	tempdir := t.TempDir()
	gitRepo := OpenGitRepositoryFromArchive(t, filepath.Join("testdata", "empty-repository.tar"), tempdir)

	ctx := context.Background()

	var userInfoProvider repository.UserInfoProvider
	parent := plumbing.ZeroHash      // Empty repository
	packageTree := plumbing.ZeroHash // Empty package
	packagePath := "catalog/namespaces/istions"
	ch, err := newCommitHelper(gitRepo, userInfoProvider, parent, packagePath, packageTree)
	if err != nil {
		t.Fatalf("newCommitHelper(%q) failed: %v", packagePath, err)
	}

	filePath := path.Join(packagePath, "hello.txt")
	fileContents := "Hello, World!"
	if err := ch.storeFile(filePath, fileContents); err != nil {
		t.Fatalf("storeFile(%q, %q) failed: %v", filePath, fileContents, err)
	}

	message := fmt.Sprintf("Commit Message: %d", time.Now().UnixMicro())
	commitHash, treeHash, err := ch.commit(ctx, message, packagePath)
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	if commitHash.IsZero() {
		t.Errorf("Commit returned zero commit hash")
	}
	if treeHash.IsZero() {
		t.Errorf("Commit returned zero package tree hash")
	}

	commit := getCommitObject(t, gitRepo, commitHash)
	if got, want := commit.Message, message; got != want {
		t.Errorf("Commit message: got %q, want %q", got, want)
	}
	root, err := commit.Tree()
	if err != nil {
		t.Fatalf("Failed to get tree from commit %q: %v", commitHash, err)
	}
	entry := findTreeEntry(t, root, packagePath)
	if got, want := entry.Hash, treeHash; got != want {
		t.Errorf("Packag tree hash: got %s, want %s", got, want)
	}
	file := findFile(t, root, filePath)
	got, err := file.Contents()
	if err != nil {
		t.Fatalf("Failed to read contents of file %q under the root commit tree %q: %v", filePath, root.Hash, err)
	}
	if want := fileContents; got != want {
		t.Errorf("File contents: got %q, want %q", got, want)
	}
}

func TestPackageCommitToMain(t *testing.T) {
	tempdir := t.TempDir()
	gitRepo := OpenGitRepositoryFromArchive(t, filepath.Join("testdata", "drafts-repository.tar"), tempdir)

	ctx := context.Background()

	var userInfoProvider repository.UserInfoProvider

	// Commit `bucket`` package from drafts/bucket/v1 into main

	main := resolveReference(t, gitRepo, defaultMainReferenceName)
	packagePath := "bucket"

	// Confirm no 'bucket' package in main
	mainRoot := getCommitTree(t, gitRepo, main.Hash())
	{
		entry, err := mainRoot.FindEntry(packagePath)
		if entry != nil || err != object.ErrEntryNotFound {
			t.Fatalf("Unexpectedly found %q package in main branch: %v, %v", packagePath, entry, err)
		}
	}
	draft := resolveReference(t, gitRepo, plumbing.NewBranchReferenceName("drafts/bucket/v1"))
	draftTree := getCommitTree(t, gitRepo, draft.Hash())
	bucketEntry := findTreeEntry(t, draftTree, packagePath)
	bucketTree := bucketEntry.Hash
	ch, err := newCommitHelper(gitRepo, userInfoProvider, main.Hash(), packagePath, bucketTree)
	if err != nil {
		t.Fatalf("Failed to create commit helper: %v", err)
	}

	commitHash, treeHash, err := ch.commit(ctx, "Move bucket to main", packagePath)
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	if commitHash.IsZero() {
		t.Errorf("Commit returned zero commit hash")
	}
	if treeHash.IsZero() {
		t.Errorf("Commit returned zero package tree hash")
	}

	commitTree := getCommitTree(t, gitRepo, commitHash)
	packageEntry := findTreeEntry(t, commitTree, packagePath)
	if got, want := packageEntry.Hash, bucketTree; got != want {
		t.Errorf("Package copied into main branch with unexpected tree hash; got %s, want %s", got, want)
	}
}

type testUserInfoProvider struct {
	userInfo *repository.UserInfo
}

func (p *testUserInfoProvider) GetUserInfo(ctx context.Context) *repository.UserInfo {
	return p.userInfo
}

func TestCommitWithUser(t *testing.T) {
	tempdir := t.TempDir()
	gitRepo := OpenGitRepositoryFromArchive(t, filepath.Join("testdata", "trivial-repository.tar"), tempdir)

	ctx := context.Background()
	main := resolveReference(t, gitRepo, defaultMainReferenceName)

	{
		const testEmail = "porch-test@porch-domain.com"
		const testName = "Porch Test"
		// Make one commit with user info provided
		userInfoProvider := &testUserInfoProvider{
			userInfo: &repository.UserInfo{
				Name:  testName,
				Email: testEmail,
			},
		}

		var zeroHash plumbing.Hash
		const packagePath = "testpackage"
		ch, err := newCommitHelper(gitRepo, userInfoProvider, main.Hash(), packagePath, zeroHash)
		if err != nil {
			t.Fatalf("newCommitHelper(%q) failed: %v", packagePath, err)
		}

		filePath := path.Join(packagePath, "hello.txt")
		fileContents := "Hello, World!"
		if err := ch.storeFile(filePath, fileContents); err != nil {
			t.Fatalf("storeFile(%q, %q) failed: %v", filePath, fileContents, err)
		}

		message := fmt.Sprintf("Commit Message: %d", time.Now().UnixMicro())
		commitHash, _, err := ch.commit(ctx, message, packagePath)
		if err != nil {
			t.Fatalf("commit failed: %v", err)
		}

		commit := getCommitObject(t, gitRepo, commitHash)

		if got, want := commit.Author.Email, testEmail; got != want {
			t.Errorf("Commit.Author.Email: got %q, want %q", got, want)
		}
		if got, want := commit.Author.Name, testName; got != want {
			t.Errorf("Commit.Author.Name: got %q, want %q", got, want)
		}

		// Committer is Porch
		if got, want := commit.Committer.Email, porchSignatureEmail; got != want {
			t.Errorf("Commit.Author.Email: got %q, want %q", got, want)
		}
		if got, want := commit.Committer.Name, porchSignatureName; got != want {
			t.Errorf("Commit.Author.Name: got %q, want %q", got, want)
		}
	}

	{
		// And another without ...
		userInfoProvider := &testUserInfoProvider{
			userInfo: nil,
		}

		var zeroHash plumbing.Hash
		const packagePath = "testpackage-nouser"
		ch, err := newCommitHelper(gitRepo, userInfoProvider, main.Hash(), packagePath, zeroHash)
		if err != nil {
			t.Fatalf("newCommitHelper(%q) failed: %v", packagePath, err)
		}

		filePath := path.Join(packagePath, "hello-nouser.txt")
		fileContents := "Hello, World!"
		if err := ch.storeFile(filePath, fileContents); err != nil {
			t.Fatalf("storeFile(%q, %q) failed: %v", filePath, fileContents, err)
		}

		message := fmt.Sprintf("Commit Message: %d", time.Now().UnixMicro())
		commitHash, _, err := ch.commit(ctx, message, packagePath)
		if err != nil {
			t.Fatalf("commit failed: %v", err)
		}

		commit := getCommitObject(t, gitRepo, commitHash)

		if got, want := commit.Author.Email, porchSignatureEmail; got != want {
			t.Errorf("Commit.Author.Email: got %q, want %q", got, want)
		}
		if got, want := commit.Author.Name, porchSignatureName; got != want {
			t.Errorf("Commit.Author.Name: got %q, want %q", got, want)
		}

		// Committer is Porch
		if got, want := commit.Committer.Email, porchSignatureEmail; got != want {
			t.Errorf("Commit.Author.Email: got %q, want %q", got, want)
		}
		if got, want := commit.Committer.Name, porchSignatureName; got != want {
			t.Errorf("Commit.Author.Name: got %q, want %q", got, want)
		}

		// Check the message
		if got, want := commit.Message, message; got != want {
			t.Errorf("Commit.Message: got %q, want %q", got, want)
		}
	}
}

func createPackageCommit(t *testing.T, repo *gogit.Repository, parentHash plumbing.Hash, packagePath string) plumbing.Hash {
	t.Helper()
	ch, err := newCommitHelper(repo, nil, parentHash, packagePath, plumbing.ZeroHash)
	if err != nil {
		t.Fatalf("newCommitHelper(%q) failed: %v", packagePath, err)
	}
	if err := ch.storeFile(path.Join(packagePath, "Kptfile"), "apiVersion: kpt.dev/v1"); err != nil {
		t.Fatalf("storeFile failed: %v", err)
	}
	hash, _, err := ch.commit(context.Background(), fmt.Sprintf("Create %s", packagePath), packagePath)
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	return hash
}

func deletePackageCommit(t *testing.T, repo *gogit.Repository, parentHash plumbing.Hash, packagePath string) plumbing.Hash {
	t.Helper()
	ch, err := newCommitHelper(repo, nil, parentHash, packagePath, plumbing.ZeroHash)
	if err != nil {
		t.Fatalf("newCommitHelper(%q) for deletion failed: %v", packagePath, err)
	}
	hash, _, err := ch.commit(context.Background(), fmt.Sprintf("Delete %s", packagePath), packagePath)
	if err != nil {
		t.Fatalf("delete commit failed: %v", err)
	}
	return hash
}

func TestStoreTreesPrunesEmptySubdirectory(t *testing.T) {
	tempdir := t.TempDir()
	gitRepo := OpenGitRepositoryFromArchive(t, filepath.Join("testdata", "empty-repository.tar"), tempdir)

	createHash := createPackageCommit(t, gitRepo, plumbing.ZeroHash, "subdir/testpkg")
	deleteHash := deletePackageCommit(t, gitRepo, createHash, "subdir/testpkg")

	root := getCommitTree(t, gitRepo, deleteHash)
	for _, entry := range root.Entries {
		if entry.Name == "subdir" {
			t.Errorf("expected 'subdir' to be pruned from root tree, but it still exists")
		}
	}
}

func TestStoreTreesPrunesNestedEmptySubdirectories(t *testing.T) {
	tempdir := t.TempDir()
	gitRepo := OpenGitRepositoryFromArchive(t, filepath.Join("testdata", "empty-repository.tar"), tempdir)

	createHash := createPackageCommit(t, gitRepo, plumbing.ZeroHash, "level1/level2/testpkg")
	deleteHash := deletePackageCommit(t, gitRepo, createHash, "level1/level2/testpkg")

	root := getCommitTree(t, gitRepo, deleteHash)
	for _, entry := range root.Entries {
		if entry.Name == "level1" {
			t.Errorf("expected 'level1' to be pruned from root tree, but it still exists")
		}
	}
}

func TestStoreTreesRetainsNonEmptySubdirectory(t *testing.T) {
	tempdir := t.TempDir()
	gitRepo := OpenGitRepositoryFromArchive(t, filepath.Join("testdata", "empty-repository.tar"), tempdir)

	commitA := createPackageCommit(t, gitRepo, plumbing.ZeroHash, "subdir/pkg-a")
	commitB := createPackageCommit(t, gitRepo, commitA, "subdir/pkg-b")
	deleteHash := deletePackageCommit(t, gitRepo, commitB, "subdir/pkg-a")

	root := getCommitTree(t, gitRepo, deleteHash)
	subdirEntry := findTreeEntry(t, root, "subdir")
	if subdirEntry == nil {
		t.Fatalf("expected 'subdir' to still exist since pkg-b is still there")
	}

	subdirTree, err := object.GetTree(gitRepo.Storer, subdirEntry.Hash)
	if err != nil {
		t.Fatalf("failed to get subdir tree: %v", err)
	}

	foundPkgB := false
	for _, entry := range subdirTree.Entries {
		if entry.Name == "pkg-a" {
			t.Errorf("expected 'pkg-a' to be deleted but it still exists")
		}
		if entry.Name == "pkg-b" {
			foundPkgB = true
		}
	}
	if !foundPkgB {
		t.Errorf("expected 'pkg-b' to still exist in subdir")
	}
}
