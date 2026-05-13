// Copyright 2022-2026 The kpt Authors
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

package gitserver

import (
	"archive/tar"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	gogit "github.com/go-git/go-git/v5"
)

func OpenGitRepositoryFromArchive(t *testing.T, tarfile, tempdir string) *gogit.Repository {
	t.Helper()

	ExtractTar(t, tarfile, tempdir)

	git, err := gogit.PlainOpen(filepath.Join(tempdir, ".git"))
	if err != nil {
		t.Fatalf("Failed to open Git Repository extracted from %q: %v", tarfile, err)
	}

	return git
}

func OpenGitRepositoryFromArchiveWithWorktree(t *testing.T, tarfile, path string) *gogit.Repository {
	t.Helper()

	ExtractTar(t, tarfile, path)

	repo, err := gogit.PlainOpen(path)
	if err != nil {
		t.Fatalf("Failed to open Git repository extracted from %q: %v", tarfile, err)
	}
	return repo
}

func ServeGitRepository(t *testing.T, tarfile, tempdir string) (*gogit.Repository, string) {
	t.Helper()

	git := OpenGitRepositoryFromArchive(t, tarfile, tempdir)
	return git, ServeExistingRepository(t, git)
}

func ServeExistingRepository(t *testing.T, repo *gogit.Repository) string {
	t.Helper()

	r, err := NewRepo(repo)
	if err != nil {
		t.Fatalf("NewRepo failed: %v", err)
	}

	key := "default"

	repos := NewStaticRepos()
	if err := repos.Add(key, r); err != nil {
		t.Fatalf("repos.Add failed: %v", err)
	}

	server, err := NewGitServer(repos)
	if err != nil {
		t.Fatalf("NewGitServer() failed: %v", err)
	}

	var wg sync.WaitGroup

	serverAddressChannel := make(chan net.Addr)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := server.ListenAndServe(ctx, "127.0.0.1:0", serverAddressChannel); err != nil {
			if ctx.Err() == nil {
				t.Errorf("Git Server ListenAndServe failed: %v", err)
			}
		}
	}()

	address, ok := <-serverAddressChannel
	if !ok {
		t.Fatalf("Git Server failed to start")
	}
	return "http://" + address.String() + "/" + key
}

func ExtractTar(t *testing.T, tarfile string, dir string) {
	t.Helper()

	reader, err := os.Open(tarfile) // #nosec G304
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", tarfile, err)
	}
	defer reader.Close()
	tr := tar.NewReader(reader)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Reading tar file %q failed: %v", tarfile, err)
		}
		if hdr.FileInfo().IsDir() {
			// #nosec G305
			path := filepath.Join(dir, hdr.Name)
			// #nosec G301 G703
			if err := os.MkdirAll(path, 0755); err != nil {
				t.Fatalf("MkdirAll(%q) failed: %v", path, err)
			}
			continue
		}
		path := filepath.Join(dir, filepath.Dir(hdr.Name))
		// #nosec G301 G703
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatalf("MkdirAll(%q) failed: %v", path, err)
		}
		// #nosec G305
		path = filepath.Join(dir, hdr.Name)
		saveToFile(t, path, tr)
	}
}

func saveToFile(t *testing.T, path string, src io.Reader) {
	t.Helper()

	dst, err := os.Create(path) // #nosec G304 G703
	if err != nil {
		t.Fatalf("Create(%q) failed; %v", path, err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatalf("Copy from tar to %q failed: %v", path, err)
	}
}
