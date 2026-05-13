// Copyright 2022, 2025 The kpt Authors
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
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/kptdev/porch/pkg/repository"
	"k8s.io/klog/v2"
)

// packageList holds a list of packages in the git repository
type packageList struct {
	// parent is the gitRepository of which this is part
	parent *gitRepository

	// commit is the commit at which we scanned for packages
	commit *object.Commit

	// packages holds the packages we found
	packages map[string]*packageListEntry
}

// packageListEntry is a single package found in a git repository
type packageListEntry struct {
	// parent is the packageList of which we are part
	parent *packageList

	// path is the relative path to the root of the package (directory containing the Kptfile)
	pkgKey repository.PackageKey

	// treeHash is the git-hash of the git tree corresponding to Path
	treeHash plumbing.Hash
}

// buildGitPackageRevision creates a gitPackageRevision for the packageListEntry
// TODO: Can packageListEntry just _be_ a gitPackageRevision?
func (p *packageListEntry) buildGitPackageRevision(ctx context.Context, revisionStr, workspace string, ref *plumbing.Reference) (*gitPackageRevision, error) {
	repo := p.parent.parent

	var updated time.Time
	var updatedBy string

	// For the published packages on a tag or draft and proposed branches we know that the latest commit
	// if specific to the package in question. Thus, we can just take the last commit on the tag/branch.
	// If the ref is nil, we consider the package as being final and on the package branch.
	if ref != nil && (isTagInLocalRepo(ref.Name()) || isDraftBranchNameInLocal(ref.Name()) || isProposedBranchNameInLocal(ref.Name())) {
		updated = p.parent.commit.Author.When
		updatedBy = p.parent.commit.Author.Email
	} else {
		// If we are on the package branch, we can not assume that the last commit
		// pertains to the package in question. So we scan the git history to find
		// the last commit for the package based on the porch commit tags. We don't
		// use the revision here, since we are looking at the package branch while
		// the revisions only helps identify the tags.
		commit, err := repo.findLatestPackageCommit(p.parent.commit, p.pkgKey)
		if err != nil && commit != nil {
			klog.Warningf("Error searching for latest package commit for package %s/%s: %s", p.pkgKey, revisionStr, err)
		}
		if commit != nil {
			updated = commit.Author.When
			updatedBy = commit.Author.Email
		} else {
			klog.Warningf("Cannot find latest package commit for package %s/%s: %s", p.pkgKey, revisionStr, err)
		}
		// If not commit was found with the porch commit tags, we don't really
		// know who approved the package or when it happend. We could find this
		// by scanning the tree for every commit, but that is a pretty expensive
		// operation.
	}

	// for backwards compatibility with packages that existed before porch supported
	// workspaceNames, we populate the workspaceName as the branch if it is empty
	// All this is necessary to support old packages and nested packages
	// TODO: Do we need nested packages, we can use directories on repos instead
	revision := repository.Revision2Int(revisionStr)
	if revision != -1 {
		if workspace == "" {
			workspace = "v" + repository.Revision2Str(revision)
		}
	} else {
		workspace = revisionStr
	}

	gitPrKey := repository.PackageRevisionKey{
		PkgKey:        p.pkgKey,
		Revision:      revision,
		WorkspaceName: workspace,
	}

	tasks, err := repo.loadTasks(ctx, p.parent.commit, gitPrKey)
	if err != nil {
		klog.Warningf("Error when loading tasks for package %s: %s", gitPrKey, err)
	}

	if len(tasks) == 0 {
		klog.V(2).Infof("Loaded no tasks for package %s", gitPrKey)
	} else if klog.V(6).Enabled() {
		marshalledTasks, _ := json.Marshal(tasks)
		klog.Infof("Loaded tasks for package %s: %s", gitPrKey, marshalledTasks)
	}

	return &gitPackageRevision{
		prKey:     gitPrKey,
		repo:      repo,
		updated:   updated,
		updatedBy: updatedBy,
		ref:       ref,
		tree:      p.treeHash,
		commit:    p.parent.commit.Hash,
		tasks:     tasks,
	}, nil
}

// discoverPackagesOptions holds the configuration for walking a git tree
type discoverPackagesOptions struct {
	// filterPrefix restricts package discovery to a particular subdirectory.
	// The subdirectory is not required to exist (we will return an empty list of packages).
	filterPrefix string

	// recurse enables recursive traversal of the git tree.
	recurse bool
}

// discoverPackages is the recursive function we use to traverse the tree and find packages.
// tree is the git-tree we are search, treePath is the repo-relative-path to tree.
func (t *packageList) discoverPackages(repoKey repository.RepositoryKey, tree *object.Tree, treePath string, recurse bool, repo *git.Repository) error {
	for _, e := range tree.Entries {
		if e.Name == "Kptfile" {
			p := path.Join(treePath, e.Name)
			if !e.Mode.IsFile() {
				klog.Warningf("skipping %q: Kptfile is not a file", p)
				continue
			}

			// Found a package
			t.packages[treePath] = &packageListEntry{
				// When discovering packages the absolute path of the package is needed in relation to the root of the repo
				pkgKey:   repository.FromFullPathname(repoKey, strings.TrimPrefix(treePath, repoKey.Path)),
				treeHash: tree.Hash,
				parent:   t,
			}
			recurse = false
			break
		}
	}

	if recurse {
		for _, e := range tree.Entries {
			if e.Mode != filemode.Dir {
				continue
			}

			dirTree, err := repo.TreeObject(e.Hash)
			if err != nil {
				return fmt.Errorf("error getting git tree %v: %w", e.Hash, err)
			}

			if err := t.discoverPackages(repoKey, dirTree, path.Join(treePath, e.Name), recurse, repo); err != nil {
				return err
			}
		}
	}

	return nil
}
