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

package engine

import (
	"context"
	"fmt"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	context1 "github.com/kptdev/porch/pkg/util/context"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/klog/v2"
)

func PushPackageRevision(ctx context.Context, repo repository.Repository, pr repository.PackageRevision, pushDraftsToGit bool, gitPR repository.PackageRevision) (kptfilev1.Locator, error) {
	ctx, span := tracer.Start(ctx, "PushPackageRevision", trace.WithAttributes())
	defer span.End()

	prName := repository.ComposePkgRevObjName(pr.Key())
	if pushDraftsToGit {
		klog.InfoS("[Engine] Pushing PackageRevision to repository and to Git for PackageRevision",
			context1.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
	} else {
		klog.InfoS("[Engine] Pushing PackageRevision to repository for PackageRevision",
			context1.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
	}
	defer func() {
		klog.V(3).InfoS("[Engine] Push PackageRevision to repository completed for PackageRevision",
			context1.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
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

	if pushDraftsToGit {
		if gitPR != nil {
			klog.V(3).InfoS("[Engine] Updating existing Git draft for PackageRevision",
				context1.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
			draft, err = repo.UpdatePackageRevision(ctx, gitPR)
			if err != nil {
				return kptfilev1.Locator{}, pkgerrors.Wrapf(err, "push of package revision %+v to repository %+v failed, could not update git PR:", pr.Key(), repo.Key())
			}
			foundExisting = true
		} else {
			klog.V(3).InfoS("[Engine] Listing existing Git revisions for PackageRevision",
				context1.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
			existingPRs, err := repo.ListPackageRevisions(ctx, repository.ListPackageRevisionFilter{
				Key: repository.PackageRevisionKey{
					PkgKey:        pr.Key().PkgKey,
					WorkspaceName: pr.Key().WorkspaceName,
				},
			})

			if err == nil && len(existingPRs) > 0 {
				klog.V(3).InfoS("[Engine] Updating existing Git package revision for PackageRevision",
					context1.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
				draft, err = repo.UpdatePackageRevision(ctx, existingPRs[0])
				if err != nil {
					return kptfilev1.Locator{}, pkgerrors.Wrapf(err, "push of package revision %+v to repository %+v failed, could not update existing package revision:", pr.Key(), repo.Key())
				}
				foundExisting = true
			}
		}
	}

	if !foundExisting {
		draft, err = repo.CreatePackageRevisionDraft(ctx, apiPr)
		if err != nil {
			return kptfilev1.Locator{}, pkgerrors.Wrapf(err, "push of package revision %+v to repository %+v failed, could not create package revision draft:", pr.Key(), repo.Key())
		}
	}

	if !foundExisting {
		commitTask := &porchapi.Task{Type: porchapi.TaskTypePush}
		if len(apiPr.Spec.Tasks) > 0 {
			commitTask = &apiPr.Spec.Tasks[0]
		}

		if err = draft.UpdateResources(ctx, resources, commitTask); err != nil {
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

func GetOrCreateGitDraft(ctx context.Context, repo repository.Repository, pr repository.PackageRevision, gitPR repository.PackageRevision) (draft repository.PackageRevisionDraft, updatedGitPR repository.PackageRevision, err error) {
	prName := repository.ComposePkgRevObjName(pr.Key())
	klog.InfoS("[Engine] Getting or creating Git draft for PackageRevision",
		context1.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
	defer func() {
		klog.V(3).InfoS("[Engine] Get or create Git draft completed for PackageRevision",
			context1.LogMetadataFromWithExtras(ctx, "packageRevision", prName)...)
	}()

	if gitPR != nil {
		gitDraft, err := repo.UpdatePackageRevision(ctx, gitPR)
		if err != nil {
			return nil, nil, pkgerrors.Wrapf(err, "failed to update git PR for %+v", pr.Key())
		}
		return gitDraft, gitPR, nil
	}

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
