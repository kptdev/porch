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

package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/errors"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	context1 "github.com/kptdev/porch/pkg/util/context"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/klog/v2"
)

var tracer = otel.Tracer("git")

const (
	defaultMainReferenceName plumbing.ReferenceName = "refs/heads/main"
	originName               string                 = "origin"
	fileContent                                     = "Created by porch"
	fileName                                        = "README.md"
	commitMessage                                   = "Initial commit: Creating main branch"

	// Retry delay constants
	baseRetryDelay = 200 * time.Millisecond
	hookRetryDelay = 1 * time.Second
)

// Retryable error patterns for git push operations
var retryableErrors = []string{
	"remote ref",
	"failed to update ref",
	"pre-receive hook declined",
	"non-fast-forward update",
	"stale file handle",
	"unable to migrate objects to permanent storage",
	"failed to lock",
	"broken pipe",
	"status code: 500",
}

// Commit message constants for different operations
const (
	commitMessageRendering       = "Rendering package"
	commitMessageEdit            = "Creating new revision by copying previous revision"
	commitMessageInit            = "Creating new empty revision"
	commitMessageClone           = "Creating new revision by cloning"
	commitMessageUpgrade         = "Upgrading revision"
	commitMessageApproveTemplate = "Approving package revision %s/%d"
)

// formatCommitMessage returns a human-readable commit message based on the change type
func formatCommitMessage(changeType porchapi.TaskType) string {
	switch changeType {
	case porchapi.TaskTypeInit:
		return commitMessageInit
	case porchapi.TaskTypeRender:
		return commitMessageRendering
	case porchapi.TaskTypeEdit:
		return commitMessageEdit
	case porchapi.TaskTypeClone:
		return commitMessageClone
	case porchapi.TaskTypeUpgrade:
		return commitMessageUpgrade
	case porchapi.TaskTypeNone:
		return "Intermediate commit"
	default:
		return fmt.Sprintf("Intermediate commit: %s", changeType)
	}
}

type GitRepository interface {
	repository.Repository
	GetPackageRevision(ctx context.Context, ref, path string) (repository.PackageRevision, kptfilev1.GitLock, error)
	UpdateDeletionProposedCache(ctx context.Context) error
}

//go:generate go run golang.org/x/tools/cmd/stringer -type=MainBranchStrategy -linecomment
type MainBranchStrategy int

const (
	ErrorIfMissing   MainBranchStrategy = iota // ErrorIsMissing
	CreateIfMissing                            // CreateIfMissing
	SkipVerification                           // SkipVerification
)

type GitRepositoryOptions struct {
	externalrepotypes.ExternalRepoOptions
	MainBranchStrategy MainBranchStrategy
}

type gitUserInfoProvider struct {
	Author, Email string
}

// TODO: This is a temporary solution until https://github.com/go-git/go-git/issues/1528 is fixed
var conflictingRequiredRemoteRefError = pkgerrors.New("Remote ref is conflicting with required remote ref")

func (prov *gitUserInfoProvider) GetUserInfo(context.Context) *repository.UserInfo {
	return &repository.UserInfo{
		Email: prov.Email,
		Name:  prov.Author,
	}
}

func OpenRepository(ctx context.Context, name, namespace string, spec *configapi.GitRepository, deployment bool, root string, opts GitRepositoryOptions) (GitRepository, error) {
	ctx, span := tracer.Start(ctx, "git.go::OpenRepository", trace.WithAttributes())
	defer span.End()
	start := time.Now()
	defer func() { klog.V(4).Infof("git.go::OpenRepository (%s) took %s", spec.Repo, time.Since(start)) }()

	replace := strings.NewReplacer("/", "-", ":", "-")
	repoPath := replace.Replace(spec.Repo)
	// Sanitize path to prevent traversal attacks
	repoPath = filepath.Clean(repoPath)
	if strings.Contains(repoPath, "..") {
		return nil, fmt.Errorf("invalid repository path: contains path traversal")
	}
	dir := filepath.Join(root, repoPath)
	// Ensure the final path is within the root directory
	if !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(root)) {
		return nil, fmt.Errorf("invalid repository path: outside root directory")
	}

	// Get or create shared repository - let SharedDirectory handle cleanup
	sharedDir, err := globalDirectoryPool.getOrCreateSharedRepository(dir, name)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "error cloning git repository %+v", spec.Repo)
	}

	// Create Remote
	if err := initializeOrigin(sharedDir.repo, spec.Repo); err != nil {
		globalDirectoryPool.releaseSharedRepository(dir, name)
		return nil, pkgerrors.Wrapf(err, "error cloning git repository %+v, cannot create remote", spec.Repo)
	}

	// NOTE: the spec.git.branch field in the Repository CRD (OpenAPI schema) is defined with
	//		 MinLength=1 validation and its default value is set to "main". This means that
	// 		 it should never be empty at this point. The following code is left here as a last resort failsafe.
	branch := mainBranch
	if spec.Branch != "" {
		branch = branchName(spec.Branch)
	}

	if err := util.ValidateDirectoryName(string(branch), false); err != nil {
		globalDirectoryPool.releaseSharedRepository(dir, name)
		return nil, pkgerrors.Wrapf(err, "error cloning git repository %+v, branch name %q invalid", spec.Repo, branch)
	}

	repository := &gitRepository{
		key: repository.RepositoryKey{
			Name:              name,
			Namespace:         namespace,
			Path:              strings.Trim(spec.Directory, "/"),
			PlaceholderWSname: string(branch),
		},
		branch:                     branch,
		secret:                     spec.SecretRef.Name,
		credentialResolver:         opts.CredentialResolver,
		userInfoProvider:           makeUserInfoProvider(spec, opts.UserInfoProvider),
		cacheDir:                   dir,
		deployment:                 deployment,
		repoOperationRetryAttempts: opts.RepoOperationRetryAttempts,
		sharedDir:                  sharedDir,
	}

	if opts.UseUserDefinedCaBundle && opts.CaBundleResolver != nil {
		if caBundle, err := opts.CaBundleResolver.ResolveCredential(ctx, namespace, namespace+"-ca-bundle"); err != nil {
			klog.Errorf("failed to obtain caBundle from secret %s/%s: %v", namespace, namespace+"-ca-bundle", err)
		} else {
			repository.caBundle = []byte(caBundle.ToString())
		}
	}

	if err := repository.fetchRemoteRepositoryWithRetry(ctx); err != nil {
		globalDirectoryPool.releaseSharedRepository(dir, name)
		return nil, pkgerrors.Wrapf(err, "error cloning git repository %+v, fetch of remote repository failed", spec.Repo)
	}

	if err := repository.verifyRepository(ctx, &opts); err != nil {
		globalDirectoryPool.releaseSharedRepository(dir, name)
		return nil, pkgerrors.Wrapf(err, "error cloning git repository %+v, fetch of remote repository failed", spec.Repo)
	}

	// SharedDirectory handles cleanup automatically via reference counting

	return repository, nil
}

// makeUserInfoProvider creates a UserInfoProvider based on the provided GitRepository spec.
// If the spec contains no author or email, def will be returned instead.
// If only one of author or email is present, the one present will be used in the other's place.
func makeUserInfoProvider(spec *configapi.GitRepository, def repository.UserInfoProvider) repository.UserInfoProvider {
	if spec.Author != "" || spec.Email != "" {
		ui := &gitUserInfoProvider{
			Author: spec.Author,
			Email:  spec.Email,
		}
		if ui.Author == "" {
			ui.Author = spec.Email
		}
		return ui
	}
	return def
}

type gitRepository struct {
	key                repository.RepositoryKey
	secret             string           // Name of the k8s Secret resource containing credentials
	branch             branchName       // The main branch from repository registration (defaults to 'main' if unspecified)
	sharedDir          *sharedDirectory // For thread-safe access to git repository - Holds *git.Repository
	credentialResolver repository.CredentialResolver
	userInfoProvider   repository.UserInfoProvider

	// Folder used for the local git cache.
	cacheDir                   string
	repoOperationRetryAttempts int

	// deployment holds spec.deployment
	// TODO: Better caching here, support repository spec changes
	deployment bool

	// credential contains the information needed to authenticate against
	// a git repository.
	credential repository.Credential

	// deletionProposedCache contains the deletionProposed branches that
	// exist in the repo so that we can easily check them without iterating
	// through all the refs each time
	deletionProposedCache map[branchName]bool

	mutex sync.Mutex

	// caBundle to use for TLS communication towards git
	caBundle []byte
}

var _ GitRepository = &gitRepository{}
var _ repository.Repository = &gitRepository{}

func (r *gitRepository) KubeObjectNamespace() string {
	return r.Key().Namespace
}

func (r *gitRepository) KubeObjectName() string {
	return r.Key().Name
}

func (r *gitRepository) Key() repository.RepositoryKey {
	return r.key
}

func (r *gitRepository) Close(context.Context) error {
	// Release shared directory reference instead of removing it
	globalDirectoryPool.releaseSharedRepository(r.cacheDir, r.Key().Name)
	klog.Infof("gitRepository:Close: completed cleanup for repo %+v", r.Key())
	return nil
}

func (r *gitRepository) Version(ctx context.Context) (string, error) {
	_, span := tracer.Start(ctx, "gitRepository::Version", trace.WithAttributes())
	defer span.End()

	// Fetch remote without holding mutex - Locked in fetchRemoteRepository function
	if err := r.fetchRemoteRepositoryWithRetry(ctx); err != nil {
		return "", err
	}

	var b bytes.Buffer
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		refs, err := repo.References()
		if err != nil {
			return err
		}
		defer refs.Close()

		for {
			ref, err := refs.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			b.WriteString(ref.String())
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(hash[:]), nil
}

func (r *gitRepository) BranchCommitHash(ctx context.Context) (string, error) {
	_, span := tracer.Start(ctx, "gitRepository::BranchCommitHash", trace.WithAttributes())
	defer span.End()

	var hash string
	err := r.sharedDir.withRLock(func(repo *git.Repository) error {
		ref, err := repo.Reference(r.branch.refInLocal(), true)
		if err != nil {
			// Branch doesn't exist yet - return empty string, not an error
			if pkgerrors.Is(err, plumbing.ErrReferenceNotFound) {
				return nil
			}
			return err
		}
		hash = ref.Hash().String()
		return nil
	})
	return hash, err
}

func (r *gitRepository) ListPackages(_ context.Context, _ repository.ListPackageFilter) ([]repository.Package, error) {

	// TODO
	return nil, fmt.Errorf("ListPackages() is only supported on repository caches")
}

func (r *gitRepository) ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "gitRepository::ListPackageRevisions", trace.WithAttributes())
	defer span.End()

	pkgRevs, err := r.listPackageRevisions(ctx, filter)
	if err != nil {
		return nil, err
	}
	var repoPkgRevs []repository.PackageRevision
	for i := range pkgRevs {
		repoPkgRevs = append(repoPkgRevs, pkgRevs[i])
	}
	return repoPkgRevs, nil
}

func (r *gitRepository) listPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]*gitPackageRevision, error) {
	ctx, span := tracer.Start(ctx, "gitRepository::listPackageRevisions", trace.WithAttributes())
	defer span.End()

	// Fetch remote without holding mutex - Locked in fetchRemoteRepository function
	if err := r.fetchRemoteRepositoryWithRetry(ctx); err != nil {
		return nil, err
	}

	var refs storer.ReferenceIter
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		var err error
		refs, err = repo.References()
		return err
	})
	if err != nil {
		return nil, err
	}
	defer refs.Close()

	var main *plumbing.Reference
	var drafts []*gitPackageRevision
	var result []*gitPackageRevision

	mainBranch := r.branch.refInLocal() // Looking for the registered branch

	for ref, err := refs.Next(); err == nil; ref, err = refs.Next() {
		switch name := ref.Name(); {
		case name == mainBranch:
			main = ref
			continue

		case isProposedBranchNameInLocal(ref.Name()), isDraftBranchNameInLocal(ref.Name()):
			klog.Infof("Loading draft from %q", ref.Name())
			draft, err := r.loadDraft(ctx, ref)
			if err != nil {
				return nil, pkgerrors.Wrapf(err, "failed to load package draft %q", name.String())
			}
			if draft != nil {
				drafts = append(drafts, draft)
			} else {
				klog.Warningf("no package draft found for ref %v", ref)
			}
		case isTagInLocalRepo(ref.Name()):
			klog.V(2).Infof("Loading tag from %q", ref.Name())
			tagged, err := r.loadTaggedPackage(ctx, ref)
			if err != nil {
				if tagged == nil {
					// this tag is not associated with any package (e.g. could be a release tag)
					klog.Warningf("Failed to load tagged package from ref %q: %s", ref.Name(), err)
					continue
				}
				klog.Warningf("Error loading tagged package from ref %q: %s", ref.Name(), err)
			}
			if tagged != nil {
				result = append(result, tagged)
			}
		}
	}

	if main != nil {
		// TODO: ignore packages that are unchanged in main branch, compared to a tagged version?
		mainpkgs, err := r.discoverFinalizedPackages(ctx, main)
		if err != nil {
			if len(mainpkgs) == 0 {
				return nil, err
			}
			klog.Warningf("Error discovering finalized packages: %s", err)
		}
		result = append(result, mainpkgs...)
	}

	result = append(result, drafts...)

	result = slices.DeleteFunc(result, func(p *gitPackageRevision) bool {
		return !filter.Matches(ctx, p)
	})

	return result, nil
}

func (r *gitRepository) CreatePackageRevisionDraft(ctx context.Context, obj *porchapi.PackageRevision) (repository.PackageRevisionDraft, error) {
	_, span := tracer.Start(ctx, "gitRepository::CreatePackageRevisionDraft", trace.WithAttributes())
	defer span.End()

	pkgKey := repository.FromFullPathname(r.Key(), obj.Spec.PackageName)
	if err := util.ValidPkgRevObjName(r.Key().Name, pkgKey.Path, pkgKey.Package, obj.Spec.WorkspaceName); err != nil {
		return nil, fmt.Errorf("failed to create packagerevision: %w", err)
	}
	klog.InfoS("[Git] Creating in-memory draft object started for PackageRevision", context1.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[Git] Creating in-memory draft object completed for PackageRevision", context1.LogMetadataFrom(ctx)...)
	}()

	var base plumbing.Hash
	refName := r.branch.refInLocal()
	err := r.sharedDir.withRLock(func(repo *git.Repository) error {
		switch main, err := repo.Reference(refName, true); err {
		case nil:
			base = main.Hash()
			return nil
		case plumbing.ErrReferenceNotFound:
			// reference not found - empty repository. Package draft has no parent commit
			return nil
		default:
			return fmt.Errorf("error when resolving target branch for the package: %w", err)
		}
	})
	if err != nil {
		return nil, err
	}

	draftKey := repository.PackageRevisionKey{
		PkgKey:        pkgKey,
		WorkspaceName: obj.Spec.WorkspaceName,
	}

	// TODO use git branches to leverage uniqueness
	draftBranchName := createDraftName(draftKey)

	// TODO: This should also create a new 'Package' resource if one does not already exist

	return &gitPackageRevisionDraft{
		prKey:     draftKey,
		repo:      r,
		metadata:  obj.ObjectMeta,
		lifecycle: porchapi.PackageRevisionLifecycleDraft,
		updated:   time.Now(),
		base:      nil, // Creating a new package
		tasks:     nil, // Creating a new package
		branch:    draftBranchName,
		commit:    base,
	}, nil
}

func (r *gitRepository) UpdatePackageRevision(ctx context.Context, old repository.PackageRevision) (repository.PackageRevisionDraft, error) {
	ctx, span := tracer.Start(ctx, "gitRepository::UpdatePackageRevision", trace.WithAttributes())
	defer span.End()

	oldGitPackage, ok := old.(*gitPackageRevision)
	if !ok {
		return nil, fmt.Errorf("cannot update non-git package %T", old)
	}

	klog.InfoS("[Git] Loading draft for update started for PackageRevision", context1.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[Git] Loading draft for update completed for PackageRevision", context1.LogMetadataFrom(ctx)...)
	}()

	ref := oldGitPackage.ref
	if ref == nil {
		return nil, fmt.Errorf("cannot update final package")
	}

	var head *plumbing.Reference
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		var err error
		head, err = repo.Reference(ref.Name(), true)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("cannot find draft package branch %q: %w", ref.Name(), err)
	}

	rev, err := r.loadDraft(ctx, head)
	if err != nil {
		return nil, fmt.Errorf("cannot load draft package: %w", err)
	}
	if rev == nil {
		return nil, fmt.Errorf("cannot load draft package %q (package not found)", ref.Name())
	}

	r.mutex.Lock()
	lifecycle := r.getLifecycle(oldGitPackage)
	r.mutex.Unlock()

	return &gitPackageRevisionDraft{
		prKey:     oldGitPackage.prKey,
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

func (r *gitRepository) DeletePackageRevision(ctx context.Context, pr2Delete repository.PackageRevision) error {
	ctx, span := tracer.Start(ctx, "gitRepository::DeletePackageRevision", trace.WithAttributes())
	defer span.End()

	// Set the default reference name using the PR key and lifecycle
	referenceName := plumbing.ReferenceName(getReferenceName(ctx, pr2Delete))

	// If this is a git PR, we use the git reference name rather than the default reference name
	gitPR2Delete, ok := pr2Delete.(*gitPackageRevision)
	if ok {
		if gitPR2Delete.ref != nil {
			referenceName = gitPR2Delete.ref.Name()
		} else {
			referenceName = ""
		}
	}
	klog.InfoS("[Git] Deleting branch from Git repository started for PackageRevision", context1.LogMetadataFrom(ctx)...)
	defer func() {
		klog.V(3).InfoS("[Git] Deleting branch from Git repository completed for PackageRevision", context1.LogMetadataFrom(ctx)...)
	}()

	if referenceName == "" {
		// This is an internal error. In some rare cases (see GetPackageRevision below) we create
		// package revisions without refs. They should never be returned via the API though.
		return fmt.Errorf("cannot delete package with no ref: %s", pr2Delete.Key().PKey().ToFullPathname())
	}

	return util.RetryOnErrorConditional(
		r.repoOperationRetryAttempts,
		func(err error) bool {
			return pkgerrors.Is(err, conflictingRequiredRemoteRefError)
		},
		func(retryNumber int) error {
			klog.Infof("Deleting PackageRevision %q try number %d", pr2Delete.Key().K8SName(), retryNumber)

			if retryNumber > 0 {
				if err := r.fetchRemoteRepositoryWithRetry(ctx); err != nil {
					return err
				}
			}

			refSpecs := newPushRefSpecBuilder()
			var commitOps *commitOperationBuilder
			deletionProposedBranch := createDeletionProposedName(pr2Delete.Key())

			switch {
			case referenceName.IsTag():
				name := createFinalTagNameInLocal(pr2Delete.Key())
				if referenceName != name {
					return fmt.Errorf("cannot delete package tagged with a tag that is not specific to the package: %s", referenceName)
				}
				var ref *plumbing.Reference
				err := r.sharedDir.withLock(func(repo *git.Repository) error {
					var err error
					ref, err = repo.Reference(referenceName, true)
					return err
				})
				if err != nil {
					return err
				}
				refSpecs.addRefToDelete(ref)
				refSpecs.addRefToDelete(plumbing.NewHashReference(deletionProposedBranch.refInLocal(), plumbing.ZeroHash))

			case isDraftBranchNameInLocal(referenceName), isProposedBranchNameInLocal(referenceName):
				var ref *plumbing.Reference
				err := r.sharedDir.withLock(func(repo *git.Repository) error {
					var err error
					ref, err = repo.Reference(referenceName, true)
					return err
				})
				if err != nil {
					return err
				}
				refSpecs.addRefToDelete(ref)

			case isBranchInLocalRepo(referenceName):
				commitOps = newCommitOperationBuilder()
				commitOps.addPackageDeletion(referenceName, pr2Delete.Key())
				refSpecs.addRefToDelete(plumbing.NewHashReference(deletionProposedBranch.refInLocal(), plumbing.ZeroHash))

			default:
				return fmt.Errorf("cannot delete package with the ref name %s", referenceName)
			}

			if err := r.pushAndCleanup(ctx, refSpecs, commitOps); err != nil {
				if pkgerrors.Is(err, git.NoErrAlreadyUpToDate) {
					klog.Infof("All remote references are already up to date for deleting package %s", pr2Delete.Key())
				} else {
					return fmt.Errorf("failed to update git references: %w", err)
				}
			}

			r.mutex.Lock()
			delete(r.deletionProposedCache, deletionProposedBranch)
			r.mutex.Unlock()

			return nil
		},
	)
}

// fetchRemoteWithRetry retries fetching the remote repository up to 4 times with a delay of 1 second between each attempt.
func (r *gitRepository) fetchRemoteRepositoryWithRetry(ctx context.Context) error {
	if err := util.RetryOnErrorConditional(
		r.repoOperationRetryAttempts,
		func(error) bool {
			return true
		},
		func(retryNumber int) error {
			if retryNumber >= 0 {
				if ctx.Err() != nil {
					klog.Infof("fetchRemoteRepositoryWithRetry %s ctx err: %v", r.Key(), ctx.Err())
				}
				if deadline, ok := ctx.Deadline(); ok {
					klog.V(2).Infof("fetchRemoteRepositoryWithRetry %s deadline: %v, reached: %t", r.Key(), deadline, deadline.Compare(time.Now()) <= 0)
				}
				select {
				case <-ctx.Done():
					klog.Infof("fetchRemoteRepositoryWithRetry %s ctx is done", r.Key())
				default:
				}

				err := r.fetchRemoteRepository(ctx)
				if err != nil {
					klog.Errorf("Fetching Remote Repository %+v failed - try number %d", r.Key(), retryNumber)
					time.Sleep(time.Duration(retryNumber+1) * baseRetryDelay)
					return pkgerrors.Wrapf(err, "fetch of remote repository %+v with retry failed on try number %d", r.Key(), retryNumber)
				}
				if retryNumber > 1 {
					klog.Infof("Successfully Fetched Remote Repository %+v after %d retries", r.Key(), retryNumber)
				}
				return nil
			}
			return nil
		},
	); err != nil {
		return pkgerrors.Wrapf(err, "fetch of remote repository %+v with retry failed", r.Key())
	}
	return nil
}

func (r *gitRepository) GetPackageRevision(ctx context.Context, version, path string) (repository.PackageRevision, kptfilev1.GitLock, error) {
	ctx, span := tracer.Start(ctx, "gitRepository::GetPackageRevision", trace.WithAttributes())
	defer span.End()

	var hash plumbing.Hash

	// Trim leading and trailing slashes
	path = strings.Trim(path, "/")

	// Versions map to gitRepo tags in one of two ways:
	//
	// * directly (tag=version)- but then this means that all packages in the repo must be versioned together.
	// * prefixed (tag=<packageDir/<version>) - solving the co-versioning problem.
	//
	// We have to check both forms when looking up a version.
	refNames := []string{}
	if path != "" {
		refNames = append(refNames, path+"/"+version)
		// HACK: Is this always refs/remotes/origin ?  Is it ever not (i.e. do we need both forms?)
		refNames = append(refNames, "refs/remotes/origin/"+path+"/"+version)
	}
	refNames = append(refNames, version)
	// HACK: Is this always refs/remotes/origin ?  Is it ever not (i.e. do we need both forms?)
	refNames = append(refNames, "refs/remotes/origin/"+version)

	// Ensure we have the latest references before resolving (without holding mutex)
	if err := r.fetchRemoteRepositoryWithRetry(ctx); err != nil {
		klog.Warningf("Failed to fetch latest references: %v", err)
	}

	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		for _, ref := range refNames {
			if resolved, err := repo.ResolveRevision(plumbing.Revision(ref)); err != nil {
				if pkgerrors.Is(err, plumbing.ErrReferenceNotFound) {
					continue
				}
				return pkgerrors.Wrapf(err, "error resolving git reference %q", ref)
			} else {
				hash = *resolved
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, kptfilev1.GitLock{}, err
	}

	if hash.IsZero() {
		r.dumpAllRefs()

		return nil, kptfilev1.GitLock{}, pkgerrors.Errorf("cannot find git reference (tried %v)", refNames)
	}

	return r.loadPackageRevision(ctx, version, path, hash)
}

func (r *gitRepository) loadPackageRevision(ctx context.Context, version, path string, hash plumbing.Hash) (repository.PackageRevision, kptfilev1.GitLock, error) {
	ctx, span := tracer.Start(ctx, "gitRepository::loadPackageRevision", trace.WithAttributes())
	defer span.End()

	if !packageInDirectory(path, r.Key().Path) {
		return nil, kptfilev1.GitLock{}, fmt.Errorf("cannot find package %s@%s; package is not under the Repository.spec.directory", path, version)
	}

	var lock kptfilev1.GitLock
	var commit *object.Commit

	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		origin, err := repo.Remote("origin")
		if err != nil {
			return pkgerrors.Wrap(err, "cannot determine repository origin")
		}

		lock = kptfilev1.GitLock{
			Repo:      origin.Config().URLs[0],
			Directory: path,
			Ref:       version,
		}

		commit, err = repo.CommitObject(hash)
		if err != nil {
			return pkgerrors.Wrapf(err, "cannot resolve git reference %s (hash: %s) to commit", version, hash)
		}
		lock.Commit = commit.Hash.String()
		return nil
	})
	if err != nil {
		return nil, lock, err
	}

	krmPackage, err := r.findPackage(commit, path)
	if err != nil {
		return nil, lock, err
	}

	if krmPackage == nil {
		return nil, lock, pkgerrors.Errorf("cannot find package %s@%s", path, version)
	}

	var ref *plumbing.Reference = nil // Cannot determine ref; this package will be considered final (immutable).

	var revisionStr, workspace string
	last := strings.LastIndex(version, "/")

	if strings.HasPrefix(version, "drafts/") || strings.HasPrefix(version, "proposed/") {
		// the passed in version is a ref to an unpublished package revision
		workspace = version[last+1:]
	} else {
		// the passed in version is a ref to a published package revision
		if version == string(r.branch) || last < 0 {
			revisionStr = version
		} else {
			revisionStr = version[last+1:]
		}
		workspace = getPkgWorkspace(commit, krmPackage, ref)
		if workspace == "" {
			workspace = revisionStr
		}
	}

	packageRevision, err := krmPackage.buildGitPackageRevision(ctx, revisionStr, workspace, ref)
	if err != nil {
		return nil, lock, err
	}
	return packageRevision, lock, nil
}

func (r *gitRepository) discoverFinalizedPackages(ctx context.Context, ref *plumbing.Reference) ([]*gitPackageRevision, error) {
	ctx, span := tracer.Start(ctx, "gitRepository::discoverFinalizedPackages", trace.WithAttributes())
	defer span.End()

	var commit *object.Commit
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		var err error
		commit, err = repo.CommitObject(ref.Hash())
		return err
	})
	if err != nil {
		return nil, err
	}

	var revisionStr string
	if rev, ok := getBranchNameInLocalRepo(ref.Name()); ok {
		revisionStr = rev
	} else if rev, ok = getTagNameInLocalRepo(ref.Name()); ok {
		revisionStr = rev
	} else {
		// TODO: ignore the ref instead?
		return nil, pkgerrors.Errorf("cannot determine revision from ref: %q", rev)
	}

	krmPackages, err := r.discoverPackagesInTree(commit, discoverPackagesOptions{filterPrefix: r.Key().Path, recurse: true})
	if err != nil {
		return nil, err
	}

	ec := errors.NewErrorCollector().WithSeparator(";").WithFormat("{%s}")
	var result []*gitPackageRevision
	for _, krmPackage := range krmPackages.packages {
		workspace := getPkgWorkspace(commit, krmPackage, ref)
		if workspace == "" {
			klog.Warningf("Failed to get workspace name for package %q (will use revision name): %s", krmPackage.pkgKey, err)
		}
		packageRevision, err := krmPackage.buildGitPackageRevision(ctx, revisionStr, workspace, ref)
		if err != nil {
			ec.Add(pkgerrors.Wrapf(err, "failed to build git package revision for package %s", krmPackage.pkgKey))
			continue
		}
		result = append(result, packageRevision)
	}

	return result, ec.Join()
}

// loadDraft will load the draft package.  If the package isn't found (we now require a Kptfile), it will return (nil, nil)
func (r *gitRepository) loadDraft(ctx context.Context, ref *plumbing.Reference) (*gitPackageRevision, error) {
	ctx, span := tracer.Start(ctx, "gitRepository::loadDraft", trace.WithAttributes())
	defer span.End()

	pkgPathAndName, workspaceName, err := parseDraftName(ref)
	if err != nil {
		return nil, err
	}

	// Only load drafts in the directory specified at repository registration.
	if !packageInDirectory(pkgPathAndName, r.Key().Path) {
		return nil, nil
	}

	var commit *object.Commit
	err = r.sharedDir.withLock(func(repo *git.Repository) error {
		var err error
		commit, err = repo.CommitObject(ref.Hash())
		return err
	})
	if err != nil {
		return nil, pkgerrors.Wrap(err, "cannot resolve draft branch to commit (corrupted repository?)")
	}

	krmPackage, err := r.findPackage(commit, pkgPathAndName)
	if err != nil {
		return nil, err
	}

	if krmPackage == nil {
		klog.Warningf("draft package %q was not found", pkgPathAndName)
		return nil, nil
	}

	packageRevision, err := krmPackage.buildGitPackageRevision(ctx, "0", workspaceName, ref)
	if err != nil {
		return nil, err
	}

	return packageRevision, nil
}

func (r *gitRepository) UpdateDeletionProposedCache(ctx context.Context) error {
	// Fetch remote without holding mutex - Locked in fetchRemoteRepository function
	if err := r.fetchRemoteRepositoryWithRetry(ctx); err != nil {
		return err
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.updateDeletionProposedCache()
}

func (r *gitRepository) updateDeletionProposedCache() error {
	// Collect deletion proposed branches
	deletionProposed := make(map[branchName]bool)

	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		refs, err := repo.References()
		if err != nil {
			return err
		}
		defer refs.Close()

		for {
			ref, err := refs.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("error getting next ref: %w", err)
			}

			branch, isDeletionProposedBranch := getdeletionProposedBranchNameInLocal(ref.Name())
			if isDeletionProposedBranch {
				deletionProposed[deletionProposedPrefix+branch] = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Update cache
	r.deletionProposedCache = deletionProposed
	return nil
}

func parseDraftName(draft *plumbing.Reference) (pkgPathAndName, workspaceName string, err error) {
	refName := draft.Name()
	var suffix string
	if b, ok := getDraftBranchNameInLocal(refName); ok {
		suffix = string(b)
	} else if b, ok = getProposedBranchNameInLocal(refName); ok {
		suffix = string(b)
	} else {
		return "", "", fmt.Errorf("invalid draft ref name: %q", refName)
	}

	revIndex := strings.LastIndex(suffix, "/")
	if revIndex <= 0 {
		return "", "", fmt.Errorf("invalid draft ref name; missing workspaceName suffix: %q", refName)
	}
	pkgPathAndName, workspaceName = suffix[:revIndex], suffix[revIndex+1:]
	return pkgPathAndName, workspaceName, nil
}

func (r *gitRepository) loadTaggedPackage(ctx context.Context, tag *plumbing.Reference) (*gitPackageRevision, error) {
	ctx, span := tracer.Start(ctx, "gitRepository::loadTaggedPackage", trace.WithAttributes())
	defer span.End()

	name, ok := getTagNameInLocalRepo(tag.Name())
	if !ok {
		return nil, pkgerrors.Errorf("invalid tag ref: %q", tag)
	}
	slash := strings.LastIndex(name, "/")

	if slash < 0 {
		// tag=<version>
		// could be a release tag or something else, we ignore these types of tags
		return nil, pkgerrors.Errorf("could not find slash in %q", name)
	}

	// tag=<package path>/version
	path, revisionStr := name[:slash], name[slash+1:]

	if !packageInDirectory(path, r.Key().Path) {
		return nil, nil
	}

	var resolvedHash *plumbing.Hash
	var commit *object.Commit
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		var err error
		resolvedHash, err = repo.ResolveRevision(plumbing.Revision(tag.Hash().String()))
		if err != nil {
			return pkgerrors.Wrapf(err, "cannot resolve tag %q to git revision", name)
		}
		if resolvedHash == nil {
			return pkgerrors.Errorf("resolved hash is nil for tag %q", name)
		}
		commit, err = repo.CommitObject(*resolvedHash)
		if err != nil {
			return pkgerrors.Wrapf(err, "cannot resolve tag %q (hash: %q) to commit (corrupted repository?)", name, resolvedHash)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	krmPackage, err := r.findPackage(commit, path)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "skipping %q; cannot find %q (corrupted repository?)", name, path)
	}

	if krmPackage == nil {
		return nil, pkgerrors.Errorf("skipping %q: Kptfile not found", name)
	}

	workspaceName := getPkgWorkspace(commit, krmPackage, tag)
	if workspaceName == "" {
		klog.Warningf("Failed to get package workspace name for package %q (will use branch name)", name)
	}
	packageRevision, err := krmPackage.buildGitPackageRevision(ctx, revisionStr, workspaceName, tag)
	if err != nil {
		if packageRevision == nil {
			return nil, err
		}
		klog.Warningf("Error building git package revision %q: %s", path, err)
	}

	return packageRevision, nil
}

func (r *gitRepository) dumpAllRefs() {
	if err := r.sharedDir.withLock(func(repo *git.Repository) error {
		refs, err := repo.References()
		if err != nil {
			klog.Warningf("failed to get references: %v", err)
		} else {
			defer refs.Close()
			for {
				ref, err := refs.Next()
				if err != nil {
					if err != io.EOF {
						klog.Warningf("failed to get next reference: %v", err)
					}
					break
				}
				klog.Infof("ref %#v", ref.Name())
			}
		}

		branches, err := repo.Branches()
		if err != nil {
			klog.Warningf("failed to get branches: %v", err)
		} else {
			defer branches.Close()
			for {
				branch, err := branches.Next()
				if err != nil {
					if err != io.EOF {
						klog.Warningf("failed to get next branch: %v", err)
					}
					break
				}
				klog.Infof("branch %#v", branch.Name())
			}
		}
		return nil
	}); err != nil {
		klog.Warningf("failed to dump refs: %v", err)
	}
}

// getAuthMethod fetches the credentials for authenticating to git. It caches the
// credentials between calls and refresh credentials when the tokens have expired.
func (r *gitRepository) getAuthMethod(ctx context.Context, forceRefresh bool) (transport.AuthMethod, error) {
	// If no secret is provided, we try without any auth.
	if r.secret == "" {
		return nil, nil
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.credential == nil || !r.credential.Valid() || forceRefresh {
		if cred, err := r.credentialResolver.ResolveCredential(ctx, r.Key().Namespace, r.secret); err != nil {
			return nil, fmt.Errorf("failed to obtain credential from secret %s/%s: %w", r.Key().Namespace, r.secret, err)
		} else {
			r.credential = cred
		}
	}

	return r.credential.ToAuthMethod(), nil
}

func (r *gitRepository) GetRepo() (string, error) {

	origin, err := r.sharedDir.repo.Remote("origin")
	if err != nil {
		return "", fmt.Errorf("cannot determine repository origin: %w", err)
	}

	return origin.Config().URLs[0], nil
}

func (r *gitRepository) fetchRemoteRepository(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "gitRepository::fetchRemoteRepository", trace.WithAttributes())
	defer span.End()
	start := time.Now()
	defer func() { klog.V(2).Infof("Fetching repository %q took %s", r.key.Name, time.Since(start)) }()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	err := r.doGitWithAuth(ctx, func(auth transport.AuthMethod) error {
		return r.sharedDir.withLock(func(repo *git.Repository) error {
			return repo.FetchContext(ctx, &git.FetchOptions{
				RemoteName: originName,
				Auth:       auth,
				Prune:      true,
				CABundle:   r.caBundle,
			})
		})
	})

	allowedErrors := []error{
		nil,
		git.NoErrAlreadyUpToDate,
		transport.ErrEmptyRemoteRepository,
	}

	if slices.Contains(allowedErrors, err) {
		return nil
	}

	return fmt.Errorf("cannot fetch repository %s: %w", r.Key(), err)
}

// Verifies repository. Repository must be fetched already.
func (r *gitRepository) verifyRepository(ctx context.Context, opts *GitRepositoryOptions) error {
	// When opening a temporary repository, such as for cloning a package
	// from unregistered upstream, we won't be pushing into the remote so
	// we don't need to verify presence of the main branch.
	if opts.MainBranchStrategy == SkipVerification {
		return nil
	}

	var needsCreation bool
	var commitHash plumbing.Hash
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		_, err := repo.Reference(r.branch.refInLocal(), false)
		if err != nil {
			switch opts.MainBranchStrategy {
			case ErrorIfMissing:
				return fmt.Errorf("branch %q doesn't exist: %v", r.branch, err)
			case CreateIfMissing:
				klog.Infof("Creating branch %s in repository %s", r.branch, r.Key().Name)
				if err := r.createBranch(repo, r.branch); err != nil {
					return err
				}
				// Get the commit hash for pushing
				ref, err := repo.Reference(r.branch.refInLocal(), false)
				if err != nil {
					return err
				}
				commitHash = ref.Hash()
				needsCreation = true
			default:
				return fmt.Errorf("unknown main branch strategy %q", opts.MainBranchStrategy.String())
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Push the branch outside of the lock to avoid race conditions
	if needsCreation {
		refSpecs := newPushRefSpecBuilder()
		refSpecs.addRefToPush(commitHash, r.branch.refInLocal())
		if err := r.pushAndCleanup(ctx, refSpecs, nil); err != nil {
			return fmt.Errorf("error pushing main branch %q: %v", r.branch, err)
		}
	}
	return nil
}

// createBranch creates the provided branch by creating a commit containing
// a README.md file on the root of the repo and then pushing it to the branch.
func (r *gitRepository) createBranch(repo *git.Repository, branch branchName) error {
	fileHash, err := storeBlob(repo, fileContent)
	if err != nil {
		return err
	}

	tree := &object.Tree{}
	tree.Entries = append(tree.Entries, object.TreeEntry{
		Name: fileName,
		Mode: filemode.Regular,
		Hash: fileHash,
	})

	treeEo := repo.Storer.NewEncodedObject()
	if err := tree.Encode(treeEo); err != nil {
		return err
	}

	treeHash, err := repo.Storer.SetEncodedObject(treeEo)
	if err != nil {
		return err
	}

	now := time.Now()
	commit := &object.Commit{
		Author: object.Signature{
			Name:  porchSignatureName,
			Email: porchSignatureEmail,
			When:  now,
		},
		Committer: object.Signature{
			Name:  porchSignatureName,
			Email: porchSignatureEmail,
			When:  now,
		},
		Message:  commitMessage,
		TreeHash: treeHash,
	}
	commitHash, err := storeCommit(repo, commit)
	if err != nil {
		return err
	}

	// Create local reference
	ref := plumbing.NewHashReference(branch.refInLocal(), commitHash)
	return repo.Storer.SetReference(ref)
}

func (r *gitRepository) commitPackageToMainInRepo(ctx context.Context, repo *git.Repository, d *gitPackageRevisionDraft) (commitHash, newPackageTreeHash plumbing.Hash, err error) {
	branch := r.branch
	localRef := branch.refInLocal()

	// Find localTarget branch - get latest state
	localTarget, err := repo.Reference(localRef, true)
	if err != nil {
		return plumbing.ZeroHash, plumbing.ZeroHash, fmt.Errorf("failed to find 'main' branch: %w", err)
	}
	headCommit, err := repo.CommitObject(localTarget.Hash())
	if err != nil {
		return plumbing.ZeroHash, plumbing.ZeroHash, fmt.Errorf("failed to resolve main branch to commit: %w", err)
	}

	// Verify package path and tree before committing
	packagePath := d.Key().PkgKey.ToFullPathname()

	if !d.tree.IsZero() {
		if _, err := repo.TreeObject(d.tree); err != nil {
			if strings.Contains(err.Error(), "packfile not found") || strings.Contains(err.Error(), "cannot read existing tree") {
				return plumbing.ZeroHash, plumbing.ZeroHash, fmt.Errorf("local git cache corruption detected for package %s - restart Porch to reinitialize cache: %w", packagePath, err)
			}
			return plumbing.ZeroHash, plumbing.ZeroHash, fmt.Errorf("failed to load package tree %s for %s: %w", d.tree.String(), packagePath, err)
		}
	}

	ch, err := newCommitHelper(repo, r.userInfoProvider, headCommit.Hash, packagePath, d.tree)
	if err != nil {
		if strings.Contains(err.Error(), "packfile not found") || strings.Contains(err.Error(), "cannot read existing tree") {
			return plumbing.ZeroHash, plumbing.ZeroHash, fmt.Errorf("local git cache corruption detected for package %s - restart Porch to reinitialize cache: %w", packagePath, err)
		}
		return plumbing.ZeroHash, plumbing.ZeroHash, fmt.Errorf("failed to initialize commit of package %s to %s: %w", packagePath, localRef, err)
	}

	message, err := annotateCommitMessage(fmt.Sprintf(commitMessageApproveTemplate, d.Key().PkgKey.ToFullPathname(), d.Key().Revision), &gitAnnotation{
		PackagePath:   d.Key().PkgKey.ToFullPathname(),
		WorkspaceName: d.Key().WorkspaceName,
		Revision:      repository.Revision2Str(d.Key().Revision),
	})
	if err != nil {
		return plumbing.ZeroHash, plumbing.ZeroHash, fmt.Errorf("failed annotation commit message for package %s: %v", d.Key().PkgKey.ToFullPathname(), err)
	}
	hash, treeHash, err := ch.commit(ctx, message, d.Key().PkgKey.ToFullPathname(), d.commit)
	if err != nil {
		return plumbing.ZeroHash, plumbing.ZeroHash, fmt.Errorf("failed to commit package %s to %s: %w", d.Key().PkgKey.ToFullPathname(), localRef, err)
	}

	if hash.IsZero() {
		return plumbing.ZeroHash, plumbing.ZeroHash, fmt.Errorf("commit returned zero hash for package %s", d.Key().PkgKey.ToFullPathname())
	}

	klog.Infof("Created commit %s for package %s on main branch", hash.String(), d.Key().PkgKey.ToFullPathname())
	return hash, treeHash, nil
}

func (r *gitRepository) createPackageDeleteCommitInRepo(ctx context.Context, repo *git.Repository, branch plumbing.ReferenceName, prKey repository.PackageRevisionKey) (plumbing.Hash, error) {
	var zero plumbing.Hash
	// find the branch
	ref, err := repo.Reference(branch, true)
	if err != nil {
		klog.Infof("Branch %q no longer exist, deleting a package from it is unnecessary", branch)
		return plumbing.ZeroHash, nil
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to resolve main branch to commit: %w", err)
	}
	root, err := commit.Tree()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to find commit tree for %s: %w", ref, err)
	}

	// Find the package in the tree - ensure exact path match
	packagePath := prKey.PKey().ToFullPathname()
	entry, err := root.FindEntry(packagePath)
	switch err {
	case object.ErrEntryNotFound:
		klog.Infof("Package %q not found in branch %q - may have been deleted or never created", packagePath, branch)
		return plumbing.ZeroHash, nil
	case nil:
		// Verify this is actually a directory (package) and not a file with similar name
		if entry.Mode != filemode.Dir {
			klog.Warningf("Path %q exists but is not a directory in branch %q - skipping deletion", packagePath, branch)
			return plumbing.ZeroHash, nil
		}
		klog.Infof("Found package directory %q in branch %q, proceeding with deletion", packagePath, branch)
	default:
		klog.Warningf("Error finding package %q in branch %q: %v - treating as not found", packagePath, branch, err)
		return plumbing.ZeroHash, nil
	}

	ch, err := newCommitHelper(repo, r.userInfoProvider, commit.Hash, prKey.PKey().ToFullPathname(), zero)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to initialize commit of package %q to %q: %w", prKey.PKey().ToFullPathname(), ref, err)
	}

	message := fmt.Sprintf("Delete %s", prKey.PKey().ToFullPathname())
	hash, _, err := ch.commit(ctx, message, prKey.PKey().ToFullPathname())
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to commit package %q to %q: %w", prKey.PKey().ToFullPathname(), ref, err)
	}
	return hash, nil
}

func (r *gitRepository) executeCommitOperations(ctx context.Context, repo *git.Repository, ph *pushRefSpecBuilder, commitOps *commitOperationBuilder) error {
	for _, op := range commitOps.getOperations() {
		switch op.opType {
		case "approval":
			data := op.data.(map[string]interface{})
			d := data["draft"].(*gitPackageRevisionDraft)
			tag := data["tag"].(plumbing.ReferenceName)

			commitHash, newTreeHash, err := r.commitPackageToMainInRepo(ctx, repo, d)
			if err != nil {
				return err
			}

			ph.addRefToPush(commitHash, r.branch.refInLocal())
			ph.addRefToPush(commitHash, tag)

			d.commit = commitHash
			d.tree = newTreeHash

		case "deletion":
			data := op.data.(map[string]interface{})
			branch := data["branch"].(plumbing.ReferenceName)
			prKey := data["prKey"].(repository.PackageRevisionKey)

			commitHash, err := r.createPackageDeleteCommitInRepo(ctx, repo, branch, prKey)
			if err != nil {
				return err
			}

			if !commitHash.IsZero() {
				ph.addRefToPush(commitHash, branch)
			}
		}
	}
	return nil
}

func (r *gitRepository) pushAndCleanup(ctx context.Context, ph *pushRefSpecBuilder, commitOps *commitOperationBuilder) error {
	ctx, span := tracer.Start(ctx, "gitRepository::pushAndCleanup")
	defer span.End()

	klog.InfoS("[Git] Pushing changes to remote Git repository started",
		context1.LogMetadataFromWithExtras(ctx, "repository", r.key.Name)...)
	defer func() {
		klog.V(3).InfoS("[Git] Pushing changes to remote Git repository completed",
			context1.LogMetadataFromWithExtras(ctx, "repository", r.key.Name)...)
	}()

	maxRetries := r.repoOperationRetryAttempts
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := r.doGitWithAuth(ctx, func(auth transport.AuthMethod) error {
			return r.sharedDir.withLock(func(repo *git.Repository) error {
				if fetchErr := repo.Fetch(&git.FetchOptions{
					RemoteName: originName,
					Auth:       auth,
					CABundle:   r.caBundle,
				}); fetchErr != nil && fetchErr != git.NoErrAlreadyUpToDate {
					if attempt > 1 {
						klog.Warningf("Failed to fetch before retry %d: %v", attempt, fetchErr)
					}
				}
				// Execute commit operations with latest state - Approve and Delete
				if commitOps != nil {
					if err := r.executeCommitOperations(ctx, repo, ph, commitOps); err != nil {
						return err
					}
				}

				ph.updateRequiredRefs(repo)

				specs, require, err := ph.buildRefSpecs()
				if err != nil {
					return err
				}

				pushStart := time.Now()
				if len(require) > 0 {
					klog.Infof("git push attempt %d: pushing %v to %s/%s, requiring %v", attempt, specs, originName, r.key.Name, require)
				} else {
					klog.Infof("git push attempt %d: pushing %v to %s/%s", attempt, specs, originName, r.key.Name)
				}

				pushErr := repo.Push(&git.PushOptions{
					RemoteName: originName,
					RefSpecs:   specs,
					Auth:       auth,
					Force:      false,
					CABundle:   r.caBundle,
				})
				if pushErr != nil {
					klog.Warningf("git push failed attempt %d: %v", attempt, pushErr)
				} else {
					klog.Infof("git push completed: pushed %v to %s/%s in %s", specs, originName, r.key.Name, time.Since(pushStart))
				}
				return pushErr
			})
		}); err != nil {
			if err == git.NoErrAlreadyUpToDate {
				return nil
			}
			if attempt >= maxRetries {
				klog.V(2).Infof("Max retries (%d) reached, returning error: %v", maxRetries, err)
				return err
			}
			isRetryable := false
			for _, pattern := range retryableErrors {
				if strings.Contains(err.Error(), pattern) {
					klog.V(2).Infof("Pattern %q matched! Error is retryable", pattern)
					isRetryable = true
					break
				}
			}

			if !isRetryable {
				klog.V(2).Infof("Error %q is not retryable as it's in the allowed retryable list", err)
				return err
			}

			baseDelay := time.Duration(attempt) * baseRetryDelay
			if strings.Contains(err.Error(), "pre-receive hook declined") {
				baseDelay = time.Duration(attempt) * hookRetryDelay
			}
			time.Sleep(baseDelay)
			continue
		}
		return nil
	}
	return fmt.Errorf("push failed after %d attempts", maxRetries)
}

func (r *gitRepository) loadTasks(_ context.Context, startCommit *object.Commit, key repository.PackageRevisionKey) ([]porchapi.Task, error) {
	var tasks []porchapi.Task
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		var logOptions = git.LogOptions{
			From:  startCommit.Hash,
			Order: git.LogOrderCommitterTime,
		}

		commits, err := repo.Log(&logOptions)
		if err != nil {
			return pkgerrors.Wrap(err, "error walking commits")
		}

		done := false
		visitCommit := func(commit *object.Commit) error {
			if done {
				return nil
			}

			gitAnnotations, err := extractGitAnnotations(commit)
			if err != nil {
				klog.Warningf("Error extracting (some) git annotations from commit %q: %s", commit.Hash, err)
			}

			for _, gitAnnotation := range gitAnnotations {
				packageMatches := gitAnnotation.PackagePath == key.PkgKey.ToFullPathname()
				workspaceNameMatches := gitAnnotation.WorkspaceName == key.WorkspaceName ||
					// this is needed for porch package revisions created before the workspaceName field existed
					(gitAnnotation.Revision == string(key.WorkspaceName) && gitAnnotation.WorkspaceName == "")

				if packageMatches && workspaceNameMatches {
					// We are iterating through the commits in reverse order.
					// Tasks that are read from separate commits will be recorded in
					// reverse order.
					// The entire `tasks` slice will get reversed later, which will give us the
					// tasks in chronological order.
					if gitAnnotation.Task != nil && porchapi.IsValidFirstTaskType(gitAnnotation.Task.Type) {
						tasks = []porchapi.Task{*gitAnnotation.Task}
					}

					if gitAnnotation.Task != nil && (gitAnnotation.Task.Type == porchapi.TaskTypeClone || gitAnnotation.Task.Type == porchapi.TaskTypeInit) {
						// we have reached the beginning of this package revision and don't need to
						// continue further
						done = true
						break
					}
				}
			}

			// TODO: If a commit has no annotations defined, we should treat it like a patch.
			// This will allow direct manipulation of the git repo.
			// We should also probably _not_ record an annotation for a patch task, so we
			// can allow direct editing.
			return nil
		}

		return visitCommitsCollectErrors(commits, visitCommit)
	})
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "errors loading tasks")
	}

	// We need to reverse the tasks so they appear in chronological order
	util.SafeReverse(tasks)

	return tasks, nil
}

func visitCommitsCollectErrors(iterator object.CommitIter, callback commitCallback) error {
	ec := errors.NewErrorCollector().WithSeparator(";").WithFormat("{%s}")
	for c, itErr := iterator.Next(); itErr == nil; c, itErr = iterator.Next() {
		err := callback(c)
		if err != nil {
			if pkgerrors.Is(err, storer.ErrStop) {
				break
			}
			ec.Add(pkgerrors.Wrapf(err, "error visiting commit %s", c.Hash))
		}
	}

	return ec.Join()
}

func (r *gitRepository) getResource(hash plumbing.Hash, filePath string) (string, error) {
	var content string
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		tree, err := repo.TreeObject(hash)
		if err != nil {
			return err
		}

		file, err := tree.File(filePath)
		if err != nil {
			return err
		}

		content, err = file.Contents()
		return err
	})
	return content, err
}

func (r *gitRepository) getResources(hash plumbing.Hash) (map[string]string, error) {
	resources := map[string]string{}

	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		tree, err := repo.TreeObject(hash)
		if err != nil {
			return err
		}

		// Files() iterator iterates recursively over all files in the tree.
		fit := tree.Files()
		defer fit.Close()
		for {
			file, err := fit.Next()
			if err == io.EOF {
				break
			} else if err != nil {
				return fmt.Errorf("failed to load package resources: %w", err)
			}

			content, err := file.Contents()
			if err != nil {
				return fmt.Errorf("failed to read package file contents: %q, %w", file.Name, err)
			}

			// TODO: decide whether paths should include package directory or not.
			resources[file.Name] = content
			//resources[path.Join(p.path, file.Name)] = content
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return resources, nil
}

// findLatestPackageCommit returns the latest commit from the history that pertains
// to the package given by the packagePath. If no commit is found, it will return nil and an error.
func (r *gitRepository) findLatestPackageCommit(startCommit *object.Commit, key repository.PackageKey) (*object.Commit, error) {
	var result *object.Commit
	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		var logOptions = git.LogOptions{
			From:  startCommit.Hash,
			Order: git.LogOrderCommitterTime,
		}

		commits, err := repo.Log(&logOptions)
		if err != nil {
			return pkgerrors.Wrap(err, "error walking commits")
		}

		for c, err := commits.Next(); err == nil; c, err = commits.Next() {
			gitAnnotations, err := extractGitAnnotations(c)
			if err != nil {
				klog.Warningf("Error extracting (some) annotations from commit %q: %s", c.Hash, err)
			}

			for _, ann := range gitAnnotations {
				if ann.PackagePath == key.ToFullPathname() {
					result = c
					return nil
				}
			}
		}

		return pkgerrors.Errorf("could not find latest commit for package %s", key.ToFullPathname())
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// commitCallback is the function type that needs to be provided to the history iterator functions.
type commitCallback func(*object.Commit) error

func (r *gitRepository) GetLifecycle(ctx context.Context, pkgRev *gitPackageRevision) porchapi.PackageRevisionLifecycle {
	_, span := tracer.Start(ctx, "gitRepository::GetLifecycle")
	defer span.End()

	r.mutex.Lock()
	defer r.mutex.Unlock()

	return r.getLifecycle(pkgRev)
}

func (r *gitRepository) getLifecycle(pkgRev *gitPackageRevision) porchapi.PackageRevisionLifecycle {
	switch ref := pkgRev.ref; {
	case ref == nil:
		return r.checkPublishedLifecycle(pkgRev)
	case isDraftBranchNameInLocal(ref.Name()):
		return porchapi.PackageRevisionLifecycleDraft
	case isProposedBranchNameInLocal(ref.Name()):
		return porchapi.PackageRevisionLifecycleProposed
	default:
		return r.checkPublishedLifecycle(pkgRev)
	}
}

func (r *gitRepository) checkPublishedLifecycle(pkgRev *gitPackageRevision) porchapi.PackageRevisionLifecycle {
	if r.deletionProposedCache == nil {
		if err := r.updateDeletionProposedCache(); err != nil {
			klog.Errorf("failed to update deletionProposed cache: %v", err)
			return porchapi.PackageRevisionLifecyclePublished
		}
	}

	branchName := createDeletionProposedName(pkgRev.Key())
	if _, found := r.deletionProposedCache[branchName]; found {
		return porchapi.PackageRevisionLifecycleDeletionProposed
	}

	return porchapi.PackageRevisionLifecyclePublished
}

func (r *gitRepository) UpdateLifecycle(ctx context.Context, pkgRev *gitPackageRevision, newLifecycle porchapi.PackageRevisionLifecycle) error {
	ctx, span := tracer.Start(ctx, "gitRepository::UpdateLifecycle", trace.WithAttributes())
	defer span.End()

	oldLifecycle := pkgRev.Lifecycle(ctx)
	klog.InfoS("[Git] Updating lifecycle started for PackageRevision",
		context1.LogMetadataFromWithExtras(ctx, "old", oldLifecycle, "new", newLifecycle)...)
	defer func() {
		klog.V(3).InfoS("[Git] Updating lifecycle completed for PackageRevision",
			context1.LogMetadataFromWithExtras(ctx, "old", oldLifecycle, "new", newLifecycle)...)
	}()

	r.mutex.Lock()
	old := r.getLifecycle(pkgRev)
	if !porchapi.LifecycleIsPublished(old) {
		r.mutex.Unlock()
		return fmt.Errorf("cannot update lifecycle for draft package revision")
	}
	r.mutex.Unlock()

	r.mutex.Lock()
	refSpecs := newPushRefSpecBuilder()
	deletionProposedBranch := createDeletionProposedName(pkgRev.Key())

	switch old {
	case porchapi.PackageRevisionLifecyclePublished:
		if newLifecycle != porchapi.PackageRevisionLifecycleDeletionProposed {
			r.mutex.Unlock()
			return fmt.Errorf("invalid new lifecycle value: %q", newLifecycle)
		}
		// Push the package revision into a deletionProposed branch.
		r.deletionProposedCache[deletionProposedBranch] = true
		refSpecs.addRefToPush(pkgRev.commit, deletionProposedBranch.refInLocal())
	case porchapi.PackageRevisionLifecycleDeletionProposed:
		if newLifecycle != porchapi.PackageRevisionLifecyclePublished {
			r.mutex.Unlock()
			return fmt.Errorf("invalid new lifecycle value: %q", newLifecycle)
		}
		// Delete the deletionProposed branch
		delete(r.deletionProposedCache, deletionProposedBranch)
		ref := plumbing.NewHashReference(deletionProposedBranch.refInLocal(), pkgRev.commit)
		refSpecs.addRefToDelete(ref)
	}
	r.mutex.Unlock() // Release mutex before git operations

	if err := r.pushAndCleanup(ctx, refSpecs, nil); err != nil {
		if !pkgerrors.Is(err, git.NoErrAlreadyUpToDate) {
			return err
		}
	}

	return nil
}

func (r *gitRepository) UpdateDraftResources(ctx context.Context, draft *gitPackageRevisionDraft, new *porchapi.PackageRevisionResources, change *porchapi.Task) error {
	ctx, span := tracer.Start(ctx, "gitRepository::UpdateResources", trace.WithAttributes())
	defer span.End()

	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		ch, err := newCommitHelper(repo, r.userInfoProvider, draft.commit, draft.Key().PkgKey.ToFullPathname(), plumbing.ZeroHash)
		if err != nil {
			return pkgerrors.Wrap(err, "failed to commit package:")
		}

		for k, v := range new.Spec.Resources {
			if err := ch.storeFile(filepath.Join(draft.Key().PkgKey.ToFullPathname(), k), v); err != nil {
				return err
			}
		}

		// Because we can't read the package back without a Kptfile, make sure one is present
		{
			p := filepath.Join(draft.Key().PkgKey.ToFullPathname(), "Kptfile")
			_, err := ch.readFile(p)
			if os.IsNotExist(err) {
				// We could write the file here; currently we return an error
				return pkgerrors.Wrap(err, "package must contain Kptfile at root")
			}
		}

		annotation := &gitAnnotation{
			PackagePath:   draft.Key().PkgKey.ToFullPathname(),
			WorkspaceName: draft.Key().WorkspaceName,
			Revision:      repository.Revision2Str(draft.Key().Revision),
			Task:          change,
		}
		message := formatCommitMessage(porchapi.TaskTypeNone)
		if change != nil {
			message = formatCommitMessage(change.Type)
			if porchapi.IsValidFirstTaskType(change.Type) {
				if len(draft.tasks) > 0 {
					klog.Warningf("Replacing first task of %q", draft.Key())
				}
				draft.tasks = []porchapi.Task{*change}
			}
		}
		message += "\n"

		message, err = annotateCommitMessage(message, annotation)
		if err != nil {
			return err
		}

		commitHash, packageTree, err := ch.commit(ctx, message, draft.Key().PkgKey.ToFullPathname())
		if err != nil {
			return pkgerrors.Wrap(err, "failed to commit package: %w")
		}

		draft.tree = packageTree
		draft.commit = commitHash
		return nil
	})
	return err
}

func (r *gitRepository) ClosePackageRevisionDraft(ctx context.Context, prd repository.PackageRevisionDraft, version int) (repository.PackageRevision, error) {
	ctx, span := tracer.Start(ctx, "gitRepository::ClosePackageRevisionDraft", trace.WithAttributes())
	defer span.End()

	d := prd.(*gitPackageRevisionDraft)
	klog.InfoS("[Git] Changing lifecycle and pushing to Git started for PackageRevision",
		context1.LogMetadataFromWithExtras(ctx, "lifecycle", d.lifecycle)...)
	defer func() {
		klog.V(3).InfoS("[Git] Changing lifecycle and pushing to Git completed for PackageRevision",
			context1.LogMetadataFromWithExtras(ctx, "lifecycle", d.lifecycle)...)
	}()

	refSpecs := newPushRefSpecBuilder()
	commitOps := newCommitOperationBuilder()
	draftBranch := createDraftName(d.Key())
	proposedBranch := createProposedName(d.Key())
	targetBranch := string(r.branch)

	var newRef *plumbing.Reference

	switch d.lifecycle {
	case porchapi.PackageRevisionLifecyclePublished, porchapi.PackageRevisionLifecycleDeletionProposed:

		if version == 0 {
			return nil, pkgerrors.New("Version cannot be empty for the next package revision")
		}
		r.mutex.Lock()
		d.prKey.Revision = version
		r.mutex.Unlock()

		// Finalize the package revision. Commit will be created in pushAndCleanup
		tag := createFinalTagNameInLocal(d.Key())
		commitOps.addPackageApproval(d, tag)

		// Delete base branch (if one exists and should be deleted)
		switch base := d.base; {
		case base == nil: // no branch to delete
		case base.Name() == draftBranch.refInLocal(), base.Name() == proposedBranch.refInLocal():
			refSpecs.addRefToDelete(base)
		}

		// Reference will be created after successful commit in pushAndCleanup
		newRef = nil

	case porchapi.PackageRevisionLifecycleProposed:
		// Push the package revision into a proposed branch.
		refSpecs.addRefToPush(d.commit, proposedBranch.refInLocal())
		// Set the target to the proposed branch.
		targetBranch = string(proposedBranch)

		// Delete base branch (if one exists and should be deleted)
		switch base := d.base; {
		case base == nil: // no branch to delete
		case base.Name() != proposedBranch.refInLocal():
			refSpecs.addRefToDelete(base)
		}

		// Update package reference (commit and tree hash stay the same)
		newRef = plumbing.NewHashReference(proposedBranch.refInLocal(), d.commit)

	case porchapi.PackageRevisionLifecycleDraft:
		// Push the package revision into a draft branch.
		refSpecs.addRefToPush(d.commit, draftBranch.refInLocal())
		// Set the target to the draft branch.
		targetBranch = string(draftBranch)
		// Delete base branch (if one exists and should be deleted)
		switch base := d.base; {
		case base == nil: // no branch to delete
		case base.Name() != draftBranch.refInLocal():
			refSpecs.addRefToDelete(base)
		}

		// Update package reference (commit and tree hash stay the same)
		newRef = plumbing.NewHashReference(draftBranch.refInLocal(), d.commit)

	default:
		return nil, fmt.Errorf("package has unrecognized lifecycle: %q", d.lifecycle)
	}

	if err := d.repo.pushAndCleanup(ctx, refSpecs, commitOps); err != nil {
		if !pkgerrors.Is(err, git.NoErrAlreadyUpToDate) {
			klog.Errorf("Failed to push package %s to %s: %v", d.Key().PkgKey.ToFullPathname(), targetBranch, err)
			return nil, err
		}
	} else {
		klog.Infof("Successfully pushed package %s to %s branch", d.Key().PkgKey.ToFullPathname(), targetBranch)
	}

	// Create reference after successful push for published packages
	if newRef == nil {
		tag := createFinalTagNameInLocal(d.Key())
		newRef = plumbing.NewHashReference(tag, d.commit)
	}

	// for backwards compatibility with packages that existed before porch supported
	// descriptions, we populate the workspaceName as the revision number if it is empty
	if d.prKey.WorkspaceName == "" {
		d.prKey.WorkspaceName = "v" + repository.Revision2Str(d.Key().Revision)
	}

	var commitHash plumbing.Hash
	if newRef != nil {
		commitHash = newRef.Hash()
	}

	// Set publish metadata on the actual Proposed → Published transition.
	var updatedTime time.Time
	var updatedBy string
	if d.lifecycle == porchapi.PackageRevisionLifecyclePublished {
		updatedTime = time.Now()
		if r.userInfoProvider != nil {
			if userInfo := r.userInfoProvider.GetUserInfo(ctx); userInfo != nil {
				updatedBy = userInfo.Email
			}
		}
	}

	return &gitPackageRevision{
		prKey:     d.prKey,
		repo:      d.repo,
		updated:   updatedTime,
		updatedBy: updatedBy,
		ref:       newRef,
		tree:      d.tree,
		commit:    commitHash,
		tasks:     d.tasks,
	}, nil
}

// doGitWithAuth fetches auth information for git and provides it
// to the provided function which performs the operation against a git repo.
func (r *gitRepository) doGitWithAuth(ctx context.Context, op func(transport.AuthMethod) error) error {
	auth, err := r.getAuthMethod(ctx, false)
	if err != nil {
		return fmt.Errorf("failed to obtain git credentials: %w", err)
	}
	err = op(auth)
	if err != nil {
		if !pkgerrors.Is(err, transport.ErrAuthenticationRequired) {
			return err
		}
		klog.Infof("Authentication failed. Trying to refresh credentials")
		// TODO: Consider having some kind of backoff here.
		auth, err := r.getAuthMethod(ctx, true)
		if err != nil {
			return fmt.Errorf("failed to obtain git credentials: %w", err)
		}
		return op(auth)
	}
	return nil
}

// findPackage finds the packages in the git repository, under commit, if it is exists at path.
// If no package is found at that path, returns nil, nil
func (r *gitRepository) findPackage(commit *object.Commit, packagePath string) (*packageListEntry, error) {
	t, err := r.discoverPackagesInTree(commit, discoverPackagesOptions{filterPrefix: packagePath, recurse: false})
	if err != nil {
		return nil, err
	}
	return t.packages[packagePath], nil
}

// discoverPackagesInTree finds the packages in the git repository, under commit.
// If filterPrefix is non-empty, only packages with the specified prefix will be returned.
// It is not an error if filterPrefix matches no packages or even is not a real directory name;
// we will simply return an empty list of packages.
func (r *gitRepository) discoverPackagesInTree(commit *object.Commit, opt discoverPackagesOptions) (*packageList, error) {
	t := &packageList{
		parent:   r,
		commit:   commit,
		packages: make(map[string]*packageListEntry),
	}

	err := r.sharedDir.withLock(func(repo *git.Repository) error {
		rootTree, err := commit.Tree()
		if err != nil {
			return pkgerrors.Wrapf(err, "cannot resolve commit %v to tree (corrupted repository?)", commit.Hash)
		}

		if opt.filterPrefix != "" {
			tree, err := rootTree.Tree(opt.filterPrefix)
			if err != nil {
				if err == object.ErrDirectoryNotFound {
					// We treat the filter prefix as a filter, the path doesn't have to exist
					klog.Warningf("could not find filterPrefix %q in commit %v; returning no packages", opt.filterPrefix, commit.Hash)
					return nil
				} else {
					return pkgerrors.Wrapf(err, "error getting tree %s", opt.filterPrefix)
				}
			}
			rootTree = tree
		}

		if rootTree != nil {
			return t.discoverPackages(r.Key(), rootTree, opt.filterPrefix, opt.recurse, repo)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if opt.filterPrefix == "" {
		klog.Infof("discovered %d packages @%v", len(t.packages), commit.Hash)
	}
	return t, nil
}

func (r *gitRepository) Refresh(ctx context.Context) error {
	return r.UpdateDeletionProposedCache(ctx)
}

// getPkgWorkspace returns the workspace name as parsed from the kpt annotations from the latest commit for the package.
// If such a commit is not found, it returns an empty string.
func getPkgWorkspace(commit *object.Commit, p *packageListEntry, ref *plumbing.Reference) string {
	if ref == nil || (!isTagInLocalRepo(ref.Name()) && !isDraftBranchNameInLocal(ref.Name()) && !isProposedBranchNameInLocal(ref.Name())) {
		// packages on the main branch may have unrelated commits, we need to find the latest commit relevant to this package
		c, err := p.parent.parent.findLatestPackageCommit(p.parent.commit, p.pkgKey)
		if c == nil {
			if err != nil {
				klog.Warningf("Error searching for latest commit for package %s: %s", p.pkgKey, err)
			}
			return ""
		}
		commit = c
	}
	annotations, err := extractGitAnnotations(commit)
	if err != nil {
		klog.Warningf("Error extracting git annotations for package %s: %s", p.pkgKey, err)
	}
	for _, a := range annotations {
		if a.PackagePath != p.pkgKey.ToFullPathname() {
			continue
		}
		if a.WorkspaceName != "" {
			return a.WorkspaceName
		}
	}
	return ""
}

// AppendRetryableErrors adds additional error patterns to the retryable errors list.
// Patterns can be specified multiple times or as comma-separated values.
func AppendRetryableErrors(patterns []string) {
	for _, p := range patterns {
		retryableErrors = append(retryableErrors, strings.TrimSpace(p))
	}
	klog.V(2).Infof("Retryable git error patterns: [%s]", strings.Join(retryableErrors, ", "))
}
