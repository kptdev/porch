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
	"context"
	"fmt"
	"slices"

	"github.com/kptdev/kpt/pkg/lib/errors"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	pkgutil "github.com/kptdev/porch/pkg/util"
	pkgerrors "github.com/pkg/errors"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type v1alpha2Runner struct {
	ctx    context.Context
	cfg    *genericclioptions.ConfigFlags
	client client.Client

	revision  int
	workspace string
	strategy  string
	discover  string

	prs []porchv1alpha2.PackageRevision
}

func newV1Alpha2Runner(ctx context.Context, rcg *genericclioptions.ConfigFlags) *v1alpha2Runner {
	return &v1alpha2Runner{ctx: ctx, cfg: rcg}
}

func (r *v1alpha2Runner) preRunE(cmd *cobra.Command, args []string) error {
	const op errors.Op = command + ".preRunE"
	if r.client == nil {
		c, err := cliutils.CreateV1Alpha2ClientWithFlags(r.cfg)
		if err != nil {
			return errors.E(op, err)
		}
		r.client = c
	}

	// Read shared flags from the command (flags are bound to the v1alpha1 runner)
	r.revision, _ = cmd.Flags().GetInt("revision")
	r.workspace, _ = cmd.Flags().GetString("workspace")
	r.strategy, _ = cmd.Flags().GetString("strategy")
	r.discover, _ = cmd.Flags().GetString("discover")

	switch r.discover {
	case "":
		if err := r.validateUpgradeArgs(args); err != nil {
			return errors.E(op, err)
		}
	case upstream, downstream:
		// do nothing
	default:
		return errors.E(op, fmt.Errorf("argument for 'discover' must be one of 'upstream' or 'downstream'"))
	}

	var list porchv1alpha2.PackageRevisionList
	listOpts := []client.ListOption{}
	if r.cfg.Namespace != nil && *r.cfg.Namespace != "" {
		listOpts = append(listOpts, client.InNamespace(*r.cfg.Namespace))
	}
	if err := r.client.List(r.ctx, &list, listOpts...); err != nil {
		return errors.E(op, err)
	}
	r.prs = list.Items

	return nil
}

func (r *v1alpha2Runner) validateUpgradeArgs(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("SOURCE_PACKAGE_REVISION is a required positional argument")
	}
	if len(args) > 1 {
		return fmt.Errorf("too many arguments; SOURCE_PACKAGE_REVISION is the only accepted positional arguments")
	}
	if r.revision < 0 {
		return fmt.Errorf("revision must be positive (and not main)")
	}
	if r.workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	if r.strategy != "" {
		validStrategies := []string{
			string(porchv1alpha2.ResourceMerge),
			string(porchv1alpha2.FastForward),
			string(porchv1alpha2.ForceDeleteReplace),
			string(porchv1alpha2.CopyMerge),
		}
		if !slices.Contains(validStrategies, r.strategy) {
			return fmt.Errorf("invalid strategy %q; must be one of: %v", r.strategy, validStrategies)
		}
	}
	return nil
}

func (r *v1alpha2Runner) runE(cmd *cobra.Command, args []string) error {
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
	var newPr *porchv1alpha2.PackageRevision
	var lastErr error
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.client.Get(r.ctx, key, pr); err != nil {
			lastErr = err
			return err
		}
		var err error
		newPr, err = r.doUpgrade(pr)
		if err == nil {
			lastErr = nil
		} else {
			lastErr = err
		}
		return err
	})
	if err == nil && lastErr != nil {
		err = lastErr
	}
	if err != nil {
		return errors.E(op, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s upgraded to %s\n", pr.Name, newPr.Name)
	return nil
}

func (r *v1alpha2Runner) doUpgrade(pr *porchv1alpha2.PackageRevision) (*porchv1alpha2.PackageRevision, error) {
	if !pr.IsPublished() {
		return nil, pkgerrors.Errorf("to upgrade a package, it must be in a published state, not %q", pr.Spec.Lifecycle)
	}

	oldUpstreamPr, err := r.resolveOldUpstream(pr)
	if err != nil {
		return nil, err
	}

	newUpstreamPr, err := r.resolveNewUpstream(oldUpstreamPr.Spec.PackageName, oldUpstreamPr.Spec.RepositoryName)
	if err != nil {
		return nil, err
	}

	if !newUpstreamPr.IsPublished() {
		return nil, pkgerrors.Errorf("new upstream package revision %s is not published", newUpstreamPr.Name)
	}

	newPr := buildUpgradePackageRevision(pr, oldUpstreamPr, newUpstreamPr, r.workspace, r.strategy)
	if err := r.client.Create(r.ctx, newPr); err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to create package revision %q", newPr.Name)
	}
	return newPr, nil
}

func (r *v1alpha2Runner) resolveOldUpstream(pr *porchv1alpha2.PackageRevision) (*porchv1alpha2.PackageRevision, error) {
	oldUpstreamName := r.findUpstreamName(pr)
	if oldUpstreamName == "" {
		return nil, pkgerrors.Errorf("upstream source not found for package revision %q:"+
			" no clone or upgrade source was found in the source spec of the package", pr.Spec.PackageName)
	}

	oldUpstreamPr := r.findPackageRevision(oldUpstreamName)
	if oldUpstreamPr == nil {
		return nil, pkgerrors.Errorf("upstream package revision %s no longer exists", oldUpstreamName)
	}
	if !oldUpstreamPr.IsPublished() {
		return nil, pkgerrors.Errorf("old upstream package revision %s is not published", oldUpstreamPr.Name)
	}
	return oldUpstreamPr, nil
}

func (r *v1alpha2Runner) resolveNewUpstream(pkgName, repoName string) (*porchv1alpha2.PackageRevision, error) {
	if r.revision == 0 {
		pr := r.findLatestPackageRevisionForRef(pkgName, repoName)
		if pr == nil {
			return nil, pkgerrors.Errorf("failed to find latest published revision for package %s in repo %s", pkgName, repoName)
		}
		return pr, nil
	}
	pr := r.findPackageRevisionForRef(pkgName, repoName, r.revision)
	if pr == nil {
		return nil, pkgerrors.Errorf("revision %d does not exist for package %s in repo %s", r.revision, pkgName, repoName)
	}
	return pr, nil
}

func buildUpgradePackageRevision(pr, oldUpstream, newUpstream *porchv1alpha2.PackageRevision, workspace, strategy string) *porchv1alpha2.PackageRevision {
	return &porchv1alpha2.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: pr.Namespace,
			Name:      pkgutil.ComposePkgRevObjName(pr.Spec.RepositoryName, "", pr.Spec.PackageName, workspace),
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    pr.Spec.PackageName,
			RepositoryName: pr.Spec.RepositoryName,
			WorkspaceName:  workspace,
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			Source: &porchv1alpha2.PackageSource{
				Upgrade: &porchv1alpha2.PackageUpgradeSpec{
					OldUpstream:    porchv1alpha2.PackageRevisionRef{Name: oldUpstream.Name},
					NewUpstream:    porchv1alpha2.PackageRevisionRef{Name: newUpstream.Name},
					CurrentPackage: porchv1alpha2.PackageRevisionRef{Name: pr.Name},
					Strategy:       porchv1alpha2.PackageMergeStrategy(strategy),
				},
			},
		},
	}
}

func (r *v1alpha2Runner) findPackageRevision(prName string) *porchv1alpha2.PackageRevision {
	for i := range r.prs {
		if r.prs[i].Name == prName {
			return &r.prs[i]
		}
	}
	return nil
}

func (r *v1alpha2Runner) findPackageRevisionForRef(name, repo string, revision int) *porchv1alpha2.PackageRevision {
	for i := range r.prs {
		pr := &r.prs[i]
		if pr.Spec.PackageName == name && pr.Spec.RepositoryName == repo && pr.IsPublished() && pr.Status.Revision == revision {
			return pr
		}
	}
	return nil
}

func (r *v1alpha2Runner) findLatestPackageRevisionForRef(name, repo string) *porchv1alpha2.PackageRevision {
	latest := 0
	var output *porchv1alpha2.PackageRevision
	for i := range r.prs {
		pr := &r.prs[i]
		if pr.Spec.PackageName == name && pr.Spec.RepositoryName == repo && pr.IsPublished() && pr.Status.Revision > latest {
			latest = pr.Status.Revision
			output = pr
		}
	}
	return output
}

// findUpstreamName walks spec.source to find the upstream PR name.
func (r *v1alpha2Runner) findUpstreamName(pr *porchv1alpha2.PackageRevision) string {
	if pr.Spec.Source == nil {
		return ""
	}
	switch {
	case pr.Spec.Source.CloneFrom != nil:
		if pr.Spec.Source.CloneFrom.UpstreamRef != nil {
			return pr.Spec.Source.CloneFrom.UpstreamRef.Name
		}
		// Git URL clone — no upstream PR name in spec. Match via selfLock.
		if up := r.findUpstreamBySelfLock(pr.Status.UpstreamLock); up != nil {
			return up.Name
		}
		return ""
	case pr.Spec.Source.CopyFrom != nil:
		if source := r.findPackageRevision(pr.Spec.Source.CopyFrom.Name); source != nil {
			return r.findUpstreamName(source)
		}
		return ""
	case pr.Spec.Source.Upgrade != nil:
		return pr.Spec.Source.Upgrade.NewUpstream.Name
	default:
		return ""
	}
}

// findUpstreamBySelfLock finds a published PR whose selfLock matches the given lock.
// Used when the downstream was cloned via git URL (no upstreamRef name in spec).
func (r *v1alpha2Runner) findUpstreamBySelfLock(lock *porchv1alpha2.Locator) *porchv1alpha2.PackageRevision {
	if lock == nil || lock.Git == nil {
		return nil
	}
	for i := range r.prs {
		pr := &r.prs[i]
		if !pr.IsPublished() {
			continue
		}
		if pr.Status.SelfLock == nil || pr.Status.SelfLock.Git == nil {
			continue
		}
		git := pr.Status.SelfLock.Git
		if git.Repo == lock.Git.Repo && git.Directory == lock.Git.Directory && git.Ref == lock.Git.Ref {
			return pr
		}
	}
	return nil
}
