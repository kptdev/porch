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

package dbcache

import (
	"fmt"
	"slices"
	"strings"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	"github.com/kptdev/porch/pkg/repository"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

func pkgListFilter2WhereClause(filter repository.ListPackageFilter) string {
	whereStatement := ""

	repoKey := filter.Key.RKey()
	whereStatement, first := filter2SubClauseStr(whereStatement, repoKey.Namespace, "repositories.k8s_name_space", true)
	whereStatement, first = filter2SubClauseStr(whereStatement, repoKey.Name, "repositories.k8s_name", first)
	whereStatement, first = filter2SubClauseStr(whereStatement, repoKey.Path, "repositories.directory", first)
	whereStatement, first = filter2SubClauseStr(whereStatement, repoKey.PlaceholderWSname, "repositories.default_ws_name", first)

	pkgKey := filter.Key
	whereStatement, first = filter2SubClauseSuffix(whereStatement, pkgKey.Package, "packages.k8s_name", first)
	whereStatement, _ = filter2SubClauseStr(whereStatement, pkgKey.Path, "packages.package_path", first)

	if whereStatement == "" {
		return whereStatement
	} else {
		return "WHERE\n" + whereStatement
	}
}

func prListFilter2WhereClause(filter repository.ListPackageRevisionFilter) string {
	whereStatement := ""

	repoKey := filter.Key.RKey()
	whereStatement, first := filter2SubClauseStr(whereStatement, repoKey.Namespace, "repositories.k8s_name_space", true)
	whereStatement, first = filter2SubClauseStr(whereStatement, repoKey.Name, "repositories.k8s_name", first)
	whereStatement, first = filter2SubClauseStr(whereStatement, repoKey.Path, "repositories.directory", first)
	whereStatement, first = filter2SubClauseStr(whereStatement, repoKey.PlaceholderWSname, "repositories.default_ws_name", first)

	pkgKey := filter.Key.PKey()
	whereStatement, first = filter2SubClauseSuffix(whereStatement, pkgKey.Package, "packages.k8s_name", first)
	whereStatement, first = filter2SubClauseStr(whereStatement, pkgKey.Path, "packages.package_path", first)

	prKey := filter.Key
	whereStatement, first = filter2SubClauseInt(whereStatement, prKey.Revision, "package_revisions.revision", first)
	whereStatement, first = filter2SubClauseWorkspace(whereStatement, prKey.WorkspaceName, "package_revisions.k8s_name", first)

	whereStatement, first = filter2SubClauseLifecycle(whereStatement, filter.Lifecycles, "package_revisions.lifecycle", first)
	whereStatement, first = filter2SubClausePrLabels(whereStatement, filter.Label, first)
	whereStatement, _ = filter2SubClauseKptfileLabels(whereStatement, filter.KptfileLabels, first)

	if whereStatement == "" {
		return whereStatement
	} else {
		return "WHERE\n" + whereStatement
	}
}

func filter2SubClauseStr(whereStatement, filterField, column string, first bool) (string, bool) {
	if filterField == "" {
		return whereStatement, first
	}

	subClause := fmt.Sprintf("%s='%s'\n", column, filterField)

	if first {
		return whereStatement + subClause, false
	} else {
		return whereStatement + "AND " + subClause, false
	}
}

func filter2SubClauseSuffix(whereStatement, suffix, column string, first bool) (string, bool) {
	if suffix == "" {
		return whereStatement, first
	}

	subClause := fmt.Sprintf("%s LIKE '%%.%s'\n", column, suffix)

	if first {
		return whereStatement + subClause, false
	} else {
		return whereStatement + "AND " + subClause, false
	}
}

func filter2SubClauseInt(whereStatement string, filterField int, column string, first bool) (string, bool) {
	if filterField == 0 {
		return whereStatement, first
	}

	subClause := fmt.Sprintf("%s=%d\n", column, filterField)

	if first {
		return whereStatement + subClause, false
	} else {
		return whereStatement + "AND " + subClause, false
	}
}

func filter2SubClauseWorkspace(whereStatement string, filterField string, column string, first bool) (string, bool) {
	if filterField == "" {
		return whereStatement, first
	}

	subClause := fmt.Sprintf("%s LIKE '%%.%s'\n", column, filterField)

	if first {
		return whereStatement + subClause, false
	} else {
		return whereStatement + "AND " + subClause, false
	}
}

func filter2SubClauseLifecycle(whereStatement string, filterField []porchapi.PackageRevisionLifecycle, column string, first bool) (string, bool) {
	if len(filterField) == 0 {
		return whereStatement, first
	}

	subClause := "("
	for i, lifecycle := range filterField {
		if i == 0 {
			subClause = subClause + fmt.Sprintf("%s='%s'", column, lifecycle)
		} else {
			subClause = subClause + fmt.Sprintf(" OR %s='%s'", column, lifecycle)
		}
	}
	subClause += ")"

	if first {
		return whereStatement + subClause, false
	} else {
		return whereStatement + "AND " + subClause, false
	}
}

func filter2SubClauseKptfileLabels(whereStatement string, filterLabels map[string]string, first bool) (string, bool) {
	if len(filterLabels) == 0 {
		return whereStatement, first
	}

	var subClauses []string
	for labelKey, labelValue := range filterLabels {
		subClause := fmt.Sprintf("(package_revisions.spec::jsonb->'packageMetadata'->'labels'->>'%s' = '%s')",
			labelKey, labelValue)
		subClauses = append(subClauses, subClause)
	}

	combinedClause := "(" + strings.Join(subClauses, " AND ") + ")"

	if first {
		return whereStatement + combinedClause, false
	} else {
		return whereStatement + "AND " + combinedClause, false
	}
}

func filter2SubClausePrLabels(whereStatement string, labelSelector labels.Selector, first bool) (string, bool) {

	if labelSelector == nil {
		return whereStatement, first
	}

	subClauses := labelSelectorToJSONBClauses(labelSelector)
	if len(subClauses) == 0 {
		return whereStatement, first
	}

	combinedClause := "(" + strings.Join(subClauses, " AND ") + ")"

	if first {
		return whereStatement + combinedClause, false
	} else {
		return whereStatement + "AND " + combinedClause, false
	}
}

func labelSelectorToJSONBClauses(labelSelector labels.Selector) []string {
	jsonbPath := "package_revisions.meta::jsonb->'labels'"
	reqs, hasSelector := labelSelector.Requirements()
	if !hasSelector {
		return nil
	}

	var clauses []string
	for _, req := range reqs {
		key := req.Key()
		values := req.Values().List()

		if key == porchapi.LatestPackageRevisionKey {
			clauses = append(clauses, latestRevisionClause(req.Operator(), values))
			continue
		}

		switch req.Operator() {
		case selection.Equals, selection.DoubleEquals:
			clauses = append(clauses, fmt.Sprintf("(%s->>'%s' = '%s')", jsonbPath, key, values[0]))
		case selection.NotEquals:
			clauses = append(clauses, fmt.Sprintf("((%s->>'%s') IS NULL OR %s->>'%s' != '%s')", jsonbPath, key, jsonbPath, key, values[0]))
		case selection.In:
			clauses = append(clauses, fmt.Sprintf("(%s->>'%s' IN (%s))", jsonbPath, key, quotedStringListForJsonB(values)))
		case selection.NotIn:
			clauses = append(clauses, fmt.Sprintf("((%s->>'%s') IS NULL OR %s->>'%s' NOT IN (%s))", jsonbPath, key, jsonbPath, key, quotedStringListForJsonB(values)))
		case selection.Exists:
			clauses = append(clauses, fmt.Sprintf("(%s ? '%s')", jsonbPath, key))
		case selection.DoesNotExist:
			clauses = append(clauses, fmt.Sprintf("(NOT (%s ? '%s'))", jsonbPath, key))
		}
	}
	return clauses
}

func latestRevisionClause(op selection.Operator, values []string) string {
	col := "package_revisions.latest"
	switch op {
	case selection.Equals, selection.DoubleEquals:
		if values[0] == porchapi.LatestPackageRevisionValue {
			return fmt.Sprintf("(%s = TRUE)", col)
		}
		return fmt.Sprintf("(%s = FALSE)", col)
	case selection.NotEquals:
		if values[0] == porchapi.LatestPackageRevisionValue {
			return fmt.Sprintf("(%s = FALSE)", col)
		}
		return fmt.Sprintf("(%s = TRUE)", col)
	case selection.In:
		if slices.Contains(values, porchapi.LatestPackageRevisionValue) {
			return fmt.Sprintf("(%s = TRUE)", col)
		}
		return fmt.Sprintf("(%s = FALSE)", col)
	case selection.NotIn:
		if slices.Contains(values, porchapi.LatestPackageRevisionValue) {
			return fmt.Sprintf("(%s = FALSE)", col)
		}
		return fmt.Sprintf("(%s = TRUE)", col)
	case selection.Exists:
		return fmt.Sprintf("(%s = TRUE)", col)
	case selection.DoesNotExist:
		return fmt.Sprintf("(%s = FALSE)", col)
	default:
		return "TRUE"
	}
}

func quotedStringListForJsonB(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("'%s'", v)
	}
	return strings.Join(quoted, ", ")
}
