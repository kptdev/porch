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

package lgcache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/cache/lgcache/lock"
	"github.com/kptdev/porch/pkg/cache/lgcache/meta"
	cachetypes "github.com/kptdev/porch/pkg/cache/types"
	"github.com/kptdev/porch/pkg/externalrepo/git"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/klog/v2"
)

type lgRepository struct {
	repoKey     key.RepositoryKey
	spec        *configapi.Repository
	pvcRoot     string
	lockTimeout time.Duration
	shardID     string
	pushDrafts  bool
	options     externalrepotypes.ExternalRepoOptions
	meta        *meta.Store
	notifier    cachetypes.RepoPRChangeNotifier

	local  git.GitRepository
	remote git.GitRepository

	lastExternalVersion string
	lastExternalPRs     map[key.PackageRevisionKey]repository.PackageRevision
}

var _ repository.Repository = &lgRepository{}

func (r *lgRepository) KubeObjectNamespace() string { return r.repoKey.Namespace }
func (r *lgRepository) KubeObjectName() string      { return r.repoKey.Name }
func (r *lgRepository) Key() key.RepositoryKey      { return r.repoKey }

func (r *lgRepository) localDir() string {
	return filepath.Join(r.pvcRoot, "repos", r.repoKey.Namespace, r.repoKey.Name)
}

func (r *lgRepository) mirrorDir() string {
	return filepath.Join(r.pvcRoot, "mirrors", r.repoKey.Namespace, r.repoKey.Name)
}

func (r *lgRepository) lockPath() string {
	shard := r.shardID
	if shard == "" {
		shard = "default"
	}
	return filepath.Join(r.pvcRoot, "locks", shard, r.repoKey.Namespace, r.repoKey.Name+".lock")
}

func (r *lgRepository) withLock(exclusive bool) (func(), error) {
	timeout := r.lockTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return lock.Lock(r.lockPath(), exclusive, timeout)
}

func (r *lgRepository) open(ctx context.Context) error {
	if r.spec.Spec.Git == nil || r.spec.Spec.Git.Repo == "" {
		return fmt.Errorf("lgcache: repository %s is not a git repository", r.repoKey)
	}
	unlock, err := r.withLock(true)
	if err != nil {
		return err
	}
	defer unlock()

	localDir := r.localDir()
	if _, err := os.Stat(filepath.Join(localDir, ".git")); err != nil {
		auth, authErr := r.auth(ctx)
		if authErr != nil {
			return authErr
		}
		if _, err := git.CloneRepository(ctx, localDir, r.spec.Spec.Git.Repo, auth, git.GitRepositoryOptions{
			Layout: git.FullRepositoryLayout,
		}); err != nil {
			return fmt.Errorf("lgcache: clone %s: %w", r.repoKey, err)
		}
	}

	local, err := git.OpenRepository(ctx, r.spec.Name, r.spec.Namespace, r.spec.Spec.Git, r.spec.Spec.Deployment, r.pvcRoot, r.localOptions())
	if err != nil {
		return err
	}
	r.local = local
	return nil
}

func (r *lgRepository) localOptions() git.GitRepositoryOptions {
	opts := r.options
	opts.DisableRemoteFetchOnRead = true
	opts.AbsoluteCacheDir = r.localDir()
	opts.SkipRemotePush = true
	opts.RetainCacheOnClose = true
	return git.GitRepositoryOptions{
		ExternalRepoOptions: opts,
		MainBranchStrategy:  git.SkipVerification,
		Layout:              git.FullRepositoryLayout,
	}
}

func (r *lgRepository) remoteOptions() git.GitRepositoryOptions {
	strategy := git.ErrorIfMissing
	if r.spec.Spec.Git != nil && r.spec.Spec.Git.CreateBranch {
		strategy = git.CreateIfMissing
	}
	opts := r.options
	opts.DisableRemoteFetchOnRead = false
	opts.SkipRemotePush = false
	opts.SkipFetchBeforePush = true
	opts.RetainCacheOnClose = true
	opts.AbsoluteCacheDir = r.mirrorDir()
	opts.RepoLayout = int(git.BareRepositoryLayout)
	return git.GitRepositoryOptions{
		ExternalRepoOptions: opts,
		MainBranchStrategy:  strategy,
		Layout:              git.BareRepositoryLayout,
	}
}

func (r *lgRepository) auth(ctx context.Context) (transport.AuthMethod, error) {
	secret := ""
	if r.spec.Spec.Git != nil {
		secret = r.spec.Spec.Git.SecretRef.Name
	}
	if secret == "" || r.options.CredentialResolver == nil {
		return nil, nil
	}
	creds, err := r.options.CredentialResolver.ResolveCredential(ctx, r.repoKey.Namespace, secret)
	if err != nil {
		return nil, err
	}
	if creds == nil || !creds.Valid() {
		return nil, fmt.Errorf("lgcache: invalid credentials for %s", r.repoKey)
	}
	return creds.ToAuthMethod(), nil
}

func (r *lgRepository) ensureRemote(ctx context.Context) (git.GitRepository, error) {
	if r.remote != nil {
		return r.remote, nil
	}
	remote, err := git.OpenRepository(ctx, r.spec.Name, r.spec.Namespace, r.spec.Spec.Git, r.spec.Spec.Deployment, r.pvcRoot, r.remoteOptions())
	if err != nil {
		return nil, err
	}
	r.remote = remote
	return remote, nil
}

func (r *lgRepository) Close(ctx context.Context) error {
	var err error
	if r.local != nil {
		err = r.local.Close(ctx)
	}
	if r.remote != nil {
		if cerr := r.remote.Close(ctx); err == nil {
			err = cerr
		}
	}
	return err
}

func (r *lgRepository) ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	unlock, err := r.withLock(false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return r.listLocked(ctx, filter)
}

func (r *lgRepository) listOne(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, bool, error) {
	k := filter.Key
	if k.PkgKey.Package == "" || k.WorkspaceName == "" || len(filter.Lifecycles) > 0 || len(filter.KptfileLabels) > 0 || filter.Label != nil {
		return nil, false, nil
	}
	found, err := r.local.FindPackageRevisionByKey(ctx, k)
	if err != nil {
		return nil, true, err
	}

	name := ""
	if found != nil {
		name = found.KubeObjectName()
	}
	if name == "" {
		name = key.ComposePkgRevObjName(key.PackageRevisionKey{
			PkgKey:        key.PackageKey{RepoKey: r.repoKey, Path: k.PkgKey.Path, Package: k.PkgKey.Package},
			WorkspaceName: k.WorkspaceName,
		})
	}
	crd, err := r.meta.Get(ctx, r.repoKey.Namespace, name)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, true, err
		}
		crd = nil
	}
	var out []repository.PackageRevision
	if found != nil || crd != nil {
		wrapped := &lgPackageRevision{repo: r, gitPR: found, crd: crd}
		if filter.Matches(ctx, wrapped) {
			out = append(out, wrapped)
		}
	}
	return out, true, nil
}

func (r *lgRepository) listLocked(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	if r.local == nil {
		return nil, fmt.Errorf("lgcache: repository %s is not open", r.repoKey)
	}
	if one, ok, err := r.listOne(ctx, filter); ok || err != nil {
		return one, err
	}
	local, err := r.local.ListPackageRevisionsShallow(ctx, filter)
	if err != nil {
		return nil, err
	}
	crds, err := r.meta.List(ctx, r.repoKey.Namespace, r.repoKey.Name)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*porchv1alpha2.PackageRevision, len(crds))
	for i := range crds {
		byName[crds[i].Name] = &crds[i]
	}

	seen := map[string]bool{}
	var out []repository.PackageRevision
	for _, gitPR := range local {
		name := gitPR.KubeObjectName()
		seen[name] = true
		crd := byName[name]
		if crd == nil {
			klog.V(4).Infof("[LG Cache] git revision %s has no v1alpha2 PackageRevision yet", name)
		}
		wrapped := &lgPackageRevision{repo: r, gitPR: gitPR, crd: crd}
		if filter.Matches(ctx, wrapped) {
			out = append(out, wrapped)
		}
	}
	for name, crd := range byName {
		if seen[name] {
			continue
		}
		wrapped := &lgPackageRevision{repo: r, crd: crd}
		if filter.Matches(ctx, wrapped) {
			out = append(out, wrapped)
		}
	}
	return out, nil
}

func (r *lgRepository) CreatePackageRevisionDraft(ctx context.Context, obj *porchapi.PackageRevision) (repository.PackageRevisionDraft, error) {
	unlock, err := r.withLock(true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return r.local.CreatePackageRevisionDraft(ctx, obj)
}

func (r *lgRepository) ClosePackageRevisionDraft(ctx context.Context, prd repository.PackageRevisionDraft, version int) (repository.PackageRevision, error) {
	unlock, err := r.withLock(true)
	if err != nil {
		return nil, err
	}
	defer unlock()

	closed, err := r.local.ClosePackageRevisionDraft(ctx, prd, version)
	if err != nil {
		return nil, err
	}
	wrapped := &lgPackageRevision{repo: r, gitPR: closed}
	if r.meta != nil {
		if crd, gerr := r.meta.Get(ctx, closed.KubeObjectNamespace(), closed.KubeObjectName()); gerr == nil {
			wrapped.crd = crd
		}
	}
	if err := r.pushClosed(ctx, wrapped); err != nil {
		return nil, err
	}
	r.notify(watch.Modified, wrapped)
	return wrapped, nil
}

func (r *lgRepository) pushClosed(ctx context.Context, pr *lgPackageRevision) error {
	if pr.Lifecycle(ctx) != porchapi.PackageRevisionLifecyclePublished {
		return nil
	}
	remote, err := r.ensureRemote(ctx)
	if err != nil {
		return err
	}
	if err := pushToExternal(ctx, remote, pr, true); err != nil {
		return err
	}
	r.markPushed(pr.Key())
	return nil
}

func (r *lgRepository) publish(ctx context.Context, pr *lgPackageRevision) error {
	unlock, err := r.withLock(true)
	if err != nil {
		return err
	}
	defer unlock()
	if pr.gitPR == nil {
		return fmt.Errorf("lgcache: cannot publish %s without local git content", pr.KubeObjectName())
	}
	draft, err := r.local.UpdatePackageRevisionHead(ctx, pr.gitPR)
	if err != nil {
		return err
	}
	if err := draft.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished); err != nil {
		return err
	}
	version := pr.Key().Revision
	if version <= 0 {
		maxRev, revErr := r.local.MaxPublishedRevision(ctx, pr.Key().PKey())
		if revErr != nil {
			return revErr
		}
		version = maxRev + 1
	}
	closed, err := r.local.ClosePackageRevisionDraft(ctx, draft, version)
	if err != nil {
		return err
	}
	pr.gitPR = closed
	remote, err := r.ensureRemote(ctx)
	if err != nil {
		return err
	}
	published := &lgPackageRevision{repo: r, gitPR: closed, crd: pr.crd}
	if err := pushToExternal(ctx, remote, published, true); err != nil {
		return err
	}
	r.markPushed(closed.Key())
	return r.writeLifecycle(ctx, pr, porchapi.PackageRevisionLifecyclePublished)
}

func (r *lgRepository) writeLifecycle(ctx context.Context, pr *lgPackageRevision, lifecycle porchapi.PackageRevisionLifecycle) error {
	if pr.crd == nil || r.meta == nil {
		return nil
	}
	updated := pr.crd.DeepCopy()
	updated.Spec.Lifecycle = porchv1alpha2.PackageRevisionLifecycle(lifecycle)
	if err := r.meta.Update(ctx, updated); err != nil {
		return err
	}
	pr.crd = updated
	return nil
}

func (r *lgRepository) DeletePackageRevision(ctx context.Context, old repository.PackageRevision) error {
	unlock, err := r.withLock(true)
	if err != nil {
		return err
	}
	defer unlock()

	target := old
	if lgPR, ok := old.(*lgPackageRevision); ok && lgPR.gitPR != nil {
		target = lgPR.gitPR
	}
	if err := r.local.DeletePackageRevision(ctx, target); err != nil {
		return err
	}
	if remote, err := r.ensureRemote(ctx); err == nil {
		if match, ferr := remote.FindPackageRevisionByKey(ctx, old.Key()); ferr == nil && match != nil {
			if derr := remote.DeletePackageRevision(ctx, match); derr != nil {
				klog.Warningf("[LG Cache] remote delete %s: %v", old.Key(), derr)
			}
		}
	}
	r.unmarkPushed(old.Key())
	r.notify(watch.Deleted, old)
	return nil
}

func (r *lgRepository) UpdatePackageRevision(ctx context.Context, old repository.PackageRevision) (repository.PackageRevisionDraft, error) {
	unlock, err := r.withLock(true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	lgPR, ok := old.(*lgPackageRevision)
	if !ok || lgPR.gitPR == nil {
		return nil, fmt.Errorf("lgcache: cannot update %T without a local git revision", old)
	}
	return r.local.UpdatePackageRevisionHead(ctx, lgPR.gitPR)
}

func (r *lgRepository) ListPackages(ctx context.Context, filter repository.ListPackageFilter) ([]repository.Package, error) {
	revs, err := r.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		return nil, err
	}
	byKey := map[key.PackageKey]*lgPackage{}
	for _, rev := range revs {
		pkgKey := rev.Key().PKey()
		pkg, ok := byKey[pkgKey]
		if !ok {
			pkg = &lgPackage{key: pkgKey}
			byKey[pkgKey] = pkg
		}
		if rev.Key().Revision > pkg.latest {
			pkg.latest = rev.Key().Revision
		}
	}
	var out []repository.Package
	for _, pkg := range byKey {
		if filter.Matches(pkg) {
			out = append(out, pkg)
		}
	}
	return out, nil
}

func (r *lgRepository) Version(ctx context.Context) (string, error) {
	if r.local == nil {
		return "", fmt.Errorf("lgcache: repository %s is not open", r.repoKey)
	}
	return r.local.Version(ctx)
}

func (r *lgRepository) BranchCommitHash(ctx context.Context) (string, error) {
	if r.local == nil {
		return "", fmt.Errorf("lgcache: repository %s is not open", r.repoKey)
	}
	return r.local.BranchCommitHash(ctx)
}

func (r *lgRepository) Refresh(ctx context.Context) error {
	unlock, err := r.withLock(true)
	if err != nil {
		return err
	}
	defer unlock()
	return r.syncOnce(ctx)
}

func (r *lgRepository) notify(event watch.EventType, obj repository.PackageRevision) {
	if r.notifier != nil {
		r.notifier.NotifyPackageRevisionChange(event, obj)
	}
}
