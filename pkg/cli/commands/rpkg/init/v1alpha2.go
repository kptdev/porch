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

package init

import (
	"context"
	"fmt"

	"github.com/kptdev/kpt/pkg/lib/errors"
	porchv1alpha2 "github.com/kptdev/porch/api/porch/v1alpha2"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/util"
	pkgutil "github.com/kptdev/porch/pkg/util"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type v1alpha2Runner struct {
	ctx    context.Context
	cfg    *genericclioptions.ConfigFlags
	client client.Client

	// Flags — shared with v1alpha1 runner via the same cobra.Command
	Keywords    []string
	Description string
	Site        string
	name        string
	repository  string
	workspace   string
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

	if len(args) < 1 {
		return errors.E(op, "PACKAGE_NAME is a required positional argument")
	}

	// Read shared flags from the command
	r.repository, _ = cmd.Flags().GetString("repository")
	r.workspace, _ = cmd.Flags().GetString("workspace")
	r.Description, _ = cmd.Flags().GetString("description")
	r.Site, _ = cmd.Flags().GetString("site")
	r.Keywords, _ = cmd.Flags().GetStringSlice("keywords")

	if r.repository == "" {
		return errors.E(op, fmt.Errorf("--repository is required to specify target repository"))
	}
	if r.workspace == "" {
		return errors.E(op, fmt.Errorf("--workspace is required to specify workspace name"))
	}

	r.name = args[0]
	pkgExists, err := util.PackageAlreadyExistsV1Alpha2(r.ctx, r.client, r.repository, r.name, *r.cfg.Namespace)
	if err != nil {
		return err
	}
	if pkgExists {
		return fmt.Errorf("`init` cannot create a new revision for package %q that already exists in repo %q; make subsequent revisions using `copy`",
			r.name, r.repository)
	}
	return nil
}

func (r *v1alpha2Runner) runE(cmd *cobra.Command, _ []string) error {
	const op errors.Op = command + ".runE"

	pr := &porchv1alpha2.PackageRevision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PackageRevision",
			APIVersion: porchv1alpha2.SchemeGroupVersion.Identifier(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: *r.cfg.Namespace,
			Name:      pkgutil.ComposePkgRevObjName(r.repository, "", r.name, r.workspace),
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    r.name,
			WorkspaceName:  r.workspace,
			RepositoryName: r.repository,
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			Source: &porchv1alpha2.PackageSource{
				Init: &porchv1alpha2.PackageInitSpec{
					Description: r.Description,
					Keywords:    r.Keywords,
					Site:        r.Site,
				},
			},
		},
	}
	if err := r.client.Create(r.ctx, pr); err != nil {
		return errors.E(op, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s created\n", pr.Name)
	return nil
}
