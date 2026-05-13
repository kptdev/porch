/*
 Copyright 2025 The kpt Authors

 Licensed under the Apache License, Version 2.0 (the "License");
 You may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package git

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/stretchr/testify/assert"
)

func TestGitRepoFactory(t *testing.T) {

	gf := GitRepoFactory{}

	repoSpec := configapi.Repository{}
	_, err := gf.NewRepositoryImpl(context.TODO(), &repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, strings.Contains(err.Error(), "git property is required"))

	repoSpec.Spec = configapi.RepositorySpec{
		Git: &configapi.GitRepository{},
	}
	_, err = gf.NewRepositoryImpl(context.TODO(), &repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, strings.Contains(err.Error(), "git.repo property is required"))
	assert.True(t, err != nil)

	repoSpec.Spec.Git.Repo = "my-repo"
	repoSpec.Spec.Git.CreateBranch = true
	_, err = gf.NewRepositoryImpl(context.TODO(), &repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, err != nil)

	repoSpec.Spec.Git.Repo = "my-repo"
	repoSpec.Spec.Git.CreateBranch = false
	_, err = gf.NewRepositoryImpl(context.TODO(), &repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, err != nil)
}

func TestCheckRepositoryConnection(t *testing.T) {
	gf := &GitRepoFactory{}

	// nil repositorySpec
	err := gf.CheckRepositoryConnection(context.TODO(), nil, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, err != nil)
	assert.Contains(t, err.Error(), "repositorySpec is nil or missing Git configuration")

	// nil Git field
	repoSpec := &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: nil,
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, err != nil)
	assert.Contains(t, err.Error(), "repositorySpec is nil or missing Git configuration")

	// empty repo URL
	repoSpec.Spec.Git = &configapi.GitRepository{
		Repo:   "",
		Branch: "main",
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.True(t, err != nil)
	assert.Contains(t, err.Error(), "repository URL is empty")

	tempDir := t.TempDir()
	tarfile := filepath.Join("testdata", "trivial-repository.tar")
	branch := "main"

	_, address := ServeGitRepositoryWithBranch(t, tarfile, tempDir, branch)

	// valid branch
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:   address,
				Branch: branch,
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err)

	// empty branch defaults to main
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:   address,
				Branch: "", // empty branch should default to main
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err)

	// branch not found but createBranch is set to true
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:         address,
				Branch:       "nonexistent-branch",
				CreateBranch: true,
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err)

	// branch not found
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:   address,
				Branch: "nonexistent-branch",
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `branch "nonexistent-branch" not found`)

	// empty secretName (no authentication)
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:      address,
				Branch:    branch,
				SecretRef: configapi.SecretRef{Name: ""},
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.NoError(t, err)

	// remote list error
	repoSpec = &configapi.Repository{
		Spec: configapi.RepositorySpec{
			Git: &configapi.GitRepository{
				Repo:   "https://invalid-url.example.com/repo.git",
				Branch: "main",
			},
		},
	}
	err = gf.CheckRepositoryConnection(context.TODO(), repoSpec, externalrepotypes.ExternalRepoOptions{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list remote refs")
}
