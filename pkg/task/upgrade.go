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
	configapi "github.com/kptdev/porch/api/porchconfig/v1alpha1"
	apiutil "github.com/kptdev/porch/api/util"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/kptdev/porch/pkg/util"
	pkgerrors "github.com/pkg/errors"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
)

var _ mutation = &upgradePackageMutation{}
var blankKey = key.PackageRevisionKey{}

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

// packageGitOrigin is the fully-resolved upstream origin of a subpackage, read
// from its Kptfile's managed upstream: the repository (URL + repo-level
// directory), the package pathname within that repo, and the revision.
type packageGitOrigin struct {
	repoURL  string
	pkgPath  string
	revision int
}

func (m *upgradePackageMutation) validateUpstreamPackages(ctx context.Context, localRevision repository.PackageRevision) error {
	if m.upgradeTask.Upgrade.ForceUpgrade {
		return nil
	}

	localUpstreamKey, currUpstreamKey, targetUpstreamKey, err :=
		m.resolvePackageRevisionKeys(ctx, localRevision)
	if err != nil {
		return pkgerrors.Wrapf(err, "error resolving package revision keys to validate upgrade request")
	}
	localUpstreamPackage := localUpstreamKey.PkgKey
	currUpstreamPackage := currUpstreamKey.PkgKey
	targetUpstreamPackage := targetUpstreamKey.PkgKey

	if localUpstreamPackage != currUpstreamPackage || localUpstreamKey.WorkspaceName != currUpstreamKey.WorkspaceName {
		return pkgerrors.Errorf("current upstream package revision %q, specified in upgrade request, does not match the actual (local) upstream package revision %q ", currUpstreamKey, localUpstreamKey)
	}

	if localUpstreamPackage != targetUpstreamPackage {
		return pkgerrors.Errorf("target upstream package revision, specified in upgrade request, is not part of the same package as the actual (local) upstream package revision (expected package %q; got %q)", localUpstreamPackage, targetUpstreamPackage)
	}

	return nil
}

func (m *upgradePackageMutation) validateSubpackageUpstreamPackages(ctx context.Context, subpackageLocalResources map[string]string) error {
	if m.upgradeTask.Upgrade.ForceUpgrade {
		return nil
	}

	localRef := m.upgradeTask.Upgrade.LocalPackageRevisionRef

	kptfileString, ok := subpackageLocalResources["Kptfile"]
	if !ok {
		return fmt.Errorf("subpackage %q in package %q is missing Kptfile", m.upgradeTask.Upgrade.SubpackageDir, localRef.Name)
	}
	kptfile, err := kptfileko.DecodeKptfile(kptfileString)
	if err != nil {
		return pkgerrors.Wrapf(err, "could not unmarshal Kptfile for subpackage %q in local package %q",
			path.Join(m.upgradeTask.Upgrade.SubpackageDir, kptfilev1.KptFileName), localRef.Name)
	}

	localUpstreamOrigin, err := m.resolvePackageOriginFromKptfile(kptfile)
	if err != nil {
		return pkgerrors.Wrapf(err, "error finding upstream for subpackage %q in local package %q",
			path.Join(m.upgradeTask.Upgrade.SubpackageDir, kptfilev1.KptFileName), localRef.Name)
	}

	// OldUpstream must be the same repository and package
	currentUpstreamOrigin, err := m.resolvePackageOriginFromRef(ctx, m.upgradeTask.Upgrade.OldUpstream)
	if err != nil {
		return err
	}
	if currentUpstreamOrigin.repoURL != localUpstreamOrigin.repoURL {
		return pkgerrors.Errorf("subpackage's current upstream repository %q, specified in upgrade request, does not match the actual (local) subpackage's upstream repository %q",
			currentUpstreamOrigin.repoURL, localUpstreamOrigin.repoURL)
	}
	if currentUpstreamOrigin.pkgPath != localUpstreamOrigin.pkgPath {
		return pkgerrors.Errorf("subpackage's current upstream package %q, specified in upgrade request, does not match the actual (local) subpackage's upstream package %q",
			currentUpstreamOrigin.pkgPath, localUpstreamOrigin.pkgPath)
	}
	if currentUpstreamOrigin.revision != localUpstreamOrigin.revision {
		return pkgerrors.Errorf(
			"subpackage's current upstream revision (v%d), specified in upgrade request, does not match the actual (local) subpackage's upstream revision (v%d)",
			currentUpstreamOrigin.revision, localUpstreamOrigin.revision)
	}

	// NewUpstream (the upgrade target) must be the same repository and package
	targetUpstreamOrigin, err := m.resolvePackageOriginFromRef(ctx, m.upgradeTask.Upgrade.NewUpstream)
	if err != nil {
		return err
	}
	if targetUpstreamOrigin.repoURL != localUpstreamOrigin.repoURL || targetUpstreamOrigin.pkgPath != localUpstreamOrigin.pkgPath {
		return pkgerrors.Errorf("target upstream package (%s, package %q), specified in upgrade request, is not part of the same package as the actual (local) subpackage's upstream (%s, package %q)",
			targetUpstreamOrigin.repoURL, targetUpstreamOrigin.pkgPath, localUpstreamOrigin.repoURL, localUpstreamOrigin.pkgPath)
	}

	return nil
}

func (m *upgradePackageMutation) resolvePackageRevisionKeys(ctx context.Context, localRevision repository.PackageRevision) (
	localUpstreamKey, currUpstreamKey, targetUpstreamKey key.PackageRevisionKey,
	err error) {

	currUpstreamRef := m.upgradeTask.Upgrade.OldUpstream
	targetUpstreamRef := m.upgradeTask.Upgrade.NewUpstream

	currUpstreamKey, err = key.PkgRevK8sName2Key(m.namespace, currUpstreamRef.Name)
	if err != nil {
		return blankKey, blankKey, blankKey,
			pkgerrors.Wrapf(err, "error resolving current upstream package revision name %q to package revision key", currUpstreamRef.Name)
	}

	targetUpstreamKey, err = key.PkgRevK8sName2Key(m.namespace, targetUpstreamRef.Name)
	if err != nil {
		return blankKey, blankKey, blankKey,
			pkgerrors.Wrapf(err, "error resolving target upstream package revision name %q to package revision key", targetUpstreamRef.Name)
	}

	localUpstreamKey, localUpstreamOrigin, err := m.resolveLocalUpstreamKey(ctx, localRevision)
	if err != nil {
		return blankKey, blankKey, blankKey, err
	}
	if localUpstreamOrigin != nil {
		// unable to resolve workspace name and Porch-repo details in this fallback
		// align other keys to what we can match, and blank out now-unmatchable
		// details in the other keys

		err := m.alignKeysToOriginBasedMatching(ctx, &currUpstreamKey, &targetUpstreamKey)
		if err != nil {
			return blankKey, blankKey, blankKey, err
		}
	}

	return
}

func (m *upgradePackageMutation) resolveLocalUpstreamKey(ctx context.Context, localRevision repository.PackageRevision) (localUpstreamKey key.PackageRevisionKey, localUpstreamLocatorOrigin *packageGitOrigin, err error) {
	var localApiRevision *porchapi.PackageRevision
	localApiRevision, err = localRevision.GetPackageRevision(ctx)
	if localApiRevision == nil {
		if err == nil {
			err = pkgerrors.Errorf("error getting metadata for revision %q to be upgraded", localRevision.KubeObjectName())
		} else {
			err = pkgerrors.Wrapf(err, "error getting metadata for revision %q to be upgraded", localRevision.KubeObjectName())
		}
		return blankKey, nil, err
	}

	var (
		localUpstreamName string
	)
	localUpstreamName, localUpstreamLocatorOrigin, err = m.findPackageUpstream(ctx, localApiRevision)
	if err != nil {
		return blankKey, nil,
			pkgerrors.Wrapf(err, "error finding upstream for local package revision %q", localRevision.KubeObjectName())
	}

	if localUpstreamName != "" {
		localUpstreamKey, err = key.PkgRevK8sName2Key(m.namespace, localUpstreamName)
		if err != nil {
			return blankKey, nil,
				pkgerrors.Wrapf(err, "error resolving local package revision's upstream name %q to package revision key", localUpstreamName)
		}
	} else if localUpstreamLocatorOrigin != nil {
		pkgPath, pkgName := key.SplitPackagePathName(localUpstreamLocatorOrigin.pkgPath)
		localUpstreamKey = key.PackageRevisionKey{
			PkgKey: key.PackageKey{
				RepoKey: key.RepositoryKey{
					Path: localUpstreamLocatorOrigin.repoURL,
				},
				Path:    pkgPath,
				Package: pkgName,
			},
		}
	} else {
		return blankKey, nil,
			pkgerrors.Errorf("upstream source not found for local package revision %q:"+
				" no clone or upgrade type package revision was found in the history of the package", localRevision.KubeObjectName())
	}
	return
}

func (m *upgradePackageMutation) findPackageUpstream(ctx context.Context, pr *porchapi.PackageRevision) (fullPkgRevRef string, packagePath *packageGitOrigin, err error) {
	if len(pr.Spec.Tasks) == 0 {
		return "", nil, pkgerrors.Errorf("package revision %q has no tasks", pr.Name)
	}

	task := &pr.Spec.Tasks[0]
	switch task.Type {
	case porchapi.TaskTypeClone:
		if task.Clone == nil || task.Clone.Upstream.UpstreamRef == nil {
			return "", nil, pkgerrors.Errorf("the clone task of package revision %q does not name an upstream package revision", pr.Name)
		}
		return task.Clone.Upstream.UpstreamRef.Name, nil, nil
	case porchapi.TaskTypeEdit:
		return m.findEditOrigin(ctx, pr)
	case porchapi.TaskTypeUpgrade:
		if task.Upgrade == nil {
			return "", nil, pkgerrors.Errorf("the upgrade task of package revision %q does not name an upstream package revision", pr.Name)
		}
		return task.Upgrade.NewUpstream.Name, nil, nil
	default:
		return
	}
}

func (m *upgradePackageMutation) findEditOrigin(ctx context.Context, currentPr *porchapi.PackageRevision) (fullPkgRevRef string, packageOrigin *packageGitOrigin, err error) {
	pr := currentPr
	for pr != nil && len(pr.Spec.Tasks) > 0 && pr.Spec.Tasks[0].Type == porchapi.TaskTypeEdit {
		edit := pr.Spec.Tasks[0].Edit
		if edit == nil || edit.Source == nil {
			err = pkgerrors.Errorf("the edit task of package revision %q does not name a source package revision", pr.Name)
			return
		}

		var found *porchapi.PackageRevision
		found, err = m.findPackageRevision(ctx, edit.Source.Name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				if pr.Status.UpstreamLock != nil {
					packageOrigin, err = m.resolvePackageOriginFromLocator(pr.Status.UpstreamLock)
					return
				}

				// edit source not found (possibly deleted) and no UpstreamLock - treat as no-origin
				err = nil
				return
			}
			return
		}
		pr = found
	}
	if pr != nil {
		fullPkgRevRef, _, err = m.findPackageUpstream(ctx, pr)
		return
	}
	return
}

func (m *upgradePackageMutation) findPackageRevision(ctx context.Context, prName string) (*porchapi.PackageRevision, error) {
	packageFetcher := &repository.PackageFetcher{
		RepoOpener:        m.repoOpener,
		ReferenceResolver: m.referenceResolver,
	}

	pr, err := packageFetcher.FetchRevision(ctx, &porchapi.PackageRevisionRef{Name: prName}, m.namespace)
	if err != nil {
		return nil, err
	}
	apiPr, err := pr.GetPackageRevision(ctx)
	if err != nil {
		return nil, err
	}
	if apiPr == nil {
		return nil, pkgerrors.Errorf("no metadata resolved for package revision %q", prName)
	}
	return apiPr, nil
}

func (m *upgradePackageMutation) resolvePackageOriginFromLocator(locator *porchapi.Locator) (*packageGitOrigin, error) {
	if locator == nil || locator.Git == nil {
		return nil, fmt.Errorf("package %q has no upstream source", m.upgradeTask.Upgrade.LocalPackageRevisionRef.Name)
	}

	kptfile := kptfilev1.KptFile{
		Upstream: &kptfilev1.Upstream{
			Git: &kptfilev1.Git{
				Repo:      locator.Git.Repo,
				Directory: locator.Git.Directory,
				Ref:       locator.Git.Ref,
			},
		},
	}
	return m.resolvePackageOriginFromKptfile(&kptfile)
}

// resolvePackageOriginFromKptfile reads the subpackage's managed upstream from its
// Kptfile and returns the full origin (repo URL + repo directory + package path
// + revision).
func (m *upgradePackageMutation) resolvePackageOriginFromKptfile(kptfile *kptfilev1.KptFile) (*packageGitOrigin, error) {
	subpackageDir := m.upgradeTask.Upgrade.SubpackageDir

	upstream := kptfile.Upstream
	if upstream == nil || upstream.Git == nil {
		return nil, fmt.Errorf("subpackage %q has no upstream source", subpackageDir)
	}

	upstreamRepo, upstreamPkg, upstreamRef, isManaged, err :=
		util.GetRepoPackageRefFromUpstream(upstream)
	if err != nil {
		return nil, pkgerrors.Wrapf(err, "could not find upstream references in upstream read from subpackage %q's Kptfile", subpackageDir)
	}
	if !isManaged {
		return nil, pkgerrors.Errorf("subpackage %q is not managed by kpt and cannot be upgraded", subpackageDir)
	}

	rev := apiutil.Revision2Int(upstreamRef)
	if rev < 1 {
		return nil, pkgerrors.Errorf("subpackage %q has invalid git ref %q in upstream (expected format \"vN\")", subpackageDir, upstreamRef)
	}

	pkgDir := strings.Trim(upstreamRepo.Git.Directory, "/")
	pkg := strings.ReplaceAll(upstreamPkg, ".", "/")

	return &packageGitOrigin{
		repoURL:  normalizeGitURL(upstreamRepo.Git.Repo),
		pkgPath:  path.Join(pkgDir, pkg),
		revision: rev,
	}, nil
}

func normalizeGitURL(u string) string {
	if !strings.HasSuffix(u, ".git") {
		u += ".git"
	}
	return u
}

func (m *upgradePackageMutation) alignKeysToOriginBasedMatching(ctx context.Context, currUpstreamKey, targetUpstreamKey *key.PackageRevisionKey) error {
	currentUpstreamOrigin, err := m.resolvePackageOriginFromRef(ctx, m.upgradeTask.Upgrade.OldUpstream)
	if err != nil {
		return err
	}
	targetUpstreamOrigin, err := m.resolvePackageOriginFromRef(ctx, m.upgradeTask.Upgrade.NewUpstream)
	if err != nil {
		return err
	}

	currUpstreamKey.WorkspaceName = ""
	currUpstreamKey.PkgKey.RepoKey = key.RepositoryKey{}
	currUpstreamKey.PkgKey.RepoKey.Path = currentUpstreamOrigin.repoURL

	targetUpstreamKey.WorkspaceName = ""
	targetUpstreamKey.PkgKey.RepoKey = key.RepositoryKey{}
	targetUpstreamKey.PkgKey.RepoKey.Path = targetUpstreamOrigin.repoURL

	return nil
}

// resolvePackageOriginFromRef resolves a request-supplied upstream PR ref (OldUpstream
// or NewUpstream) to its repository (URL + repo directory), package pathname and
// revision, using name-based lookups only.
func (m *upgradePackageMutation) resolvePackageOriginFromRef(ctx context.Context, ref porchapi.PackageRevisionRef) (packageGitOrigin, error) {
	prKey, err := key.PkgRevK8sName2Key(m.namespace, ref.Name)
	if err != nil {
		return packageGitOrigin{}, pkgerrors.Wrapf(err, "error resolving upstream package revision name %q to package revision key",
			ref.Name)
	}

	var upstreamRepo configapi.Repository
	if err := m.referenceResolver.ResolveReference(ctx, m.namespace, prKey.RKey().Name, &upstreamRepo); err != nil {
		return packageGitOrigin{}, pkgerrors.Wrapf(err, "cannot resolve repository %q for upstream ref %q", prKey.RKey().Name, ref.Name)
	}
	if upstreamRepo.Spec.Git == nil {
		return packageGitOrigin{}, pkgerrors.Errorf("repository %q for upstream ref %q is not a git repository", prKey.RKey().Name, ref.Name)
	}

	pr, err := m.findPackageRevision(ctx, ref.Name)
	if err != nil {
		return packageGitOrigin{}, pkgerrors.Wrapf(err, "cannot fetch upstream ref %q", ref.Name)
	}

	return packageGitOrigin{
		repoURL:  normalizeGitURL(upstreamRepo.Spec.Git.Repo),
		pkgPath:  prKey.PkgKey.ToPkgPathname(),
		revision: pr.Spec.Revision,
	}, nil
}
