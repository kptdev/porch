// Copyright 2022, 2025 The kpt Authors
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

package porch

import (
	"context"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/registry/rest"
)

type tableConvertor struct {
	resource schema.GroupResource
	// cells creates a single row of cells of the table from a runtime.Object
	cells func(obj runtime.Object) []interface{}
	// columns stores column definitions for the table convertor
	columns []metav1.TableColumnDefinition
}

var _ rest.TableConvertor = tableConvertor{}

// ConvertToTable implements rest.TableConvertor
func (c tableConvertor) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	var table metav1.Table

	fn := func(obj runtime.Object) error {
		cells := c.cells(obj)
		if len(cells) == 0 {
			return newResourceNotAcceptableError(ctx, c.resource)
		}
		table.Rows = append(table.Rows, metav1.TableRow{
			Cells:  cells,
			Object: runtime.RawExtension{Object: obj},
		})
		return nil
	}

	// Create table rows
	switch {
	case meta.IsListType(object):
		if err := meta.EachListItem(object, fn); err != nil {
			return nil, err
		}
	default:
		if err := fn(object); err != nil {
			return nil, err
		}
	}

	// Populate table metadata
	table.APIVersion = metav1.SchemeGroupVersion.Identifier()
	table.Kind = "Table"
	if l, err := meta.ListAccessor(object); err == nil {
		table.ResourceVersion = l.GetResourceVersion()
		table.Continue = l.GetContinue()
		table.RemainingItemCount = l.GetRemainingItemCount()
	} else if c, err := meta.CommonAccessor(object); err == nil {
		table.ResourceVersion = c.GetResourceVersion()
	}
	if opt, ok := tableOptions.(*metav1.TableOptions); !ok || !opt.NoHeaders {
		table.ColumnDefinitions = c.columns
	}

	return &table, nil
}

var (
	packageTableConvertor = tableConvertor{
		resource: porchapi.Resource("packages"),
		cells: func(obj runtime.Object) []interface{} {
			pr, ok := obj.(*porchapi.PorchPackage)
			if !ok {
				return nil
			}
			return []interface{}{
				pr.Name,
				pr.Spec.PackageName,
				pr.Spec.RepositoryName,
				pr.Status.LatestRevision,
			}
		},
		columns: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Package", Type: "string"},
			{Name: "Repository", Type: "string"},
			{Name: "Latest Revision", Type: "int"},
		},
	}

	packageRevisionTableConvertor = tableConvertor{
		resource: porchapi.Resource("packagerevisions"),
		cells: func(obj runtime.Object) []interface{} {
			pr, ok := obj.(*porchapi.PackageRevision)
			if !ok {
				return nil
			}
			return []interface{}{
				pr.Name,
				pr.Spec.PackageName,
				pr.Spec.WorkspaceName,
				pr.Spec.Revision,
				isLatest(pr),
				pr.Spec.Lifecycle,
				pr.Spec.RepositoryName,
			}
		},
		columns: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Package", Type: "string"},
			{Name: "WorkspaceName", Type: "string"},
			{Name: "Revision", Type: "int"},
			{Name: "Latest", Type: "boolean"},
			{Name: "Lifecycle", Type: "string"},
			{Name: "Repository", Type: "string"},
		},
	}

	packageRevisionResourcesTableConvertor = tableConvertor{
		resource: porchapi.Resource("packagerevisionresources"),
		cells: func(obj runtime.Object) []interface{} {
			pr, ok := obj.(*porchapi.PackageRevisionResources)
			if !ok {
				return nil
			}
			return []interface{}{
				pr.Name,
				pr.Spec.PackageName,
				pr.Spec.WorkspaceName,
				pr.Spec.Revision,
				pr.Spec.RepositoryName,
				len(pr.Spec.Resources),
			}
		},
		columns: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Package", Type: "string"},
			{Name: "WorkspaceName", Type: "string"},
			{Name: "Revision", Type: "int"},
			{Name: "Repository", Type: "string"},
			{Name: "Files", Type: "integer"},
		},
	}
)

func isLatest(pr *porchapi.PackageRevision) bool {
	val, ok := pr.Labels[porchapi.LatestPackageRevisionKey]
	return ok && val == porchapi.LatestPackageRevisionValue
}
