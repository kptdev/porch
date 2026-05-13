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
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
	pkgerrors "github.com/pkg/errors"
)

// This file contains helpers for interacting with gogit.

func initEmptyRepository(path string) (*git.Repository, error) {
	isBare := true // Porch only uses bare repositories
	repo, err := git.PlainInit(path, isBare)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "gogit: repo plain init failed on path %q", path)
	}
	if err := initializeDefaultBranches(repo); err != nil {
		return nil, pkgerrors.Wrapf(err, "gogit: default branch initialize failed on repo for path %q", path)
	}
	return repo, nil
}

func initializeDefaultBranches(repo *git.Repository) error {
	// Adjust default references
	if err := repo.Storer.RemoveReference(plumbing.Master); err != nil {
		return pkgerrors.Wrapf(err, "gogit: failed to remove reference %+v on repo %+v", plumbing.Master, repo)
	}
	// gogit points HEAD at a wrong branch; point it at main
	main := plumbing.NewSymbolicReference(plumbing.HEAD, defaultMainReferenceName)
	if err := repo.Storer.SetReference(main); err != nil {
		return pkgerrors.Wrapf(err, "gogit: failed to set reference %+v on repo %+v", main, repo)
	}
	return nil
}

func openRepository(path string) (*git.Repository, error) {
	dot := osfs.New(path)
	storage := filesystem.NewStorageWithOptions(dot, cache.NewObjectLRUDefault(), filesystem.Options{
		ExclusiveAccess: true,
	})
	return git.Open(storage, dot)
}

func initializeOrigin(repo *git.Repository, address string) error {
	cfg, err := repo.Config()
	if err != nil {
		return pkgerrors.Wrapf(err, "gogit: failed to get configuration for repo %+v", repo)
	}

	cfg.Remotes[originName] = &config.RemoteConfig{
		Name:  originName,
		URLs:  []string{address},
		Fetch: defaultFetchSpec,
	}

	if err := repo.SetConfig(cfg); err != nil {
		return pkgerrors.Wrapf(err, "gogit: failed to set configuration for repo %+v", repo)
	}

	return nil
}

func storeCommit(repo *git.Repository, commit *object.Commit) (plumbing.Hash, error) {
	eo := repo.Storer.NewEncodedObject()
	if err := commit.Encode(eo); err != nil {
		return plumbing.Hash{}, pkgerrors.Wrapf(err, "gogit: failed to store commit %+v on repo %+v", commit, repo)
	}
	return repo.Storer.SetEncodedObject(eo)
}

func storeTree(repo *git.Repository, tree *object.Tree) (plumbing.Hash, error) {
	eo := repo.Storer.NewEncodedObject()
	if err := tree.Encode(eo); err != nil {
		return plumbing.Hash{}, pkgerrors.Wrapf(err, "gogit: failed to store tree %+v on repo %+v", tree, repo)
	}
	return repo.Storer.SetEncodedObject(eo)
}

func storeBlob(repo *git.Repository, value string) (plumbing.Hash, error) {
	data := []byte(value)
	eo := repo.Storer.NewEncodedObject()
	eo.SetType(plumbing.BlobObject)
	eo.SetSize(int64(len(data)))

	w, err := eo.Writer()
	if err != nil {
		return plumbing.Hash{}, pkgerrors.Wrapf(err, "gogit: failed to get writer for repo %+v", repo)
	}

	if _, err := w.Write(data); err != nil {
		w.Close()
		return plumbing.Hash{}, pkgerrors.Wrapf(err, "gogit: failed to write blob %+v to repo %+v", value, repo)
	}

	if err := w.Close(); err != nil {
		return plumbing.Hash{}, pkgerrors.Wrapf(err, "gogit: failed to close repo %+v", repo)
	}

	return repo.Storer.SetEncodedObject(eo)
}
