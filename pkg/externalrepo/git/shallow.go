// Copyright 2026 The kpt Authors
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
	"io"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/api/util"
	"github.com/kptdev/porch/pkg/repository"
	porchutil "github.com/kptdev/porch/pkg/util"
	pkgerrors "github.com/pkg/errors"
	"k8s.io/klog/v2"
)

type refSnap struct {
	name plumbing.ReferenceName
	hash plumbing.Hash
}

// ListPackageRevisionsShallow builds package revisions from refs and each ref's own commit. It does not walk parent commits.
func (r *gitRepository) ListPackageRevisionsShallow(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	if err := r.fetchRemoteRepositoryWithRetry(ctx); err != nil {
		return nil, err
	}
	snaps, err := r.snapshotRefs()
	if err != nil {
		return nil, err
	}

	var drafts []*gitPackageRevision
	var result []*gitPackageRevision
	var main *refSnap
	mainBranch := r.branch.refInLocal()

	for i := range snaps {
		snap := snaps[i]
		ref := plumbing.NewHashReference(snap.name, snap.hash)
		switch name := snap.name; {
		case name == mainBranch:
			copied := snap
			main = &copied
		case isProposedBranchNameInLocal(name), isDraftBranchNameInLocal(name):
			if !shallowWantsDraft(filter, name) {
				continue
			}
			draft, err := r.loadDraftShallow(ref, filter)
			if err != nil {
				return nil, pkgerrors.Wrapf(err, "failed to load package draft %q", name.String())
			}
			if draft != nil {
				drafts = append(drafts, draft)
			}
		case isTagInLocalRepo(name):
			if !shallowWantsPublished(filter) {
				continue
			}
			tagged, err := r.loadTaggedPackageShallow(ref, filter)
			if err != nil {
				if tagged == nil {
					continue
				}
				klog.Warningf("Error loading tagged package from ref %q: %s", name, err)
			}
			if tagged != nil {
				result = append(result, tagged)
			}
		}
	}

	if main != nil && shallowMayIncludeMain(filter, main.name) {
		mainpkgs, err := r.discoverFinalizedPackagesShallow(plumbing.NewHashReference(main.name, main.hash), filter)
		if err != nil && len(mainpkgs) == 0 {
			return nil, err
		}
		result = append(result, mainpkgs...)
	}
	result = append(result, drafts...)

	out := make([]repository.PackageRevision, 0, len(result))
	for _, p := range result {
		if filter.Matches(ctx, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *gitRepository) MaxPublishedRevision(ctx context.Context, pkg key.PackageKey) (int, error) {
	snaps, err := r.snapshotRefs()
	if err != nil {
		return 0, err
	}
	want := pkg.ToFullPathname()
	maxRev := 0
	for _, snap := range snaps {
		if !isTagInLocalRepo(snap.name) {
			continue
		}
		name, ok := getTagNameInLocalRepo(snap.name)
		if !ok {
			continue
		}
		path, revStr, ok := splitTagPath(name)
		if !ok || path != want {
			continue
		}
		if rev := util.Revision2Int(revStr); rev > maxRev {
			maxRev = rev
		}
	}
	return maxRev, nil
}

// FindPackageRevisionByKey reads at most the draft, proposed, and tag refs for one key.
func (r *gitRepository) FindPackageRevisionByKey(ctx context.Context, k key.PackageRevisionKey) (repository.PackageRevision, error) {
	names := []plumbing.ReferenceName{
		createDraftName(k).refInLocal(),
		createProposedName(k).refInLocal(),
	}
	if k.Revision > 0 {
		names = append(names, createFinalTagNameInLocal(k))
	}
	if rev := util.Revision2Int(k.WorkspaceName); rev > 0 {
		tagKey := k
		tagKey.Revision = rev
		names = append(names, createFinalTagNameInLocal(tagKey))
	}
	ref, err := r.firstRef(names)
	if err != nil {
		return nil, err
	}
	if ref != nil {
		return r.revisionFromRef(ref)
	}
	return r.findTagByWorkspace(k)
}

func (r *gitRepository) UpdatePackageRevisionHead(ctx context.Context, old repository.PackageRevision) (repository.PackageRevisionDraft, error) {
	oldGit, ok := old.(*gitPackageRevision)
	if !ok {
		return nil, fmt.Errorf("cannot update non-git package %T", old)
	}
	if oldGit.ref == nil {
		return nil, fmt.Errorf("cannot update final package")
	}
	ref, err := r.firstRef([]plumbing.ReferenceName{oldGit.ref.Name()})
	if err != nil {
		return nil, fmt.Errorf("cannot find draft package branch %q: %w", oldGit.ref.Name(), err)
	}
	if ref == nil {
		return nil, fmt.Errorf("cannot find draft package branch %q", oldGit.ref.Name())
	}
	rev, err := r.loadDraftShallow(ref, repository.ListPackageRevisionFilter{})
	if err != nil {
		return nil, fmt.Errorf("cannot load draft package: %w", err)
	}
	if rev == nil {
		return nil, fmt.Errorf("cannot load draft package %q (package not found)", ref.Name())
	}
	r.mutex.Lock()
	lifecycle := r.getLifecycle(oldGit)
	r.mutex.Unlock()
	return &gitPackageRevisionDraft{
		prKey:     oldGit.prKey,
		repo:      r,
		metadata:  old.GetMeta(),
		lifecycle: lifecycle,
		updated:   rev.updated,
		base:      rev.ref,
		tree:      rev.tree,
		commit:    rev.commit,
		tasks:     rev.tasks,
	}, nil
}

func (r *gitRepository) firstRef(names []plumbing.ReferenceName) (*plumbing.Reference, error) {
	var found *plumbing.Reference
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		for _, name := range names {
			ref, err := repo.Reference(name, true)
			if err == nil {
				copied := plumbing.NewHashReference(ref.Name(), ref.Hash())
				found = copied
				return nil
			}
			if err != plumbing.ErrReferenceNotFound {
				return err
			}
		}
		return nil
	})
	return found, err
}

func (r *gitRepository) revisionFromRef(ref *plumbing.Reference) (repository.PackageRevision, error) {
	if isTagInLocalRepo(ref.Name()) {
		return r.loadTaggedPackageShallow(ref, repository.ListPackageRevisionFilter{})
	}
	return r.loadDraftShallow(ref, repository.ListPackageRevisionFilter{})
}

// findTagByWorkspace scans tag names for one package when the workspace is not
// encoded in the tag. Direct vN lookup handles the common case without this.
func (r *gitRepository) findTagByWorkspace(k key.PackageRevisionKey) (repository.PackageRevision, error) {
	if k.WorkspaceName == "" || k.PkgKey.Package == "" {
		return nil, nil
	}
	snaps, err := r.snapshotRefs()
	if err != nil {
		return nil, err
	}
	want := k.PkgKey.ToFullPathname()
	filter := repository.ListPackageRevisionFilter{Key: k}
	for _, snap := range snaps {
		if !isTagInLocalRepo(snap.name) {
			continue
		}
		name, ok := getTagNameInLocalRepo(snap.name)
		if !ok {
			continue
		}
		path, _, ok := splitTagPath(name)
		if !ok || path != want {
			continue
		}
		pr, err := r.loadTaggedPackageShallow(plumbing.NewHashReference(snap.name, snap.hash), filter)
		if err != nil || pr == nil {
			continue
		}
		if pr.Key().WorkspaceName == k.WorkspaceName {
			return pr, nil
		}
	}
	return nil, nil
}

func (r *gitRepository) snapshotRefs() ([]refSnap, error) {
	var snaps []refSnap
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		refs, err := repo.References()
		if err != nil {
			return err
		}
		defer refs.Close()
		for {
			ref, err := refs.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			snaps = append(snaps, refSnap{name: ref.Name(), hash: ref.Hash()})
		}
	})
	return snaps, err
}

func (r *gitRepository) loadDraftShallow(ref *plumbing.Reference, filter repository.ListPackageRevisionFilter) (*gitPackageRevision, error) {
	pkgPath, workspace, err := parseDraftName(ref)
	if err != nil {
		return nil, err
	}
	if !packageInDirectory(pkgPath, r.Key().Path) {
		return nil, nil
	}
	if !shallowKeyMayMatch(filter, pkgPath, workspace, 0) {
		return nil, nil
	}
	commit, err := r.commitFor(ref.Hash())
	if err != nil {
		return nil, pkgerrors.Wrap(err, "cannot resolve draft branch to commit (corrupted repository?)")
	}
	krmPackage, err := r.findPackage(commit, pkgPath)
	if err != nil {
		return nil, err
	}
	if krmPackage == nil {
		return nil, nil
	}
	return krmPackage.buildGitPackageRevisionHead("0", workspace, ref), nil
}

func (r *gitRepository) loadTaggedPackageShallow(tag *plumbing.Reference, filter repository.ListPackageRevisionFilter) (*gitPackageRevision, error) {
	name, ok := getTagNameInLocalRepo(tag.Name())
	if !ok {
		return nil, pkgerrors.Errorf("invalid tag ref: %q", tag)
	}
	path, revisionStr, ok := splitTagPath(name)
	if !ok {
		return nil, pkgerrors.Errorf("could not find slash in %q", name)
	}
	if !packageInDirectory(path, r.Key().Path) {
		return nil, nil
	}
	if !shallowKeyMayMatch(filter, path, "", util.Revision2Int(revisionStr)) {
		return nil, nil
	}
	commit, err := r.resolveCommit(tag.Hash())
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "cannot resolve tag %q to commit", name)
	}
	workspace := workspaceFromCommit(commit, path)
	if workspace == "" {
		workspace = "v" + util.Revision2Str(util.Revision2Int(revisionStr))
	}
	if !shallowKeyMayMatch(filter, path, workspace, util.Revision2Int(revisionStr)) {
		return nil, nil
	}
	krmPackage, err := r.findPackage(commit, path)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "skipping %q; cannot find %q (corrupted repository?)", name, path)
	}
	if krmPackage == nil {
		return nil, pkgerrors.Errorf("skipping %q: Kptfile not found", name)
	}
	return krmPackage.buildGitPackageRevisionHead(revisionStr, workspace, tag), nil
}

func (r *gitRepository) discoverFinalizedPackagesShallow(ref *plumbing.Reference, filter repository.ListPackageRevisionFilter) ([]*gitPackageRevision, error) {
	commit, err := r.commitFor(ref.Hash())
	if err != nil {
		return nil, err
	}
	revisionStr, ok := getBranchNameInLocalRepo(ref.Name())
	if !ok {
		if revisionStr, ok = getTagNameInLocalRepo(ref.Name()); !ok {
			return nil, pkgerrors.Errorf("cannot determine revision from ref: %q", ref.Name())
		}
	}
	opt := discoverPackagesOptions{filterPrefix: r.Key().Path, recurse: true}
	if filter.Key.PkgKey.Package != "" {
		opt = discoverPackagesOptions{filterPrefix: filter.Key.PkgKey.ToFullPathname(), recurse: false}
	}
	krmPackages, err := r.discoverPackagesInTree(commit, opt)
	if err != nil {
		return nil, err
	}
	var result []*gitPackageRevision
	for _, krmPackage := range krmPackages.packages {
		if filter.Key.PkgKey.Package != "" && krmPackage.pkgKey.ToFullPathname() != filter.Key.PkgKey.ToFullPathname() {
			continue
		}
		result = append(result, krmPackage.buildGitPackageRevisionHead(revisionStr, "", ref))
	}
	return result, nil
}

func (p *packageListEntry) buildGitPackageRevisionHead(revisionStr, workspace string, ref *plumbing.Reference) *gitPackageRevision {
	revision := util.Revision2Int(revisionStr)
	if revision != -1 {
		if workspace == "" {
			workspace = "v" + util.Revision2Str(revision)
		}
	} else {
		workspace = revisionStr
	}
	return &gitPackageRevision{
		prKey: key.PackageRevisionKey{
			PkgKey:        p.pkgKey,
			Revision:      revision,
			WorkspaceName: workspace,
		},
		repo:            p.parent.parent,
		updated:         p.parent.commit.Author.When,
		updatedBy:       p.parent.commit.Author.Email,
		ref:             ref,
		tree:            p.treeHash,
		commit:          p.parent.commit.Hash,
		loadTasksOnRead: true,
	}
}

func (p *gitPackageRevision) ensureTasks(ctx context.Context) {
	if !p.loadTasksOnRead {
		return
	}
	p.mutex.Lock()
	if !p.loadTasksOnRead {
		p.mutex.Unlock()
		return
	}
	p.mutex.Unlock()

	commit, err := p.repo.commitFor(p.commit)
	if err != nil {
		klog.Warningf("shallow package revision %s: %v", p.prKey, err)
		p.mutex.Lock()
		p.loadTasksOnRead = false
		p.mutex.Unlock()
		return
	}
	tasks, err := p.repo.loadTasksUntilStart(ctx, commit, p.prKey)
	if err != nil {
		klog.Warningf("Error when loading tasks for package %s: %s", p.prKey, err)
	}
	p.mutex.Lock()
	p.tasks = tasks
	p.loadTasksOnRead = false
	p.mutex.Unlock()
}

// loadTasksUntilStart walks commits only until this revision's init or clone.
// loadTasks keeps walking to the root, which gets slower as main grows.
// dbcache calls loadTasks, not this.
func (r *gitRepository) loadTasksUntilStart(_ context.Context, startCommit *object.Commit, pkgRev key.PackageRevisionKey) ([]porchapi.Task, error) {
	var tasks []porchapi.Task
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		commits, err := repo.Log(&git.LogOptions{From: startCommit.Hash, Order: git.LogOrderCommitterTime})
		if err != nil {
			return pkgerrors.Wrap(err, "error walking commits")
		}
		return visitCommitsCollectErrors(commits, func(commit *object.Commit) error {
			annotations, err := extractGitAnnotations(commit)
			if err != nil {
				klog.Warningf("Error extracting (some) git annotations from commit %q: %s", commit.Hash, err)
			}
			for _, annotation := range annotations {
				packageMatches := annotation.PackagePath == pkgRev.PkgKey.ToFullPathname()
				workspaceMatches := annotation.WorkspaceName == pkgRev.WorkspaceName ||
					(annotation.Revision == string(pkgRev.WorkspaceName) && annotation.WorkspaceName == "")
				if !packageMatches || !workspaceMatches || annotation.Task == nil {
					continue
				}
				if porchapi.IsValidFirstTaskType(annotation.Task.Type) {
					tasks = []porchapi.Task{*annotation.Task}
				}
				if annotation.Task.Type == porchapi.TaskTypeClone || annotation.Task.Type == porchapi.TaskTypeInit {
					return storer.ErrStop
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "errors loading tasks")
	}
	porchutil.SafeReverse(tasks)
	return tasks, nil
}

func (r *gitRepository) commitFor(hash plumbing.Hash) (*object.Commit, error) {
	var commit *object.Commit
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		var err error
		commit, err = repo.CommitObject(hash)
		return err
	})
	return commit, err
}

func (r *gitRepository) resolveCommit(hash plumbing.Hash) (*object.Commit, error) {
	var commit *object.Commit
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		resolved, err := repo.ResolveRevision(plumbing.Revision(hash.String()))
		if err != nil {
			return err
		}
		commit, err = repo.CommitObject(*resolved)
		return err
	})
	return commit, err
}

func workspaceFromCommit(commit *object.Commit, packagePath string) string {
	annotations, err := extractGitAnnotations(commit)
	if err != nil {
		return ""
	}
	for _, a := range annotations {
		if a.PackagePath == packagePath && a.WorkspaceName != "" {
			return a.WorkspaceName
		}
	}
	return ""
}

func splitTagPath(name string) (path, revision string, ok bool) {
	slash := strings.LastIndex(name, "/")
	if slash < 0 {
		return "", "", false
	}
	return name[:slash], name[slash+1:], true
}

func shallowKeyMayMatch(filter repository.ListPackageRevisionFilter, fullPath, workspace string, revision int) bool {
	k := filter.Key
	if k.PkgKey.Package != "" && fullPath != k.PkgKey.ToFullPathname() {
		return false
	}
	if k.WorkspaceName != "" && workspace != "" && k.WorkspaceName != workspace {
		return false
	}
	if k.Revision != 0 && revision != 0 && k.Revision != revision {
		return false
	}
	return true
}

func shallowMayIncludeMain(filter repository.ListPackageRevisionFilter, mainRef plumbing.ReferenceName) bool {
	branch, ok := getBranchNameInLocalRepo(mainRef)
	if !ok {
		return false
	}
	k := filter.Key
	if k.WorkspaceName != "" && k.WorkspaceName != branch {
		return false
	}
	if k.Revision != 0 && k.Revision != -1 {
		return false
	}
	return true
}

func shallowWantsDraft(filter repository.ListPackageRevisionFilter, name plumbing.ReferenceName) bool {
	if isProposedBranchNameInLocal(name) {
		return shallowWantsLifecycle(filter, porchapi.PackageRevisionLifecycleProposed)
	}
	return shallowWantsLifecycle(filter, porchapi.PackageRevisionLifecycleDraft)
}

func shallowWantsPublished(filter repository.ListPackageRevisionFilter) bool {
	return shallowWantsLifecycle(filter, porchapi.PackageRevisionLifecyclePublished) ||
		shallowWantsLifecycle(filter, porchapi.PackageRevisionLifecycleDeletionProposed)
}

func shallowWantsLifecycle(filter repository.ListPackageRevisionFilter, life porchapi.PackageRevisionLifecycle) bool {
	if len(filter.Lifecycles) == 0 {
		return true
	}
	return slices.Contains(filter.Lifecycles, life)
}
