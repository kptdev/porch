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

package get

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kptdev/kpt/pkg/lib/errors"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/printers"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	// v1alpha2 CRD resource — same plural name, different API version.
	// The resource builder resolves the version via discovery.
	resourceNameV1Alpha2 = "packagerevisions.v1alpha2.porch.kpt.dev"
)

// runV1Alpha2 is the v1alpha2 variant of the get command's runE.
// The only differences from v1alpha1:
//   - resource name targets v1alpha2
//   - field selector uses status.revision instead of spec.revision
func (r *runner) runV1Alpha2(cmd *cobra.Command, args []string) error {
	const op errors.Op = command + ".runE"

	if r.showKptfile {
		if len(args) != 1 {
			return errors.E(op, fmt.Errorf("--show-kptfile requires exactly one package revision name as an argument"))
		}
		return r.showKptfileContent(cmd, args[0])
	}

	var objs []runtime.Object
	b, err := r.getFlags.ResourceBuilder()
	if err != nil {
		return err
	}

	if r.requestTable {
		scheme := runtime.NewScheme()
		if err := metav1.AddMetaToScheme(scheme); err != nil {
			return fmt.Errorf("error building runtime.Scheme: %w", err)
		}
		b = b.WithScheme(scheme, schema.GroupVersion{Version: "v1"})
	} else {
		b = b.Unstructured()
	}

	useSelectors := true
	if len(args) > 0 {
		b = b.ResourceNames(resourceNameV1Alpha2, args...)
		useSelectors = false
	} else {
		b = b.ResourceTypes(resourceNameV1Alpha2)
	}

	if useSelectors {
		fieldSelector := fields.Everything()
		if r.revision != -2 {
			// v1alpha2: revision is in status, not spec
			fieldSelector = fields.OneTermEqualSelector("status.revision", strconv.FormatInt(r.revision, 10))
		}
		if r.workspace != "" {
			fieldSelector = fields.OneTermEqualSelector("spec.workspaceName", r.workspace)
		}
		if r.packageName != "" {
			fieldSelector = fields.OneTermEqualSelector("spec.packageName", r.packageName)
		}
		if s := fieldSelector.String(); s != "" {
			b = b.FieldSelectorParam(s)
		} else {
			b = b.SelectAllParam(true)
		}
	}

	b = b.ContinueOnError().
		Latest().
		Flatten()

	if r.requestTable {
		b = b.TransformRequests(func(req *rest.Request) {
			req.SetHeader("Accept", strings.Join([]string{
				"application/json;as=Table;g=meta.k8s.io;v=v1",
				"application/json",
			}, ","))
		})
	}

	res := b.Do()
	if err := res.Err(); err != nil {
		return errors.E(op, err)
	}

	infos, err := res.Infos()
	if err != nil {
		return errors.E(op, err)
	}

	for _, i := range infos {
		if table, ok := i.Object.(*metav1.Table); ok {
			for i := range table.Rows {
				row := &table.Rows[i]
				if row.Object.Object == nil && row.Object.Raw != nil {
					u := &unstructured.Unstructured{}
					if err := u.UnmarshalJSON(row.Object.Raw); err != nil {
						klog.Warningf("error parsing raw object: %v", err)
					}
					row.Object.Object = u
				}
			}
		}
	}

	for _, i := range infos {
		switch obj := i.Object.(type) {
		case *unstructured.Unstructured:
			match, err := r.packageRevisionMatchesV1Alpha2(obj)
			if err != nil {
				return errors.E(op, err)
			}
			if match {
				objs = append(objs, obj)
			}
		case *metav1.Table:
			if err := r.filterTableRowsV1Alpha2(obj); err != nil {
				return err
			}
			objs = append(objs, obj)
		default:
			return errors.E(op, fmt.Sprintf("Unrecognized response %T", obj))
		}
	}

	printer, err := r.printFlags.ToPrinter()
	if err != nil {
		return errors.E(op, err)
	}

	w := printers.GetNewTabWriter(cmd.OutOrStdout())
	for _, obj := range objs {
		if err := printer.PrintObj(obj, w); err != nil {
			return errors.E(op, err)
		}
	}
	return w.Flush()
}

// packageRevisionMatchesV1Alpha2 filters unstructured objects using v1alpha2 field paths.
// In v1alpha2, revision is in status.revision instead of spec.revision.
func (r *runner) packageRevisionMatchesV1Alpha2(o *unstructured.Unstructured) (bool, error) {
	packageName, _, err := unstructured.NestedString(o.Object, "spec", "packageName")
	if err != nil {
		return false, err
	}
	// v1alpha2: revision is in status
	revision, _, err := unstructured.NestedInt64(o.Object, "status", "revision")
	if err != nil {
		return false, err
	}
	workspace, _, err := unstructured.NestedString(o.Object, "spec", "workspaceName")
	if err != nil {
		return false, err
	}
	if r.packageName != "" && r.packageName != packageName {
		return false, nil
	}
	if r.revision != -2 && r.revision != revision {
		return false, nil
	}
	if r.workspace != "" && r.workspace != workspace {
		return false, nil
	}
	return true, nil
}

// filterTableRowsV1Alpha2 filters table rows for v1alpha2.
// Column names come from kubebuilder printcolumn markers — same names as v1alpha1.
func (r *runner) filterTableRowsV1Alpha2(table *metav1.Table) error {
	return r.filterTableRows(table)
}
