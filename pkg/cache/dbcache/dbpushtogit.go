// Copyright 2025 The kpt Authors
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

package dbcache

import (
	"context"
	"database/sql"
	"fmt"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	pctx "github.com/kptdev/porch/pkg/util/context"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/klog/v2"
)

func PushPublishedPackageRevision(ctx context.Context, repo repository.Repository, pr repository.PackageRevision, pushDraftsToGit, existingGitBranch bool) (kptfilev1.Locator, error) {
	ctx, span := tracer.Start(ctx, "PushPackageRevision", trace.WithAttributes())
	defer span.End()

	prName := repository.ComposePkgRevObjName(pr.Key())
	if pushDraftsToGit {
		klog.InfoS("[DBCache] Pushing PackageRevision to repository and to Git for PackageRevision",
			pctx.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
	} else {
		klog.InfoS("[DBCache] Pushing PackageRevision to repository for PackageRevision",
			pctx.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
	}
	defer func() {
		klog.V(3).InfoS("[DBCache] Push PackageRevision to repository completed for PackageRevision",
			pctx.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
	}()

	prLifecycle := pr.Lifecycle(ctx)
	if prLifecycle != porchapi.PackageRevisionLifecyclePublished {
		return kptfilev1.Locator{}, fmt.Errorf("cannot push package revision %+v, package revision lifecycle is %q, it should be \"Published\"", pr.Key(), prLifecycle)
	}

	apiPr, err := pr.GetPackageRevision(ctx)
	if err != nil {
		return kptfilev1.Locator{}, pkgerrors.Wrapf(err, "push of package revision %+v to repository %+v failed, could not get API definition:", pr.Key(), repo.Key())
	}

	resources, err := pr.GetResources(ctx)
	if err != nil {
		return kptfilev1.Locator{}, pkgerrors.Wrapf(err, "push of package revision %+v to repository %+v failed, could not get package revision resources:", pr.Key(), repo.Key())
	}

	var draft repository.PackageRevisionDraft
	var foundExisting bool

	if pushDraftsToGit && existingGitBranch {
		existingPRs, err := repo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
			Key: repository.PackageRevisionKey{
				PkgKey:        pr.Key().PkgKey,
				WorkspaceName: pr.Key().WorkspaceName,
			},
		})

		if err == nil && len(existingPRs) > 0 {
			draft, err = repo.UpdatePackageRevision(ctx, existingPRs[0])
			if err != nil {
				return kptfilev1.Locator{}, pkgerrors.Wrapf(err, "push of package revision %+v to repository %+v failed, could not update existing package revision:", pr.Key(), repo.Key())
			}

			if err = draft.UpdateResources(ctx, resources, commitTaskForPublishedPush(apiPr.Spec.Tasks, true)); err != nil {
				return kptfilev1.Locator{}, pkgerrors.Wrapf(err, "push of package revision %+v to repository %+v failed, could not update package revision resources on existing draft:", pr.Key(), repo.Key())
			}
			foundExisting = true
		}
	}

	if !foundExisting {
		draft, err = repo.CreatePackageRevisionDraft(ctx, apiPr)
		if err != nil {
			return kptfilev1.Locator{}, pkgerrors.Wrapf(err, "push of package revision %+v to repository %+v failed, could not create package revision draft:", pr.Key(), repo.Key())
		}

		if err = draft.UpdateResources(ctx, resources, commitTaskForPublishedPush(apiPr.Spec.Tasks, false)); err != nil {
			return kptfilev1.Locator{}, pkgerrors.Wrapf(err, "push of package revision %+v to repository %+v failed, could not update package revision resources:", pr.Key(), repo.Key())
		}
	}

	if err = draft.UpdateLifecycle(ctx, porchapi.PackageRevisionLifecyclePublished); err != nil {
		return kptfilev1.Locator{}, pkgerrors.Wrapf(err, "push of package revision %+v to repository %+v failed, could not update package revision draft lifecycle to \"Published\":", pr.Key(), repo.Key())
	}

	pushedPR, err := repo.ClosePackageRevisionDraft(ctx, draft, pr.Key().Revision)
	if err != nil {
		return kptfilev1.Locator{}, pkgerrors.Wrapf(err, "push of package revision %+v to repository %+v failed, could not close package revision draft:", pr.Key(), repo.Key())
	}

	_, pushedPRUpstreamLock, err := pushedPR.GetLock(ctx)
	if err != nil {
		return kptfilev1.Locator{}, pkgerrors.Wrapf(err, "read of upstream lock for package revision %+v pushed to repository %+v failed", pr.Key(), repo.Key())
	}

	return pushedPRUpstreamLock, nil
}

func PushDraftPackageRevision(ctx context.Context, repoKey repository.RepositoryKey, pr *dbPackageRevision) {
	prKey := pr.Key()
	klog.Infof("PushDraftPackageRevision: repo %+v started for %+v", repoKey, prKey)

	pkgMutex := getOrInsertPkgLock(prKey.PKey())
	pkgMutex.Lock()
	defer func() {
		pkgMutex.Unlock()
		deletePkgLock(prKey.PKey())
	}()

	freshPR, err := pkgRevReadFromDB(ctx, prKey, true)
	if err != nil {
		if err == sql.ErrNoRows {
			klog.Infof("PushDraftPackageRevision: repo %+v: PR %+v no longer exists in the database, skipping push", repoKey, prKey)
			return
		}
		klog.Warningf("PushDraftPackageRevision: repo %+v: failed to re-read PR %+v from database: %v", repoKey, prKey, err)
		return
	}
	pr = freshPR

	if pr.lifecycle != porchapi.PackageRevisionLifecycleDraft && pr.lifecycle != porchapi.PackageRevisionLifecycleProposed {
		klog.Infof("PushDraftPackageRevision: repo %+v: PR %+v is no longer Draft/Proposed (lifecycle=%s), skipping (publish handles its own push)", repoKey, prKey, pr.lifecycle)
		return
	}

	if !prNeedsPushToGit(pr) {
		klog.Infof("PushDraftPackageRevision: repo %+v: PR %+v already up to date in git, skipping", repoKey, prKey)
		return
	}

	resources := pr.resources
	if len(resources) == 0 {
		prResources, err := pr.GetResources(ctx)
		if err != nil {
			klog.Warningf("PushDraftPackageRevision: repo %+v: failed to load resources for %+v: %v", repoKey, prKey, err)
			return
		}
		if prResources != nil {
			resources = prResources.Spec.Resources
		}
	}

	updatedBeforePush := pr.updated

	gitPRDraft, existingGitPR, err := GetOrCreateGitDraft(ctx, pr.repo.externalRepo, pr)
	if err != nil {
		klog.Warningf("PushDraftPackageRevision: repo %+v: GetOrCreateGitDraft failed for %+v: %v", repoKey, prKey, err)
		return
	}

	if err = gitPRDraft.UpdateResources(ctx, &porchapi.PackageRevisionResources{
		Spec: porchapi.PackageRevisionResourcesSpec{
			Resources: resources,
		},
	}, commitTaskForPush(pr, existingGitPR != nil)); err != nil {
		klog.Warningf("PushDraftPackageRevision: repo %+v: UpdateResources failed for %+v: %v", repoKey, prKey, err)
		return
	}

	if err = gitPRDraft.UpdateLifecycle(ctx, pr.lifecycle); err != nil {
		klog.Warningf("PushDraftPackageRevision: repo %+v: UpdateLifecycle failed for %+v: %v", repoKey, prKey, err)
		return
	}

	pushedGitPR, err := pr.repo.externalRepo.ClosePackageRevisionDraft(ctx, gitPRDraft, prKey.Revision)
	if err != nil {
		klog.Warningf("PushDraftPackageRevision: repo %+v: ClosePackageRevisionDraft failed for %+v: %v", repoKey, prKey, err)
		return
	}

	_, pushedLock, err := pushedGitPR.GetLock(ctx)
	if err != nil {
		klog.Warningf("PushDraftPackageRevision: repo %+v: GetLock failed for %+v: %v", repoKey, prKey, err)
		return
	}

	recorded, err := pkgRevSetLastPushedInDB(ctx, prKey, pushedLock, updatedBeforePush)
	if err != nil {
		klog.Warningf("PushDraftPackageRevision: repo %+v: failed to record push markers for %+v: %v", repoKey, prKey, err)
		return
	}

	if !recorded {
		klog.Warningf("PushDraftPackageRevision: repo %+v: PR %+v was modified or published during push (updated changed from %v), not recording push markers — next sync will retry with fresh data",
			repoKey, prKey, updatedBeforePush)
		return
	}

	commit := extPRCommitFromLocator(pushedLock)
	klog.Infof("PushDraftPackageRevision: repo %+v: successfully pushed %+v to git at commit %q", repoKey, prKey, commit)
}

func GetOrCreateGitDraft(ctx context.Context, repo repository.Repository, pr repository.PackageRevision) (draft repository.PackageRevisionDraft, updatedGitPR repository.PackageRevision, err error) {
	prName := repository.ComposePkgRevObjName(pr.Key())
	klog.InfoS("[DBCache] Getting or creating Git draft for PackageRevision",
		pctx.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
	defer func() {
		klog.V(3).InfoS("[DBCache] Get or create Git draft completed for PackageRevision",
			pctx.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
	}()

	existingPRs, err := repo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
		Key: repository.PackageRevisionKey{
			PkgKey:        pr.Key().PkgKey,
			WorkspaceName: pr.Key().WorkspaceName,
		},
	})

	if err == nil && len(existingPRs) > 0 {
		gitDraft, err := repo.UpdatePackageRevision(ctx, existingPRs[0])
		if err != nil {
			return nil, nil, pkgerrors.Wrapf(err, "failed to update existing git branch for %+v", pr.Key())
		}
		return gitDraft, existingPRs[0], nil
	}

	apiPr, err := pr.GetPackageRevision(ctx)
	if err != nil {
		return nil, nil, pkgerrors.Wrapf(err, "failed to get API representation for %+v", pr.Key())
	}

	gitDraft, err := repo.CreatePackageRevisionDraft(ctx, apiPr)
	if err != nil {
		return nil, nil, pkgerrors.Wrapf(err, "failed to create git draft for %+v", pr.Key())
	}

	return gitDraft, nil, nil
}
