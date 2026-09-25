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
func (r *PackageRevisionReconciler) applySource(ctx context.Context, pr *porchv1alpha2.PackageRevision) (map[string]string, error) {
	_, action := r.selectPackageSourceAction(pr)
	if action == nil {
		return nil, fmt.Errorf("source has no fields set")
	}

	resources, err := action(ctx, pr)
	return resources, err
}

type prCreationOperation func(context.Context, *porchv1alpha2.PackageRevision) (map[string]string, error)

var noOp = func(context.Context, *porchv1alpha2.PackageRevision) (map[string]string, error) { return nil, nil }

func (r *PackageRevisionReconciler) selectPackageSourceAction(pr *porchv1alpha2.PackageRevision) (string, prCreationOperation) {
	if pr.Status.CreationSource != "" {
		return "", noOp
	}
	if pr.Spec.Source == nil {
		return "", noOp
	}

	switch {
	case pr.Spec.Source.Init != nil:
		return "init", initPackage
	case pr.Spec.Source.CloneFrom != nil:
		return "clone", r.clonePackage
	case pr.Spec.Source.CopyFrom != nil:
		return "copy", r.copyPackage
	case pr.Spec.Source.Upgrade != nil:
		return "upgrade", r.upgradePackage
	default:
		return "", nil
	}
}

func initPackage(ctx context.Context, pr *porchv1alpha2.PackageRevision) (map[string]string, error) {
	fs := filesys.MakeFsInMemory()
	pkgPath := "/"
	pkgName := pr.Spec.PackageName
	spec := pr.Spec.Source.Init

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
