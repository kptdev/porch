// Copyright 2025,2026 The kpt Authors
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
	"context"
	"fmt"

	"slices"

	"github.com/kptdev/kpt/pkg/lib/errors"
	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/docs"
	pkgerrors "github.com/pkg/errors"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	command = "cmdrpkgupgrade"

	upstream   = "upstream"
	downstream = "downstream"
)

func NewCommand(ctx context.Context, rcg *genericclioptions.ConfigFlags) *cobra.Command {
	v1 := newRunner(ctx, rcg)
	v2 := newV1Alpha2Runner(ctx, rcg)
	cliutils.WrapVersionDispatch(v1.Command, v2.preRunE, v2.runE)
	return v1.Command
}

func newRunner(ctx context.Context, rcg *genericclioptions.ConfigFlags) *runner {
	r := &runner{
		ctx: ctx,
		cfg: rcg,
	}
	r.Command = &cobra.Command{
		Use:     "upgrade SOURCE_PACKAGE_REVISION",
		PreRunE: r.preRunE,
		RunE:    r.runE,
		Short:   docs.UpgradeShort,
		Long:    docs.UpgradeShort + "\n" + docs.UpgradeLong,
		Example: docs.UpgradeExamples,
		Hidden:  cliutils.HidePorchCommands,
	}
	r.Command.Flags().IntVar(&r.revision, "revision", 0, "Revision of the upstream package to upgrade to.")
	r.Command.Flags().StringVar(&r.workspace, "workspace", "", "Workspace name of the upgrade package revision.")
	r.Command.Flags().StringVar(&r.strategy, "strategy", "resource-merge", "Strategy to use for the upgrade. Options: resource-merge (default), fast-forward, force-delete-replace, copy-merge.")
	r.Command.Flags().StringVar(&r.discover, "discover", "",
		`If set, search for available updates instead of performing an update.
Setting this to 'upstream' will discover upstream updates of downstream packages.
Setting this to 'downstream' will discover downstream package revisions of upstream packages that need to be updated.`)
	return r
}

type runner struct {
	ctx     context.Context
	cfg     *genericclioptions.ConfigFlags
	client  client.Client
	Command *cobra.Command

	revision  int // Target package revision
	workspace string
	strategy  string // Merge strategy to use, default is "resource-merge"

	discover string // If set, discover updates rather than do updates

	// there are multiple places where we need access to all package revisions, so
	// we store it in the runner
	prs []porchapi.PackageRevision
}

func (r *runner) preRunE(_ *cobra.Command, args []string) error {
	const op errors.Op = command + ".preRunE"
	c, err := cliutils.CreateClientWithFlags(r.cfg)
	if err != nil {
		return errors.E(op, err)
	}
	r.client = c

	switch r.discover {
	case "":
		if len(args) < 1 {
			return errors.E(op, fmt.Errorf("SOURCE_PACKAGE_REVISION is a required positional argument"))
		}
		if len(args) > 1 {
			return errors.E(op, fmt.Errorf("too many arguments; SOURCE_PACKAGE_REVISION is the only accepted positional arguments"))
		}
		if r.revision < 0 {
			return errors.E(op, fmt.Errorf("revision must be positive (and not main)"))
		}
		if r.workspace == "" {
			return errors.E(op, fmt.Errorf("workspace is required"))
		}
		if r.strategy != "" {
			validStrategies := []string{string(porchapi.ResourceMerge), string(porchapi.FastForward), string(porchapi.ForceDeleteReplace), string(porchapi.CopyMerge)}
			valid := slices.Contains(validStrategies, r.strategy)
			if !valid {
				return errors.E(op, fmt.Errorf("invalid strategy %q; must be one of: %v", r.strategy, validStrategies))
			}
		}
	case upstream, downstream:
		packageRevisionList := porchapi.PackageRevisionList{}
		listOpts := []client.ListOption{}
		if r.cfg.Namespace != nil && *r.cfg.Namespace != "" {
			listOpts = append(listOpts, client.InNamespace(*r.cfg.Namespace))
		}
		if err := r.client.List(r.ctx, &packageRevisionList, listOpts...); err != nil {
			return errors.E(op, err)
		}
		r.prs = packageRevisionList.Items
	default:
		return errors.E(op, fmt.Errorf("argument for 'discover' must be one of 'upstream' or 'downstream'"))
	}

	return nil
}

func (r *runner) runE(cmd *cobra.Command, args []string) error {
	const op errors.Op = command + ".runE"

	if r.discover != "" {
		if err := r.discoverUpdates(cmd, args); err != nil {
			return errors.E(op, err)
		}
		return nil
	}

	pr := r.findPackageRevision(args[0])
	if pr == nil {
		return errors.E(op, pkgerrors.Errorf("could not find package revision %s", args[0]))
	}
	key := client.ObjectKeyFromObject(pr)
	var newPr *porchapi.PackageRevision
	var lastErr error
	err := retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		if err = r.client.Get(r.ctx, key, pr); err != nil {
			lastErr = err
			return err
		}
		newPr, err = r.doUpgrade(pr)
		if err == nil {
			lastErr = nil
		} else {
			lastErr = err
		}
		return err
	})
	// Workaround for k8s retry library bug: OnError/RetryOnConflict sometimes returns nil even when errors occur
	if err == nil && lastErr != nil {
		err = lastErr
	}
	if err != nil {
		return errors.E(op, err)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s upgraded to %s\n", pr.Name, newPr.Name); err != nil {
		return errors.E(op, err)
	}

	return nil
}

func (r *runner) doUpgrade(pr *porchapi.PackageRevision) (*porchapi.PackageRevision, error) {
	if !pr.IsPublished() {
		return nil, pkgerrors.Errorf("to upgrade a package, it must be in a published state, not %q", pr.Spec.Lifecycle)
	}

	oldUpstreamName := r.findUpstreamName(pr)
	if oldUpstreamName == "" {
		return nil, pkgerrors.Errorf("upstream source not found for package revision %q:"+
			" no clone or upgrade type package revision was found in the history of the package", pr.Spec.PackageName)
	}

	oldUpstreamPr := r.findPackageRevision(oldUpstreamName)
	if oldUpstreamPr == nil {
		return nil, pkgerrors.Errorf("upstream package revision %s no longer exists", oldUpstreamName)
	}
	if !oldUpstreamPr.IsPublished() {
		return nil, pkgerrors.Errorf("old upstream package revision %s is not published", oldUpstreamPr.Name)
	}
	upstreamPackageName := oldUpstreamPr.Spec.PackageName
	upstreamRepoName := oldUpstreamPr.Spec.RepositoryName
	var newUpstreamPr *porchapi.PackageRevision
	if r.revision == 0 {
		newUpstreamPr = r.findLatestPackageRevisionForRef(upstreamPackageName, upstreamRepoName)
		if newUpstreamPr == nil {
			return nil, pkgerrors.Errorf("failed to find latest published revision for package %s in repo %s (--revision was %d)", upstreamPackageName, upstreamRepoName, r.revision)
		}
	} else {
		newUpstreamPr = r.findPackageRevisionForRef(upstreamPackageName, upstreamRepoName, r.revision)
		if newUpstreamPr == nil {
			return nil, pkgerrors.Errorf("revision %d does not exist for package %s in repo %s", r.revision, upstreamPackageName, upstreamRepoName)
		}
	}

	if !newUpstreamPr.IsPublished() {
		return nil, pkgerrors.Errorf("new upstream package revision %s is not published", newUpstreamPr.Name)
	}

	upgradeTask := &porchapi.Task{
		Type: porchapi.TaskTypeUpgrade,
		Upgrade: &porchapi.PackageUpgradeTaskSpec{
			OldUpstream: porchapi.PackageRevisionRef{
				Name: oldUpstreamPr.Name,
			},
			NewUpstream: porchapi.PackageRevisionRef{
				Name: newUpstreamPr.Name,
			},
			LocalPackageRevisionRef: porchapi.PackageRevisionRef{
				Name: pr.Name,
			},
			Strategy: porchapi.PackageMergeStrategy(r.strategy),
		},
	}
	newPr := makePackageRevision(pr, r.workspace, upgradeTask)

	err := r.client.Create(r.ctx, newPr)
	return newPr, pkgerrors.Wrapf(err, "failed to do create package revision %q", newPr.Name)
}

func makePackageRevision(oldLocal *porchapi.PackageRevision, workspace string, task *porchapi.Task) *porchapi.PackageRevision {
	return &porchapi.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			APIVersion: porchapi.SchemeGroupVersion.String(),
			Kind:       "PackageRevision",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: oldLocal.Namespace,
		},
		Spec: porchapi.PackageRevisionSpec{
			PackageName:    oldLocal.Spec.PackageName,
			RepositoryName: oldLocal.Spec.RepositoryName,
			WorkspaceName:  workspace,
			Tasks:          []porchapi.Task{*task},
		},
	}
}

func (r *runner) findPackageRevision(prName string) *porchapi.PackageRevision {
	// Use GET instead of searching through cached list
	if r.discover == "" {
		pr := &porchapi.PackageRevision{}
		ns := ""
		if r.cfg.Namespace != nil {
			ns = *r.cfg.Namespace
		}
		if err := r.client.Get(r.ctx, client.ObjectKey{Namespace: ns, Name: prName}, pr); err != nil {
			return nil
		}
		return pr
	}
	// Discover mode uses cached list
	for i := range r.prs {
		pr := r.prs[i]
		if pr.Name == prName {
			return &pr
		}
	}
	return nil
}

func (r *runner) findPackageRevisionForRef(name, repo string, revision int) *porchapi.PackageRevision {
	// Use List with server-side filtering by package name, repo, and revision
	if r.discover == "" {
		list := &porchapi.PackageRevisionList{}
		ns := ""
		if r.cfg.Namespace != nil {
			ns = *r.cfg.Namespace
		}

		// Build list options with server-side filtering
		listOpts := []client.ListOption{
			client.InNamespace(ns),
		}

		// Add field selectors for package name, repo, and revision
		fields := make(map[string]string)
		if name != "" {
			fields["spec.packageName"] = name
		}
		if repo != "" {
			fields["spec.repository"] = repo
		}
		if revision > 0 {
			fields["spec.revision"] = fmt.Sprintf("%d", revision)
		}
		if len(fields) > 0 {
			listOpts = append(listOpts, client.MatchingFields(fields))
		}

		if err := r.client.List(r.ctx, list, listOpts...); err != nil {
			return nil
		}

		for i := range list.Items {
			pr := &list.Items[i]
			if pr.Spec.PackageName == name && pr.Spec.RepositoryName == repo && pr.IsPublished() && pr.Spec.Revision == revision {
				return pr
			}
		}
		return nil
	}
	// Discover mode uses cached list
	for i := range r.prs {
		pr := r.prs[i]
		if pr.Spec.PackageName == name && pr.Spec.RepositoryName == repo && pr.IsPublished() && pr.Spec.Revision == revision {
			return &pr
		}
	}
	return nil
}

func (r *runner) findLatestPackageRevisionForRef(name, repo string) *porchapi.PackageRevision {
	// Discovery mode always uses cached list
	if r.discover != "" {
		latest := 0
		var output *porchapi.PackageRevision
		for _, pr := range r.prs {
			if pr.Spec.PackageName == name && pr.Spec.RepositoryName == repo && pr.IsPublished() && pr.Spec.Revision > latest {
				latest = pr.Spec.Revision
				output = &pr
			}
		}
		return output
	}

	// Non-discovery mode: list with server-side filtering
	list := &porchapi.PackageRevisionList{}
	ns := ""
	if r.cfg.Namespace != nil {
		ns = *r.cfg.Namespace
	}

	// Build list options with server-side filtering
	listOpts := []client.ListOption{
		client.InNamespace(ns),
	}

	// Add field selectors for package name and repo
	fields := make(map[string]string)
	if name != "" {
		fields["spec.packageName"] = name
	}
	if repo != "" {
		fields["spec.repository"] = repo
	}
	if len(fields) > 0 {
		listOpts = append(listOpts, client.MatchingFields(fields))
	}

	if err := r.client.List(r.ctx, list, listOpts...); err != nil {
		return nil
	}
	latest := 0
	var output *porchapi.PackageRevision
	for i := range list.Items {
		pr := &list.Items[i]
		if pr.Spec.PackageName == name && pr.Spec.RepositoryName == repo && pr.IsPublished() && pr.Spec.Revision > latest {
			latest = pr.Spec.Revision
			output = pr
		}
	}
	return output
}

func (r *runner) findUpstreamName(pr *porchapi.PackageRevision) string {
	switch pr.Spec.Tasks[0].Type {
	case porchapi.TaskTypeClone:
		return pr.Spec.Tasks[0].Clone.Upstream.UpstreamRef.Name
	case porchapi.TaskTypeEdit:
		if n := r.findEditOrigin(pr); n != "" {
			return n
		}
		if pr.Status.UpstreamLock != nil {
			if err := r.listPackageRevisions(); err != nil {
				return ""
			}
			if up := r.findUpstreamByLock(pr.Status.UpstreamLock); up != nil {
				return up.Name
			}
		}
		return ""
	case porchapi.TaskTypeUpgrade:
		return pr.Spec.Tasks[0].Upgrade.NewUpstream.Name
	default:
		return ""
	}
}

func (r *runner) findEditOrigin(currentPr *porchapi.PackageRevision) string {
	pr := currentPr
	for pr != nil && pr.Spec.Tasks[0].Type == porchapi.TaskTypeEdit {
		sourceName := pr.Spec.Tasks[0].Edit.Source.Name
		pr = r.findPackageRevision(sourceName)
	}
	if pr != nil {
		return r.findUpstreamName(pr)
	}
	return ""
}

func (r *runner) listPackageRevisions() error {
	packageRevisionList := porchapi.PackageRevisionList{}
	listOpts := []client.ListOption{}
	if r.cfg.Namespace != nil && *r.cfg.Namespace != "" {
		listOpts = append(listOpts, client.InNamespace(*r.cfg.Namespace))
	}
	if err := r.client.List(r.ctx, &packageRevisionList, listOpts...); err != nil {
		return err
	}
	r.prs = packageRevisionList.Items
	return nil
}

func (r *runner) findUpstreamByLock(lock *porchapi.Locator) *porchapi.PackageRevision {
	if lock == nil || lock.Git == nil {
		return nil
	}

	target := lock.Git
	var bestMatch *porchapi.PackageRevision

	for i := range r.prs {
		candidate := r.prs[i]

		if !r.matchesTarget(candidate, target) {
			continue
		}

		if target.Ref != "" && candidate.Status.UpstreamLock.Git.Ref == target.Ref {
			if bestMatch == nil || candidate.Spec.Revision > bestMatch.Spec.Revision {
				tmp := candidate
				bestMatch = &tmp
			}
		}
	}

	return bestMatch
}

func (r *runner) matchesTarget(candidate porchapi.PackageRevision, target *porchapi.GitLock) bool {
	if !candidate.IsPublished() {
		return false
	}
	upstream := candidate.Status.UpstreamLock
	if upstream == nil || upstream.Git == nil {
		return false
	}

	cGit := upstream.Git
	if cGit.Repo != target.Repo || cGit.Directory != target.Directory {
		return false
	}

	return true
}
