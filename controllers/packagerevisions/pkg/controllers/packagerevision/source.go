// Copyright 2026 The kpt and Nephio Authors
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

package packagerevision

import (
	"context"
	"fmt"
	iofs "io/fs"
	"maps"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/pkg/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/kptfile/kptfileutil"
	"github.com/kptdev/kpt/pkg/kptpkg"
	"github.com/kptdev/kpt/pkg/lib/kptops"
	"github.com/kptdev/kpt/pkg/printer"
	"github.com/kptdev/kpt/pkg/printer/fake"
	porchv1alpha2 "github.com/nephio-project/porch/api/porch/v1alpha2"
	"github.com/nephio-project/porch/pkg/repository"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

// applySource executes the package creation source and returns the resulting resources.
// Returns nil, nil if no source needs to be applied (package already created).
func (r *PackageRevisionReconciler) applySource(ctx context.Context, pr *porchv1alpha2.PackageRevision) (map[string]string, string, error) {
	if pr.Status.CreationSource != "" {
		return nil, "", nil
	}
	if pr.Spec.Source == nil {
		return nil, "", nil
	}

	switch {
	case pr.Spec.Source.Init != nil:
		resources, err := initPackage(ctx, pr.Spec.PackageName, pr.Spec.Source.Init)
		return resources, "init", err
	case pr.Spec.Source.CloneFrom != nil:
		resources, err := r.clonePackage(ctx, pr)
		return resources, "clone", err
	case pr.Spec.Source.CopyFrom != nil:
		resources, err := r.copyPackage(ctx, pr)
		return resources, "copy", err
	case pr.Spec.Source.Upgrade != nil:
		resources, err := r.upgradePackage(ctx, pr)
		return resources, "upgrade", err
	default:
		return nil, "", fmt.Errorf("source has no fields set")
	}
}

func initPackage(ctx context.Context, pkgName string, spec *porchv1alpha2.PackageInitSpec) (map[string]string, error) {
	fs := filesys.MakeFsInMemory()
	pkgPath := "/"

	if err := fs.Mkdir(pkgPath); err != nil {
		return nil, err
	}

	init := kptpkg.DefaultInitializer{}
	if err := init.Initialize(printer.WithContext(ctx, &fake.Printer{}), fs, kptpkg.InitOptions{
		PkgPath:  pkgPath,
		PkgName:  pkgName,
		Desc:     spec.Description,
		Keywords: spec.Keywords,
		Site:     spec.Site,
	}); err != nil {
		return nil, fmt.Errorf("failed to initialize pkg %q: %w", pkgName, err)
	}

	return readFsToMap(fs)
}

// copyPackage reads the source package referenced by CopyFrom and returns its resources.
// Validates the source is from the same repository and is published.
func (r *PackageRevisionReconciler) copyPackage(ctx context.Context, pr *porchv1alpha2.PackageRevision) (map[string]string, error) {
	log := log.FromContext(ctx)
	sourceRef := pr.Spec.Source.CopyFrom

	var sourcePR porchv1alpha2.PackageRevision
	if err := r.Get(ctx, client.ObjectKey{Namespace: pr.Namespace, Name: sourceRef.Name}, &sourcePR); err != nil {
		return nil, fmt.Errorf("failed to get source package %q: %w", sourceRef.Name, err)
	}

	if sourcePR.Spec.RepositoryName != pr.Spec.RepositoryName {
		return nil, fmt.Errorf("source package must be from same repository %q, got %q", pr.Spec.RepositoryName, sourcePR.Spec.RepositoryName)
	}
	if sourcePR.Spec.PackageName != pr.Spec.PackageName {
		return nil, fmt.Errorf("source package must be same package %q, got %q", pr.Spec.PackageName, sourcePR.Spec.PackageName)
	}
	if !porchv1alpha2.LifecycleIsPublished(sourcePR.Spec.Lifecycle) {
		return nil, fmt.Errorf("source package %q must be published", sourceRef.Name)
	}

	log.V(1).Info("copying from source", "source", sourceRef.Name)
	repoKey := repository.RepositoryKey{Namespace: pr.Namespace, Name: pr.Spec.RepositoryName}
	content, err := r.ContentCache.GetPackageContent(ctx, repoKey, sourcePR.Spec.PackageName, sourcePR.Spec.WorkspaceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get source package content: %w", err)
	}

	return content.GetResourceContents(ctx)
}

// clonePackage reads the source package referenced by CloneFrom and returns its resources
// with Kptfile upstream/upstreamLock updated.
// Currently only supports upstreamRef (registered repo). Raw git URL is not yet implemented.
func (r *PackageRevisionReconciler) clonePackage(ctx context.Context, pr *porchv1alpha2.PackageRevision) (map[string]string, error) {
	cloneFrom := pr.Spec.Source.CloneFrom

	if cloneFrom.UpstreamRef != nil {
		return r.cloneFromUpstreamRef(ctx, pr, cloneFrom.UpstreamRef)
	}
	if cloneFrom.Git != nil {
		return r.cloneFromGit(ctx, pr, cloneFrom.Git)
	}
	return nil, fmt.Errorf("clone source must specify either upstreamRef or git")
}

func (r *PackageRevisionReconciler) cloneFromUpstreamRef(ctx context.Context, pr *porchv1alpha2.PackageRevision, ref *porchv1alpha2.PackageRevisionRef) (map[string]string, error) {
	log := log.FromContext(ctx)
	var sourcePR porchv1alpha2.PackageRevision
	if err := r.Get(ctx, client.ObjectKey{Namespace: pr.Namespace, Name: ref.Name}, &sourcePR); err != nil {
		return nil, fmt.Errorf("failed to get upstream package %q: %w", ref.Name, err)
	}

	if !porchv1alpha2.LifecycleIsPublished(sourcePR.Spec.Lifecycle) {
		return nil, fmt.Errorf("upstream package %q must be published", ref.Name)
	}

	log.V(1).Info("cloning from upstream ref", "upstream", ref.Name)

	repoKey := repository.RepositoryKey{Namespace: pr.Namespace, Name: sourcePR.Spec.RepositoryName}
	content, err := r.ContentCache.GetPackageContent(ctx, repoKey, sourcePR.Spec.PackageName, sourcePR.Spec.WorkspaceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get upstream package content: %w", err)
	}

	resources, err := content.GetResourceContents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read upstream resources: %w", err)
	}

	upstream, lock, err := content.GetLock(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get upstream lock for %q: %w", ref.Name, err)
	}

	if err := kptops.UpdateKptfileUpstream(pr.Spec.PackageName, resources, upstream, lock); err != nil {
		return nil, fmt.Errorf("failed to update Kptfile upstream: %w", err)
	}

	return resources, nil
}

func (r *PackageRevisionReconciler) cloneFromGit(ctx context.Context, pr *porchv1alpha2.PackageRevision, gitSpec *porchv1alpha2.GitPackage) (map[string]string, error) {
	log.FromContext(ctx).V(1).Info("cloning from git", "repo", gitSpec.Repo, "ref", gitSpec.Ref, "directory", gitSpec.Directory)
	resources, lock, err := r.ExternalPackageFetcher.FetchExternalGitPackage(ctx, gitSpec, pr.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from git: %w", err)
	}

	if err := kptops.UpdateKptfileUpstream(pr.Spec.PackageName, resources, kptfilev1.Upstream{
		Type: kptfilev1.GitOrigin,
		Git: &kptfilev1.Git{
			Repo:      lock.Repo,
			Directory: lock.Directory,
			Ref:       lock.Ref,
		},
	}, kptfilev1.Locator{
		Type: kptfilev1.GitOrigin,
		Git:  &lock,
	}); err != nil {
		return nil, fmt.Errorf("failed to update Kptfile upstream: %w", err)
	}

	return resources, nil
}

// upgradePackage performs a 3-way merge between the old upstream, new upstream,
// and current local package, then updates the Kptfile upstream/upstreamLock to
// point at the new upstream.
func (r *PackageRevisionReconciler) upgradePackage(ctx context.Context, pr *porchv1alpha2.PackageRevision) (map[string]string, error) {
	log := log.FromContext(ctx)
	upgrade := pr.Spec.Source.Upgrade
	log.V(1).Info("upgrading package", "oldUpstream", upgrade.OldUpstream.Name,
		"newUpstream", upgrade.NewUpstream.Name, "current", upgrade.CurrentPackage.Name)

	strategy := string(upgrade.Strategy)
	if strategy == "" {
		strategy = string(porchv1alpha2.ResourceMerge)
	}

	// Look up all three package revisions.
	oldUpstreamPR, err := r.getPublishedPackageRevision(ctx, pr.Namespace, upgrade.OldUpstream.Name)
	if err != nil {
		return nil, fmt.Errorf("old upstream: %w", err)
	}
	newUpstreamPR, err := r.getPublishedPackageRevision(ctx, pr.Namespace, upgrade.NewUpstream.Name)
	if err != nil {
		return nil, fmt.Errorf("new upstream: %w", err)
	}
	currentPR, err := r.getPublishedPackageRevision(ctx, pr.Namespace, upgrade.CurrentPackage.Name)
	if err != nil {
		return nil, fmt.Errorf("current package: %w", err)
	}

	// Read content and resources. Retain new upstream content for lock extraction.
	oldUpstreamResources, err := r.getPackageResources(ctx, oldUpstreamPR)
	if err != nil {
		return nil, fmt.Errorf("failed to read old upstream resources: %w", err)
	}
	newUpstreamContent, newUpstreamResources, err := r.getPackageContentAndResources(ctx, newUpstreamPR)
	if err != nil {
		return nil, fmt.Errorf("failed to read new upstream resources: %w", err)
	}
	currentResources, err := r.getPackageResources(ctx, currentPR)
	if err != nil {
		return nil, fmt.Errorf("failed to read current package resources: %w", err)
	}

	// Workaround for kpt bug: fast-forward's hasKfDiff strips Upstream and
	// UpstreamLock but not Status, so the Rendered condition written by kpt
	// render is treated as a local modification. Only strip for fast-forward
	// since other strategies need status for the 3-way merge.
	if strategy == string(porchv1alpha2.FastForward) {
		currentResources = copyResources(currentResources)
		stripKptfileStatus(currentResources)
	}

	// 3-way merge.
	updated, err := (&repository.DefaultPackageUpdater{}).Update(ctx,
		repository.PackageResources{Contents: currentResources},
		repository.PackageResources{Contents: oldUpstreamResources},
		repository.PackageResources{Contents: newUpstreamResources},
		strategy,
	)
	if err != nil {
		return nil, fmt.Errorf("3-way merge failed: %w", err)
	}

	// Update Kptfile upstream/upstreamLock to point at new upstream.
	newUpstream, newUpstreamLock, err := newUpstreamContent.GetLock(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get new upstream lock: %w", err)
	}
	if err := kptops.UpdateKptfileUpstream(pr.Spec.PackageName, updated.Contents, newUpstream, newUpstreamLock); err != nil {
		return nil, fmt.Errorf("failed to update Kptfile upstream: %w", err)
	}

	// Add merge-key comments to newly added resources.
	result, err := ensureMergeKey(updated.Contents)
	if err != nil {
		// Non-fatal — log and return unmodified resources.
		log.V(1).Info("merge-key annotation failed, using unmodified resources")
		result = updated.Contents
	}

	return result, nil
}

// getPublishedPackageRevision looks up a PackageRevision CRD and validates it is published.
func (r *PackageRevisionReconciler) getPublishedPackageRevision(ctx context.Context, namespace, name string) (*porchv1alpha2.PackageRevision, error) {
	var pr porchv1alpha2.PackageRevision
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &pr); err != nil {
		return nil, fmt.Errorf("failed to get package %q: %w", name, err)
	}
	if !porchv1alpha2.LifecycleIsPublished(pr.Spec.Lifecycle) {
		return nil, fmt.Errorf("package %q must be published", name)
	}
	return &pr, nil
}

// getPackageResources reads the resource contents for a package revision via the cache.
func (r *PackageRevisionReconciler) getPackageResources(ctx context.Context, pr *porchv1alpha2.PackageRevision) (map[string]string, error) {
	_, resources, err := r.getPackageContentAndResources(ctx, pr)
	return resources, err
}

// getPackageContentAndResources reads both the content handle and resource map
// for a package revision. Use this when you need the content for more than just
// resources (e.g. to call GetLock).
func (r *PackageRevisionReconciler) getPackageContentAndResources(ctx context.Context, pr *porchv1alpha2.PackageRevision) (repository.PackageContent, map[string]string, error) {
	repoKey := repository.RepositoryKey{Namespace: pr.Namespace, Name: pr.Spec.RepositoryName}
	content, err := r.ContentCache.GetPackageContent(ctx, repoKey, pr.Spec.PackageName, pr.Spec.WorkspaceName)
	if err != nil {
		return nil, nil, err
	}
	resources, err := content.GetResourceContents(ctx)
	if err != nil {
		return nil, nil, err
	}
	return content, resources, nil
}

// stripKptfileStatus removes the status section from the Kptfile in a resource map.
// Workaround for kpt bug: hasKfDiff in fastforward.go strips Upstream and
// UpstreamLock but not Status, so the Rendered condition written by kpt render
// is treated as a local modification and fast-forward rejects the upgrade.
func stripKptfileStatus(resources map[string]string) {
	kfStr, ok := resources[kptfilev1.KptFileName]
	if !ok {
		return
	}
	kf, err := kptfileutil.DecodeKptfile(strings.NewReader(kfStr))
	if err != nil || kf.Status == nil {
		return
	}
	kf.Status = nil
	out, err := yaml.Marshal(kf)
	if err != nil {
		return
	}
	resources[kptfilev1.KptFileName] = string(out)
}

func copyResources(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}

func readFsToMap(fs filesys.FileSystem) (map[string]string, error) {
	contents := map[string]string{}
	if err := fs.Walk("/", func(path string, info iofs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			data, err := fs.ReadFile(path)
			if err != nil {
				return err
			}
			contents[strings.TrimPrefix(path, "/")] = string(data)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return contents, nil
}
