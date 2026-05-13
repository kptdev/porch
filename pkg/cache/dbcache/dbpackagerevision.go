// Copyright 2024-2025 The kpt Authors
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
	"maps"
	"strings"
	"time"

	kptfile "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/kptfile/kptfileutil"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/engine"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	context1 "github.com/kptdev/porch/pkg/util/context"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/klog/v2"
)

var (
	_ repository.PackageRevision      = &dbPackageRevision{}
	_ repository.PackageRevisionDraft = &dbPackageRevision{}
)

type kptfileStatus struct {
	Conditions   []porchapi.Condition `json:"conditions,omitempty"`
	UpstreamLock *kptfile.Locator     `json:"upstreamLock,omitempty"`
}

func extractKptfileStatus(resources map[string]string) kptfileStatus {
	s, _, _ := extractFromKptfile(resources)
	return s
}

func extractFromKptfile(resources map[string]string) (kptfileStatus, []porchapi.ReadinessGate, *porchapi.PackageMetadata) {
	kfString, ok := resources[kptfile.KptFileName]
	if !ok {
		return kptfileStatus{}, nil, nil
	}
	kf, err := kptfileutil.DecodeKptfile(strings.NewReader(kfString))
	if err != nil {
		klog.Warningf("extractFromKptfile: failed to decode Kptfile: %v", err)
		return kptfileStatus{}, nil, nil
	}
	var s kptfileStatus
	if kf.Status != nil {
		s.Conditions = repository.ToAPIConditions(*kf)
	}
	s.UpstreamLock = kf.UpstreamLock
	return s, repository.ToAPIReadinessGates(*kf), &porchapi.PackageMetadata{
		Labels:      kf.Labels,
		Annotations: kf.Annotations,
	}
}

type dbPackageRevision struct {
	repo           *dbRepository
	pkgRevKey      repository.PackageRevisionKey
	meta           metav1.ObjectMeta
	spec           *porchapi.PackageRevisionSpec
	updated        time.Time
	updatedBy      string
	lifecycle      porchapi.PackageRevisionLifecycle
	extPRID        kptfile.Locator
	latest         bool
	deployment     bool
	tasks          []porchapi.Task
	resources      map[string]string
	resourcesDirty bool
	kptfileStatus  kptfileStatus

	// gitDraftPR maintains the draft in the external git repository during editing (when pushDraftsToGit is true)
	gitPRDraft repository.PackageRevisionDraft

	// gitPR is the closed package revision in git (when pushDraftsToGit is true)
	gitPR repository.PackageRevision
}

func (pr *dbPackageRevision) specReadinessGates() []porchapi.ReadinessGate {
	if pr.spec == nil {
		return nil
	}
	return pr.spec.ReadinessGates
}

func (pr *dbPackageRevision) specPackageMetadata() *porchapi.PackageMetadata {
	if pr.spec == nil {
		return nil
	}
	return pr.spec.PackageMetadata
}

func (pr *dbPackageRevision) KubeObjectName() string {
	return repository.ComposePkgRevObjName(pr.Key())
}

func (pr *dbPackageRevision) KubeObjectNamespace() string {
	return pr.Key().RKey().Namespace
}

func (pr *dbPackageRevision) UID() types.UID {
	return util.GenerateUid("packagerevision:", pr.KubeObjectNamespace(), pr.KubeObjectName())
}

func (pr *dbPackageRevision) Key() repository.PackageRevisionKey {
	return pr.pkgRevKey
}

func (pr *dbPackageRevision) savePackageRevision(ctx context.Context, saveResources bool) (*dbPackageRevision, error) {
	_, span := tracer.Start(ctx, "dbPackageRevision::savePackageRevision", trace.WithAttributes())
	defer span.End()

	if saveResources && pr.deployment {
		pr.updated = time.Now()
		pr.updatedBy = getCurrentUser()
	}

	_, err := pkgRevReadFromDB(ctx, pr.Key(), false)
	if err == nil {
		updErr := pkgRevUpdateDB(ctx, pr, saveResources)
		if updErr == nil && saveResources {
			sent := pr.repo.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Modified, pr)
			klog.Infof("DB cache %+v: sent %d notifications for updated package revision %+v", pr.repo.Key(), sent, pr.Key())
		}
		return pr, updErr
	} else if err != sql.ErrNoRows {
		return pr, err
	}

	writeErr := pkgRevWriteToDB(ctx, pr)
	if writeErr == nil {
		sent := pr.repo.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Added, pr)
		klog.Infof("DB cache %+v: sent %d notifications for added package revision %+v", pr.repo.Key(), sent, pr.Key())
	}

	return pr, writeErr
}

func (pr *dbPackageRevision) UpdatePackageRevision(ctx context.Context) error {
	_, span := tracer.Start(ctx, "dbPackageRevision::UpdatePackageRevision", trace.WithAttributes())
	defer span.End()

	if readPr, err := pkgRevReadFromDB(ctx, pr.Key(), true); err == nil {
		pr.copyToThis(readPr)
		return nil
	} else {
		return err
	}
}

func (pr *dbPackageRevision) Lifecycle(ctx context.Context) porchapi.PackageRevisionLifecycle {
	_, span := tracer.Start(ctx, "dbPackageRevision::Lifecycle", trace.WithAttributes())
	defer span.End()

	return pr.lifecycle
}

func (pr *dbPackageRevision) UpdateLifecycle(ctx context.Context, newLifecycle porchapi.PackageRevisionLifecycle) error {
	_, span := tracer.Start(ctx, "dbPackageRevision::UpdateLifecycle", trace.WithAttributes())
	defer span.End()

	if pr.repo == nil {
		return fmt.Errorf("cannot update lifecycle for package revision %s: no associated repository", pr.KubeObjectName())
	}

	// Only Approve (Proposed → Published) pushes to external repo
	if pr.lifecycle == porchapi.PackageRevisionLifecycleProposed && newLifecycle == porchapi.PackageRevisionLifecyclePublished {
		klog.InfoS("[DB Cache] Updating lifecycle in database and pushing to external repo for PackageRevision",
			context1.LogMetadataFrom(ctx)...)
		defer func() {
			klog.V(3).InfoS("[DB Cache] Lifecycle updated in database and pushed to external repo for PackageRevision",
				context1.LogMetadataFrom(ctx)...)
		}()
	} else if pr.repo.pushDraftsToGit && pr.gitPRDraft != nil {
		klog.InfoS("[DB Cache] Updating lifecycle in database and in Git draft for PackageRevision", context1.LogMetadataFrom(ctx)...)
		defer func() {
			klog.V(3).InfoS("[DB Cache] Lifecycle updated in database and in Git draft for PackageRevision", context1.LogMetadataFrom(ctx)...)
		}()
	} else {
		klog.InfoS("[DB Cache] Updating lifecycle in database for PackageRevision", context1.LogMetadataFrom(ctx)...)
		defer func() {
			klog.V(3).InfoS("[DB Cache] Lifecycle updated in database for PackageRevision", context1.LogMetadataFrom(ctx)...)
		}()
	}

	if pr.lifecycle == porchapi.PackageRevisionLifecycleProposed && newLifecycle == porchapi.PackageRevisionLifecyclePublished {
		if err := pr.publishPR(ctx, newLifecycle); err != nil {
			pr.pkgRevKey.Revision = 0
			return pkgerrors.Wrapf(err, "dbPackageRevision:UpdateLifecycle: could not publish package revision %+v", pr.Key())
		}
	} else if porchapi.LifecycleIsPublished(pr.lifecycle) {
		return pr.updateLifecycleOnPublishedPR(ctx, newLifecycle)
	}

	pr.lifecycle = newLifecycle

	if pr.repo.pushDraftsToGit && pr.gitPRDraft != nil {
		if err := pr.gitPRDraft.UpdateLifecycle(ctx, newLifecycle); err != nil {
			klog.Warningf("failed to update git draft lifecycle for %+v: %v", pr.Key(), err)
		}
	}

	return nil
}

func (pr *dbPackageRevision) GetPackageRevision(ctx context.Context) (*porchapi.PackageRevision, error) {
	_, span := tracer.Start(ctx, "dbPackageRevision::GetPackageRevision", trace.WithAttributes())
	defer span.End()

	if pr == nil {
		return nil, fmt.Errorf("invalid package revision: nil object")
	}

	_, upstreamLock, _ := pr.GetUpstreamLock(ctx)
	_, selfLock, _ := pr.GetLock(ctx)

	status := porchapi.PackageRevisionStatus{
		UpstreamLock: repository.KptUpstreamLock2APIUpstreamLock(upstreamLock),
		SelfLock:     repository.KptUpstreamLock2APIUpstreamLock(selfLock),
		Deployment:   pr.deployment,
		Conditions:   pr.kptfileStatus.Conditions,
	}

	if porchapi.LifecycleIsPublished(pr.Lifecycle(ctx)) {
		if !pr.updated.IsZero() {
			status.PublishedAt = metav1.Time{Time: pr.updated}
		}
		if pr.updatedBy != "" {
			status.PublishedBy = pr.updatedBy
		}
	}

	// Set the "latest" label
	labels := pr.GetMeta().Labels

	if pr.latest {
		// copy the labels in case the cached object is being read by another go routine
		newLabels := make(map[string]string, len(labels))
		maps.Copy(newLabels, labels)
		newLabels[porchapi.LatestPackageRevisionKey] = porchapi.LatestPackageRevisionValue
		labels = newLabels
	}

	return &porchapi.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              pr.KubeObjectName(),
			Namespace:         pr.Key().RKey().Namespace,
			UID:               pr.UID(),
			ResourceVersion:   pr.ResourceVersion(),
			CreationTimestamp: pr.GetMeta().CreationTimestamp,
			DeletionTimestamp: pr.GetMeta().DeletionTimestamp,
			Labels:            labels,
			OwnerReferences:   pr.GetMeta().OwnerReferences,
			Annotations:       pr.GetMeta().Annotations,
			Finalizers:        pr.GetMeta().Finalizers,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:     pr.Key().PKey().ToPkgPathname(),
			RepositoryName:  pr.Key().RKey().Name,
			Lifecycle:       pr.Lifecycle(ctx),
			Tasks:           pr.tasks,
			ReadinessGates:  pr.specReadinessGates(),
			WorkspaceName:   pr.Key().WorkspaceName,
			Revision:        pr.Key().Revision,
			PackageMetadata: pr.specPackageMetadata(),
		},
		Status: status,
	}, nil
}

func (pr *dbPackageRevision) GetResources(ctx context.Context) (*porchapi.PackageRevisionResources, error) {
	_, span := tracer.Start(ctx, "dbPackageRevision::GetResources", trace.WithAttributes())
	defer span.End()

	resources, err := pkgRevResourcesReadFromDB(ctx, pr.pkgRevKey)
	if err != nil {
		klog.V(5).Infof("pkgRevScanRowsFromDB: reading package revision %+v resources returned err: %q", pr.Key(), err)
		return nil, err
	}

	klog.V(5).Infof("pkgRevScanRowsFromDB: reading package revision resources succeeded %+v", pr.Key())

	return &porchapi.PackageRevisionResources{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevisionResources",
			APIVersion: porchapi.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            pr.KubeObjectName(),
			Namespace:       pr.Key().RKey().Namespace,
			UID:             pr.UID(),
			ResourceVersion: pr.ResourceVersion(),
			CreationTimestamp: metav1.Time{
				Time: pr.updated,
			},
		},
		Spec: porchapi.PackageRevisionResourcesSpec{
			PackageName:    pr.Key().PKey().Package,
			WorkspaceName:  pr.Key().WorkspaceName,
			Revision:       pr.Key().Revision,
			RepositoryName: pr.Key().RKey().Name,
			Resources:      resources,
		},
	}, nil
}

func (pr *dbPackageRevision) GetUpstreamLock(ctx context.Context) (kptfile.Upstream, kptfile.Locator, error) {
	if pr.kptfileStatus.UpstreamLock == nil || pr.kptfileStatus.UpstreamLock.Git == nil {
		return kptfile.Upstream{}, kptfile.Locator{}, nil
	}
	return repository.KptUpstreamLock2KptUpstream(*pr.kptfileStatus.UpstreamLock), *pr.kptfileStatus.UpstreamLock, nil
}

func (pr *dbPackageRevision) ToMainPackageRevision(ctx context.Context) repository.PackageRevision {
	_, span := tracer.Start(ctx, "dbPackageRevision::ToMainPackageRevision", trace.WithAttributes())
	defer span.End()

	mainPR := &dbPackageRevision{
		repo: pr.repo,
		pkgRevKey: repository.PackageRevisionKey{
			PkgKey:        pr.Key().PKey(),
			Revision:      -1,
			WorkspaceName: pr.Key().RKey().PlaceholderWSname,
		},
		meta:          metav1.ObjectMeta{},
		spec:          &porchapi.PackageRevisionSpec{},
		updated:       time.Now(),
		updatedBy:     getCurrentUser(),
		lifecycle:     pr.lifecycle,
		extPRID:       pr.extPRID,
		latest:        false,
		deployment:    pr.deployment,
		tasks:         pr.tasks,
		resources:     pr.resources,
		kptfileStatus: pr.kptfileStatus,
	}

	mainPR.meta.CreationTimestamp = metav1.Time{Time: time.Now()}

	if mainPR.pkgRevKey.WorkspaceName == "" {
		mainPR.pkgRevKey.WorkspaceName = "main"
	}

	klog.V(5).Infof("ToMainPackageRevision: %+v, main package revision generated", mainPR.Key())

	return mainPR
}

func (pr *dbPackageRevision) GetMeta() metav1.ObjectMeta {
	return pr.meta
}

func (pr *dbPackageRevision) SetMeta(ctx context.Context, meta metav1.ObjectMeta) error {
	_, span := tracer.Start(ctx, "dbPackageRevision::SetMeta", trace.WithAttributes())
	defer span.End()

	pr.meta = meta
	return pkgRevUpdateDB(ctx, pr, false)
}

func (pr *dbPackageRevision) IsLatestRevision() bool {
	return pr.latest
}

func (pr *dbPackageRevision) GetCommitInfo() (time.Time, string) {
	return pr.updated, pr.updatedBy
}

func (pr *dbPackageRevision) GetKptfile(ctx context.Context) (kptfile.KptFile, error) {
	_, span := tracer.Start(ctx, "dbPackageRevision::GetKptfile", trace.WithAttributes())
	defer span.End()

	_, kfString, err := pkgRevResourceReadFromDB(ctx, pr.Key(), kptfile.KptFileName)
	if err != nil {
		return kptfile.KptFile{}, fmt.Errorf("no Kptfile for packagerevision %+v found in DB: %q", pr.Key(), err)
	}

	kf, err := kptfileutil.DecodeKptfile(strings.NewReader(kfString))
	if err != nil {
		return kptfile.KptFile{}, fmt.Errorf("error decoding Kptfile: %w", err)
	}
	return *kf, nil
}

func (pr *dbPackageRevision) GetLock(ctx context.Context) (kptfile.Upstream, kptfile.Locator, error) {
	_, span := tracer.Start(ctx, "dbPackageRevision::GetLock", trace.WithAttributes())
	defer span.End()
	return repository.KptUpstreamLock2KptUpstream(pr.extPRID), pr.extPRID, nil
}

func (pr *dbPackageRevision) ResourceVersion() string {
	return fmt.Sprintf("%s.%d", pr.KubeObjectName(), pr.updated.UnixMicro())
}

func (pr *dbPackageRevision) Delete(ctx context.Context, deleteExternal bool) error {
	_, span := tracer.Start(ctx, "dbPackageRevision::Delete", trace.WithAttributes())
	defer span.End()

	if deleteExternal && porchapi.LifecycleIsPublished(pr.lifecycle) {
		if err := pr.repo.externalRepo.DeletePackageRevision(ctx, pr); err != nil {
			// Check if the error indicates the package doesn't exist in external repo
			if repository.IsNotFoundError(err) {
				klog.Infof("dbPackageRevision:Delete: package revision %+v not found in external repository, continuing with cache deletion", pr.Key())
			} else {
				klog.Warningf("dbPackageRevision:Delete: deletion of %+v failed on external repository %q", pr.Key(), err)
				return err
			}
		}
	}

	err := pkgRevDeleteFromDB(ctx, pr.Key())
	if err != nil && err != sql.ErrNoRows {
		klog.Warningf("dbPackage:DeletePackageRevision: deletion of %+v failed on database %q", pr.Key(), err)
	}

	sent := pr.repo.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Deleted, pr)
	klog.Infof("DB cache %+v: sent %d notifications for deleted package revision %+v", pr.repo.Key(), sent, pr.Key())

	return err
}

func (pr *dbPackageRevision) copyToThis(otherPr *dbPackageRevision) {
	otherPr.pkgRevKey.DeepCopy(&pr.pkgRevKey)

	pr.updated = otherPr.updated
	pr.updatedBy = otherPr.updatedBy
	pr.lifecycle = otherPr.lifecycle
	pr.tasks = otherPr.tasks
	pr.resources = otherPr.resources
}

func (pr *dbPackageRevision) UpdateResources(ctx context.Context, new *porchapi.PackageRevisionResources, change *porchapi.Task) error {
	_, span := tracer.Start(ctx, "dbPackageRevision::UpdateResources", trace.WithAttributes())
	defer span.End()

	if pr.repo == nil {
		return fmt.Errorf("cannot update resources for package revision %s: no associated repository", pr.KubeObjectName())
	}

	if pr.repo.pushDraftsToGit && pr.gitPRDraft != nil {
		klog.InfoS("[DB Cache] Updating resources in memory and in Git draft for PackageRevision", context1.LogMetadataFrom(ctx)...)
		defer func() {
			klog.V(3).InfoS("[DB Cache] Resources updated in memory and in Git draft for PackageRevision", context1.LogMetadataFrom(ctx)...)
		}()
	} else {
		klog.InfoS("[DB Cache] Updating resources in memory for PackageRevision", context1.LogMetadataFrom(ctx)...)
		defer func() {
			klog.V(3).InfoS("[DB Cache] Resources updated in memory for PackageRevision", context1.LogMetadataFrom(ctx)...)
		}()
	}

	pr.resources = new.Spec.Resources
	pr.resourcesDirty = true
	status, gates, pkgMeta := extractFromKptfile(pr.resources)
	pr.kptfileStatus = status
	if gates != nil || pkgMeta != nil {
		if pr.spec == nil {
			pr.spec = &porchapi.PackageRevisionSpec{}
		}
		pr.spec.ReadinessGates = gates
		pr.spec.PackageMetadata = pkgMeta
	}

	if change != nil && porchapi.IsValidFirstTaskType(change.Type) {
		if len(pr.tasks) > 0 {
			klog.Warningf("Replacing first task of %q", pr.Key())
		}
		pr.tasks = []porchapi.Task{*change}
	}

	if pr.repo.pushDraftsToGit && pr.gitPRDraft != nil {
		if err := pr.gitPRDraft.UpdateResources(ctx, new, change); err != nil {
			klog.Warningf("failed to update git draft resources for %+v: %v", pr.Key(), err)
		}
	}

	return nil
}

func (pr *dbPackageRevision) publishPR(ctx context.Context, newLifecycle porchapi.PackageRevisionLifecycle) error {
	_, span := tracer.Start(ctx, "dbPackageRevision::publishPR", trace.WithAttributes())
	defer span.End()

	latestRev, err := pkgRevGetlatestRevFromDB(ctx, pr.Key().PkgKey)
	if err != nil {
		return pkgerrors.Wrapf(err, "dbPackageRevision:publishPR: could not get latest package revision for package revision %+v from DB", pr.Key())
	}

	pr.pkgRevKey.Revision = latestRev + 1
	pr.lifecycle = newLifecycle

	var gitPR repository.PackageRevision
	if pr.repo.pushDraftsToGit {
		if pr.gitPR != nil {
			gitPR = pr.gitPR
		} else {
			gitPR = pr.repo.getCachedGitPR(pr.Key().PkgKey, pr.Key().WorkspaceName)
		}
	}

	pushedPRExtID, err := engine.PushPackageRevision(ctx, pr.repo.externalRepo, pr, pr.repo.pushDraftsToGit, gitPR)
	if err != nil {
		klog.Warningf("push of package revision %+v to external repo failed, %q", pr.Key(), err)
		pr.pkgRevKey.Revision = 0
		pr.lifecycle = porchapi.PackageRevisionLifecycleProposed
		return pkgerrors.Wrapf(err, "dbPackageRevision:publishPR: push of package revision %+v to external repo failed", pr.Key())
	}

	pr.extPRID = pushedPRExtID

	if err = pkgRevUpdateDB(ctx, pr, false); err != nil {
		return pkgerrors.Wrapf(err, "dbPackageRevision:publishPR: failed to save package revision %+v to database after push to external repo", pr.Key())
	}

	return pr.publishPlaceholderPRForPR(ctx)
}

func (pr *dbPackageRevision) publishPlaceholderPRForPR(ctx context.Context) error {
	_, span := tracer.Start(ctx, "dbPackageRevision::publishPlaceholderPRForPR", trace.WithAttributes())
	defer span.End()

	prWithResources := pr
	if len(prWithResources.resources) == 0 {
		if readPR, err := pkgRevReadFromDB(ctx, pr.Key(), true); err == nil {
			prWithResources = readPR
		} else {
			return pkgerrors.Wrapf(err, "dbPackageRevision:publishPlaceholderPRForPR: could read resources for package revision %+v to DB", pr.Key())
		}
	}

	placeholderPR := prWithResources.ToMainPackageRevision(ctx).(*dbPackageRevision)

	if prWithResources.pkgRevKey.Revision == 1 {
		if err := pkgRevUpdateDB(ctx, placeholderPR, true); err != nil {
			return pkgerrors.Wrapf(err, "dbPackageRevision:publishPlaceholderPRForPR: could not write placeholder package revision for package revision %+v to DB", placeholderPR.Key())
		}
		sent := placeholderPR.repo.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Added, placeholderPR)
		klog.Infof("DB cache %+v: sent %d notifications for added package revision %+v", placeholderPR.repo.Key(), sent, placeholderPR.Key())
	} else if prWithResources.pkgRevKey.Revision > 1 {
		if err := pkgRevUpdateDB(ctx, placeholderPR, true); err != nil {
			return pkgerrors.Wrapf(err, "dbPackageRevision:publishPlaceholderPRForPR: could not update placeholder package revision for package revision %+v to DB", placeholderPR.Key())
		}
		sent := placeholderPR.repo.repoPRChangeNotifier.NotifyPackageRevisionChange(watch.Modified, placeholderPR)
		klog.Infof("DB cache %+v: sent %d notifications for updated package revision %+v", placeholderPR.repo.Key(), sent, placeholderPR.Key())
	}

	return nil
}

func (pr *dbPackageRevision) updateLifecycleOnPublishedPR(ctx context.Context, newLifecycle porchapi.PackageRevisionLifecycle) error {
	ctx, span := tracer.Start(ctx, "dbPackageRevision::updateLifecycleOnPublishedPR", trace.WithAttributes())
	defer span.End()

	pr.lifecycle = newLifecycle

	_, err := pr.savePackageRevision(ctx, false)
	return err
}
