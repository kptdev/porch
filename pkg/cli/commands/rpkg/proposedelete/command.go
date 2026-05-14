// Copyright 2022,2026 The kpt Authors
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

package proposedelete

import (
	"context"
	"fmt"

	porchapi "github.com/kptdev/porch/api/porch/v1alpha1"
	cliutils "github.com/kptdev/porch/internal/cliutils"
	"github.com/kptdev/porch/pkg/cli/commands/rpkg/docs"
	rpkgutil "github.com/kptdev/porch/pkg/cli/commands/rpkg/util"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	command = "cmdrpkgpropose-delete"
)

func newRunner(ctx context.Context, rcg *genericclioptions.ConfigFlags) *runner {
	r := &runner{
		Runner: rpkgutil.Runner{Ctx: ctx, Cfg: rcg},
	}
	c := &cobra.Command{
		Use:        "propose-delete PACKAGE",
		Aliases:    []string{"propose-del"},
		Short:      docs.ProposeDeleteShort,
		Long:       docs.ProposeDeleteShort + "\n" + docs.ProposeDeleteLong,
		Example:    docs.ProposeDeleteExamples,
		SuggestFor: []string{},
		PreRunE:    rpkgutil.MakePreRunE(command+".preRunE", rcg, &r.Client),
		RunE:       r.runE,
		Hidden:     cliutils.HidePorchCommands,
	}
	r.Command = c

	// Create flags

	return r
}

func NewCommand(ctx context.Context, rcg *genericclioptions.ConfigFlags) *cobra.Command {
	v1 := newRunner(ctx, rcg)
	v2 := newV1Alpha2Runner(ctx, rcg)
	cliutils.WrapVersionDispatch(v1.Command, v2.preRunE, v2.runE)
	return v1.Command
}

type runner struct {
	rpkgutil.Runner
}

func (r *runner) proposeDeleteAction(ctx context.Context, client client.Client, pr *porchapi.PackageRevision) (string, error) {
	switch pr.Spec.Lifecycle {
	case porchapi.PackageRevisionLifecyclePublished:
		pr.Spec.Lifecycle = porchapi.PackageRevisionLifecycleDeletionProposed
		if err := client.Update(ctx, pr); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s proposed for deletion", pr.Name), nil
	case porchapi.PackageRevisionLifecycleDeletionProposed:
		fmt.Fprintf(r.Command.OutOrStderr(), "%s is already proposed for deletion\n", pr.Name)
		return "", nil
	default:
		return "", fmt.Errorf("can only propose published packages for deletion; package %s is not published", pr.Name)
	}
}

func (r *runner) runE(_ *cobra.Command, args []string) error {
	return rpkgutil.RunForEachPackage(r.Ctx, r.Client, r.Command, *r.Cfg.Namespace, args,
		rpkgutil.RunForEachOpts{CmdName: command, WithRetry: true, CheckReadiness: false},
		r.proposeDeleteAction)
}
