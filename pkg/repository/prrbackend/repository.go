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

// Package prrbackend provides a git-backed repository.Repository for v1alpha2 that
// write-throughs package content into the PRRStore.
package prrbackend

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	"github.com/kptdev/porch/pkg/externalrepo"
	externalrepotypes "github.com/kptdev/porch/pkg/externalrepo/types"
	"github.com/kptdev/porch/pkg/prrstore"
	"github.com/kptdev/porch/pkg/repository"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/klog/v2"
)

var tracer = otel.Tracer("prrbackend")

var _ repository.Repository = &Repository{}

// Repository is a v1alpha2 git repository that write-throughs package content into
// the PRRStore on discovery (Refresh) and publish (ClosePackageRevisionDraft). All
// other operations delegate to the underlying git repository. It satisfies
// repository.Repository so the cache map can hold it alongside v1alpha1 handles.
type Repository struct {
	repoKey      key.RepositoryKey
	externalRepo repository.Repository
	meta         configapi.Repository
	store        prrstore.PRRStore
	// skipDraftPush mirrors the git option: Draft/Proposed closes are not pushed, so the
	// PRRStore holds them and marks them store-only.
	skipDraftPush bool
}

// NewRepository creates a v1alpha2 Repository that write-throughs git content into the PRRStore.
func NewRepository(ctx context.Context, key key.RepositoryKey, repositorySpec *configapi.Repository, store prrstore.PRRStore, externalRepoOpts externalrepotypes.ExternalRepoOptions) (repository.Repository, error) {
	if store == nil {
		return nil, fmt.Errorf("PRRStore not configured for repository %s/%s", repositorySpec.Namespace, repositorySpec.Name)
	}
	// Fail here, with the fix in the message, rather than with a missing-relation error
	// on the first write: the schema is not applied to existing databases automatically.
	if err := store.CheckSchema(ctx); err != nil {
		return nil, fmt.Errorf("repository %s/%s: %w", repositorySpec.Namespace, repositorySpec.Name, err)
	}

	externalRepo, err := externalrepo.CreateRepositoryImpl(ctx, repositorySpec, externalRepoOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create external repository for PRRStore repository: %w", err)
	}

	return &Repository{
		repoKey:      key,
		externalRepo: externalRepo,
		meta:         *repositorySpec,
		store:        store,

		skipDraftPush: externalRepoOpts.SkipDraftPush,
	}, nil
}

// Meta returns the repository metadata.
func (r *Repository) Meta() configapi.Repository {
	return r.meta
}

// KubeObjectName returns the Kubernetes object name.
func (r *Repository) KubeObjectName() string {
	return r.repoKey.Name
}

// KubeObjectNamespace returns the Kubernetes namespace.
func (r *Repository) KubeObjectNamespace() string {
	return r.repoKey.Namespace
}

// Key returns the repository key.
func (r *Repository) Key() key.RepositoryKey {
	return r.repoKey
}

// ListPackageRevisions lists git's revisions plus unpushed Draft/Proposed ones kept only
// in the PRRStore, which git discovery cannot see. For a name in both, the store wins
// unless git holds it as Published or DeletionProposed: the marker is cleared on publish,
// so a marked row for a published revision is a leftover.
func (r *Repository) ListPackageRevisions(ctx context.Context, filter repository.ListPackageRevisionFilter) ([]repository.PackageRevision, error) {
	_, span := tracer.Start(ctx, "PRRRepository::ListPackageRevisions", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("PRRRepository %+v: ListPackageRevisions", r.Key())
	gitRevs, err := r.externalRepo.ListPackageRevisions(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Filters that cannot match a Draft or Proposed revision (published-only lookups such
	// as a clone upstream) skip the store.
	if !filterMayMatchStoreOnly(filter) {
		return gitRevs, nil
	}

	storeOnly, err := r.store.ListStoreOnly(ctx, r.repoKey)
	if err != nil {
		return nil, fmt.Errorf("list store-only package revisions: %w", err)
	}
	if len(storeOnly) == 0 {
		return gitRevs, nil
	}
	return r.mergeStoreOnly(ctx, filter, gitRevs, storeOnly), nil
}

// filterMayMatchStoreOnly reports whether filter could match a Draft or Proposed revision.
func filterMayMatchStoreOnly(filter repository.ListPackageRevisionFilter) bool {
	if len(filter.Lifecycles) == 0 {
		return true
	}
	for _, lc := range filter.Lifecycles {
		if lc == porchapi.PackageRevisionLifecycleDraft || lc == porchapi.PackageRevisionLifecycleProposed {
			return true
		}
	}
	return false
}

// mergeStoreOnly overlays store-only revisions on git's before filtering, so a stale git
// Draft is replaced even if the filter would match only the git copy.
func (r *Repository) mergeStoreOnly(ctx context.Context, filter repository.ListPackageRevisionFilter, gitRevs []repository.PackageRevision, storeOnly []prrstore.StoreOnlyRevision) []repository.PackageRevision {
	byName := make(map[string]*storeDraftRevision, len(storeOnly))
	for _, so := range storeOnly {
		rev := newStoreDraftRevision(so, r.store)
		byName[rev.KubeObjectName()] = rev
	}

	merged := make([]repository.PackageRevision, 0, len(gitRevs)+len(byName))
	for _, g := range gitRevs {
		name := g.KubeObjectName()
		if _, shadowed := byName[name]; shadowed {
			if porchapi.LifecycleIsPublished(g.Lifecycle(ctx)) {
				// Git owns a published revision; the store marker is a leftover.
				delete(byName, name)
			} else {
				// The store owns an unpushed draft; drop git's older copy.
				continue
			}
		}
		merged = append(merged, g)
	}
	// Append in name order so the listing is stable between calls.
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if rev := byName[name]; filter.Matches(ctx, rev) {
			merged = append(merged, rev)
		}
	}
	return merged
}

// BranchCommitHash returns the commit hash of the branch.
func (r *Repository) BranchCommitHash(ctx context.Context) (string, error) {
	_, span := tracer.Start(ctx, "PRRRepository::BranchCommitHash", trace.WithAttributes())
	defer span.End()

	return r.externalRepo.BranchCommitHash(ctx)
}

// Version returns a version string that changes when repo contents change.
func (r *Repository) Version(ctx context.Context) (string, error) {
	_, span := tracer.Start(ctx, "PRRRepository::Version", trace.WithAttributes())
	defer span.End()

	return r.externalRepo.Version(ctx)
}

// Refresh synchronizes git and writes resources into the PRRStore.
func (r *Repository) Refresh(ctx context.Context) error {
	_, span := tracer.Start(ctx, "PRRRepository::Refresh", trace.WithAttributes())
	defer span.End()

	if err := r.externalRepo.Refresh(ctx); err != nil {
		return fmt.Errorf("PRRRepository refresh failed: %w", err)
	}

	// Fail the refresh if the store could not be populated: CRDs are created from the
	// listing, and the store is the only place their content is read from. The next
	// refresh retries, and only revisions still missing from the store are written.
	if err := r.cacheExternalPackageResources(ctx); err != nil {
		return fmt.Errorf("PRRRepository %+v: populate PRRStore from git: %w", r.Key(), err)
	}

	return nil
}

// cacheExternalPackageResources fetches git packages and writes the ones the PRRStore
// has not seen into it. A revision whose content cannot be read from git is logged and
// skipped. A failed store write is collected and returned after the rest are written,
// so one failure does not leave the others unpopulated.
func (r *Repository) cacheExternalPackageResources(ctx context.Context) error {
	pkgRevs, err := r.externalRepo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{})
	if err != nil {
		return fmt.Errorf("list external package revisions: %w", err)
	}

	var writeErrs []error
	for _, extPR := range pkgRevs {
		prKey := extPR.Key()

		// The store is authoritative for content; discovery only hydrates revisions it has
		// not seen, so a sync cannot clobber newer pushed/rendered content with git's.
		version, verr := r.store.GetResourceVersion(ctx, prKey)
		if verr != nil {
			klog.Warningf("PRRRepository %+v: failed to read the PRRStore version of PR %+v: %v", r.Key(), prKey, verr)
			writeErrs = append(writeErrs, fmt.Errorf("read resource version of %s: %w", prKey, verr))
			continue
		}
		if version != "" {
			klog.V(5).Infof("PRRRepository %+v: PR %+v already in PRRStore (version %s); skipping discovery write", r.Key(), prKey, version)
			continue
		}

		extPRResources, err := extPR.GetResources(ctx)
		if err != nil {
			klog.Warningf("PRRRepository %+v: failed to get resources for PR %+v: %v", r.Key(), prKey, err)
			continue
		}

		resources, _ := r.sanitizeResources(prKey, extPRResources)
		// Write only if no entry exists, so content stored since the check above is kept.
		_, conflict, err := r.store.WriteResourcesIfUnchanged(ctx, prKey, resources, "")
		if err != nil {
			klog.Warningf("PRRRepository %+v: failed to write resources for PR %+v: %v", r.Key(), prKey, err)
			writeErrs = append(writeErrs, fmt.Errorf("write resources for %s: %w", prKey, err))
			continue
		}
		if conflict {
			klog.V(5).Infof("PRRRepository %+v: PR %+v was stored during discovery; keeping the stored content", r.Key(), prKey)
		}
	}

	return errors.Join(writeErrs...)
}

// sanitizeResources drops files with invalid UTF-8 or NUL bytes (PostgreSQL TEXT incompatible).
func (r *Repository) sanitizeResources(prKey key.PackageRevisionKey, extPRResources *porchapi.PackageRevisionResources) (map[string]string, int64) {
	var resources map[string]string
	var resourcesSize int64
	if extPRResources == nil || extPRResources.Spec.Resources == nil {
		resources = make(map[string]string)
		resourcesSize = 0
	} else {
		resources = make(map[string]string, len(extPRResources.Spec.Resources))
		for key, val := range extPRResources.Spec.Resources {
			if !utf8.ValidString(key) || strings.Contains(key, "\x00") ||
				!utf8.ValidString(val) || strings.Contains(val, "\x00") {
				klog.Warningf("PRRRepository %+v: skipping file %q in PR %+v (not compatible with PostgreSQL TEXT)", r.Key(), key, prKey)
				continue
			}
			resources[key] = val
			resourcesSize += int64(len(val))
		}
	}
	return resources, resourcesSize
}

// CreatePackageRevisionDraft creates a draft package revision by delegating to the external git repository.
func (r *Repository) CreatePackageRevisionDraft(ctx context.Context, obj *porchapi.PackageRevision) (repository.PackageRevisionDraft, error) {
	klog.V(5).Infof("PRRRepository %+v: CreatePackageRevisionDraft for %+v", r.Key(), obj.Spec.PackageName)
	inner, err := r.externalRepo.CreatePackageRevisionDraft(ctx, obj)
	if err != nil {
		return nil, err
	}
	return newPRRDraft(inner, porchapi.PackageRevisionLifecycleDraft), nil
}

// StageDraftFromCache writes the draft's current PRRStore content onto the git draft,
// so the commit produced by ClosePackageRevisionDraft reflects the authoritative
// pushed/rendered content. A lifecycle-only transition opens the draft from git and
// stages nothing, so without this the git draft lags behind the PRRStore and the
// post-close write-back would clobber the pushed content with stale git content.
// Implements the draftSeeder interface the content accessor calls before close.
func (r *Repository) StageDraftFromCache(ctx context.Context, draft repository.PackageRevisionDraft) error {
	prKey := draft.Key()

	// Take the version before the content: a push landing in between then makes the
	// version older than the content, which the close write-back detects as a conflict.
	version, err := r.store.GetResourceVersion(ctx, prKey)
	if err != nil {
		return fmt.Errorf("staging draft from PRRStore: get resource version for %+v: %w", prKey, err)
	}
	if pd, ok := draft.(*prrDraft); ok {
		pd.markStaged(version)
	}

	resources, err := r.store.GetResources(ctx, prKey)
	if err != nil {
		return fmt.Errorf("staging draft from PRRStore: get resources for %+v: %w", prKey, err)
	}
	// No cached content (e.g. a draft that was never pushed to) — nothing to stage;
	// leave the git draft as-is.
	if len(resources) == 0 {
		klog.V(5).Infof("PRRRepository %+v: no PRRStore content to stage for draft %+v", r.Key(), prKey)
		return nil
	}

	prr := &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{Resources: resources},
	}
	// Record as a render task so it does not displace the package's creation task,
	// matching how other non-creation writes are recorded.
	task := &porchapi.Task{Type: porchapi.TaskTypeRender}
	if err := draft.UpdateResources(ctx, prr, task); err != nil {
		return fmt.Errorf("staging draft from PRRStore: update resources for %+v: %w", prKey, err)
	}

	klog.V(3).Infof("PRRRepository %+v: staged %d resources from PRRStore onto draft %+v", r.Key(), len(resources), prKey)
	return nil
}

// ClosePackageRevisionDraft closes a draft in git and writes its content to the PRRStore.
// When draft pushes are skipped, a Draft/Proposed close is not pushed, so the store holds
// the only copy: it is written with the store-only marker and a failed write is an error.
// Otherwise git holds the revision, the store copy is best-effort, and the marker is
// cleared, which hands a published revision back to git.
func (r *Repository) ClosePackageRevisionDraft(ctx context.Context, prd repository.PackageRevisionDraft, version int) (repository.PackageRevision, error) {
	inner, lifecycle := unwrapPRRDraft(prd)

	closedPR, err := r.externalRepo.ClosePackageRevisionDraft(ctx, inner, version)
	if err != nil {
		return nil, fmt.Errorf("close package revision draft: %w", err)
	}
	if closedPR == nil {
		return closedPR, nil
	}

	storeOnly := ""
	if r.skipDraftPush {
		switch lifecycle {
		case porchapi.PackageRevisionLifecycleDraft:
			storeOnly = prrstore.StoreOnlyDraft
		case porchapi.PackageRevisionLifecycleProposed:
			storeOnly = prrstore.StoreOnlyProposed
		}
	}

	prKey := closedPR.Key()
	resources, err := closedPR.GetResources(ctx)
	if err != nil {
		if storeOnly != "" {
			return nil, fmt.Errorf("read content of store-only package revision %s: %w", prKey, err)
		}
		klog.Warningf("PRRRepository %+v: failed to get resources for published PR %+v: %v", r.Key(), prKey, err)
		return closedPR, nil
	}

	sanitized, _ := r.sanitizeResources(prKey, resources)
	base, staged := "", false
	if pd, ok := prd.(*prrDraft); ok {
		base, staged = pd.stagedBase()
	}
	if err := r.writeClosedContent(ctx, prKey, sanitized, storeOnly, base, staged); err != nil {
		if storeOnly != "" {
			return nil, fmt.Errorf("write store-only package revision %s: %w", prKey, err)
		}
		// Git holds the revision. The next refresh writes it to the store if it is
		// still missing, as only revisions the store has not seen are populated.
		klog.Errorf("PRRRepository %+v: failed to cache resources for PR %+v; the next refresh will populate them: %v", r.Key(), prKey, err)
	}

	return closedPR, nil
}

// storeWriteAttempts and storeWriteRetryDelay bound the retries of the close write-back,
// which absorb a brief database interruption right after git has already been updated.
var (
	storeWriteAttempts   = 3
	storeWriteRetryDelay = 100 * time.Millisecond
)

// writeClosedContent writes the content of a closed draft to the store. A draft staged
// from the store is written only if the store still has the version it was staged from.
// If a push moved the store on in the meantime, the pushed content is kept and only the
// store-only marker is applied; overwriting it would discard an update the API accepted.
// A draft that was not staged brings its own content and replaces the store's.
func (r *Repository) writeClosedContent(ctx context.Context, prKey key.PackageRevisionKey, resources map[string]string, storeOnly, base string, staged bool) error {
	write := func() error {
		if !staged {
			_, err := r.store.WriteResourcesAs(ctx, prKey, resources, storeOnly)
			return err
		}
		_, conflict, err := r.store.WriteResourcesAsIfUnchanged(ctx, prKey, resources, storeOnly, base)
		if err != nil {
			return err
		}
		if conflict {
			klog.Warningf("PRRRepository %+v: content of %+v changed while it was being closed; keeping the newer content", r.Key(), prKey)
			return r.store.SetStoreOnlyLifecycle(ctx, prKey, storeOnly)
		}
		return nil
	}

	var err error
	for attempt := 1; attempt <= storeWriteAttempts; attempt++ {
		if err = write(); err == nil {
			return nil
		}
		if attempt == storeWriteAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(attempt) * storeWriteRetryDelay):
		}
	}
	return err
}

// DeletePackageRevision deletes a package revision. A store-only revision is in no git
// ref, so only its store row goes, and failure is an error. For a git-backed revision git
// goes first and the store row is removed best-effort, so a leftover cannot shadow a
// later revision of the same name.
func (r *Repository) DeletePackageRevision(ctx context.Context, old repository.PackageRevision) error {
	klog.V(5).Infof("PRRRepository %+v: DeletePackageRevision %+v", r.Key(), old.Key())

	if _, ok := old.(*storeDraftRevision); ok {
		if err := r.store.DeleteResources(ctx, old.Key()); err != nil {
			return fmt.Errorf("delete store-only package revision %s: %w", old.Key(), err)
		}
		return nil
	}

	if err := r.externalRepo.DeletePackageRevision(ctx, old); err != nil {
		return err
	}
	if err := r.store.DeleteResources(ctx, old.Key()); err != nil {
		klog.Warningf("PRRRepository %+v: failed to delete cached resources for %+v: %v", r.Key(), old.Key(), err)
	}
	return nil
}

// UpdatePackageRevision opens a draft for mutation. A store-only revision has no git ref,
// so a new git draft is created for it and seeded from the store.
func (r *Repository) UpdatePackageRevision(ctx context.Context, old repository.PackageRevision) (repository.PackageRevisionDraft, error) {
	klog.V(5).Infof("PRRRepository %+v: UpdatePackageRevision %+v", r.Key(), old.Key())

	if sd, ok := old.(*storeDraftRevision); ok {
		return r.openStoreDraft(ctx, sd)
	}

	inner, err := r.externalRepo.UpdatePackageRevision(ctx, old)
	if err != nil {
		return nil, err
	}
	return newPRRDraft(inner, old.Lifecycle(ctx)), nil
}

// openStoreDraft opens a store-only revision for mutation. The new git draft starts empty,
// so it is seeded from the store at once; otherwise a close with no further writes would
// replace the content with nothing.
func (r *Repository) openStoreDraft(ctx context.Context, sd *storeDraftRevision) (repository.PackageRevisionDraft, error) {
	inner, err := r.externalRepo.CreatePackageRevisionDraft(ctx, sd.toPackageRevision(ctx))
	if err != nil {
		return nil, fmt.Errorf("open git draft for store-only %s: %w", sd.Key(), err)
	}
	draft := newPRRDraft(inner, sd.lifecycle)
	if err := r.StageDraftFromCache(ctx, draft); err != nil {
		return nil, err
	}
	return draft, nil
}

// ListPackages lists packages from git.
func (r *Repository) ListPackages(ctx context.Context, filter repository.ListPackageFilter) ([]repository.Package, error) {
	_, span := tracer.Start(ctx, "PRRRepository::ListPackages", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("PRRRepository %+v: ListPackages", r.Key())
	return r.externalRepo.ListPackages(ctx, filter)
}

// Close releases resources.
func (r *Repository) Close(ctx context.Context) error {
	_, span := tracer.Start(ctx, "PRRRepository::Close", trace.WithAttributes())
	defer span.End()

	klog.V(5).Infof("PRRRepository %+v: Close", r.Key())
	return r.externalRepo.Close(ctx)
}
