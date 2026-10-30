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

package task

import (
	"context"
	"fmt"
	"path"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/lib/kptops"
	"github.com/kptdev/krm-functions-sdk/go/fn/kptfileko"
	"github.com/kptdev/porch/api/porch/key"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	porchapiv1alpha1 "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
)

var _ mutation = &upgradePackageMutation{}

type upgradePackageMutation struct {
	upgradeTask       *porchapi.Task
	repoOpener        repository.RepositoryOpener
	referenceResolver repository.ReferenceResolver
	namespace         string
	pkgName           string
}

func (m *upgradePackageMutation) apply(ctx context.Context, _ repository.PackageResources) (repository.PackageResources, *porchapi.TaskResult, error) {
	ctx, span := tracer.Start(ctx, "upgradePackageMutation::apply", trace.WithAttributes())
	defer span.End()

	currUpstreamRef := m.upgradeTask.Upgrade.OldUpstream
	targetUpstreamRef := m.upgradeTask.Upgrade.NewUpstream
	localRef := m.upgradeTask.Upgrade.LocalPackageRevisionRef

	packageFetcher := &repository.PackageFetcher{
		RepoOpener:        m.repoOpener,
		ReferenceResolver: m.referenceResolver,
	}

	currUpstreamResources, err := packageFetcher.FetchResources(ctx, &currUpstreamRef, m.namespace)
	if err != nil {
		return repository.PackageResources{}, nil, pkgerrors.Wrapf(err, "error fetching the resources for package %q with ref %+v",
			m.pkgName, currUpstreamRef)
	}

	targetUpstreamRevision, err := packageFetcher.FetchRevision(ctx, &targetUpstreamRef, m.namespace)
	if err != nil {
		return repository.PackageResources{}, nil, pkgerrors.Wrapf(err, "error fetching revision for target upstream %q", targetUpstreamRef.Name)
	}

	targetUpstreamIsPlaceholder, err := repository.PackageRevisionIsPlaceholder(ctx, m.namespace, m.referenceResolver, targetUpstreamRevision)
	if err != nil {
		return repository.PackageResources{}, nil, pkgerrors.Wrap(err, "error checking for placeholder package revision")
	}
	if targetUpstreamIsPlaceholder {
		// We only allow upgrade to create new revisions with non-placeholder package revisions as target upstream
		return repository.PackageResources{}, nil, fmt.Errorf("target upstream revision may not be the placeholder package revision %s/%s", targetUpstreamRevision.Key().RKey().Name, targetUpstreamRevision.KubeObjectName())
	}

	targetUpstreamResources, err := targetUpstreamRevision.GetResources(ctx)
	if err != nil {
		return repository.PackageResources{}, nil, pkgerrors.Wrapf(err, "error fetching resources for target upstream %q", targetUpstreamRef.Name)
	}

	localRevision, err := packageFetcher.FetchRevision(ctx, &localRef, m.namespace)
	if err != nil {
		return repository.PackageResources{}, nil, pkgerrors.Wrapf(err, "error fetching revision %q to be upgraded", localRef.Name)
	}

	localIsPlaceholder, err := repository.PackageRevisionIsPlaceholder(ctx, m.namespace, m.referenceResolver, localRevision)
	if err != nil {
		return repository.PackageResources{}, nil, pkgerrors.Wrap(err, "error checking for placeholder package revision")
	}
	if localIsPlaceholder {
		// We only allow upgrade to upgrade non-placeholder package revisions
		return repository.PackageResources{}, nil, fmt.Errorf("source revision may not be the placeholder package revision %s/%s", localRevision.Key().RKey().Name, localRevision.KubeObjectName())
	}

	localResources, err := localRevision.GetResources(ctx)
	if err != nil {
		return repository.PackageResources{}, nil, pkgerrors.Wrapf(err, "error fetching resources for local revision %q", localRef.Name)
	}

	if m.upgradeTask.Upgrade.SubpackageDir == "" {
		if err := m.validateUpstreamPackages(ctx, localRevision); err != nil {
			return repository.PackageResources{}, nil, pkgerrors.Wrapf(err, "invalid upstream package reference")
		}
	} else {
		subpackageLocalResources := make(map[string]string)

		for localResourceKey, localResourceValue := range localResources.Spec.Resources {
			if trimmed, found := strings.CutPrefix(localResourceKey, m.upgradeTask.Upgrade.SubpackageDir+"/"); found {
				subpackageLocalResources[trimmed] = localResourceValue
			}
		}

		if len(subpackageLocalResources) == 0 {
			return repository.PackageResources{}, nil, fmt.Errorf("subpackage %q not found in package %q", m.upgradeTask.Upgrade.SubpackageDir, localRef.Name)
		}

		if err := m.validateSubpackageUpstreamPackages(ctx, subpackageLocalResources); err != nil {
			return repository.PackageResources{}, nil, pkgerrors.Wrapf(err, "invalid upstream package reference")
		}

		localResources.Spec.Resources = subpackageLocalResources
	}

	klog.Infof("performing pkg upgrade operation for pkg %s resource counts: local[%d]; original upstream[%d]; target upstream[%d]",
		m.pkgName, len(localResources.Spec.Resources), len(currUpstreamResources.Spec.Resources), len(targetUpstreamResources.Spec.Resources))

	//TODO: May be have packageUpdater part of the Porch core to make it easy for testing ?
	updatedResources, err := (&repository.DefaultPackageUpdater{}).Update(ctx,
		repository.PackageResources{
			Contents: localResources.Spec.Resources,
		},
		repository.PackageResources{
			Contents: currUpstreamResources.Spec.Resources,
		},
		repository.PackageResources{
			Contents: targetUpstreamResources.Spec.Resources,
		},
		string(m.upgradeTask.Upgrade.Strategy))
	if err != nil {
		return repository.PackageResources{}, nil, pkgerrors.Wrapf(err, "error updating the package %q to revision %q", m.pkgName, targetUpstreamRef.Name)
	}

	newUpstream, newUpstreamLock, err := targetUpstreamRevision.GetLock(ctx)
	if err != nil {
		return repository.PackageResources{}, nil, pkgerrors.Wrapf(err, "error fetching the resources for package revision %q", targetUpstreamRef.Name)
	}
	if err := kptops.UpdateKptfileUpstream("", updatedResources.Contents, newUpstream, newUpstreamLock); err != nil {
		return repository.PackageResources{}, nil, pkgerrors.Wrapf(err, "failed to apply upstream lock to package %q", m.pkgName)
	}

	// ensure merge-key comment is added to newly added resources.
	result, err := ensureMergeKey(ctx, updatedResources)
	if err != nil {
		klog.Infof("failed to add merge key comments: %v", err)
	}
	return result, &porchapi.TaskResult{Task: m.upgradeTask}, nil
}

func (m *upgradePackageMutation) validateUpstreamPackages(ctx context.Context, localRevision repository.PackageRevision) error {
	localUpstreamKey, currUpstreamKey, targetUpstreamKey, err :=
		m.resolvePackageRevisionKeys(ctx, localRevision)
	if err != nil {
		return pkgerrors.Wrapf(err, "error resolving package revision keys to validate upgrade request")
	}

	if !localUpstreamKey.Matches(currUpstreamKey) {
		return pkgerrors.Errorf("current upstream package revision %q, specified in upgrade request, does not match the actual (local) upstream package revision %q ", currUpstreamKey, localUpstreamKey)
	}

	localUpstreamPackage := localUpstreamKey.PkgKey
	targetUpstreamPackage := targetUpstreamKey.PkgKey
	if !localUpstreamPackage.Matches(targetUpstreamPackage) {
		return pkgerrors.Errorf("target upstream package revision, specified in upgrade request, is not part of the same package as the actual (local) upstream package revision (expected package %q; got %q)", localUpstreamPackage, targetUpstreamPackage)
	}

	return nil
}

func (m *upgradePackageMutation) validateSubpackageUpstreamPackages(ctx context.Context, subpackageLocalResources map[string]string) error {

	localRef := m.upgradeTask.Upgrade.LocalPackageRevisionRef

	kptfileString, ok := subpackageLocalResources["Kptfile"]
	if !ok {
		return fmt.Errorf("subpackage %q in package %q is missing Kptfile", m.upgradeTask.Upgrade.SubpackageDir, localRef.Name)
	}
	kptfile, err := kptfileko.DecodeKptfile(kptfileString)
	if err != nil {
		return pkgerrors.Wrapf(err, "could not unmarshal Kptfile for subpackage %q in local package %q", path.Join(m.upgradeTask.Upgrade.SubpackageDir, kptfilev1.KptFileName), localRef.Name)
	}
	localUpstreamPackage, err := m.findUpstreamPackageNameFromKptfile(kptfile)
	if err != nil {
		return pkgerrors.Wrapf(err, "error finding upstream for subpackage %q in local package %q", path.Join(m.upgradeTask.Upgrade.SubpackageDir, kptfilev1.KptFileName), localRef.Name)
	}

	_, currUpstreamKey, targetUpstreamKey, err := m.resolvePackageRevisionKeys(ctx, nil)
	if err != nil {
		return pkgerrors.Wrapf(err, "error resolving package revision keys to validate subpackage upgrade request")
	}

	currUpstreamPackage := func() string {
		if currUpstreamKey.PkgKey.Path == "" {
			return ""
		}
		return currUpstreamKey.PkgKey.Path + "/"
	}() + currUpstreamKey.PkgKey.Package
	if currUpstreamPackage != localUpstreamPackage {
		return pkgerrors.Errorf("subpackage's current upstream package %q, specified in upgrade request, does not match the actual (local) subpackage's upstream package %q ", currUpstreamPackage, localUpstreamPackage)
	}

	targetUpstreamPackage := func() string {
		if targetUpstreamKey.PkgKey.Path == "" {
			return ""
		}
		return targetUpstreamKey.PkgKey.Path + "/"
	}() + targetUpstreamKey.PkgKey.Package
	if localUpstreamPackage != targetUpstreamPackage {
		return pkgerrors.Errorf("target upstream package revision, specified in upgrade request, is not part of the same package as the actual (local) upstream package revision (expected package %q; got %q)", localUpstreamPackage, targetUpstreamPackage)
	}

	return nil
}

func (m *upgradePackageMutation) resolvePackageRevisionKeys(ctx context.Context, localRevision repository.PackageRevision) (
	localUpstreamKey, currUpstreamKey, targetUpstreamKey key.PackageRevisionKey,
	err error) {

	if localRevision != nil {
		var localApiRevision *porchapiv1alpha1.PackageRevision
		localApiRevision, err = localRevision.GetPackageRevision(ctx)
		if localApiRevision == nil {
			if err == nil {
				err = pkgerrors.Errorf("error getting metadata for revision %q to be upgraded", localRevision.KubeObjectName())
			}
			err = pkgerrors.Wrapf(err, "error getting metadata for revision %q to be upgraded", localRevision.KubeObjectName())
			return
		}

		var localUpstreamName string
		localUpstreamName, err = m.findUpstreamName(ctx, localApiRevision)
		if err != nil {
			err = pkgerrors.Wrapf(err, "error finding upstream for local package revision %q", localRevision.KubeObjectName())
			return
		}
		if localUpstreamName == "" {
			err = pkgerrors.Errorf("upstream source not found for local package revision %q:"+
				" no clone or upgrade type package revision was found in the history of the package", localRevision.KubeObjectName())
			return
		}

		localUpstreamKey, err = key.PkgRevK8sName2Key(m.namespace, localUpstreamName)
		if err != nil {
			err = pkgerrors.Wrapf(err, "error resolving local package revision's upstream name %q to package revision key", localUpstreamName)
			return
		}
	}

	currUpstreamRef := m.upgradeTask.Upgrade.OldUpstream
	targetUpstreamRef := m.upgradeTask.Upgrade.NewUpstream

	currUpstreamKey, err = key.PkgRevK8sName2Key(m.namespace, currUpstreamRef.Name)
	if err != nil {
		err = pkgerrors.Wrapf(err, "error resolving current upstream package revision name %q to package revision key", currUpstreamRef.Name)
		return
	}
	targetUpstreamKey, err = key.PkgRevK8sName2Key(m.namespace, targetUpstreamRef.Name)
	if err != nil {
		err = pkgerrors.Wrapf(err, "error resolving target upstream package revision name %q to package revision key", targetUpstreamRef.Name)
		return
	}
	return
}

func (m *upgradePackageMutation) findUpstreamName(ctx context.Context, pr *porchapiv1alpha1.PackageRevision) (string, error) {
	if len(pr.Spec.Tasks) == 0 {
		return "", pkgerrors.Errorf("package revision %q has no tasks", pr.Name)
	}

	task := &pr.Spec.Tasks[0]
	switch task.Type {
	case porchapiv1alpha1.TaskTypeClone:
		if task.Clone == nil || task.Clone.Upstream.UpstreamRef == nil {
			return "", pkgerrors.Errorf("the clone task of package revision %q does not name an upstream package revision", pr.Name)
		}
		return task.Clone.Upstream.UpstreamRef.Name, nil
	case porchapiv1alpha1.TaskTypeEdit:
		n, err := m.findEditOrigin(ctx, pr)
		if err != nil {
			return "", err
		}
		if n != "" {
			return n, nil
		}
		return "", nil
	case porchapiv1alpha1.TaskTypeUpgrade:
		if task.Upgrade == nil {
			return "", pkgerrors.Errorf("the upgrade task of package revision %q does not name an upstream package revision", pr.Name)
		}
		return task.Upgrade.NewUpstream.Name, nil
	default:
		return "", nil
	}
}

func (m *upgradePackageMutation) findEditOrigin(ctx context.Context, currentPr *porchapiv1alpha1.PackageRevision) (string, error) {
	pr := currentPr
	for pr != nil && len(pr.Spec.Tasks) > 0 && pr.Spec.Tasks[0].Type == porchapiv1alpha1.TaskTypeEdit {
		edit := pr.Spec.Tasks[0].Edit
		if edit == nil || edit.Source == nil {
			return "", pkgerrors.Errorf("the edit task of package revision %q does not name a source package revision", pr.Name)
		}

		packageFetcher := &repository.PackageFetcher{
			RepoOpener:        m.repoOpener,
			ReferenceResolver: m.referenceResolver,
		}
		found, err := m.findPackageRevision(ctx, edit.Source.Name, packageFetcher)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return "", nil
			}
			return "", err
		}
		pr = found
	}
	if pr != nil {
		return m.findUpstreamName(ctx, pr)
	}
	return "", nil
}

func (m *upgradePackageMutation) findPackageRevision(ctx context.Context, prName string, fetcher *repository.PackageFetcher) (*porchapiv1alpha1.PackageRevision, error) {
	// Use fetcher.FetchRevision instead of searching through cached list
	ns := ""
	if m.namespace != "" {
		ns = m.namespace
	}
	pr, err := fetcher.FetchRevision(ctx, &porchapiv1alpha1.PackageRevisionRef{Name: prName}, ns)
	if err != nil {
		return nil, err
	}
	apiPr, err := pr.GetPackageRevision(ctx)
	if err != nil {
		return nil, err
	}
	return apiPr, nil
}

func (m *upgradePackageMutation) findUpstreamPackageNameFromKptfile(kptfile *kptfilev1.KptFile) (string, error) {
	upstream := kptfile.Upstream
	if upstream == nil || upstream.Git == nil {
		return "", fmt.Errorf("subpackage %q has no upstream source", m.upgradeTask.Upgrade.SubpackageDir)
	}

	_, upstreamPkg, _, isManaged, err := util.GetRepoPackageRefFromUpstream(upstream)
	if err != nil {
		return "", pkgerrors.Wrapf(err, "could not find upstream references in upstream read from subpackage kptfile")
	}
	if !isManaged {
		return "", pkgerrors.Errorf("subpackage %q is not managed by kpt and cannot be upgraded", upstreamPkg)
	}
	upstreamPkg = strings.ReplaceAll(upstreamPkg, ".", "/")
	return upstreamPkg, err
}
