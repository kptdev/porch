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

package deps

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/kptdev/kpt/pkg/lib/errors"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	rpkgutil "github.com/kptdev/porch/pkg/cli/commands/rpkg/util"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type v1alpha2Runner struct {
	ctx    context.Context
	cfg    *genericclioptions.ConfigFlags
	client client.Client
	cmd    *cobra.Command

	dependents bool
	canDelete  bool
}

func newV1Alpha2Runner(ctx context.Context, rcg *genericclioptions.ConfigFlags) *v1alpha2Runner {
	return &v1alpha2Runner{ctx: ctx, cfg: rcg}
}

func (r *v1alpha2Runner) preRunE(_ *cobra.Command, _ []string) error {
	const op errors.Op = command + ".preRunE"
	if r.client == nil {
		c, err := cliutils.CreateV1Alpha2ClientWithFlags(r.cfg)
		if err != nil {
			return errors.E(op, err)
		}
		r.client = c
	}
	return nil
}

func (r *v1alpha2Runner) runE(cmd *cobra.Command, args []string) error {
	const op errors.Op = command + ".runE"
	r.cmd = cmd
	namespace := rpkgutil.EnsureNamespace(r.cfg)
	name := args[0]

	var pr porchv1alpha2.PackageRevision
	if err := r.client.Get(r.ctx, client.ObjectKey{Namespace: namespace, Name: name}, &pr); err != nil {
		return errors.E(op, err)
	}

	// --can-delete implies the dependents query.
	if r.dependents || r.canDelete {
		return r.reportDependents(namespace, &pr)
	}
	return r.reportUpstreams(&pr)
}

// reportUpstreams prints the upstream packages the package depends on
// (its own root upstream plus any sub-package upstreams).
func (r *v1alpha2Runner) reportUpstreams(pr *porchv1alpha2.PackageRevision) error {
	out := r.cmd.OutOrStdout()

	if pr.Status.DependencyTruncated {
		fmt.Fprintf(r.cmd.ErrOrStderr(),
			"warning: %s has a truncated dependency list; results may be incomplete\n", pr.Name)
	}

	type row struct {
		path string // "" = root
		loc  *porchv1alpha2.Locator
	}
	var rows []row
	if pr.Status.UpstreamLock != nil {
		rows = append(rows, row{path: "", loc: pr.Status.UpstreamLock})
	}
	for i := range pr.Status.SubpackageUpstreams {
		rows = append(rows, row{
			path: pr.Status.SubpackageUpstreams[i].Path,
			loc:  pr.Status.SubpackageUpstreams[i].Upstream,
		})
	}

	if len(rows) == 0 {
		fmt.Fprintf(out, "%s has no upstream dependencies\n", pr.Name)
		return nil
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].path < rows[j].path })
	fmt.Fprintf(out, "%s depends on:\n", pr.Name)
	for _, rw := range rows {
		where := "root"
		if rw.path != "" {
			where = "subpackage " + rw.path
		}
		fmt.Fprintf(out, "  - %s: %s\n", where, locatorString(rw.loc))
	}
	return nil
}

// reportDependents lists downstream package revisions that reference this
// package as an upstream, and backs --can-delete.
//
// It matches the server-side deletion guard: a dependent references the target
// either by name (Spec.Source: CopyFrom/CloneFrom/Upgrade) or by git locator
// (a subpackage recorded in Status.UpstreamKeys). The reverse query is a
// namespace List + client-side scan, since status.upstreamKeys is a
// controller-cache index rather than a CRD-selectable field.
func (r *v1alpha2Runner) reportDependents(namespace string, pr *porchv1alpha2.PackageRevision) error {
	const op errors.Op = command + ".reportDependents"
	out := r.cmd.OutOrStdout()

	// selfKey may be empty if the package has no resolved self locator; in that
	// case only name-based references can match, which is still checked below.
	selfKey := porchv1alpha2.UpstreamKey(pr.Status.SelfLock)

	var list porchv1alpha2.PackageRevisionList
	if err := r.client.List(r.ctx, &list, client.InNamespace(namespace)); err != nil {
		return errors.E(op, err)
	}

	dependents, truncatedSeen := collectDependents(&list, pr.Name, selfKey)
	sort.Strings(dependents)

	if truncatedSeen {
		fmt.Fprintf(r.cmd.ErrOrStderr(),
			"warning: one or more packages have a truncated dependency list; results may be incomplete\n")
	}

	if r.canDelete {
		return r.reportCanDelete(op, out, pr.Name, dependents)
	}

	if len(dependents) == 0 {
		fmt.Fprintf(out, "%s has no dependents\n", pr.Name)
		return nil
	}
	fmt.Fprintf(out, "%s is depended on by %d package(s):\n", pr.Name, len(dependents))
	for _, d := range dependents {
		fmt.Fprintf(out, "  - %s\n", d)
	}
	return nil
}

// collectDependents scans list for package revisions (other than selfName) that
// reference the target — either by name (Spec.Source) or by git locator
// (selfKey, matched against Status.UpstreamKeys). selfKey may be empty, in which
// case only name-based references match. It also reports whether any scanned
// package had a truncated dependency list. This mirrors the server-side guard.
func collectDependents(list *porchv1alpha2.PackageRevisionList, selfName, selfKey string) (dependents []string, truncatedSeen bool) {
	for i := range list.Items {
		p := &list.Items[i]
		if p.Name == selfName {
			continue
		}
		// Name-based reference (top-level clone/copy/upgrade).
		if p.SourceReferencesName(selfName) {
			dependents = append(dependents, p.Namespace+"/"+p.Name)
			continue
		}
		// A truncated projection means the package's upstreams are not fully
		// known, so it must be treated as a possible dependent (fail closed),
		// matching the server-side delete guard.
		if p.Status.DependencyTruncated {
			truncatedSeen = true
			dependents = append(dependents, p.Namespace+"/"+p.Name)
			continue
		}
		// Locator-based reference (subpackage).
		if selfKey != "" && slices.Contains(p.Status.UpstreamKeys, selfKey) {
			dependents = append(dependents, p.Namespace+"/"+p.Name)
		}
	}
	return dependents, truncatedSeen
}

// reportCanDelete implements the --can-delete output: it errors if dependents
// exist, otherwise confirms the package can be deleted.
func (r *v1alpha2Runner) reportCanDelete(op errors.Op, out io.Writer, name string, dependents []string) error {
	if len(dependents) > 0 {
		return errors.E(op, fmt.Errorf("%s cannot be deleted: referenced by %d downstream package(s): %s",
			name, len(dependents), joinCapped(dependents, maxListed)))
	}
	fmt.Fprintf(out, "%s can be deleted (no dependents)\n", name)
	return nil
}

// maxListed caps how many dependent names are printed for --can-delete errors.
const maxListed = 50

func joinCapped(items []string, limit int) string {
	if len(items) <= limit {
		return joinComma(items)
	}
	return fmt.Sprintf("%s, ... (%d more)", joinComma(items[:limit]), len(items)-limit)
}

func joinComma(items []string) string {
	var s strings.Builder
	for i, it := range items {
		if i > 0 {
			s.WriteString(", ")
		}
		s.WriteString(it)
	}
	return s.String()
}

func locatorString(loc *porchv1alpha2.Locator) string {
	if loc == nil || loc.Git == nil {
		return "<unresolved>"
	}
	g := loc.Git
	s := fmt.Sprintf("%s directory=%s ref=%s", g.Repo, g.Directory, g.Ref)
	if g.Commit != "" {
		s += " commit=" + g.Commit
	}
	return s
}
