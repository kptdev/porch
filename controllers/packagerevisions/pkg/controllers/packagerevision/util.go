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
	iofs "io/fs"
	"maps"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	"github.com/kptdev/kpt/pkg/kptfile/kptfileutil"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

// getDraftPackageRevision looks up a PackageRevision CRD and validates it is a draft.
func (r *PackageRevisionReconciler) getDraftPackageRevision(ctx context.Context, namespace, name string) (*porchv1alpha2.PackageRevision, error) {
	var pr porchv1alpha2.PackageRevision
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &pr); err != nil {
		return nil, fmt.Errorf("failed to get package %q: %w", name, err)
	}
	if pr.Spec.Lifecycle != porchv1alpha2.PackageRevisionLifecycleDraft {
		return nil, fmt.Errorf("package %q must be a draft", name)
	}
	return &pr, nil
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
