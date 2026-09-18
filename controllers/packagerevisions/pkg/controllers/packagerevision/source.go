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

	"github.com/kptdev/kpt/pkg/kptpkg"
	"github.com/kptdev/kpt/pkg/printer"
	"github.com/kptdev/kpt/pkg/printer/fake"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/kustomize/kyaml/filesys"
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
