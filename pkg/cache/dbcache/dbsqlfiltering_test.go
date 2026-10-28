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
	"strings"
	"testing"

	"github.com/kptdev/porch/api/porch/key"
	"github.com/kptdev/porch/pkg/repository"
)

// TestWorkspaceFilterIsExactMatch verifies that the --workspace filter produces
// an exact-match predicate against the composed package_revisions name, rather
// than the previous leading-wildcard LIKE. The LIKE form was both unable to use
// an index and incorrect for dotted workspaces (e.g. "v1" would also match
// "2.v1").
func TestWorkspaceFilterIsExactMatch(t *testing.T) {
	tests := []struct {
		name          string
		workspaceName string
		wantContains  string
	}{
		{
			name:          "simple workspace",
			workspaceName: "v1",
			wantContains:  "substring(package_revisions.k8s_name from length(package_revisions.package_k8s_name)+2)='v1'",
		},
		{
			name:          "dotted workspace",
			workspaceName: "v1.2.3",
			wantContains:  "substring(package_revisions.k8s_name from length(package_revisions.package_k8s_name)+2)='v1.2.3'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filter := repository.ListPackageRevisionFilter{
				Key: key.PackageRevisionKey{
					WorkspaceName: tc.workspaceName,
				},
			}

			where := prListFilter2WhereClause(filter)

			if !strings.Contains(where, tc.wantContains) {
				t.Errorf("workspace filter did not produce exact match.\n got: %q\nwant substring: %q", where, tc.wantContains)
			}

			// Guard against regression to the buggy leading-wildcard LIKE on the
			// package_revisions name column.
			if strings.Contains(where, "k8s_name LIKE '%.") {
				t.Errorf("workspace filter still uses leading-wildcard LIKE: %q", where)
			}
		})
	}
}

// TestWorkspaceFilterEmpty verifies that an empty workspace adds no clause.
func TestWorkspaceFilterEmpty(t *testing.T) {
	filter := repository.ListPackageRevisionFilter{
		Key: key.PackageRevisionKey{WorkspaceName: ""},
	}

	where := prListFilter2WhereClause(filter)

	if strings.Contains(where, "substring(package_revisions.k8s_name") {
		t.Errorf("empty workspace should not add a workspace clause, got: %q", where)
	}
}

// TestPackageNameFilterUsesSplitPart verifies the --name filter uses the
// index-friendly split_part equality (backed by idx_packages_k8s_name_pkg)
// rather than a leading-wildcard LIKE.
func TestPackageNameFilterUsesSplitPart(t *testing.T) {
	filter := repository.ListPackageRevisionFilter{
		Key: key.PackageRevisionKey{
			PkgKey: key.PackageKey{Package: "mypkg"},
		},
	}

	where := prListFilter2WhereClause(filter)

	want := "split_part(packages.k8s_name, '.', -1)='mypkg'"
	if !strings.Contains(where, want) {
		t.Errorf("package name filter did not use split_part.\n got: %q\nwant substring: %q", where, want)
	}
}

// TestKptfileLabelFilterUsesContainment verifies the spec.packageMetadata.labels
// filter (fieldSelector path) emits a GIN-indexable @> containment predicate
// rather than the unindexable scalar ->>'key'='value' comparison.
func TestKptfileLabelFilterUsesContainment(t *testing.T) {
	filter := repository.ListPackageRevisionFilter{
		KptfileLabels: map[string]string{"env": "prod"},
	}

	where := prListFilter2WhereClause(filter)

	want := `package_revisions.spec::jsonb->'packageMetadata'->'labels' @> '{"env":"prod"}'`
	if !strings.Contains(where, want) {
		t.Errorf("kptfile label filter did not use @> containment.\n got: %q\nwant substring: %q", where, want)
	}
	if strings.Contains(where, "->>'env'") {
		t.Errorf("kptfile label filter still uses scalar ->> comparison: %q", where)
	}
}

// TestJSONBContainsClauseEscaping verifies keys/values are JSON- and SQL-escaped.
func TestJSONBContainsClauseEscaping(t *testing.T) {
	got := jsonbContainsClause("col", "k", "it's a value")
	want := `(col @> '{"k":"it''s a value"}')`
	if got != want {
		t.Errorf("escaping wrong.\n got: %q\nwant: %q", got, want)
	}
}
