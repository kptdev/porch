// Copyright 2022, 2024-2025 The kpt Authors
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

package task

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo/fake"
	"github.com/kptdev/porch/pkg/repository"
	gitserver "github.com/kptdev/porch/test/git/pkg"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

var (
	repositoryNamespace    = "test-namespace"
	repositoryName         = "repo"
	pkg                    = "1234567890"
	revision               = -1
	workspace              = "ws"
	packageName            = "repo.1234567890.ws"
	placeholderPackageName = "repo.1234567890.main"
	packageRevision        = &fake.FakePackageRevision{
		PrKey: repository.PackageRevisionKey{
			PkgKey: repository.PackageKey{
				RepoKey: repository.RepositoryKey{
					Namespace: repositoryNamespace,
					Name:      repositoryName,
				},
				Package: pkg,
			},
			Revision:      revision,
			WorkspaceName: workspace,
		},
		PackageLifecycle: porchapi.PackageRevisionLifecyclePublished,
		Resources: &porchapi.PackageRevisionResources{
			Spec: porchapi.PackageRevisionResourcesSpec{
				PackageName:    pkg,
				Revision:       revision,
				RepositoryName: repositoryName,
				Resources: map[string]string{
					kptfilev1.KptFileName: strings.TrimSpace(`
apiVersion: kpt.dev/v1
kind: Kptfile
metadata:
  name: example
  annotations:
    config.kubernetes.io/local-config: "true"
info:
  description: sample description
					`),
				},
			},
		},
		Kptfile: kptfilev1.KptFile{
			Upstream: &kptfilev1.Upstream{
				Type: "git",
				Git: &kptfilev1.Git{
					Repo:      "dummy",
					Directory: "dummy",
					Ref:       "dummy",
				},
			},
			UpstreamLock: &kptfilev1.UpstreamLock{
				Type: "git",
				Git: &kptfilev1.GitLock{
					Repo:      "dummy",
					Directory: "dummy",
					Ref:       "dummy",
					Commit:    "dummy",
				},
			},
		},
	}
	repo = &fake.Repository{
		PackageRevisions: []repository.PackageRevision{
			packageRevision,
		},
	}
	repoOpener = &fakeRepositoryOpener{
		repository: repo,
	}
)

func createRepoWithContents(t *testing.T, contentDir string) *gogit.Repository {
	repo, err := gogit.Init(memory.NewStorage(), memfs.New())
	if err != nil {
		t.Fatalf("Failed to initialize in-memory git repository: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Failed to get git repository worktree: %v", err)
	}

	if err := filepath.Walk(contentDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("irregular file object detected: %q (%s)", path, info.Mode())
		}
		rel, err := filepath.Rel(contentDir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path from %q to %q: %w", contentDir, path, err)
		}
		dir := filepath.Dir(rel)
		if err := wt.Filesystem.MkdirAll(dir, 0777); err != nil {
			return fmt.Errorf("failed to create directories for %q: %w", rel, err)
		}
		src, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open the source file %q: %w", path, err)
		}
		defer src.Close()
		dst, err := wt.Filesystem.Create(rel)
		if err != nil {
			return fmt.Errorf("failed to create the destination file %q: %w", rel, err)
		}
		defer dst.Close()
		if _, err := io.Copy(dst, src); err != nil {
			return fmt.Errorf("failed to copy file contents %q -> %q: %w", path, rel, err)
		}
		//nolint:errcheck
		wt.Add(rel)
		return nil
	}); err != nil {
		t.Fatalf("Failed populating Git repository worktree: %v", err)
	}

	sig := object.Signature{
		Name:  "Porch Unit Test",
		Email: "porch-unit-test@kpt.dev",
		When:  time.Now(),
	}

	hash, err := wt.Commit("Initial Commit", &gogit.CommitOptions{
		All:       true,
		Author:    &sig,
		Committer: &sig,
	})
	if err != nil {
		t.Fatalf("Failed creating initial commit: %v", err)
	}

	main := plumbing.NewHashReference(plumbing.ReferenceName("refs/heads/main"), hash)
	if err := repo.Storer.SetReference(main); err != nil {
		t.Fatalf("Failed to set refs/heads/main to commit sha %s: %v", hash, err)
	}
	head := plumbing.NewSymbolicReference(plumbing.HEAD, "refs/heads/main")
	if err := repo.Storer.SetReference(head); err != nil {
		t.Fatalf("Failed to set HEAD to refs/heads/main: %v", err)
	}

	_ = repo.Storer.RemoveReference(plumbing.Master)

	return repo
}

func startGitServer(t *testing.T, repo *gitserver.Repo, _ ...gitserver.GitServerOption) string {
	key := "default"
	repos := gitserver.NewStaticRepos()
	if err := repos.Add(key, repo); err != nil {
		t.Fatalf("repos.Add failed: %v", err)
	}

	server, err := gitserver.NewGitServer(repos)
	if err != nil {
		t.Fatalf("Failed to create git server: %v", err)
	}

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	addressChannel := make(chan net.Addr)

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := server.ListenAndServe(ctx, "127.0.0.1:0", addressChannel)
		if err != nil {
			if err == http.ErrServerClosed {
				t.Log("Git server shut down successfully")
			} else {
				t.Errorf("Git server exited with error: %v", err)
			}
		}
	}()

	// Wait for server to start up
	address, ok := <-addressChannel
	if !ok {
		t.Fatalf("Server failed to start")
		return ""
	}

	return fmt.Sprintf("http://%s/%s", address, key)
}

// TODO(mortent): See if we can restructure the packages to
// avoid having to create separate implementations of the auth
// interfaces here.
type credentialResolver struct {
	username, password string
}

type credential struct {
	username, password string
}

// ToString implements repository.Credential.
func (c *credential) ToString() string {
	panic("unimplemented")
}

func (c *credential) Valid() bool {
	return true
}

func (c *credential) ToAuthMethod() transport.AuthMethod {
	return &githttp.BasicAuth{
		Username: c.username,
		Password: c.password,
	}
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, n)
	for i := range result {
		result[i] = letters[rand.Intn(len(letters))]
	}
	return string(result)
}

func randomCredentials() *credentialResolver {
	return &credentialResolver{
		username: randomString(30),
		password: randomString(30),
	}
}

func (r *credentialResolver) ResolveCredential(ctx context.Context, namespace, name string) (repository.Credential, error) {
	return &credential{
		username: r.username,
		password: r.password,
	}, nil
}

func TestCloneGitBasicAuth(t *testing.T) {
	testdata, err := filepath.Abs(filepath.Join(".", "testdata", "clone"))
	if err != nil {
		t.Fatalf("Failed to find testdata: %+v", err)
	}

	auth := randomCredentials()
	gogitRepo := createRepoWithContents(t, testdata)

	repo, err := gitserver.NewRepo(gogitRepo, gitserver.WithBasicAuth(auth.username, auth.password))
	if err != nil {
		t.Fatalf("NewRepo failed: %+v", err)
	}

	addr := startGitServer(t, repo)

	cpm := clonePackageMutation{
		task: &porchapi.Task{
			Type: "clone",
			Clone: &porchapi.PackageCloneTaskSpec{
				Upstream: porchapi.UpstreamPackage{
					Type: "git",
					Git: &porchapi.GitPackage{
						Repo:      addr,
						Ref:       "main",
						Directory: "configmap",
						SecretRef: porchapi.SecretRef{
							Name: "git-credentials",
						},
					},
				},
			},
		},
		namespace:                  "test-namespace",
		name:                       "test-configmap",
		repoOperationRetryAttempts: 3,
		credentialResolver: &credentialResolver{
			username: "",
			password: "",
		},
	}

	_, _, err = cpm.apply(context.Background(), repository.PackageResources{})
	if err == nil {
		t.Errorf("Expected error (unauthorized); got none")
	}

	cpm.credentialResolver = auth
	cpm.repoOperationRetryAttempts = 3

	r, _, err := cpm.apply(context.Background(), repository.PackageResources{})
	if err != nil {
		t.Errorf("task apply failed: %+v", err)
	}

	t.Logf("%v", r)
}

func TestCloneReferenceBasicAuth(t *testing.T) {
	testdata, err := filepath.Abs(filepath.Join(".", "testdata", "clone"))
	if err != nil {
		t.Fatalf("Failed to find testdata: %+v", err)
	}

	auth := randomCredentials()
	gogitRepo := createRepoWithContents(t, testdata)

	repo, err := gitserver.NewRepo(gogitRepo, gitserver.WithBasicAuth(auth.username, auth.password))
	if err != nil {
		t.Fatalf("NewRepo failed: %+v", err)
	}

	addr := startGitServer(t, repo)
	res := &fakeReferenceResolver{address: addr}

	cpm := clonePackageMutation{
		task: &porchapi.Task{
			Type: "clone",
			Clone: &porchapi.PackageCloneTaskSpec{
				Upstream: porchapi.UpstreamPackage{
					UpstreamRef: &porchapi.PackageRevisionRef{Name: packageName},
				},
			},
		},
		namespace:                  repositoryNamespace,
		name:                       "test-configmap",
		repoOperationRetryAttempts: 3,
		credentialResolver:         auth,
		referenceResolver:          res,
		repoOpener:                 repoOpener,
	}

	_, _, err = cpm.apply(context.Background(), repository.PackageResources{})
	assert.NoErrorf(t, err, "task apply failed: %+v", err)

	cpm.referenceResolver = (&errorAfterNReferenceResolver{r: res, err: errors.New("error resolving reference")}).startAt(1)

	_, _, err = cpm.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "failed to resolve repository reference")

	cpm.referenceResolver = res
	cpm.task.Clone.Upstream.UpstreamRef.Name = placeholderPackageName
	origWorkspaceName := packageRevision.PrKey.WorkspaceName
	packageRevision.PrKey.WorkspaceName = "main"
	t.Cleanup(func() {
		packageRevision.PrKey.WorkspaceName = origWorkspaceName
	})

	_, _, err = cpm.apply(context.Background(), repository.PackageResources{})
	assert.ErrorContains(t, err, "placeholder package revision", "Expected error cloning from the placeholder package revision")
}
