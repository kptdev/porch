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

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type fieldIndex struct {
	field   porchv1alpha2.PkgRevFieldSelector
	extract func(*porchv1alpha2.PackageRevision) string
}

var fieldIndexes = []fieldIndex{
	{porchv1alpha2.PkgRevSelectorLifecycle, func(pr *porchv1alpha2.PackageRevision) string {
		return string(pr.Spec.Lifecycle)
	}},
	{porchv1alpha2.PkgRevSelectorRepository, func(pr *porchv1alpha2.PackageRevision) string {
		return pr.Spec.RepositoryName
	}},
	{porchv1alpha2.PkgRevSelectorPackageName, func(pr *porchv1alpha2.PackageRevision) string {
		return pr.Spec.PackageName
	}},
	{porchv1alpha2.PkgRevSelectorWorkspaceName, func(pr *porchv1alpha2.PackageRevision) string {
		return pr.Spec.WorkspaceName
	}},
	{porchv1alpha2.PkgRevSelectorRevision, func(pr *porchv1alpha2.PackageRevision) string {
		return fmt.Sprint(pr.Status.Revision)
	}},
}

func setupFieldIndexes(mgr ctrl.Manager) error {
	ctx := context.Background()
	indexer := mgr.GetFieldIndexer()

	for _, idx := range fieldIndexes {
		extract := idx.extract
		if err := indexer.IndexField(ctx, &porchv1alpha2.PackageRevision{}, string(idx.field),
			func(obj client.Object) []string {
				return []string{extract(obj.(*porchv1alpha2.PackageRevision))}
			}); err != nil {
			return fmt.Errorf("failed to index field %s: %w", idx.field, err)
		}
	}

	return nil
}
