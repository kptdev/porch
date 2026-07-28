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

package packagerevision

import (
	"context"
	"fmt"
	"path"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/lib/kptops"
	"github.com/kptdev/porch/api/porch/v1alpha2"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// clonePackage reads the source package referenced by CloneFrom and returns its resources
// with Kptfile upstream/upstreamLock updated.
// Currently only supports upstreamRef (registered repo). Raw git URL is not yet implemented.
func (r *PackageRevisionReconciler) clonePackage(ctx context.Context, pr *porchv1alpha2.PackageRevision) (map[string]string, error) {
	cloneFrom := r.getCloneFrom(pr)

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

	if err := kptops.UpdateKptfileUpstream(r.getClonePackagename(pr), resources, upstream, lock); err != nil {
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

	if err := kptops.UpdateKptfileUpstream(r.getClonePackagename(pr), resources, kptfilev1.Upstream{
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

// getCloneFrom returns the upstream package for a clone in the case of a source clone or a subpackage
// operation clone
func (r *PackageRevisionReconciler) getCloneFrom(pr *porchv1alpha2.PackageRevision) *v1alpha2.UpstreamPackage {
	if pr.Spec.SubpackageOperation != nil && pr.Spec.SubpackageOperation.CloneFrom != nil {
		return pr.Spec.SubpackageOperation.CloneFrom
	}
	return pr.Spec.Source.CloneFrom
}

// getClonePackagename returns the package name of a clone in the case of a source clone or a subpackage
// operation clone
func (r *PackageRevisionReconciler) getClonePackagename(pr *porchv1alpha2.PackageRevision) string {
	if pr.Spec.SubpackageOperation != nil && pr.Spec.SubpackageOperation.CloneFrom != nil {
		return path.Base(pr.Spec.SubpackageOperation.SubpackageDir)
	}
	return pr.Spec.PackageName
}
