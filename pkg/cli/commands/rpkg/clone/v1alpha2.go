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

package clone

import (
	"context"
	"fmt"
	"strings"

	"github.com/kptdev/kpt/pkg/lib/errors"
	"github.com/kptdev/kpt/pkg/lib/util/parse"
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

	upstream   porchv1alpha2.UpstreamPackage
	repository string
	workspace  string
	target     string
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

	if len(args) < 2 {
		return errors.E(op, fmt.Errorf("SOURCE_PACKAGE and NAME are required positional arguments; %d provided", len(args)))
	}

	r.repository, _ = cmd.Flags().GetString("repository")
	r.workspace, _ = cmd.Flags().GetString("workspace")
	directory, _ := cmd.Flags().GetString("directory")
	ref, _ := cmd.Flags().GetString("ref")
	secretRef, _ := cmd.Flags().GetString("secret-ref")

	if r.repository == "" {
		return errors.E(op, fmt.Errorf("--repository is required to specify downstream repository"))
	}
	if r.workspace == "" {
		return errors.E(op, fmt.Errorf("--workspace is required to specify downstream workspace name"))
	}

	source := args[0]
	r.target = args[1]

	pkgExists, err := util.PackageAlreadyExistsV1Alpha2(r.ctx, r.client, r.repository, r.target, *r.cfg.Namespace)
	if err != nil {
		return err
	}
	if pkgExists {
		return fmt.Errorf("`clone` cannot create a new revision for package %q that already exists in repo %q; make subsequent revisions using `copy`",
			r.target, r.repository)
	}

	switch {
	case strings.HasPrefix(source, "oci://"):
		return errors.E(op, fmt.Errorf("OCI upstream is not supported in v1alpha2"))

	case strings.Contains(source, "/"):
		if parse.HasGitSuffix(source) {
			repo, dir, parsedRef, err := parse.URL(source)
			if err != nil {
				return err
			}
			if directory != "" && dir != "" && directory != dir {
				return errors.E(op, fmt.Errorf("directory %s specified by --directory contradicts directory %s specified by SOURCE_PACKAGE",
					directory, dir))
			}
			if ref != "" && parsedRef != "" && ref != parsedRef {
				return errors.E(op, fmt.Errorf("ref %s specified by --ref contradicts ref %s specified by SOURCE_PACKAGE",
					ref, parsedRef))
			}
			if directory == "" {
				directory = dir
			}
			if ref == "" {
				ref = parsedRef
			}
			source = repo + ".git"
		}
		if ref == "" {
			ref = "main"
		}
		if directory == "" {
			directory = "/"
		}
		r.upstream = porchv1alpha2.UpstreamPackage{
			Type: porchv1alpha2.RepositoryTypeGit,
			Git: &porchv1alpha2.GitPackage{
				Repo:      source,
				Ref:       ref,
				Directory: directory,
				SecretRef: porchv1alpha2.SecretRef{Name: secretRef},
			},
		}

	default:
		r.upstream = porchv1alpha2.UpstreamPackage{
			UpstreamRef: &porchv1alpha2.PackageRevisionRef{Name: source},
		}
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
			Name:      pkgutil.ComposePkgRevObjName(r.repository, "", r.target, r.workspace),
		},
		Spec: porchv1alpha2.PackageRevisionSpec{
			PackageName:    r.target,
			WorkspaceName:  r.workspace,
			RepositoryName: r.repository,
			Lifecycle:      porchv1alpha2.PackageRevisionLifecycleDraft,
			Source: &porchv1alpha2.PackageSource{
				CloneFrom: &r.upstream,
			},
		},
	}
	if err := r.client.Create(r.ctx, pr); err != nil {
		return errors.E(op, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s created\n", pr.Name)
	return nil
}
