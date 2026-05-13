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

package upgrade

import (
	"fmt"
	"io"
	"strings"

	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	"github.com/kptdev/porch/pkg/repository"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/printers"
)

func (r *v1alpha2Runner) discoverUpdates(cmd *cobra.Command, args []string) error {
	var prs []porchv1alpha2.PackageRevision
	var errs []string
	if len(args) == 0 || r.discover == downstream {
		prs = r.prs
	} else {
		for _, arg := range args {
			pr := r.findPackageRevision(arg)
			if pr == nil {
				errs = append(errs, fmt.Sprintf("could not find package revision %s", arg))
				continue
			}
			prs = append(prs, *pr)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors:\n  %s", strings.Join(errs, "\n  "))
	}

	switch r.discover {
	case upstream:
		return r.findUpstreamUpdates(prs, cmd.OutOrStdout())
	case downstream:
		return r.findDownstreamUpdates(prs, args, cmd.OutOrStdout())
	default:
		return fmt.Errorf("invalid argument %q for --discover", r.discover)
	}
}

// availableUpdatesV2 finds published package revisions in the same repo/package
// that have a higher revision than the current upstream.
func (r *v1alpha2Runner) availableUpdatesV2(pr porchv1alpha2.PackageRevision) ([]porchv1alpha2.PackageRevision, string) {
	upstreamName := r.findUpstreamName(&pr)
	if upstreamName == "" {
		return nil, ""
	}

	upstreamPr := r.findPackageRevision(upstreamName)
	if upstreamPr == nil {
		return nil, ""
	}

	var updates []porchv1alpha2.PackageRevision
	for i := range r.prs {
		candidate := &r.prs[i]
		if !candidate.IsPublished() {
			continue
		}
		if candidate.Spec.RepositoryName != upstreamPr.Spec.RepositoryName {
			continue
		}
		if candidate.Spec.PackageName != upstreamPr.Spec.PackageName {
			continue
		}
		if candidate.Status.Revision > upstreamPr.Status.Revision {
			updates = append(updates, *candidate)
		}
	}
	return updates, upstreamPr.Spec.RepositoryName
}

func (r *v1alpha2Runner) findUpstreamUpdates(prs []porchv1alpha2.PackageRevision, w io.Writer) error {
	var rows [][]string
	for _, pr := range prs {
		updates, repoName := r.availableUpdatesV2(pr)
		if len(updates) == 0 {
			rows = append(rows, []string{pr.Name, repoName, "No update available"})
		} else {
			var revisions []string
			for _, u := range updates {
				revisions = append(revisions, "v"+repository.Revision2Str(u.Status.Revision))
			}
			rows = append(rows, []string{pr.Name, repoName, strings.Join(revisions, ", ")})
		}
	}
	return printUpstreamUpdates(rows, w)
}

func (r *v1alpha2Runner) findDownstreamUpdates(prs []porchv1alpha2.PackageRevision, args []string, w io.Writer) error {
	rows := r.buildDownstreamRows(prs)
	filtered := filterRowsByArgs(rows, args)
	return printDownstreamRowsV2(filtered, w)
}

func (r *v1alpha2Runner) buildDownstreamRows(prs []porchv1alpha2.PackageRevision) [][]string {
	// map key: "upstreamName:newRevision"
	downstreamMap := make(map[string][]porchv1alpha2.PackageRevision)
	for _, pr := range prs {
		updates, _ := r.availableUpdatesV2(pr)
		for _, u := range updates {
			key := fmt.Sprintf("%s:%d", u.Name, u.Status.Revision)
			downstreamMap[key] = append(downstreamMap[key], pr)
		}
	}

	var rows [][]string
	for upstreamKey, downstreamPrs := range downstreamMap {
		parts := strings.SplitN(upstreamKey, ":", 2)
		upstreamName := parts[0]
		newRev := parts[1]
		for _, dsPr := range downstreamPrs {
			oldRev := r.oldUpstreamRevStr(&dsPr)
			rows = append(rows, []string{upstreamName, dsPr.Name, fmt.Sprintf("%s->v%s", oldRev, newRev)})
		}
	}
	return rows
}

func (r *v1alpha2Runner) oldUpstreamRevStr(pr *porchv1alpha2.PackageRevision) string {
	oldUpstreamName := r.findUpstreamName(pr)
	if up := r.findPackageRevision(oldUpstreamName); up != nil {
		return "v" + repository.Revision2Str(up.Status.Revision)
	}
	return "v0"
}

func filterRowsByArgs(rows [][]string, args []string) [][]string {
	if len(args) == 0 {
		return rows
	}
	var filtered [][]string
	for _, arg := range args {
		for _, row := range rows {
			if arg == row[0] {
				filtered = append(filtered, row)
			}
		}
	}
	return filtered
}

func printDownstreamRowsV2(rows [][]string, w io.Writer) error {
	printer := printers.GetNewTabWriter(w)
	if len(rows) == 0 {
		if _, err := fmt.Fprintln(printer, "All downstream packages are up to date."); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(printer, "PACKAGE REVISION\tDOWNSTREAM PACKAGE\tDOWNSTREAM UPDATE"); err != nil {
			return err
		}
		for _, row := range rows {
			if _, err := fmt.Fprintln(printer, strings.Join(row, "\t")); err != nil {
				return err
			}
		}
	}
	return printer.Flush()
}
